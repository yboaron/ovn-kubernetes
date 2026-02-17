#!/bin/bash
# Creates a broker access token and kubeconfig for a member cluster

set -e

if [ $# -lt 1 ]; then
    echo "Usage: $0 <cluster-name> [output-file]"
    echo ""
    echo "Examples:"
    echo "  $0 cluster-west"
    echo "  $0 cluster-west broker-kubeconfig-west.yaml"
    exit 1
fi

CLUSTER_NAME="$1"
OUTPUT_FILE="${2:-broker-kubeconfig-${CLUSTER_NAME}.yaml}"
BROKER_CONTEXT="${BROKER_CONTEXT:-kind-broker}"
BROKER_NS="${BROKER_NS:-ovn-kubernetes-broker}"
SA_NAME="ovn-mcs-broker-agent"

echo "Creating broker access for cluster: $CLUSTER_NAME" >&2
echo "Broker context: $BROKER_CONTEXT" >&2
echo "Output file: $OUTPUT_FILE" >&2
echo "" >&2

# Create token
echo "Generating token..." >&2
TOKEN=$(kubectl --context "$BROKER_CONTEXT" create token "$SA_NAME" \
  -n "$BROKER_NS" \
  --duration=87600h)

if [ -z "$TOKEN" ]; then
    echo "ERROR: Failed to create token" >&2
    exit 1
fi

# Get broker API server
BROKER_API=$(kubectl --context "$BROKER_CONTEXT" config view --minify -o jsonpath='{.clusters[0].cluster.server}')
BROKER_CA=$(kubectl --context "$BROKER_CONTEXT" config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')

if [ -z "$BROKER_API" ]; then
    echo "ERROR: Failed to get broker API server URL" >&2
    exit 1
fi

echo "Broker API: $BROKER_API" >&2

# Create kubeconfig
echo "Creating kubeconfig..." >&2
cat > "$OUTPUT_FILE" <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: $BROKER_API
EOF

if [ -n "$BROKER_CA" ]; then
    cat >> "$OUTPUT_FILE" <<EOF
    certificate-authority-data: $BROKER_CA
EOF
else
    echo "WARNING: No CA data found, using insecure connection" >&2
    cat >> "$OUTPUT_FILE" <<EOF
    insecure-skip-tls-verify: true
EOF
fi

cat >> "$OUTPUT_FILE" <<EOF
  name: broker
contexts:
- context:
    cluster: broker
    namespace: $BROKER_NS
    user: broker-agent-$CLUSTER_NAME
  name: broker
current-context: broker
users:
- name: broker-agent-$CLUSTER_NAME
  user:
    token: $TOKEN
EOF

echo "" >&2
echo "✅ Broker kubeconfig created: $OUTPUT_FILE" >&2
echo "" >&2
echo "Test access:" >&2
echo "  kubectl --kubeconfig=$OUTPUT_FILE get clusterinfos" >&2
echo "" >&2
echo "Next: Setup member cluster" >&2
echo "  ./setup-member.sh <member-context> $OUTPUT_FILE" >&2
