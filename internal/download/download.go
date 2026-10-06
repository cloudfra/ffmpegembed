// Copyright 2026 Cloudfra
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package download fetches a pinned ffmpeg/ffprobe build for platforms that
// have neither an embedded binary nor one installed.
//
// What is downloaded is fixed by manifest.json: one release version, and for
// each <goos>_<goarch> the gzip-compressed ffmpeg, ffprobe and license assets
// of that release with their SHA-256 digests. A download is only used when its
// digest matches, and is then cached so it happens once per version. The
// license text is always stored next to the binaries. To move to
// another release, run scripts/update-ffmpeg.sh, which rewrites the manifest.
package download

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed" // for manifest.json
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

var (
	// ErrUnsupported is returned (wrapped) when the manifest has no build for
	// the requested platform or binary.
	ErrUnsupported = errors.New("no downloadable build for this platform")

	// ErrChecksum is returned (wrapped) when a downloaded file does not match
	// the SHA-256 digest pinned in the manifest. The file is discarded.
	ErrChecksum = errors.New("checksum mismatch")
)

// LicenseFile is the name the license text of the ffmpeg/ffprobe build is
// stored under, in the same directory as the binaries.
const LicenseFile = "ffmpeg-LICENSE.txt"

// licenseAsset is the key of the license text in a platform's assets.
const licenseAsset = "license"

// maxSize bounds both a downloaded asset and a decompressed binary, so a
// misbehaving server or a corrupt archive cannot fill the disk.
const maxSize = 1 << 30

//go:embed manifest.json
var manifestJSON []byte

// Asset is one downloadable file of a release.
type Asset struct {
	// File is the asset's file name, appended to Manifest.BaseURL. It is the
	// gzip-compressed binary itself, not an archive.
	File string `json:"file"`
	// SHA256 is the lowercase hex SHA-256 digest of File.
	SHA256 string `json:"sha256"`
	// BinarySHA256 is the lowercase hex SHA-256 digest of File once
	// decompressed. It ties the embedded copy of a binary to this release.
	BinarySHA256 string `json:"binary_sha256,omitempty"`
}

// Manifest pins the release that is downloaded.
type Manifest struct {
	// Version is the release tag; it also names the cache directory.
	Version string `json:"version"`
	// License is the SPDX expression the builds are distributed under.
	License string `json:"license"`
	// BaseURL is the URL prefix every Asset.File is fetched from.
	BaseURL string `json:"base_url"`
	// Platforms maps "<goos>_<goarch>" to its assets by name: the binaries
	// "ffmpeg" and "ffprobe", and "license" for their license text.
	Platforms map[string]map[string]Asset `json:"platforms"`
}

// Default returns the manifest compiled into this package.
func Default() (*Manifest, error) {
	m := &Manifest{}
	if err := json.Unmarshal(manifestJSON, m); err != nil {
		return nil, fmt.Errorf("parse embedded ffmpeg download manifest: %w", err)
	}
	return m, nil
}

// Asset returns the asset for the named binary on goos/goarch, if any.
func (m *Manifest) Asset(goos, goarch, name string) (Asset, bool) {
	a, ok := m.Platforms[goos+"_"+goarch][name]
	return a, ok
}

// Fetcher downloads binaries described by a Manifest into a cache directory.
type Fetcher struct {
	Manifest *Manifest
	// CacheDir is the root under which binaries are kept, as
	// <CacheDir>/<version>/<goos>_<goarch>/<name>.
	CacheDir string
	// Client is the HTTP client to use; nil means http.DefaultClient.
	Client *http.Client
}

// Fetch returns the path of the named binary ("ffmpeg" or "ffprobe") for
// goos/goarch, downloading and verifying it first unless it is already cached.
// The build's license text is fetched the same way and kept beside it as
// LicenseFile.
func (f *Fetcher) Fetch(ctx context.Context, goos, goarch, name string) (string, error) {
	asset, ok := f.Manifest.Asset(goos, goarch, name)
	if !ok || name == licenseAsset {
		return "", fmt.Errorf("%s for %s/%s (%s): %w", name, goos, goarch, f.Manifest.Version, ErrUnsupported)
	}

	dir := filepath.Join(f.CacheDir, f.Manifest.Version, goos+"_"+goarch)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	if license, ok := f.Manifest.Asset(goos, goarch, licenseAsset); ok {
		if err := f.ensure(ctx, license, filepath.Join(dir, LicenseFile), 0o600); err != nil {
			return "", err
		}
	}
	path := filepath.Join(dir, name)
	if goos == "windows" {
		path += ".exe"
	}
	if err := f.ensure(ctx, asset, path, 0o700); err != nil {
		return "", err
	}
	return path, nil
}

// ensure makes path hold the decompressed asset, downloading and verifying it
// unless the file is already there.
func (f *Fetcher) ensure(ctx context.Context, asset Asset, path string, perm os.FileMode) error {
	if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
		return nil
	}

	// Download and unpack next to the destination, then rename into place, so
	// a concurrent or interrupted Fetch never leaves a partial file at path.
	archive, err := f.download(ctx, filepath.Dir(path), asset)
	if err != nil {
		return err
	}
	defer os.Remove(archive) //nolint:errcheck // best-effort cleanup of a temp file

	src, err := os.Open(archive) //nolint:gosec // G304: a temp file this function just created
	if err != nil {
		return err
	}
	defer src.Close() //nolint:errcheck // read-only file

	tmp := archive + ".out"
	if err := ExtractGzip(tmp, src, perm); err != nil {
		return fmt.Errorf("unpack %s: %w", asset.File, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return nil
}

// download saves asset into a new temp file in dir and returns its path once
// its SHA-256 digest has been checked against the manifest.
func (f *Fetcher) download(ctx context.Context, dir string, asset Asset) (path string, err error) {
	url := f.Manifest.BaseURL + "/" + asset.File
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req) //nolint:gosec // G107: URL comes from the compiled-in manifest
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // nothing to do about a failed close of a response body
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: unexpected HTTP status %s", url, resp.Status)
	}

	out, err := os.CreateTemp(dir, "download-*")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.Remove(out.Name()))
		}
	}()

	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, sum), io.LimitReader(resp.Body, maxSize+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	if n > maxSize {
		return "", fmt.Errorf("download %s: larger than %d bytes", url, maxSize)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != asset.SHA256 {
		return "", fmt.Errorf("download %s: %w: got sha256 %s, want %s", url, ErrChecksum, got, asset.SHA256)
	}
	return out.Name(), nil
}

// ExtractGzip decompresses the gzip stream src into a file at path created
// with perm, replacing any existing file. On failure the partial file is
// removed.
func ExtractGzip(path string, src io.Reader, perm os.FileMode) (err error) {
	zr, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm) //nolint:gosec // G302,G304: a caller-chosen path and mode (owner-only)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			err = errors.Join(err, os.Remove(path))
		}
	}()

	n, err := io.Copy(out, io.LimitReader(zr, maxSize+1))
	if err != nil {
		return err
	}
	if n > maxSize {
		return fmt.Errorf("decompressed size exceeds %d bytes", maxSize)
	}
	return zr.Close()
}
