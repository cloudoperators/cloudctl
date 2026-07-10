// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/version"
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
back to an authenticated GET to /version using the kubeconfig credentials.

If the API server is unreachable the command exits after --timeout (default 10s).

Examples:
  # Version of the current context
  cloudctl cluster-version

  # Version of a specific context
  cloudctl cluster-version --context prod-eu

  # Machine-readable output
  cloudctl cluster-version --context prod-eu -o json

  # Shorter timeout when scripting
  cloudctl cluster-version --context prod-eu --timeout 5s`,
	RunE: runClusterVersion,
}

var (
	kubeconfig  string
	kubecontext string
)

func runClusterVersion(cmd *cobra.Command, args []string) error {
	kubeconfig = viper.GetString("kubeconfig")
	kubecontext = viper.GetString("context")

	timeoutStr := viper.GetString("timeout")
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return fmt.Errorf("invalid --timeout %q: %w", timeoutStr, err)
	}

	cfg, err := configWithContext(kubecontext, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to build kubeconfig with context %q: %w", kubecontext, err)
	}

	// Resolve the actual context name used so the output is never empty.
	// When --context is not given, kubecontext is "" and we fall back to the
	// current-context field from the kubeconfig.
	effectiveContext := kubecontext
	if effectiveContext == "" {
		loadingRules := clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
		raw, rawErr := loadingRules.Load()
		if rawErr == nil && raw != nil {
			effectiveContext = raw.CurrentContext
		}
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	// 1) Try unauthenticated GET /version
	ver, err := getUnauthenticatedVersion(ctx, cfg)
	if err != nil {
		// 2) Fallback to authenticated
		if !hasAuth(cfg) {
			return fmt.Errorf("no authentication methods found in your kubeconfig. Please authenticate (`kubelogin`, etc.) and try again")
		}

		ver, err = getAuthenticatedVersion(ctx, cfg)
		if err != nil {
			return fmt.Errorf("authenticated version fetch failed: %w", err)
		}
	}

	// Strip build metadata so we get a clean semver string (e.g. "1.29.3").
	parts := strings.Split(ver.GitVersion, "-")
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
	return printer.Print(output.ClusterVersionResult{Context: effectiveContext, Version: clusterVersion})
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

// getAuthenticatedVersion fetches the server version using a fully-authenticated
// REST client. The provided context controls cancellation and deadline.
func getAuthenticatedVersion(ctx context.Context, cfg *rest.Config) (*version.Info, error) {
	// Build a transport with credentials from cfg so the request is authenticated.
	transport, err := rest.TransportFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build authenticated transport: %w", err)
	}
	client := &http.Client{Transport: transport}
	url := strings.TrimRight(cfg.Host, "/") + "/version"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
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

// getUnauthenticatedVersion does a direct HTTP GET to /version using the same
// Host and CA / TLS settings from cfg, but no credentials.
// The provided context controls the request deadline.
func getUnauthenticatedVersion(ctx context.Context, cfg *rest.Config) (*version.Info, error) {
	url := strings.TrimRight(cfg.Host, "/") + "/version"

	tlsCfg := &tls.Config{}
	if cfg.Insecure {
		tlsCfg.InsecureSkipVerify = true // #nosec G402 — user explicitly opted in
	}
	if cfg.ServerName != "" {
		tlsCfg.ServerName = cfg.ServerName
	}

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

	// Clone the default transport so proxy settings, dial/keepalive defaults,
	// and HTTP/2 support are preserved; only override TLS configuration.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsCfg
	client := &http.Client{Transport: transport}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
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
	clusterVersionCmd.Flags().String("timeout", "10s", "Maximum time to wait for the API server to respond")

	// BindPFlags can theoretically return an error if called with `nil` as an argument
	// which should never happen after at least one flag was defined. That's why the output
	// there is ignored.
	_ = viper.BindPFlags(clusterVersionCmd.Flags())
}
