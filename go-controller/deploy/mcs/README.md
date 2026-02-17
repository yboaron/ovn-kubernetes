# OVN-Kubernetes Multi-Cluster Services (MCS) Deployment

This directory contains deployment manifests for OVN-Kubernetes Multi-Cluster Services with CUDN support.

## Architecture

```
┌─────────────────────────────────────────────────────┐
│              Broker Cluster                          │
│  • Stores ClusterInfo from all clusters             │
│  • Stores ServiceExport from all clusters           │
│  • Central coordination point                       │
└─────────────────────────────────────────────────────┘
         ▲                           ▲
         │                           │
    ┌────┴──────────┐           ┌────┴──────────┐
    │               │           │               │
┌───▼─────────┐  ┌──▼─────────┐
│ Cluster A   │  │ Cluster B  │
│             │  │            │
│ MCS Agent   │  │ MCS Agent  │
│ • Export    │  │ • Export   │
│ • Import    │  │ • Import   │
└─────────────┘  └────────────┘
```

## Components

### Broker Cluster Resources

1. **Broker CRDs** (`00-broker-crds.yaml`)
   - `ClusterInfo` - Cluster metadata
   - `ServiceExport` - Exported services (broker format)
   - `ServiceImport` - Imported services (broker format)

2. **Broker Namespace** (`10-broker-namespace.yaml`)
   - Namespace: `ovn-kubernetes-broker`
   - ServiceAccount: `ovn-mcs-broker-agent`

3. **Broker RBAC** (`11-broker-rbac.yaml`)
   - ClusterRole and binding for broker access
   - Allows member clusters to read/write broker resources

### Member Cluster Resources

1. **MCS API CRDs** (`01-mcs-api-crds.yaml`)
   - `ServiceExport` (multicluster.x-k8s.io) - User-facing export API
   - `ServiceImport` (multicluster.x-k8s.io) - User-facing import API

2. **Member RBAC** (`20-member-rbac.yaml`)
   - ServiceAccount: `ovn-mcs-controller`
   - ClusterRole for local resource access

## Deployment Steps

### 1. Setup Broker Cluster

```bash
# Set broker cluster context
export BROKER_CONTEXT=kind-broker

# Install broker CRDs
kubectl --context $BROKER_CONTEXT apply -f 00-broker-crds.yaml

# Create broker namespace and RBAC
kubectl --context $BROKER_CONTEXT apply -f 10-broker-namespace.yaml
kubectl --context $BROKER_CONTEXT apply -f 11-broker-rbac.yaml
```

### 2. Create Broker Access Tokens

Generate kubeconfig for member clusters to access broker:

```bash
# Create a token for cluster-west
kubectl --context $BROKER_CONTEXT create token ovn-mcs-broker-agent \
  -n ovn-kubernetes-broker \
  --duration=87600h > broker-token-west.txt

# Create a token for cluster-east
kubectl --context $BROKER_CONTEXT create token ovn-mcs-broker-agent \
  -n ovn-kubernetes-broker \
  --duration=87600h > broker-token-east.txt

# Get broker API server URL
BROKER_API=$(kubectl --context $BROKER_CONTEXT config view --minify -o jsonpath='{.clusters[0].cluster.server}')
echo "Broker API: $BROKER_API"

# Create kubeconfig for member clusters
cat > broker-kubeconfig.yaml <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: $BROKER_API
    insecure-skip-tls-verify: true
  name: broker
contexts:
- context:
    cluster: broker
    user: broker-agent
  name: broker
current-context: broker
users:
- name: broker-agent
  user:
    token: $(cat broker-token-west.txt)
EOF
```

### 3. Setup Member Clusters

For each member cluster (repeat for cluster-west and cluster-east):

```bash
# Set member cluster context
export MEMBER_CONTEXT=kind-west  # or kind-east

# Install MCS API CRDs
kubectl --context $MEMBER_CONTEXT apply -f 01-mcs-api-crds.yaml

# Install member RBAC
kubectl --context $MEMBER_CONTEXT apply -f 20-member-rbac.yaml

# Create broker kubeconfig secret
kubectl --context $MEMBER_CONTEXT create secret generic ovn-mcs-broker-config \
  -n ovn-kubernetes \
  --from-file=kubeconfig=broker-kubeconfig.yaml
```

### 4. Configure MCS Controller

The MCS controller is integrated into ovnkube-cluster-manager. Configuration:

```yaml
# ConfigMap for MCS controller
apiVersion: v1
kind: ConfigMap
metadata:
  name: ovn-mcs-config
  namespace: ovn-kubernetes
data:
  cluster-id: "cluster-west"  # Unique cluster identifier
  broker-namespace: "ovn-kubernetes-broker"
  service-cidr: "10.96.0.0/12"
  cluster-cidr: "10.244.0.0/16"
```

Add to ovnkube-cluster-manager deployment:
- Mount broker kubeconfig secret
- Add MCS controller configuration
- Enable MCS feature flag

## Usage

### Export a Service

```bash
# Deploy a service on CUDN network
kubectl apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: backend
  labels:
    tenant: green
---
apiVersion: v1
kind: Service
metadata:
  name: payment-api
  namespace: backend
spec:
  selector:
    app: payment
  ports:
  - port: 8080
    targetPort: 8080
---
# Export the service
apiVersion: multicluster.x-k8s.io/v1alpha1
kind: ServiceExport
metadata:
  name: payment-api
  namespace: backend
EOF
```

### Verify Export

```bash
# Check local ServiceExport status
kubectl get serviceexport payment-api -n backend -o yaml

# Check broker (on broker cluster)
kubectl --context $BROKER_CONTEXT get serviceexports -n ovn-kubernetes-broker

# Check ClusterInfo (on broker cluster)
kubectl --context $BROKER_CONTEXT get clusterinfos
```

### Check Imported Services

```bash
# On another cluster, check for imported services
kubectl get serviceimports -n backend

# Check EndpointSlices
kubectl get endpointslices -n backend \
  -l multicluster.kubernetes.io/service-name=payment-api

# Check if OVN LB is configured
kubectl exec -n ovn-kubernetes ovnkube-master-xxx -- \
  ovn-nbctl list load_balancer | grep payment-api
```

### Test Connectivity

```bash
# Get ClusterSetIP
CLUSTERSETIP=$(kubectl get serviceimport payment-api -n backend \
  -o jsonpath='{.spec.ips[0]}')

# Test from a pod
kubectl exec -n backend test-pod -- curl http://$CLUSTERSETIP:8080
```

## Network Isolation

Services are only exported/imported between clusters that have matching CUDN networks:

```
Cluster West: cudn_green (10.50.0.0/16)
Cluster East: cudn_green (10.51.0.0/16)
              cudn_blue  (10.52.0.0/16)

Service exported from West/cudn_green:
✅ Imported to East/cudn_green
❌ NOT imported to East/cudn_blue (different network)
```

The MCS controller ensures:
1. Only services on CUDN networks are exported
2. Only imported to clusters with matching CUDN name
3. OVN Load Balancers only configured on matching logical switches
4. Complete network isolation at data plane level

## Troubleshooting

### Check MCS Controller Logs

```bash
kubectl logs -n ovn-kubernetes deployment/ovnkube-cluster-manager \
  | grep -i mcs
```

### Check Broker Connectivity

```bash
# From member cluster pod
kubectl exec -n ovn-kubernetes ovnkube-cluster-manager-xxx -- \
  curl -k https://broker-api-server/api/v1/namespaces/ovn-kubernetes-broker
```

### Verify CUDN Configuration

```bash
# List CUDNs
kubectl get clusteruserdefinednetworks

# Check namespace labels
kubectl get namespace backend -o yaml

# Verify CUDN selects namespace
kubectl get cudn cudn_green -o jsonpath='{.spec.namespaceSelector}'
```

### Common Issues

1. **ServiceExport created but not in broker**
   - Check MCS controller logs
   - Verify broker kubeconfig secret
   - Check RBAC permissions

2. **ServiceImport not created**
   - Verify matching CUDN exists in local cluster
   - Check if CUDN has same name in both clusters
   - Verify BGP EVPN connectivity

3. **EndpointSlice has no endpoints**
   - Check if EndpointSlice mirroring is working
   - Verify pods have IPs on CUDN network
   - Check EndpointSlice labels for network annotation

## Advanced Configuration

### Custom Broker Namespace

```bash
# Use a different broker namespace
kubectl create namespace my-broker-ns
# Update all manifests to use my-broker-ns
```

### Multiple ClusterSets

Create separate broker clusters for different ClusterSets:

```
Broker-Production  → ClusterSet-Prod
Broker-Development → ClusterSet-Dev
```

### BGP EVPN Integration

Ensure BGP EVPN is configured for pod IP reachability:
- Same Route Target for same CUDN across clusters
- BGP session established between clusters
- Routes advertised via EVPN Type-5

See your BGP EVPN PoC repository for datapath setup.
