package broker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	brokerclientset "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/ovnbroker/v1alpha1/apis/clientset/versioned"
	brokerinformers "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/ovnbroker/v1alpha1/apis/informers/externalversions"
)

const (
	// defaultResyncPeriod is the default resync period for informers
	defaultResyncPeriod = 30 * time.Minute

	// maxRetries is the maximum number of times to retry processing an item
	maxRetries = 5
)

// Syncer synchronizes resources between local and broker clusters.
// It manages informers for both clusters and dispatches events to registered handlers.
type Syncer struct {
	agent    *Agent
	handlers []Handler

	// Local cluster clients
	localDynamicClient dynamic.Interface

	// Broker cluster clients
	brokerDynamicClient dynamic.Interface
	brokerClientset     brokerclientset.Interface

	// Informer factories
	localInformerFactory  dynamicinformer.DynamicSharedInformerFactory
	brokerInformerFactory brokerinformers.SharedInformerFactory

	// Resource-specific informers and queues
	localInformers  map[schema.GroupVersionResource]cache.SharedIndexInformer
	brokerInformers map[schema.GroupVersionResource]cache.SharedIndexInformer
	queues          map[string]workqueue.RateLimitingInterface

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// queueItem represents an item in the work queue.
type queueItem struct {
	handler  Handler
	gvr      schema.GroupVersionResource
	eventType string // "add", "update", "delete"
	oldObj   interface{}
	newObj   interface{}
	isLocal  bool // true if from local cluster, false if from broker
}

// NewSyncer creates a new syncer for the broker agent.
func NewSyncer(agent *Agent, handlers []Handler) (*Syncer, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create dynamic client for local cluster
	localDynamicClient, err := dynamic.NewForConfig(agent.config.LocalKubeConfig)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create local dynamic client: %w", err)
	}

	// Create dynamic client for broker cluster
	brokerDynamicClient, err := dynamic.NewForConfig(agent.config.BrokerKubeConfig)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create broker dynamic client: %w", err)
	}

	// Create broker typed clientset (already exists in agent.brokerClient)
	brokerClientset := agent.brokerClient.brokerClient

	s := &Syncer{
		agent:               agent,
		handlers:            handlers,
		localDynamicClient:  localDynamicClient,
		brokerDynamicClient: brokerDynamicClient,
		brokerClientset:     brokerClientset,
		localInformers:      make(map[schema.GroupVersionResource]cache.SharedIndexInformer),
		brokerInformers:     make(map[schema.GroupVersionResource]cache.SharedIndexInformer),
		queues:              make(map[string]workqueue.RateLimitingInterface),
		ctx:                 ctx,
		cancel:              cancel,
	}

	// Initialize informer factories
	s.localInformerFactory = dynamicinformer.NewDynamicSharedInformerFactory(localDynamicClient, defaultResyncPeriod)
	s.brokerInformerFactory = brokerinformers.NewSharedInformerFactory(brokerClientset, defaultResyncPeriod)

	// Setup informers for each handler
	for _, handler := range handlers {
		if err := s.setupHandlerInformers(handler); err != nil {
			cancel()
			return nil, fmt.Errorf("failed to setup informers for handler %s: %w", handler.Name(), err)
		}
	}

	return s, nil
}

// setupHandlerInformers creates informers for all resources watched by a handler.
func (s *Syncer) setupHandlerInformers(handler Handler) error {
	for _, resource := range handler.GetWatchedResources() {
		gvr := schema.GroupVersionResource{
			Group:    resource.GroupVersionKind.Group,
			Version:  resource.GroupVersionKind.Version,
			Resource: s.pluralizeKind(resource.GroupVersionKind.Kind),
		}

		if resource.Local {
			if err := s.setupLocalInformer(handler, gvr, resource.GroupVersionKind); err != nil {
				return err
			}
		}

		if resource.Broker {
			if err := s.setupBrokerInformer(handler, gvr, resource.GroupVersionKind); err != nil {
				return err
			}
		}
	}

	return nil
}

// setupLocalInformer creates an informer for a local cluster resource.
func (s *Syncer) setupLocalInformer(handler Handler, gvr schema.GroupVersionResource, gvk schema.GroupVersionKind) error {
	klog.V(4).Infof("Setting up local informer for %s (handler: %s)", gvr.String(), handler.Name())

	// Check if we already have an informer for this resource
	if _, exists := s.localInformers[gvr]; exists {
		klog.V(4).Infof("Local informer for %s already exists, reusing", gvr.String())
		return nil
	}

	// Create informer using dynamic client
	informer := s.localInformerFactory.ForResource(gvr).Informer()

	// Add event handlers
	queueName := fmt.Sprintf("local-%s-%s", handler.Name(), gvr.Resource)
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	s.queues[queueName] = queue

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			queue.Add(&queueItem{
				handler:   handler,
				gvr:       gvr,
				eventType: "add",
				newObj:    obj,
				isLocal:   true,
			})
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			queue.Add(&queueItem{
				handler:   handler,
				gvr:       gvr,
				eventType: "update",
				oldObj:    oldObj,
				newObj:    newObj,
				isLocal:   true,
			})
		},
		DeleteFunc: func(obj interface{}) {
			queue.Add(&queueItem{
				handler:   handler,
				gvr:       gvr,
				eventType: "delete",
				oldObj:    obj,
				isLocal:   true,
			})
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler: %w", err)
	}

	s.localInformers[gvr] = informer
	klog.V(2).Infof("Created local informer for %s", gvr.String())

	return nil
}

// setupBrokerInformer creates an informer for a broker cluster resource.
func (s *Syncer) setupBrokerInformer(handler Handler, gvr schema.GroupVersionResource, gvk schema.GroupVersionKind) error {
	klog.V(4).Infof("Setting up broker informer for %s (handler: %s)", gvr.String(), handler.Name())

	// Check if we already have an informer for this resource
	if _, exists := s.brokerInformers[gvr]; exists {
		klog.V(4).Infof("Broker informer for %s already exists, reusing", gvr.String())
		return nil
	}

	// For broker resources in our custom API group, use typed informers
	// For other resources, use dynamic informer
	var informer cache.SharedIndexInformer

	if gvk.Group == "broker.ovn.org" {
		// Our broker CRDs - use typed informers
		switch gvk.Kind {
		case "ServiceExport":
			informer = s.brokerInformerFactory.Broker().V1alpha1().ServiceExports().Informer()
		case "ServiceImport":
			informer = s.brokerInformerFactory.Broker().V1alpha1().ServiceImports().Informer()
		case "ClusterInfo":
			informer = s.brokerInformerFactory.Broker().V1alpha1().ClusterInfos().Informer()
		default:
			return fmt.Errorf("unsupported broker resource kind: %s", gvk.Kind)
		}
	} else {
		// Other CRDs - use dynamic informer
		klog.V(4).Infof("Using dynamic informer for broker resource %s", gvr.String())
		// Create informer using dynamic client factory
		// Note: We'll need a separate dynamic factory for broker cluster
		return fmt.Errorf("dynamic broker informers not yet implemented")
	}

	// Add event handlers
	queueName := fmt.Sprintf("broker-%s-%s", handler.Name(), gvr.Resource)
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	s.queues[queueName] = queue

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			queue.Add(&queueItem{
				handler:   handler,
				gvr:       gvr,
				eventType: "add",
				newObj:    obj,
				isLocal:   false,
			})
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			queue.Add(&queueItem{
				handler:   handler,
				gvr:       gvr,
				eventType: "update",
				oldObj:    oldObj,
				newObj:    newObj,
				isLocal:   false,
			})
		},
		DeleteFunc: func(obj interface{}) {
			queue.Add(&queueItem{
				handler:   handler,
				gvr:       gvr,
				eventType: "delete",
				oldObj:    obj,
				isLocal:   false,
			})
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler: %w", err)
	}

	s.brokerInformers[gvr] = informer
	klog.V(2).Infof("Created broker informer for %s", gvr.String())

	return nil
}

// Start starts all informers and worker goroutines.
func (s *Syncer) Start(ctx context.Context) {
	klog.Info("Starting broker syncer")

	// Start local informer factory
	s.localInformerFactory.Start(ctx.Done())

	// Start broker informer factory
	s.brokerInformerFactory.Start(ctx.Done())

	// Wait for caches to sync
	klog.V(2).Info("Waiting for informer caches to sync")

	// Wait for local informers
	for gvr, informer := range s.localInformers {
		if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
			klog.Errorf("Failed to sync local cache for %s", gvr.String())
			return
		}
		klog.V(4).Infof("Local cache synced for %s", gvr.String())
	}

	// Wait for broker informers
	for gvr, informer := range s.brokerInformers {
		if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
			klog.Errorf("Failed to sync broker cache for %s", gvr.String())
			return
		}
		klog.V(4).Infof("Broker cache synced for %s", gvr.String())
	}

	klog.Info("All informer caches synced")

	// Start workers for each queue
	for queueName, queue := range s.queues {
		s.wg.Add(1)
		go s.runWorker(ctx, queueName, queue)
	}

	klog.Infof("Started %d workers", len(s.queues))
}

// runWorker processes items from a queue.
func (s *Syncer) runWorker(ctx context.Context, queueName string, queue workqueue.RateLimitingInterface) {
	defer s.wg.Done()
	klog.V(4).Infof("Starting worker for queue: %s", queueName)

	for {
		select {
		case <-ctx.Done():
			klog.V(4).Infof("Stopping worker for queue: %s", queueName)
			return
		default:
		}

		item, shutdown := queue.Get()
		if shutdown {
			klog.V(4).Infof("Queue %s shutdown", queueName)
			return
		}

		if err := s.processQueueItem(item.(*queueItem)); err != nil {
			if queue.NumRequeues(item) < maxRetries {
				klog.Warningf("Error processing item (will retry): %v", err)
				queue.AddRateLimited(item)
			} else {
				klog.Errorf("Max retries reached for item, dropping: %v", err)
				queue.Forget(item)
			}
		} else {
			queue.Forget(item)
		}

		queue.Done(item)
	}
}

// processQueueItem dispatches an event to the appropriate handler method.
func (s *Syncer) processQueueItem(item *queueItem) error {
	klog.Infof("[SYNCER-DEBUG] Processing %s event for %s (local: %v, handler: %s)",
		item.eventType, item.gvr.String(), item.isLocal, item.handler.Name())

	var err error

	if item.isLocal {
		// Local cluster events
		switch item.eventType {
		case "add":
			klog.Infof("[SYNCER-DEBUG] About to call OnLocalAdd, obj type: %T", item.newObj)
			err = item.handler.OnLocalAdd(item.newObj)
			klog.Infof("[SYNCER-DEBUG] OnLocalAdd returned, err: %v", err)
		case "update":
			err = item.handler.OnLocalUpdate(item.oldObj, item.newObj)
		case "delete":
			err = item.handler.OnLocalDelete(item.oldObj)
		default:
			return fmt.Errorf("unknown event type: %s", item.eventType)
		}
	} else {
		// Broker cluster events
		switch item.eventType {
		case "add":
			err = item.handler.OnBrokerAdd(item.newObj)
		case "update":
			err = item.handler.OnBrokerUpdate(item.oldObj, item.newObj)
		case "delete":
			err = item.handler.OnBrokerDelete(item.oldObj)
		default:
			return fmt.Errorf("unknown event type: %s", item.eventType)
		}
	}

	if err != nil {
		return fmt.Errorf("handler %s failed to process %s event: %w",
			item.handler.Name(), item.eventType, err)
	}

	return nil
}

// Stop stops all informers and workers.
func (s *Syncer) Stop() {
	klog.Info("Stopping broker syncer")

	// Stop all queues
	for name, queue := range s.queues {
		klog.V(4).Infof("Shutting down queue: %s", name)
		queue.ShutDown()
	}

	s.cancel()
	s.wg.Wait()

	klog.Info("Broker syncer stopped")
}

// pluralizeKind converts a Kind to its plural resource name.
// This is a simple implementation - in production, you'd want a more robust solution.
func (s *Syncer) pluralizeKind(kind string) string {
	// Simple pluralization rules
	switch kind {
	case "ServiceExport":
		return "serviceexports"
	case "ServiceImport":
		return "serviceimports"
	case "ClusterInfo":
		return "clusterinfos"
	default:
		// Simple heuristic: add 's'
		return kind + "s"
	}
}
