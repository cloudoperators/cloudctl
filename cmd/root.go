// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

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

var (
	configFilePath string
)

// Execute runs the CLI with the provided context.
func Execute(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}

func init() {
	cobra.OnInitialize(func() {
		cobra.CheckErr(setupConfig())
	})
	rootCmd.PersistentFlags().StringVar(&configFilePath, "config", "", "Path to configuration file")

	// BindPFlags can theroretically return an error if called with `nil` as an argument
	// which should never happened after at least one flag was defined. That's why the output
	// there is ignored.
	viper.BindPFlags(rootCmd.PersistentFlags())

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

func setupConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Optionally read environment variables, config files, etc.
	viper.SetEnvPrefix("CLOUDCTL")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	viper.SetConfigType("yaml")

	configFilePath = viper.GetString("config")
	if len(configFilePath) > 0 {
		// Phase 1
		// First we are trying config provided as a command line parameter. Fail if there was an error
		// during reading configuration from this specified path.
		viper.SetConfigFile(configFilePath)
		return viper.ReadInConfig()
	} else {
		// Phase 2
		// Then we are searching for ".cloudctl.yaml" in current or home directory
		viper.AddConfigPath(".")
		viper.AddConfigPath(home)
		// NOTE: viper is automatically adding a file extension basing on the value of called above `SetConfigType`
		viper.SetConfigName(".cloudctl")
	}

	err = viper.ReadInConfig()
	if _, ok := err.(viper.ConfigFileNotFoundError); ok {
		// Phase 3
		// If reading config in above described locations failed, we are looking for configuration
		// in these locations:
		//   locations set in PHASE 2:
		//     ./cloudctl.yaml
		//     $HOME/cloudctl.yaml
		//   if $XDG_CONFIG_HOME is set:
		//     $XDG_CONFIG_HOME/cloudctl/cloudctl.yaml
		//     $XDG_CONFIG_HOME/cloudctl.yaml
		//   else:
		//     $HOME/.config/cloudctl/cloudctl.yaml
		//     $HOME/.config/cloudctl.yaml
		// NOTE: viper is automatically adding a file extension basing on the value of called above `SetConfigType`
		viper.SetConfigName("cloudctl")
		if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); len(xdgConfig) > 0 {
			viper.AddConfigPath(filepath.Join(xdgConfig, "cloudctl"))
			viper.AddConfigPath(xdgConfig)
		} else {
			viper.AddConfigPath(filepath.Join(home, ".config", "cloudctl"))
			viper.AddConfigPath(filepath.Join(home, ".config"))
		}
		err = viper.ReadInConfig()
		// If configuration was not found in any of above listed locations - that's ok.
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			err = nil
		}
	}

	return err
}
