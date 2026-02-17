package mcs

import (
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"

	cudnv1 "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/userdefinednetwork/v1"
	cudnlisters "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/userdefinednetwork/v1/apis/listers/userdefinednetwork/v1"
)

// CUDNProvider provides information about CUDN networks.
type CUDNProvider interface {
	// GetCUDNForNamespace returns the CUDN name for a given namespace, or empty string if none.
	GetCUDNForNamespace(namespace string) (string, error)

	// HasCUDN checks if a CUDN with the given name exists in this cluster.
	HasCUDN(name string) bool

	// GetCUDN returns the CUDN object by name.
	GetCUDN(name string) (*cudnv1.ClusterUserDefinedNetwork, error)

	// GetCUDNForService returns the CUDN name for a service's namespace.
	GetCUDNForService(namespace, serviceName string) (string, error)
}

// DefaultCUDNProvider is the default implementation of CUDNProvider.
type DefaultCUDNProvider struct {
	cudnLister      cudnlisters.ClusterUserDefinedNetworkLister
	namespaceLister corelisters.NamespaceLister

	// Cache for namespace -> CUDN mapping
	// Rebuilt on demand when CUDN or namespace changes
	nsCache map[string]string // namespace -> CUDN name
	mu      sync.RWMutex
}

// NewDefaultCUDNProvider creates a new default CUDN provider.
func NewDefaultCUDNProvider(
	cudnLister cudnlisters.ClusterUserDefinedNetworkLister,
	namespaceLister corelisters.NamespaceLister,
) *DefaultCUDNProvider {
	return &DefaultCUDNProvider{
		cudnLister:      cudnLister,
		namespaceLister: namespaceLister,
		nsCache:         make(map[string]string),
	}
}

// GetCUDNForNamespace returns the CUDN name for a given namespace.
func (p *DefaultCUDNProvider) GetCUDNForNamespace(namespace string) (string, error) {
	// Check cache first
	p.mu.RLock()
	if cudnName, exists := p.nsCache[namespace]; exists {
		p.mu.RUnlock()
		return cudnName, nil
	}
	p.mu.RUnlock()

	// Cache miss - compute and cache
	cudnName, err := p.computeCUDNForNamespace(namespace)
	if err != nil {
		return "", err
	}

	// Update cache
	p.mu.Lock()
	p.nsCache[namespace] = cudnName
	p.mu.Unlock()

	return cudnName, nil
}

// computeCUDNForNamespace finds which CUDN selects the given namespace.
func (p *DefaultCUDNProvider) computeCUDNForNamespace(namespace string) (string, error) {
	// Get the namespace object
	ns, err := p.namespaceLister.Get(namespace)
	if err != nil {
		return "", fmt.Errorf("failed to get namespace %s: %w", namespace, err)
	}

	// List all CUDNs
	cudns, err := p.cudnLister.List(labels.Everything())
	if err != nil {
		return "", fmt.Errorf("failed to list CUDNs: %w", err)
	}

	// Find the CUDN that selects this namespace
	// Note: In a real implementation, we should handle conflicts
	// (multiple CUDNs selecting the same namespace)
	for _, cudn := range cudns {
		if p.cudnSelectsNamespace(cudn, ns) {
			return cudn.Name, nil
		}
	}

	// No CUDN selects this namespace
	return "", nil
}

// cudnSelectsNamespace checks if a CUDN's namespace selector matches a namespace.
func (p *DefaultCUDNProvider) cudnSelectsNamespace(cudn *cudnv1.ClusterUserDefinedNetwork, ns *corev1.Namespace) bool {
	selector, err := metav1.LabelSelectorAsSelector(&cudn.Spec.NamespaceSelector)
	if err != nil {
		klog.Errorf("Invalid namespace selector for CUDN %s: %v", cudn.Name, err)
		return false
	}

	return selector.Matches(labels.Set(ns.Labels))
}

// HasCUDN checks if a CUDN with the given name exists.
func (p *DefaultCUDNProvider) HasCUDN(name string) bool {
	_, err := p.cudnLister.Get(name)
	return err == nil
}

// GetCUDN returns the CUDN object by name.
func (p *DefaultCUDNProvider) GetCUDN(name string) (*cudnv1.ClusterUserDefinedNetwork, error) {
	cudn, err := p.cudnLister.Get(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get CUDN %s: %w", name, err)
	}
	return cudn, nil
}

// GetCUDNForService returns the CUDN name for a service's namespace.
func (p *DefaultCUDNProvider) GetCUDNForService(namespace, serviceName string) (string, error) {
	return p.GetCUDNForNamespace(namespace)
}

// InvalidateCache invalidates the namespace cache.
// Should be called when CUDNs or namespaces change.
func (p *DefaultCUDNProvider) InvalidateCache() {
	p.mu.Lock()
	p.nsCache = make(map[string]string)
	p.mu.Unlock()
	klog.V(4).Info("CUDN provider cache invalidated")
}

// InvalidateNamespace invalidates the cache entry for a specific namespace.
func (p *DefaultCUDNProvider) InvalidateNamespace(namespace string) {
	p.mu.Lock()
	delete(p.nsCache, namespace)
	p.mu.Unlock()
	klog.V(5).Infof("CUDN provider cache invalidated for namespace %s", namespace)
}

// GetAllCUDNs returns all CUDNs in the cluster.
func (p *DefaultCUDNProvider) GetAllCUDNs() ([]*cudnv1.ClusterUserDefinedNetwork, error) {
	cudns, err := p.cudnLister.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("failed to list CUDNs: %w", err)
	}
	return cudns, nil
}

// GetCUDNInfo returns network information for a CUDN.
// This can be used to populate broker ClusterInfo.
func (p *DefaultCUDNProvider) GetCUDNInfo(cudn *cudnv1.ClusterUserDefinedNetwork) NetworkInfo {
	info := NetworkInfo{
		Name:     cudn.Name,
		Topology: string(cudn.Spec.Network.Topology),
	}

	// Extract subnets based on topology
	switch cudn.Spec.Network.Topology {
	case cudnv1.NetworkTopologyLayer3:
		if cudn.Spec.Network.Layer3 != nil && cudn.Spec.Network.Layer3.Subnets != nil {
			for _, subnet := range cudn.Spec.Network.Layer3.Subnets {
				info.Subnets = append(info.Subnets, string(subnet.CIDR))
			}
		}
	case cudnv1.NetworkTopologyLayer2:
		if cudn.Spec.Network.Layer2 != nil && cudn.Spec.Network.Layer2.Subnets != nil {
			for _, subnet := range cudn.Spec.Network.Layer2.Subnets {
				info.Subnets = append(info.Subnets, string(subnet))
			}
		}
	}

	// Extract transport
	if cudn.Spec.Network.Transport != "" {
		info.Transport = string(cudn.Spec.Network.Transport)
	}

	// Extract EVPN config
	if cudn.Spec.Network.Transport == cudnv1.TransportOptionEVPN && cudn.Spec.Network.EVPN != nil {
		info.EVPN = &EVPNInfo{}

		if cudn.Spec.Network.EVPN.IPVRF != nil {
			info.EVPN.IPVRFVNI = cudn.Spec.Network.EVPN.IPVRF.VNI
			info.EVPN.IPVRFRouteTarget = string(cudn.Spec.Network.EVPN.IPVRF.RouteTarget)
		}

		if cudn.Spec.Network.EVPN.MACVRF != nil {
			info.EVPN.MACVRFVNI = cudn.Spec.Network.EVPN.MACVRF.VNI
			info.EVPN.MACVRFRouteTarget = string(cudn.Spec.Network.EVPN.MACVRF.RouteTarget)
		}
	}

	return info
}

// NetworkInfo represents network information (mirrors broker CRD).
type NetworkInfo struct {
	Name      string
	Subnets   []string
	Topology  string
	Transport string
	EVPN      *EVPNInfo
}

// EVPNInfo contains EVPN-specific configuration (mirrors broker CRD).
type EVPNInfo struct {
	IPVRFVNI           int32
	IPVRFRouteTarget   string
	MACVRFVNI          int32
	MACVRFRouteTarget  string
	ASN                int32
}
