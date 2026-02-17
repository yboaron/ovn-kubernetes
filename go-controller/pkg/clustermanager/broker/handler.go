package broker

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Handler is the interface for pluggable broker components (MCS, Network, etc.).
// Each handler implements specific multi-cluster functionality using the generic broker infrastructure.
type Handler interface {
	// Name returns the handler name for logging and identification.
	Name() string

	// GetWatchedResources returns the CRDs this handler wants to watch.
	// The syncer will set up informers for these resources in both local and broker clusters.
	GetWatchedResources() []WatchedResource

	// OnLocalAdd is called when a local resource is added.
	// The handler should process the resource and potentially publish to broker.
	OnLocalAdd(obj interface{}) error

	// OnLocalUpdate is called when a local resource is updated.
	OnLocalUpdate(oldObj, newObj interface{}) error

	// OnLocalDelete is called when a local resource is deleted.
	OnLocalDelete(obj interface{}) error

	// OnBrokerAdd is called when a broker resource is added (from any cluster).
	// The handler should process the resource and potentially create local resources.
	OnBrokerAdd(obj interface{}) error

	// OnBrokerUpdate is called when a broker resource is updated.
	OnBrokerUpdate(oldObj, newObj interface{}) error

	// OnBrokerDelete is called when a broker resource is deleted.
	OnBrokerDelete(obj interface{}) error

	// Start starts any background goroutines or controllers.
	// This is called once when the broker agent starts.
	Start(ctx context.Context) error

	// Stop stops the handler and cleans up resources.
	Stop()
}

// WatchedResource defines a resource type that a handler wants to watch.
type WatchedResource struct {
	// GroupVersionKind identifies the resource type
	GroupVersionKind schema.GroupVersionKind

	// Local indicates if this resource should be watched in the local cluster
	Local bool

	// Broker indicates if this resource should be watched in the broker cluster
	Broker bool
}
