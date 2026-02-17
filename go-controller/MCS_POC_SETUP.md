# MCS PoC Setup Guide

This guide shows how to run the BGP EVPN PoC with the MCS-enabled OVN-Kubernetes branch for manual testing and debugging.

## Prerequisites

- Docker installed and running
- kind, kubectl installed
- Git

## Repository Setup (Already Complete ✅)

Your local setup:
- **OVN-K MCS Branch**: `/home/yboaron/prj/ovn-kubernetes` (mcs-broker-agent-poc)
- **BGP EVPN PoC**: `/home/yboaron/prj/ovn-bgp-mcn-udn-poc` (svd-datapath)
- **Config**: `config.env` already configured with correct paths

## What's Ready to Test vs TODO

### ✅ Ready to Test Now

1. **BGP EVPN Datapath**
   - Pod-to-pod L3 connectivity on same CUDN across clusters
   - BGP Type-5 routes for pod IPs
   - VXLAN L3VNI transport

2. **MCS Infrastructure**
   - Broker CRDs (ClusterInfo, ServiceExport, ServiceImport)
   - MCS API CRDs (multicluster.x-k8s.io/v1alpha1)
   - RBAC and ServiceAccount setup
   - Broker token generation

3. **MCS Code**
   - Broker/Agent infrastructure
   - MCS Handler (Exporter + Importer with correct CUDN labels)
   - CUDN Provider for network lookups
   - EndpointSlice generation with correct labels/annotations

4. **Services Datapath**
   - Services controller already filters by CUDN
   - OVN Load Balancers scoped to networks
   - EndpointSlice mirroring for CUDN

### 🔧 TODO (Requires Controller Integration)

1. **MCS Controller Integration**
   - Wire MCS handler into ovnkube-cluster-manager
   - Add command-line flags for broker configuration
   - Start broker agent and register MCS handler

2. **Automated Sync** (works once controller runs)
   - ServiceExport → Broker sync
   - Broker → ServiceImport sync
   - ClusterInfo publishing
   - EndpointSlice creation in remote clusters

### 🎯 Testing Strategy

1. **Now**: Deploy BGP EVPN infrastructure, verify datapath, verify MCS CRDs install
2. **Next**: Integrate MCS controller into ovnkube-cluster-manager
3. **Then**: Test full E2E automated sync and service discovery
4. **Finally**: Automate testing in PoC repo

## Quick Start

```bash
# 1. Go to BGP PoC directory
cd /home/yboaron/prj/ovn-bgp-mcn-udn-poc

# 2. Source config (already points to your MCS branch)
source config.env

# 3. Deploy BGP EVPN infrastructure
./deploy-all.sh

# 4. Source cluster info after deployment
source cluster-info.env
```

The `deploy-all.sh` script will automatically:
- Build OVN-K images from your `mcs-broker-agent-poc` branch
- Create two Kind clusters (West and East)
- Install frr-k8s for BGP
- Create UDNs (blue, red)
- Configure BGP EVPN L3VNI
- Verify pod-to-pod connectivity

## Step 1: Verify Current Setup

Before deploying, check that both repos are on the correct branches:

```bash
# Check OVN-K branch
cd /home/yboaron/prj/ovn-kubernetes
git branch --show-current  # Should show: mcs-broker-agent-poc
git log --oneline -3       # Should show MCS commits

# Check BGP PoC branch
cd /home/yboaron/prj/ovn-bgp-mcn-udn-poc
git branch --show-current  # Should show: svd-datapath
```

## Step 2: Deploy BGP EVPN Infrastructure

```bash
cd /home/yboaron/prj/ovn-bgp-mcn-udn-poc
source config.env
./deploy-all.sh
```

**What happens:**
1. Builds OVN-K images from your `mcs-broker-agent-poc` branch
2. Creates two Kind clusters (West and East)
3. Installs frr-k8s for BGP
4. Creates UDNs (blue, red) on both clusters
5. Configures BGP peering between gateways
6. Sets up VXLAN dataplane (L2 + L3VNI)
7. Verifies pod-to-pod L3 connectivity

**Build time:** First run takes ~10-15 minutes to build OVN-K images.

## Step 3: Source Cluster Info

After deployment completes:

```bash
source cluster-info.env
```

This sets environment variables:
- `WEST_CLUSTER_NAME`, `EAST_CLUSTER_NAME`
- `KUBECONFIG_WEST`, `KUBECONFIG_EAST`
- `WEST_GATEWAY_NODE`, `EAST_GATEWAY_NODE`
- `WEST_GATEWAY_IP`, `EAST_GATEWAY_IP`

## Step 4: Verify BGP EVPN Connectivity

Before testing MCS, verify the BGP EVPN infrastructure is working:

```bash
# Check BGP session status
KUBECONFIG=$KUBECONFIG_WEST kubectl exec -n frr-k8s-system \
  $(kubectl get pods -n frr-k8s-system -l app=frr-k8s --field-selector spec.nodeName=$WEST_GATEWAY_NODE -o name) \
  -c frr -- vtysh -c 'show bgp summary'

# Check EVPN Type-5 routes
KUBECONFIG=$KUBECONFIG_WEST kubectl exec -n frr-k8s-system \
  $(kubectl get pods -n frr-k8s-system -l app=frr-k8s --field-selector spec.nodeName=$WEST_GATEWAY_NODE -o name) \
  -c frr -- vtysh -c 'show bgp l2vpn evpn route type prefix'

# Test pod-to-pod connectivity (should already work from deploy-all.sh)
KUBECONFIG=$KUBECONFIG_WEST kubectl exec -n blue test-blue-any-west -- \
  ping -c 3 <east-pod-ip>
```

If this works, you have L3 pod connectivity between clusters. Now we can test MCS layer on top!

## Step 5: Setup MCS Broker (Manual)

**NOTE:** The MCS controller is not yet integrated into ovnkube-cluster-manager. This step sets up the CRDs and RBAC for manual testing. Automated sync will work once controller integration is complete.

### 5.1: Apply Broker CRDs and RBAC

Use West cluster as broker:

```bash
# Set broker context
export BROKER_CONTEXT=kind-west
export BROKER_KUBECONFIG=$KUBECONFIG_WEST

# Apply broker setup
kubectl --kubeconfig=$BROKER_KUBECONFIG apply -f /home/yboaron/prj/ovn-kubernetes/go-controller/deploy/mcs/00-broker-crds.yaml
kubectl --kubeconfig=$BROKER_KUBECONFIG apply -f /home/yboaron/prj/ovn-kubernetes/go-controller/deploy/mcs/10-broker-namespace.yaml
kubectl --kubeconfig=$BROKER_KUBECONFIG apply -f /home/yboaron/prj/ovn-kubernetes/go-controller/deploy/mcs/11-broker-rbac.yaml

# Verify
kubectl --kubeconfig=$BROKER_KUBECONFIG get namespace ovn-kubernetes-broker
kubectl --kubeconfig=$BROKER_KUBECONFIG get crd | grep broker.ovn.org
```

### 5.2: Create Broker Tokens

Create broker access tokens for each member cluster:

```bash
cd /home/yboaron/prj/ovn-kubernetes/go-controller/deploy/mcs

# Create token for West cluster
./create-broker-token.sh west broker-kubeconfig-west.yaml

# Create token for East cluster
./create-broker-token.sh east broker-kubeconfig-east.yaml

# Verify tokens created
ls -l broker-kubeconfig-*.yaml
```

### 5.3: Setup Member Clusters

Apply MCS CRDs and configure broker access:

```bash
# Setup West cluster
./setup-member.sh kind-west broker-kubeconfig-west.yaml

# Setup East cluster
./setup-member.sh kind-east broker-kubeconfig-east.yaml

# Verify MCS CRDs installed
kubectl --kubeconfig=$KUBECONFIG_WEST get crd | grep multicluster
kubectl --kubeconfig=$KUBECONFIG_EAST get crd | grep multicluster
```

## Step 6: Deploy Test Service (Manual Testing)

### 6.1: Create CUDN for Demo Namespace

First, create the `cudn_green` network that the demo namespace will use:

```bash
# Create cudn_green in West cluster
kubectl --kubeconfig=$KUBECONFIG_WEST apply -f - <<EOF
apiVersion: k8s.ovn.org/v1
kind: ClusterUserDefinedNetwork
metadata:
  name: cudn_green
spec:
  namespaceSelector:
    matchLabels:
      tenant: green
  network:
    topology: Layer3
    transport: EVPN
    layer3:
      role: Primary
      subnets:
      - cidr: 10.70.0.0/16
        hostSubnet: /24
    evpn:
      vtep: vtep-external
      ipVRF:
        vni: 10000
        routeTarget: "65000:100"
EOF

# Create cudn_green in East cluster
kubectl --kubeconfig=$KUBECONFIG_EAST apply -f - <<EOF
apiVersion: k8s.ovn.org/v1
kind: ClusterUserDefinedNetwork
metadata:
  name: cudn_green
spec:
  namespaceSelector:
    matchLabels:
      tenant: green
  network:
    topology: Layer3
    transport: EVPN
    layer3:
      role: Primary
      subnets:
      - cidr: 10.71.0.0/16
        hostSubnet: /24
    evpn:
      vtep: vtep-external
      ipVRF:
        vni: 10000
        routeTarget: "65000:100"
EOF

# Verify CUDN created
kubectl --kubeconfig=$KUBECONFIG_WEST get clusteruserdefinednetwork cudn_green
kubectl --kubeconfig=$KUBECONFIG_EAST get clusteruserdefinednetwork cudn_green
```

### 6.2: Export a Service from West Cluster

```bash
# Apply example service in West cluster
kubectl --kubeconfig=$KUBECONFIG_WEST apply -f /home/yboaron/prj/ovn-kubernetes/go-controller/deploy/mcs/examples/nginx-service-export.yaml

# Wait for pod to be ready
kubectl --kubeconfig=$KUBECONFIG_WEST wait --for=condition=Ready pod/nginx -n demo --timeout=60s
```

This creates:
- Namespace `demo` with label `tenant: green`
- Nginx pod with label `app: nginx`
- Service `nginx` on port 80
- ServiceExport `nginx` for multi-cluster discovery

### 6.3: Verify Service Export (What Works Now)

Check that the service and export are created:

```bash
# Check namespace has correct label
kubectl --kubeconfig=$KUBECONFIG_WEST get namespace demo --show-labels

# Check local ServiceExport
kubectl --kubeconfig=$KUBECONFIG_WEST get serviceexport -n demo

# Check service and endpoints
kubectl --kubeconfig=$KUBECONFIG_WEST get service nginx -n demo -o wide
kubectl --kubeconfig=$KUBECONFIG_WEST get endpointslices -n demo -o yaml

# Verify pod got IP from cudn_green subnet (10.70.x.x)
kubectl --kubeconfig=$KUBECONFIG_WEST get pod nginx -n demo -o wide
```

### 6.4: Check Broker Synchronization (TODO - Requires Controller)

**NOTE:** This step requires the MCS controller to be running in ovnkube-cluster-manager (not yet integrated).

Once controller is integrated, you'll be able to verify:

```bash
# Check if broker has received the export
kubectl --kubeconfig=$BROKER_KUBECONFIG get serviceexports.broker.ovn.org -n ovn-kubernetes-broker

# Check ClusterInfo in broker
kubectl --kubeconfig=$BROKER_KUBECONFIG get clusterinfos.broker.ovn.org -n ovn-kubernetes-broker
```

### 6.5: What You Can Verify Now

Even without the controller, you can verify the datapath is ready:

```bash
# 1. Verify CUDN network is working
kubectl --kubeconfig=$KUBECONFIG_WEST get clusteruserdefinednetwork cudn_green -o yaml

# 2. Verify pod got IP from CUDN subnet
NGINX_IP=$(kubectl --kubeconfig=$KUBECONFIG_WEST get pod nginx -n demo -o jsonpath='{.status.podIP}')
echo "Nginx pod IP (should be 10.70.x.x): $NGINX_IP"

# 3. Verify service has endpoints
kubectl --kubeconfig=$KUBECONFIG_WEST get endpoints nginx -n demo -o yaml

# 4. Verify EndpointSlice has correct labels (mirrored for CUDN)
kubectl --kubeconfig=$KUBECONFIG_WEST get endpointslices -n demo -o yaml | grep -A 5 "k8s.ovn.org"

# 5. Test local service access works
kubectl --kubeconfig=$KUBECONFIG_WEST run test-local --rm -it --image=nicolaka/netshoot -n demo -- \
  curl -s http://nginx.demo.svc.cluster.local
```

## Step 7: Test Cross-Cluster Pod Connectivity (BGP EVPN)

Before testing MCS, verify that BGP EVPN provides L3 connectivity between pods on the same CUDN:

```bash
# Deploy test pod in East cluster on cudn_green
kubectl --kubeconfig=$KUBECONFIG_EAST apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: demo
  labels:
    tenant: green
---
apiVersion: v1
kind: Pod
metadata:
  name: test-client
  namespace: demo
spec:
  containers:
  - name: client
    image: nicolaka/netshoot:latest
    command: ["sleep", "infinity"]
EOF

# Wait for pod
kubectl --kubeconfig=$KUBECONFIG_EAST wait --for=condition=Ready pod/test-client -n demo --timeout=60s

# Get nginx IP from West cluster
NGINX_IP=$(kubectl --kubeconfig=$KUBECONFIG_WEST get pod nginx -n demo -o jsonpath='{.status.podIP}')
echo "Nginx pod IP in West: $NGINX_IP"

# Test direct pod-to-pod connectivity via BGP EVPN
kubectl --kubeconfig=$KUBECONFIG_EAST exec -n demo test-client -- ping -c 3 $NGINX_IP
kubectl --kubeconfig=$KUBECONFIG_EAST exec -n demo test-client -- curl -s http://$NGINX_IP

# This should work! BGP EVPN provides L3 reachability for pods on same CUDN.
```

If this works, your BGP EVPN datapath is ready for MCS layer on top!

## Step 8: Debug and Verify

### Check OVN Load Balancer

Verify that the OVN load balancer includes remote endpoints:

```bash
# Get ovnkube-master pod in East cluster
MASTER_POD=$(KUBECONFIG=$KUBECONFIG_EAST kubectl get pod -n ovn-kubernetes \
  -l app=ovnkube-master --no-headers -o custom-columns=":metadata.name" | head -1)

# Check load balancer for nginx service
KUBECONFIG=$KUBECONFIG_EAST kubectl exec -n ovn-kubernetes $MASTER_POD -- \
  ovn-nbctl list load_balancer | grep -A 10 nginx
```

### Check CUDN Assignment

Verify namespaces are assigned to correct CUDN:

```bash
# Check namespace labels
KUBECONFIG=$KUBECONFIG_WEST kubectl get namespace demo --show-labels

# Check CUDN configuration
KUBECONFIG=$KUBECONFIG_WEST kubectl get clusteruserdefinednetwork cudn_green -o yaml
```

### Check EndpointSlice Labels

Verify imported EndpointSlices have correct labels/annotations:

```bash
KUBECONFIG=$KUBECONFIG_EAST kubectl get endpointslices -n demo -o yaml | \
  grep -A 5 -B 5 "k8s.ovn.org"
```

Expected labels:
- `k8s.ovn.org/service-name: nginx`
- `multicluster.kubernetes.io/service-name: nginx`
- `multicluster.kubernetes.io/source-cluster: west`

Expected annotations:
- `k8s.ovn.org/endpointslice-network: cudn_green`

## Step 9: Check OVN-Kubernetes Logs

Check that your MCS-enabled OVN-K build is running:

```bash
# Check ovnkube-cluster-manager version
kubectl --kubeconfig=$KUBECONFIG_WEST get pod -n ovn-kubernetes -l name=ovnkube-cluster-manager

# Check logs (when controller is integrated, MCS logs will appear here)
kubectl --kubeconfig=$KUBECONFIG_WEST logs -n ovn-kubernetes \
  -l name=ovnkube-cluster-manager --tail=100

# Verify image is from your local build
kubectl --kubeconfig=$KUBECONFIG_WEST get pod -n ovn-kubernetes \
  -l name=ovnkube-cluster-manager -o jsonpath='{.items[0].spec.containers[0].image}'
```

### Common Issues

1. **ServiceExport not syncing to broker**
   - Check MCS controller is running
   - Check broker RBAC permissions
   - Check network connectivity to broker

2. **ServiceImport not created in remote cluster**
   - Check broker has ServiceExport
   - Check ClusterInfo is present
   - Check CUDN name matches across clusters

3. **Connectivity fails to ClusterSetIP**
   - Check EndpointSlice has correct labels/annotations
   - Check OVN load balancer includes remote endpoints
   - Check BGP EVPN routes are advertised
   - Check pod IPs are reachable (ping remote pod IP directly)

## Summary: What You Can Verify Today

After running through this guide, you can verify:

1. ✅ **BGP EVPN works**: Direct pod-to-pod connectivity on cudn_green across clusters
2. ✅ **MCS CRDs installed**: Broker and member cluster CRDs are applied
3. ✅ **CUDN isolation**: Pods get IPs from correct CUDN subnets
4. ✅ **Code is ready**: MCS handler, exporter, importer all implemented
5. ✅ **Services datapath ready**: Services controller has CUDN filtering logic

What's **NOT** working yet (requires controller integration):
- ❌ Automatic ServiceExport → Broker sync
- ❌ Automatic ServiceImport creation
- ❌ ClusterInfo publishing to broker
- ❌ Cross-cluster service discovery via ClusterSetIP

## Next Steps

### Immediate: Deploy and Verify Infrastructure

```bash
cd /home/yboaron/prj/ovn-bgp-mcn-udn-poc
source config.env
./deploy-all.sh
source cluster-info.env

# Follow steps 5-7 above to:
# - Setup MCS broker
# - Deploy test service
# - Verify BGP EVPN connectivity
```

### Next: Controller Integration

Wire the MCS handler into ovnkube-cluster-manager:

1. Add MCS controller manager code
2. Initialize broker agent in cluster-manager main
3. Register MCS handler
4. Add command-line flags for broker config
5. Test automated sync

### Finally: Full E2E Testing

Once controller integration is complete:

1. Test ServiceExport → Broker sync
2. Verify ServiceImport creation in remote cluster
3. Test connectivity via ClusterSetIP
4. Verify OVN LB includes remote endpoints
5. Automate testing in BGP PoC repo

## Cleanup

To tear down the PoC environment:

```bash
cd /home/yboaron/prj/ovn-bgp-mcn-udn-poc
./cleanup-all.sh
```

## Notes

- CUDN `cudn_green` must exist with same name and RouteTarget in both clusters
- BGP EVPN provides L3 connectivity for pod IPs (underlay)
- MCS provides service-level discovery with ClusterSetIP (overlay)
- Services are isolated by CUDN network (tenant isolation)
