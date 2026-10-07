// Copyright 2026 Cloudfra
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package download fetches files over HTTP and unpacks them, verifying
// SHA-256 digests along the way.
package download

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
)

// ErrChecksum is returned (wrapped) when a file does not have the SHA-256
// digest it was required to have. The file is removed.
var ErrChecksum = errors.New("checksum mismatch")

// maxSize bounds both a downloaded file and a decompressed one, so a
// misbehaving server or a corrupt file cannot fill the disk.
const maxSize = 1 << 30

// File downloads url into a new file at path and returns the lowercase hex
// SHA-256 digest of what was written. When wantSHA256 is not empty, the
// digest must equal it or the download fails with ErrChecksum. On any failure
// the partial file is removed. A nil client means http.DefaultClient.
func File(ctx context.Context, client *http.Client, url, path, wantSHA256 string) (digest string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req) //nolint:gosec // G107: the caller chooses the URL
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // nothing to do about a failed close of a response body
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: unexpected HTTP status %s", url, resp.Status)
	}

	digest, err = writeHashed(path, resp.Body, 0o600)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	if wantSHA256 != "" && digest != wantSHA256 {
		err = fmt.Errorf("download %s: %w: got sha256 %s, want %s", url, ErrChecksum, digest, wantSHA256)
		return "", errors.Join(err, os.Remove(path))
	}
	return digest, nil
}

// Gunzip decompresses the gzip file at src into a new file at dst created
// with perm, and returns the lowercase hex SHA-256 digest of the decompressed
// content. On failure the partial file is removed.
func Gunzip(dst, src string, perm os.FileMode) (digest string, err error) {
	in, err := os.Open(src) //nolint:gosec // G304: a path the caller chose
	if err != nil {
		return "", err
	}
	defer in.Close() //nolint:errcheck // read-only file
	zr, err := gzip.NewReader(in)
	if err != nil {
		return "", fmt.Errorf("gunzip %s: %w", src, err)
	}
	digest, err = writeHashed(dst, zr, perm)
	if err != nil {
		return "", fmt.Errorf("gunzip %s: %w", src, err)
	}
	return digest, nil
}

// writeHashed copies src into a new file at path created with perm and
// returns the hex SHA-256 digest of the bytes written, removing the file if
// anything fails or src is larger than maxSize.
func writeHashed(path string, src io.Reader, perm os.FileMode) (digest string, err error) {
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm) //nolint:gosec // G302,G304: a caller-chosen path and mode
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			err = errors.Join(err, os.Remove(path))
		}
	}()
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, sum), io.LimitReader(src, maxSize+1))
	if err != nil {
		return "", err
	}
	if n > maxSize {
		return "", fmt.Errorf("larger than %d bytes", maxSize)
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}
