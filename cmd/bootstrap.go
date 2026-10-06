// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/cloudoperators/cloudctl/cmd/output"
)

var (
	bootstrapData          string
	bootstrapServer        string
	bootstrapOrg           string
	bootstrapCAData        string
	bootstrapIDPIssuerURL  string
	bootstrapClientID      string
	bootstrapClientSecret  string
	bootstrapExtraScopes   string
	bootstrapNamespace     string
	bootstrapKubeconfig    string
	bootstrapContextName   string
	bootstrapSetCurrentCtx bool
	bootstrapDryRun        bool
)

func init() {
	bootstrapCmd.Flags().StringVar(&bootstrapData, "data", "", "Base64-encoded kubeconfig (as downloaded from the Greenhouse Web UI)")
	bootstrapCmd.Flags().StringVar(&bootstrapServer, "greenhouse-server", "", "Greenhouse API server URL (e.g. https://greenhouse.example.com)")
	bootstrapCmd.Flags().StringVar(&bootstrapOrg, "greenhouse-org", "", "Greenhouse organization name")
	bootstrapCmd.Flags().StringVar(&bootstrapCAData, "greenhouse-ca-data", "", "Base64-encoded CA certificate data for the Greenhouse server")
	bootstrapCmd.Flags().StringVar(&bootstrapIDPIssuerURL, "greenhouse-idp-issuer-url", "", "OIDC issuer URL (e.g. https://idp.example.com)")
	bootstrapCmd.Flags().StringVar(&bootstrapClientID, "greenhouse-client-id", "", "OIDC client ID")
	bootstrapCmd.Flags().StringVar(&bootstrapClientSecret, "greenhouse-client-secret", "", "OIDC client secret (optional)")
	bootstrapCmd.Flags().StringVar(&bootstrapExtraScopes, "greenhouse-extra-scopes", "", "Comma-separated extra OIDC scopes (optional, e.g. groups)")
	bootstrapCmd.Flags().StringVar(&bootstrapNamespace, "greenhouse-namespace", "", "Kubernetes namespace to set in the context (defaults to --greenhouse-org)")
	bootstrapCmd.Flags().StringVar(&bootstrapKubeconfig, "kubeconfig", clientcmd.RecommendedHomeFile, "Path to the local kubeconfig file to merge into")
	bootstrapCmd.Flags().StringVar(&bootstrapContextName, "context-name", "", "Context name to use in the local kubeconfig (default: from the downloaded kubeconfig or greenhouse-<org>)")
	bootstrapCmd.Flags().BoolVar(&bootstrapSetCurrentCtx, "set-current-context", false, "Set the bootstrapped context as the current context")
	bootstrapCmd.Flags().BoolVar(&bootstrapDryRun, "dry-run", false, "Preview what would be written without touching the kubeconfig file")

	_ = viper.BindPFlags(bootstrapCmd.Flags())

	rootCmd.AddCommand(bootstrapCmd)
}

var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Bootstrap first-time access to a Greenhouse cluster",
	Long: `Merges a Greenhouse kubeconfig into your local kubeconfig so you can
reach the Greenhouse API server with kubectl or cloudctl.

Two input modes are supported:

  Mode 1 — kubeconfig blob from the Greenhouse Web UI (recommended):
    cloudctl bootstrap --data=<base64-kubeconfig>

  The --data value is a standard kubeconfig file base64-encoded.
  You can generate it yourself with:
    base64 < ~/Downloads/greenhouse-my-org.kubeconfig

  Mode 2 — individual flags (OIDC, scriptable):
    cloudctl bootstrap \
      --greenhouse-server=https://greenhouse.example.com \
      --greenhouse-org=my-org \
      --greenhouse-idp-issuer-url=https://idp.example.com \
      --greenhouse-client-id=greenhouse \
      --greenhouse-extra-scopes=groups \
      --greenhouse-ca-data=<base64-ca>

Both modes ask interactively for the context name and whether to set it as
the current context, unless --context-name and --set-current-context are given.
Running twice with the same input is safe (idempotent).

Examples:
  # Bootstrap from a kubeconfig downloaded from the Web UI
  cloudctl bootstrap --data=$(base64 < greenhouse-my-org.kubeconfig)

  # Bootstrap from individual flags (non-interactive)
  cloudctl bootstrap \
    --greenhouse-server=https://greenhouse.example.com \
    --greenhouse-org=my-org \
    --greenhouse-idp-issuer-url=https://idp.example.com \
    --greenhouse-client-id=greenhouse \
    --greenhouse-extra-scopes=groups \
    --context-name=greenhouse-my-org \
    --set-current-context

  # Preview what would change without writing
  cloudctl bootstrap --data=<blob> --dry-run`,
	RunE: runBootstrap,
}

func runBootstrap(cmd *cobra.Command, args []string) error {
	bootstrapData = viper.GetString("data")
	bootstrapServer = viper.GetString("greenhouse-server")
	bootstrapOrg = viper.GetString("greenhouse-org")
	bootstrapCAData = viper.GetString("greenhouse-ca-data")
	bootstrapIDPIssuerURL = viper.GetString("greenhouse-idp-issuer-url")
	bootstrapClientID = viper.GetString("greenhouse-client-id")
	bootstrapClientSecret = viper.GetString("greenhouse-client-secret")
	bootstrapExtraScopes = viper.GetString("greenhouse-extra-scopes")
	bootstrapNamespace = viper.GetString("greenhouse-namespace")
	// Use the flag value directly; only fall back to KUBECONFIG/default when not explicitly set.
	if cmd.Flags().Changed("kubeconfig") {
		bootstrapKubeconfig, _ = cmd.Flags().GetString("kubeconfig")
	} else if kc := os.Getenv("KUBECONFIG"); kc != "" {
		if parts := strings.SplitN(kc, string(os.PathListSeparator), 2); len(parts) > 0 && parts[0] != "" {
			bootstrapKubeconfig = parts[0]
		} else {
			return fmt.Errorf("cannot determine write target: KUBECONFIG=%q contains no usable first path", kc)
		}
	} else {
		bootstrapKubeconfig = clientcmd.RecommendedHomeFile
	}
	bootstrapContextName = viper.GetString("context-name")
	bootstrapSetCurrentCtx = viper.GetBool("set-current-context")
	// Read dry-run directly from the flag to avoid viper cross-command pollution
	// (sync also binds "dry-run" to the global viper instance).
	bootstrapDryRun, _ = cmd.Flags().GetBool("dry-run")

	format, err := output.ParseFormat(viper.GetString("output"))
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	printer := output.New(format, output.IsTTYWriter(w), w)

	incoming, org, err := resolveIncomingKubeconfig()
	if err != nil {
		return err
	}

	// Determine default context name from the incoming config or the org.
	defaultCtxName := incoming.CurrentContext
	if defaultCtxName == "" && org != "" {
		defaultCtxName = fmt.Sprintf("greenhouse-%s", org)
	}
	if defaultCtxName == "" && len(incoming.Contexts) == 1 {
		for name := range incoming.Contexts {
			defaultCtxName = name
		}
	}
	if defaultCtxName == "" {
		defaultCtxName = "greenhouse"
	}

	// Capture org from the selected context's namespace before any rename, so
	// the sync hint is correct even when --context-name differs from greenhouse-<org>.
	if org == "" {
		if ctxEntry, ok := incoming.Contexts[defaultCtxName]; ok && ctxEntry.Namespace != "" {
			org = ctxEntry.Namespace
		}
	}

	contextName := bootstrapContextName
	if contextName == "" {
		contextName = defaultCtxName
	}

	// Prompt interactively for context name when running on a TTY and the flag wasn't set.
	if !cmd.Flags().Changed("context-name") && output.IsTTYWriter(w) {
		contextName, err = promptContextName(contextName)
		if err != nil {
			return err
		}
	}

	setCurrentCtx := bootstrapSetCurrentCtx
	if !cmd.Flags().Changed("set-current-context") && output.IsTTYWriter(w) {
		setCurrentCtx, err = promptYesNo(fmt.Sprintf("Set %q as the current context?", contextName), false)
		if err != nil {
			return err
		}
	}

	// Rename entries in the incoming config to use the chosen context name.
	renameKubeconfigContext(incoming, contextName)

	// Load the config that will be mutated and written back (first file only).
	// When KUBECONFIG contains multiple files we also build a merged view used
	// solely for collision detection — we never write the merged object back so
	// unmanaged entries in other files are not copied into the first file.
	var localConfig *clientcmdapi.Config
	if _, statErr := os.Stat(bootstrapKubeconfig); statErr == nil {
		localConfig, err = clientcmd.LoadFromFile(bootstrapKubeconfig)
		if err != nil {
			return fmt.Errorf("failed to load local kubeconfig %q: %w", bootstrapKubeconfig, err)
		}
	}
	if localConfig == nil {
		localConfig = clientcmdapi.NewConfig()
	}

	// When --kubeconfig was not explicitly set, also load the merged view of all
	// KUBECONFIG files so we can detect collisions with entries in other files.
	mergedView := localConfig
	if !cmd.Flags().Changed("kubeconfig") {
		mv, mvErr := clientcmd.NewDefaultClientConfigLoadingRules().Load()
		if mvErr != nil {
			return fmt.Errorf("failed to load kubeconfig: %w", mvErr)
		}
		mergedView = mv
	}

	result, err := mergeBootstrapKubeconfig(localConfig, mergedView, incoming, contextName, setCurrentCtx, org)
	if err != nil {
		return err
	}

	if bootstrapDryRun {
		result.DryRun = true
		return printer.Print(result)
	}

	if err := writeConfig(localConfig, bootstrapKubeconfig); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	result.KubeconfigPath = bootstrapKubeconfig
	return printer.Print(result)
}

// resolveIncomingKubeconfig returns a clientcmdapi.Config from either the --data blob
// or the individual --greenhouse-* flags. It also returns the org name (may be empty
// when derived from a blob with no org context).
func resolveIncomingKubeconfig() (*clientcmdapi.Config, string, error) {
	if bootstrapData != "" {
		raw, err := base64.StdEncoding.DecodeString(bootstrapData)
		if err != nil {
			raw, err = base64.RawStdEncoding.DecodeString(bootstrapData)
			if err != nil {
				return nil, "", fmt.Errorf("--data is not valid base64: %w", err)
			}
		}
		cfg, err := clientcmd.Load(raw)
		if err != nil {
			return nil, "", fmt.Errorf("--data does not contain a valid kubeconfig: %w", err)
		}
		if len(cfg.Clusters) == 0 {
			return nil, "", fmt.Errorf("--data kubeconfig contains no clusters")
		}
		// Verify we can identify exactly which context to use.
		if cfg.CurrentContext == "" && len(cfg.Contexts) != 1 {
			return nil, "", fmt.Errorf("--data kubeconfig has no current-context and contains %d contexts; set one explicitly with kubectl config use-context", len(cfg.Contexts))
		}
		activeCtx := cfg.CurrentContext
		if activeCtx == "" {
			for name := range cfg.Contexts {
				activeCtx = name
			}
		}
		if ctx, ok := cfg.Contexts[activeCtx]; !ok || ctx == nil {
			return nil, "", fmt.Errorf("--data kubeconfig current-context %q not found in contexts", activeCtx)
		} else if _, clOK := cfg.Clusters[ctx.Cluster]; !clOK {
			return nil, "", fmt.Errorf("--data kubeconfig context %q references unknown cluster %q", activeCtx, ctx.Cluster)
		} else if _, aiOK := cfg.AuthInfos[ctx.AuthInfo]; !aiOK {
			return nil, "", fmt.Errorf("--data kubeconfig context %q references unknown user %q", activeCtx, ctx.AuthInfo)
		}
		return cfg, bootstrapOrg, nil
	}

	// Individual flags path.
	if bootstrapServer == "" {
		return nil, "", fmt.Errorf("one of --data or --greenhouse-server is required")
	}
	if bootstrapOrg == "" {
		return nil, "", fmt.Errorf("--greenhouse-org is required when not using --data")
	}
	if bootstrapIDPIssuerURL == "" {
		return nil, "", fmt.Errorf("--greenhouse-idp-issuer-url is required when not using --data")
	}
	if bootstrapClientID == "" {
		return nil, "", fmt.Errorf("--greenhouse-client-id is required when not using --data")
	}

	cfg, err := buildOIDCKubeconfig(bootstrapServer, bootstrapOrg, bootstrapCAData, bootstrapIDPIssuerURL, bootstrapClientID, bootstrapClientSecret, bootstrapExtraScopes, bootstrapNamespace)
	if err != nil {
		return nil, "", err
	}
	return cfg, bootstrapOrg, nil
}

// buildOIDCKubeconfig constructs a kubeconfig matching the Greenhouse auth-provider shape:
//
//	users:
//	- name: greenhouse-<org>
//	  user:
//	    auth-provider:
//	      name: oidc
//	      config:
//	        idp-issuer-url: ...
//	        client-id: ...
//	        client-secret: ...   (always present; may be empty string)
//	        extra-scopes: ...    (omitted when empty)
func buildOIDCKubeconfig(server, org, caDataB64, idpIssuerURL, clientID, clientSecret, extraScopes, namespace string) (*clientcmdapi.Config, error) {
	name := fmt.Sprintf("greenhouse-%s", org)

	cluster := &clientcmdapi.Cluster{Server: server}
	if caDataB64 != "" {
		caBytes, err := base64.StdEncoding.DecodeString(caDataB64)
		if err != nil {
			caBytes, err = base64.RawStdEncoding.DecodeString(caDataB64)
			if err != nil {
				return nil, fmt.Errorf("--greenhouse-ca-data is not valid base64: %w", err)
			}
		}
		cluster.CertificateAuthorityData = caBytes
	}

	oidcConfig := map[string]string{
		"idp-issuer-url": idpIssuerURL,
		"client-id":      clientID,
		"client-secret":  clientSecret,
	}
	if extraScopes != "" {
		oidcConfig["extra-scopes"] = extraScopes
	}

	ns := namespace
	if ns == "" {
		ns = org
	}

	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[name] = cluster
	cfg.AuthInfos[name] = &clientcmdapi.AuthInfo{
		AuthProvider: &clientcmdapi.AuthProviderConfig{
			Name:   "oidc",
			Config: oidcConfig,
		},
	}
	cfg.Contexts[name] = &clientcmdapi.Context{
		Cluster:   name,
		AuthInfo:  name,
		Namespace: ns,
	}
	cfg.CurrentContext = name
	return cfg, nil
}

// renameKubeconfigContext renames the active context (and its referenced cluster/authinfo)
// in cfg to targetName. When there is exactly one context it is always the one renamed.
// All other entries (multiple contexts in a blob) are left untouched.
// Old cluster/authinfo keys are only removed when no other context still references them.
func renameKubeconfigContext(cfg *clientcmdapi.Config, targetName string) {
	// Identify which context to rename: prefer CurrentContext, fall back to the only one.
	source := cfg.CurrentContext
	if _, ok := cfg.Contexts[source]; !ok {
		if len(cfg.Contexts) == 1 {
			for name := range cfg.Contexts {
				source = name
			}
		}
	}
	if source == "" {
		cfg.CurrentContext = targetName
		return
	}

	ctx := cfg.Contexts[source]
	if ctx == nil {
		cfg.CurrentContext = targetName
		return
	}

	oldCluster := ctx.Cluster
	oldAuth := ctx.AuthInfo

	// Only delete the old cluster key if no other context (other than source) references it.
	clusterRefCount := 0
	for ctxName, c := range cfg.Contexts {
		if ctxName != source && c != nil && c.Cluster == oldCluster {
			clusterRefCount++
		}
	}

	// Rename cluster (when it differs from the target name).
	if cl, ok := cfg.Clusters[oldCluster]; ok && oldCluster != targetName {
		cfg.Clusters[targetName] = cl
		ctx.Cluster = targetName
		if clusterRefCount == 0 {
			delete(cfg.Clusters, oldCluster)
		}
	}

	// Only delete the old authinfo key if no other context (other than source) references it.
	authRefCount := 0
	for ctxName, c := range cfg.Contexts {
		if ctxName != source && c != nil && c.AuthInfo == oldAuth {
			authRefCount++
		}
	}

	// Rename authinfo (when it differs from the target name).
	if ai, ok := cfg.AuthInfos[oldAuth]; ok && oldAuth != targetName {
		cfg.AuthInfos[targetName] = ai
		ctx.AuthInfo = targetName
		if authRefCount == 0 {
			delete(cfg.AuthInfos, oldAuth)
		}
	}

	// Rename context key when it differs; otherwise just update CurrentContext.
	if source != targetName {
		cfg.Contexts[targetName] = ctx
		delete(cfg.Contexts, source)
	}
	cfg.CurrentContext = targetName
}

// mergeBootstrapKubeconfig merges incoming into localConfig (the file that will
// be written). collisionView is used for existence checks — when KUBECONFIG
// spans multiple files it is the merged view of all of them, so we detect
// collisions with entries in other files without copying those entries into the
// first file.
func mergeBootstrapKubeconfig(localConfig, collisionView, incoming *clientcmdapi.Config, ctxName string, setCurrentCtx bool, org string) (output.BootstrapResult, error) {
	result := output.BootstrapResult{
		ContextName:  ctxName,
		SetAsCurrent: setCurrentCtx,
		Org:          org,
	}

	for name, cluster := range incoming.Clusters {
		if _, exists := collisionView.Clusters[name]; !exists {
			localConfig.Clusters[name] = cluster
			result.Added = append(result.Added, fmt.Sprintf("cluster %q", name))
		} else {
			result.Skipped = append(result.Skipped, fmt.Sprintf("cluster %q (already exists)", name))
		}
	}

	for name, auth := range incoming.AuthInfos {
		if _, exists := collisionView.AuthInfos[name]; !exists {
			localConfig.AuthInfos[name] = auth
			result.Added = append(result.Added, fmt.Sprintf("user %q", name))
		} else {
			result.Skipped = append(result.Skipped, fmt.Sprintf("user %q (already exists)", name))
		}
	}

	for name, ctx := range incoming.Contexts {
		if _, exists := collisionView.Contexts[name]; !exists {
			localConfig.Contexts[name] = ctx
			result.Added = append(result.Added, fmt.Sprintf("context %q", name))
		} else {
			result.Skipped = append(result.Skipped, fmt.Sprintf("context %q (already exists)", name))
		}
	}

	if setCurrentCtx && localConfig.CurrentContext != ctxName {
		localConfig.CurrentContext = ctxName
		result.CurrentContextUpdated = true
	}

	return result, nil
}

// promptContextName reads a context name from stdin, returning defaultName on empty input.
func promptContextName(defaultName string) (string, error) {
	fmt.Fprintf(os.Stderr, "Context name [%s]: ", defaultName)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("failed to read context name: %w", err)
		}
		return defaultName, nil
	}
	if val := strings.TrimSpace(scanner.Text()); val != "" {
		return val, nil
	}
	return defaultName, nil
}

// promptYesNo asks a yes/no question; defaultVal is used on empty input or unrecognised input.
func promptYesNo(question string, defaultVal bool) (bool, error) {
	hint := "y/N"
	if defaultVal {
		hint = "Y/n"
	}
	fmt.Fprintf(os.Stderr, "%s [%s]: ", question, hint)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, fmt.Errorf("failed to read answer: %w", err)
		}
		return defaultVal, nil
	}
	switch strings.TrimSpace(strings.ToLower(scanner.Text())) {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return defaultVal, nil
	}
}
