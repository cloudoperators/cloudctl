//go:build e2e
// +build e2e

package e2e

import (
	"encoding/json"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func TestE2E_Version(t *testing.T) {
	g := NewWithT(t)

	bin := resolveBin(t)
	requireFileG(g, bin)

	stdout, stderr, err := runCmd(bin, "version", "--json")
	g.Expect(err).ToNot(HaveOccurred(), "stderr: %s", stderr)

	var vi versionInfo
	g.Expect(json.Unmarshal([]byte(stdout), &vi)).To(Succeed(), "output: %s", stdout)

	g.Expect(vi.Version).ToNot(BeEmpty())
	g.Expect(vi.GoVersion).ToNot(BeEmpty())
	g.Expect(vi.Platform).ToNot(BeEmpty())

	_ = filepath.Separator // keep filepath import used on all platforms
}
