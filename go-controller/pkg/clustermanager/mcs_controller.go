package clustermanager

import (
	"context"
	"fmt"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/clustermanager/broker"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/clustermanager/mcs"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/config"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/factory"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/util"
)

// MCSController manages the Multi-Cluster Services controller
type MCSController struct {
	agent *broker.Agent
}

// NewMCSController creates a new MCS controller
func NewMCSController(
	ovnClient *util.OVNClusterManagerClientset,
	wf *factory.WatchFactory,
) (*MCSController, error) {
	if !config.MCS.Enabled {
		klog.Info("MCS controller is disabled")
		return nil, nil
	}

	klog.Infof("Creating MCS controller with broker: %s, cluster: %s",
		config.MCS.BrokerKubeconfig, config.MCS.ClusterID)

	// Load broker kubeconfig from file
	brokerRestConfig, err := clientcmd.BuildConfigFromFlags("", config.MCS.BrokerKubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to load broker kubeconfig from %s: %w", config.MCS.BrokerKubeconfig, err)
	}

	// Get local cluster rest config (in-cluster config)
	localRestConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load in-cluster config: %w", err)
	}

	// Create broker agent configuration
	brokerConfig := &broker.AgentConfig{
		ClusterID:         config.MCS.ClusterID,
		LocalKubeConfig:   localRestConfig,
		BrokerKubeConfig:  brokerRestConfig,
		LocalKubeClient:   ovnClient.KubeClient,
		BrokerNamespace:   config.MCS.BrokerNamespace,
		ClusterSetIPCIDR:  config.MCS.ClusterSetIPCIDR,
	}

	// Create broker agent
	agent, err := broker.NewAgent(brokerConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create broker agent: %w", err)
	}

	// Create MCS handler configuration
	mcsHandlerConfig := &mcs.HandlerConfig{
		Agent:               agent,
		ServiceLister:       wf.ServiceCoreInformer().Lister(),
		EndpointSliceLister: wf.EndpointSliceCoreInformer().Lister(),
		NamespaceLister:     wf.NamespaceInformer().Lister(),
		CUDNLister:          wf.ClusterUserDefinedNetworkInformer().Lister(),
	}

	// Create MCS handler
	mcsHandler := mcs.NewHandler(mcsHandlerConfig)

	// Register MCS handler with broker agent
	agent.RegisterHandler(mcsHandler)

	return &MCSController{
		agent: agent,
	}, nil
}

// Start starts the MCS controller
func (mc *MCSController) Start(ctx context.Context) error {
	if mc == nil || mc.agent == nil {
		klog.V(4).Info("MCS controller is nil or disabled, skipping start")
		return nil
	}

	klog.Info("Starting MCS controller")
	if err := mc.agent.Start(ctx); err != nil {
		return fmt.Errorf("failed to start MCS broker agent: %w", err)
	}

	klog.Info("MCS controller started successfully")
	return nil
}

// Stop stops the MCS controller
func (mc *MCSController) Stop() {
	if mc == nil || mc.agent == nil {
		klog.V(4).Info("MCS controller is nil or disabled, skipping stop")
		return
	}

	klog.Info("Stopping MCS controller")
	mc.agent.Stop()
}
