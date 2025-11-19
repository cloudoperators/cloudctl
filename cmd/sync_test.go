// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"testing"

	greenhousemetav1alpha1 "github.com/cloudoperators/greenhouse/api/meta/v1alpha1"
	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
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
