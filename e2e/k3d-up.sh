#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

CLUSTER_NAME="${1:-cloudctl-e2e}"
OUT_KUBECONFIG="${2:-./tmp/e2e-kubeconfig}"

command -v k3d >/dev/null 2>&1 || { echo "k3d is required. Install from https://k3d.io/." >&2; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo "kubectl is required. Install kubectl." >&2; exit 1; }

mkdir -p "$(dirname "$OUT_KUBECONFIG")"

# Create cluster if it doesn't exist
if k3d cluster list | awk 'NR>1 {print $1}' | grep -qx "$CLUSTER_NAME"; then
  echo "Cluster '$CLUSTER_NAME' already exists."
else
  echo "Creating k3d cluster '$CLUSTER_NAME'..."
  k3d cluster create "$CLUSTER_NAME" --wait
fi

echo "Writing kubeconfig to $OUT_KUBECONFIG"
# IMPORTANT: use 'k3d kubeconfig get' to emit YAML, not 'write' (which prints a path)
k3d kubeconfig get "$CLUSTER_NAME" > "$OUT_KUBECONFIG"
chmod 600 "$OUT_KUBECONFIG"

echo "Cluster '$CLUSTER_NAME' is ready."
kubectl --kubeconfig "$OUT_KUBECONFIG" get nodes