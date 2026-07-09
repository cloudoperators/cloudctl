// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	"sigs.k8s.io/yaml"

	"github.com/cloudoperators/cloudctl/cmd/output"
)

// ---------------------------------------------------------------------------
// Format parsing
// ---------------------------------------------------------------------------

func TestParseFormat_Valid(t *testing.T) {
	tests := []struct {
		input    string
		expected output.Format
	}{
		{"text", output.FormatText},
		{"json", output.FormatJSON},
		{"yaml", output.FormatYAML},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			g := NewWithT(t)
			f, err := output.ParseFormat(tc.input)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(f).To(Equal(tc.expected))
		})
	}
}

func TestParseFormat_Invalid(t *testing.T) {
	g := NewWithT(t)
	_, err := output.ParseFormat("xml")
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("xml"))
}

// ---------------------------------------------------------------------------
// JSON printer
// ---------------------------------------------------------------------------

func TestJSONPrinter_SyncResult(t *testing.T) {
	g := NewWithT(t)
	p := output.New(output.FormatJSON, false, new(bytes.Buffer))

	var buf bytes.Buffer
	p2 := output.New(output.FormatJSON, false, &buf)
	result := output.SyncResult{
		Clusters: []output.ClusterSyncResult{
			{Name: "a", Context: "ctx-a", Status: output.ClusterSyncStatusSynced},
			{Name: "b", Context: "ctx-b", Status: output.ClusterSyncStatusSkipped, Reason: "not ready"},
		},
		Synced:  1,
		Skipped: 1,
		Failed:  0,
	}
	g.Expect(p2.Print(result)).To(Succeed())
	_ = p // satisfy linter

	var got output.SyncResult
	g.Expect(json.Unmarshal(buf.Bytes(), &got)).To(Succeed())
	g.Expect(got.Synced).To(Equal(1))
	g.Expect(got.Skipped).To(Equal(1))
	g.Expect(got.Failed).To(Equal(0))
	g.Expect(got.Clusters).To(HaveLen(2))
	g.Expect(got.Clusters[1].Reason).To(Equal("not ready"))
}

func TestJSONPrinter_VersionInfo(t *testing.T) {
	g := NewWithT(t)
	var buf bytes.Buffer
	p := output.New(output.FormatJSON, false, &buf)
	info := output.VersionInfo{
		Version:   "v1.2.3",
		GitCommit: "abc123",
		BuildDate: "2025-01-01",
		GoVersion: "go1.25.0",
		Compiler:  "gc",
		Platform:  "linux/amd64",
	}
	g.Expect(p.Print(info)).To(Succeed())

	var got output.VersionInfo
	g.Expect(json.Unmarshal(buf.Bytes(), &got)).To(Succeed())
	g.Expect(got.Version).To(Equal("v1.2.3"))
	g.Expect(got.GoVersion).To(Equal("go1.25.0"))
	g.Expect(got.Platform).To(Equal("linux/amd64"))
}

// ---------------------------------------------------------------------------
// YAML printer
// ---------------------------------------------------------------------------

func TestYAMLPrinter_SyncResult(t *testing.T) {
	g := NewWithT(t)
	var buf bytes.Buffer
	p := output.New(output.FormatYAML, false, &buf)
	result := output.SyncResult{
		Clusters: []output.ClusterSyncResult{
			{Name: "c1", Context: "ctx1", Status: output.ClusterSyncStatusFailed, Reason: "timeout"},
		},
		Synced:  0,
		Skipped: 0,
		Failed:  1,
	}
	g.Expect(p.Print(result)).To(Succeed())

	var got output.SyncResult
	g.Expect(yaml.Unmarshal(buf.Bytes(), &got)).To(Succeed())
	g.Expect(got.Failed).To(Equal(1))
	g.Expect(got.Clusters[0].Reason).To(Equal("timeout"))
}

// ---------------------------------------------------------------------------
// Plain printer
// ---------------------------------------------------------------------------

func TestPlainPrinter_SyncResult(t *testing.T) {
	g := NewWithT(t)
	var buf bytes.Buffer
	p := output.New(output.FormatText, false, &buf)
	result := output.SyncResult{
		Clusters: []output.ClusterSyncResult{
			{Name: "a", Context: "ctx-a", Status: output.ClusterSyncStatusSynced},
			{Name: "b", Context: "ctx-b", Status: output.ClusterSyncStatusSkipped},
			{Name: "c", Context: "ctx-c", Status: output.ClusterSyncStatusFailed, Reason: "error"},
		},
		Synced:  1,
		Skipped: 1,
		Failed:  1,
	}
	g.Expect(p.Print(result)).To(Succeed())

	out := buf.String()
	g.Expect(out).To(ContainSubstring("[+]"))
	g.Expect(out).To(ContainSubstring("[-]"))
	g.Expect(out).To(ContainSubstring("[!]"))
	g.Expect(out).To(ContainSubstring("synced=1"))
	g.Expect(out).To(ContainSubstring("skipped=1"))
	g.Expect(out).To(ContainSubstring("failed=1"))
}

func TestPlainPrinter_ClusterVersionResult(t *testing.T) {
	g := NewWithT(t)
	var buf bytes.Buffer
	p := output.New(output.FormatText, false, &buf)
	g.Expect(p.Print(output.ClusterVersionResult{Context: "my-ctx", Version: "1.29.0"})).To(Succeed())

	out := strings.TrimSpace(buf.String())
	g.Expect(out).To(Equal("1.29.0"))
}

// ---------------------------------------------------------------------------
// TTY / Non-TTY selection
// ---------------------------------------------------------------------------

func TestNew_NonTTY_Text_NoANSI(t *testing.T) {
	g := NewWithT(t)
	var buf bytes.Buffer
	p := output.New(output.FormatText, false, &buf)
	g.Expect(p.Print(output.SyncResult{})).To(Succeed())
	// Plain printer should produce no ANSI escape sequences
	g.Expect(buf.String()).ToNot(ContainSubstring("\x1b["))
}

// ---------------------------------------------------------------------------
// Spinner no-op on JSON
// ---------------------------------------------------------------------------

func TestStartSpinner_NoOp_JSON(t *testing.T) {
	g := NewWithT(t)
	var buf bytes.Buffer
	p := output.New(output.FormatJSON, false, &buf)
	stop := p.StartSpinner("loading...")
	stop()
	g.Expect(buf.String()).To(BeEmpty())
}
