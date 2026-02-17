package mcs

import (
	"context"
	"fmt"

	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	brokerv1alpha1 "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/ovnbroker/v1alpha1"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/types"
)

// Importer handles importing remote services from the broker.
type Importer struct {
	handler *Handler
}

// NewImporter creates a new Importer.
func NewImporter(handler *Handler) *Importer {
	return &Importer{
		handler: handler,
	}
}

// ImportService imports a remote service from the broker by creating a ServiceImport and EndpointSlices.
func (i *Importer) ImportService(brokerExport *brokerv1alpha1.ServiceExport) error {
	klog.V(4).Infof("Importing service %s from cluster %s (network: %s)",
		brokerExport.Spec.ServiceName, brokerExport.Status.ClusterID, brokerExport.Status.NetworkName)

	namespace := brokerExport.Spec.ServiceNamespace
	serviceName := brokerExport.Spec.ServiceName
	sourceCluster := brokerExport.Status.ClusterID

	// 1. Create or update ServiceImport
	if err := i.ensureServiceImport(namespace, serviceName, brokerExport); err != nil {
		klog.Errorf("Failed to ensure ServiceImport: %v", err)
		return err
	}

	// 2. Create or update EndpointSlice for this source cluster
	if err := i.ensureEndpointSlice(namespace, serviceName, sourceCluster, brokerExport); err != nil {
		klog.Errorf("Failed to ensure EndpointSlice: %v", err)
		return err
	}

	klog.Infof("Successfully imported service %s/%s from cluster %s (%d endpoints)",
		namespace, serviceName, sourceCluster, len(brokerExport.Status.Endpoints))

	return nil
}

// UnimportService removes an imported service.
func (i *Importer) UnimportService(brokerExport *brokerv1alpha1.ServiceExport) error {
	klog.V(4).Infof("Unimporting service %s from cluster %s",
		brokerExport.Spec.ServiceName, brokerExport.Status.ClusterID)

	namespace := brokerExport.Spec.ServiceNamespace
	serviceName := brokerExport.Spec.ServiceName
	sourceCluster := brokerExport.Status.ClusterID

	// Delete the EndpointSlice for this source cluster
	sliceName := fmt.Sprintf("imported-%s-%s", serviceName, sourceCluster)
	err := i.handler.agent.LocalKubeClient().DiscoveryV1().EndpointSlices(namespace).Delete(
		context.TODO(), sliceName, metav1.DeleteOptions{})
	if err != nil {
		klog.Warningf("Failed to delete EndpointSlice %s/%s: %v", namespace, sliceName, err)
	}

	// TODO: Check if we should delete ServiceImport (only if no more remote clusters exporting)
	// For now, leaving ServiceImport in place

	klog.Infof("Successfully unimported service %s/%s from cluster %s", namespace, serviceName, sourceCluster)
	return nil
}

// ensureServiceImport creates or updates a ServiceImport.
func (i *Importer) ensureServiceImport(namespace, serviceName string, brokerExport *brokerv1alpha1.ServiceExport) error {
	// Create ServiceImport
	serviceImport := &brokerv1alpha1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
			Labels: map[string]string{
				"multicluster.kubernetes.io/service-name": serviceName,
				"networking.k8s.ovn.org/network":          brokerExport.Status.NetworkName,
			},
		},
		Spec: brokerv1alpha1.ServiceImportSpec{
			Type:  brokerv1alpha1.ClusterSetIP,
			Ports: brokerExport.Spec.Ports,
		},
		Status: brokerv1alpha1.ServiceImportStatus{
			NetworkName: brokerExport.Status.NetworkName,
		},
	}

	// Set ClusterSetIP if available
	if brokerExport.Status.ClusterSetIP != "" {
		serviceImport.Spec.IPs = []string{brokerExport.Status.ClusterSetIP}
	}

	// Try to get existing ServiceImport
	existing, err := i.handler.agent.BrokerClient().BrokerClient().BrokerV1alpha1().
		ServiceImports(namespace).Get(context.TODO(), serviceName, metav1.GetOptions{})

	if err == nil {
		// Update existing
		serviceImport.ResourceVersion = existing.ResourceVersion
		_, err = i.handler.agent.BrokerClient().BrokerClient().BrokerV1alpha1().
			ServiceImports(namespace).Update(context.TODO(), serviceImport, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update ServiceImport: %w", err)
		}
		klog.V(4).Infof("Updated ServiceImport %s/%s", namespace, serviceName)
	} else {
		// Create new
		_, err = i.handler.agent.BrokerClient().BrokerClient().BrokerV1alpha1().
			ServiceImports(namespace).Create(context.TODO(), serviceImport, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create ServiceImport: %w", err)
		}
		klog.V(4).Infof("Created ServiceImport %s/%s", namespace, serviceName)
	}

	return nil
}

// ensureEndpointSlice creates or updates an EndpointSlice for remote endpoints.
func (i *Importer) ensureEndpointSlice(namespace, serviceName, sourceCluster string,
	brokerExport *brokerv1alpha1.ServiceExport) error {

	sliceName := fmt.Sprintf("imported-%s-%s", serviceName, sourceCluster)

	// Build endpoints from broker export
	var endpoints []discoveryv1.Endpoint
	for _, epInfo := range brokerExport.Status.Endpoints {
		ready := epInfo.Ready
		ep := discoveryv1.Endpoint{
			Addresses: []string{epInfo.Address},
			Conditions: discoveryv1.EndpointConditions{
				Ready: &ready,
			},
		}

		if epInfo.Hostname != "" {
			ep.Hostname = &epInfo.Hostname
		}
		if epInfo.NodeName != "" {
			ep.NodeName = &epInfo.NodeName
		}
		if epInfo.Zone != "" {
			ep.Zone = &epInfo.Zone
		}

		endpoints = append(endpoints, ep)
	}

	// Create EndpointSlice
	// NOTE: For CUDN services, we use different labels than default network services:
	// - Label: types.LabelUserDefinedServiceName (not discoveryv1.LabelServiceName)
	// - Annotation: types.UserDefinedNetworkEndpointSliceAnnotation (not label)
	// This matches what the services controller expects for UDN/CUDN services.
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sliceName,
			Namespace: namespace,
			Labels: map[string]string{
				// Use UDN-specific label for CUDN services
				types.LabelUserDefinedServiceName:           serviceName,
				discoveryv1.LabelManagedBy:                  "ovn-k8s-mcs-controller",
				"multicluster.kubernetes.io/service-name":   serviceName,
				"multicluster.kubernetes.io/source-cluster": sourceCluster,
			},
			Annotations: map[string]string{
				// Network name goes in annotation (not label) for CUDN services
				types.UserDefinedNetworkEndpointSliceAnnotation: brokerExport.Status.NetworkName,
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints:   endpoints,
		Ports:       brokerExport.Spec.Ports,
	}

	// Try to get existing EndpointSlice
	existing, err := i.handler.agent.LocalKubeClient().DiscoveryV1().EndpointSlices(namespace).Get(
		context.TODO(), sliceName, metav1.GetOptions{})

	if err == nil {
		// Update existing
		slice.ResourceVersion = existing.ResourceVersion
		_, err = i.handler.agent.LocalKubeClient().DiscoveryV1().EndpointSlices(namespace).Update(
			context.TODO(), slice, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update EndpointSlice: %w", err)
		}
		klog.V(4).Infof("Updated EndpointSlice %s/%s", namespace, sliceName)
	} else {
		// Create new
		_, err = i.handler.agent.LocalKubeClient().DiscoveryV1().EndpointSlices(namespace).Create(
			context.TODO(), slice, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create EndpointSlice: %w", err)
		}
		klog.V(4).Infof("Created EndpointSlice %s/%s with %d endpoints", namespace, sliceName, len(endpoints))
	}

	return nil
}
