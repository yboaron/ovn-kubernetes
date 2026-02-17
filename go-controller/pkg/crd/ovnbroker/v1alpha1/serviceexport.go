package v1alpha1

import (
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ServiceExport represents a service exported to the broker for multi-cluster discovery.
// This is stored in the broker cluster and aggregates service information from member clusters.
//
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:path=serviceexports,scope=Namespaced
// +kubebuilder:singular=serviceexport
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type ServiceExport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec ServiceExportSpec `json:"spec,omitempty"`

	// +optional
	Status ServiceExportStatus `json:"status,omitempty"`
}

// ServiceExportSpec defines the desired state of ServiceExport.
type ServiceExportSpec struct {
	// ServiceName is the name of the exported service.
	// +kubebuilder:validation:Required
	// +required
	ServiceName string `json:"serviceName"`

	// ServiceNamespace is the namespace of the exported service.
	// +kubebuilder:validation:Required
	// +required
	ServiceNamespace string `json:"serviceNamespace"`

	// Ports are the ports exposed by this service.
	// +optional
	Ports []discoveryv1.EndpointPort `json:"ports,omitempty"`
}

// ServiceExportStatus contains the observed status of the ServiceExport.
type ServiceExportStatus struct {
	// ClusterID is the source cluster for this export.
	// +optional
	ClusterID string `json:"clusterID,omitempty"`

	// NetworkName is the CUDN this service belongs to.
	// +optional
	NetworkName string `json:"networkName,omitempty"`

	// ClusterSetIP is the allocated ClusterSet-scoped VIP for this service.
	// This IP is consistent across all clusters importing this service.
	// +optional
	ClusterSetIP string `json:"clusterSetIP,omitempty"`

	// Endpoints are the backend pod IPs for this service (network-specific).
	// +optional
	Endpoints []EndpointInfo `json:"endpoints,omitempty"`

	// Conditions slice of condition objects.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// EndpointInfo represents a single endpoint (pod) backing the service.
type EndpointInfo struct {
	// Address is the pod IP address (network-specific).
	// +kubebuilder:validation:Required
	// +required
	Address string `json:"address"`

	// Hostname is the pod's hostname.
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// NodeName is the node hosting this pod.
	// +optional
	NodeName string `json:"nodeName,omitempty"`

	// Zone is the topology zone of the node.
	// +optional
	Zone string `json:"zone,omitempty"`

	// Ready indicates if the endpoint is ready to serve traffic.
	// +optional
	Ready bool `json:"ready,omitempty"`
}

// ServiceExportList contains a list of ServiceExport.
// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ServiceExportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceExport `json:"items"`
}
