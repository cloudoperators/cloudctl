// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestE2E_Sync(t *testing.T) {
	g := NewWithT(t)

	kubeconfig := resolveKubeconfig(t)
	requireFileG(g, kubeconfig)
	bin := resolveBin(t)
	requireFileG(g, bin)

	ns := "e2e-sync"
	prefix := "e2e"
	crFile := filepath.Join(os.TempDir(), "clusterkubeconfig-e2e.yaml")

	// Prefer applying the CRD from the repository path that matches the provided spec (greenhouse.sap group).
	remoteCRD := "https://raw.githubusercontent.com/cloudoperators/greenhouse/refs/heads/main/charts/manager/crds/greenhouse.sap_clusterkubeconfigs.yaml"

	// Try local cache first (optional), otherwise fall back to the remote CRD above.
	appliedCRD := false
	if modDir := getModuleDir(t, "github.com/cloudoperators/greenhouse"); modDir != "" {
		local := filepath.Join(modDir, "charts", "manager", "crds", "greenhouse.sap_clusterkubeconfigs.yaml")
		if fi, err := os.Stat(local); err == nil && !fi.IsDir() {
			if _, stderr, err := runCmd("kubectl", "--kubeconfig", kubeconfig, "apply", "-f", local); err == nil {
				appliedCRD = true
			} else {
				t.Logf("failed applying local CRD %s: %s", local, stderr)
			}
		}
	}
	if !appliedCRD {
		if _, stderr, err := runCmd("kubectl", "--kubeconfig", kubeconfig, "apply", "-f", remoteCRD); err == nil {
			appliedCRD = true
		} else {
			t.Skipf("failed applying CRD from %s: %s", remoteCRD, stderr)
		}
	}

	// Wait until the CRD is established; this CRD uses greenhouse.sap
	g.Eventually(func() error {
		if _, _, err := runCmd("kubectl", "--kubeconfig", kubeconfig, "get", "crd", "clusterkubeconfigs.greenhouse.sap"); err == nil {
			return nil
		}
		return fmt.Errorf("crd not found yet")
	}, 90*time.Second, 3*time.Second).Should(Succeed())

	// Demo CR aligned with the CRD schema; omit empty certificate-authority-data to satisfy byte type.
	crYAML := `
apiVersion: greenhouse.sap/v1alpha1
kind: ClusterKubeconfig
metadata:
  name: demo
  namespace: ` + ns + `
spec:
  kubeconfig:
    clusters:
    - name: rc
      cluster:
        server: https://example.invalid
    users:
    - name: user
      user:
        auth-provider:
          name: oidc
          config:
            client-id: demo
    contexts:
    - name: ctx1
      context:
        cluster: rc
        user: user
        namespace: default
`
	writeFile(t, crFile, crYAML)

	// Ensure namespace
	if _, _, err := runCmd("kubectl", "--kubeconfig", kubeconfig, "get", "ns", ns); err != nil {
		if _, stderr, err := runCmd("kubectl", "--kubeconfig", kubeconfig, "create", "ns", ns); err != nil {
			t.Fatalf("create ns: %v (stderr: %s)", err, stderr)
		}
	}

	// Apply CR
	if _, stderr, err := runCmd("kubectl", "--kubeconfig", kubeconfig, "apply", "-f", crFile); err != nil {
		t.Fatalf("apply CR failed: %v (stderr: %s)", err, stderr)
	}

	// Target kubeconfig file
	targetKubeconfig := filepath.Join(os.TempDir(), "e2e-sync-target-kubeconfig")
	createEmptyKubeconfigFile(t, targetKubeconfig)

	// Run sync
	if _, stderr, err := runCmd(bin,
		"sync",
		"--greenhouse-cluster-kubeconfig", kubeconfig,
		"--greenhouse-cluster-namespace", ns,
		"--remote-cluster-kubeconfig", targetKubeconfig,
		"--prefix", prefix,
	); err != nil {
		t.Fatalf("sync failed: %v (stderr: %s)", err, stderr)
	}

	// Validate
	cfg, loadErr := clientcmd.LoadFromFile(targetKubeconfig)
	g.Expect(loadErr).ToNot(HaveOccurred())

	managedCluster := prefix + ":" + "rc"
	g.Expect(cfg.Clusters).To(HaveKey(managedCluster))
	g.Expect(cfg.Contexts).To(HaveKey("ctx1"))
	g.Expect(cfg.Contexts["ctx1"].Cluster).To(Equal(managedCluster))
	g.Expect(cfg.Contexts["ctx1"].AuthInfo).To(ContainSubstring(prefix + ":"))

	// Cleanup (best-effort)
	_, _, _ = runCmd("kubectl", "--kubeconfig", kubeconfig, "delete", "-f", crFile, "--ignore-not-found=true")
	_, _, _ = runCmd("kubectl", "--kubeconfig", kubeconfig, "delete", "ns", ns, "--ignore-not-found=true")
}

// Helpers

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func createEmptyKubeconfigFile(t *testing.T, path string) {
	t.Helper()
	cfg := clientcmdapi.NewConfig()
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		t.Fatalf("write empty kubeconfig: %v", err)
	}
}

func getModuleDir(t *testing.T, module string) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-json", module).Output()
	if err != nil {
		// Return empty when not available; caller may skip to remote
		return ""
	}
	var m struct {
		Path string
		Dir  string
	}
	if jerr := json.Unmarshal(out, &m); jerr != nil {
		return ""
	}
	return m.Dir
}
