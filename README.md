[![REUSE status](https://api.reuse.software/badge/github.com/cloudoperators/cloudctl)](https://api.reuse.software/info/github.com/cloudoperators/cloudctl)

# cloudctl

Unified Kubernetes CLI for the cloud.

```
cloudctl is a command line interface that helps:
    
    1) Fetch and merge kubeconfigs from the central Greenhouse cluster into your local kubeconfig
    2) Sync contexts and credentials for seamless kubectl usage
    3) Inspect the Kubernetes version of a target cluster
    4) Print the cloudctl version and build information

Examples:
  - Merge/refresh kubeconfigs from Greenhouse:
      cloudctl sync

  - Show Kubernetes version for a specific context:
      cloudctl cluster-version --context my-cluster

  - Show cloudctl version:
      cloudctl version

Usage:
  cloudctl [command]

Available Commands:
  cluster-version Prints the cluster version of the context in kubeconfig
  completion      Generate the autocompletion script for the specified shell
  help            Help about any command
  sync            Fetches kubeconfigs of remote clusters from Greenhouse cluster and merges them into your local config
  version         Print the cloudctl version information

Flags:
  -h, --help   help for cloudctl

Use "cloudctl [command] --help" for more information about a command.
```

## Requirements and Setup
Download the latest release from [here](https://github.com/cloudoperators/cloudctl/releases), move to a location in PATH and update file permissions.


## Support, Feedback, Contributing

This project is open to feature requests/suggestions, bug reports etc. via [GitHub issues](https://github.com/cloudoperators/cloudctl/issues). Contribution and feedback are encouraged and always welcome. For more information about how to contribute, the project structure, as well as additional contribution information, see our [Contribution Guidelines](CONTRIBUTING.md).

## Security / Disclosure
If you find any bug that may be a security problem, please follow our instructions at [in our security policy](https://github.com/cloudoperators/cloudctl/security/policy) on how to report it. Please do not create GitHub issues for security-related doubts or problems.

## Code of Conduct

We as members, contributors, and leaders pledge to make participation in our community a harassment-free experience for everyone. By participating in this project, you agree to abide by its [Code of Conduct](https://github.com/SAP/.github/blob/main/CODE_OF_CONDUCT.md) at all times.

## Licensing

Copyright 2025 SAP SE or an SAP affiliate company and cloudctl contributors. Please see our [LICENSE](LICENSE) for copyright and license information. Detailed information including third-party components and their licensing/copyright information is available [via the REUSE tool](https://api.reuse.software/info/github.com/cloudoperators/cloudctl).
