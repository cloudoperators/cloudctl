// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/adrg/xdg"

	"github.com/cloudoperators/cloudctl/pkg/config"
)

// GetReleaseAssetDigest fetches a GitHub release by tag and returns the sha256 hex digest for the
// asset with the given name. It accepts formats like "sha256:<hex>" or plain 64-hex.
func GetReleaseAssetDigest(ctx context.Context, owner, repo, tag, assetName string) (string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, http.NoBody)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// Set a user agent to avoid 403s from GitHub API
	req.Header.Set("User-Agent", config.CloudCtlUserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github api %s returned status %d", apiURL, resp.StatusCode)
	}
	var payload struct {
		Assets []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	for _, a := range payload.Assets {
		if a.Name == assetName {
			if a.Digest == "" {
				return "", errors.New("asset has empty digest")
			}
			lower := strings.ToLower(a.Digest)
			if rest, ok := strings.CutPrefix(lower, "sha256:"); ok {
				return rest, nil
			}
			return a.Digest, nil
		}
	}
	return "", fmt.Errorf("asset %s not found in release %s", assetName, tag)
}

// NOTE: We have currently two binaries installed from GitHub public: cloudctl and kubelogin. Exposing digests for release assets is a new feature from GitHub:
// https://github.blog/changelog/2025-06-03-releases-now-expose-digests-for-release-assets/
// When we switch to latest release of kubelogin (which has hash information in release digest) we can remove this function and use the digest directly.

// SelectChecksumSource tries to obtain the sha256 checksum for a GitHub release asset via the GitHub Releases API digest field. If successful, it writes the 64-hex digest into a temporary file and returns its path along with a cleanup function.
// If it cannot obtain the digest, it falls back to the provided sha256URL (typically the asset URL with ".sha256" suffix) and the cleanup function is a no-op. The returned cleanup func must be called by the caller.
func SelectChecksumSource(ctx context.Context, owner, repo, tag, assetName, sha256URL string) (checksumSource string, cleanup func(), err error) {
	cleanup = func() {}

	digest, derr := GetReleaseAssetDigest(ctx, owner, repo, tag, assetName)
	if derr == nil && digest != "" {
		f, e := os.CreateTemp("", fmt.Sprintf("%s-%s-sha-*.sha256", repo, assetName))
		if e == nil {
			if _, werr := f.WriteString(digest); werr == nil {
				_ = f.Sync() //nolint:errcheck
				checksumSource = f.Name()
				cleanup = func() {
					name := f.Name()
					_ = f.Close()
					_ = os.Remove(name)
				}
				return checksumSource, cleanup, nil
			}
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}
	// fallback to provided URL
	return sha256URL, cleanup, nil
}

// DownloadArchive downloads an archive (zip or tar.gz) from archiveURL, verifies it using the
// provided checksumSource (which can be a URL or local file path as produced by SelectChecksumSource),
// extracts a single file matched by filterFiles, and writes it to destPath. The ext parameter must be
// either "zip" or "tar.gz".
func DownloadArchive(ctx context.Context, archiveURL, checksumSource, destPath, ext string, filterFiles func(string) bool) error {
	switch ext {
	case "zip":
		return DownloadZipFile(ctx, archiveURL, checksumSource, destPath, filterFiles)
	case "tar.gz":
		return DownloadTarGzFile(ctx, archiveURL, checksumSource, destPath, filterFiles)
	default:
		return fmt.Errorf("unsupported archive extension: %s", ext)
	}
}

// DownloadGitHubReleaseBinary centralizes downloading a single binary from a GitHub release asset.
// It constructs the download URL as:
//
//	https://github.com/{owner}/{repo}/releases/download/v{version}/{assetName}
//
// It verifies checksum using SelectChecksumSource (preferring GitHub API digest), extracts the
// single binary from the archive, writes it to the cache under the provided cacheName, and chmods it 0700.
// The caller provides assetName (which typically includes os/arch and the archive extension),
// the archive extension ("zip" or "tar.gz"), the cache file name, the base binary name inside the
// archive (without .exe), and whether to accept common variants inside archives.
func DownloadGitHubReleaseBinary(ctx context.Context, owner, repo, version, assetName, archiveExt, cacheName, binaryBase string, acceptVariants bool) (string, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported architecture: %s", goarch)
	}

	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/v%s/%s", owner, repo, version, assetName)

	destPath := filepath.Join(xdg.CacheHome, cacheName)
	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return "", err
	}
	if _, err := os.Stat(destPath); err == nil {
		slog.Debug("File already exists. Skipping download", "name", repo, "path", destPath)
		return destPath, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	slog.Debug("Downloading file", "name", repo, "version", "v"+version, "url", url)

	// Build filter for the binary inside the archive
	binName := binaryBase
	if goos == "windows" {
		binName += ".exe"
	}
	filter := func(name string) bool {
		base := filepath.Base(name)
		if base == binName {
			return true
		}
		if !acceptVariants {
			return false
		}
		// Accept common naming variants inside archives, e.g. <base>_<os>_<arch> or <base>-<os>-<arch>
		variants := []string{
			fmt.Sprintf("%s_%s_%s", binaryBase, goos, goarch),
			fmt.Sprintf("%s-%s-%s", binaryBase, goos, goarch),
		}
		for _, v := range variants {
			if goos == "windows" {
				if base == v+".exe" {
					return true
				}
			} else {
				if base == v {
					return true
				}
			}
		}
		return false
	}

	checksumSource, cleanup, err := SelectChecksumSource(ctx, owner, repo, "v"+version, assetName, url+".sha256")
	if err != nil {
		return "", err
	}
	defer cleanup()

	staged, err := os.CreateTemp(filepath.Dir(destPath), filepath.Base(destPath)+"-*")
	if err != nil {
		return "", err
	}
	stagedPath := staged.Name()
	defer func() {
		_ = os.Remove(stagedPath)
	}()
	if err := staged.Close(); err != nil {
		return "", err
	}
	if err := DownloadArchive(ctx, url, checksumSource, stagedPath, archiveExt, filter); err != nil {
		return "", err
	}
	if err := os.Chmod(stagedPath, 0o700); err != nil {
		return "", err
	}
	if err := os.Rename(stagedPath, destPath); err != nil {
		return "", err
	}
	return destPath, nil
}
