# Multi-Cluster Services PoC Architecture

## Two-Piece PoC Structure

### Piece 1: OVN-Kubernetes Core Code (This Repo)
Core MCS functionality integrated into ovn-kubernetes

### Piece 2: PoC Demo Repository
End-to-end demo based on BGP EVPN PoC with automated testing

---

## Piece 1: OVN-Kubernetes Core Code

### Status: ~95% Complete

### What's Implemented ✅

1. **Broker/Agent Control Plane**
   - Generic broker infrastructure (broker.Agent, broker.Client, broker.Syncer)
   - Pluggable handler pattern
   - Informer-based resource watching
   - Work queue with retries

2. **MCS Handler**
   - Service export logic (mcs.Exporter)
   - Service import logic (mcs.Importer)
   - CUDN provider for network lookups
   - ClusterInfo provider

3. **Broker CRDs**
   - ClusterInfo (cluster metadata + CUDN networks)
   - ServiceExport (broker format with endpoints)
   - ServiceImport (broker format with ClusterSetIP)

4. **Deployment Manifests**
   - Broker CRDs, namespace, RBAC
   - Member cluster MCS API CRDs, RBAC
   - Automated setup scripts

5. **Code Generation**
   - Clientsets, listers, informers
   - Deepcopy functions
   - Apply configurations

### What Needs Fixing 🔧

#### Critical Fix: EndpointSlice Labels for CUDN Services

**Problem:** Services controller expects different labels for CUDN services

**Current Implementation** (importer.go:165-169):
```go
slice := &discoveryv1.EndpointSlice{
    ObjectMeta: metav1.ObjectMeta{
        Labels: map[string]string{
            discoveryv1.LabelServiceName: serviceName,  // ❌ Wrong for CUDN
            "networking.k8s.ovn.org/network": brokerExport.Status.NetworkName,  // ❌ Should be annotation
        },
    },
}
```

**Required for CUDN Services** (based on services controller analysis):
```go
slice := &discoveryv1.EndpointSlice{
    ObjectMeta: metav1.ObjectMeta{
        Labels: map[string]string{
            types.LabelUserDefinedServiceName: serviceName,  // ✅ "k8s.ovn.org/service-name"
            discoveryv1.LabelManagedBy: "ovn-k8s-mcs-controller",
            "multicluster.kubernetes.io/service-name": serviceName,
            "multicluster.kubernetes.io/source-cluster": sourceCluster,
        },
        Annotations: map[string]string{
            types.UserDefinedNetworkEndpointSliceAnnotation: brokerExport.Status.NetworkName,  // ✅ "k8s.ovn.org/endpointslice-network"
        },
    },
}
```

**Fix Location:** `go-controller/pkg/clustermanager/mcs/importer.go:160-175`

**Constants Used:**
- File: `go-controller/pkg/types/const.go`
- `types.LabelUserDefinedServiceName = "k8s.ovn.org/service-name"`
- `types.UserDefinedNetworkEndpointSliceAnnotation = "k8s.ovn.org/endpointslice-network"`

**Services Controller Filtering Logic:**
- File: `go-controller/pkg/util/util.go` (functions: `IsEndpointSliceForNetwork`)
- For CUDN: Looks for EndpointSlices with `types.UserDefinedNetworkEndpointSliceAnnotation` **annotation** (not label)
- Uses `types.LabelUserDefinedServiceName` **label** for service matching

### What's Already Working (No Changes Needed) ✅

**Services Controller Datapath** - Analysis confirms:

1. **EndpointSlice Discovery**
   - Already filters by `ovn.org/user-defined-service-name` label
   - Already checks `ovn.org/user-defined-network` annotation
   - Will automatically find MCS-imported slices (once labels fixed)

2. **OVN Load Balancer Creation**
   - Already includes ALL EndpointSlices matching service name
   - No filtering by EndpointSlice creator
   - Remote endpoints will be added automatically

3. **Network Isolation**
   - LBs already attach only to network-scoped switches
   - Switch names use network prefix (e.g., `cudn_green_node1`)
   - Complete isolation at dataplane level

4. **Multi-Cluster Pod IPs**
   - Remote pod IPs reachable via BGP EVPN (from PoC repo setup)
   - OVN will route to remote IPs normally
   - No special handling needed

**Conclusion:** Once EndpointSlice labels are fixed, existing services controller will handle multi-cluster services automatically!

---

## Piece 2: PoC Demo Repository

### Base: BGP EVPN PoC + MCS Layer

### Repository: `ovn-bgp-mcn-udn-poc` (new branch: `mcs-poc`)

### Layer 1: BGP EVPN Setup (Already Exists ✅)

**From:** https://github.com/yboaron/ovn-bgp-mcn-udn-poc/tree/svd-datapath

**What it provides:**
- Two Kind clusters (West, East)
- CUDN networks (cudn_green, cudn_red)
- BGP EVPN configuration
- Pod-to-pod L3 connectivity between clusters
- FRR integration
- Single VXLAN Device (SVD) datapath

**Verification:**
```bash
# BGP session established
kubectl exec -n kube-system frr-xxx -- vtysh -c "show bgp summary"

# EVPN routes advertised
kubectl exec -n kube-system frr-xxx -- vtysh -c "show bgp l2vpn evpn"

# Pod-to-pod connectivity
kubectl exec -n demo pod-west -- ping <pod-east-ip>
```

### Layer 2: MCS Addition (To Be Added 🔧)

**New Scripts for PoC Repo:**

#### 1. `scripts/7-setup-broker.sh`
```bash
#!/bin/bash
# Setup broker cluster for MCS

set -e
source config.env

echo "=== Setting up MCS Broker ==="

# Create broker cluster (can reuse West or East, or create separate)
if [ "$BROKER_MODE" = "separate" ]; then
    echo "Creating separate broker cluster..."
    kind create cluster --name kind-broker
    BROKER_CONTEXT="kind-broker"
else
    echo "Using West cluster as broker..."
    BROKER_CONTEXT="kind-$CLUSTER_NAME_WEST"
fi

# Clone ovn-kubernetes repo (or mount from local)
if [ ! -d "ovn-kubernetes" ]; then
    git clone https://github.com/ovn-org/ovn-kubernetes.git
    cd ovn-kubernetes
    git checkout mcs-broker-agent-poc  # Your branch
    cd ..
fi

# Apply broker CRDs and setup
kubectl --context $BROKER_CONTEXT apply -f ovn-kubernetes/go-controller/deploy/mcs/00-broker-crds.yaml
kubectl --context $BROKER_CONTEXT apply -f ovn-kubernetes/go-controller/deploy/mcs/10-broker-namespace.yaml
kubectl --context $BROKER_CONTEXT apply -f ovn-kubernetes/go-controller/deploy/mcs/11-broker-rbac.yaml

echo "✅ Broker setup complete"
```

#### 2. `scripts/8-setup-mcs-members.sh`
```bash
#!/bin/bash
# Setup MCS in member clusters

set -e
source config.env

echo "=== Setting up MCS in Member Clusters ==="

# Create broker tokens
for cluster in $CLUSTER_NAME_WEST $CLUSTER_NAME_EAST; do
    echo "Creating broker token for $cluster..."

    cd ovn-kubernetes/go-controller/deploy/mcs
    ./create-broker-token.sh $cluster broker-kubeconfig-${cluster}.yaml

    # Setup member cluster
    ./setup-member.sh kind-${cluster} broker-kubeconfig-${cluster}.yaml
done

echo "✅ MCS member setup complete"
```

#### 3. `scripts/9-deploy-mcs-test-service.sh`
```bash
#!/bin/bash
# Deploy test service and export it

set -e
source config.env

CLUSTER_CONTEXT="kind-$CLUSTER_NAME_WEST"
NETWORK="cudn_green"  # or from config
NAMESPACE="demo"

echo "=== Deploying Test Service on $CLUSTER_CONTEXT ==="

# Create namespace with CUDN label
kubectl --context $CLUSTER_CONTEXT apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: $NAMESPACE
  labels:
    tenant: green  # Matches CUDN selector
EOF

# Deploy nginx
kubectl --context $CLUSTER_CONTEXT apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: nginx
  namespace: $NAMESPACE
  labels:
    app: nginx
spec:
  containers:
  - name: nginx
    image: nginx:latest
    ports:
    - containerPort: 80
---
apiVersion: v1
kind: Service
metadata:
  name: nginx
  namespace: $NAMESPACE
spec:
  selector:
    app: nginx
  ports:
  - port: 80
    targetPort: 80
---
apiVersion: multicluster.x-k8s.io/v1alpha1
kind: ServiceExport
metadata:
  name: nginx
  namespace: $NAMESPACE
EOF

echo "✅ Service deployed and exported"
```

#### 4. `scripts/10-verify-mcs-sync.sh`
```bash
#!/bin/bash
# Verify MCS synchronization

set -e
source config.env

WEST_CONTEXT="kind-$CLUSTER_NAME_WEST"
EAST_CONTEXT="kind-$CLUSTER_NAME_EAST"
BROKER_CONTEXT="${BROKER_CONTEXT:-$WEST_CONTEXT}"
NAMESPACE="demo"

echo "=== Verifying MCS Synchronization ==="

# 1. Check ServiceExport in West cluster
echo "1. ServiceExport in West cluster:"
kubectl --context $WEST_CONTEXT get serviceexport -n $NAMESPACE

# 2. Check broker has ServiceExport
echo "2. ServiceExport in Broker:"
kubectl --context $BROKER_CONTEXT get serviceexports.broker.ovn.org \
    -n ovn-kubernetes-broker -l multicluster.kubernetes.io/source-cluster=$CLUSTER_NAME_WEST

# 3. Check ClusterInfo in broker
echo "3. ClusterInfo in Broker:"
kubectl --context $BROKER_CONTEXT get clusterinfos.broker.ovn.org

# 4. Check ServiceImport created in East
echo "4. ServiceImport in East cluster:"
kubectl --context $EAST_CONTEXT get serviceimports.broker.ovn.org -n $NAMESPACE

# 5. Check EndpointSlice in East
echo "5. EndpointSlice in East (remote endpoints):"
kubectl --context $EAST_CONTEXT get endpointslices -n $NAMESPACE \
    -l multicluster.kubernetes.io/source-cluster=$CLUSTER_NAME_WEST

# 6. Get ClusterSetIP
echo "6. ClusterSetIP:"
CLUSTERSETIP=$(kubectl --context $EAST_CONTEXT get serviceimport nginx -n $NAMESPACE \
    -o jsonpath='{.spec.ips[0]}' 2>/dev/null || echo "Not allocated yet")
echo "ClusterSetIP: $CLUSTERSETIP"

# 7. Check OVN Load Balancer
echo "7. OVN Load Balancer configuration:"
MASTER_POD=$(kubectl --context $EAST_CONTEXT get pod -n ovn-kubernetes \
    -l app=ovnkube-master --no-headers -o custom-columns=":metadata.name" | head -1)

if [ -n "$MASTER_POD" ]; then
    echo "Checking LB for service nginx in network cudn_green..."
    kubectl --context $EAST_CONTEXT exec -n ovn-kubernetes $MASTER_POD -- \
        ovn-nbctl list load_balancer | grep -A 10 nginx || echo "LB not found yet"
fi

echo ""
echo "✅ Verification complete"
```

#### 5. `scripts/11-test-mcs-connectivity.sh`
```bash
#!/bin/bash
# Test multi-cluster service connectivity

set -e
source config.env

EAST_CONTEXT="kind-$CLUSTER_NAME_EAST"
NAMESPACE="demo"

echo "=== Testing Multi-Cluster Service Connectivity ==="

# Deploy test client in East cluster
echo "1. Deploying test client in East cluster..."
kubectl --context $EAST_CONTEXT apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: test-client
  namespace: $NAMESPACE
  labels:
    app: test-client
spec:
  containers:
  - name: client
    image: nicolaka/netshoot:latest
    command: ["sleep", "infinity"]
EOF

# Wait for pod to be ready
kubectl --context $EAST_CONTEXT wait --for=condition=Ready \
    pod/test-client -n $NAMESPACE --timeout=60s

# Get ClusterSetIP
CLUSTERSETIP=$(kubectl --context $EAST_CONTEXT get serviceimport nginx -n $NAMESPACE \
    -o jsonpath='{.spec.ips[0]}')

if [ -z "$CLUSTERSETIP" ]; then
    echo "ERROR: ClusterSetIP not found"
    exit 1
fi

echo "2. ClusterSetIP: $CLUSTERSETIP"

# Test connectivity
echo "3. Testing connectivity from East to West service..."
kubectl --context $EAST_CONTEXT exec -n $NAMESPACE test-client -- \
    curl -s --max-time 5 http://$CLUSTERSETIP | grep "Welcome to nginx"

if [ $? -eq 0 ]; then
    echo "✅ SUCCESS: Multi-cluster service connectivity working!"
else
    echo "❌ FAILED: Could not reach remote service"
    exit 1
fi

# Get endpoint details
echo "4. Endpoint details:"
kubectl --context $EAST_CONTEXT get endpointslices -n $NAMESPACE \
    -l multicluster.kubernetes.io/service-name=nginx -o yaml | \
    grep -A 5 "addresses:"

echo ""
echo "✅ MCS connectivity test complete"
```

#### 6. `test-full-mcs.sh` (Master Test Script)
```bash
#!/bin/bash
# Full MCS PoC test - runs all steps

set -e

echo "=========================================="
echo "  OVN-K Multi-Cluster Services PoC"
echo "=========================================="
echo ""

# Layer 1: BGP EVPN Setup (existing)
echo "Layer 1: Setting up BGP EVPN connectivity..."
./deploy-all.sh  # Existing script from BGP PoC

# Layer 2: MCS Setup
echo ""
echo "Layer 2: Setting up Multi-Cluster Services..."

./scripts/7-setup-broker.sh
./scripts/8-setup-mcs-members.sh

# Wait for MCS controllers to start
sleep 10

# Deploy and test
./scripts/9-deploy-mcs-test-service.sh

# Wait for sync
echo "Waiting for service sync..."
sleep 15

./scripts/10-verify-mcs-sync.sh
./scripts/11-test-mcs-connectivity.sh

echo ""
echo "=========================================="
echo "  ✅ MCS PoC Complete!"
echo "=========================================="
```

### Config Addition

Update `config.env.sample`:
```bash
# ... existing BGP EVPN config ...

# MCS Configuration
BROKER_MODE="${BROKER_MODE:-reuse}"  # "reuse" or "separate"
MCS_ENABLED="${MCS_ENABLED:-true}"

# OVN-K source (for MCS manifests)
OVNK_REPO_PATH="${OVNK_REPO_PATH:-../ovn-kubernetes}"
```

---

## Integration Summary

### Piece 1 (OVN-K): Remaining Work

1. **Fix EndpointSlice Labels** (1 file change)
   - File: `go-controller/pkg/clustermanager/mcs/importer.go`
   - Change labels to use `ovn.org/user-defined-service-name`
   - Move network to annotation

2. **Wire into ovnkube-cluster-manager** (TODO - separate task)
   - Create MCS controller manager
   - Initialize broker agent
   - Register MCS handler
   - Add command-line flags

### Piece 2 (PoC Repo): New Work

1. **Create mcs-poc branch** from svd-datapath
2. **Add 5 new scripts** (7-11)
3. **Update config.env**
4. **Add test-full-mcs.sh**
5. **Update README** with MCS layer docs

### Testing Flow

```
1. Clone PoC repo (mcs-poc branch)
2. Configure config.env
3. Run: ./test-full-mcs.sh
   ↓
4. Validates:
   - BGP EVPN connectivity ✅
   - Service export to broker ✅
   - Service import to remote cluster ✅
   - EndpointSlice creation ✅
   - OVN LB configuration ✅
   - Cross-cluster connectivity ✅
```

---

## Next Steps

1. Fix EndpointSlice labels in ovn-k (5 min)
2. Create PoC repo branch and scripts (1-2 hours)
3. Test end-to-end (verify all pieces work together)
4. Document findings
5. Integration into ovnkube-cluster-manager (separate effort)
