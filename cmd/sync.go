// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	greenhousemetav1alpha1 "github.com/cloudoperators/greenhouse/api/meta/v1alpha1"
	"github.com/cloudoperators/greenhouse/api/v1alpha1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cloudoperators/cloudctl/cmd/output"
)

var (
	greenhouseClusterKubeconfig string
	greenhouseClusterContext    string
	greenhouseClusterNamespace  string
	remoteClusterKubeconfig     string
	remoteClusterName           string
	prefix                      string
	mergeIdenticalUsers         bool
	authType                    string
	kubeloginPath               string
	kubeloginExtraArgs          []string
	kubeloginTokenCacheDir      string
	dryRun                      bool
)

func init() {
	syncCmd.Flags().StringVarP(&greenhouseClusterKubeconfig, "greenhouse-cluster-kubeconfig", "k", clientcmd.RecommendedHomeFile, "Path to the Greenhouse cluster kubeconfig")
	syncCmd.Flags().StringVarP(&greenhouseClusterContext, "greenhouse-cluster-context", "c", "", "Context to use from the Greenhouse kubeconfig (defaults to current context)")
	syncCmd.Flags().StringVarP(&greenhouseClusterNamespace, "greenhouse-cluster-namespace", "n", "", "Greenhouse organization namespace (required)")
	if err := syncCmd.MarkFlagRequired("greenhouse-cluster-namespace"); err != nil {
		panic(err)
	}
	syncCmd.Flags().StringVarP(&remoteClusterKubeconfig, "remote-cluster-kubeconfig", "r", clientcmd.RecommendedHomeFile, "Local kubeconfig file to merge into")
	syncCmd.Flags().StringVar(&remoteClusterName, "remote-cluster-name", "", "Sync only this cluster by name (default: all ready clusters)")
	syncCmd.Flags().StringVar(&prefix, "prefix", "cloudctl", "Prefix applied to managed kubeconfig entries to avoid collisions")
	syncCmd.Flags().BoolVar(&mergeIdenticalUsers, "merge-identical-users", true, "Deduplicate auth entries that share the same OIDC config (single login for all such clusters)")

	// Authentication flags
	syncCmd.Flags().StringVar(&authType, "auth-type", "exec-plugin", "Auth credential style: exec-plugin (kubelogin) or auth-provider (legacy)")
	syncCmd.Flags().StringVar(&kubeloginPath, "kubelogin-path", "kubelogin", "Path to the kubelogin binary (used with --auth-type=exec-plugin)")
	syncCmd.Flags().StringSliceVar(&kubeloginExtraArgs, "kubelogin-extra-args", nil, "Additional arguments passed to the kubelogin exec plugin")
	defaultTokenCacheDir := os.Getenv("HOME")
	if defaultTokenCacheDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			defaultTokenCacheDir = home
		}
	}
	if defaultTokenCacheDir != "" {
		defaultTokenCacheDir = filepath.Join(defaultTokenCacheDir, ".kube", "cache", "oidc-login")
	} else {
		defaultTokenCacheDir = filepath.Join("~", ".kube", "cache", "oidc-login")
	}
	syncCmd.Flags().StringVar(&kubeloginTokenCacheDir, "kubelogin-token-cache-dir", defaultTokenCacheDir, "Directory for OIDC token cache files")

	syncCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview changes without writing to the kubeconfig file")

	// BindPFlags can theroretically return an error if called with `nil` as an argument
	// which should never happened after at least one flag was defined. That's why the output
	// there is ignored.
	viper.BindPFlags(syncCmd.Flags())
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync ClusterKubeconfigs from Greenhouse into your local kubeconfig",
	Long: `Fetches ClusterKubeconfig resources from a Greenhouse cluster and merges
them into your local kubeconfig file.

Only clusters whose Ready condition is True are merged. Clusters that have
been removed from Greenhouse are cleaned up from your local config. Existing
non-managed entries are never touched.

OIDC credentials are preserved across syncs: id-token and refresh-token are
carried forward so you do not need to re-authenticate after every sync.

Examples:
  # Sync all clusters for an organization
  cloudctl sync -n my-org

  # Sync a single cluster
  cloudctl sync -n my-org --remote-cluster-name prod-eu

  # Use a dedicated Greenhouse kubeconfig and emit JSON output
  cloudctl sync -n my-org -k ~/.kube/greenhouse.yaml -o json

  # Preview what would change without writing
  cloudctl sync -n my-org --dry-run

  # Debug mode — shows every cluster/authinfo/context decision on stderr
  cloudctl sync -n my-org --log-level debug`,
	RunE: runSync,
}

func runSync(cmd *cobra.Command, args []string) error {
	// Use viper as a source of configuration
	greenhouseClusterKubeconfig = viper.GetString("greenhouse-cluster-kubeconfig")
	greenhouseClusterContext = viper.GetString("greenhouse-cluster-context")
	greenhouseClusterNamespace = viper.GetString("greenhouse-cluster-namespace")
	remoteClusterKubeconfig = viper.GetString("remote-cluster-kubeconfig")
	remoteClusterName = viper.GetString("remote-cluster-name")
	prefix = viper.GetString("prefix")
	mergeIdenticalUsers = viper.GetBool("merge-identical-users")
	authType = viper.GetString("auth-type")
	kubeloginPath = viper.GetString("kubelogin-path")
	kubeloginExtraArgs = viper.GetStringSlice("kubelogin-extra-args")
	kubeloginTokenCacheDir = viper.GetString("kubelogin-token-cache-dir")
	dryRun = viper.GetBool("dry-run")

	format, err := output.ParseFormat(viper.GetString("output"))
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	printer := output.New(format, output.IsTTYWriter(w), w)

	if greenhouseClusterKubeconfig == "" {
		return fmt.Errorf("greenhouse cluster kubeconfig path is empty")
	}

	if _, err := os.Stat(greenhouseClusterKubeconfig); err != nil {
		return fmt.Errorf("greenhouse cluster kubeconfig file not found at %q: %w", greenhouseClusterKubeconfig, err)
	}

	if err := validateAuthType(authType, kubeloginPath); err != nil {
		return err
	}

	var (
		centralConfig *rest.Config
	)
	if greenhouseClusterContext != "" {
		centralConfig, err = configWithContext(greenhouseClusterContext, greenhouseClusterKubeconfig)
		if err != nil {
			return fmt.Errorf("failed to build greenhouse kubeconfig with context %q from %q: %w", greenhouseClusterContext, greenhouseClusterKubeconfig, err)
		}
	} else {
		centralConfig, err = clientcmd.BuildConfigFromFlags("", greenhouseClusterKubeconfig)
		if err != nil {
			return fmt.Errorf("failed to build greenhouse kubeconfig from %q: %w", greenhouseClusterKubeconfig, err)
		}
	}

	// Create a scheme and register Greenhouse types.
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add greenhouse scheme: %w", err)
	}

	// Create a typed client.
	c, err := client.New(centralConfig, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	ctx := cmd.Context()

	stopFetch := printer.StartSpinner("Fetching cluster kubeconfigs...")
	var allKubeconfigs []v1alpha1.ClusterKubeconfig

	// If a specific remote cluster name is provided, fetch that single resource;
	// otherwise, list all ClusterKubeconfigs in the given namespace.
	if remoteClusterName != "" {
		var ckc v1alpha1.ClusterKubeconfig
		if err := c.Get(ctx, client.ObjectKey{Namespace: greenhouseClusterNamespace, Name: remoteClusterName}, &ckc); err != nil {
			stopFetch()
			return fmt.Errorf("failed to get ClusterKubeconfig %q: %w", remoteClusterName, err)
		}
		allKubeconfigs = append(allKubeconfigs, ckc)
	} else {
		var list v1alpha1.ClusterKubeconfigList
		if err := c.List(ctx, &list, client.InNamespace(greenhouseClusterNamespace)); err != nil {
			stopFetch()
			return fmt.Errorf("failed to list ClusterKubeconfigs: %w", err)
		}
		allKubeconfigs = list.Items
	}
	stopFetch()

	ready, notReady := partitionReady(allKubeconfigs)

	if len(ready) == 0 {
		return printer.Print(buildSyncResult(nil, notReady))
	}

	localConfig, err := clientcmd.LoadFromFile(remoteClusterKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load local kubeconfig: %w", err)
	}

	if localConfig == nil {
		localConfig = clientcmdapi.NewConfig()
	}

	serverConfig, err := buildIncomingKubeconfig(ready)
	if err != nil {
		return fmt.Errorf("failed to create server config: %w", err)
	}

	// Take a snapshot before merge for dry-run diff.
	localConfigBefore := localConfig.DeepCopy()

	spinnerLabel := "Merging kubeconfigs..."
	if dryRun {
		spinnerLabel = "Simulating merge (dry-run)..."
	}
	stopMerge := printer.StartSpinner(spinnerLabel)
	err = mergeKubeconfig(localConfig, serverConfig)
	stopMerge()
	if err != nil {
		_ = printer.Print(buildFailedSyncResult(ready, notReady, err))
		return fmt.Errorf(`failed to merge ClusterKubeconfig: %w`, err)
	}

	if dryRun {
		diff := diffKubeconfig(localConfigBefore, localConfig)
		return printer.Print(buildDryRunResult(diff, localConfigBefore, localConfig))
	}

	if writeErr := writeConfig(localConfig, remoteClusterKubeconfig); writeErr != nil {
		_ = printer.Print(buildFailedSyncResult(ready, notReady, writeErr))
		return fmt.Errorf("failed to write merged kubeconfig: %w", writeErr)
	}

	return printer.Print(buildSyncResult(ready, notReady))
}

// partitionReady splits ClusterKubeconfigs into ready and notReady slices.
// Ready means the Ready condition is set to True.
func partitionReady(items []v1alpha1.ClusterKubeconfig) (ready, notReady []v1alpha1.ClusterKubeconfig) {
	for _, ckc := range items {
		cond := ckc.Status.Conditions.GetConditionByType(greenhousemetav1alpha1.ReadyCondition)
		if cond != nil && cond.IsTrue() {
			ready = append(ready, ckc)
		} else {
			notReady = append(notReady, ckc)
		}
	}
	return ready, notReady
}

// filterReady returns only ClusterKubeconfigs that have Ready condition set to True.
// Deprecated: use partitionReady instead.
func filterReady(items []v1alpha1.ClusterKubeconfig) []v1alpha1.ClusterKubeconfig {
	ready, _ := partitionReady(items)
	return ready
}

// buildSyncResult constructs an output.SyncResult from ready and notReady cluster lists.
func buildSyncResult(ready, notReady []v1alpha1.ClusterKubeconfig) output.SyncResult {
	result := output.SyncResult{}
	for _, ckc := range ready {
		ctxName := ""
		if len(ckc.Spec.Kubeconfig.Contexts) > 0 {
			ctxName = ckc.Spec.Kubeconfig.Contexts[0].Name
		}
		result.Clusters = append(result.Clusters, output.ClusterSyncResult{
			Name:    ckc.Name,
			Context: ctxName,
			Status:  output.ClusterSyncStatusSynced,
		})
		result.Synced++
	}
	for _, ckc := range notReady {
		ctxName := ""
		if len(ckc.Spec.Kubeconfig.Contexts) > 0 {
			ctxName = ckc.Spec.Kubeconfig.Contexts[0].Name
		}
		result.Clusters = append(result.Clusters, output.ClusterSyncResult{
			Name:    ckc.Name,
			Context: ctxName,
			Status:  output.ClusterSyncStatusSkipped,
			Reason:  "not ready",
		})
		result.Skipped++
	}
	if result.Clusters == nil {
		result.Clusters = []output.ClusterSyncResult{}
	}
	return result
}

// buildFailedSyncResult is like buildSyncResult but marks all ready clusters as failed
// with the given error as the reason. Used when either the merge or the kubeconfig write step fails.
func buildFailedSyncResult(ready, notReady []v1alpha1.ClusterKubeconfig, reason error) output.SyncResult {
	result := output.SyncResult{}
	msg := reason.Error()
	for _, ckc := range ready {
		ctxName := ""
		if len(ckc.Spec.Kubeconfig.Contexts) > 0 {
			ctxName = ckc.Spec.Kubeconfig.Contexts[0].Name
		}
		result.Clusters = append(result.Clusters, output.ClusterSyncResult{
			Name:    ckc.Name,
			Context: ctxName,
			Status:  output.ClusterSyncStatusFailed,
			Reason:  msg,
		})
		result.Failed++
	}
	for _, ckc := range notReady {
		ctxName := ""
		if len(ckc.Spec.Kubeconfig.Contexts) > 0 {
			ctxName = ckc.Spec.Kubeconfig.Contexts[0].Name
		}
		result.Clusters = append(result.Clusters, output.ClusterSyncResult{
			Name:    ckc.Name,
			Context: ctxName,
			Status:  output.ClusterSyncStatusSkipped,
			Reason:  "not ready",
		})
		result.Skipped++
	}
	if result.Clusters == nil {
		result.Clusters = []output.ClusterSyncResult{}
	}
	return result
}

// buildIncomingKubeconfig converts the list of typed ClusterKubeconfig objects
// into a clientcmdapi.Config.
func buildIncomingKubeconfig(items []v1alpha1.ClusterKubeconfig) (*clientcmdapi.Config, error) {
	kubeconfig := clientcmdapi.NewConfig()

	for _, ckc := range items {
		// Add all contexts
		for _, ctxItem := range ckc.Spec.Kubeconfig.Contexts {
			kubeconfig.Contexts[ctxItem.Name] = &clientcmdapi.Context{
				Cluster:   ctxItem.Context.Cluster,
				AuthInfo:  ctxItem.Context.AuthInfo,
				Namespace: ctxItem.Context.Namespace,
			}
		}

		// Add all users (auth infos)
		for _, authItem := range ckc.Spec.Kubeconfig.AuthInfo {
			// Depending on the selected auth type, keep legacy auth-provider or convert to exec plugin
			if strings.EqualFold(authType, "exec-plugin") && authItem.AuthInfo.AuthProvider.Name == "oidc" {
				execAuth := &clientcmdapi.AuthInfo{
					ClientCertificateData: authItem.AuthInfo.ClientCertificateData,
					ClientKeyData:         authItem.AuthInfo.ClientKeyData,
					Exec: &clientcmdapi.ExecConfig{
						APIVersion:      "client.authentication.k8s.io/v1",
						Command:         kubeloginPath,
						Args:            buildKubeloginArgs(authItem.AuthInfo.AuthProvider.Config, kubeloginExtraArgs, kubeloginTokenCacheDir),
						InteractiveMode: clientcmdapi.IfAvailableExecInteractiveMode,
					},
				}
				kubeconfig.AuthInfos[authItem.Name] = execAuth
			} else {
				// Preserve the same data shape; exclude nothing here (merging will handle dedupe)
				kubeconfig.AuthInfos[authItem.Name] = &clientcmdapi.AuthInfo{
					ClientCertificateData: authItem.AuthInfo.ClientCertificateData,
					ClientKeyData:         authItem.AuthInfo.ClientKeyData,
					AuthProvider:          &authItem.AuthInfo.AuthProvider,
				}
			}
		}

		// Add all clusters
		for _, clusterItem := range ckc.Spec.Kubeconfig.Clusters {

			kubeconfig.Clusters[clusterItem.Name] = &clientcmdapi.Cluster{
				Server:                   clusterItem.Cluster.Server,
				CertificateAuthorityData: clusterItem.Cluster.CertificateAuthorityData,
			}

			// Add/overwrite a "labels" named extension with the labels from the ClusterKubeconfig metadata.
			if len(ckc.Labels) > 0 {
				labelsJSON, err := json.Marshal(ckc.Labels)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal labels for cluster %q: %w", clusterItem.Name, err)
				}
				if kubeconfig.Clusters[clusterItem.Name].Extensions == nil {
					kubeconfig.Clusters[clusterItem.Name].Extensions = map[string]runtime.Object{}
				}
				kubeconfig.Clusters[clusterItem.Name].Extensions["labels"] = &runtime.Unknown{Raw: labelsJSON}
			}

		}
	}

	return kubeconfig, nil
}

func writeConfig(config *clientcmdapi.Config, filepath string) error {
	if err := clientcmd.WriteToFile(*config, filepath); err != nil {
		return fmt.Errorf("failed to write kubeconfig to %s: %w", filepath, err)
	}
	return nil
}

// managedNameFunc prefixes the given name with the configured prefix.
func managedNameFunc(name string) string {
	return fmt.Sprintf("%s:%s", prefix, name)
}

// unmanagedNameFunc removes the prefix from the given managed name.
// Returns the raw server-side name.
func unmanagedNameFunc(managedName string) string {
	return strings.TrimPrefix(managedName, prefix+":")
}

// isManaged checks if the given name is managed by cloudctl based on the prefix.
func isManaged(name string) bool {
	return strings.HasPrefix(name, prefix+":")
}

// buildKubeloginArgs constructs kubelogin arguments from an oidc auth-provider config and extra args
func buildKubeloginArgs(cfg map[string]string, extra []string, tokenCacheDir string) []string {
	args := []string{"get-token"}
	if v := cfg["idp-issuer-url"]; v != "" {
		args = append(args, "--oidc-issuer-url="+v)
	}
	if v := cfg["client-id"]; v != "" {
		args = append(args, "--oidc-client-id="+v)
	}
	if v := cfg["client-secret"]; v != "" {
		args = append(args, "--oidc-client-secret="+v)
	}
	if v := cfg["extra-scopes"]; v != "" {
		for _, s := range strings.Split(v, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				args = append(args, "--oidc-extra-scope="+s)
			}
		}
	}
	if v := cfg["auth-request-extra-params"]; v != "" {
		args = append(args, "--oidc-auth-request-extra-params="+v)
		// If connector_id is used, use a separate token cache directory to avoid collisions
		// between multiple users on the same machine.
		// See https://github.com/int128/kubelogin/issues/29
		for _, param := range strings.Split(v, ",") {
			kv := strings.SplitN(param, "=", 2)
			if len(kv) == 2 && strings.TrimSpace(kv[0]) == "connector_id" {
				connectorID := strings.TrimSpace(kv[1])
				if connectorID != "" {
					args = append(args, fmt.Sprintf("--token-cache-dir=%s/%s", tokenCacheDir, connectorID))
				}
				break
			}
		}
	}
	// allow caller to inject additional flags
	if len(extra) > 0 {
		args = append(args, extra...)
	}
	return args
}

func mergeKubeconfig(localConfig *clientcmdapi.Config, serverConfig *clientcmdapi.Config) error {
	// Merge Clusters
	for serverName, serverCluster := range serverConfig.Clusters {
		managedName := managedNameFunc(serverName)
		localCluster, exists := localConfig.Clusters[managedName]
		if !exists {
			// Add the managed cluster from serverConfig to localConfig
			slog.Debug("adding cluster", "name", managedName)
			localConfig.Clusters[managedName] = serverCluster
		} else {
			// Check if Server, CertificateAuthorityData or the labels extension has changed
			if localCluster.Server != serverCluster.Server ||
				!bytes.Equal(localCluster.CertificateAuthorityData, serverCluster.CertificateAuthorityData) ||
				!labelsExtensionEqual(localCluster.Extensions, serverCluster.Extensions) {
				slog.Debug("updating cluster", "name", managedName)
				localConfig.Clusters[managedName] = serverCluster
			} else {
				slog.Debug("cluster unchanged", "name", managedName)
			}
		}
	}

	// Prepare a map to track unique AuthInfos if merging is enabled
	var authInfoMap map[string]string // key: unique identifier, value: managed AuthInfo name
	if mergeIdenticalUsers {
		authInfoMap = make(map[string]string)

		// Build a reverse lookup of unmanaged local auth entries so we can reuse their names
		// instead of creating new cloudctl:auth-<hash> entries.
		// Collect all names per key first, then pick the lexicographically smallest to ensure
		// deterministic reuse when multiple unmanaged entries share the same key.
		keyToNames := make(map[string][]string)
		for localName, localAuth := range localConfig.AuthInfos {
			if !isManaged(localName) {
				key := generateAuthInfoKey(localAuth)
				keyToNames[key] = append(keyToNames[key], localName)
			}
		}
		existingLocalKeys := make(map[string]string, len(keyToNames)) // authInfoKey → local name
		for key, names := range keyToNames {
			existingLocalKeys[key] = slices.Min(names)
		}

		// Merge AuthInfos
		for serverName, serverAuth := range serverConfig.AuthInfos {
			uniqueKey := generateAuthInfoKey(serverAuth)

			// If an unmanaged local entry has the same credentials, reuse its name.
			// Gate on authInfoEqual (excluding tokens) so we don't rewire a context
			// to an unmanaged entry whose non-OIDC fields differ from the server config.
			if localName, found := existingLocalKeys[uniqueKey]; found {
				localAuth := localConfig.AuthInfos[localName]
				if authInfoEqual(localAuth, serverAuth) {
					slog.Debug("reusing existing local authinfo", "name", localName, "server", serverName)
					// Merge server config into the local entry to pick up any server-side
					// changes while preserving local tokens.
					localConfig.AuthInfos[localName] = mergeAuthInfo(serverAuth, localAuth)
					authInfoMap[uniqueKey] = localName
					continue
				}
			}

			hash := sha256.Sum256([]byte(uniqueKey))
			hashString := hex.EncodeToString(hash[:])[:16] // Using the first 16 chars for brevity
			managedAuthName := fmt.Sprintf("%s:auth-%s", prefix, hashString)

			// Merge AuthInfo to preserve id-token and refresh-token
			if existingAuth, exists := localConfig.AuthInfos[managedAuthName]; exists {
				slog.Debug("merging authinfo tokens", "name", managedAuthName, "server", serverName)
				mergedAuth := mergeAuthInfo(serverAuth, existingAuth)
				localConfig.AuthInfos[managedAuthName] = mergedAuth
			} else {
				slog.Debug("adding authinfo", "name", managedAuthName, "server", serverName)
				localConfig.AuthInfos[managedAuthName] = serverAuth
			}

			authInfoMap[uniqueKey] = managedAuthName
		}
	} else {
		// Without merging, manage AuthInfos normally
		for serverName, serverAuth := range serverConfig.AuthInfos {
			managedAuthName := managedNameFunc(serverName)
			localAuth, exists := localConfig.AuthInfos[managedAuthName]
			if !exists {
				slog.Debug("adding authinfo", "name", managedAuthName)
				localConfig.AuthInfos[managedAuthName] = serverAuth
			} else {
				if !authInfoEqual(localAuth, serverAuth) {
					// Merge AuthInfo to preserve id-token and refresh-token
					slog.Debug("updating authinfo", "name", managedAuthName)
					mergedAuth := mergeAuthInfo(serverAuth, localAuth)
					localConfig.AuthInfos[managedAuthName] = mergedAuth
				}
			}
		}
	}

	// Merge Contexts
	for serverName, serverCtx := range serverConfig.Contexts {
		managedName := serverName // it is the same for context

		var managedAuthInfoName string
		if mergeIdenticalUsers {
			// Generate the unique key for the AuthInfo referenced by this context
			serverAuthName := serverCtx.AuthInfo
			serverAuth, exists := serverConfig.AuthInfos[serverAuthName]
			if !exists {
				return fmt.Errorf("AuthInfo %s referenced in context %s does not exist", serverAuthName, serverName)
			}
			uniqueKey := generateAuthInfoKey(serverAuth)
			var existsInMap bool
			managedAuthInfoName, existsInMap = authInfoMap[uniqueKey]
			if !existsInMap {
				// This should not happen as all AuthInfos should have been processed.
				// However, to be safe, generate a new managedAuthName
				hash := sha256.Sum256([]byte(uniqueKey))
				hashString := hex.EncodeToString(hash[:])[:16]
				managedAuthInfoName = fmt.Sprintf("%s:auth-%s", prefix, hashString)
				authInfoMap[uniqueKey] = managedAuthInfoName
				localConfig.AuthInfos[managedAuthInfoName] = serverAuth
			}
		} else {
			managedAuthInfoName = managedNameFunc(serverCtx.AuthInfo)
		}

		// Update Cluster name
		managedClusterName := managedNameFunc(serverCtx.Cluster)

		serverCtxCopy := serverCtx.DeepCopy()
		serverCtxCopy.Cluster = managedClusterName
		serverCtxCopy.AuthInfo = managedAuthInfoName

		localCtx, exists := localConfig.Contexts[managedName]
		if !exists {
			// Add the managed Context from serverConfig to localConfig
			slog.Debug("adding context", "name", managedName)
			localConfig.Contexts[managedName] = serverCtxCopy
		} else {
			// Check if Cluster, AuthInfo, or Namespace has changed
			if localCtx.Cluster != serverCtxCopy.Cluster ||
				localCtx.AuthInfo != serverCtxCopy.AuthInfo ||
				localCtx.Namespace != serverCtxCopy.Namespace {
				slog.Debug("updating context", "name", managedName)
				localConfig.Contexts[managedName] = serverCtxCopy
			}
		}
	}

	// Delete managed Clusters not present in serverConfig
	for localName := range localConfig.Clusters {
		if isManaged(localName) {
			// Derive the server-side name by stripping the prefix
			serverName := unmanagedNameFunc(localName)
			if _, exists := serverConfig.Clusters[serverName]; !exists {
				slog.Debug("removing stale cluster", "name", localName)
				delete(localConfig.Clusters, localName)
			}
		}
	}

	// Delete managed AuthInfos not present in serverConfig
	for localName := range localConfig.AuthInfos {
		if isManaged(localName) {
			if mergeIdenticalUsers {
				// If merging, keep AuthInfos that are mapped
				found := false
				for _, name := range authInfoMap {
					if name == localName {
						found = true
						break
					}
				}
				if !found {
					slog.Debug("removing stale authinfo", "name", localName)
					delete(localConfig.AuthInfos, localName)
				}
			} else {
				// Derive the server-side name by stripping the prefix
				serverName := unmanagedNameFunc(localName)
				if _, exists := serverConfig.AuthInfos[serverName]; !exists {
					slog.Debug("removing stale authinfo", "name", localName)
					delete(localConfig.AuthInfos, localName)
				}
			}
		}
	}

	// Delete managed Contexts not present in serverConfig
	for localName, localCtx := range localConfig.Contexts {
		if isManaged(localName) {
			// Derive the server-side name by stripping the prefix
			serverName := unmanagedNameFunc(localName)
			if _, exists := serverConfig.Contexts[serverName]; !exists {
				slog.Debug("removing stale context", "name", localName)
				delete(localConfig.Contexts, localName)
			} else {
				// Additionally, verify that the context's Cluster and AuthInfo are still managed
				serverCtx := serverConfig.Contexts[serverName]
				expectedCluster := managedNameFunc(serverCtx.Cluster)
				var expectedAuthInfo string
				if mergeIdenticalUsers {
					serverAuthName := serverCtx.AuthInfo
					serverAuth, exists := serverConfig.AuthInfos[serverAuthName]
					if !exists {
						slog.Debug("removing stale context (missing authinfo)", "name", localName)
						delete(localConfig.Contexts, localName)
						continue
					}
					uniqueKey := generateAuthInfoKey(serverAuth)
					mappedName, exists := authInfoMap[uniqueKey]
					if !exists {
						slog.Debug("removing stale context (unmapped authinfo)", "name", localName)
						delete(localConfig.Contexts, localName)
						continue
					}
					expectedAuthInfo = mappedName
				} else {
					expectedAuthInfo = managedNameFunc(serverCtx.AuthInfo)
				}

				if localCtx.Cluster != expectedCluster || localCtx.AuthInfo != expectedAuthInfo {
					slog.Debug("removing stale context (mismatched refs)", "name", localName)
					delete(localConfig.Contexts, localName)
				}
			}
		}
	}

	return nil
}

// labelsExtensionEqual returns true if the "labels" named extension is equal in both maps.
func labelsExtensionEqual(a, b map[string]runtime.Object) bool {
	ar := extensionRaw(a, "labels")
	br := extensionRaw(b, "labels")
	return bytes.Equal(ar, br)
}

// extensionRaw extracts the raw JSON bytes for the given extension name, if present.
func extensionRaw(m map[string]runtime.Object, name string) []byte {
	if m == nil {
		return nil
	}
	obj, ok := m[name]
	if !ok || obj == nil {
		return nil
	}
	switch t := obj.(type) {
	case *runtime.Unknown:
		return bytes.TrimSpace(t.Raw)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return nil
		}
		return bytes.TrimSpace(b)
	}
}

// validateAuthType checks that authType is one of the accepted values and, when
// exec-plugin is selected, that the kubelogin binary is resolvable.
func validateAuthType(authType, kubeloginPath string) error {
	switch strings.ToLower(authType) {
	case "auth-provider":
		return nil
	case "exec-plugin":
		if _, err := exec.LookPath(kubeloginPath); err != nil {
			return fmt.Errorf("could not resolve kubelogin binary %q: install kubelogin or set --kubelogin-path, or use --auth-type=auth-provider: %w", kubeloginPath, err)
		}
		return nil
	default:
		return fmt.Errorf("invalid --auth-type %q: must be one of \"auth-provider\" or \"exec-plugin\"", authType)
	}
}
