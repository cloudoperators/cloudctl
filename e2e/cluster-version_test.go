// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestE2E_ClusterVersion(t *testing.T) {
	g := NewWithT(t)

	kubeconfig := resolveKubeconfig(t)
	requireFileG(g, kubeconfig)

	bin := resolveBin(t)
	requireFileG(g, bin)

	re := regexp.MustCompile(`^\d+(\.\d+)*$`)

	g.Eventually(func() error {
		stdout, stderr, err := runCmd(bin, "cluster-version", "-k", kubeconfig)
		if err != nil {
			return fmt.Errorf("cluster-version error: %v (stderr: %s)", err, stderr)
		}
		out := strings.TrimSpace(stdout)
		if out == "" {
			return fmt.Errorf("empty cluster-version output")
		}
		if !re.MatchString(out) {
			return fmt.Errorf("unexpected version format: %q", out)
		}
		return nil
	}, time.Minute, 3*time.Second).Should(Succeed())
}
