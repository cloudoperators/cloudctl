// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/spf13/pflag"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// realGreenHouseKubeconfig returns a kubeconfig that matches exactly what
// Greenhouse serves for an organization: OIDC auth-provider, CA data, namespace.
func realGreenhouseKubeconfig(org string) *clientcmdapi.Config {
	name := "greenhouse-" + org
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[name] = &clientcmdapi.Cluster{
		Server:                   "https://greenhouse.global.cloud.sap",
		CertificateAuthorityData: []byte("fake-ca-data"),
	}
	cfg.AuthInfos[name] = &clientcmdapi.AuthInfo{
		AuthProvider: &clientcmdapi.AuthProviderConfig{
			Name: "oidc",
			Config: map[string]string{
				"idp-issuer-url": "https://idp.global.cloud.sap",
				"client-id":      "greenhouse",
				"client-secret":  "",
				"extra-scopes":   "groups",
			},
		},
	}
	cfg.Contexts[name] = &clientcmdapi.Context{
		Cluster:   name,
		AuthInfo:  name,
		Namespace: org,
	}
	cfg.CurrentContext = name
	return cfg
}

// encodeKubeconfig serialises cfg to YAML and base64-encodes it.
func encodeKubeconfig(t *testing.T, cfg *clientcmdapi.Config) string {
	t.Helper()
	raw, err := clientcmd.Write(*cfg)
	if err != nil {
		t.Fatalf("encodeKubeconfig: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// writeTempKubeconfig writes cfg to a temp file and returns the path.
func writeTempKubeconfig(t *testing.T, cfg *clientcmdapi.Config) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "kubeconfig-*.yaml")
	if err != nil {
		t.Fatalf("writeTempKubeconfig: %v", err)
	}
	raw, err := clientcmd.Write(*cfg)
	if err != nil {
		t.Fatalf("writeTempKubeconfig write: %v", err)
	}
	if _, err := f.Write(raw); err != nil {
		t.Fatalf("writeTempKubeconfig: %v", err)
	}
	_ = f.Close()
	return f.Name()
}

// loadKubeconfig loads a kubeconfig from disk.
func loadKubeconfig(t *testing.T, path string) *clientcmdapi.Config {
	t.Helper()
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		t.Fatalf("loadKubeconfig: %v", err)
	}
	return cfg
}

// runBootstrapCmd executes the bootstrap cobra command with the given args and
// returns stdout, stderr, and any error. It resets global flag vars before each run.
func runBootstrapCmd(t *testing.T, args []string) (stdout, stderr string, err error) {
	t.Helper()

	// Reset package-level flag vars so tests are isolated from each other.
	bootstrapData = ""
	bootstrapServer = ""
	bootstrapOrg = ""
	bootstrapCAData = ""
	bootstrapIDPIssuerURL = ""
	bootstrapClientID = ""
	bootstrapClientSecret = ""
	bootstrapExtraScopes = ""
	bootstrapNamespace = ""
	bootstrapKubeconfig = ""
	bootstrapContextName = ""
	bootstrapSetCurrentCtx = false
	bootstrapDryRun = false

	// Reset cobra's "Changed" state on bootstrapCmd flags so flag.Changed() is
	// accurate for each test regardless of what previous tests passed.
	bootstrapCmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })

	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}

	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	rootCmd.SetArgs(append([]string{"bootstrap"}, args...))
	err = rootCmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// ── buildOIDCKubeconfig ───────────────────────────────────────────────────────

func TestBuildOIDCKubeconfig_AllFields(t *testing.T) {
	g := NewWithT(t)

	caB64 := base64.StdEncoding.EncodeToString([]byte("fake-ca"))
	cfg, err := buildOIDCKubeconfig(
		"https://greenhouse.example.com",
		"my-org",
		caB64,
		"https://idp.example.com",
		"greenhouse",
		"secret123",
		"groups",
		"",
	)
	g.Expect(err).To(BeNil())

	name := "greenhouse-my-org"
	g.Expect(cfg.CurrentContext).To(Equal(name))

	cl := cfg.Clusters[name]
	g.Expect(cl).NotTo(BeNil())
	g.Expect(cl.Server).To(Equal("https://greenhouse.example.com"))
	g.Expect(cl.CertificateAuthorityData).To(Equal([]byte("fake-ca")))

	ai := cfg.AuthInfos[name]
	g.Expect(ai).NotTo(BeNil())
	g.Expect(ai.AuthProvider).NotTo(BeNil())
	g.Expect(ai.AuthProvider.Name).To(Equal("oidc"))
	g.Expect(ai.AuthProvider.Config["idp-issuer-url"]).To(Equal("https://idp.example.com"))
	g.Expect(ai.AuthProvider.Config["client-id"]).To(Equal("greenhouse"))
	g.Expect(ai.AuthProvider.Config["client-secret"]).To(Equal("secret123"))
	g.Expect(ai.AuthProvider.Config["extra-scopes"]).To(Equal("groups"))

	ctx := cfg.Contexts[name]
	g.Expect(ctx).NotTo(BeNil())
	g.Expect(ctx.Cluster).To(Equal(name))
	g.Expect(ctx.AuthInfo).To(Equal(name))
	g.Expect(ctx.Namespace).To(Equal("my-org")) // defaults to org
}

func TestBuildOIDCKubeconfig_ExplicitNamespace(t *testing.T) {
	g := NewWithT(t)
	cfg, err := buildOIDCKubeconfig(
		"https://greenhouse.example.com", "my-org", "",
		"https://idp.example.com", "greenhouse", "", "", "custom-ns",
	)
	g.Expect(err).To(BeNil())
	g.Expect(cfg.Contexts["greenhouse-my-org"].Namespace).To(Equal("custom-ns"))
}

func TestBuildOIDCKubeconfig_NoExtraScopes(t *testing.T) {
	g := NewWithT(t)
	cfg, err := buildOIDCKubeconfig(
		"https://greenhouse.example.com", "my-org", "",
		"https://idp.example.com", "greenhouse", "", "", "",
	)
	g.Expect(err).To(BeNil())
	_, hasScopes := cfg.AuthInfos["greenhouse-my-org"].AuthProvider.Config["extra-scopes"]
	g.Expect(hasScopes).To(BeFalse())
}

func TestBuildOIDCKubeconfig_InvalidCAData(t *testing.T) {
	g := NewWithT(t)
	_, err := buildOIDCKubeconfig(
		"https://greenhouse.example.com", "my-org", "not-valid-base64!!!",
		"https://idp.example.com", "greenhouse", "", "", "",
	)
	g.Expect(err).To(MatchError(ContainSubstring("not valid base64")))
}

func TestBuildOIDCKubeconfig_MatchesRealGreenhouseShape(t *testing.T) {
	g := NewWithT(t)

	caB64 := base64.StdEncoding.EncodeToString([]byte("fake-ca-data"))
	cfg, err := buildOIDCKubeconfig(
		"https://greenhouse.global.cloud.sap",
		"sap-cna",
		caB64,
		"https://idp.global.cloud.sap",
		"greenhouse",
		"",
		"groups",
		"",
	)
	g.Expect(err).To(BeNil())

	want := realGreenhouseKubeconfig("sap-cna")

	name := "greenhouse-sap-cna"
	g.Expect(cfg.Clusters[name].Server).To(Equal(want.Clusters[name].Server))
	g.Expect(cfg.Clusters[name].CertificateAuthorityData).To(Equal(want.Clusters[name].CertificateAuthorityData))
	g.Expect(cfg.AuthInfos[name].AuthProvider.Name).To(Equal(want.AuthInfos[name].AuthProvider.Name))
	g.Expect(cfg.AuthInfos[name].AuthProvider.Config).To(Equal(want.AuthInfos[name].AuthProvider.Config))
	g.Expect(cfg.Contexts[name].Namespace).To(Equal(want.Contexts[name].Namespace))
	g.Expect(cfg.CurrentContext).To(Equal(want.CurrentContext))
}

// ── renameKubeconfigContext ───────────────────────────────────────────────────

func TestRenameKubeconfigContext_RenamesAll(t *testing.T) {
	g := NewWithT(t)

	cfg := realGreenhouseKubeconfig("sap-cna")
	renameKubeconfigContext(cfg, "my-custom-name")

	g.Expect(cfg.CurrentContext).To(Equal("my-custom-name"))
	g.Expect(cfg.Contexts).To(HaveKey("my-custom-name"))
	g.Expect(cfg.Contexts).NotTo(HaveKey("greenhouse-sap-cna"))
	g.Expect(cfg.Clusters).To(HaveKey("my-custom-name"))
	g.Expect(cfg.Clusters).NotTo(HaveKey("greenhouse-sap-cna"))
	g.Expect(cfg.AuthInfos).To(HaveKey("my-custom-name"))
	g.Expect(cfg.AuthInfos).NotTo(HaveKey("greenhouse-sap-cna"))

	ctx := cfg.Contexts["my-custom-name"]
	g.Expect(ctx.Cluster).To(Equal("my-custom-name"))
	g.Expect(ctx.AuthInfo).To(Equal("my-custom-name"))
	g.Expect(ctx.Namespace).To(Equal("sap-cna")) // namespace unchanged
}

func TestRenameKubeconfigContext_NoOpWhenNameUnchanged(t *testing.T) {
	g := NewWithT(t)

	cfg := realGreenhouseKubeconfig("sap-cna")
	renameKubeconfigContext(cfg, "greenhouse-sap-cna")

	g.Expect(cfg.CurrentContext).To(Equal("greenhouse-sap-cna"))
	g.Expect(cfg.Contexts).To(HaveKey("greenhouse-sap-cna"))
	g.Expect(cfg.Clusters).To(HaveKey("greenhouse-sap-cna"))
	g.Expect(cfg.AuthInfos).To(HaveKey("greenhouse-sap-cna"))
}

func TestRenameKubeconfigContext_MultiContextBlobOnlyRenamesCurrent(t *testing.T) {
	g := NewWithT(t)

	// A blob with two contexts — only the current one should be renamed.
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["cluster-a"] = &clientcmdapi.Cluster{Server: "https://a.example.com"}
	cfg.Clusters["cluster-b"] = &clientcmdapi.Cluster{Server: "https://b.example.com"}
	cfg.AuthInfos["user-a"] = &clientcmdapi.AuthInfo{}
	cfg.AuthInfos["user-b"] = &clientcmdapi.AuthInfo{}
	cfg.Contexts["ctx-a"] = &clientcmdapi.Context{Cluster: "cluster-a", AuthInfo: "user-a"}
	cfg.Contexts["ctx-b"] = &clientcmdapi.Context{Cluster: "cluster-b", AuthInfo: "user-b"}
	cfg.CurrentContext = "ctx-a"

	renameKubeconfigContext(cfg, "gh-prod")

	// ctx-a → gh-prod; ctx-b untouched
	g.Expect(cfg.Contexts).To(HaveKey("gh-prod"))
	g.Expect(cfg.Contexts).NotTo(HaveKey("ctx-a"))
	g.Expect(cfg.Contexts).To(HaveKey("ctx-b"))
	g.Expect(cfg.Clusters).To(HaveKey("gh-prod"))
	g.Expect(cfg.Clusters).To(HaveKey("cluster-b"))
	g.Expect(cfg.AuthInfos).To(HaveKey("gh-prod"))
	g.Expect(cfg.AuthInfos).To(HaveKey("user-b"))
	g.Expect(cfg.CurrentContext).To(Equal("gh-prod"))
}

func TestRenameKubeconfigContext_SharedClusterPreserved(t *testing.T) {
	g := NewWithT(t)

	// Two contexts share the same cluster and authinfo — renaming one should
	// not delete the shared keys.
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["shared-cluster"] = &clientcmdapi.Cluster{Server: "https://shared.example.com"}
	cfg.AuthInfos["shared-user"] = &clientcmdapi.AuthInfo{}
	cfg.Contexts["ctx-a"] = &clientcmdapi.Context{Cluster: "shared-cluster", AuthInfo: "shared-user"}
	cfg.Contexts["ctx-b"] = &clientcmdapi.Context{Cluster: "shared-cluster", AuthInfo: "shared-user"}
	cfg.CurrentContext = "ctx-a"

	renameKubeconfigContext(cfg, "gh-prod")

	// The renamed context uses new keys.
	g.Expect(cfg.Contexts).To(HaveKey("gh-prod"))
	g.Expect(cfg.Contexts).NotTo(HaveKey("ctx-a"))
	// ctx-b still references shared-cluster and shared-user — they must not be deleted.
	g.Expect(cfg.Clusters).To(HaveKey("shared-cluster"))
	g.Expect(cfg.AuthInfos).To(HaveKey("shared-user"))
	// The renamed context also gets gh-prod cluster/user copies.
	g.Expect(cfg.Clusters).To(HaveKey("gh-prod"))
	g.Expect(cfg.AuthInfos).To(HaveKey("gh-prod"))
}

func TestRenameKubeconfigContext_ContextKeyMatchesButEntriesDiffer(t *testing.T) {
	g := NewWithT(t)

	// The context key already equals targetName, but cluster/authinfo keys differ.
	// renameKubeconfigContext must still rename the cluster and authinfo entries.
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["greenhouse-org-cluster"] = &clientcmdapi.Cluster{Server: "https://greenhouse.example.com"}
	cfg.AuthInfos["greenhouse-org-user"] = &clientcmdapi.AuthInfo{}
	cfg.Contexts["greenhouse-org"] = &clientcmdapi.Context{Cluster: "greenhouse-org-cluster", AuthInfo: "greenhouse-org-user"}
	cfg.CurrentContext = "greenhouse-org"

	renameKubeconfigContext(cfg, "greenhouse-org")

	// Context key unchanged.
	g.Expect(cfg.Contexts).To(HaveKey("greenhouse-org"))
	g.Expect(cfg.CurrentContext).To(Equal("greenhouse-org"))
	// Cluster and authinfo must be renamed to match the context name.
	g.Expect(cfg.Clusters).To(HaveKey("greenhouse-org"))
	g.Expect(cfg.Clusters).NotTo(HaveKey("greenhouse-org-cluster"))
	g.Expect(cfg.AuthInfos).To(HaveKey("greenhouse-org"))
	g.Expect(cfg.AuthInfos).NotTo(HaveKey("greenhouse-org-user"))
	// The context's Cluster and AuthInfo fields must point to the new keys.
	g.Expect(cfg.Contexts["greenhouse-org"].Cluster).To(Equal("greenhouse-org"))
	g.Expect(cfg.Contexts["greenhouse-org"].AuthInfo).To(Equal("greenhouse-org"))
}

func TestRenameKubeconfigContext_NilContextEntryDoesNotPanic(t *testing.T) {
	// A multi-context blob where one context value is nil (e.g. decoded from a
	// YAML null) must not panic when computing cluster/authinfo ref-counts.
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["greenhouse-org"] = &clientcmdapi.Cluster{Server: "https://greenhouse.example.com"}
	cfg.AuthInfos["greenhouse-org"] = &clientcmdapi.AuthInfo{}
	cfg.Contexts["greenhouse-org"] = &clientcmdapi.Context{Cluster: "greenhouse-org", AuthInfo: "greenhouse-org"}
	cfg.Contexts["other-ctx"] = nil // nil entry simulating a null in the YAML
	cfg.CurrentContext = "greenhouse-org"

	g := NewWithT(t)
	g.Expect(func() { renameKubeconfigContext(cfg, "gh-prod") }).NotTo(Panic())
	g.Expect(cfg.Contexts).To(HaveKey("gh-prod"))
	g.Expect(cfg.CurrentContext).To(Equal("gh-prod"))
}

// ── mergeBootstrapKubeconfig ──────────────────────────────────────────────────

func TestMergeBootstrapKubeconfig_AddsAllEntries(t *testing.T) {
	g := NewWithT(t)

	local := clientcmdapi.NewConfig()
	incoming := realGreenhouseKubeconfig("sap-cna")

	result, err := mergeBootstrapKubeconfig(local, local, incoming, "greenhouse-sap-cna", false, "sap-cna")
	g.Expect(err).To(BeNil())

	g.Expect(result.Added).To(HaveLen(3))
	g.Expect(result.Skipped).To(BeEmpty())
	g.Expect(local.Clusters).To(HaveKey("greenhouse-sap-cna"))
	g.Expect(local.AuthInfos).To(HaveKey("greenhouse-sap-cna"))
	g.Expect(local.Contexts).To(HaveKey("greenhouse-sap-cna"))
	g.Expect(local.CurrentContext).To(BeEmpty()) // setCurrentCtx=false
}

func TestMergeBootstrapKubeconfig_SetsCurrentContext(t *testing.T) {
	g := NewWithT(t)

	local := clientcmdapi.NewConfig()
	incoming := realGreenhouseKubeconfig("sap-cna")

	result, err := mergeBootstrapKubeconfig(local, local, incoming, "greenhouse-sap-cna", true, "sap-cna")
	g.Expect(err).To(BeNil())
	g.Expect(local.CurrentContext).To(Equal("greenhouse-sap-cna"))
	g.Expect(result.CurrentContextUpdated).To(BeTrue())
}

func TestMergeBootstrapKubeconfig_IdempotentSkipsExisting(t *testing.T) {
	g := NewWithT(t)

	incoming := realGreenhouseKubeconfig("sap-cna")
	local := realGreenhouseKubeconfig("sap-cna") // same entries already present

	result, err := mergeBootstrapKubeconfig(local, local, incoming, "greenhouse-sap-cna", false, "sap-cna")
	g.Expect(err).To(BeNil())
	g.Expect(result.Added).To(BeEmpty())
	g.Expect(result.Skipped).To(HaveLen(3))
}

func TestMergeBootstrapKubeconfig_NeverOverwritesExistingEntries(t *testing.T) {
	g := NewWithT(t)

	// Local has an existing cluster entry with a different server URL.
	local := clientcmdapi.NewConfig()
	local.Clusters["greenhouse-sap-cna"] = &clientcmdapi.Cluster{Server: "https://original.example.com"}

	incoming := realGreenhouseKubeconfig("sap-cna")
	_, err := mergeBootstrapKubeconfig(local, local, incoming, "greenhouse-sap-cna", false, "sap-cna")
	g.Expect(err).To(BeNil())

	// Original server must not be overwritten.
	g.Expect(local.Clusters["greenhouse-sap-cna"].Server).To(Equal("https://original.example.com"))
}

func TestMergeBootstrapKubeconfig_PreservesExistingLocalEntries(t *testing.T) {
	g := NewWithT(t)

	// Local already has unrelated entries.
	local := clientcmdapi.NewConfig()
	local.Clusters["other-cluster"] = &clientcmdapi.Cluster{Server: "https://other.example.com"}
	local.AuthInfos["other-user"] = &clientcmdapi.AuthInfo{Token: "tok"}
	local.Contexts["other-ctx"] = &clientcmdapi.Context{Cluster: "other-cluster", AuthInfo: "other-user"}
	local.CurrentContext = "other-ctx"

	incoming := realGreenhouseKubeconfig("sap-cna")
	_, err := mergeBootstrapKubeconfig(local, local, incoming, "greenhouse-sap-cna", false, "sap-cna")
	g.Expect(err).To(BeNil())

	// Unrelated entries must still be there.
	g.Expect(local.Clusters).To(HaveKey("other-cluster"))
	g.Expect(local.AuthInfos).To(HaveKey("other-user"))
	g.Expect(local.Contexts).To(HaveKey("other-ctx"))
	// Current context unchanged because setCurrentCtx=false.
	g.Expect(local.CurrentContext).To(Equal("other-ctx"))
}

// ── resolveIncomingKubeconfig (via package-level vars) ───────────────────────

func TestResolveIncomingKubeconfig_DataBlob(t *testing.T) {
	g := NewWithT(t)

	want := realGreenhouseKubeconfig("sap-cna")
	bootstrapData = encodeKubeconfig(t, want)
	bootstrapOrg = ""
	t.Cleanup(func() { bootstrapData = ""; bootstrapOrg = "" })

	cfg, org, err := resolveIncomingKubeconfig()
	g.Expect(err).To(BeNil())
	g.Expect(org).To(BeEmpty()) // no --greenhouse-org provided alongside --data
	g.Expect(cfg.Clusters).To(HaveKey("greenhouse-sap-cna"))
	g.Expect(cfg.AuthInfos["greenhouse-sap-cna"].AuthProvider.Name).To(Equal("oidc"))
	g.Expect(cfg.AuthInfos["greenhouse-sap-cna"].AuthProvider.Config["idp-issuer-url"]).
		To(Equal("https://idp.global.cloud.sap"))
	g.Expect(cfg.Contexts["greenhouse-sap-cna"].Namespace).To(Equal("sap-cna"))
}

func TestResolveIncomingKubeconfig_DataBlobWithOrgOverride(t *testing.T) {
	g := NewWithT(t)

	want := realGreenhouseKubeconfig("sap-cna")
	bootstrapData = encodeKubeconfig(t, want)
	bootstrapOrg = "override-org"
	t.Cleanup(func() { bootstrapData = ""; bootstrapOrg = "" })

	_, org, err := resolveIncomingKubeconfig()
	g.Expect(err).To(BeNil())
	g.Expect(org).To(Equal("override-org"))
}

func TestResolveIncomingKubeconfig_DataBlobInvalidBase64(t *testing.T) {
	g := NewWithT(t)

	bootstrapData = "not!!!base64"
	t.Cleanup(func() { bootstrapData = "" })

	_, _, err := resolveIncomingKubeconfig()
	g.Expect(err).To(MatchError(ContainSubstring("not valid base64")))
}

func TestResolveIncomingKubeconfig_DataBlobNotKubeconfig(t *testing.T) {
	g := NewWithT(t)

	bootstrapData = base64.StdEncoding.EncodeToString([]byte("just some text, not yaml"))
	t.Cleanup(func() { bootstrapData = "" })

	_, _, err := resolveIncomingKubeconfig()
	g.Expect(err).To(MatchError(ContainSubstring("valid kubeconfig")))
}

func TestResolveIncomingKubeconfig_DataBlobEmptyClusters(t *testing.T) {
	g := NewWithT(t)

	empty := clientcmdapi.NewConfig()
	bootstrapData = encodeKubeconfig(t, empty)
	t.Cleanup(func() { bootstrapData = "" })

	_, _, err := resolveIncomingKubeconfig()
	g.Expect(err).To(MatchError(ContainSubstring("no clusters")))
}

func TestResolveIncomingKubeconfig_DataBlobNoCurrentContext(t *testing.T) {
	g := NewWithT(t)

	// Two contexts, no CurrentContext set — ambiguous, should error.
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["cluster-a"] = &clientcmdapi.Cluster{Server: "https://a.example.com"}
	cfg.Clusters["cluster-b"] = &clientcmdapi.Cluster{Server: "https://b.example.com"}
	cfg.AuthInfos["user-a"] = &clientcmdapi.AuthInfo{}
	cfg.AuthInfos["user-b"] = &clientcmdapi.AuthInfo{}
	cfg.Contexts["ctx-a"] = &clientcmdapi.Context{Cluster: "cluster-a", AuthInfo: "user-a"}
	cfg.Contexts["ctx-b"] = &clientcmdapi.Context{Cluster: "cluster-b", AuthInfo: "user-b"}
	bootstrapData = encodeKubeconfig(t, cfg)
	t.Cleanup(func() { bootstrapData = "" })

	_, _, err := resolveIncomingKubeconfig()
	g.Expect(err).To(MatchError(ContainSubstring("no current-context")))
}

func TestResolveIncomingKubeconfig_DataBlobMissingClusterRef(t *testing.T) {
	g := NewWithT(t)

	// Context references a cluster that doesn't exist in the blob.
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["cluster-a"] = &clientcmdapi.Cluster{Server: "https://a.example.com"}
	cfg.AuthInfos["user-a"] = &clientcmdapi.AuthInfo{}
	cfg.Contexts["ctx-a"] = &clientcmdapi.Context{Cluster: "missing-cluster", AuthInfo: "user-a"}
	cfg.CurrentContext = "ctx-a"
	bootstrapData = encodeKubeconfig(t, cfg)
	t.Cleanup(func() { bootstrapData = "" })

	_, _, err := resolveIncomingKubeconfig()
	g.Expect(err).To(MatchError(ContainSubstring("missing-cluster")))
}

func TestResolveIncomingKubeconfig_IndividualFlags(t *testing.T) {
	g := NewWithT(t)

	bootstrapServer = "https://greenhouse.example.com"
	bootstrapOrg = "my-org"
	bootstrapIDPIssuerURL = "https://idp.example.com"
	bootstrapClientID = "greenhouse"
	bootstrapClientSecret = ""
	bootstrapExtraScopes = "groups"
	bootstrapNamespace = ""
	t.Cleanup(func() {
		bootstrapServer = ""
		bootstrapOrg = ""
		bootstrapIDPIssuerURL = ""
		bootstrapClientID = ""
		bootstrapClientSecret = ""
		bootstrapExtraScopes = ""
		bootstrapNamespace = ""
	})

	cfg, org, err := resolveIncomingKubeconfig()
	g.Expect(err).To(BeNil())
	g.Expect(org).To(Equal("my-org"))

	name := "greenhouse-my-org"
	g.Expect(cfg.Clusters[name].Server).To(Equal("https://greenhouse.example.com"))
	g.Expect(cfg.AuthInfos[name].AuthProvider.Name).To(Equal("oidc"))
	g.Expect(cfg.AuthInfos[name].AuthProvider.Config["idp-issuer-url"]).To(Equal("https://idp.example.com"))
	g.Expect(cfg.AuthInfos[name].AuthProvider.Config["client-id"]).To(Equal("greenhouse"))
	g.Expect(cfg.AuthInfos[name].AuthProvider.Config["extra-scopes"]).To(Equal("groups"))
	g.Expect(cfg.Contexts[name].Namespace).To(Equal("my-org"))
}

func TestResolveIncomingKubeconfig_MissingServer(t *testing.T) {
	g := NewWithT(t)
	bootstrapOrg = "my-org"
	bootstrapIDPIssuerURL = "https://idp.example.com"
	bootstrapClientID = "greenhouse"
	t.Cleanup(func() { bootstrapOrg = ""; bootstrapIDPIssuerURL = ""; bootstrapClientID = "" })

	_, _, err := resolveIncomingKubeconfig()
	g.Expect(err).To(MatchError(ContainSubstring("--greenhouse-server")))
}

func TestResolveIncomingKubeconfig_MissingOrg(t *testing.T) {
	g := NewWithT(t)
	bootstrapServer = "https://greenhouse.example.com"
	bootstrapIDPIssuerURL = "https://idp.example.com"
	bootstrapClientID = "greenhouse"
	t.Cleanup(func() { bootstrapServer = ""; bootstrapIDPIssuerURL = ""; bootstrapClientID = "" })

	_, _, err := resolveIncomingKubeconfig()
	g.Expect(err).To(MatchError(ContainSubstring("--greenhouse-org")))
}

func TestResolveIncomingKubeconfig_MissingIDPIssuerURL(t *testing.T) {
	g := NewWithT(t)
	bootstrapServer = "https://greenhouse.example.com"
	bootstrapOrg = "my-org"
	bootstrapClientID = "greenhouse"
	t.Cleanup(func() { bootstrapServer = ""; bootstrapOrg = ""; bootstrapClientID = "" })

	_, _, err := resolveIncomingKubeconfig()
	g.Expect(err).To(MatchError(ContainSubstring("--greenhouse-idp-issuer-url")))
}

func TestResolveIncomingKubeconfig_MissingClientID(t *testing.T) {
	g := NewWithT(t)
	bootstrapServer = "https://greenhouse.example.com"
	bootstrapOrg = "my-org"
	bootstrapIDPIssuerURL = "https://idp.example.com"
	t.Cleanup(func() { bootstrapServer = ""; bootstrapOrg = ""; bootstrapIDPIssuerURL = "" })

	_, _, err := resolveIncomingKubeconfig()
	g.Expect(err).To(MatchError(ContainSubstring("--greenhouse-client-id")))
}

// ── end-to-end via cobra command ──────────────────────────────────────────────

// newEmptyKubeconfigFile creates a temp file with an empty (but valid) kubeconfig.
func newEmptyKubeconfigFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	empty := clientcmdapi.NewConfig()
	raw, err := clientcmd.Write(*empty)
	if err != nil {
		t.Fatalf("newEmptyKubeconfigFile: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("newEmptyKubeconfigFile: %v", err)
	}
	return path
}

func TestBootstrapCmd_DataBlob_WritesCorrectKubeconfig(t *testing.T) {
	g := NewWithT(t)

	src := realGreenhouseKubeconfig("sap-cna")
	data := encodeKubeconfig(t, src)
	dest := newEmptyKubeconfigFile(t)

	stdout, _, err := runBootstrapCmd(t, []string{
		"--data=" + data,
		"--context-name=greenhouse-sap-cna",
		"--set-current-context",
		"--kubeconfig=" + dest,
	})
	g.Expect(err).To(BeNil())
	g.Expect(stdout).To(ContainSubstring(`[+] cluster "greenhouse-sap-cna"`))
	g.Expect(stdout).To(ContainSubstring(`[+] user "greenhouse-sap-cna"`))
	g.Expect(stdout).To(ContainSubstring(`[+] context "greenhouse-sap-cna"`))
	g.Expect(stdout).To(ContainSubstring("Bootstrap complete."))
	g.Expect(stdout).To(ContainSubstring("cloudctl sync -n sap-cna"))

	result := loadKubeconfig(t, dest)
	g.Expect(result.CurrentContext).To(Equal("greenhouse-sap-cna"))

	cl := result.Clusters["greenhouse-sap-cna"]
	g.Expect(cl).NotTo(BeNil())
	g.Expect(cl.Server).To(Equal("https://greenhouse.global.cloud.sap"))
	g.Expect(cl.CertificateAuthorityData).To(Equal([]byte("fake-ca-data")))

	ai := result.AuthInfos["greenhouse-sap-cna"]
	g.Expect(ai).NotTo(BeNil())
	g.Expect(ai.AuthProvider.Name).To(Equal("oidc"))
	g.Expect(ai.AuthProvider.Config["idp-issuer-url"]).To(Equal("https://idp.global.cloud.sap"))
	g.Expect(ai.AuthProvider.Config["client-id"]).To(Equal("greenhouse"))
	g.Expect(ai.AuthProvider.Config["client-secret"]).To(Equal(""))
	g.Expect(ai.AuthProvider.Config["extra-scopes"]).To(Equal("groups"))

	ctx := result.Contexts["greenhouse-sap-cna"]
	g.Expect(ctx).NotTo(BeNil())
	g.Expect(ctx.Cluster).To(Equal("greenhouse-sap-cna"))
	g.Expect(ctx.AuthInfo).To(Equal("greenhouse-sap-cna"))
	g.Expect(ctx.Namespace).To(Equal("sap-cna"))
}

func TestBootstrapCmd_IndividualFlags_WritesOIDCKubeconfig(t *testing.T) {
	g := NewWithT(t)

	caB64 := base64.StdEncoding.EncodeToString([]byte("fake-ca-data"))
	dest := newEmptyKubeconfigFile(t)

	_, _, err := runBootstrapCmd(t, []string{
		"--greenhouse-server=https://greenhouse.global.cloud.sap",
		"--greenhouse-org=sap-cna",
		"--greenhouse-idp-issuer-url=https://idp.global.cloud.sap",
		"--greenhouse-client-id=greenhouse",
		"--greenhouse-client-secret=",
		"--greenhouse-extra-scopes=groups",
		"--greenhouse-ca-data=" + caB64,
		"--context-name=greenhouse-sap-cna",
		"--set-current-context",
		"--kubeconfig=" + dest,
	})
	g.Expect(err).To(BeNil())

	result := loadKubeconfig(t, dest)
	cl := result.Clusters["greenhouse-sap-cna"]
	g.Expect(cl.Server).To(Equal("https://greenhouse.global.cloud.sap"))
	g.Expect(cl.CertificateAuthorityData).To(Equal([]byte("fake-ca-data")))

	ai := result.AuthInfos["greenhouse-sap-cna"]
	g.Expect(ai.AuthProvider.Name).To(Equal("oidc"))
	g.Expect(ai.AuthProvider.Config["idp-issuer-url"]).To(Equal("https://idp.global.cloud.sap"))
	g.Expect(ai.AuthProvider.Config["client-id"]).To(Equal("greenhouse"))
	g.Expect(ai.AuthProvider.Config["extra-scopes"]).To(Equal("groups"))

	ctx := result.Contexts["greenhouse-sap-cna"]
	g.Expect(ctx.Namespace).To(Equal("sap-cna"))
	g.Expect(result.CurrentContext).To(Equal("greenhouse-sap-cna"))
}

func TestBootstrapCmd_IndividualFlags_DataAndIndividualFlagsProduceSameShape(t *testing.T) {
	g := NewWithT(t)

	// Build via individual flags.
	caB64 := base64.StdEncoding.EncodeToString([]byte("fake-ca-data"))
	dest1 := newEmptyKubeconfigFile(t)
	_, _, err := runBootstrapCmd(t, []string{
		"--greenhouse-server=https://greenhouse.global.cloud.sap",
		"--greenhouse-org=sap-cna",
		"--greenhouse-idp-issuer-url=https://idp.global.cloud.sap",
		"--greenhouse-client-id=greenhouse",
		"--greenhouse-client-secret=",
		"--greenhouse-extra-scopes=groups",
		"--greenhouse-ca-data=" + caB64,
		"--context-name=greenhouse-sap-cna",
		"--kubeconfig=" + dest1,
	})
	g.Expect(err).To(BeNil())

	// Build via --data blob from the same source config.
	src := realGreenhouseKubeconfig("sap-cna")
	data := encodeKubeconfig(t, src)
	dest2 := newEmptyKubeconfigFile(t)
	_, _, err = runBootstrapCmd(t, []string{
		"--data=" + data,
		"--context-name=greenhouse-sap-cna",
		"--kubeconfig=" + dest2,
	})
	g.Expect(err).To(BeNil())

	r1 := loadKubeconfig(t, dest1)
	r2 := loadKubeconfig(t, dest2)

	cl1 := r1.Clusters["greenhouse-sap-cna"]
	cl2 := r2.Clusters["greenhouse-sap-cna"]
	g.Expect(cl1.Server).To(Equal(cl2.Server))
	g.Expect(cl1.CertificateAuthorityData).To(Equal(cl2.CertificateAuthorityData))

	ai1 := r1.AuthInfos["greenhouse-sap-cna"]
	ai2 := r2.AuthInfos["greenhouse-sap-cna"]
	g.Expect(ai1.AuthProvider.Name).To(Equal(ai2.AuthProvider.Name))
	g.Expect(ai1.AuthProvider.Config).To(Equal(ai2.AuthProvider.Config))

	ctx1 := r1.Contexts["greenhouse-sap-cna"]
	ctx2 := r2.Contexts["greenhouse-sap-cna"]
	g.Expect(ctx1.Namespace).To(Equal(ctx2.Namespace))
}

func TestBootstrapCmd_DryRun_DoesNotWriteFile(t *testing.T) {
	g := NewWithT(t)

	src := realGreenhouseKubeconfig("sap-cna")
	data := encodeKubeconfig(t, src)
	dest := newEmptyKubeconfigFile(t)

	before, err := os.ReadFile(dest)
	g.Expect(err).To(BeNil())

	stdout, _, err := runBootstrapCmd(t, []string{
		"--data=" + data,
		"--context-name=greenhouse-sap-cna",
		"--kubeconfig=" + dest,
		"--dry-run",
	})
	g.Expect(err).To(BeNil())
	g.Expect(stdout).To(ContainSubstring("Dry-run"))
	g.Expect(stdout).To(ContainSubstring(`[+] cluster "greenhouse-sap-cna"`))

	after, err := os.ReadFile(dest)
	g.Expect(err).To(BeNil())
	g.Expect(after).To(Equal(before)) // file must be unchanged
}

func TestBootstrapCmd_Idempotent(t *testing.T) {
	g := NewWithT(t)

	src := realGreenhouseKubeconfig("sap-cna")
	data := encodeKubeconfig(t, src)
	dest := newEmptyKubeconfigFile(t)

	args := []string{
		"--data=" + data,
		"--context-name=greenhouse-sap-cna",
		"--kubeconfig=" + dest,
	}

	_, _, err := runBootstrapCmd(t, args)
	g.Expect(err).To(BeNil())

	// Second run must succeed and report all entries as skipped.
	stdout, _, err := runBootstrapCmd(t, args)
	g.Expect(err).To(BeNil())
	g.Expect(stdout).To(ContainSubstring(`[=] cluster "greenhouse-sap-cna" (already exists)`))
	g.Expect(stdout).To(ContainSubstring(`[=] user "greenhouse-sap-cna" (already exists)`))
	g.Expect(stdout).To(ContainSubstring(`[=] context "greenhouse-sap-cna" (already exists)`))
	g.Expect(stdout).NotTo(ContainSubstring("[+]"))
}

func TestBootstrapCmd_ContextRename(t *testing.T) {
	g := NewWithT(t)

	src := realGreenhouseKubeconfig("sap-cna") // default name: greenhouse-sap-cna
	data := encodeKubeconfig(t, src)
	dest := newEmptyKubeconfigFile(t)

	_, _, err := runBootstrapCmd(t, []string{
		"--data=" + data,
		"--context-name=gh-prod", // user picks a custom name
		"--set-current-context",
		"--kubeconfig=" + dest,
	})
	g.Expect(err).To(BeNil())

	result := loadKubeconfig(t, dest)
	g.Expect(result.CurrentContext).To(Equal("gh-prod"))
	g.Expect(result.Clusters).To(HaveKey("gh-prod"))
	g.Expect(result.AuthInfos).To(HaveKey("gh-prod"))
	g.Expect(result.Contexts).To(HaveKey("gh-prod"))
	g.Expect(result.Clusters).NotTo(HaveKey("greenhouse-sap-cna"))
	// Namespace preserved after rename.
	g.Expect(result.Contexts["gh-prod"].Namespace).To(Equal("sap-cna"))
}

func TestBootstrapCmd_PreservesExistingUnmanagedEntries(t *testing.T) {
	g := NewWithT(t)

	// Start with a kubeconfig that already has an unrelated entry.
	existing := clientcmdapi.NewConfig()
	existing.Clusters["my-cluster"] = &clientcmdapi.Cluster{Server: "https://my.cluster.example.com"}
	existing.AuthInfos["my-user"] = &clientcmdapi.AuthInfo{Token: "tok"}
	existing.Contexts["my-ctx"] = &clientcmdapi.Context{Cluster: "my-cluster", AuthInfo: "my-user"}
	existing.CurrentContext = "my-ctx"
	dest := writeTempKubeconfig(t, existing)

	src := realGreenhouseKubeconfig("sap-cna")
	data := encodeKubeconfig(t, src)

	_, _, err := runBootstrapCmd(t, []string{
		"--data=" + data,
		"--context-name=greenhouse-sap-cna",
		"--kubeconfig=" + dest,
	})
	g.Expect(err).To(BeNil())

	result := loadKubeconfig(t, dest)
	// Existing entry must still be present.
	g.Expect(result.Clusters).To(HaveKey("my-cluster"))
	g.Expect(result.AuthInfos).To(HaveKey("my-user"))
	g.Expect(result.Contexts).To(HaveKey("my-ctx"))
	// Current context unchanged because --set-current-context was not passed.
	g.Expect(result.CurrentContext).To(Equal("my-ctx"))
	// New Greenhouse entry also present.
	g.Expect(result.Clusters).To(HaveKey("greenhouse-sap-cna"))
}

func TestBootstrapCmd_CreatesKubeconfigFileWhenAbsent(t *testing.T) {
	g := NewWithT(t)

	src := realGreenhouseKubeconfig("sap-cna")
	data := encodeKubeconfig(t, src)
	dest := filepath.Join(t.TempDir(), "new-kubeconfig.yaml") // does not exist yet

	_, _, err := runBootstrapCmd(t, []string{
		"--data=" + data,
		"--context-name=greenhouse-sap-cna",
		"--kubeconfig=" + dest,
	})
	g.Expect(err).To(BeNil())
	g.Expect(dest).To(BeAnExistingFile())

	result := loadKubeconfig(t, dest)
	g.Expect(result.Clusters).To(HaveKey("greenhouse-sap-cna"))
}

func TestBootstrapCmd_JSONOutput(t *testing.T) {
	g := NewWithT(t)

	src := realGreenhouseKubeconfig("sap-cna")
	data := encodeKubeconfig(t, src)
	dest := newEmptyKubeconfigFile(t)

	stdout, _, err := runBootstrapCmd(t, []string{
		"--data=" + data,
		"--context-name=greenhouse-sap-cna",
		"--kubeconfig=" + dest,
		"-o", "json",
	})
	g.Expect(err).To(BeNil())
	g.Expect(stdout).To(ContainSubstring(`"contextName": "greenhouse-sap-cna"`))
	g.Expect(stdout).To(ContainSubstring(`"added"`))
}

func TestBootstrapCmd_MissingBothDataAndServer(t *testing.T) {
	g := NewWithT(t)
	dest := newEmptyKubeconfigFile(t)

	_, _, err := runBootstrapCmd(t, []string{
		"--kubeconfig=" + dest,
		"--greenhouse-org=my-org",
	})
	g.Expect(err).To(MatchError(ContainSubstring("--greenhouse-server")))
}

func TestBootstrapCmd_RawBase64Encoding(t *testing.T) {
	g := NewWithT(t)

	src := realGreenhouseKubeconfig("sap-cna")
	raw, err := clientcmd.Write(*src)
	g.Expect(err).To(BeNil())

	// Some tools emit raw base64 (no padding).
	data := base64.RawStdEncoding.EncodeToString(raw)
	// Ensure there's no padding so we're testing the raw path.
	g.Expect(strings.Contains(data, "=")).To(BeFalse())

	dest := newEmptyKubeconfigFile(t)
	_, _, err = runBootstrapCmd(t, []string{
		"--data=" + data,
		"--context-name=greenhouse-sap-cna",
		"--kubeconfig=" + dest,
	})
	g.Expect(err).To(BeNil())
	result := loadKubeconfig(t, dest)
	g.Expect(result.Clusters).To(HaveKey("greenhouse-sap-cna"))
}

func TestBootstrapCmd_KUBECONFIGEmptyFirstSegment(t *testing.T) {
	g := NewWithT(t)
	cfg := realGreenhouseKubeconfig("sap")
	data := encodeKubeconfig(t, cfg)

	second := writeTempKubeconfig(t, clientcmdapi.NewConfig())
	t.Setenv("KUBECONFIG", string(os.PathListSeparator)+second)

	_, _, err := runBootstrapCmd(t, []string{
		"--data=" + data,
		"--context-name=greenhouse-sap",
	})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("no usable first path"))
}
