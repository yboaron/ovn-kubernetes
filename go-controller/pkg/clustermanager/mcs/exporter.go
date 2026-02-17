package mcs

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	brokerv1alpha1 "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/ovnbroker/v1alpha1"
	mcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

// Exporter handles exporting local services to the broker.
type Exporter struct {
	handler *Handler
}

// NewExporter creates a new Exporter.
func NewExporter(handler *Handler) *Exporter {
	return &Exporter{
		handler: handler,
	}
}

// ExportService exports a local service to the broker.
func (e *Exporter) ExportService(export *mcsv1alpha1.ServiceExport) error {
	klog.V(4).Infof("Exporting service %s/%s to broker", export.Namespace, export.Name)

	// 1. Get the corresponding Service
	svc, err := e.handler.getService(export.Namespace, export.Name)
	if err != nil {
		klog.Errorf("Failed to get service for export %s/%s: %v", export.Namespace, export.Name, err)
		return err
	}

	// 2. Determine CUDN
	cudnName, err := e.handler.cudnProvider.GetCUDNForNamespace(export.Namespace)
	if err != nil {
		klog.Errorf("Failed to get CUDN for namespace %s: %v", export.Namespace, err)
		return err
	}

	if cudnName == "" {
		klog.Warningf("Service %s/%s is not on a CUDN network, skipping export", export.Namespace, export.Name)
		return fmt.Errorf("service not on CUDN network")
	}

	klog.V(4).Infof("Service %s/%s is on CUDN network: %s", export.Namespace, export.Name, cudnName)

	// 3. Collect endpoints (network-specific IPs)
	endpoints, err := e.collectEndpoints(svc, cudnName)
	if err != nil {
		klog.Errorf("Failed to collect endpoints for service %s/%s: %v", export.Namespace, export.Name, err)
		return err
	}

	klog.V(4).Infof("Collected %d endpoints for service %s/%s on network %s",
		len(endpoints), export.Namespace, export.Name, cudnName)

	// 4. Convert ports
	ports := e.convertServicePorts(svc)

	// 5. Create broker ServiceExport
	// Name format: <cluster>-<namespace>-<service>
	brokerExportName := fmt.Sprintf("%s-%s-%s",
		e.handler.agent.ClusterID(), export.Namespace, export.Name)

	brokerExport := &brokerv1alpha1.ServiceExport{
		ObjectMeta: metav1.ObjectMeta{
			Name: brokerExportName,
			Labels: map[string]string{
				"multicluster.kubernetes.io/source-cluster":   e.handler.agent.ClusterID(),
				"multicluster.kubernetes.io/source-namespace": export.Namespace,
				"multicluster.kubernetes.io/service-name":     export.Name,
				"networking.k8s.ovn.org/network":              cudnName,
			},
		},
		Spec: brokerv1alpha1.ServiceExportSpec{
			ServiceName:      export.Name,
			ServiceNamespace: export.Namespace,
			Ports:            ports,
		},
		Status: brokerv1alpha1.ServiceExportStatus{
			ClusterID:   e.handler.agent.ClusterID(),
			NetworkName: cudnName,
			Endpoints:   endpoints,
		},
	}

	// 6. Publish to broker
	if err := e.handler.agent.BrokerClient().CreateOrUpdateServiceExport(context.TODO(), brokerExport); err != nil {
		klog.Errorf("Failed to publish ServiceExport to broker: %v", err)
		return err
	}

	klog.Infof("Successfully exported service %s/%s to broker (network: %s, endpoints: %d)",
		export.Namespace, export.Name, cudnName, len(endpoints))

	return nil
}

// UnexportService removes a service export from the broker.
func (e *Exporter) UnexportService(export *mcsv1alpha1.ServiceExport) error {
	klog.V(4).Infof("Unexporting service %s/%s from broker", export.Namespace, export.Name)

	brokerExportName := fmt.Sprintf("%s-%s-%s",
		e.handler.agent.ClusterID(), export.Namespace, export.Name)

	if err := e.handler.agent.BrokerClient().DeleteServiceExport(context.TODO(), brokerExportName); err != nil {
		klog.Errorf("Failed to delete ServiceExport from broker: %v", err)
		return err
	}

	klog.Infof("Successfully unexported service %s/%s from broker", export.Namespace, export.Name)
	return nil
}

// collectEndpoints gathers CUDN-specific pod IPs from EndpointSlices.
func (e *Exporter) collectEndpoints(svc *metav1.Object, cudnName string) ([]brokerv1alpha1.EndpointInfo, error) {
	// List all EndpointSlices for this service
	slices, err := e.handler.listEndpointSlices(svc.GetNamespace(), svc.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to list endpoint slices: %w", err)
	}

	var endpoints []brokerv1alpha1.EndpointInfo

	for _, slice := range slices {
		// Only collect from mirrored EndpointSlices for this CUDN
		// Mirrored slices have the network label
		networkLabel, hasLabel := slice.Labels["networking.k8s.ovn.org/network"]
		if !hasLabel || networkLabel != cudnName {
			klog.V(5).Infof("Skipping EndpointSlice %s/%s - not for network %s (has label: %v, label value: %s)",
				slice.Namespace, slice.Name, cudnName, hasLabel, networkLabel)
			continue
		}

		// Collect ready endpoints
		for _, ep := range slice.Endpoints {
			ready := true
			if ep.Conditions.Ready != nil {
				ready = *ep.Conditions.Ready
			}

			if !ready {
				continue
			}

			// Create endpoint info for each address
			for _, addr := range ep.Addresses {
				epInfo := brokerv1alpha1.EndpointInfo{
					Address: addr,
					Ready:   true,
				}

				if ep.Hostname != nil {
					epInfo.Hostname = *ep.Hostname
				}

				if ep.NodeName != nil {
					epInfo.NodeName = *ep.NodeName
				}

				if ep.Zone != nil {
					epInfo.Zone = *ep.Zone
				}

				endpoints = append(endpoints, epInfo)
			}
		}
	}

	return endpoints, nil
}

// convertServicePorts converts corev1.ServicePort to discoveryv1.EndpointPort.
func (e *Exporter) convertServicePorts(svc *metav1.Object) []discoveryv1.EndpointPort {
	// Note: In a real implementation, we'd need the actual Service object
	// For now, returning empty array - will be filled by actual service controller
	return []discoveryv1.EndpointPort{}
}
