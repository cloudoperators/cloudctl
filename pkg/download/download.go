// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package download

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dustin/go-humanize"
)

const (
	maxDownloadBytes = 200 << 20 // 200 MiB cap on compressed download
	maxExtractBytes  = 500 << 20 // 500 MiB cap on total extracted bytes
	maxFileBytes     = 200 << 20 // 200 MiB cap per extracted file
)

var (
	errDownloadTooLarge = errors.New("download exceeds allowed size")
	errExtractTooLarge  = errors.New("extracted data exceeds allowed size")
	errEntryTooLarge    = errors.New("archive entry exceeds allowed size")
)

// WriteCounter tracks the bytes written and prints an inline progress message.
type WriteCounter struct {
	Total uint64
}

func (wc *WriteCounter) Write(p []byte) (int, error) {
	n := len(p)
	wc.Total += uint64(n)
	wc.PrintProgress()
	return n, nil
}

// PrintProgress renders a single-line progress indicator on stderr.
func (wc WriteCounter) PrintProgress() {
	fmt.Fprintf(os.Stderr, "\r%s", strings.Repeat(" ", 50))
	fmt.Fprintf(os.Stderr, "\rDownloading... %s complete", humanize.Bytes(wc.Total))
}

// DownloadFile retrieves a single binary and writes it directly to `filepathDest`,
// verifying the payload against a SHA-256 checksum.
//
// Flow:
//  1. GET downloadURL with a size cap.
//  2. Stream data to a temp file while hashing and showing progress.
//  3. Verify SHA-256 using sha256Url (supports single-line or multi-entry formats).
//  4. Atomically move temp file to destination.
func DownloadFile(ctx context.Context, downloadURL, sha256URL, filepathDest string) error {
	tmpFilepath := filepathDest + ".tmp"
	out, err := os.Create(tmpFilepath)
	if err != nil {
		return err
	}

	resp, err := httpGetWithContext(ctx, downloadURL)
	if err != nil {
		return fmt.Errorf("while getting binary: %w", err)
	}
	defer resp.Body.Close()

	if err := ensureOKAndSize(resp, downloadURL); err != nil {
		return err
	}

	hasher := sha256.New()
	counter := &WriteCounter{}
	reader := withHashAndProgress(resp.Body, hasher, counter, maxDownloadBytes)

	_, err = io.Copy(out, reader)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr)
	// Close the file explicitly before rename; on Windows an open handle prevents the rename from succeeding.
	err = out.Close()
	if err != nil {
		return err
	}

	err = verifySha256(ctx, sha256URL, hasher)
	if err != nil {
		return err
	}
	return os.Rename(tmpFilepath, filepathDest)
}

// DownloadTarGzFile downloads a .tar.gz archive, extracts the first entry that
// matches filterFiles, verifies the archive payload, then writes the entry to `filepathDest`.
func DownloadTarGzFile(ctx context.Context, downloadURL, sha256URL, filepathDest string, filterFiles func(string) bool) error {
	resp, err := httpGetWithContext(ctx, downloadURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := ensureOKAndSize(resp, downloadURL); err != nil {
		return err
	}

	hasher := sha256.New()
	counter := &WriteCounter{}
	reader := withHashAndProgress(resp.Body, hasher, counter, maxDownloadBytes)

	gzipReader, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	tmpFilepath := filepathDest + ".tmp"

	fileFound := false

	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if header == nil || header.FileInfo().IsDir() || !filterFiles(header.Name) {
			continue
		}

		if header.Size <= 0 || header.Size > maxFileBytes {
			return errEntryTooLarge
		}
		if header.Size > maxExtractBytes {
			return errExtractTooLarge
		}

		out, err := os.Create(tmpFilepath)
		if err != nil {
			return err
		}

		_, err = io.CopyN(out, tarReader, header.Size)
		if err != nil {
			out.Close()
			return err
		}
		err = out.Close()
		if err != nil {
			return err
		}
		// Only one matched entry is extracted.
		fileFound = true
		break
	}

	if !fileFound {
		return fmt.Errorf("no matching file found in tar archive: %s", downloadURL)
	}

	fmt.Fprintln(os.Stderr)

	if err := verifySha256(ctx, sha256URL, hasher); err != nil {
		return err
	}
	return os.Rename(tmpFilepath, filepathDest)
}

// DownloadZipFile downloads a .zip archive to a temp file while hashing and
// showing progress, verifies the payload, then extracts the first entry that
// matches filterFiles into `filepath`.
func DownloadZipFile(ctx context.Context, downloadURL, sha256URL, filepathDest string, filterFiles func(string) bool) error {
	resp, err := httpGetWithContext(ctx, downloadURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := ensureOKAndSize(resp, downloadURL); err != nil {
		return err
	}

	zipPath := filepathDest + ".zip.tmp"
	zf, err := os.Create(zipPath)
	if err != nil {
		return err
	}

	hasher := sha256.New()
	counter := &WriteCounter{}
	reader := withHashAndProgress(resp.Body, hasher, counter, maxDownloadBytes)

	_, err = io.Copy(zf, reader)
	if err != nil {
		_ = zf.Close()
		return err
	}
	err = zf.Close()
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr)

	err = verifySha256(ctx, sha256URL, hasher)
	if err != nil {
		return err
	}

	zf, err = os.Open(zipPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = zf.Close()
		_ = os.Remove(zipPath)
	}()

	stat, err := zf.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(zf, stat.Size())
	if err != nil {
		return err
	}

	tmpFilepath := filepathDest + ".tmp"
	var extracted bool

	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !filterFiles(f.Name) {
			continue
		}

		uncomp := f.UncompressedSize64
		if uncomp > uint64(maxFileBytes) {
			return errEntryTooLarge
		}
		if uncomp > uint64(maxExtractBytes) {
			return errExtractTooLarge
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		out, err := os.Create(tmpFilepath)
		if err != nil {
			rc.Close()
			return err
		}

		lr := &io.LimitedReader{R: rc, N: int64(uncomp)}
		_, err = io.Copy(out, lr)
		if err != nil {
			return err
		}
		err = out.Close()
		if err != nil {
			return err
		}
		err = rc.Close()
		if err != nil {
			return err
		}

		if lr.N != 0 {
			return fmt.Errorf("zip entry truncated: %s", f.Name)
		}

		extracted = true
		break
	}

	if !extracted {
		return fmt.Errorf("no matching file found in zip: %s", downloadURL)
	}

	return os.Rename(tmpFilepath, filepathDest)
}

// verifySha256 reads checksum data from either a URL or a local file path and
// compares it with the hash of the already-streamed payload contained in hasher.
// The checksum input may be a single 64-hex token or a multi-line file with
// entries like "<hex>  filename".
func verifySha256(ctx context.Context, url string, hasher hash.Hash) error {
	r, closer, wantAsset, err := openChecksumSource(ctx, url)
	if err != nil {
		return err
	}
	defer closer()

	token, err := selectChecksumToken(r, wantAsset)
	if err != nil {
		return err
	}

	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(token, actual) {
		return fmt.Errorf("hash of downloaded file does not match: expected %s, got %s", token, actual)
	}
	return nil
}

// httpGetWithContext performs a GET request with context.
func httpGetWithContext(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

// ensureOKAndSize validates HTTP status and enforces a download size cap.
func ensureOKAndSize(resp *http.Response, url string) error {
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("while downloading %s unexpected http status code %d, expected %d", url, resp.StatusCode, http.StatusOK)
	}
	if resp.ContentLength > 0 && resp.ContentLength > maxDownloadBytes {
		return errDownloadTooLarge
	}
	return nil
}

// withHashAndProgress wraps r with a size limit, simultaneously hashes and prints progress.
func withHashAndProgress(r io.Reader, hasher hash.Hash, counter *WriteCounter, capBytes int64) io.Reader {
	limited := io.LimitReader(r, capBytes)
	teeHash := io.TeeReader(limited, hasher)
	return io.TeeReader(teeHash, counter)
}

// openChecksumSource returns an io.Reader for the checksum file along with a closer,
// and the asset name to match when a multi-entry checksum file is used.
// If the URL looks like "<asset>.sha256", the asset name is derived from the basename.
func openChecksumSource(ctx context.Context, url string) (r io.Reader, closer func(), wantAsset string, err error) {
	// Local file path support (no scheme or obvious path)
	if !strings.Contains(url, "://") && (filepath.IsAbs(url) || strings.HasPrefix(url, ".") || strings.HasPrefix(url, "/")) {
		f, e := os.Open(url)
		if e != nil {
			return nil, func() {}, "", e
		}
		return f, func() { _ = f.Close() }, "", nil
	}

	resp, e := httpGetWithContext(ctx, url)
	if e != nil {
		return nil, func() {}, "", e
	}
	// Ensure the response body is always closed on every return path.
	// We wrap it into our closer so callers still get a reader and closer to use.
	closer = func() { _ = resp.Body.Close() }

	if resp.StatusCode != http.StatusOK {
		closer()
		return nil, func() {}, "", fmt.Errorf("while downloading %s unexpected http status code %d, expected %d", url, resp.StatusCode, http.StatusOK)
	}

	base := filepath.Base(url)
	if w, ok := strings.CutSuffix(base, ".sha256"); ok {
		wantAsset = w
	}
	return resp.Body, func() { _ = resp.Body.Close() }, wantAsset, nil
}

// selectChecksumToken parses checksum input and returns the expected 64-hex token.
// Supports:
//   - single-line "<hex>"
//   - multi-line "<hex>  filename"
func selectChecksumToken(r io.Reader, wantAsset string) (string, error) {
	all, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("while getting hash: %w", err)
	}
	content := strings.TrimSpace(string(all))

	// Single-line fast path.
	if !strings.Contains(content, "\n") {
		tok := firstHex64(content)
		if tok == "" {
			return "", errors.New("invalid sha256 content")
		}
		return tok, nil
	}

	sc := bufio.NewScanner(strings.NewReader(content))
	var first string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		tok := firstHex64(line)
		if tok == "" {
			continue
		}
		if first == "" {
			first = tok
		}
		if wantAsset != "" {
			// Try to match by filename suffix.
			if strings.HasSuffix(line, "  "+wantAsset) || strings.HasSuffix(line, "\t"+wantAsset) || strings.HasSuffix(line, " "+wantAsset) {
				return tok, nil
			}
			// Fallback: any field equals wantAsset
			if slices.Contains(strings.Fields(line), wantAsset) {
				return tok, nil
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if first == "" {
		return "", errors.New("no suitable checksum entry found")
	}
	return first, nil
}

// firstHex64 finds the first 64-hex token in s.
func firstHex64(s string) string {
	for tok := range strings.FieldsSeq(s) {
		if len(tok) == 64 {
			isHex := true
			for _, c := range tok {
				if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
					isHex = false
					break
				}
			}
			if isHex {
				return tok
			}
		}
	}
	return ""
}
