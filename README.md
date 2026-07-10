[![REUSE status](https://api.reuse.software/badge/github.com/cloudoperators/cloudctl)](https://api.reuse.software/info/github.com/cloudoperators/cloudctl)

# cloudctl

**cloudctl** is a CLI for managing Kubernetes cluster access via [Greenhouse](https://github.com/cloudoperators/greenhouse). It keeps your local kubeconfig in sync with the clusters registered in your Greenhouse organization — so `kubectl` just works.

## What it does

- **Syncs kubeconfigs** — fetches `ClusterKubeconfig` resources from Greenhouse and merges them into your local `~/.kube/config`, handling OIDC token caching, deduplication, and prefix-based entry management
- **Reports cluster versions** — queries the Kubernetes API version of any context, trying unauthenticated first for speed
- **Structured output** — every command supports `--output text|json|yaml` for scripting and pipelines; interactive terminals get a spinner and a colour-coded table

## Installation

Download the latest binary from the [releases page](https://github.com/cloudoperators/cloudctl/releases), place it on your `PATH`, and make it executable:

```sh
# macOS / Linux
curl -Lo cloudctl https://github.com/cloudoperators/cloudctl/releases/latest/download/cloudctl-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m)
chmod +x cloudctl
sudo mv cloudctl /usr/local/bin/
```

Or build from source (requires Go 1.25+):

```sh
git clone https://github.com/cloudoperators/cloudctl.git
cd cloudctl
make install   # installs to $GOBIN
```

## Quick start

```sh
# Sync all clusters from a Greenhouse organization into your local kubeconfig
cloudctl sync --greenhouse-cluster-namespace <org>

# Sync a single cluster
cloudctl sync -n <org> --remote-cluster-name <cluster>

# Check the Kubernetes version of a context
cloudctl cluster-version --context <context>

# Print version info
cloudctl version
cloudctl version --output json   # machine-readable
```

## Output formats

All commands accept `-o / --output`:

| Value  | Description                                              |
|--------|----------------------------------------------------------|
| `text` | Human-readable (default). Interactive terminals get a spinner and styled table. |
| `json` | Indented JSON — suitable for `jq` pipelines.             |
| `yaml` | YAML — suitable for GitOps tooling.                      |

```sh
# Pipeline example
cloudctl version -o json | python3 -m json.tool
cloudctl sync -n <org> -o json | python3 -c "import sys,json; r=json.load(sys.stdin); print(r['synced'])"
```

## Logging

Logs are written to **stderr** so they never pollute stdout pipelines. By default only `info`-level messages appear.

| Flag           | Values                       | Default  |
|----------------|------------------------------|----------|
| `--log-level`  | `debug`, `info`, `warn`, `error` | `info` |
| `--log-format` | `text`, `json`               | `text`   |

```sh
# Show debug logs while syncing
cloudctl sync -n <org> --log-level debug

# Structured JSON logs (useful in CI)
cloudctl sync -n <org> --log-level info --log-format json 2>sync.log
```

## Configuration

Every flag can be set via an environment variable (prefix `CLOUDCTL_`, dashes become underscores) or a config file.

**Environment variable example:**
```sh
export CLOUDCTL_GREENHOUSE_CLUSTER_NAMESPACE=my-org
cloudctl sync
```

**Config file** — searched in order, first match wins:

1. Path given by `--config` or `$CLOUDCTL_CONFIG`
2. `./.cloudctl.yaml` or `~/.cloudctl.yaml`
3. `./cloudctl.yaml` or `~/cloudctl.yaml`
4. `$XDG_CONFIG_HOME/cloudctl/cloudctl.yaml` or `$XDG_CONFIG_HOME/cloudctl.yaml`
   (falls back to `~/.config/cloudctl/cloudctl.yaml` or `~/.config/cloudctl.yaml` when `$XDG_CONFIG_HOME` is unset)

Example `~/.cloudctl.yaml`:
```yaml
greenhouse-cluster-namespace: my-org
greenhouse-cluster-kubeconfig: /home/user/.kube/greenhouse.yaml
auth-type: exec-plugin
log-level: info
```

## Commands

### `sync`

Fetches `ClusterKubeconfig` resources from Greenhouse and merges them into your local kubeconfig.

```
cloudctl sync -n <org> [flags]

Flags:
  -k, --greenhouse-cluster-kubeconfig   Path to Greenhouse cluster kubeconfig (default: ~/.kube/config)
  -c, --greenhouse-cluster-context      Context inside the Greenhouse kubeconfig
  -n, --greenhouse-cluster-namespace    Greenhouse organization namespace (required)
  -r, --remote-cluster-kubeconfig       Local kubeconfig to merge into (default: ~/.kube/config)
      --remote-cluster-name             Sync only this cluster (default: all ready clusters)
      --prefix                          Prefix for managed kubeconfig entries (default: cloudctl)
      --merge-identical-users           Share a single auth entry for clusters with identical OIDC config (default: true)
      --auth-type                       auth-provider or exec-plugin (default: exec-plugin)
      --kubelogin-path                  Path to kubelogin binary (default: kubelogin)
      --kubelogin-extra-args            Extra flags passed to kubelogin
      --kubelogin-token-cache-dir       OIDC token cache directory
```

### `cluster-version`

Queries the Kubernetes server version for a given kubeconfig context. Tries an unauthenticated request first; falls back to an authenticated one if needed.

```
cloudctl cluster-version [flags]

Flags:
  -k, --kubeconfig   Path to kubeconfig file (default: ~/.kube/config)
  -c, --context      Context to query
      --timeout      Maximum time to wait for the API server (default: 10s)
```

### `version`

Prints cloudctl build information.

```
cloudctl version [flags]

Flags:
      --short   Print only the version number
```

## Support, Feedback, Contributing

This project is open to feature requests, bug reports, and contributions via [GitHub issues](https://github.com/cloudoperators/cloudctl/issues) and pull requests. See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## Security / Disclosure

If you find a security issue, follow the instructions in our [security policy](https://github.com/cloudoperators/cloudctl/security/policy). Do not open GitHub issues for security-related problems.

## Code of Conduct

By participating in this project you agree to abide by the [SAP Open Source Code of Conduct](https://github.com/SAP/.github/blob/main/CODE_OF_CONDUCT.md).

## Licensing

Copyright 2025 SAP SE or an SAP affiliate company and cloudctl contributors. See [LICENSE](LICENSE) for details. Third-party licensing information is available via the [REUSE tool](https://api.reuse.software/info/github.com/cloudoperators/cloudctl).
