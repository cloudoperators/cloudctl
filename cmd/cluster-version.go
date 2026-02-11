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
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	clientcmd "k8s.io/client-go/tools/clientcmd"
)

var clusterVersionCmd = &cobra.Command{
	Use:   "cluster-version",
	Short: "Prints the cluster version of the context in kubeconfig",
	RunE:  runClusterVersion,
}

var (
	kubeconfig  string
	kubecontext string
)

func runClusterVersion(cmd *cobra.Command, args []string) error {

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
	fmt.Println(clusterVersion)
	return nil
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
	clusterVersionCmd.Flags().StringVarP(&kubeconfig, "kubeconfig", "k", clientcmd.RecommendedHomeFile, "kubeconfig file path")
	clusterVersionCmd.Flags().StringVarP(&kubecontext, "context", "c", "", "cluster version of the specified context in kubeconfig")
}
