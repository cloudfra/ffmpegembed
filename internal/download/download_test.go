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
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// gz returns data gzip-compressed, along with the hex SHA-256 of the result.
func gz(t *testing.T, data []byte) (compressed []byte, digest string) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// licenseText is what the test server serves as the build's license.
const licenseText = "the license"

// newFetcher serves body as the ffmpeg asset for linux/amd64, along with a
// license, and returns a Fetcher whose manifest pins the binary to digest,
// plus a counter of requests served.
func newFetcher(t *testing.T, body []byte, digest string) (*Fetcher, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	license, licenseDigest := gz(t, []byte(licenseText))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/ffmpeg-linux-x64.gz":
		case "/LICENSE.gz":
			if _, err := w.Write(license); err != nil {
				t.Errorf("serve %s: %v", r.URL.Path, err)
			}
			return
		default:
			http.NotFound(w, r)
			return
		}
		if _, err := w.Write(body); err != nil {
			t.Errorf("serve %s: %v", r.URL.Path, err)
		}
	}))
	t.Cleanup(srv.Close)
	return &Fetcher{
		CacheDir: t.TempDir(),
		Manifest: &Manifest{
			Version: "v1",
			BaseURL: srv.URL,
			Platforms: map[string]map[string]Asset{
				"linux_amd64": {
					"ffmpeg":  {File: "ffmpeg-linux-x64.gz", SHA256: digest},
					"ffprobe": {File: "missing.gz", SHA256: digest},
					"license": {File: "LICENSE.gz", SHA256: licenseDigest},
				},
			},
		},
	}, &hits
}

func TestFetchDownloadsVerifiesAndCaches(t *testing.T) {
	want := []byte("pretend this is ffmpeg")
	body, digest := gz(t, want)
	f, hits := newFetcher(t, body, digest)

	path, err := f.Fetch(context.Background(), "linux", "amd64", "ffmpeg")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if wantPath := filepath.Join(f.CacheDir, "v1", "linux_amd64", "ffmpeg"); path != wantPath {
		t.Errorf("Fetch path = %q; want %q", path, wantPath)
	}
	got, err := os.ReadFile(path) //nolint:gosec // G304: path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("fetched binary = %q; want %q", got, want)
	}

	// The license text is stored next to the binary.
	gotLicense, err := os.ReadFile(filepath.Join(filepath.Dir(path), LicenseFile)) //nolint:gosec // G304: path under t.TempDir()
	if err != nil {
		t.Fatalf("license was not stored beside the binary: %v", err)
	}
	if string(gotLicense) != licenseText {
		t.Errorf("license = %q; want %q", gotLicense, licenseText)
	}

	// A second Fetch is served from the cache without touching the network.
	if _, err := f.Fetch(context.Background(), "linux", "amd64", "ffmpeg"); err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("server saw %d requests; want 2 (binary and license, once each)", n)
	}

	// Only the binary and the license are left: no temp download or partial file.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("cache dir has %d entries; want the binary and the license", len(entries))
	}
}

func TestFetchRejectsChecksumMismatch(t *testing.T) {
	body, _ := gz(t, []byte("tampered"))
	_, pinned := gz(t, []byte("original"))
	f, _ := newFetcher(t, body, pinned)

	path, err := f.Fetch(context.Background(), "linux", "amd64", "ffmpeg")
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("Fetch = %q, %v; want ErrChecksum", path, err)
	}
	entries, err := os.ReadDir(filepath.Join(f.CacheDir, "v1", "linux_amd64"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != LicenseFile {
			t.Errorf("a rejected download left %s in the cache", e.Name())
		}
	}
}

func TestFetchUnsupportedPlatform(t *testing.T) {
	f, hits := newFetcher(t, nil, "")
	if _, err := f.Fetch(context.Background(), "plan9", "amd64", "ffmpeg"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Fetch(plan9) error = %v; want ErrUnsupported", err)
	}
	// The license is fetched with a binary, never as one.
	if _, err := f.Fetch(context.Background(), "linux", "amd64", "license"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Fetch(license) error = %v; want ErrUnsupported", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("server saw %d requests for an unsupported platform; want 0", n)
	}
}

func TestFetchHTTPError(t *testing.T) {
	f, _ := newFetcher(t, nil, "")
	if _, err := f.Fetch(context.Background(), "linux", "amd64", "ffprobe"); err == nil {
		t.Error("Fetch of a 404 asset succeeded; want an error")
	}
}

// TestDefaultManifest checks the compiled-in manifest is well formed: every
// platform pins both binaries and the license to a plain file name and to
// SHA-256 digests of the download and of its decompressed content.
func TestDefaultManifest(t *testing.T) {
	m, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if m.Version == "" || m.License == "" || m.BaseURL == "" || len(m.Platforms) == 0 {
		t.Fatalf("manifest is missing required fields: %+v", m)
	}
	for platform, assets := range m.Platforms {
		for _, name := range []string{"ffmpeg", "ffprobe", "license"} {
			a, ok := assets[name]
			if !ok {
				t.Errorf("%s: no %s asset", platform, name)
				continue
			}
			if sum, err := hex.DecodeString(a.SHA256); err != nil || len(sum) != sha256.Size {
				t.Errorf("%s/%s: sha256 %q is not a SHA-256 hex digest", platform, name, a.SHA256)
			}
			if sum, err := hex.DecodeString(a.BinarySHA256); err != nil || len(sum) != sha256.Size {
				t.Errorf("%s/%s: binary_sha256 %q is not a SHA-256 hex digest", platform, name, a.BinarySHA256)
			}
			if a.File == "" || filepath.Base(a.File) != a.File {
				t.Errorf("%s/%s: file %q is not a plain file name", platform, name, a.File)
			}
		}
	}
}
