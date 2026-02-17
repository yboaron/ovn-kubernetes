#!/bin/bash
# Quick setup script for MCS broker cluster

set -e

BROKER_CONTEXT="${BROKER_CONTEXT:-kind-broker}"
BROKER_NS="${BROKER_NS:-ovn-kubernetes-broker}"

echo "=== Setting up MCS Broker Cluster ==="
echo "Context: $BROKER_CONTEXT"
echo "Namespace: $BROKER_NS"
echo ""

# Check if broker cluster is accessible
if ! kubectl --context "$BROKER_CONTEXT" cluster-info &>/dev/null; then
    echo "ERROR: Cannot access broker cluster with context: $BROKER_CONTEXT"
    echo "Please ensure the cluster is running and BROKER_CONTEXT is set correctly"
    exit 1
fi

# Install broker CRDs
echo "1. Installing broker CRDs..."
kubectl --context "$BROKER_CONTEXT" apply -f 00-broker-crds.yaml

# Wait for CRDs to be established
echo "   Waiting for CRDs to be ready..."
kubectl --context "$BROKER_CONTEXT" wait --for condition=established \
  crd/clusterinfos.broker.ovn.org \
  crd/serviceexports.broker.ovn.org \
  crd/serviceimports.broker.ovn.org \
  --timeout=30s

# Create namespace and RBAC
echo "2. Creating broker namespace and RBAC..."
kubectl --context "$BROKER_CONTEXT" apply -f 10-broker-namespace.yaml
kubectl --context "$BROKER_CONTEXT" apply -f 11-broker-rbac.yaml

# Verify setup
echo ""
echo "=== Broker Setup Complete ==="
echo ""
echo "Verify setup:"
echo "  kubectl --context $BROKER_CONTEXT get crd | grep broker.ovn.org"
echo "  kubectl --context $BROKER_CONTEXT get ns $BROKER_NS"
echo "  kubectl --context $BROKER_CONTEXT get sa -n $BROKER_NS"
echo ""
echo "Next steps:"
echo "  1. Create broker access tokens for member clusters:"
echo "     ./create-broker-token.sh cluster-west > broker-token-west.txt"
echo "     ./create-broker-token.sh cluster-east > broker-token-east.txt"
echo ""
echo "  2. Setup member clusters:"
echo "     ./setup-member.sh kind-west broker-token-west.txt"
echo "     ./setup-member.sh kind-east broker-token-east.txt"
