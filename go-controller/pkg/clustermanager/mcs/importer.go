package mcs

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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

	// 2. Create or update derived Service for the imported service
	if err := i.ensureService(namespace, serviceName, brokerExport); err != nil {
		klog.Errorf("Failed to ensure Service: %v", err)
		return err
	}

	// 3. Create or update EndpointSlice for this source cluster
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

	// Check if we should delete Service and ServiceImport (only if no more remote clusters exporting)
	// List all EndpointSlices for this service to see if any remain
	remainingSlices, err := i.handler.listEndpointSlices(namespace, serviceName)
	if err != nil {
		klog.Warningf("Failed to list EndpointSlices for service %s/%s: %v", namespace, serviceName, err)
	} else if len(remainingSlices) == 0 {
		// No more EndpointSlices, delete the Service
		err = i.handler.agent.LocalKubeClient().CoreV1().Services(namespace).Delete(
			context.TODO(), serviceName, metav1.DeleteOptions{})
		if err != nil {
			klog.Warningf("Failed to delete Service %s/%s: %v", namespace, serviceName, err)
		} else {
			klog.Infof("Deleted Service %s/%s (no more remote endpoints)", namespace, serviceName)
		}
	}

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

	// Try to get existing ServiceImport (in local cluster)
	existing, err := i.handler.agent.LocalBrokerClient().BrokerV1alpha1().
		ServiceImports(namespace).Get(context.TODO(), serviceName, metav1.GetOptions{})

	if err == nil {
		// Update existing - need to update spec and status separately
		serviceImport.ResourceVersion = existing.ResourceVersion

		// Update spec
		updated, err := i.handler.agent.LocalBrokerClient().BrokerV1alpha1().
			ServiceImports(namespace).Update(context.TODO(), serviceImport, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update ServiceImport spec: %w", err)
		}

		// Update status
		serviceImport.ResourceVersion = updated.ResourceVersion
		_, err = i.handler.agent.LocalBrokerClient().BrokerV1alpha1().
			ServiceImports(namespace).UpdateStatus(context.TODO(), serviceImport, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update ServiceImport status: %w", err)
		}

		klog.V(4).Infof("Updated ServiceImport %s/%s", namespace, serviceName)
	} else {
		// Create new
		created, err := i.handler.agent.LocalBrokerClient().BrokerV1alpha1().
			ServiceImports(namespace).Create(context.TODO(), serviceImport, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create ServiceImport: %w", err)
		}

		// Update status after creation
		serviceImport.ResourceVersion = created.ResourceVersion
		_, err = i.handler.agent.LocalBrokerClient().BrokerV1alpha1().
			ServiceImports(namespace).UpdateStatus(context.TODO(), serviceImport, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update ServiceImport status after creation: %w", err)
		}

		klog.V(4).Infof("Created ServiceImport %s/%s", namespace, serviceName)
	}

	return nil
}

// ensureService creates or updates a derived Service for the imported service.
// This Service is required for OVN-K's service controller to create load balancer backends.
func (i *Importer) ensureService(namespace, serviceName string, brokerExport *brokerv1alpha1.ServiceExport) error {
	// Convert broker ports to Service ports
	var servicePorts []corev1.ServicePort
	for _, epPort := range brokerExport.Spec.Ports {
		svcPort := corev1.ServicePort{
			Protocol: *epPort.Protocol,
			Port:     *epPort.Port,
		}
		if epPort.Name != nil {
			svcPort.Name = *epPort.Name
		}
		// For imported services, we use the same port as target port
		svcPort.TargetPort = intstr.FromInt(int(*epPort.Port))
		servicePorts = append(servicePorts, svcPort)
	}

	// Create Service without selector (endpoints come from EndpointSlices)
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
			Labels: map[string]string{
				"multicluster.kubernetes.io/service-name":   serviceName,
				"multicluster.kubernetes.io/imported":       "true",
				"networking.k8s.ovn.org/network":            brokerExport.Status.NetworkName,
			},
			Annotations: map[string]string{
				"multicluster.kubernetes.io/source-cluster": brokerExport.Status.ClusterID,
			},
		},
		Spec: corev1.ServiceSpec{
			Type:  corev1.ServiceTypeClusterIP,
			Ports: servicePorts,
			// No selector - endpoints come from manually created EndpointSlices
		},
	}

	// IMPORTANT: Store ClusterSetIP in annotation instead of Service.Spec.ClusterIP
	// The ClusterSetIP is managed separately and OVN will create LB rules for it
	if brokerExport.Status.ClusterSetIP != "" {
		if service.Annotations == nil {
			service.Annotations = make(map[string]string)
		}
		service.Annotations["multicluster.kubernetes.io/clusterset-ip"] = brokerExport.Status.ClusterSetIP
		klog.Infof("Set ClusterSetIP annotation %s for imported service %s/%s",
			brokerExport.Status.ClusterSetIP, namespace, serviceName)
	} else {
		klog.Warningf("No ClusterSetIP allocated for service %s/%s",
			namespace, serviceName)
	}
	// Let Kubernetes assign a regular ClusterIP from the service CIDR
	// service.Spec.ClusterIP remains unset (will be auto-assigned)

	// Try to get existing Service
	existing, err := i.handler.agent.LocalKubeClient().CoreV1().Services(namespace).Get(
		context.TODO(), serviceName, metav1.GetOptions{})

	if err == nil {
		// Update existing Service
		// Preserve ResourceVersion and ClusterIP (don't change ClusterIP once assigned)
		service.ResourceVersion = existing.ResourceVersion
		service.Spec.ClusterIP = existing.Spec.ClusterIP
		service.Spec.ClusterIPs = existing.Spec.ClusterIPs
		_, err = i.handler.agent.LocalKubeClient().CoreV1().Services(namespace).Update(
			context.TODO(), service, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update Service: %w", err)
		}
		klog.V(4).Infof("Updated Service %s/%s for imported service (ClusterSetIP annotation: %s)",
			namespace, serviceName, service.Annotations["multicluster.kubernetes.io/clusterset-ip"])
	} else {
		// Create new Service (Kubernetes will auto-assign ClusterIP)
		_, err = i.handler.agent.LocalKubeClient().CoreV1().Services(namespace).Create(
			context.TODO(), service, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create Service: %w", err)
		}
		klog.Infof("Created Service %s/%s for imported service (ClusterSetIP annotation: %s)",
			namespace, serviceName, service.Annotations["multicluster.kubernetes.io/clusterset-ip"])
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
	// - Annotation: types.UserDefinedNetworkEndpointSliceAnnotation with format cluster_udn_<networkname>
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
				// Format: cluster_udn_<networkname>
				types.UserDefinedNetworkEndpointSliceAnnotation: fmt.Sprintf("cluster_udn_%s", brokerExport.Status.NetworkName),
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
