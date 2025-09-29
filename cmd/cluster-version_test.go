// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/rest"
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
	v, err := getUnauthenticatedVersion(cfg)
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
	_, err := getUnauthenticatedVersion(cfg)
	g.Expect(err).To(HaveOccurred())
}

func TestGetUnauthenticatedVersion_InsecureTLS(t *testing.T) {
	g := NewWithT(t)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(&version.Info{GitVersion: "v0.0.0"})
	}))
	defer srv.Close()

	cfgInsecure := &rest.Config{Host: srv.URL, TLSClientConfig: rest.TLSClientConfig{Insecure: true}}
	_, err := getUnauthenticatedVersion(cfgInsecure)
	g.Expect(err).ToNot(HaveOccurred())

	cfgStrict := &rest.Config{Host: srv.URL, TLSClientConfig: rest.TLSClientConfig{Insecure: false}}
	_, err = getUnauthenticatedVersion(cfgStrict)
	g.Expect(err).To(HaveOccurred())

	_ = tls.Config{} // keep import used
}
