// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"strings"

	"github.com/cloudoperators/greenhouse/api/v1alpha1"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	greenhouseClusterKubeconfig string
	greenhouseClusterContext    string
	greenhouseClusterNamespace  string
	remoteClusterKubeconfig     string
	remoteClusterName           string
	prefix                      string
	mergeIdenticalUsers         bool
)

func init() {
	syncCmd.Flags().StringVarP(&greenhouseClusterKubeconfig, "greenhouse-cluster-kubeconfig", "k", clientcmd.RecommendedHomeFile, "kubeconfig file path for Greenhouse cluster")
	syncCmd.Flags().StringVarP(&greenhouseClusterContext, "greenhouse-cluster-context", "c", "", "context in greenhouse-cluster-kubeconfig, the context in the file is used if this flag is not set")
	syncCmd.Flags().StringVarP(&greenhouseClusterNamespace, "greenhouse-cluster-namespace", "n", "", "namespace for greenhouse-cluster-kubeconfig, it is the same value as Greenhouse organization")
	syncCmd.MarkFlagRequired("greenhouse-cluster-namespace")
	syncCmd.Flags().StringVarP(&remoteClusterKubeconfig, "remote-cluster-kubeconfig", "r", clientcmd.RecommendedHomeFile, "kubeconfig file path for remote clusters")
	syncCmd.Flags().StringVar(&remoteClusterName, "remote-cluster-name", "", "name of the remote cluster, if not set all clusters are retrieved")
	syncCmd.Flags().StringVar(&prefix, "prefix", "cloudctl", "prefix for kubeconfig entries. it is used to separate and manage the entries of this tool only")
	syncCmd.Flags().BoolVar(&mergeIdenticalUsers, "merge-identical-users", true, "merge identical user information in kubeconfig file so that you only login once for the clusters that share the same auth info")
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Fetches kubeconfigs of remote clusters from Greenhouse cluster and merges them into your local config",
	RunE:  runSync,
}

func runSync(cmd *cobra.Command, args []string) error {
	centralConfig, err := clientcmd.BuildConfigFromFlags("", greenhouseClusterKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to build greenhouse kubeconfig: %w", err)
	}

	if greenhouseClusterContext != "" {
		centralConfig, err = configWithContext(greenhouseClusterContext, greenhouseClusterKubeconfig)
		if err != nil {
			return fmt.Errorf("failed to build greenhouse kubeconfig with context %s: %w", greenhouseClusterContext, err)
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
	var clusterKubeconfigs []v1alpha1.ClusterKubeconfig

	// If a specific remote cluster name is provided, fetch that single resource;
	// otherwise, list all ClusterKubeconfigs in the given namespace.
	if remoteClusterName != "" {
		var ckc v1alpha1.ClusterKubeconfig
		if err := c.Get(ctx, client.ObjectKey{Namespace: greenhouseClusterNamespace, Name: remoteClusterName}, &ckc); err != nil {
			return fmt.Errorf("failed to get ClusterKubeconfig %q: %w", remoteClusterName, err)
		}
		clusterKubeconfigs = append(clusterKubeconfigs, ckc)
	} else {
		var list v1alpha1.ClusterKubeconfigList
		if err := c.List(ctx, &list, client.InNamespace(greenhouseClusterNamespace)); err != nil {
			return fmt.Errorf("failed to list ClusterKubeconfigs: %w", err)
		}
		clusterKubeconfigs = list.Items
	}
	if len(clusterKubeconfigs) == 0 {
		log.Println("No ClusterKubeconfigs found to sync.")
		return nil
	}

	localConfig, err := clientcmd.LoadFromFile(remoteClusterKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load local kubeconfig: %w", err)
	}

	if localConfig == nil {
		localConfig = clientcmdapi.NewConfig()
	}

	serverConfig, err := buildIncomingKubeconfig(clusterKubeconfigs)
	if err != nil {
		return fmt.Errorf("failed to create server config: %w", err)
	}

	err = mergeKubeconfig(localConfig, serverConfig)
	if err != nil {
		return fmt.Errorf(`failed to merge ClusterKubeconfig: %w`, err)
	}

	err = writeConfig(localConfig, remoteClusterKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to write merged kubeconfig: %w", err)
	}

	log.Println("Successfully synced and merged into your local config.")
	return nil
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
			// Preserve the same data shape; exclude nothing here (merging will handle dedupe)
			authProvider := authItem.AuthInfo.AuthProvider
			kubeconfig.AuthInfos[authItem.Name] = &clientcmdapi.AuthInfo{
				ClientCertificateData: authItem.AuthInfo.ClientCertificateData,
				ClientKeyData:         authItem.AuthInfo.ClientKeyData,
				AuthProvider:          &authProvider,
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

// authInfoEqual compares two AuthInfo objects, excluding "id-token" and "refresh-token".
func authInfoEqual(a, b *clientcmdapi.AuthInfo) bool {
	// Compare ClientCertificateData
	if !bytes.Equal(a.ClientCertificateData, b.ClientCertificateData) {
		return false
	}

	// Compare ClientKeyData
	if !bytes.Equal(a.ClientKeyData, b.ClientKeyData) {
		return false
	}

	// Compare AuthProvider, excluding "id-token" and "refresh-token"
	if a.AuthProvider == nil && b.AuthProvider != nil || a.AuthProvider != nil && b.AuthProvider == nil {
		return false
	}
	if a.AuthProvider != nil && b.AuthProvider != nil {
		// Compare AuthProvider Name
		if a.AuthProvider.Name != b.AuthProvider.Name {
			return false
		}

		// Compare AuthProvider Config excluding "id-token" and "refresh-token"
		aConfigFiltered := filterAuthProviderConfig(a.AuthProvider.Config)
		bConfigFiltered := filterAuthProviderConfig(b.AuthProvider.Config)
		if !maps.Equal(aConfigFiltered, bConfigFiltered) {
			return false
		}
	}

	return true
}

// filterAuthProviderConfig returns a copy of the config map excluding "id-token" and "refresh-token".
func filterAuthProviderConfig(config map[string]string) map[string]string {
	filtered := make(map[string]string)
	for k, v := range config {
		if k != "id-token" && k != "refresh-token" {
			filtered[k] = v
		}
	}
	return filtered
}

// generateAuthInfoKey creates a unique key for an AuthInfo based on specific AuthProvider fields,
// excluding "id-token" and "refresh-token". It uses "client-id", "client-secret",
// "auth-request-extra-params", and "extra-scopes" to generate the key.
func generateAuthInfoKey(authInfo *clientcmdapi.AuthInfo) string {
	if authInfo.AuthProvider == nil {
		// For AuthInfos without AuthProvider, use a different unique identifier
		// Here, we'll use the hash of ClientCertificateData and ClientKeyData
		h := sha256.New()
		h.Write(authInfo.ClientCertificateData)
		h.Write(authInfo.ClientKeyData)
		return fmt.Sprintf("cert:%s", hex.EncodeToString(h.Sum(nil)))
	}

	// Extract the required fields from AuthProvider Config
	clientID := authInfo.AuthProvider.Config["client-id"]
	clientSecret := authInfo.AuthProvider.Config["client-secret"]
	authRequestExtraParams := authInfo.AuthProvider.Config["auth-request-extra-params"]
	extraScopes := authInfo.AuthProvider.Config["extra-scopes"]

	// Concatenate the fields in a consistent order
	data := fmt.Sprintf("client-id:%s;client-secret:%s;auth-request-extra-params:%s;extra-scopes:%s",
		clientID, clientSecret, authRequestExtraParams, extraScopes)

	return data
}

func mergeKubeconfig(localConfig *clientcmdapi.Config, serverConfig *clientcmdapi.Config) error {
	// Merge Clusters
	for serverName, serverCluster := range serverConfig.Clusters {
		managedName := managedNameFunc(serverName)
		localCluster, exists := localConfig.Clusters[managedName]
		if !exists {
			// Add the managed cluster from serverConfig to localConfig
			localConfig.Clusters[managedName] = serverCluster
		} else {
			// Check if Server, CertificateAuthorityData or the labels extension has changed
			if localCluster.Server != serverCluster.Server ||
				!bytes.Equal(localCluster.CertificateAuthorityData, serverCluster.CertificateAuthorityData) ||
				!labelsExtensionEqual(localCluster.Extensions, serverCluster.Extensions) {
				localConfig.Clusters[managedName] = serverCluster
			}
		}
	}

	// Prepare a map to track unique AuthInfos if merging is enabled
	var authInfoMap map[string]string // key: unique identifier, value: managed AuthInfo name
	if mergeIdenticalUsers {
		authInfoMap = make(map[string]string)
	}

	// Merge AuthInfos
	for serverName, serverAuth := range serverConfig.AuthInfos {
		var managedAuthName string

		if mergeIdenticalUsers {
			// Generate a unique key based on AuthInfo excluding id-token and refresh-token
			uniqueKey := generateAuthInfoKey(serverAuth)
			hash := sha256.Sum256([]byte(uniqueKey))
			hashString := hex.EncodeToString(hash[:])[:16] // Using the first 16 chars for brevity
			managedAuthName = fmt.Sprintf("%s:auth-%s", prefix, hashString)

			// **Merge AuthInfo to preserve id-token and refresh-token**
			if existingAuth, exists := localConfig.AuthInfos[managedAuthName]; exists {
				mergedAuth := mergeAuthInfo(serverAuth, existingAuth)
				localConfig.AuthInfos[managedAuthName] = mergedAuth
			} else {
				localConfig.AuthInfos[managedAuthName] = serverAuth
			}

			authInfoMap[uniqueKey] = managedAuthName
		} else {
			// Without merging, manage AuthInfos normally
			managedAuthName = managedNameFunc(serverName)
			localAuth, exists := localConfig.AuthInfos[managedAuthName]
			if !exists {
				localConfig.AuthInfos[managedAuthName] = serverAuth
			} else {
				if !authInfoEqual(localAuth, serverAuth) {
					// **Merge AuthInfo to preserve id-token and refresh-token**
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
			localConfig.Contexts[managedName] = serverCtxCopy
		} else {
			// Check if Cluster, AuthInfo, or Namespace has changed
			if localCtx.Cluster != serverCtxCopy.Cluster ||
				localCtx.AuthInfo != serverCtxCopy.AuthInfo ||
				localCtx.Namespace != serverCtxCopy.Namespace {
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
					delete(localConfig.AuthInfos, localName)
				}
			} else {
				// Derive the server-side name by stripping the prefix
				serverName := unmanagedNameFunc(localName)
				if _, exists := serverConfig.AuthInfos[serverName]; !exists {
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
						delete(localConfig.Contexts, localName)
						continue
					}
					uniqueKey := generateAuthInfoKey(serverAuth)
					mappedName, exists := authInfoMap[uniqueKey]
					if !exists {
						delete(localConfig.Contexts, localName)
						continue
					}
					expectedAuthInfo = mappedName
				} else {
					expectedAuthInfo = managedNameFunc(serverCtx.AuthInfo)
				}

				if localCtx.Cluster != expectedCluster || localCtx.AuthInfo != expectedAuthInfo {
					delete(localConfig.Contexts, localName)
				}
			}
		}
	}

	return nil
}

// Helper function to merge AuthInfo objects while preserving id-token and refresh-token
func mergeAuthInfo(serverAuth, localAuth *clientcmdapi.AuthInfo) *clientcmdapi.AuthInfo {
	if localAuth == nil {
		// If there's no local AuthInfo, return the server AuthInfo as is
		return serverAuth
	}

	// Create a copy of the serverAuth to avoid mutating the original
	mergedAuth := serverAuth.DeepCopy()

	// Preserve id-token and refresh-token from localAuth
	if localAuth.AuthProvider != nil && mergedAuth.AuthProvider != nil {
		// Ensure the merged config map is initialized to avoid panics on assignment
		if mergedAuth.AuthProvider.Config == nil {
			mergedAuth.AuthProvider.Config = make(map[string]string)
		}
		if idToken, exists := localAuth.AuthProvider.Config["id-token"]; exists {
			mergedAuth.AuthProvider.Config["id-token"] = idToken
		}
		if refreshToken, exists := localAuth.AuthProvider.Config["refresh-token"]; exists {
			mergedAuth.AuthProvider.Config["refresh-token"] = refreshToken
		}
	}

	// Additionally, preserve other fields if necessary.
	// For example, ClientCertificateData and ClientKeyData are already handled

	return mergedAuth
}

// labelsExtensionEqual returns true if the \"labels\" named extension is equal in both maps.
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

func configWithContext(context, kubeconfigPath string) (*rest.Config, error) {
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{
			CurrentContext: context,
		}).ClientConfig()
}
