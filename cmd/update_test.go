// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/cloudoperators/cloudctl/cmd/output"
)

// buildTestArchive creates a minimal valid .tar.gz containing a "cloudctl" binary entry.
// Returns the archive bytes and the SHA256 digest of those bytes.
func buildTestArchive(t *testing.T, binaryContent []byte) (archiveBytes, checksum []byte) {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	hdr := &tar.Header{
		Name: "cloudctl",
		Mode: 0o755,
		Size: int64(len(binaryContent)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar.WriteHeader: %v", err)
	}
	if _, err := tw.Write(binaryContent); err != nil {
		t.Fatalf("tar.Write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip.Close: %v", err)
	}

	archiveBytes = buf.Bytes()
	digest := sha256.Sum256(archiveBytes)
	checksum = digest[:]
	return archiveBytes, checksum
}

// --- fetchLatestRelease tests ---

func TestFetchLatestRelease_OK(t *testing.T) {
	g := NewWithT(t)

	rel := ghRelease{
		TagName: "v1.2.3",
		Assets: []ghAsset{
			{Name: "cloudctl_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/archive.tar.gz"},
		},
	}
	body, _ := json.Marshal(rel)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	got, err := fetchLatestReleaseFrom(t.Context(), srv.Client(), srv.URL)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(got.TagName).To(Equal("v1.2.3"))
	g.Expect(got.Assets).To(HaveLen(1))
	g.Expect(got.Assets[0].Name).To(Equal("cloudctl_linux_amd64.tar.gz"))
}

func TestFetchLatestRelease_HTTPError(t *testing.T) {
	g := NewWithT(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := fetchLatestReleaseFrom(t.Context(), srv.Client(), srv.URL)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("404"))
}

// --- findAssetURLs tests ---

func TestFindAssetURLs_Found(t *testing.T) {
	g := NewWithT(t)

	rel := &ghRelease{
		TagName: "v1.0.0",
		Assets: []ghAsset{
			{Name: "cloudctl_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/archive.tar.gz"},
			{Name: "cloudctl_linux_amd64.tar.gz.sha256", BrowserDownloadURL: "https://example.com/archive.tar.gz.sha256"},
		},
	}

	archURL, csURL, err := findAssetURLs(rel, "cloudctl_linux_amd64.tar.gz", "cloudctl_linux_amd64.tar.gz.sha256")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(archURL).To(Equal("https://example.com/archive.tar.gz"))
	g.Expect(csURL).To(Equal("https://example.com/archive.tar.gz.sha256"))
}

func TestFindAssetURLs_MissingArchive(t *testing.T) {
	g := NewWithT(t)

	rel := &ghRelease{
		Assets: []ghAsset{
			{Name: "cloudctl_linux_amd64.tar.gz.sha256", BrowserDownloadURL: "https://example.com/checksum"},
		},
	}

	_, _, err := findAssetURLs(rel, "cloudctl_linux_amd64.tar.gz", "cloudctl_linux_amd64.tar.gz.sha256")
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("cloudctl_linux_amd64.tar.gz"))
}

func TestFindAssetURLs_MissingChecksum(t *testing.T) {
	g := NewWithT(t)

	rel := &ghRelease{
		Assets: []ghAsset{
			{Name: "cloudctl_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/archive.tar.gz"},
		},
	}

	_, _, err := findAssetURLs(rel, "cloudctl_linux_amd64.tar.gz", "cloudctl_linux_amd64.tar.gz.sha256")
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("cloudctl_linux_amd64.tar.gz.sha256"))
}

// --- downloadChecksum tests ---

func TestDownloadChecksum_OK(t *testing.T) {
	g := NewWithT(t)

	expected := make([]byte, 32)
	for i := range expected {
		expected[i] = byte(i)
	}
	hexStr := hex.EncodeToString(expected)
	// BSD-style line: "<hex>  <filename>"
	body := fmt.Sprintf("%s  cloudctl_linux_amd64.tar.gz\n", hexStr)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	got, err := downloadChecksumFrom(t.Context(), srv.Client(), srv.URL)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(got).To(Equal(expected))
}

func TestDownloadChecksum_BareHex(t *testing.T) {
	g := NewWithT(t)

	expected := make([]byte, 32)
	for i := range expected {
		expected[i] = byte(i * 2 % 256)
	}
	hexStr := hex.EncodeToString(expected)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(hexStr + "\n"))
	}))
	defer srv.Close()

	got, err := downloadChecksumFrom(t.Context(), srv.Client(), srv.URL)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(got).To(Equal(expected))
}

// --- downloadAndExtract tests ---

func TestDownloadAndExtract_OK(t *testing.T) {
	g := NewWithT(t)

	binaryContent := []byte("fake cloudctl binary content")
	archiveBytes, checksum := buildTestArchive(t, binaryContent)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archiveBytes)
	}))
	defer srv.Close()

	r, err := downloadAndExtractFrom(t.Context(), srv.Client(), srv.URL, checksum)
	g.Expect(err).NotTo(HaveOccurred())

	var got bytes.Buffer
	_, _ = got.ReadFrom(r)
	g.Expect(got.Bytes()).To(Equal(binaryContent))
}

func TestDownloadAndExtract_ChecksumMismatch(t *testing.T) {
	g := NewWithT(t)

	archiveBytes, _ := buildTestArchive(t, []byte("content"))
	wrongChecksum := make([]byte, 32) // all zeros

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archiveBytes)
	}))
	defer srv.Close()

	_, err := downloadAndExtractFrom(t.Context(), srv.Client(), srv.URL, wrongChecksum)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("checksum mismatch"))
}

func TestDownloadAndExtract_BinaryNotFound(t *testing.T) {
	g := NewWithT(t)

	// Build an archive without a "cloudctl" entry.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "other-binary", Mode: 0o755, Size: 4}
	_ = tw.WriteHeader(hdr)
	_, _ = tw.Write([]byte("data"))
	_ = tw.Close()
	_ = gz.Close()
	archiveBytes := buf.Bytes()
	digest := sha256.Sum256(archiveBytes)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archiveBytes)
	}))
	defer srv.Close()

	_, err := downloadAndExtractFrom(t.Context(), srv.Client(), srv.URL, digest[:])
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("cloudctl"))
}

// --- printer output tests ---

func TestUpdateResult_PlainPrinter_UpToDate(t *testing.T) {
	g := NewWithT(t)

	var buf strings.Builder
	p := output.New(output.FormatText, false, &buf)
	err := p.Print(output.UpdateResult{
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.0.0",
		Status:         output.UpdateStatusUpToDate,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(buf.String()).To(ContainSubstring("up to date"))
}

func TestUpdateResult_PlainPrinter_Available(t *testing.T) {
	g := NewWithT(t)

	var buf strings.Builder
	p := output.New(output.FormatText, false, &buf)
	err := p.Print(output.UpdateResult{
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.1.0",
		Status:         output.UpdateStatusAvailable,
	})
	g.Expect(err).NotTo(HaveOccurred())
	out := buf.String()
	g.Expect(out).To(ContainSubstring("v1.0.0"))
	g.Expect(out).To(ContainSubstring("v1.1.0"))
}

func TestUpdateResult_PlainPrinter_Updated(t *testing.T) {
	g := NewWithT(t)

	var buf strings.Builder
	p := output.New(output.FormatText, false, &buf)
	err := p.Print(output.UpdateResult{
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.1.0",
		Status:         output.UpdateStatusUpdated,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(buf.String()).To(ContainSubstring("->"))
}

func TestUpdateResult_JSONPrinter(t *testing.T) {
	g := NewWithT(t)

	var buf strings.Builder
	p := output.New(output.FormatJSON, false, &buf)
	err := p.Print(output.UpdateResult{
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.1.0",
		Status:         output.UpdateStatusAvailable,
	})
	g.Expect(err).NotTo(HaveOccurred())

	var got output.UpdateResult
	g.Expect(json.Unmarshal([]byte(buf.String()), &got)).To(Succeed())
	g.Expect(got.CurrentVersion).To(Equal("v1.0.0"))
	g.Expect(got.LatestVersion).To(Equal("v1.1.0"))
	g.Expect(got.Status).To(Equal(output.UpdateStatusAvailable))
}
