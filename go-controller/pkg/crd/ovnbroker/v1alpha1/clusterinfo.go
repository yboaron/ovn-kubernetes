package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ClusterInfo represents a cluster participating in the multi-cluster broker.
// It stores cluster-level metadata including network configurations.
//
// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:path=clusterinfos,scope=Cluster
// +kubebuilder:singular=clusterinfo
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type ClusterInfo struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	// +required
	Spec ClusterInfoSpec `json:"spec"`

	// +optional
	Status ClusterInfoStatus `json:"status,omitempty"`
}

// ClusterInfoSpec defines the desired state of ClusterInfo.
type ClusterInfoSpec struct {
	// ClusterID is the unique identifier for this cluster.
	// +kubebuilder:validation:Required
	// +required
	ClusterID string `json:"clusterID"`

	// ServiceCIDR is the service CIDR range(s) for this cluster.
	// +optional
	ServiceCIDR []string `json:"serviceCIDR,omitempty"`

	// ClusterCIDR is the pod CIDR range(s) for the default network.
	// +optional
	ClusterCIDR []string `json:"clusterCIDR,omitempty"`

	// GlobalCIDR is the global CIDR for this cluster (if using Globalnet-style NAT).
	// +optional
	GlobalCIDR []string `json:"globalCIDR,omitempty"`

	// Networks contains CUDN network information for this cluster.
	// +optional
	Networks []NetworkInfo `json:"networks,omitempty"`
}

// NetworkInfo describes a CUDN network in the cluster.
type NetworkInfo struct {
	// Name of the CUDN.
	// +kubebuilder:validation:Required
	// +required
	Name string `json:"name"`

	// CIDR range(s) for this network.
	// +optional
	CIDR []string `json:"cidr,omitempty"`

	// Topology of the network (Layer2, Layer3, Localnet).
	// +kubebuilder:validation:Enum=Layer2;Layer3;Localnet
	// +optional
	Topology string `json:"topology,omitempty"`

	// Transport technology (Geneve, EVPN, NoOverlay).
	// +kubebuilder:validation:Enum=NoOverlay;Geneve;EVPN
	// +optional
	Transport string `json:"transport,omitempty"`

	// EVPN configuration if transport is EVPN.
	// +optional
	EVPN *EVPNInfo `json:"evpn,omitempty"`
}

// EVPNInfo contains EVPN-specific configuration.
type EVPNInfo struct {
	// IPVRFVNI is the VNI for IP-VRF.
	// +optional
	IPVRFVNI int32 `json:"ipvrfVNI,omitempty"`

	// IPVRFRouteTarget is the Route Target for IP-VRF.
	// +optional
	IPVRFRouteTarget string `json:"ipvrfRouteTarget,omitempty"`

	// MACVRFVNI is the VNI for MAC-VRF (Layer2 only).
	// +optional
	MACVRFVNI int32 `json:"macvrfVNI,omitempty"`

	// MACVRFRouteTarget is the Route Target for MAC-VRF (Layer2 only).
	// +optional
	MACVRFRouteTarget string `json:"macvrfRouteTarget,omitempty"`

	// ASN is the BGP ASN for this cluster.
	// +optional
	ASN int32 `json:"asn,omitempty"`
}

// ClusterInfoStatus contains the observed status of the ClusterInfo.
type ClusterInfoStatus struct {
	// Conditions slice of condition objects indicating details about ClusterInfo status.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ClusterInfoList contains a list of ClusterInfo.
// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ClusterInfoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterInfo `json:"items"`
}
