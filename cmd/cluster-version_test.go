// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
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

	// Verify that the --kubeconfig flag default equals clientcmd.RecommendedHomeFile
	// so that resolveKubeconfig can detect "user did not explicitly set a path".
	f := clusterVersionCmd.Flags().Lookup("kubeconfig")
	g.Expect(f).ToNot(BeNil())
	g.Expect(f.DefValue).To(Equal(clientcmd.RecommendedHomeFile))
}
