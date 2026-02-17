package broker

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	brokerv1alpha1 "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/ovnbroker/v1alpha1"
	brokerclientset "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/ovnbroker/v1alpha1/apis/clientset/versioned"
)

const (
	// DefaultBrokerNamespace is the default namespace in the broker cluster
	// where multi-cluster resources are stored.
	DefaultBrokerNamespace = "ovn-kubernetes-broker"
)

// Client provides access to the broker cluster.
type Client struct {
	// clusterID is the unique identifier for this cluster
	clusterID string

	// namespace is the broker namespace where resources are stored
	namespace string

	// kubeClient is the standard Kubernetes client for the broker
	kubeClient kubernetes.Interface

	// brokerClient is the typed client for broker CRDs
	brokerClient brokerclientset.Interface
}

// NewClient creates a new broker client.
func NewClient(config *rest.Config, clusterID, namespace string) (*Client, error) {
	if namespace == "" {
		namespace = DefaultBrokerNamespace
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	brokerClient, err := brokerclientset.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create broker client: %w", err)
	}

	return &Client{
		clusterID:    clusterID,
		namespace:    namespace,
		kubeClient:   kubeClient,
		brokerClient: brokerClient,
	}, nil
}

// ClusterID returns the cluster identifier.
func (c *Client) ClusterID() string {
	return c.clusterID
}

// Namespace returns the broker namespace.
func (c *Client) Namespace() string {
	return c.namespace
}

// KubeClient returns the Kubernetes client for the broker cluster.
func (c *Client) KubeClient() kubernetes.Interface {
	return c.kubeClient
}

// BrokerClient returns the typed broker CRD client.
func (c *Client) BrokerClient() brokerclientset.Interface {
	return c.brokerClient
}

// PublishClusterInfo publishes or updates cluster information to the broker.
func (c *Client) PublishClusterInfo(ctx context.Context, clusterInfo *brokerv1alpha1.ClusterInfo) error {
	klog.V(4).Infof("Publishing ClusterInfo for cluster %s to broker", c.clusterID)

	// Ensure the cluster info is named after our cluster ID
	clusterInfo.Name = c.clusterID

	// Try to get existing ClusterInfo
	existing, err := c.brokerClient.BrokerV1alpha1().ClusterInfos().Get(ctx, c.clusterID, metav1.GetOptions{})
	if err == nil {
		// Update existing
		clusterInfo.ResourceVersion = existing.ResourceVersion
		_, err = c.brokerClient.BrokerV1alpha1().ClusterInfos().Update(ctx, clusterInfo, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update ClusterInfo: %w", err)
		}
		klog.V(4).Infof("Updated ClusterInfo for cluster %s", c.clusterID)
		return nil
	}

	// Create new
	_, err = c.brokerClient.BrokerV1alpha1().ClusterInfos().Create(ctx, clusterInfo, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create ClusterInfo: %w", err)
	}
	klog.V(4).Infof("Created ClusterInfo for cluster %s", c.clusterID)
	return nil
}

// CreateOrUpdateServiceExport creates or updates a ServiceExport in the broker.
func (c *Client) CreateOrUpdateServiceExport(ctx context.Context, export *brokerv1alpha1.ServiceExport) error {
	klog.V(4).Infof("Publishing ServiceExport %s/%s from cluster %s to broker",
		export.Namespace, export.Name, c.clusterID)

	// Ensure it's in the broker namespace
	export.Namespace = c.namespace

	// Ensure cluster ID is set in status
	export.Status.ClusterID = c.clusterID

	// Try to get existing ServiceExport
	existing, err := c.brokerClient.BrokerV1alpha1().ServiceExports(c.namespace).Get(ctx, export.Name, metav1.GetOptions{})
	if err == nil {
		// Update existing
		export.ResourceVersion = existing.ResourceVersion
		_, err = c.brokerClient.BrokerV1alpha1().ServiceExports(c.namespace).Update(ctx, export, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update ServiceExport: %w", err)
		}
		klog.V(4).Infof("Updated ServiceExport %s", export.Name)
		return nil
	}

	// Create new
	_, err = c.brokerClient.BrokerV1alpha1().ServiceExports(c.namespace).Create(ctx, export, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create ServiceExport: %w", err)
	}
	klog.V(4).Infof("Created ServiceExport %s", export.Name)
	return nil
}

// DeleteServiceExport deletes a ServiceExport from the broker.
func (c *Client) DeleteServiceExport(ctx context.Context, name string) error {
	klog.V(4).Infof("Deleting ServiceExport %s from cluster %s in broker", name, c.clusterID)

	err := c.brokerClient.BrokerV1alpha1().ServiceExports(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete ServiceExport: %w", err)
	}

	klog.V(4).Infof("Deleted ServiceExport %s", name)
	return nil
}
