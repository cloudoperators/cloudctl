// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"k8s.io/client-go/tools/clientcmd"

	. "github.com/onsi/gomega"
)

var ConfigA = []byte(`kubeconfig: A
config: B
`)

func TestNotSetConfigPath(t *testing.T) {
	g := NewWithT(t)

	t.Cleanup(func() { viper.Reset() })

	// Ensure default config file location is not set
	config := rootCmd.PersistentFlags().Lookup("config")
	g.Expect(config).NotTo(BeNil())
	g.Expect(config.Value.String()).To(BeEmpty())

	// ... and that does not lead to an error during configuration setup
	g.Expect(setupConfig()).To(BeNil())
}

func TestConfigurationLoad(t *testing.T) {
	g := NewWithT(t)

	f, err := os.CreateTemp("", "test_cloudctl_config")
	g.Expect(err).To(BeNil())
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	_, err = f.Write(ConfigA)
	g.Expect(err).To(BeNil())
	err = f.Close()
	g.Expect(err).To(BeNil())

	// Set config file location env variable
	t.Setenv("CLOUDCTL_CONFIG", f.Name())
	t.Cleanup(func() { viper.Reset() })

	// Do the setup
	g.Expect(setupConfig()).To(BeNil())

	// Check if config file variable was not overwriten with data from config file
	g.Expect(viper.GetString("config")).To(Equal(f.Name()))

	// Check if `kubeconfig` variable was set to the value from temporary file
	g.Expect(viper.GetString("kubeconfig")).To(Equal("A"))
}

func TestEnvKeyReplacerDashToUnderscore(t *testing.T) {
	g := NewWithT(t)

	const envKey = "CLOUDCTL_GREENHOUSE_CLUSTER_KUBECONFIG"
	const viperKey = "greenhouse-cluster-kubeconfig"
	testValue := filepath.Join(t.TempDir(), "test-kubeconfig")

	t.Setenv(envKey, testValue)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("CLOUDCTL_CONFIG", "")
	t.Chdir(t.TempDir())
	t.Cleanup(func() { viper.Reset() })

	g.Expect(setupConfig()).To(BeNil())

	// The env key replacer must map CLOUDCTL_GREENHOUSE_CLUSTER_KUBECONFIG → greenhouse-cluster-kubeconfig
	g.Expect(viper.GetString(viperKey)).To(Equal(testValue))
}

func TestMissingConfigurationFile(t *testing.T) {
	g := NewWithT(t)

	// Set config file location env variable
	t.Setenv("CLOUDCTL_CONFIG", "A")
	t.Cleanup(func() { viper.Reset() })

	// Do the setup
	err := setupConfig()
	g.Expect(err).NotTo(BeNil())
}

func TestResolveKubeconfig_ExplicitlySet(t *testing.T) {
	g := NewWithT(t)

	t.Setenv("KUBECONFIG", "/some/other/config")
	t.Cleanup(func() { viper.Reset() })

	// When the viper key is explicitly set (simulating flag/CLOUDCTL_* env var),
	// the provided value must be returned as-is regardless of KUBECONFIG.
	viper.Set("kubeconfig", "/my/explicit/path")
	result := resolveKubeconfig("kubeconfig", "/my/explicit/path")
	g.Expect(result).To(Equal("/my/explicit/path"))
}

func TestResolveKubeconfig_NotSetWithKUBECONFIG(t *testing.T) {
	g := NewWithT(t)

	t.Setenv("KUBECONFIG", "/env/kubeconfig")
	t.Cleanup(func() { viper.Reset() })

	// When the key was not explicitly set and KUBECONFIG is set, return "" to let client-go handle it.
	result := resolveKubeconfig("kubeconfig", clientcmd.RecommendedHomeFile)
	g.Expect(result).To(BeEmpty())
}

func TestResolveKubeconfig_NotSetWithoutKUBECONFIG(t *testing.T) {
	g := NewWithT(t)

	t.Setenv("KUBECONFIG", "")
	t.Cleanup(func() { viper.Reset() })

	// When KUBECONFIG is not set either, return the default path as-is.
	result := resolveKubeconfig("kubeconfig", clientcmd.RecommendedHomeFile)
	g.Expect(result).To(Equal(clientcmd.RecommendedHomeFile))
}
