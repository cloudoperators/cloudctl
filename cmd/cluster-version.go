// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	clientcmd "k8s.io/client-go/tools/clientcmd"

	"github.com/cloudoperators/cloudctl/cmd/output"
)

var clusterVersionCmd = &cobra.Command{
	Use:   "cluster-version",
	Short: "Print the Kubernetes server version for a kubeconfig context",
	Long: `Queries the Kubernetes API server version for the given kubeconfig context.

An unauthenticated GET to /version is attempted first (faster, no token
refresh required). If the server requires authentication, cloudctl falls
back to the standard authenticated discovery endpoint.

Examples:
  # Version of the current context
  cloudctl cluster-version

  # Version of a specific context
  cloudctl cluster-version --context prod-eu

  # Machine-readable output
  cloudctl cluster-version --context prod-eu -o json`,
	RunE: runClusterVersion,
}

var (
	kubeconfig  string
	kubecontext string
)

func runClusterVersion(cmd *cobra.Command, args []string) error {
	// Use viper as a source of configuration
	kubeconfig = viper.GetString("kubeconfig")
	kubecontext = viper.GetString("context")

	cfg, err := configWithContext(kubecontext, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to build kubeconfig with context %s: %w", kubecontext, err)
	}

	// 1) Try unauthenticated GET /version
	version, err := getUnauthenticatedVersion(cfg)
	if err != nil {
		// 2) Fallback to authenticated
		if !hasAuth(cfg) {
			return fmt.Errorf("no authentication methods found in your kubeconfig. please authenticate (`kubelogin`, etc.) and try again")
		}

		clientset, cerr := kubernetes.NewForConfig(cfg)
		if cerr != nil {
			return fmt.Errorf("failed to create client: %w", cerr)
		}
		version, err = clientset.Discovery().ServerVersion()
		if err != nil {
			return fmt.Errorf("authenticated version fetch failed: %w", err)
		}
	}

	// print out the relevant fields
	parts := strings.Split(version.GitVersion, "-")
	clean := parts[0]
	parts = strings.Split(clean, "+")
	clean = parts[0]
	clusterVersion := strings.TrimPrefix(clean, "v")

	format, err := output.ParseFormat(viper.GetString("output"))
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	printer := output.New(format, output.IsTTYWriter(w), w)
	return printer.Print(output.ClusterVersionResult{Context: kubecontext, Version: clusterVersion})
}

// hasAuth returns true if the rest.Config contains any credential source.
func hasAuth(cfg *rest.Config) bool {
	if cfg.BearerToken != "" || cfg.BearerTokenFile != "" {
		return true
	}
	if cfg.Username != "" && cfg.Password != "" {
		return true
	}
	if len(cfg.CertData) > 0 || cfg.CertFile != "" {
		return true
	}
	if cfg.ExecProvider != nil {
		return true
	}
	if cfg.AuthProvider != nil {
		if cfg.AuthProvider.Config["id-token"] != "" {
			return true
		}
	}
	return false
}

// getUnauthenticatedVersion does a direct HTTP GET to /version,
// using the same Host and CA / TLS settings from cfg, but no creds.
func getUnauthenticatedVersion(cfg *rest.Config) (*version.Info, error) {
	url := strings.TrimRight(cfg.Host, "/") + "/version"

	// build TLS config
	tlsCfg := &tls.Config{}
	if cfg.Insecure {
		tlsCfg.InsecureSkipVerify = true
	}

	// trust the same CA if provided
	if len(cfg.CAData) > 0 {
		pool := x509.NewCertPool()
		if ok := pool.AppendCertsFromPEM(cfg.CAData); !ok {
			return nil, fmt.Errorf("failed to append CA data")
		}
		tlsCfg.RootCAs = pool
	} else if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if ok := pool.AppendCertsFromPEM(pem); !ok {
			return nil, fmt.Errorf("failed to append CA file")
		}
		tlsCfg.RootCAs = pool
	}

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status: %s", resp.Status)
	}

	var v version.Info
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	return &v, nil
}

func init() {
	clusterVersionCmd.Flags().StringVarP(&kubeconfig, "kubeconfig", "k", clientcmd.RecommendedHomeFile, "Path to kubeconfig file")
	clusterVersionCmd.Flags().StringVarP(&kubecontext, "context", "c", "", "Kubeconfig context to query (defaults to current context)")

	// BindPFlags can theroretically return an error if called with `nil` as an argument
	// which should never happened after at least one flag was defined. That's why the output
	// there is ignored.
	viper.BindPFlags(clusterVersionCmd.Flags())
}
