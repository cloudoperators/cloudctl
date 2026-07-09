// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var rootCmd = &cobra.Command{
	Use:          "cloudctl",
	Short:        "Manage Kubernetes cluster access via Greenhouse",
	SilenceUsage: true,
	SilenceErrors: true,
	Long: `cloudctl keeps your local kubeconfig in sync with the clusters registered
in your Greenhouse organization — so kubectl just works.

Commands:
  sync              Fetch ClusterKubeconfigs from Greenhouse and merge them locally
  cluster-version   Query the Kubernetes server version of a kubeconfig context
  version           Print cloudctl build information

Global flags available on every command:
  -o, --output text|json|yaml   Output format (default: text)
      --log-level debug|info|warn|error
      --log-format text|json

Examples:
  # Sync all clusters for an organization
  cloudctl sync -n my-org

  # Sync a single cluster, emit JSON for scripting
  cloudctl sync -n my-org --remote-cluster-name prod-eu -o json

  # Query Kubernetes version of a context
  cloudctl cluster-version --context prod-eu

  # Print version as YAML
  cloudctl version -o yaml`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return setupLogger()
	},
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
	rootCmd.PersistentFlags().String("log-level", "info", "Log verbosity: debug, info, warn, error")
	rootCmd.PersistentFlags().String("log-format", "text", "Log format: text or json (written to stderr)")
	rootCmd.PersistentFlags().StringP("output", "o", "text", "Output format: text, json, or yaml")

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

// setupLogger configures slog based on --log-level and --log-format flags.
func setupLogger() error {
	levelStr := viper.GetString("log-level")
	format := viper.GetString("log-format")

	var level slog.Level
	if err := level.UnmarshalText([]byte(levelStr)); err != nil {
		return err
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stderr, opts)
	default:
		return fmt.Errorf("invalid --log-format %q: must be \"text\" or \"json\"", format)
	}
	slog.SetDefault(slog.New(handler))
	return nil
}
