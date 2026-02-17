package broker

import (
	"k8s.io/klog/v2"

	brokerv1alpha1 "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/ovnbroker/v1alpha1"
	cudnv1 "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/userdefinednetwork/v1"
	cudnlisters "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/userdefinednetwork/v1/apis/listers/userdefinednetwork/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// DefaultClusterInfoProvider implements ClusterInfoProvider using CUDN listers.
type DefaultClusterInfoProvider struct {
	cudnLister cudnlisters.ClusterUserDefinedNetworkLister

	// Static cluster information
	serviceCIDR []string
	clusterCIDR []string
}

// NewDefaultClusterInfoProvider creates a new default cluster info provider.
func NewDefaultClusterInfoProvider(
	cudnLister cudnlisters.ClusterUserDefinedNetworkLister,
	serviceCIDR []string,
	clusterCIDR []string,
) *DefaultClusterInfoProvider {
	return &DefaultClusterInfoProvider{
		cudnLister:  cudnLister,
		serviceCIDR: serviceCIDR,
		clusterCIDR: clusterCIDR,
	}
}

// GetServiceCIDR returns the service CIDR range(s) for this cluster.
func (p *DefaultClusterInfoProvider) GetServiceCIDR() []string {
	return p.serviceCIDR
}

// GetClusterCIDR returns the pod CIDR range(s) for the default network.
func (p *DefaultClusterInfoProvider) GetClusterCIDR() []string {
	return p.clusterCIDR
}

// GetNetworks returns the CUDN network information for this cluster.
func (p *DefaultClusterInfoProvider) GetNetworks() []brokerv1alpha1.NetworkInfo {
	cudns, err := p.cudnLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("Failed to list CUDNs for cluster info: %v", err)
		return []brokerv1alpha1.NetworkInfo{}
	}

	networks := make([]brokerv1alpha1.NetworkInfo, 0, len(cudns))
	for _, cudn := range cudns {
		networks = append(networks, p.cudnToNetworkInfo(cudn))
	}

	return networks
}

// cudnToNetworkInfo converts a CUDN to broker NetworkInfo.
func (p *DefaultClusterInfoProvider) cudnToNetworkInfo(cudn *cudnv1.ClusterUserDefinedNetwork) brokerv1alpha1.NetworkInfo {
	info := brokerv1alpha1.NetworkInfo{
		Name:     cudn.Name,
		Topology: string(cudn.Spec.Network.Topology),
	}

	// Extract CIDR based on topology
	switch cudn.Spec.Network.Topology {
	case cudnv1.NetworkTopologyLayer3:
		if cudn.Spec.Network.Layer3 != nil && cudn.Spec.Network.Layer3.Subnets != nil {
			for _, subnet := range cudn.Spec.Network.Layer3.Subnets {
				info.CIDR = append(info.CIDR, string(subnet.CIDR))
			}
		}
	case cudnv1.NetworkTopologyLayer2:
		if cudn.Spec.Network.Layer2 != nil && cudn.Spec.Network.Layer2.Subnets != nil {
			for _, cidr := range cudn.Spec.Network.Layer2.Subnets {
				info.CIDR = append(info.CIDR, string(cidr))
			}
		}
	case cudnv1.NetworkTopologyLocalnet:
		if cudn.Spec.Network.Localnet != nil && cudn.Spec.Network.Localnet.Subnets != nil {
			for _, cidr := range cudn.Spec.Network.Localnet.Subnets {
				info.CIDR = append(info.CIDR, string(cidr))
			}
		}
	}

	// Transport
	if cudn.Spec.Network.Transport != "" {
		info.Transport = string(cudn.Spec.Network.Transport)
	}

	// EVPN configuration
	if cudn.Spec.Network.Transport == cudnv1.TransportOptionEVPN && cudn.Spec.Network.EVPN != nil {
		evpnInfo := &brokerv1alpha1.EVPNInfo{}

		// IP-VRF (Layer3)
		if cudn.Spec.Network.EVPN.IPVRF != nil {
			evpnInfo.IPVRFVNI = cudn.Spec.Network.EVPN.IPVRF.VNI
			evpnInfo.IPVRFRouteTarget = string(cudn.Spec.Network.EVPN.IPVRF.RouteTarget)
		}

		// MAC-VRF (Layer2)
		if cudn.Spec.Network.EVPN.MACVRF != nil {
			evpnInfo.MACVRFVNI = cudn.Spec.Network.EVPN.MACVRF.VNI
			evpnInfo.MACVRFRouteTarget = string(cudn.Spec.Network.EVPN.MACVRF.RouteTarget)
		}

		info.EVPN = evpnInfo
	}

	return info
}
