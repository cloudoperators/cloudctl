// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

type versionInfo struct {
	Version   string `json:"version"`
	GitCommit string `json:"gitCommit"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
	Compiler  string `json:"compiler"`
	Platform  string `json:"platform"`
}

func runCmd(bin string, args ...string) (string, string, error) {
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(),
		"CLOUDCTL_TEST=1",
		"KUBECONFIG="+os.Getenv("E2E_KUBECONFIG"),
	)
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func requireFileG(g Gomega, path string) {
	fi, err := os.Stat(path)
	g.Expect(err).ToNot(HaveOccurred(), "stat %s", path)
	g.Expect(fi.IsDir()).To(BeFalse(), "expected file, got directory: %s", path)
}

// resolveBin attempts to find the cloudctl binary using E2E_BIN or common locations.
func resolveBin(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("E2E_BIN"); v != "" {
		if fi, err := os.Stat(v); err == nil && !fi.IsDir() {
			return v
		}
	}
	candidates := []string{
		filepath.Join(".", "bin", "cloudctl"),
		filepath.Join("..", "..", "bin", "cloudctl"),
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	if p, err := exec.LookPath("cloudctl"); err == nil {
		return p
	}
	t.Fatalf("cloudctl binary not found. Set E2E_BIN or run 'make build' to produce ./bin/cloudctl")
	return ""
}

// resolveKubeconfig attempts to find kubeconfig using E2E_KUBECONFIG or common locations.
func resolveKubeconfig(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("E2E_KUBECONFIG"); v != "" {
		if fi, err := os.Stat(v); err == nil && !fi.IsDir() {
			return v
		}
	}
	cluster := os.Getenv("E2E_CLUSTER_NAME")
	if cluster == "" {
		cluster = "cloudctl-e2e"
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(".", "tmp", "e2e-kubeconfig"),
		filepath.Join(home, ".config", "k3d", "kubeconfig-"+cluster+".yaml"),
		filepath.Join(".", "test", "e2e", "e2e-kubeconfig"),
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	t.Skipf("kubeconfig not found. Set E2E_KUBECONFIG or run 'make e2e-up' to create a kubeconfig")
	return ""
}
