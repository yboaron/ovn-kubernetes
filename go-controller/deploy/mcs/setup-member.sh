#!/bin/bash
# Setup script for MCS member cluster

set -e

if [ $# -lt 2 ]; then
    echo "Usage: $0 <member-context> <broker-kubeconfig>"
    echo ""
    echo "Examples:"
    echo "  $0 kind-west broker-kubeconfig-west.yaml"
    echo "  $0 kind-east broker-kubeconfig-east.yaml"
    exit 1
fi

MEMBER_CONTEXT="$1"
BROKER_KUBECONFIG="$2"
MEMBER_NS="${MEMBER_NS:-ovn-kubernetes}"

echo "=== Setting up MCS Member Cluster ==="
echo "Context: $MEMBER_CONTEXT"
echo "Broker kubeconfig: $BROKER_KUBECONFIG"
echo "Namespace: $MEMBER_NS"
echo ""

# Check if member cluster is accessible
if ! kubectl --context "$MEMBER_CONTEXT" cluster-info &>/dev/null; then
    echo "ERROR: Cannot access member cluster with context: $MEMBER_CONTEXT"
    exit 1
fi

# Check if broker kubeconfig exists
if [ ! -f "$BROKER_KUBECONFIG" ]; then
    echo "ERROR: Broker kubeconfig not found: $BROKER_KUBECONFIG"
    echo "Create it first using: ./create-broker-token.sh <cluster-name>"
    exit 1
fi

# Test broker access
echo "Testing broker access..."
if ! kubectl --kubeconfig "$BROKER_KUBECONFIG" get clusterinfos &>/dev/null; then
    echo "WARNING: Cannot access broker. Continuing anyway..."
fi

# Install MCS API CRDs
echo "1. Installing MCS API CRDs..."
kubectl --context "$MEMBER_CONTEXT" apply -f 01-mcs-api-crds.yaml

# Wait for CRDs
echo "   Waiting for CRDs to be ready..."
kubectl --context "$MEMBER_CONTEXT" wait --for condition=established \
  crd/serviceexports.multicluster.x-k8s.io \
  crd/serviceimports.multicluster.x-k8s.io \
  --timeout=30s

# Create namespace if it doesn't exist
if ! kubectl --context "$MEMBER_CONTEXT" get ns "$MEMBER_NS" &>/dev/null; then
    echo "2. Creating namespace: $MEMBER_NS"
    kubectl --context "$MEMBER_CONTEXT" create namespace "$MEMBER_NS"
else
    echo "2. Namespace already exists: $MEMBER_NS"
fi

# Install member RBAC
echo "3. Installing member RBAC..."
kubectl --context "$MEMBER_CONTEXT" apply -f 20-member-rbac.yaml

# Create broker kubeconfig secret
echo "4. Creating broker kubeconfig secret..."
kubectl --context "$MEMBER_CONTEXT" create secret generic ovn-mcs-broker-config \
  -n "$MEMBER_NS" \
  --from-file=kubeconfig="$BROKER_KUBECONFIG" \
  --dry-run=client -o yaml | kubectl --context "$MEMBER_CONTEXT" apply -f -

# Verify setup
echo ""
echo "=== Member Cluster Setup Complete ==="
echo ""
echo "Verify setup:"
echo "  kubectl --context $MEMBER_CONTEXT get crd | grep multicluster.x-k8s.io"
echo "  kubectl --context $MEMBER_CONTEXT get sa -n $MEMBER_NS ovn-mcs-controller"
echo "  kubectl --context $MEMBER_CONTEXT get secret -n $MEMBER_NS ovn-mcs-broker-config"
echo ""
echo "Next steps:"
echo "  1. Update ovnkube-cluster-manager to enable MCS controller"
echo "  2. Export a service: kubectl apply -f examples/service-export.yaml"
echo "  3. Verify in broker: kubectl --kubeconfig $BROKER_KUBECONFIG get serviceexports -A"
