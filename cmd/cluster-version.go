// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudoperators/greenhouse/api/v1alpha1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/rest"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cloudoperators/cloudctl/cmd/output"
)

var clusterVersionCmd = &cobra.Command{
	Use:   "cluster-version",
	Short: "Print the Kubernetes server version for a kubeconfig context",
	Long: `Queries the Kubernetes server version for the given kubeconfig context.

When Greenhouse connection flags are provided (--greenhouse-cluster-namespace and
--greenhouse-cluster-name), the version is read from the greenhouse.sap/kubernetes-version
label on the ClusterKubeconfig resource — faster and resilient to remote API downtime.

If the label is absent or Greenhouse flags are not provided, cloudctl falls back to
querying the remote cluster directly: an unauthenticated GET to /version is attempted
first; if the server requires authentication, an authenticated GET is used instead.

If the API server is unreachable the command exits after --timeout (default 10s).

Examples:
  # Version of the current context (live query)
  cloudctl cluster-version

  # Version from Greenhouse label (preferred when syncing via cloudctl)
  cloudctl cluster-version -n my-org --greenhouse-cluster-name prod-eu

  # Version of a specific context with live query
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

	cvGreenhouseKubeconfig  string
	cvGreenhouseContext     string
	cvGreenhouseNamespace   string
	cvGreenhouseClusterName string
)

func runClusterVersion(cmd *cobra.Command, args []string) error {
	kubeconfig = resolveKubeconfig("kubeconfig", viper.GetString("kubeconfig"))
	kubecontext = viper.GetString("context")

	// Reject an explicit empty-string value.
	if viper.IsSet("kubeconfig") && kubeconfig == "" {
		return fmt.Errorf("--kubeconfig must not be empty")
	}

	// Read Greenhouse flags directly from the cobra flag set to avoid Viper key
	// collisions with the identically-named flags registered by sync.go.
	// Use cmd.Flags().Changed() instead of viper.IsSet() for the same reason.
	cvGreenhouseKubeconfig, _ = cmd.Flags().GetString("greenhouse-cluster-kubeconfig")
	if cmd.Flags().Changed("greenhouse-cluster-kubeconfig") {
		if cvGreenhouseKubeconfig == "" {
			return fmt.Errorf("--greenhouse-cluster-kubeconfig must not be empty")
		}
	} else if os.Getenv("KUBECONFIG") != "" {
		cvGreenhouseKubeconfig = ""
	}
	cvGreenhouseContext, _ = cmd.Flags().GetString("greenhouse-cluster-context")
	cvGreenhouseNamespace, _ = cmd.Flags().GetString("greenhouse-cluster-namespace")
	cvGreenhouseClusterName, _ = cmd.Flags().GetString("greenhouse-cluster-name")

	timeoutStr := viper.GetString("timeout")
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return fmt.Errorf("invalid --timeout %q: %w", timeoutStr, err)
	}

	cfg, err := configWithContext(kubecontext, kubeconfig)
	if err != nil {
		ctxDisplay := kubecontext
		if ctxDisplay == "" {
			ctxDisplay = "(current context)"
		}
		return fmt.Errorf("failed to build kubeconfig (source: %s, context: %s): %w", displayKubeconfig(kubeconfig), ctxDisplay, err)
	}

	// Resolve the actual context name used so the output is never empty.
	// When --context is not given, kubecontext is "" and we fall back to the
	// current-context field from the kubeconfig.
	effectiveContext := kubecontext
	if effectiveContext == "" {
		var loadingRules *clientcmd.ClientConfigLoadingRules
		if kubeconfig != "" {
			loadingRules = &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
		} else {
			loadingRules = clientcmd.NewDefaultClientConfigLoadingRules()
		}
		raw, rawErr := loadingRules.Load()
		if rawErr == nil && raw != nil {
			effectiveContext = raw.CurrentContext
		}
	}
	if effectiveContext == "" {
		effectiveContext = "(unknown)"
	}

	format, err := output.ParseFormat(viper.GetString("output"))
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	printer := output.New(format, output.IsTTYWriter(w), w)

	stopQuery := printer.StartSpinner("Querying cluster version...")
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	var clusterVersion string

	// 1) Try reading version from the ClusterKubeconfig label on Greenhouse.
	// Use half the total timeout so the live-query fallback always has a
	// meaningful deadline even if the Greenhouse cluster is slow to respond.
	if cvGreenhouseNamespace != "" && cvGreenhouseClusterName != "" {
		labelCtx, labelCancel := context.WithTimeout(cmd.Context(), timeout/2)
		labelVer, labelErr := getVersionFromLabel(labelCtx, cvGreenhouseKubeconfig, cvGreenhouseContext, cvGreenhouseNamespace, cvGreenhouseClusterName)
		labelCancel()
		if labelErr != nil {
			slog.Debug("label-based version lookup failed, falling back to live query", "error", labelErr)
		} else if labelVer != "" {
			clusterVersion = normalizeVersion(labelVer)
		}
	}

	if clusterVersion == "" {
		// 2) Try unauthenticated GET /version
		var ver *version.Info
		ver, err = getUnauthenticatedVersion(ctx, cfg)
		if err != nil {
			// 3) Fallback to authenticated
			if !hasAuth(cfg) {
				stopQuery()
				return fmt.Errorf("no authentication methods found in your kubeconfig. Please authenticate (`kubelogin`, etc.) and try again")
			}

			ver, err = getAuthenticatedVersion(ctx, cfg)
			if err != nil {
				stopQuery()
				return fmt.Errorf("authenticated version fetch failed: %w", err)
			}
		}

		// Strip build metadata so we get a clean semver string (e.g. "1.29.3").
		clusterVersion = normalizeVersion(ver.GitVersion)
	}

	stopQuery()
	return printer.Print(output.ClusterVersionResult{Context: effectiveContext, Version: clusterVersion})
}

// normalizeVersion strips a leading "v", prerelease suffix, and build metadata
// from a Kubernetes version string, returning a clean semver (e.g. "1.29.3").
func normalizeVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	v = strings.Split(v, "-")[0]
	v = strings.Split(v, "+")[0]
	return v
}

// getVersionFromLabel reads the greenhouse.sap/kubernetes-version label from the
// named ClusterKubeconfig resource. Returns ("", nil) when the resource has no
// such label or when the resource is not found, so callers can fall through to
// a live query.
func getVersionFromLabel(ctx context.Context, greenhouseKubeconfig, greenhouseContext, namespace, clusterName string) (string, error) {
	cfg, err := configWithContext(greenhouseContext, greenhouseKubeconfig)
	if err != nil {
		return "", fmt.Errorf("failed to build greenhouse kubeconfig: %w", err)
	}

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return "", fmt.Errorf("failed to add greenhouse scheme: %w", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return "", fmt.Errorf("failed to create greenhouse client: %w", err)
	}

	return versionLabelFromClient(ctx, c, namespace, clusterName)
}

// versionLabelFromClient fetches the greenhouse.sap/kubernetes-version label
// using an already-constructed client. Separated for testability.
// Returns ("", nil) only on not-found; other errors (RBAC, network, timeout)
// are propagated so the caller can log them and fall back to a live query.
func versionLabelFromClient(ctx context.Context, c client.Client, namespace, clusterName string) (string, error) {
	var ckc v1alpha1.ClusterKubeconfig
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: clusterName}, &ckc); err != nil {
		return "", client.IgnoreNotFound(err)
	}
	return ckc.Labels["greenhouse.sap/kubernetes-version"], nil
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

	clusterVersionCmd.Flags().StringVarP(&cvGreenhouseKubeconfig, "greenhouse-cluster-kubeconfig", "g", clientcmd.RecommendedHomeFile, "Path to the Greenhouse cluster kubeconfig (for label-based version lookup)")
	clusterVersionCmd.Flags().StringVar(&cvGreenhouseContext, "greenhouse-cluster-context", "", "Context to use from the Greenhouse kubeconfig")
	clusterVersionCmd.Flags().StringVarP(&cvGreenhouseNamespace, "greenhouse-cluster-namespace", "n", "", "Greenhouse organization namespace")
	clusterVersionCmd.Flags().StringVar(&cvGreenhouseClusterName, "greenhouse-cluster-name", "", "ClusterKubeconfig resource name in Greenhouse to read the version label from")

	// BindPFlags can theoretically return an error if called with `nil` as an argument
	// which should never happen after at least one flag was defined. That's why the output
	// there is ignored.
	_ = viper.BindPFlags(clusterVersionCmd.Flags())
}
