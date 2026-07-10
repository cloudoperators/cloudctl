// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

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

	// Compare Exec first (new style)
	if (a.Exec == nil) != (b.Exec == nil) {
		return false
	}
	if a.Exec != nil && b.Exec != nil {
		if a.Exec.Command != b.Exec.Command || a.Exec.APIVersion != b.Exec.APIVersion {
			return false
		}
		if !slices.Equal(a.Exec.Args, b.Exec.Args) {
			return false
		}
		if a.Exec.InteractiveMode != b.Exec.InteractiveMode {
			return false
		}
		if !equalExecEnv(a.Exec.Env, b.Exec.Env) {
			return false
		}
		return true
	}

	// Compare AuthProvider, excluding "id-token" and "refresh-token"
	if (a.AuthProvider == nil) != (b.AuthProvider == nil) {
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

// equalExecEnv compares two ExecEnvVar slices for equality.
func equalExecEnv(a, b []clientcmdapi.ExecEnvVar) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Value != b[i].Value {
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
// excluding "id-token" and "refresh-token". It uses "idp-issuer-url", "client-id", "client-secret",
// "auth-request-extra-params", and "extra-scopes" to generate the key.
func generateAuthInfoKey(authInfo *clientcmdapi.AuthInfo) string {
	// Exec-based key: derive from stable subset of args to avoid including tokens
	if authInfo.Exec != nil {
		// Extract known kubelogin flags
		var issuer, clientID, clientSecret, extraParams string
		var scopes []string
		var envParts []string
		for _, arg := range authInfo.Exec.Args {
			switch {
			case strings.HasPrefix(arg, "--oidc-issuer-url="):
				issuer = strings.TrimPrefix(arg, "--oidc-issuer-url=")
			case strings.HasPrefix(arg, "--oidc-client-id="):
				clientID = strings.TrimPrefix(arg, "--oidc-client-id=")
			case strings.HasPrefix(arg, "--oidc-client-secret="):
				clientSecret = strings.TrimPrefix(arg, "--oidc-client-secret=")
			case strings.HasPrefix(arg, "--oidc-extra-scope="):
				scopes = append(scopes, strings.TrimPrefix(arg, "--oidc-extra-scope="))
			case strings.HasPrefix(arg, "--oidc-auth-request-extra-params="):
				extraParams = strings.TrimPrefix(arg, "--oidc-auth-request-extra-params=")
			}
		}
		sort.Strings(scopes)
		// Include sorted Env in the key so changes to env vars result in a different key
		for _, e := range authInfo.Exec.Env {
			envParts = append(envParts, e.Name+"="+e.Value)
		}
		sort.Strings(envParts)
		data := fmt.Sprintf("exec:issuer:%s;client-id:%s;client-secret:%s;extra-params:%s;scopes:%s;env:%s",
			issuer, clientID, clientSecret, extraParams, strings.Join(scopes, ","), strings.Join(envParts, ","))
		return data
	}

	if authInfo.AuthProvider == nil {
		// For AuthInfos without AuthProvider, use a different unique identifier
		// Here, we'll use the hash of ClientCertificateData and ClientKeyData
		h := sha256.New()
		h.Write(authInfo.ClientCertificateData)
		h.Write(authInfo.ClientKeyData)
		return fmt.Sprintf("cert:%s", hex.EncodeToString(h.Sum(nil)))
	}

	// Extract the required fields from AuthProvider Config, including idp-issuer-url
	// to avoid incorrectly deduplicating clusters with different issuers but same client-id.
	issuerURL := authInfo.AuthProvider.Config["idp-issuer-url"]
	clientID := authInfo.AuthProvider.Config["client-id"]
	clientSecret := authInfo.AuthProvider.Config["client-secret"]
	authRequestExtraParams := authInfo.AuthProvider.Config["auth-request-extra-params"]
	extraScopes := authInfo.AuthProvider.Config["extra-scopes"]

	// Concatenate the fields in a consistent order
	data := fmt.Sprintf("idp-issuer-url:%s;client-id:%s;client-secret:%s;auth-request-extra-params:%s;extra-scopes:%s",
		issuerURL, clientID, clientSecret, authRequestExtraParams, extraScopes)

	return data
}

// mergeAuthInfo merges two AuthInfo objects, preserving id-token and refresh-token from localAuth.
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

	return mergedAuth
}
