#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${1:-cloudctl-e2e}"

command -v k3d >/dev/null 2>&1 || { echo "k3d is required. Install from https://k3d.io/." >&2; exit 1; }

if k3d cluster list | awk 'NR>1 {print $1}' | grep -qx "$CLUSTER_NAME"; then
  echo "Deleting k3d cluster '$CLUSTER_NAME'..."
  k3d cluster delete "$CLUSTER_NAME"
else
  echo "Cluster '$CLUSTER_NAME' not found. Nothing to delete."
fi
