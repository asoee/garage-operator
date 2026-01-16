#!/bin/bash
set -euo pipefail

# Cleanup script for garage-operator test clusters
# Usage: ./hack/cleanup-test.sh [cluster-name]

CLUSTER_NAME="${1:-}"

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

if [ -n "$CLUSTER_NAME" ]; then
    # Delete specific cluster
    log_info "Deleting kind cluster: $CLUSTER_NAME"
    kind delete cluster --name "$CLUSTER_NAME"
else
    # List and optionally delete all garage-related clusters
    clusters=$(kind get clusters 2>/dev/null | grep -E "^garage" || true)

    if [ -z "$clusters" ]; then
        log_info "No garage-related kind clusters found"
        exit 0
    fi

    echo "Found garage-related clusters:"
    echo "$clusters"
    echo ""
    read -p "Delete all these clusters? [y/N] " -n 1 -r
    echo ""

    if [[ $REPLY =~ ^[Yy]$ ]]; then
        for cluster in $clusters; do
            log_info "Deleting: $cluster"
            kind delete cluster --name "$cluster"
        done
        log_info "All clusters deleted"
    else
        log_info "Cancelled"
    fi
fi
