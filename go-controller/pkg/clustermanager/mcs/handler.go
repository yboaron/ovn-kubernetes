package mcs

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	corev1listers "k8s.io/client-go/listers/core/v1"
	discoverylisters "k8s.io/client-go/listers/discovery/v1"
	"k8s.io/klog/v2"

	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/clustermanager/broker"
	brokerv1alpha1 "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/ovnbroker/v1alpha1"
	cudnlisters "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/userdefinednetwork/v1/apis/listers/userdefinednetwork/v1"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/types"
	mcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

// HandlerConfig contains the configuration for the MCS handler.
type HandlerConfig struct {
	// Agent is the broker agent
	Agent *broker.Agent

	// ServiceLister lists services in the local cluster
	ServiceLister corev1listers.ServiceLister

	// EndpointSliceLister lists endpoint slices in the local cluster
	EndpointSliceLister discoverylisters.EndpointSliceLister

	// NamespaceLister lists namespaces in the local cluster
	NamespaceLister corev1listers.NamespaceLister

	// CUDNProvider provides CUDN network information
	CUDNProvider CUDNProvider

	// CUDNLister lists CUDNs in the local cluster (for provider creation)
	CUDNLister cudnlisters.ClusterUserDefinedNetworkLister
}

// Handler implements broker.Handler for Multi-Cluster Services.
type Handler struct {
	agent               *broker.Agent
	serviceLister       corev1listers.ServiceLister
	endpointSliceLister discoverylisters.EndpointSliceLister
	namespaceLister     corev1listers.NamespaceLister
	cudnProvider        CUDNProvider

	exporter *Exporter
	importer *Importer

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
}

// NewHandler creates a new MCS handler.
func NewHandler(config *HandlerConfig) *Handler {
	ctx, cancel := context.WithCancel(context.Background())

	// Create CUDN provider if not provided
	cudnProvider := config.CUDNProvider
	if cudnProvider == nil && config.CUDNLister != nil && config.NamespaceLister != nil {
		cudnProvider = NewDefaultCUDNProvider(config.CUDNLister, config.NamespaceLister)
		klog.V(2).Info("Created default CUDN provider for MCS handler")
	}

	// Log if any listers are nil
	if config.ServiceLister == nil {
		klog.Warning("MCS Handler: ServiceLister is nil")
	}
	if config.EndpointSliceLister == nil {
		klog.Warning("MCS Handler: EndpointSliceLister is nil")
	}
	if config.NamespaceLister == nil {
		klog.Warning("MCS Handler: NamespaceLister is nil")
	}

	h := &Handler{
		agent:               config.Agent,
		serviceLister:       config.ServiceLister,
		endpointSliceLister: config.EndpointSliceLister,
		namespaceLister:     config.NamespaceLister,
		cudnProvider:        cudnProvider,
		ctx:                 ctx,
		cancel:              cancel,
	}

	// Create exporter and importer
	h.exporter = NewExporter(h)
	h.importer = NewImporter(h)

	// Create ClusterSetIP allocator if this is the broker cluster
	// Check if the agent has ClusterSetIPCIDR configured
	if cidr := config.Agent.ClusterSetIPCIDR(); cidr != "" {
		allocator, err := NewClusterSetIPAllocator(cidr)
		if err != nil {
			klog.Errorf("Failed to create ClusterSetIP allocator: %v", err)
		} else {
			config.Agent.BrokerClient().SetClusterSetIPAllocator(allocator)
			klog.Infof("Created ClusterSetIP allocator for CIDR: %s", cidr)
		}
	}

	return h
}

// Name implements broker.Handler
func (h *Handler) Name() string {
	return "MCS"
}

// GetWatchedResources implements broker.Handler
func (h *Handler) GetWatchedResources() []broker.WatchedResource {
	return []broker.WatchedResource{
		{
			// Watch local ServiceExport (upstream MCS API)
			GroupVersionKind: schema.GroupVersionKind{
				Group:   "multicluster.x-k8s.io",
				Version: "v1alpha1",
				Kind:    "ServiceExport",
			},
			Local:  true,
			Broker: false,
		},
		{
			// Watch broker ServiceExport (our custom CRD)
			GroupVersionKind: schema.GroupVersionKind{
				Group:   brokerv1alpha1.GroupName,
				Version: brokerv1alpha1.Version,
				Kind:    "ServiceExport",
			},
			Local:  false,
			Broker: true,
		},
	}
}

// convertToServiceExport converts an unstructured object to a ServiceExport.
func convertToServiceExport(obj interface{}) (*mcsv1alpha1.ServiceExport, error) {
	// Try direct type assertion first
	if export, ok := obj.(*mcsv1alpha1.ServiceExport); ok {
		return export, nil
	}

	// Try unstructured conversion
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T, expected *mcsv1alpha1.ServiceExport or *unstructured.Unstructured", obj)
	}

	export := &mcsv1alpha1.ServiceExport{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, export); err != nil {
		return nil, fmt.Errorf("failed to convert unstructured to ServiceExport: %w", err)
	}

	return export, nil
}

// OnLocalAdd implements broker.Handler - handles local ServiceExport creation
func (h *Handler) OnLocalAdd(obj interface{}) error {
	export, err := convertToServiceExport(obj)
	if err != nil {
		klog.Warningf("MCS handler: OnLocalAdd conversion failed: %v", err)
		return nil
	}

	klog.Infof("MCS handler: ServiceExport added: %s/%s", export.Namespace, export.Name)
	return h.exporter.ExportService(export)
}

// OnLocalUpdate implements broker.Handler - handles local ServiceExport updates
func (h *Handler) OnLocalUpdate(oldObj, newObj interface{}) error {
	export, err := convertToServiceExport(newObj)
	if err != nil {
		klog.Warningf("MCS handler: OnLocalUpdate conversion failed: %v", err)
		return nil
	}

	klog.V(4).Infof("MCS handler: ServiceExport updated: %s/%s", export.Namespace, export.Name)
	return h.exporter.ExportService(export)
}

// OnLocalDelete implements broker.Handler - handles local ServiceExport deletion
func (h *Handler) OnLocalDelete(obj interface{}) error {
	export, err := convertToServiceExport(obj)
	if err != nil {
		klog.Warningf("MCS handler: OnLocalDelete conversion failed: %v", err)
		return nil
	}

	klog.V(4).Infof("MCS handler: ServiceExport deleted: %s/%s", export.Namespace, export.Name)
	return h.exporter.UnexportService(export)
}

// OnBrokerAdd implements broker.Handler - handles broker ServiceExport from other clusters
func (h *Handler) OnBrokerAdd(obj interface{}) error {
	export, ok := obj.(*brokerv1alpha1.ServiceExport)
	if !ok {
		return nil
	}

	// Skip our own exports
	if export.Status.ClusterID == h.agent.ClusterID() {
		klog.V(5).Infof("Skipping own ServiceExport: %s", export.Name)
		return nil
	}

	// Check if we have a matching CUDN
	if !h.cudnProvider.HasCUDN(export.Status.NetworkName) {
		klog.V(5).Infof("Skipping ServiceExport %s - no matching CUDN %s", export.Name, export.Status.NetworkName)
		return nil
	}

	klog.V(4).Infof("MCS handler: Broker ServiceExport added from cluster %s: %s (network: %s)",
		export.Status.ClusterID, export.Name, export.Status.NetworkName)
	return h.importer.ImportService(export)
}

// OnBrokerUpdate implements broker.Handler
func (h *Handler) OnBrokerUpdate(oldObj, newObj interface{}) error {
	return h.OnBrokerAdd(newObj)
}

// OnBrokerDelete implements broker.Handler
func (h *Handler) OnBrokerDelete(obj interface{}) error {
	export, ok := obj.(*brokerv1alpha1.ServiceExport)
	if !ok {
		return nil
	}

	// Skip our own exports
	if export.Status.ClusterID == h.agent.ClusterID() {
		return nil
	}

	klog.V(4).Infof("MCS handler: Broker ServiceExport deleted from cluster %s: %s",
		export.Status.ClusterID, export.Name)
	return h.importer.UnimportService(export)
}

// Start implements broker.Handler
func (h *Handler) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	klog.V(2).Info("Starting MCS handler")

	// Start exporter and importer background workers if needed
	// For now, they work synchronously via broker callbacks

	return nil
}

// Stop implements broker.Handler
func (h *Handler) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()

	klog.V(2).Info("Stopping MCS handler")
	h.cancel()
}

// Helper method to get service by namespace and name
func (h *Handler) getService(namespace, name string) (*corev1.Service, error) {
	svc, err := h.serviceLister.Services(namespace).Get(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get service %s/%s: %w", namespace, name, err)
	}
	return svc, nil
}

// Helper method to get namespace
func (h *Handler) getNamespace(name string) (*corev1.Namespace, error) {
	ns, err := h.namespaceLister.Get(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get namespace %s: %w", name, err)
	}
	return ns, nil
}

// Helper method to list endpoint slices for a service
func (h *Handler) listEndpointSlices(namespace, serviceName string) ([]*discoveryv1.EndpointSlice, error) {
	// Use the Kubernetes client directly since the lister may not be initialized
	// for cluster manager (which doesn't watch EndpointSlices by default)
	//
	// For CUDN services, mirrored EndpointSlices use the label "k8s.ovn.org/service-name"
	// instead of the standard "kubernetes.io/service-name"
	labelSelector := fmt.Sprintf("%s=%s", types.LabelUserDefinedServiceName, serviceName)

	sliceList, err := h.agent.LocalKubeClient().DiscoveryV1().EndpointSlices(namespace).List(
		context.TODO(),
		metav1.ListOptions{
			LabelSelector: labelSelector,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list EndpointSlices: %w", err)
	}

	var slices []*discoveryv1.EndpointSlice
	for i := range sliceList.Items {
		slices = append(slices, &sliceList.Items[i])
	}

	return slices, nil
}
