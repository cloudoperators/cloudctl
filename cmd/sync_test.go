// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	greenhousemetav1alpha1 "github.com/cloudoperators/greenhouse/api/meta/v1alpha1"
	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/cloudoperators/cloudctl/cmd/output"
)

// sync_merge_test.go

func TestManagedNameHelpers(t *testing.T) {
	g := NewWithT(t)

	orig := prefix
	prefix = "cloudctl"
	t.Cleanup(func() { prefix = orig })

	name := "mycluster"
	mn := managedNameFunc(name)
	g.Expect(mn).To(Equal("cloudctl:mycluster"))
	g.Expect(isManaged(mn)).To(BeTrue())
	g.Expect(isManaged(name)).To(BeFalse())

	g.Expect(unmanagedNameFunc(mn)).To(Equal(name))
}

func TestFilterAuthProviderConfig(t *testing.T) {
	g := NewWithT(t)

	in := map[string]string{
		"id-token":                  "secret",
		"refresh-token":             "secret2",
		"client-id":                 "cid",
		"client-secret":             "csec",
		"auth-request-extra-params": "aud=foo",
		"extra-scopes":              "groups,offline_access",
		"keep":                      "x",
	}
	out := filterAuthProviderConfig(in)

	g.Expect(out).ToNot(HaveKey("id-token"))
	g.Expect(out).ToNot(HaveKey("refresh-token"))
	g.Expect(out).To(HaveKeyWithValue("client-id", "cid"))
	g.Expect(out).To(HaveKeyWithValue("client-secret", "csec"))
	g.Expect(out).To(HaveKeyWithValue("auth-request-extra-params", "aud=foo"))
	g.Expect(out).To(HaveKeyWithValue("extra-scopes", "groups,offline_access"))
	g.Expect(out).To(HaveKeyWithValue("keep", "x"))
}

func TestAuthInfoEqual_IgnoresTokens(t *testing.T) {
	g := NewWithT(t)

	a := &clientcmdapi.AuthInfo{
		AuthProvider: &clientcmdapi.AuthProviderConfig{
			Name: "oidc",
			Config: map[string]string{
				"client-id":     "cid",
				"client-secret": "csec",
				"id-token":      "tokA",
				"refresh-token": "refA",
			},
		},
	}
	b := &clientcmdapi.AuthInfo{
		AuthProvider: &clientcmdapi.AuthProviderConfig{
			Name: "oidc",
			Config: map[string]string{
				"client-id":     "cid",
				"client-secret": "csec",
				"id-token":      "tokB",
				"refresh-token": "refB",
			},
		},
	}
	g.Expect(authInfoEqual(a, b)).To(BeTrue(), "token differences should be ignored")
}

func TestAuthInfoEqual_DiffCerts(t *testing.T) {
	g := NewWithT(t)

	a := &clientcmdapi.AuthInfo{
		ClientCertificateData: []byte("certA"),
		ClientKeyData:         []byte("keyA"),
	}
	b := &clientcmdapi.AuthInfo{
		ClientCertificateData: []byte("certB"),
		ClientKeyData:         []byte("keyA"),
	}
	g.Expect(authInfoEqual(a, b)).To(BeFalse(), "different certs should not be equal")
}

func TestGenerateAuthInfoKey_OIDC(t *testing.T) {
	g := NewWithT(t)

	a := &clientcmdapi.AuthInfo{
		AuthProvider: &clientcmdapi.AuthProviderConfig{
			Name: "oidc",
			Config: map[string]string{
				"client-id":                 "cid",
				"client-secret":             "csec",
				"auth-request-extra-params": "aud=foo",
				"extra-scopes":              "groups,offline_access",
				"id-token":                  "tokA",
				"refresh-token":             "refA",
			},
		},
	}
	b := &clientcmdapi.AuthInfo{
		AuthProvider: &clientcmdapi.AuthProviderConfig{
			Name: "oidc",
			Config: map[string]string{
				"client-id":                 "cid",
				"client-secret":             "csec",
				"auth-request-extra-params": "aud=foo",
				"extra-scopes":              "groups,offline_access",
				"id-token":                  "tokB",
				"refresh-token":             "refB",
			},
		},
	}
	ka := generateAuthInfoKey(a)
	kb := generateAuthInfoKey(b)
	g.Expect(ka).To(Equal(kb), "tokens must not affect dedupe key")
}

func TestGenerateAuthInfoKey_CertBased(t *testing.T) {
	g := NewWithT(t)

	a := &clientcmdapi.AuthInfo{
		ClientCertificateData: []byte("certA"),
		ClientKeyData:         []byte("keyA"),
	}
	b := &clientcmdapi.AuthInfo{
		ClientCertificateData: []byte("certA"),
		ClientKeyData:         []byte("keyA"),
	}
	ka := generateAuthInfoKey(a)
	kb := generateAuthInfoKey(b)

	g.Expect(ka).To(Equal(kb))
	g.Expect(bytes.HasPrefix([]byte(ka), []byte("cert:"))).To(BeTrue(), "cert-based key should have cert: prefix")
}

func TestFilterReady_IncludesOnlyReady(t *testing.T) {
	g := NewWithT(t)

	readyCkc := greenhousev1alpha1.ClusterKubeconfig{
		ObjectMeta: metav1.ObjectMeta{Name: "ready-cluster"},
		Status:     greenhousev1alpha1.ClusterKubeconfigStatus{},
	}
	// Set Ready=True
	readyCkc.Status.Conditions.SetConditions(
		greenhousemetav1alpha1.TrueCondition(
			greenhousemetav1alpha1.ReadyCondition,
			"TestReason",
			"ready",
		),
	)

	notReadyCkc := greenhousev1alpha1.ClusterKubeconfig{
		ObjectMeta: metav1.ObjectMeta{Name: "notready-cluster"},
		Status:     greenhousev1alpha1.ClusterKubeconfigStatus{},
	}
	// Set Ready=False
	notReadyCkc.Status.Conditions.SetConditions(
		greenhousemetav1alpha1.FalseCondition(
			greenhousemetav1alpha1.ReadyCondition,
			"TestReason",
			"not ready",
		),
	)

	noCondCkc := greenhousev1alpha1.ClusterKubeconfig{
		ObjectMeta: metav1.ObjectMeta{Name: "nocond-cluster"},
	}

	out := filterReady([]greenhousev1alpha1.ClusterKubeconfig{readyCkc, notReadyCkc, noCondCkc})
	g.Expect(out).To(HaveLen(1))
	g.Expect(out[0].Name).To(Equal("ready-cluster"))
}

func TestFilterReady_EmptyAndNoneReady(t *testing.T) {
	g := NewWithT(t)

	// Empty input
	out := filterReady(nil)
	g.Expect(out).To(BeNil())

	// None ready input
	a := greenhousev1alpha1.ClusterKubeconfig{ObjectMeta: metav1.ObjectMeta{Name: "a"}}
	a.Status.Conditions.SetConditions(
		greenhousemetav1alpha1.FalseCondition(
			greenhousemetav1alpha1.ReadyCondition,
			"TestReason",
			"not ready",
		),
	)
	b := greenhousev1alpha1.ClusterKubeconfig{ObjectMeta: metav1.ObjectMeta{Name: "b"}}
	out2 := filterReady([]greenhousev1alpha1.ClusterKubeconfig{a, b})
	g.Expect(out2).To(HaveLen(0))
}

// ---- New tests for exec-plugin flags and helpers ----

func TestValidateAuthType(t *testing.T) {
	g := NewWithT(t)

	// auth-provider is always valid, no binary lookup needed
	g.Expect(validateAuthType("auth-provider", "nonexistent-binary")).To(BeNil())
	g.Expect(validateAuthType("Auth-Provider", "nonexistent-binary")).To(BeNil())

	// exec-plugin with a real binary succeeds; use os.Executable() so the test
	// is independent of PATH (the test binary is always resolvable).
	self, err := os.Executable()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(validateAuthType("exec-plugin", self)).To(BeNil())

	// exec-plugin with a missing binary fails with a helpful message
	err = validateAuthType("exec-plugin", "nonexistent-kubelogin-binary")
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("--kubelogin-path"))
	g.Expect(err.Error()).To(ContainSubstring("--auth-type=auth-provider"))

	// unknown value is rejected
	err = validateAuthType("bad-value", "")
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("invalid --auth-type"))
}

func TestSyncFlags_AuthTypeAndKubeloginDefaults(t *testing.T) {
	g := NewWithT(t)

	// Ensure flags are registered on syncCmd with correct defaults
	fAuthType := syncCmd.Flags().Lookup("auth-type")
	g.Expect(fAuthType).ToNot(BeNil())
	g.Expect(fAuthType.DefValue).To(Equal("exec-plugin"))

	fPath := syncCmd.Flags().Lookup("kubelogin-path")
	g.Expect(fPath).ToNot(BeNil())
	g.Expect(fPath.DefValue).To(Equal("kubelogin"))

	fExtra := syncCmd.Flags().Lookup("kubelogin-extra-args")
	g.Expect(fExtra).ToNot(BeNil())
	// StringSliceVar defaults to [] if nil; DefValue is representation of default (empty)
	g.Expect(fExtra.DefValue).To(Or(Equal("[]"), Equal("")))

	fCache := syncCmd.Flags().Lookup("kubelogin-token-cache-dir")
	g.Expect(fCache).ToNot(BeNil())
	home, err := os.UserHomeDir()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(fCache.DefValue).To(Equal(filepath.Join(home, ".kube", "cache", "oidc-login")))
}

func makeCKC(name, contextName string) greenhousev1alpha1.ClusterKubeconfig {
	ckc := greenhousev1alpha1.ClusterKubeconfig{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	if contextName != "" {
		ckc.Spec.Kubeconfig.Contexts = []greenhousev1alpha1.ClusterKubeconfigContextItem{
			{Name: contextName},
		}
	}
	return ckc
}

func TestBuildSyncResult(t *testing.T) {
	g := NewWithT(t)

	ready := []greenhousev1alpha1.ClusterKubeconfig{
		makeCKC("cluster-a", "ctx-a"),
		makeCKC("cluster-b", ""),
	}
	notReady := []greenhousev1alpha1.ClusterKubeconfig{
		makeCKC("cluster-c", "ctx-c"),
	}

	result := buildSyncResult(ready, notReady)

	g.Expect(result.Synced).To(Equal(2))
	g.Expect(result.Skipped).To(Equal(1))
	g.Expect(result.Failed).To(Equal(0))
	g.Expect(result.Clusters).To(HaveLen(3))

	// Ready cluster with context name
	g.Expect(result.Clusters[0].Name).To(Equal("cluster-a"))
	g.Expect(result.Clusters[0].Context).To(Equal("ctx-a"))
	g.Expect(result.Clusters[0].Status).To(Equal(output.ClusterSyncStatusSynced))

	// Ready cluster without context (empty kubeconfig)
	g.Expect(result.Clusters[1].Name).To(Equal("cluster-b"))
	g.Expect(result.Clusters[1].Context).To(Equal(""))

	// Not-ready cluster
	g.Expect(result.Clusters[2].Name).To(Equal("cluster-c"))
	g.Expect(result.Clusters[2].Context).To(Equal("ctx-c"))
	g.Expect(result.Clusters[2].Status).To(Equal(output.ClusterSyncStatusSkipped))
	g.Expect(result.Clusters[2].Reason).To(Equal("not ready"))
}

func TestBuildSyncResult_Empty(t *testing.T) {
	g := NewWithT(t)

	result := buildSyncResult(nil, nil)
	g.Expect(result.Synced).To(Equal(0))
	g.Expect(result.Skipped).To(Equal(0))
	g.Expect(result.Failed).To(Equal(0))
	g.Expect(result.Clusters).ToNot(BeNil())
	g.Expect(result.Clusters).To(BeEmpty())
}

func TestBuildFailedSyncResult(t *testing.T) {
	g := NewWithT(t)

	ready := []greenhousev1alpha1.ClusterKubeconfig{
		makeCKC("cluster-a", "ctx-a"),
	}
	notReady := []greenhousev1alpha1.ClusterKubeconfig{
		makeCKC("cluster-b", "ctx-b"),
	}
	reason := errors.New("merge failed: some error")

	result := buildFailedSyncResult(ready, notReady, reason)

	g.Expect(result.Synced).To(Equal(0))
	g.Expect(result.Failed).To(Equal(1))
	g.Expect(result.Skipped).To(Equal(1))
	g.Expect(result.Clusters).To(HaveLen(2))

	g.Expect(result.Clusters[0].Name).To(Equal("cluster-a"))
	g.Expect(result.Clusters[0].Context).To(Equal("ctx-a"))
	g.Expect(result.Clusters[0].Status).To(Equal(output.ClusterSyncStatusFailed))
	g.Expect(result.Clusters[0].Reason).To(Equal("merge failed: some error"))

	g.Expect(result.Clusters[1].Name).To(Equal("cluster-b"))
	g.Expect(result.Clusters[1].Status).To(Equal(output.ClusterSyncStatusSkipped))
	g.Expect(result.Clusters[1].Reason).To(Equal("not ready"))
}

func TestBuildKubeloginArgs_MappingAndExtras(t *testing.T) {
	g := NewWithT(t)

	cfg := map[string]string{
		"idp-issuer-url":            "https://issuer.example.com",
		"client-id":                 "cid",
		"client-secret":             "csec",
		"extra-scopes":              "groups, offline_access ,email",
		"auth-request-extra-params": "aud=foo,foo=bar",
	}
	extra := []string{"--v=4", "--token-cache-dir=/tmp/k"}

	args := buildKubeloginArgs(cfg, extra, "$(HOME)/.kube/cache/oidc-login")

	// Starts with subcommand
	g.Expect(args[0]).To(Equal("get-token"))
	// Contains mapped flags
	g.Expect(args).To(ContainElement("--oidc-issuer-url=https://issuer.example.com"))
	g.Expect(args).To(ContainElement("--oidc-client-id=cid"))
	g.Expect(args).To(ContainElement("--oidc-client-secret=csec"))
	// Each scope becomes separate flag; whitespace trimmed
	g.Expect(args).To(ContainElements(
		"--oidc-extra-scope=groups",
		"--oidc-extra-scope=offline_access",
		"--oidc-extra-scope=email",
	))
	// Extra params
	g.Expect(args).To(ContainElement("--oidc-auth-request-extra-params=aud=foo,foo=bar"))
	// Extra args appended
	g.Expect(args[len(args)-2:]).To(Equal(extra))
}

func TestBuildKubeloginArgs_ConnectorID(t *testing.T) {
	g := NewWithT(t)

	cfg := map[string]string{
		"idp-issuer-url":            "https://issuer.example.com",
		"client-id":                 "cid",
		"auth-request-extra-params": "connector_id=my-id",
	}

	args := buildKubeloginArgs(cfg, nil, "$(HOME)/.kube/cache/oidc-login")

	g.Expect(args).To(ContainElement("--oidc-auth-request-extra-params=connector_id=my-id"))
	g.Expect(args).To(ContainElement(HavePrefix("--token-cache-dir=")))
	g.Expect(args).To(ContainElement(ContainSubstring("my-id")))
}

func TestBuildKubeloginArgs_ConnectorIDInMultiParams(t *testing.T) {
	g := NewWithT(t)

	cfg := map[string]string{
		"idp-issuer-url":            "https://issuer.example.com",
		"client-id":                 "cid",
		"auth-request-extra-params": "foo=bar,connector_id=another-id,baz=qux",
	}

	args := buildKubeloginArgs(cfg, nil, "/custom/cache")

	g.Expect(args).To(ContainElement("--oidc-auth-request-extra-params=foo=bar,connector_id=another-id,baz=qux"))
	g.Expect(args).To(ContainElement("--token-cache-dir=/custom/cache/another-id"))
}

func TestAuthInfoEqual_ExecBased(t *testing.T) {
	g := NewWithT(t)

	baseArgs := []string{"get-token", "--oidc-issuer-url=x", "--oidc-client-id=a"}
	a := &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{APIVersion: "client.authentication.k8s.io/v1", Command: "kubelogin", Args: append([]string{}, baseArgs...)}}
	b := &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{APIVersion: "client.authentication.k8s.io/v1", Command: "kubelogin", Args: append([]string{}, baseArgs...)}}
	g.Expect(authInfoEqual(a, b)).To(BeTrue())

	// Change an arg should make them different
	b.Exec.Args[2] = "--oidc-client-id=DIFF"
	g.Expect(authInfoEqual(a, b)).To(BeFalse())

	// Change command should make them different
	b = b.DeepCopy()
	b.Exec.Command = "other"
	g.Expect(authInfoEqual(a, b)).To(BeFalse())
}

func TestGenerateAuthInfoKey_ExecStableIgnoresOrder(t *testing.T) {
	g := NewWithT(t)

	// Same effective parameters but different order of scope flags
	a := &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{
		APIVersion: "client.authentication.k8s.io/v1",
		Command:    "kubelogin",
		Args: []string{
			"get-token",
			"--oidc-issuer-url=https://issuer",
			"--oidc-client-id=cid",
			"--oidc-client-secret=csec",
			"--oidc-auth-request-extra-params=aud=foo",
			"--oidc-extra-scope=email",
			"--oidc-extra-scope=groups",
		},
	}}

	b := &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{
		APIVersion: "client.authentication.k8s.io/v1",
		Command:    "kubelogin",
		Args: []string{
			"get-token",
			"--oidc-extra-scope=groups",
			"--oidc-issuer-url=https://issuer",
			"--oidc-client-id=cid",
			"--oidc-client-secret=csec",
			"--oidc-extra-scope=email",
			"--oidc-auth-request-extra-params=aud=foo",
			"--v=4", // unrelated extra should not affect extracted key fields
		},
	}}

	ka := generateAuthInfoKey(a)
	kb := generateAuthInfoKey(b)
	g.Expect(ka).To(Equal(kb))
}

// ---------------------------------------------------------------------------
// AuthInfo refactor (#54) — new tests
// ---------------------------------------------------------------------------

func TestAuthInfoEqual_ExecEnvConsidered(t *testing.T) {
	g := NewWithT(t)

	base := &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{
		APIVersion: "client.authentication.k8s.io/v1",
		Command:    "kubelogin",
		Args:       []string{"get-token"},
		Env:        []clientcmdapi.ExecEnvVar{{Name: "HTTP_PROXY", Value: "http://proxy.example.com"}},
	}}
	diff := &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{
		APIVersion: "client.authentication.k8s.io/v1",
		Command:    "kubelogin",
		Args:       []string{"get-token"},
		Env:        []clientcmdapi.ExecEnvVar{{Name: "HTTP_PROXY", Value: "http://other.example.com"}},
	}}

	g.Expect(authInfoEqual(base, diff)).To(BeFalse(), "different Env values must not be equal")

	same := base.DeepCopy()
	g.Expect(authInfoEqual(base, same)).To(BeTrue(), "identical Env must be equal")
}

func TestAuthInfoEqual_ExecInteractiveModeConsidered(t *testing.T) {
	g := NewWithT(t)

	a := &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{
		APIVersion:      "client.authentication.k8s.io/v1",
		Command:         "kubelogin",
		Args:            []string{"get-token"},
		InteractiveMode: clientcmdapi.IfAvailableExecInteractiveMode,
	}}
	b := a.DeepCopy()
	b.Exec.InteractiveMode = clientcmdapi.NeverExecInteractiveMode

	g.Expect(authInfoEqual(a, b)).To(BeFalse(), "different InteractiveMode must not be equal")
	g.Expect(authInfoEqual(a, a.DeepCopy())).To(BeTrue())
}

func TestGenerateAuthInfoKey_AuthProviderIncludesIssuer(t *testing.T) {
	g := NewWithT(t)

	// Same client-id but different issuers — must produce different keys
	a := &clientcmdapi.AuthInfo{
		AuthProvider: &clientcmdapi.AuthProviderConfig{
			Name: "oidc",
			Config: map[string]string{
				"idp-issuer-url": "https://issuer-a.example.com",
				"client-id":      "same-client-id",
				"client-secret":  "same-secret",
			},
		},
	}
	b := &clientcmdapi.AuthInfo{
		AuthProvider: &clientcmdapi.AuthProviderConfig{
			Name: "oidc",
			Config: map[string]string{
				"idp-issuer-url": "https://issuer-b.example.com",
				"client-id":      "same-client-id",
				"client-secret":  "same-secret",
			},
		},
	}

	ka := generateAuthInfoKey(a)
	kb := generateAuthInfoKey(b)
	g.Expect(ka).ToNot(Equal(kb), "different idp-issuer-url must produce different keys")
}

func TestGenerateAuthInfoKey_ExecEnvAffectsKey(t *testing.T) {
	g := NewWithT(t)

	a := &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{
		APIVersion: "client.authentication.k8s.io/v1",
		Command:    "kubelogin",
		Args:       []string{"get-token", "--oidc-issuer-url=https://issuer", "--oidc-client-id=cid"},
		Env:        []clientcmdapi.ExecEnvVar{{Name: "HTTP_PROXY", Value: "http://proxy.example.com"}},
	}}
	b := &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{
		APIVersion: "client.authentication.k8s.io/v1",
		Command:    "kubelogin",
		Args:       []string{"get-token", "--oidc-issuer-url=https://issuer", "--oidc-client-id=cid"},
		// no Env
	}}

	ka := generateAuthInfoKey(a)
	kb := generateAuthInfoKey(b)
	g.Expect(ka).ToNot(Equal(kb), "exec env change must produce different key")
}

func TestMergeKubeconfig_PrefersExistingLocalAuthName(t *testing.T) {
	g := NewWithT(t)

	orig := prefix
	origMerge := mergeIdenticalUsers
	prefix = "cloudctl"
	mergeIdenticalUsers = true
	t.Cleanup(func() {
		prefix = orig
		mergeIdenticalUsers = origMerge
	})

	// The local kubeconfig has an unmanaged user entry with the same OIDC creds
	// as what the server would produce.
	localAuth := &clientcmdapi.AuthInfo{
		AuthProvider: &clientcmdapi.AuthProviderConfig{
			Name: "oidc",
			Config: map[string]string{
				"idp-issuer-url": "https://issuer.example.com",
				"client-id":      "cid",
				"client-secret":  "csec",
			},
		},
	}
	localConfig := clientcmdapi.NewConfig()
	localConfig.AuthInfos["my-existing-user"] = localAuth

	// Server config has the same OIDC creds
	serverAuth := &clientcmdapi.AuthInfo{
		AuthProvider: &clientcmdapi.AuthProviderConfig{
			Name: "oidc",
			Config: map[string]string{
				"idp-issuer-url": "https://issuer.example.com",
				"client-id":      "cid",
				"client-secret":  "csec",
			},
		},
	}
	serverConfig := clientcmdapi.NewConfig()
	serverConfig.AuthInfos["server-user"] = serverAuth
	serverConfig.Clusters["prod"] = &clientcmdapi.Cluster{Server: "https://prod.example.com"}
	serverConfig.Contexts["prod"] = &clientcmdapi.Context{
		Cluster:  "prod",
		AuthInfo: "server-user",
	}

	err := mergeKubeconfig(localConfig, serverConfig)
	g.Expect(err).ToNot(HaveOccurred())

	// The unmanaged "my-existing-user" should be reused — no cloudctl:auth-* should be created
	for name := range localConfig.AuthInfos {
		g.Expect(name).ToNot(HavePrefix("cloudctl:auth-"), "should reuse existing local user, not create managed auth entry")
	}
	// The context should reference the existing local user
	ctx, ok := localConfig.Contexts["prod"]
	g.Expect(ok).To(BeTrue())
	g.Expect(ctx.AuthInfo).To(Equal("my-existing-user"))
}

func TestMergeKubeconfig_DeduplicatesSameOIDCUsers(t *testing.T) {
	g := NewWithT(t)

	orig := prefix
	origMerge := mergeIdenticalUsers
	prefix = "cloudctl"
	mergeIdenticalUsers = true
	t.Cleanup(func() {
		prefix = orig
		mergeIdenticalUsers = origMerge
	})

	// Two clusters with the same OIDC config should share one auth entry
	sharedAuth := clientcmdapi.AuthInfo{
		AuthProvider: &clientcmdapi.AuthProviderConfig{
			Name: "oidc",
			Config: map[string]string{
				"idp-issuer-url": "https://issuer.example.com",
				"client-id":      "shared-client-id",
				"client-secret":  "shared-secret",
			},
		},
	}
	serverConfig := clientcmdapi.NewConfig()
	serverConfig.AuthInfos["cluster-a-user"] = sharedAuth.DeepCopy()
	serverConfig.AuthInfos["cluster-b-user"] = sharedAuth.DeepCopy()
	serverConfig.Clusters["cluster-a"] = &clientcmdapi.Cluster{Server: "https://a.example.com"}
	serverConfig.Clusters["cluster-b"] = &clientcmdapi.Cluster{Server: "https://b.example.com"}
	serverConfig.Contexts["cluster-a"] = &clientcmdapi.Context{Cluster: "cluster-a", AuthInfo: "cluster-a-user"}
	serverConfig.Contexts["cluster-b"] = &clientcmdapi.Context{Cluster: "cluster-b", AuthInfo: "cluster-b-user"}

	localConfig := clientcmdapi.NewConfig()
	err := mergeKubeconfig(localConfig, serverConfig)
	g.Expect(err).ToNot(HaveOccurred())

	// Count managed auth entries — should be exactly 1
	managedAuthCount := 0
	var sharedAuthName string
	for name := range localConfig.AuthInfos {
		if isManaged(name) {
			managedAuthCount++
			sharedAuthName = name
		}
	}
	g.Expect(managedAuthCount).To(Equal(1), "two clusters with same OIDC config should share one auth entry")

	// Both contexts must reference the same auth entry
	ctxA := localConfig.Contexts["cluster-a"]
	ctxB := localConfig.Contexts["cluster-b"]
	g.Expect(ctxA).ToNot(BeNil())
	g.Expect(ctxB).ToNot(BeNil())
	g.Expect(ctxA.AuthInfo).To(Equal(sharedAuthName))
	g.Expect(ctxB.AuthInfo).To(Equal(sharedAuthName))
}

// ---------------------------------------------------------------------------
// Dry-run / diffKubeconfig tests (#51)
// ---------------------------------------------------------------------------

func newCfg() *clientcmdapi.Config {
	return clientcmdapi.NewConfig()
}

func TestDiffKubeconfig_AddedCluster(t *testing.T) {
	g := NewWithT(t)
	orig := prefix
	prefix = "cloudctl"
	t.Cleanup(func() { prefix = orig })

	oldCfg := newCfg()
	newCfg2 := newCfg()
	newCfg2.Clusters["cloudctl:prod-eu-1"] = &clientcmdapi.Cluster{Server: "https://prod-eu-1.example.com"}

	diff := diffKubeconfig(oldCfg, newCfg2)
	g.Expect(diff.Clusters).To(HaveLen(1))
	g.Expect(diff.Clusters[0].Name).To(Equal("cloudctl:prod-eu-1"))
	g.Expect(diff.Clusters[0].ChangeType).To(Equal(DiffChangeAdded))
}

func TestDiffKubeconfig_RemovedCluster(t *testing.T) {
	g := NewWithT(t)
	orig := prefix
	prefix = "cloudctl"
	t.Cleanup(func() { prefix = orig })

	oldCfg := newCfg()
	oldCfg.Clusters["cloudctl:staging-de"] = &clientcmdapi.Cluster{Server: "https://staging.example.com"}
	newCfg2 := newCfg()

	diff := diffKubeconfig(oldCfg, newCfg2)
	g.Expect(diff.Clusters).To(HaveLen(1))
	g.Expect(diff.Clusters[0].Name).To(Equal("cloudctl:staging-de"))
	g.Expect(diff.Clusters[0].ChangeType).To(Equal(DiffChangeRemoved))
}

func TestDiffKubeconfig_ModifiedClusterServerURL(t *testing.T) {
	g := NewWithT(t)
	orig := prefix
	prefix = "cloudctl"
	t.Cleanup(func() { prefix = orig })

	oldCfg := newCfg()
	oldCfg.Clusters["cloudctl:prod-eu-2"] = &clientcmdapi.Cluster{Server: "https://old.example.com"}
	newCfg2 := newCfg()
	newCfg2.Clusters["cloudctl:prod-eu-2"] = &clientcmdapi.Cluster{Server: "https://new.example.com"}

	diff := diffKubeconfig(oldCfg, newCfg2)
	g.Expect(diff.Clusters).To(HaveLen(1))
	g.Expect(diff.Clusters[0].ChangeType).To(Equal(DiffChangeModified))
	g.Expect(diff.Clusters[0].Fields).To(HaveLen(1))
	g.Expect(diff.Clusters[0].Fields[0].Field).To(Equal("Server"))
	g.Expect(diff.Clusters[0].Fields[0].Old).To(Equal("https://old.example.com"))
	g.Expect(diff.Clusters[0].Fields[0].New).To(Equal("https://new.example.com"))
}

func TestDiffKubeconfig_ModifiedClusterCA(t *testing.T) {
	g := NewWithT(t)
	orig := prefix
	prefix = "cloudctl"
	t.Cleanup(func() { prefix = orig })

	oldCfg := newCfg()
	oldCfg.Clusters["cloudctl:prod"] = &clientcmdapi.Cluster{CertificateAuthorityData: []byte("old-ca-data")}
	newCfg2 := newCfg()
	newCfg2.Clusters["cloudctl:prod"] = &clientcmdapi.Cluster{CertificateAuthorityData: []byte("new-ca-data")}

	diff := diffKubeconfig(oldCfg, newCfg2)
	g.Expect(diff.Clusters).To(HaveLen(1))
	g.Expect(diff.Clusters[0].ChangeType).To(Equal(DiffChangeModified))
	caField := diff.Clusters[0].Fields[0]
	g.Expect(caField.Field).To(Equal("CA"))
	// Value should be a hex fingerprint, not raw bytes
	g.Expect(caField.Old).To(HaveLen(16))
	g.Expect(caField.New).To(HaveLen(16))
	g.Expect(caField.Old).ToNot(Equal(caField.New))
}

func TestDiffKubeconfig_AddedContext(t *testing.T) {
	g := NewWithT(t)
	orig := prefix
	prefix = "cloudctl"
	t.Cleanup(func() { prefix = orig })

	oldCfg := newCfg()
	newCfg2 := newCfg()
	// Context name has no prefix; cluster reference is prefixed (managed)
	newCfg2.Contexts["prod"] = &clientcmdapi.Context{Cluster: "cloudctl:prod", AuthInfo: "cloudctl:auth-abc"}

	diff := diffKubeconfig(oldCfg, newCfg2)
	g.Expect(diff.Contexts).To(HaveLen(1))
	g.Expect(diff.Contexts[0].ChangeType).To(Equal(DiffChangeAdded))
}

func TestDiffKubeconfig_RemovedContext(t *testing.T) {
	g := NewWithT(t)
	orig := prefix
	prefix = "cloudctl"
	t.Cleanup(func() { prefix = orig })

	oldCfg := newCfg()
	// Context name has no prefix; cluster reference is prefixed (managed)
	oldCfg.Contexts["staging"] = &clientcmdapi.Context{Cluster: "cloudctl:staging"}
	newCfg2 := newCfg()

	diff := diffKubeconfig(oldCfg, newCfg2)
	g.Expect(diff.Contexts).To(HaveLen(1))
	g.Expect(diff.Contexts[0].ChangeType).To(Equal(DiffChangeRemoved))
}

func TestDiffKubeconfig_ModifiedAuthInfoExecArgs(t *testing.T) {
	g := NewWithT(t)
	orig := prefix
	prefix = "cloudctl"
	t.Cleanup(func() { prefix = orig })

	oldCfg := newCfg()
	oldCfg.AuthInfos["cloudctl:auth-abc123"] = &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			Command: "kubelogin",
			Args:    []string{"get-token", "--oidc-client-secret=old-secret"},
		},
	}
	newCfg2 := newCfg()
	newCfg2.AuthInfos["cloudctl:auth-abc123"] = &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			Command: "kubelogin",
			Args:    []string{"get-token", "--oidc-client-secret=new-secret"},
		},
	}

	diff := diffKubeconfig(oldCfg, newCfg2)
	g.Expect(diff.AuthInfos).To(HaveLen(1))
	g.Expect(diff.AuthInfos[0].ChangeType).To(Equal(DiffChangeModified))
	g.Expect(diff.AuthInfos[0].Fields).ToNot(BeEmpty())
}

func TestDiffKubeconfig_UnchangedEntriesExcluded(t *testing.T) {
	g := NewWithT(t)
	orig := prefix
	prefix = "cloudctl"
	t.Cleanup(func() { prefix = orig })

	cfg := newCfg()
	cfg.Clusters["cloudctl:unchanged"] = &clientcmdapi.Cluster{Server: "https://same.example.com"}

	diff := diffKubeconfig(cfg, cfg)
	g.Expect(diff.Clusters).To(BeEmpty(), "unchanged entries must not appear in diff")
}

func TestDiffKubeconfig_UnmanagedEntriesIgnored(t *testing.T) {
	g := NewWithT(t)
	orig := prefix
	prefix = "cloudctl"
	t.Cleanup(func() { prefix = orig })

	oldCfg := newCfg()
	oldCfg.Clusters["my-own-cluster"] = &clientcmdapi.Cluster{Server: "https://personal.example.com"}
	newCfg2 := newCfg()
	// unmanaged entry removed from new — must not appear in diff

	diff := diffKubeconfig(oldCfg, newCfg2)
	g.Expect(diff.Clusters).To(BeEmpty())
	g.Expect(diff.Contexts).To(BeEmpty())
	g.Expect(diff.AuthInfos).To(BeEmpty())
}

func TestRunSync_DryRun_NoWrite(t *testing.T) {
	g := NewWithT(t)

	orig := prefix
	origMerge := mergeIdenticalUsers
	prefix = "cloudctl"
	mergeIdenticalUsers = true
	t.Cleanup(func() {
		prefix = orig
		mergeIdenticalUsers = origMerge
	})

	// Build a "before" and "after" config to simulate what dry-run does
	localConfigBefore := clientcmdapi.NewConfig()
	localConfigBefore.Clusters["cloudctl:existing"] = &clientcmdapi.Cluster{Server: "https://existing.example.com"}
	localConfigBefore.Contexts["existing"] = &clientcmdapi.Context{Cluster: "cloudctl:existing", AuthInfo: "cloudctl:auth-abc"}

	// Simulate an incoming server config that adds a new cluster
	serverConfig := clientcmdapi.NewConfig()
	serverConfig.Clusters["existing"] = &clientcmdapi.Cluster{Server: "https://existing.example.com"}
	serverConfig.Clusters["new-cluster"] = &clientcmdapi.Cluster{Server: "https://new.example.com"}
	sharedAuth := &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			APIVersion: "client.authentication.k8s.io/v1",
			Command:    "kubelogin",
			Args:       []string{"get-token", "--oidc-issuer-url=https://issuer.example.com"},
		},
	}
	serverConfig.AuthInfos["shared-user"] = sharedAuth
	serverConfig.Contexts["existing"] = &clientcmdapi.Context{Cluster: "existing", AuthInfo: "shared-user"}
	serverConfig.Contexts["new-cluster"] = &clientcmdapi.Context{Cluster: "new-cluster", AuthInfo: "shared-user"}

	localConfig := localConfigBefore.DeepCopy()
	err := mergeKubeconfig(localConfig, serverConfig)
	g.Expect(err).ToNot(HaveOccurred())

	diff := diffKubeconfig(localConfigBefore, localConfig)
	result := buildDryRunResult(diff, localConfigBefore, localConfig)

	// The new cluster access should appear as added
	g.Expect(result.Added).To(BeNumerically(">=", 1))
	g.Expect(result.Accesses).To(ContainElement(
		HaveField("ChangeType", "added"),
	))

	// Original localConfigBefore must not have been modified
	g.Expect(localConfigBefore.Clusters).ToNot(HaveKey("cloudctl:new-cluster"))
}

// ---------------------------------------------------------------------------
// buildAccessDiffs tests
// ---------------------------------------------------------------------------

func TestBuildAccessDiffs_Added(t *testing.T) {
	g := NewWithT(t)
	orig := prefix
	prefix = "cloudctl"
	t.Cleanup(func() { prefix = orig })

	oldCfg := newCfg()
	newCfg2 := newCfg()
	newCfg2.Clusters["cloudctl:prod-eu-1"] = &clientcmdapi.Cluster{Server: "https://prod-eu-1.example.com"}
	newCfg2.Contexts["prod-eu-1"] = &clientcmdapi.Context{Cluster: "cloudctl:prod-eu-1", AuthInfo: "cloudctl:auth-abc"}

	diff := diffKubeconfig(oldCfg, newCfg2)
	accesses := buildAccessDiffs(diff, oldCfg, newCfg2)

	g.Expect(accesses).To(HaveLen(1))
	g.Expect(accesses[0].Name).To(Equal("prod-eu-1"))
	g.Expect(accesses[0].ChangeType).To(Equal("added"))
	g.Expect(accesses[0].Server).To(Equal("https://prod-eu-1.example.com"))
}

func TestBuildAccessDiffs_Removed(t *testing.T) {
	g := NewWithT(t)
	orig := prefix
	prefix = "cloudctl"
	t.Cleanup(func() { prefix = orig })

	oldCfg := newCfg()
	oldCfg.Clusters["cloudctl:staging"] = &clientcmdapi.Cluster{Server: "https://staging.example.com"}
	oldCfg.Contexts["staging"] = &clientcmdapi.Context{Cluster: "cloudctl:staging", AuthInfo: "cloudctl:auth-abc"}
	newCfg2 := newCfg()

	diff := diffKubeconfig(oldCfg, newCfg2)
	accesses := buildAccessDiffs(diff, oldCfg, newCfg2)

	g.Expect(accesses).To(HaveLen(1))
	g.Expect(accesses[0].Name).To(Equal("staging"))
	g.Expect(accesses[0].ChangeType).To(Equal("removed"))
	g.Expect(accesses[0].Server).To(Equal("https://staging.example.com"))
}

func TestBuildAccessDiffs_ModifiedServer(t *testing.T) {
	g := NewWithT(t)
	orig := prefix
	prefix = "cloudctl"
	t.Cleanup(func() { prefix = orig })

	oldCfg := newCfg()
	oldCfg.Clusters["cloudctl:prod-eu-2"] = &clientcmdapi.Cluster{Server: "https://old.example.com"}
	oldCfg.Contexts["prod-eu-2"] = &clientcmdapi.Context{Cluster: "cloudctl:prod-eu-2", AuthInfo: "cloudctl:auth-abc"}
	oldCfg.AuthInfos["cloudctl:auth-abc"] = &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{Command: "kubelogin", Args: []string{"get-token"}},
	}

	newCfg2 := newCfg()
	newCfg2.Clusters["cloudctl:prod-eu-2"] = &clientcmdapi.Cluster{Server: "https://new.example.com"}
	newCfg2.Contexts["prod-eu-2"] = &clientcmdapi.Context{Cluster: "cloudctl:prod-eu-2", AuthInfo: "cloudctl:auth-abc"}
	newCfg2.AuthInfos["cloudctl:auth-abc"] = &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{Command: "kubelogin", Args: []string{"get-token"}},
	}

	diff := diffKubeconfig(oldCfg, newCfg2)
	accesses := buildAccessDiffs(diff, oldCfg, newCfg2)

	g.Expect(accesses).To(HaveLen(1))
	g.Expect(accesses[0].Name).To(Equal("prod-eu-2"))
	g.Expect(accesses[0].ChangeType).To(Equal("modified"))
	g.Expect(accesses[0].Fields).To(ContainElement(
		And(HaveField("Field", "Server"), HaveField("Old", "https://old.example.com"), HaveField("New", "https://new.example.com")),
	))
}

func TestBuildAccessDiffs_NoChanges(t *testing.T) {
	g := NewWithT(t)
	orig := prefix
	prefix = "cloudctl"
	t.Cleanup(func() { prefix = orig })

	cfg := newCfg()
	cfg.Clusters["cloudctl:unchanged"] = &clientcmdapi.Cluster{Server: "https://same.example.com"}
	cfg.Contexts["unchanged"] = &clientcmdapi.Context{Cluster: "cloudctl:unchanged", AuthInfo: "cloudctl:auth-abc"}

	diff := diffKubeconfig(cfg, cfg)
	accesses := buildAccessDiffs(diff, cfg, cfg)

	g.Expect(accesses).To(BeEmpty())
}
