// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"maps"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/cloudoperators/greenhouse/pkg/apis/greenhouse/v1alpha1"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

var (
	greenhouseCentralClusterKubeconfig string
	greenhouseCentralClusterContext    string
	greenhouseCentralClusterNamespace  string
	greenhouseRemoteClusterKubeconfig  string
	greenhouseRemoteClusterName        string
	prefix                             string
	mergeIdenticalUsers                bool
)

func init() {
	syncCmd.Flags().StringVar(&greenhouseCentralClusterKubeconfig, "central-cluster-kubeconfig", clientcmd.RecommendedHomeFile, "kubeconfig for central Greenhouse cluster")
	syncCmd.Flags().StringVar(&greenhouseCentralClusterContext, "central-cluster-context", "", "context in central-cluster-kubeconfig,  the context in the file is used if this flag is not set")
	syncCmd.Flags().StringVar(&greenhouseCentralClusterNamespace, "central-cluster-namespace", "", "namespace for central-cluster-kubeconfig, if not set, kubeconfigs from all namespaces are retrieved")
	syncCmd.Flags().StringVar(&greenhouseRemoteClusterKubeconfig, "remote-cluster-kubeconfig", clientcmd.RecommendedHomeFile, "kubeconfig for remote Greenhouse clusters")
	syncCmd.Flags().StringVar(&greenhouseRemoteClusterName, "remote-cluster-name", "", "name of the remote cluster, if not set (by default) all clusters are retrieved")
	syncCmd.Flags().StringVar(&prefix, "prefix", "cloudctl", "prefix for kubeconfig entries. It is used to separate and manage the entries of this tool only")
	syncCmd.Flags().BoolVar(&mergeIdenticalUsers, "merge-identical-users", true, "merge identical user information in kubeconfig file so that you only login once for the clusters that share the same auth info")
}

var (
	syncCmd = &cobra.Command{
		Use:   "sync",
		Short: "Fetches remote kubeconfigs from Greenhouse cluster and merges them into your local config",
		RunE:  runSync,
	}
)

func runSync(cmd *cobra.Command, args []string) error {

	centralConfig, err := clientcmd.BuildConfigFromFlags("", greenhouseCentralClusterKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to build central kubeconfig: %w", err)
	}

	if greenhouseCentralClusterContext != "" {
		centralConfig, err = configWithContext(greenhouseCentralClusterContext, greenhouseCentralClusterKubeconfig)
		if err != nil {
			return fmt.Errorf("failed to build central kubeconfig with context %s: %w", greenhouseCentralClusterContext, err)
		}
	}

	dynamicClient, err := dynamic.NewForConfig(centralConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "greenhouse.sap",
		Version:  "v1alpha1",
		Resource: "clusterkubeconfigs",
	}

	var items []unstructured.Unstructured

	// If user wants a single remote cluster, do a GET instead of a List.
	if greenhouseRemoteClusterName != "" {
		unstructuredObj, err := dynamicClient.Resource(gvr).Namespace(greenhouseCentralClusterNamespace).
			Get(cmd.Context(), greenhouseRemoteClusterName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get ClusterKubeconfig %q: %w", greenhouseRemoteClusterName, err)
		}
		items = append(items, *unstructuredObj)
	} else {
		unstructuredList, err := dynamicClient.Resource(gvr).Namespace(greenhouseCentralClusterNamespace).List(cmd.Context(), metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list ClusterKubeconfigs: %w", err)
		}
		items = unstructuredList.Items
	}
	if len(items) == 0 {
		log.Println("No ClusterKubeconfigs found to sync.")
		return nil
	}

	localConfig, err := clientcmd.LoadFromFile(greenhouseRemoteClusterKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load local kubeconfig: %w", err)
	}

	if localConfig == nil {
		localConfig = clientcmdapi.NewConfig()
	}

	serverConfig, err := buildIncomingKubeconfig(items)
	if err != nil {
		return fmt.Errorf("failed to create server config: %w", err)
	}

	err = mergeKubeconfig(localConfig, serverConfig)
	if err != nil {
		return fmt.Errorf("failed to merge ClusterKubeconfig: %w", err)
	}

	err = writeConfig(localConfig, greenhouseRemoteClusterKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to write merged kubeconfig: %w", err)
	}

	log.Println("Successfully synced and merged the new cluster kubeconfig with your local config.")
	return nil
}

func buildIncomingKubeconfig(items []unstructured.Unstructured) (*clientcmdapi.Config, error) {
	kubeconfig := clientcmdapi.NewConfig()

	for _, unstructuredItem := range items {
		var ckc v1alpha1.ClusterKubeconfig
		err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredItem.Object, &ckc)
		if err != nil {
			return nil, fmt.Errorf("failed to convert unstructured to ClusterKubeconfig: %w", err)
		}

		// Assuming each ClusterKubeconfig has exactly one context, authInfo, and cluster
		if len(ckc.Spec.Kubeconfig.Contexts) > 0 {
			ctx := ckc.Spec.Kubeconfig.Contexts[0]
			kubeconfig.Contexts[ctx.Name] = &clientcmdapi.Context{
				Cluster:   ctx.Context.Cluster,
				AuthInfo:  ctx.Context.AuthInfo,
				Namespace: ctx.Context.Namespace,
			}
		}

		if len(ckc.Spec.Kubeconfig.AuthInfo) > 0 {
			auth := ckc.Spec.Kubeconfig.AuthInfo[0].AuthInfo
			kubeconfig.AuthInfos[ckc.Spec.Kubeconfig.AuthInfo[0].Name] = &clientcmdapi.AuthInfo{
				ClientCertificateData: auth.ClientCertificateData,
				ClientKeyData:         auth.ClientKeyData,
				AuthProvider:          &auth.AuthProvider,
			}
		}

		if len(ckc.Spec.Kubeconfig.Clusters) > 0 {
			cluster := ckc.Spec.Kubeconfig.Clusters[0].Cluster
			kubeconfig.Clusters[ckc.Spec.Kubeconfig.Clusters[0].Name] = &clientcmdapi.Cluster{
				Server:                   cluster.Server,
				CertificateAuthorityData: cluster.CertificateAuthorityData,
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
			// Check if Server or CertificateAuthorityData has changed
			if localCluster.Server != serverCluster.Server ||
				!bytes.Equal(localCluster.CertificateAuthorityData, serverCluster.CertificateAuthorityData) {
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
			hashString := hex.EncodeToString(hash[:])[:16] // Using first 16 chars for brevity
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
		managedName := serverName // it is same for context

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
				// This should not happen as all AuthInfos should have been processed
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
		if idToken, exists := localAuth.AuthProvider.Config["id-token"]; exists {
			mergedAuth.AuthProvider.Config["id-token"] = idToken
		}
		if refreshToken, exists := localAuth.AuthProvider.Config["refresh-token"]; exists {
			mergedAuth.AuthProvider.Config["refresh-token"] = refreshToken
		}
	}

	// Additionally, preserve other fields if necessary
	// For example, ClientCertificateData and ClientKeyData are already handled

	return mergedAuth
}

func configWithContext(context, kubeconfigPath string) (*rest.Config, error) {
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{
			CurrentContext: context,
		}).ClientConfig()
}
