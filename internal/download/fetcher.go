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

package download

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cloudfra/ffmpegembed/internal/manifest"
)

// ErrUnsupported is returned (wrapped) when the manifest has no build for the
// requested platform or binary.
var ErrUnsupported = errors.New("no downloadable build for this platform")

// LicenseFile is the name the license text of an ffmpeg/ffprobe build is
// stored under, in the same directory as the binaries.
const LicenseFile = "ffmpeg-LICENSE.txt"

// Fetcher downloads the binaries a Manifest pins into a cache directory.
type Fetcher struct {
	Manifest *manifest.Manifest
	// CacheDir is the root under which files are kept, as
	// <CacheDir>/<version>/<goos>_<goarch>/<name>.
	CacheDir string
	// Client is the HTTP client to use; nil means http.DefaultClient.
	Client *http.Client
}

// Fetch returns the path of the named binary (manifest.Ffmpeg or
// manifest.Ffprobe) for goos/goarch, downloading it first unless it is
// already cached. A download is kept only if both the file and its
// decompressed content have the digests the manifest pins. The build's
// license text is fetched the same way and kept beside the binary as
// LicenseFile.
func (f *Fetcher) Fetch(ctx context.Context, goos, goarch, name string) (string, error) {
	platform, ok := f.Manifest.Platforms[manifest.Key(goos, goarch)]
	if !ok || (name != manifest.Ffmpeg && name != manifest.Ffprobe) {
		return "", fmt.Errorf("%s for %s/%s (%s): %w", name, goos, goarch, f.Manifest.Version, ErrUnsupported)
	}
	asset, _ := platform.Asset(name)

	dir := filepath.Join(f.CacheDir, f.Manifest.Version, manifest.Key(goos, goarch))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	if err := f.ensure(ctx, platform.License, filepath.Join(dir, LicenseFile), 0o600); err != nil {
		return "", err
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
func (f *Fetcher) ensure(ctx context.Context, asset manifest.Asset, path string, perm os.FileMode) (err error) {
	if fi, serr := os.Stat(path); serr == nil && fi.Mode().IsRegular() {
		return nil
	}

	// Download and unpack next to the destination under temporary names, then
	// rename into place, so a concurrent or interrupted Fetch never leaves a
	// partial file at path.
	tmp, err := os.CreateTemp(filepath.Dir(path), "download-*")
	if err != nil {
		return err
	}
	gz, out := tmp.Name(), tmp.Name()+".out"
	defer func() {
		// Whatever is still there under a temporary name is not wanted.
		for _, p := range []string{gz, out} {
			if rerr := os.Remove(p); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
				err = errors.Join(err, rerr)
			}
		}
	}()
	if err := tmp.Close(); err != nil {
		return err
	}

	if _, err := File(ctx, f.Client, f.Manifest.URL(asset), gz, asset.SHA256); err != nil {
		return err
	}
	digest, err := Gunzip(out, gz, perm)
	if err != nil {
		return err
	}
	if digest != asset.BinarySHA256 {
		return fmt.Errorf("%s: %w: decompressed sha256 is %s, manifest pins %s", asset.File, ErrChecksum, digest, asset.BinarySHA256)
	}
	return os.Rename(out, path)
}
