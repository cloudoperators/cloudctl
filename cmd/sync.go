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
	includeNotReady             bool
)

func init() {
	syncCmd.Flags().StringVarP(&greenhouseClusterKubeconfig, "greenhouse-cluster-kubeconfig", "k", clientcmd.RecommendedHomeFile, "Path to the Greenhouse cluster kubeconfig")
	syncCmd.Flags().StringVarP(&greenhouseClusterContext, "greenhouse-cluster-context", "c", "", "Context to use from the Greenhouse kubeconfig (defaults to current context)")
	syncCmd.Flags().StringVarP(&greenhouseClusterNamespace, "greenhouse-cluster-namespace", "n", "", "Greenhouse organization namespace")
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
	syncCmd.Flags().BoolVar(&includeNotReady, "include-not-ready", false, "Also merge clusters whose Ready condition is False (spec data must be present)")

	// BindPFlags can theoretically return an error if called with `nil` as an argument
	// which should never happen after at least one flag was defined. That's why the output
	// there is ignored.
	_ = viper.BindPFlags(syncCmd.Flags())
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync ClusterKubeconfigs from Greenhouse into your local kubeconfig",
	Long: `Fetches ClusterKubeconfig resources from a Greenhouse cluster and merges
them into your local kubeconfig file.

Only clusters whose Ready condition is True are merged by default. Clusters
whose Ready condition is False are preserved in the local kubeconfig so they
remain reachable (e.g. to investigate or recover the cluster). Use
--include-not-ready to also merge their spec data.

Clusters that have been removed from Greenhouse are cleaned up from your
local config. Existing non-managed entries are never touched.

OIDC credentials are preserved across syncs: id-token and refresh-token are
carried forward so you do not need to re-authenticate after every sync.

One-time setup: persist connection defaults in ~/.cloudctl.yaml so you can
run "cloudctl sync" with no flags:

  greenhouse-cluster-namespace: my-org
  greenhouse-cluster-context: greenhouse
  greenhouse-cluster-kubeconfig: ~/.kube/greenhouse.yaml   # tilde is expanded

Examples:
  # Sync all clusters for an organization
  cloudctl sync -n my-org

  # Sync a single cluster
  cloudctl sync -n my-org --remote-cluster-name prod-eu

  # Use a dedicated Greenhouse kubeconfig and emit JSON output
  cloudctl sync -n my-org -k ~/.kube/greenhouse.yaml -o json

  # Preview what would change without writing
  cloudctl sync -n my-org --dry-run

  # No flags after one-time setup via ~/.cloudctl.yaml
  cloudctl sync

  # Debug mode — shows every cluster/authinfo/context decision on stderr
  cloudctl sync -n my-org --log-level debug`,
	RunE: runSync,
}

func runSync(cmd *cobra.Command, args []string) error {
	// Use viper as a source of configuration
	greenhouseClusterKubeconfig = resolveKubeconfig("greenhouse-cluster-kubeconfig", viper.GetString("greenhouse-cluster-kubeconfig"))
	greenhouseClusterContext = viper.GetString("greenhouse-cluster-context")
	greenhouseClusterNamespace = viper.GetString("greenhouse-cluster-namespace")
	remoteClusterKubeconfig = resolveKubeconfig("remote-cluster-kubeconfig", viper.GetString("remote-cluster-kubeconfig"))

	// Reject an explicit empty-string value — it would silently fall through to
	// client-go default loading rules instead of failing fast.
	if viper.IsSet("greenhouse-cluster-kubeconfig") && greenhouseClusterKubeconfig == "" {
		return fmt.Errorf("--greenhouse-cluster-kubeconfig must not be empty")
	}
	if viper.IsSet("remote-cluster-kubeconfig") && remoteClusterKubeconfig == "" {
		return fmt.Errorf("--remote-cluster-kubeconfig must not be empty")
	}
	if greenhouseClusterNamespace == "" {
		// Fall back to the namespace from the active Greenhouse context (org name == namespace).
		// loadKubeconfigContext also resolves the effective context name in one load.
		resolvedCtx, ns, err := loadKubeconfigContext(greenhouseClusterContext, greenhouseClusterKubeconfig)
		if err != nil {
			return fmt.Errorf("greenhouse organization namespace is required: set --greenhouse-cluster-namespace, add 'greenhouse-cluster-namespace: <org>' to ~/.cloudctl.yaml, or ensure the active context in your kubeconfig has a namespace set (%w)", err)
		}
		if ns == "" {
			return fmt.Errorf("greenhouse organization namespace is required: set --greenhouse-cluster-namespace, add 'greenhouse-cluster-namespace: <org>' to ~/.cloudctl.yaml, or ensure the active context in your kubeconfig has a namespace set")
		}
		greenhouseClusterNamespace = ns
		// Backfill resolved context so conn and error messages show the real name.
		if greenhouseClusterContext == "" {
			greenhouseClusterContext = resolvedCtx
		}
	}

	remoteClusterName = viper.GetString("remote-cluster-name")
	prefix = viper.GetString("prefix")
	mergeIdenticalUsers = viper.GetBool("merge-identical-users")
	authType = viper.GetString("auth-type")
	kubeloginPath = viper.GetString("kubelogin-path")
	kubeloginExtraArgs = viper.GetStringSlice("kubelogin-extra-args")
	kubeloginTokenCacheDir = viper.GetString("kubelogin-token-cache-dir")
	dryRun = viper.GetBool("dry-run")
	includeNotReady = viper.GetBool("include-not-ready")

	format, err := output.ParseFormat(viper.GetString("output"))
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	printer := output.New(format, output.IsTTYWriter(w), w)

	// When path is not empty (explicit file), verify it exists before proceeding.
	if greenhouseClusterKubeconfig != "" {
		if _, err := os.Stat(greenhouseClusterKubeconfig); err != nil {
			return fmt.Errorf("greenhouse cluster kubeconfig file not found at %q: %w", greenhouseClusterKubeconfig, err)
		}
	}

	// Resolve the display context name: if not already backfilled by namespace autodetect,
	// read current-context from the kubeconfig (one load, no extra I/O on the hot path).
	if greenhouseClusterContext == "" {
		if resolvedCtx, _, err := loadKubeconfigContext("", greenhouseClusterKubeconfig); err == nil {
			greenhouseClusterContext = resolvedCtx
		}
	}

	conn := &output.SyncConnection{
		Kubeconfig: resolveDisplaySources(greenhouseClusterKubeconfig),
		Context:    greenhouseClusterContext,
		Namespace:  greenhouseClusterNamespace,
	}

	if err := validateAuthType(authType, kubeloginPath); err != nil {
		return err
	}

	var (
		centralConfig *rest.Config
	)
	centralConfig, err = configWithContext(greenhouseClusterContext, greenhouseClusterKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to build greenhouse kubeconfig (source: %s, context: %s): %w", conn.Kubeconfig, conn.Context, err)
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

	if shouldSkipMerge(ready, notReady, includeNotReady) {
		r := buildSyncResult(nil, notReady)
		r.Connection = conn
		return printer.Print(r)
	}
	var localConfig *clientcmdapi.Config
	if remoteClusterKubeconfig != "" {
		localConfig, err = clientcmd.LoadFromFile(remoteClusterKubeconfig)
		if err != nil {
			return fmt.Errorf("failed to load local kubeconfig %q: %w", remoteClusterKubeconfig, err)
		}
	} else {
		localConfig, err = clientcmd.NewDefaultClientConfigLoadingRules().Load()
		if err != nil {
			return fmt.Errorf("failed to load local kubeconfig (%s): %w", displayKubeconfig(""), err)
		}
	}

	if localConfig == nil {
		localConfig = clientcmdapi.NewConfig()
	}

	// merged = ready + notReady entries that were opted into full merge.
	// skipped = notReady entries that were NOT merged (preserved in place or absent).
	// notReady entries with an empty spec are always kept on the stub/preservation path
	// regardless of --include-not-ready: they contribute no incoming data and merging
	// them would delete any existing local entry without adding anything in return.
	merged := ready
	var skipped []v1alpha1.ClusterKubeconfig
	if includeNotReady {
		for _, ckc := range notReady {
			if hasSpecData(ckc) {
				merged = append(merged, ckc)
			} else {
				skipped = append(skipped, ckc)
			}
		}
	} else {
		skipped = notReady
	}

	serverConfig, err := buildIncomingKubeconfig(merged)
	if err != nil {
		return fmt.Errorf("failed to create server config: %w", err)
	}

	// Preserve notReady cluster entries that already exist in the local kubeconfig.
	// A cluster whose Ready condition is transiently false (e.g. controller bug, rolling
	// upgrade) must not be wiped from the local kubeconfig — the user may need it to
	// reach and fix the cluster. We inject stub entries into serverConfig so the stale-
	// cleanup pass in mergeKubeconfig treats them as still-present on the server side.
	// The stubs only affect deletion logic; they are never written to disk.
	// When --include-not-ready is set, notReady entries are fully merged instead.
	// stubsToPreserve is only populated when stubs are injected (i.e. !includeNotReady).
	// It maps server-side authinfo names to the local managed name so mergeKubeconfig
	// can pre-seed authInfoMap and avoid redirecting stub contexts to a wrong authinfo.
	var stubsToPreserve map[string]string
	if !includeNotReady {
		stubsToPreserve = injectNotReadyStubs(serverConfig, skipped, localConfig)
	}

	// Take a snapshot before merge for dry-run diff — only needed when --dry-run is set.
	var localConfigBefore *clientcmdapi.Config
	if dryRun {
		localConfigBefore = localConfig.DeepCopy()
	}

	spinnerLabel := "Merging kubeconfigs..."
	if dryRun {
		spinnerLabel = "Simulating merge (dry-run)..."
	}
	stopMerge := printer.StartSpinner(spinnerLabel)
	err = mergeKubeconfig(localConfig, serverConfig, stubsToPreserve)
	stopMerge()
	if err != nil {
		r := buildFailedSyncResult(merged, skipped, err)
		r.Connection = conn
		_ = printer.Print(r)
		return fmt.Errorf(`failed to merge ClusterKubeconfig: %w`, err)
	}

	if dryRun {
		diff := diffKubeconfig(localConfigBefore, localConfig)
		return printer.Print(buildDryRunResult(diff, localConfigBefore, localConfig))
	}

	writeTarget, writeTargetErr := resolveWriteTarget(remoteClusterKubeconfig)
	if writeTargetErr != nil {
		return writeTargetErr
	}

	if writeErr := writeConfig(localConfig, writeTarget); writeErr != nil {
		r := buildFailedSyncResult(merged, skipped, writeErr)
		r.Connection = conn
		_ = printer.Print(r)
		return fmt.Errorf("failed to write merged kubeconfig: %w", writeErr)
	}

	r := buildSyncResult(merged, skipped)
	r.Connection = conn
	// Show write target only when it is not already expressed by conn.Kubeconfig.
	// For a single path they are equal; for a multi-file KUBECONFIG list the write
	// target (first entry) is a subset of the list, so we check containment.
	if !isWriteTargetInSources(writeTarget, conn.Kubeconfig) {
		r.WriteTarget = writeTarget
	}
	return printer.Print(r)
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

// hasSpecData reports whether the ClusterKubeconfig has at least one cluster, context, or
// authinfo entry in its spec. Resources with an entirely empty spec contribute no entries to
// the merged kubeconfig and should remain on the preservation path even when --include-not-ready
// is set, to avoid their existing local entries being deleted by the stale-cleanup pass.
func hasSpecData(ckc v1alpha1.ClusterKubeconfig) bool {
	return len(ckc.Spec.Kubeconfig.Clusters) > 0 ||
		len(ckc.Spec.Kubeconfig.Contexts) > 0 ||
		len(ckc.Spec.Kubeconfig.AuthInfo) > 0
}

// shouldSkipMerge returns true when there is nothing useful to merge:
// no ready clusters and either the --include-not-ready flag is not set or there are no
// not-ready clusters to merge either.
func shouldSkipMerge(ready, notReady []v1alpha1.ClusterKubeconfig, inclNotReady bool) bool {
	return len(ready) == 0 && (!inclNotReady || len(notReady) == 0)
}

// filterReady returns only ClusterKubeconfigs that have Ready condition set to True.
// Deprecated: use partitionReady instead.
func filterReady(items []v1alpha1.ClusterKubeconfig) []v1alpha1.ClusterKubeconfig {
	ready, _ := partitionReady(items)
	return ready
}

// injectNotReadyStubs adds stub entries into serverConfig for each notReady ClusterKubeconfig
// that already has a corresponding managed entry in localConfig. This prevents mergeKubeconfig's
// stale-cleanup pass from deleting local entries for clusters that are temporarily not-ready —
// the user needs those entries to reach and recover the cluster.
// Stubs are derived from the existing local entries (no spec data required) and are never written to disk.
// The returned map (server-side unprefixed authinfo name → local managed name) is passed to
// mergeKubeconfig as a pre-seed so stub context authinfo references are not redirected by deduplication.
func injectNotReadyStubs(serverConfig *clientcmdapi.Config, notReady []v1alpha1.ClusterKubeconfig, localConfig *clientcmdapi.Config) map[string]string {
	authPreSeed := make(map[string]string)
	for _, ckc := range notReady {
		// Collect the set of context names this CKC owns: prefer spec context names when
		// available (spec may be populated even if Ready=False), fall back to the resource
		// name so we still protect entries written by an older sync.
		ctxNames := make([]string, 0, len(ckc.Spec.Kubeconfig.Contexts)+1)
		for _, ctxItem := range ckc.Spec.Kubeconfig.Contexts {
			ctxNames = append(ctxNames, ctxItem.Name)
		}
		if len(ctxNames) == 0 {
			ctxNames = append(ctxNames, ckc.Name)
		}

		for _, ctxName := range ctxNames {
			localCtx, ok := localConfig.Contexts[ctxName]
			if !ok || localCtx == nil {
				continue
			}
			// skip unmanaged contexts — only protect entries cloudctl wrote.
			if !isManaged(localCtx.Cluster) {
				continue
			}

			// stub context must carry unprefixed cluster/authinfo references so
			// mergeKubeconfig's lookup (which strips the prefix before checking serverConfig)
			// resolves correctly and does not produce double-prefixed names.
			stubCtx := &clientcmdapi.Context{
				Cluster:   unmanagedNameFunc(localCtx.Cluster),
				AuthInfo:  unmanagedNameFunc(localCtx.AuthInfo),
				Namespace: localCtx.Namespace,
			}
			if _, exists := serverConfig.Contexts[ctxName]; !exists {
				serverConfig.Contexts[ctxName] = stubCtx
			}

			// Inject stub cluster keyed by the unprefixed name.
			clusterName := unmanagedNameFunc(localCtx.Cluster)
			if _, exists := serverConfig.Clusters[clusterName]; !exists {
				if src, ok := localConfig.Clusters[localCtx.Cluster]; ok {
					serverConfig.Clusters[clusterName] = src.DeepCopy()
				}
			}

			// Record the authinfo pre-seed mapping instead of adding the stub to serverConfig.
			// Putting the stub authinfo into serverConfig would cause mergeKubeconfig's
			// deduplication pass to assign a new hash-derived managed name, redirecting the
			// preserved context to a different authinfo entry and altering its effective identity.
			// The pre-seed tells mergeKubeconfig to map the stub authinfo name directly to the
			// existing local managed name, keeping the context unchanged.
			authName := unmanagedNameFunc(localCtx.AuthInfo)
			if authName != "" && localCtx.AuthInfo != "" {
				if _, exists := serverConfig.AuthInfos[authName]; !exists {
					authPreSeed[authName] = localCtx.AuthInfo
				}
			}
		}
	}
	return authPreSeed
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

func mergeKubeconfig(localConfig *clientcmdapi.Config, serverConfig *clientcmdapi.Config, preSeedAuthInfoMap map[string]string) error {
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

	// Prepare a map to track unique AuthInfos if merging is enabled.
	// Keyed by server-side authinfo name so that two server AuthInfos that share
	// the same generateAuthInfoKey (e.g. same OIDC flags but different non-OIDC
	// exec args) each get their own managed-name entry and are never conflated.
	var authInfoMap map[string]string // key: server authinfo name, value: authinfo name to use (may be unmanaged local or managed hash-based)

	// skippedServerContexts holds server-side context names whose authinfo was missing
	// at merge time. Their existing local managed entries must be preserved through
	// the cleanup pass rather than deleted as "stale".
	skippedServerContexts := make(map[string]struct{})

	if mergeIdenticalUsers {
		authInfoMap = make(map[string]string)

		// Pre-seed stub authinfo mappings from notReady preservation. These entries were
		// not added to serverConfig (to avoid dedup side effects) but their context
		// references must still resolve to the correct local managed name.
		for serverName, localManagedName := range preSeedAuthInfoMap {
			authInfoMap[serverName] = localManagedName
		}

		// Build a reverse lookup of unmanaged local auth entries so we can reuse their names
		// instead of creating new cloudctl:auth-<hash> entries.
		// Store all candidate names per key; at reuse time we pick the lexicographically
		// smallest one that actually passes authInfoEqual so that key collisions between
		// non-equivalent exec-plugin entries (different non-OIDC args) don't cause a miss.
		keyToNames := make(map[string][]string)
		for localName, localAuth := range localConfig.AuthInfos {
			if !isManaged(localName) {
				if localAuth == nil {
					continue
				}
				key := generateAuthInfoKey(localAuth)
				keyToNames[key] = append(keyToNames[key], localName)
			}
		}
		// Pre-sort each candidate list so iteration order is deterministic.
		for key := range keyToNames {
			slices.Sort(keyToNames[key])
		}

		// Merge AuthInfos
		for serverName, serverAuth := range serverConfig.AuthInfos {
			if serverAuth == nil {
				slog.Debug("skipping nil server authinfo", "name", serverName)
				continue
			}
			uniqueKey := generateAuthInfoKey(serverAuth)

			// If an unmanaged local entry has the same credentials, reuse its name.
			// Iterate candidates in sorted order and pick the first that passes
			// authInfoEqual — handles key collisions where only some entries are
			// actually equivalent (e.g. differing in non-OIDC exec args).
			for _, localName := range keyToNames[uniqueKey] {
				localAuth := localConfig.AuthInfos[localName]
				if localAuth == nil {
					continue
				}
				if authInfoEqual(localAuth, serverAuth) {
					slog.Debug("reusing existing local authinfo", "name", localName, "server", serverName)
					// Credentials are identical — leave the unmanaged local entry untouched
					// to preserve any local-only fields (Token, TokenFile, Impersonate, etc.)
					// that are outside authInfoEqual's comparison scope.
					authInfoMap[serverName] = localName
					goto nextServerAuth
				}
			}

			{
				hash := sha256.Sum256([]byte(uniqueKey))
				hashString := hex.EncodeToString(hash[:])[:16] // Using the first 16 chars for brevity
				managedAuthName := fmt.Sprintf("%s:auth-%s", prefix, hashString)

				// If the hash-derived name is already taken by a non-equal authinfo
				// (two server authinfos share the same OIDC key but differ in non-OIDC
				// exec args), fall back to a per-server managed name to avoid conflation.
				if existingAuth, exists := localConfig.AuthInfos[managedAuthName]; exists && !authInfoEqual(existingAuth, serverAuth) {
					slog.Debug("hash collision with non-equal authinfo, using per-server name", "name", managedAuthName, "server", serverName)
					managedAuthName = managedNameFunc(serverName)
				}

				// Merge AuthInfo to preserve id-token and refresh-token
				if existingAuth, exists := localConfig.AuthInfos[managedAuthName]; exists {
					slog.Debug("merging authinfo tokens", "name", managedAuthName, "server", serverName)
					mergedAuth := mergeAuthInfo(serverAuth, existingAuth)
					localConfig.AuthInfos[managedAuthName] = mergedAuth
				} else {
					slog.Debug("adding authinfo", "name", managedAuthName, "server", serverName)
					localConfig.AuthInfos[managedAuthName] = serverAuth
				}

				authInfoMap[serverName] = managedAuthName
			}
		nextServerAuth:
		}
	} else {
		// Without merging, manage AuthInfos normally
		for serverName, serverAuth := range serverConfig.AuthInfos {
			if serverAuth == nil {
				slog.Debug("skipping nil server authinfo", "name", serverName)
				continue
			}
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
			// Look up by server authinfo name — authInfoMap is keyed by server name,
			// so each distinct server AuthInfo resolves to its own managed entry.
			serverAuthName := serverCtx.AuthInfo
			var existsInMap bool
			managedAuthInfoName, existsInMap = authInfoMap[serverAuthName]
			if !existsInMap {
				// This should not happen as all AuthInfos should have been processed.
				// However, to be safe, generate a new managedAuthName.
				serverAuth, exists := serverConfig.AuthInfos[serverAuthName]
				if !exists || serverAuth == nil {
					slog.Debug("skipping context: authinfo missing from server config", "context", serverName, "authinfo", serverAuthName)
					skippedServerContexts[serverName] = struct{}{}
					continue
				}
				uniqueKey := generateAuthInfoKey(serverAuth)
				hash := sha256.Sum256([]byte(uniqueKey))
				hashString := hex.EncodeToString(hash[:])[:16]
				managedAuthInfoName = fmt.Sprintf("%s:auth-%s", prefix, hashString)
				authInfoMap[serverAuthName] = managedAuthInfoName
				localConfig.AuthInfos[managedAuthInfoName] = serverAuth
			}
		} else {
			// Without deduplication, derive the managed name from the server-side name.
			// For stub contexts injected by injectNotReadyStubs the authinfo was not added
			// to serverConfig; its correct local managed name is in preSeedAuthInfoMap.
			if preSeedName, ok := preSeedAuthInfoMap[serverCtx.AuthInfo]; ok {
				managedAuthInfoName = preSeedName
			} else {
				// Only write the context when the authinfo it references exists in serverConfig;
				// a context pointing at a missing authinfo would produce a broken kubeconfig entry.
				if _, exists := serverConfig.AuthInfos[serverCtx.AuthInfo]; !exists {
					slog.Debug("skipping context: authinfo missing from server config", "context", serverName, "authinfo", serverCtx.AuthInfo)
					skippedServerContexts[serverName] = struct{}{}
					continue
				}
				managedAuthInfoName = managedNameFunc(serverCtx.AuthInfo)
			}
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
				// Derive the server-side name by stripping the prefix.
				// Also protect authinfos pre-seeded from notReady stubs — those were not
				// added to serverConfig, so the server-side name lookup would otherwise
				// cause them to be deleted.
				serverName := unmanagedNameFunc(localName)
				_, inServer := serverConfig.AuthInfos[serverName]
				_, inPreSeed := preSeedAuthInfoMap[serverName]
				if !inServer && !inPreSeed {
					slog.Debug("removing stale authinfo", "name", localName)
					delete(localConfig.AuthInfos, localName)
				}
			}
		}
	}

	// Delete managed Contexts not present in serverConfig.
	// A context is considered managed when its cluster reference is managed
	// (context names are not prefixed — only the referenced cluster is).
	for localName, localCtx := range localConfig.Contexts {
		if localCtx == nil || !isManaged(localCtx.Cluster) {
			continue
		}
		// Context name equals the server-side name (no prefix applied).
		serverName := localName
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
				mappedName, exists := authInfoMap[serverAuthName]
				if !exists {
					// If the context was explicitly skipped due to a missing authinfo,
					// preserve the existing local entry rather than deleting it.
					if _, skipped := skippedServerContexts[serverName]; skipped {
						slog.Debug("preserving local context: authinfo was missing at merge time", "name", localName)
						continue
					}
					slog.Debug("removing stale context (unmapped authinfo)", "name", localName)
					delete(localConfig.Contexts, localName)
					continue
				}
				expectedAuthInfo = mappedName
			} else {
				// Same pre-seed lookup as in context merging above.
				if preSeedName, ok := preSeedAuthInfoMap[serverCtx.AuthInfo]; ok {
					expectedAuthInfo = preSeedName
				} else {
					expectedAuthInfo = managedNameFunc(serverCtx.AuthInfo)
				}
			}

			if localCtx.Cluster != expectedCluster || localCtx.AuthInfo != expectedAuthInfo {
				slog.Debug("removing stale context (mismatched refs)", "name", localName)
				delete(localConfig.Contexts, localName)
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

// resolveWriteTarget returns the kubeconfig file path to write merged config into.
// When remoteKubeconfig is non-empty it is used directly. Otherwise the first
// path from the KUBECONFIG env var is used (kubectl convention for multi-file merge).
// An error is returned when no usable path can be determined.
func resolveWriteTarget(remoteKubeconfig string) (string, error) {
	if remoteKubeconfig != "" {
		return remoteKubeconfig, nil
	}
	// Mirror kubectl's default file selection: first entry in KUBECONFIG, else ~/.kube/config.
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		if parts := strings.SplitN(kc, string(os.PathListSeparator), 2); len(parts) > 0 && parts[0] != "" {
			return parts[0], nil
		}
		return "", fmt.Errorf("cannot determine write target: KUBECONFIG=%q contains no usable first path", kc)
	}
	return clientcmd.RecommendedHomeFile, nil
}

// resolveDisplaySources returns the kubeconfig source description for the connection header.
// When kubeconfigPath is explicit it is returned as-is. When KUBECONFIG is set the full
// colon-separated list is returned so multi-file setups are not misrepresented. Otherwise
// ~/.kube/config is returned.
func resolveDisplaySources(kubeconfigPath string) string {
	if kubeconfigPath != "" {
		return kubeconfigPath
	}
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		return kc
	}
	return clientcmd.RecommendedHomeFile
}

// isWriteTargetInSources reports whether writeTarget is already represented by the
// sources string (which may be a single path or a colon-separated list).
func isWriteTargetInSources(writeTarget, sources string) bool {
	for _, p := range strings.Split(sources, string(os.PathListSeparator)) {
		if p == writeTarget {
			return true
		}
	}
	return false
}

// loadKubeconfigContext resolves the active context name and its namespace from
// the given kubeconfig in a single load. When contextName is explicitly provided
// only the namespace lookup uses the file; otherwise both are read from
// current-context. Returns ("", "", nil) when the context has no namespace set.
func loadKubeconfigContext(contextName, kubeconfigPath string) (activeContext, namespace string, err error) {
	var loadingRules *clientcmd.ClientConfigLoadingRules
	if kubeconfigPath != "" {
		loadingRules = &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath}
	} else {
		loadingRules = clientcmd.NewDefaultClientConfigLoadingRules()
	}
	cfg, err := loadingRules.Load()
	if err != nil {
		return "", "", fmt.Errorf("failed to load kubeconfig: %w", err)
	}
	activeContext = contextName
	if activeContext == "" {
		activeContext = cfg.CurrentContext
	}
	ctx, ok := cfg.Contexts[activeContext]
	if !ok || ctx == nil {
		return activeContext, "", nil
	}
	return activeContext, ctx.Namespace, nil
}
