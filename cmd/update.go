// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/minio/selfupdate"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/mod/semver"

	"github.com/cloudoperators/cloudctl/cmd/output"
)

const githubReleasesLatestURL = "https://api.github.com/repos/cloudoperators/cloudctl/releases/latest"

// updateHTTPClient is used for all update-related network calls.
// It clones http.DefaultTransport (when it is a *http.Transport) so proxy
// settings, TLS config, and HTTP/2 support are preserved.
// ResponseHeaderTimeout (30s) prevents hangs waiting for response headers.
// Timeout (10m) provides a hard upper bound on total request time, allowing
// large archive downloads to complete while still bounding worst-case duration.
var updateHTTPClient = func() *http.Client {
	var transport http.RoundTripper
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		t := dt.Clone()
		t.ResponseHeaderTimeout = 30 * time.Second
		transport = t
	} else {
		transport = http.DefaultTransport
	}
	return &http.Client{
		Transport: transport,
		Timeout:   10 * time.Minute,
	}
}()

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Check for and install the latest cloudctl release",
	Long: `Queries the GitHub Releases API for the latest cloudctl version,
downloads the archive for the current OS/architecture, verifies the SHA256
checksum, and atomically replaces the running binary.

Use --check to only report whether an update is available without installing.

Examples:
  # Check for updates without installing
  cloudctl update --check

  # Check and emit JSON for scripting
  cloudctl update --check -o json

  # Install the latest release
  cloudctl update`,
	RunE: runUpdate,
}

func init() {
	updateCmd.Flags().Bool("check", false, "Check for updates without installing")
	_ = viper.BindPFlags(updateCmd.Flags())
}

func runUpdate(cmd *cobra.Command, _ []string) error {
	checkOnly := viper.GetBool("check")

	format, err := output.ParseFormat(viper.GetString("output"))
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	printer := output.New(format, output.IsTTYWriter(w), w)

	if Version == "dev" {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning: running a dev build; version comparison may be inaccurate")
	}

	stop := printer.StartSpinner("Checking for updates...")
	rel, err := fetchLatestRelease(cmd.Context())
	stop()
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}

	// Normalise the local version to a "v" prefix for comparison.
	currentVersion := Version
	if !strings.HasPrefix(currentVersion, "v") {
		currentVersion = "v" + currentVersion
	}

	result := output.UpdateResult{
		CurrentVersion: currentVersion,
		LatestVersion:  rel.TagName,
	}

	// Use semver comparison so that current >= latest (e.g., pre-release or newer
	// self-built version) is reported as up-to-date rather than triggering a downgrade.
	// If either version is not a valid semver string, fall back to string equality.
	if semver.IsValid(currentVersion) && semver.IsValid(rel.TagName) {
		if semver.Compare(currentVersion, rel.TagName) >= 0 {
			result.Status = output.UpdateStatusUpToDate
			return printer.Print(result)
		}
	} else if currentVersion == rel.TagName {
		result.Status = output.UpdateStatusUpToDate
		return printer.Print(result)
	}

	if checkOnly {
		result.Status = output.UpdateStatusAvailable
		return printer.Print(result)
	}

	assetName := fmt.Sprintf("cloudctl_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	checksumName := assetName + ".sha256"

	if runtime.GOOS == "windows" {
		return fmt.Errorf("self-update on Windows is not yet supported (releases use .zip archives); please download manually from https://github.com/cloudoperators/cloudctl/releases")
	}

	archiveURL, checksumURL, err := findAssetURLs(rel, assetName, checksumName)
	if err != nil {
		return err
	}

	stop = printer.StartSpinner(fmt.Sprintf("Downloading %s...", rel.TagName))
	expectedChecksum, err := downloadChecksum(cmd.Context(), checksumURL)
	if err != nil {
		stop()
		return fmt.Errorf("downloading checksum: %w", err)
	}

	binary, err := downloadAndExtract(cmd.Context(), archiveURL, expectedChecksum)
	stop()
	if err != nil {
		return fmt.Errorf("downloading and extracting archive: %w", err)
	}

	if err := applyUpdate(binary); err != nil {
		return fmt.Errorf("applying update: %w", err)
	}

	result.Status = output.UpdateStatusUpdated
	return printer.Print(result)
}

func fetchLatestRelease(ctx context.Context) (*ghRelease, error) {
	return fetchLatestReleaseFrom(ctx, updateHTTPClient, githubReleasesLatestURL)
}

func fetchLatestReleaseFrom(ctx context.Context, client *http.Client, url string) (*ghRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "cloudctl/"+Version)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decoding GitHub API response: %w", err)
	}
	return &rel, nil
}

func findAssetURLs(rel *ghRelease, assetName, checksumName string) (archiveURL, checksumURL string, err error) {
	for _, a := range rel.Assets {
		switch a.Name {
		case assetName:
			archiveURL = a.BrowserDownloadURL
		case checksumName:
			checksumURL = a.BrowserDownloadURL
		}
	}
	if archiveURL == "" {
		return "", "", fmt.Errorf("no release asset found for %q", assetName)
	}
	if checksumURL == "" {
		return "", "", fmt.Errorf("no checksum asset found for %q", checksumName)
	}
	return archiveURL, checksumURL, nil
}

// downloadChecksum fetches a .sha256 file and returns the raw digest bytes.
// It handles both bare hex strings and BSD-style "<hex>  <filename>" lines.
func downloadChecksum(ctx context.Context, url string) ([]byte, error) {
	return downloadChecksumFrom(ctx, updateHTTPClient, url)
}

func downloadChecksumFrom(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading checksum: HTTP %d", resp.StatusCode)
	}

	const maxChecksumSize = 4 << 10 // 4 KiB — far more than any checksum line needs
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxChecksumSize {
		return nil, fmt.Errorf("checksum file exceeds maximum allowed size of 4 KiB")
	}

	line := strings.TrimSpace(string(raw))
	// BSD-style: "<hex>  <filename>" — take the first field.
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil, fmt.Errorf("checksum file is empty")
	}
	hexStr := fields[0]

	digest, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("parsing checksum hex %q: %w", hexStr, err)
	}
	if len(digest) != sha256.Size {
		return nil, fmt.Errorf("invalid checksum length: got %d bytes, want %d", len(digest), sha256.Size)
	}
	return digest, nil
}

// downloadAndExtract downloads the .tar.gz archive, verifies its SHA256 against
// expectedChecksum, then extracts and returns the cloudctl binary as an io.Reader.
func downloadAndExtract(ctx context.Context, archiveURL string, expectedChecksum []byte) (io.Reader, error) {
	return downloadAndExtractFrom(ctx, updateHTTPClient, archiveURL, expectedChecksum)
}

func downloadAndExtractFrom(ctx context.Context, client *http.Client, url string, expectedChecksum []byte) (io.Reader, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading archive: HTTP %d", resp.StatusCode)
	}

	const maxArchiveSize = 256 << 20 // 256 MiB
	archiveBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading archive: %w", err)
	}
	if int64(len(archiveBytes)) > maxArchiveSize {
		return nil, fmt.Errorf("archive exceeds maximum allowed size of 256 MiB")
	}

	// Verify SHA256 of the raw archive bytes.
	digest := sha256.Sum256(archiveBytes)
	if !bytes.Equal(digest[:], expectedChecksum) {
		return nil, fmt.Errorf("checksum mismatch: expected %x, got %x", expectedChecksum, digest)
	}

	// Decompress and extract the cloudctl binary from the tar.
	gz, err := gzip.NewReader(bytes.NewReader(archiveBytes))
	if err != nil {
		return nil, fmt.Errorf("decompressing archive: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading archive: %w", err)
		}
		if hdr.Name == "cloudctl" || strings.HasSuffix(hdr.Name, "/cloudctl") {
			if hdr.Typeflag != tar.TypeReg {
				return nil, fmt.Errorf("archive entry %q is not a regular file (type %d)", hdr.Name, hdr.Typeflag)
			}
			binaryBytes, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("reading binary from archive: %w", err)
			}
			return bytes.NewReader(binaryBytes), nil
		}
	}

	return nil, fmt.Errorf("binary \"cloudctl\" not found in archive")
}

func applyUpdate(r io.Reader) error {
	return selfupdate.Apply(r, selfupdate.Options{})
}
