package broker

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	brokerv1alpha1 "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/ovnbroker/v1alpha1"
	brokerclientset "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/ovnbroker/v1alpha1/apis/clientset/versioned"
)

// AgentConfig contains the configuration for the broker agent.
type AgentConfig struct {
	// ClusterID is the unique identifier for this cluster
	ClusterID string

	// LocalKubeConfig is the kubeconfig for the local cluster
	LocalKubeConfig *rest.Config

	// BrokerKubeConfig is the kubeconfig for accessing the broker cluster
	BrokerKubeConfig *rest.Config

	// LocalKubeClient is the kubernetes client for the local cluster
	LocalKubeClient kubernetes.Interface

	// BrokerNamespace is the namespace in the broker cluster (optional, defaults to ovn-kubernetes-broker)
	BrokerNamespace string

	// ClusterInfoProvider provides cluster-level information
	ClusterInfoProvider ClusterInfoProvider

	// ClusterSetIPCIDR is the CIDR range for ClusterSet-scoped IPs (optional)
	// If set, this cluster will act as the ClusterSetIP allocator
	ClusterSetIPCIDR string
}

// ClusterInfoProvider provides cluster-level information for publishing to the broker.
type ClusterInfoProvider interface {
	// GetServiceCIDR returns the service CIDR range(s) for this cluster
	GetServiceCIDR() []string

	// GetClusterCIDR returns the pod CIDR range(s) for the default network
	GetClusterCIDR() []string

	// GetNetworks returns the CUDN network information for this cluster
	GetNetworks() []brokerv1alpha1.NetworkInfo
}

// Agent is the generic broker agent that syncs data between local cluster and broker.
// It provides a framework for pluggable handlers (MCS, Network, etc.)
type Agent struct {
	config            *AgentConfig
	brokerClient      *Client
	localBrokerClient brokerclientset.Interface
	handlers          []Handler
	syncer            *Syncer

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	started bool
	mu      sync.Mutex
}

// NewAgent creates a new broker agent.
func NewAgent(config *AgentConfig) (*Agent, error) {
	if config.ClusterID == "" {
		return nil, fmt.Errorf("cluster ID is required")
	}

	if config.BrokerKubeConfig == nil {
		return nil, fmt.Errorf("broker kubeconfig is required")
	}

	if config.LocalKubeClient == nil {
		return nil, fmt.Errorf("local kube client is required")
	}

	brokerClient, err := NewClient(config.BrokerKubeConfig, config.ClusterID, config.BrokerNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create broker client: %w", err)
	}

	// Create local broker clientset for creating ServiceImports, etc. in local cluster
	localBrokerClient, err := brokerclientset.NewForConfig(config.LocalKubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create local broker clientset: %w", err)
	}

	// Create ClusterSetIP allocator if CIDR is provided
	// This cluster will act as the ClusterSetIP allocator for the broker
	if config.ClusterSetIPCIDR != "" {
		// Import mcs package to create allocator
		// We'll add this in a separate package to avoid circular dependency
		klog.Infof("Broker cluster will allocate ClusterSetIPs from CIDR: %s", config.ClusterSetIPCIDR)
		// The allocator will be created and set by the MCS handler
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Agent{
		config:            config,
		brokerClient:      brokerClient,
		localBrokerClient: localBrokerClient,
		handlers:          []Handler{},
		ctx:               ctx,
		cancel:            cancel,
	}, nil
}

// RegisterHandler registers a handler (MCS, Network, etc.)
func (a *Agent) RegisterHandler(handler Handler) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.started {
		klog.Warningf("Cannot register handler %s after agent has started", handler.Name())
		return
	}

	klog.V(2).Infof("Registering broker handler: %s", handler.Name())
	a.handlers = append(a.handlers, handler)
}

// BrokerClient returns the broker client for use by handlers.
func (a *Agent) BrokerClient() *Client {
	return a.brokerClient
}

// LocalKubeClient returns the local kubernetes client.
func (a *Agent) LocalKubeClient() kubernetes.Interface {
	return a.config.LocalKubeClient
}

// LocalBrokerClient returns the local broker CRD clientset.
func (a *Agent) LocalBrokerClient() brokerclientset.Interface {
	return a.localBrokerClient
}

// ClusterID returns the cluster identifier.
func (a *Agent) ClusterID() string {
	return a.config.ClusterID
}

// ClusterSetIPCIDR returns the ClusterSetIP CIDR configuration.
func (a *Agent) ClusterSetIPCIDR() string {
	return a.config.ClusterSetIPCIDR
}

// Start starts the broker agent and all registered handlers.
func (a *Agent) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.started {
		return fmt.Errorf("agent already started")
	}

	klog.Infof("Starting broker agent for cluster %s", a.config.ClusterID)

	// 1. Publish ClusterInfo to broker
	if err := a.publishClusterInfo(ctx); err != nil {
		return fmt.Errorf("failed to publish cluster info: %w", err)
	}

	// 2. Create syncer
	syncer, err := NewSyncer(a, a.handlers)
	if err != nil {
		return fmt.Errorf("failed to create syncer: %w", err)
	}
	a.syncer = syncer

	// 3. Start all handlers
	for _, handler := range a.handlers {
		klog.V(2).Infof("Starting handler: %s", handler.Name())
		if err := handler.Start(ctx); err != nil {
			return fmt.Errorf("failed to start handler %s: %w", handler.Name(), err)
		}
	}

	// 4. Start syncer (informers and workers)
	a.syncer.Start(ctx)

	a.started = true
	klog.Infof("Broker agent started successfully with %d handlers", len(a.handlers))
	return nil
}

// Stop stops the broker agent and all handlers.
func (a *Agent) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.started {
		return
	}

	klog.Infof("Stopping broker agent for cluster %s", a.config.ClusterID)

	// Stop syncer first
	if a.syncer != nil {
		a.syncer.Stop()
	}

	// Stop all handlers
	for _, handler := range a.handlers {
		klog.V(2).Infof("Stopping handler: %s", handler.Name())
		handler.Stop()
	}

	a.cancel()
	a.wg.Wait()

	a.started = false
	klog.Infof("Broker agent stopped")
}

// publishClusterInfo publishes cluster information to the broker.
func (a *Agent) publishClusterInfo(ctx context.Context) error {
	if a.config.ClusterInfoProvider == nil {
		klog.V(2).Info("No ClusterInfoProvider configured, skipping ClusterInfo publish")
		return nil
	}

	clusterInfo := &brokerv1alpha1.ClusterInfo{
		Spec: brokerv1alpha1.ClusterInfoSpec{
			ClusterID:   a.config.ClusterID,
			ServiceCIDR: a.config.ClusterInfoProvider.GetServiceCIDR(),
			ClusterCIDR: a.config.ClusterInfoProvider.GetClusterCIDR(),
			Networks:    a.config.ClusterInfoProvider.GetNetworks(),
		},
	}

	return a.brokerClient.PublishClusterInfo(ctx, clusterInfo)
}
