// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"

	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var rootCmd = &cobra.Command{
	Use:   "cloudctl",
	Short: "Manage and access Kubernetes clusters via Greenhouse",
	Long: `cloudctl is a command line interface that helps:
    
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
      cloudctl version`,
}

// Execute runs the CLI with the provided context.
func Execute(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}

func init() {
	// Add subcommands here
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(clusterVersionCmd)
	rootCmd.AddCommand(versionCmd)
}

// configWithContext builds a rest.Config for the specified context name from the given kubeconfig path.
func configWithContext(contextName, kubeconfigPath string) (*rest.Config, error) {
	loadingRules := &clientcmd.ClientConfigLoadingRules{
		ExplicitPath: kubeconfigPath,
	}
	overrides := &clientcmd.ConfigOverrides{
		CurrentContext: contextName,
	}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
	return cc.ClientConfig()
}
