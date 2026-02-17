package v1alpha1

import (
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ServiceImport represents an imported service from the broker.
// This is created in local clusters to represent remote services.
//
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:path=serviceimports,scope=Namespaced
// +kubebuilder:singular=serviceimport
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type ServiceImport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec ServiceImportSpec `json:"spec,omitempty"`

	// +optional
	Status ServiceImportStatus `json:"status,omitempty"`
}

// ServiceImportSpec defines the desired state of ServiceImport.
type ServiceImportSpec struct {
	// Type defines the type of this service.
	// +kubebuilder:validation:Enum=ClusterSetIP;Headless
	// +optional
	Type ServiceImportType `json:"type,omitempty"`

	// IPs are the ClusterSet IPs for this service.
	// For ClusterSetIP type, this will contain the allocated VIP.
	// +optional
	IPs []string `json:"ips,omitempty"`

	// Ports are the ports exposed by this service.
	// +optional
	Ports []discoveryv1.EndpointPort `json:"ports,omitempty"`

	// SessionAffinity defines the session affinity configuration.
	// +kubebuilder:validation:Enum=None;ClientIP
	// +optional
	SessionAffinity string `json:"sessionAffinity,omitempty"`

	// SessionAffinityConfig contains session affinity configuration.
	// +optional
	SessionAffinityConfig *SessionAffinityConfig `json:"sessionAffinityConfig,omitempty"`
}

// ServiceImportType defines the type of ServiceImport.
type ServiceImportType string

const (
	// ClusterSetIP means the service has a ClusterSet-scoped VIP.
	ClusterSetIP ServiceImportType = "ClusterSetIP"
	// Headless means the service is headless (no VIP, direct pod IPs).
	Headless ServiceImportType = "Headless"
)

// SessionAffinityConfig represents the configurations of session affinity.
type SessionAffinityConfig struct {
	// ClientIP contains the configurations of Client IP based session affinity.
	// +optional
	ClientIP *ClientIPConfig `json:"clientIP,omitempty"`
}

// ClientIPConfig represents the configurations of Client IP based session affinity.
type ClientIPConfig struct {
	// TimeoutSeconds specifies the seconds of ClientIP type session sticky time.
	// +optional
	TimeoutSeconds *int32 `json:"timeoutSeconds,omitempty"`
}

// ServiceImportStatus contains the observed status of the ServiceImport.
type ServiceImportStatus struct {
	// NetworkName is the CUDN this service belongs to.
	// +optional
	NetworkName string `json:"networkName,omitempty"`

	// Clusters contains the source clusters exporting this service.
	// +optional
	Clusters []ClusterStatus `json:"clusters,omitempty"`

	// Conditions slice of condition objects.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ClusterStatus represents the status from a specific source cluster.
type ClusterStatus struct {
	// ClusterID is the source cluster identifier.
	// +required
	ClusterID string `json:"clusterID"`

	// EndpointCount is the number of endpoints from this cluster.
	// +optional
	EndpointCount int32 `json:"endpointCount,omitempty"`
}

// ServiceImportList contains a list of ServiceImport.
// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ServiceImportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceImport `json:"items"`
}
