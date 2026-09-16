// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/cloudoperators/greenhouse/api/v1alpha1"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHasAuth(t *testing.T) {
	g := NewWithT(t)

	g.Expect(hasAuth(&rest.Config{})).To(BeFalse(), "no auth should be detected")

	g.Expect(hasAuth(&rest.Config{BearerToken: "x"})).To(BeTrue(), "bearer token should be detected")
	g.Expect(hasAuth(&rest.Config{BearerTokenFile: "/tmp/token"})).To(BeTrue(), "bearer token file should be detected")
	g.Expect(hasAuth(&rest.Config{Username: "u", Password: "p"})).To(BeTrue(), "basic auth should be detected")
	g.Expect(hasAuth(&rest.Config{TLSClientConfig: rest.TLSClientConfig{CertData: []byte("cert")}})).To(BeTrue(), "client cert data should be detected")
	g.Expect(hasAuth(&rest.Config{TLSClientConfig: rest.TLSClientConfig{CertFile: "/tmp/cert"}})).To(BeTrue(), "client cert file should be detected")
	g.Expect(hasAuth(&rest.Config{ExecProvider: &clientcmdapi.ExecConfig{Command: "kubelogin"}})).To(BeTrue(), "exec provider should be detected")

	g.Expect(hasAuth(&rest.Config{
		AuthProvider: &clientcmdapi.AuthProviderConfig{Config: map[string]string{"id-token": "t"}},
	})).To(BeTrue(), "auth provider with id-token should be detected")

	g.Expect(hasAuth(&rest.Config{
		AuthProvider: &clientcmdapi.AuthProviderConfig{Config: map[string]string{"refresh-token": "r"}},
	})).To(BeFalse(), "auth provider without id-token should not be detected")
}

func TestGetUnauthenticatedVersion_OK(t *testing.T) {
	g := NewWithT(t)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(&version.Info{GitVersion: "v1.28.3"})
	}))
	defer srv.Close()

	cfg := &rest.Config{Host: srv.URL, TLSClientConfig: rest.TLSClientConfig{Insecure: true}}
	v, err := getUnauthenticatedVersion(context.Background(), cfg)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(v).ToNot(BeNil())
	g.Expect(v.GitVersion).To(Equal("v1.28.3"))
}

func TestGetUnauthenticatedVersion_StatusError(t *testing.T) {
	g := NewWithT(t)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	cfg := &rest.Config{Host: srv.URL, TLSClientConfig: rest.TLSClientConfig{Insecure: true}}
	_, err := getUnauthenticatedVersion(context.Background(), cfg)
	g.Expect(err).To(HaveOccurred())
}

func TestGetUnauthenticatedVersion_InsecureTLS(t *testing.T) {
	g := NewWithT(t)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(&version.Info{GitVersion: "v0.0.0"})
	}))
	defer srv.Close()

	cfgInsecure := &rest.Config{Host: srv.URL, TLSClientConfig: rest.TLSClientConfig{Insecure: true}}
	_, err := getUnauthenticatedVersion(context.Background(), cfgInsecure)
	g.Expect(err).ToNot(HaveOccurred())

	cfgStrict := &rest.Config{Host: srv.URL, TLSClientConfig: rest.TLSClientConfig{Insecure: false}}
	_, err = getUnauthenticatedVersion(context.Background(), cfgStrict)
	g.Expect(err).To(HaveOccurred())

	_ = tls.Config{} // keep import used
}

func TestGetAuthenticatedVersion_OK(t *testing.T) {
	g := NewWithT(t)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(&version.Info{GitVersion: "v1.30.0"})
	}))
	defer srv.Close()

	// Use the test server's CA so TLS verification passes without InsecureSkipVerify.
	cfg := &rest.Config{
		Host:      srv.URL,
		Transport: srv.Client().Transport,
	}
	v, err := getAuthenticatedVersion(context.Background(), cfg)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(v).ToNot(BeNil())
	g.Expect(v.GitVersion).To(Equal("v1.30.0"))
}

func TestGetAuthenticatedVersion_NonOKStatus(t *testing.T) {
	g := NewWithT(t)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &rest.Config{
		Host:      srv.URL,
		Transport: srv.Client().Transport,
	}
	_, err := getAuthenticatedVersion(context.Background(), cfg)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("500"))
}

func TestClusterVersionKubeconfigFlag_DefaultEqualsRecommendedHomeFile(t *testing.T) {
	g := NewWithT(t)

	// Verify that the --kubeconfig flag default equals clientcmd.RecommendedHomeFile.
	// resolveKubeconfig detects "user did not explicitly set a path" via viper.IsSet:
	// when the flag is unset, viper returns the default and IsSet returns false.
	f := clusterVersionCmd.Flags().Lookup("kubeconfig")
	g.Expect(f).ToNot(BeNil())
	g.Expect(f.DefValue).To(Equal(clientcmd.RecommendedHomeFile))
}

func newGreenhouseFakeClient(objs ...v1alpha1.ClusterKubeconfig) *fake.ClientBuilder {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	builder := fake.NewClientBuilder().WithScheme(scheme)
	for i := range objs {
		builder = builder.WithObjects(&objs[i])
	}
	return builder
}

func TestNormalizeVersion(t *testing.T) {
	g := NewWithT(t)

	g.Expect(normalizeVersion("v1.29.3")).To(Equal("1.29.3"))
	g.Expect(normalizeVersion("1.29.3")).To(Equal("1.29.3"))
	g.Expect(normalizeVersion("v1.31.4+k3s1")).To(Equal("1.31.4"))
	g.Expect(normalizeVersion("v1.29.3-eks-1234567")).To(Equal("1.29.3"))
	g.Expect(normalizeVersion("v1.31.4-k3s1")).To(Equal("1.31.4"))
}

func TestVersionLabelFromClient_LabelPresent(t *testing.T) {
	g := NewWithT(t)

	ckc := v1alpha1.ClusterKubeconfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-eu",
			Namespace: "my-org",
			// Greenhouse controller stores values like "v1.29.3" or "v1.31.4-k3s1".
			Labels: map[string]string{"greenhouse.sap/kubernetes-version": "v1.29.3"},
		},
	}
	c := newGreenhouseFakeClient(ckc).Build()

	ver, err := versionLabelFromClient(context.Background(), c, "my-org", "prod-eu")
	g.Expect(err).ToNot(HaveOccurred())
	// versionLabelFromClient returns the raw label; normalization is the caller's job.
	g.Expect(ver).To(Equal("v1.29.3"))
	g.Expect(normalizeVersion(ver)).To(Equal("1.29.3"))
}

func TestVersionLabelFromClient_LabelAbsent(t *testing.T) {
	g := NewWithT(t)

	ckc := v1alpha1.ClusterKubeconfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-eu",
			Namespace: "my-org",
		},
	}
	c := newGreenhouseFakeClient(ckc).Build()

	ver, err := versionLabelFromClient(context.Background(), c, "my-org", "prod-eu")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(ver).To(BeEmpty())
}

func TestVersionLabelFromClient_NotFound(t *testing.T) {
	g := NewWithT(t)

	c := newGreenhouseFakeClient().Build()

	ver, err := versionLabelFromClient(context.Background(), c, "my-org", "missing-cluster")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(ver).To(BeEmpty())
}

func TestVersionLabelFromClient_WrongNamespace(t *testing.T) {
	g := NewWithT(t)

	ckc := v1alpha1.ClusterKubeconfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-eu",
			Namespace: "other-org",
			Labels:    map[string]string{"greenhouse.sap/kubernetes-version": "1.30.0"},
		},
	}
	c := newGreenhouseFakeClient(ckc).Build()

	// Looking up in the wrong namespace returns not-found, falls back gracefully.
	ver, err := versionLabelFromClient(context.Background(), c, "my-org", "prod-eu")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(ver).To(BeEmpty())
}

func TestClusterVersionGreenhouseFlags(t *testing.T) {
	g := NewWithT(t)

	g.Expect(clusterVersionCmd.Flags().Lookup("greenhouse-cluster-kubeconfig")).ToNot(BeNil())
	g.Expect(clusterVersionCmd.Flags().Lookup("greenhouse-cluster-context")).ToNot(BeNil())
	g.Expect(clusterVersionCmd.Flags().Lookup("greenhouse-cluster-namespace")).ToNot(BeNil())
	g.Expect(clusterVersionCmd.Flags().Lookup("greenhouse-cluster-name")).ToNot(BeNil())
}

func TestGetVersionFromLabel_BadKubeconfig(t *testing.T) {
	g := NewWithT(t)

	// A kubeconfig with invalid YAML should cause getVersionFromLabel to return an error.
	f, err := os.CreateTemp("", "bad-kube-*.yaml")
	g.Expect(err).ToNot(HaveOccurred())
	defer func() { _ = os.Remove(f.Name()) }()
	_, _ = f.WriteString("invalid yaml: [")
	g.Expect(f.Close()).To(Succeed())

	ver, err := getVersionFromLabel(context.Background(), f.Name(), "", "my-org", "prod-eu")
	g.Expect(err).To(HaveOccurred())
	g.Expect(ver).To(BeEmpty())
}
