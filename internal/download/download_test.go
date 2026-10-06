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

// newFetcher serves body as the ffmpeg asset for linux/amd64 and returns a
// Fetcher whose manifest pins it to digest, plus a counter of requests served.
func newFetcher(t *testing.T, body []byte, digest string) (*Fetcher, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/ffmpeg-linux-x64.gz" {
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

	// A second Fetch is served from the cache without touching the network.
	if _, err := f.Fetch(context.Background(), "linux", "amd64", "ffmpeg"); err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("server saw %d requests; want 1", n)
	}

	// Only the binary is left behind: no temp download or partial file.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("cache dir has %d entries; want only the binary", len(entries))
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
	if len(entries) != 0 {
		t.Errorf("a rejected download left %d files in the cache; want none", len(entries))
	}
}

func TestFetchUnsupportedPlatform(t *testing.T) {
	f, hits := newFetcher(t, nil, "")
	if _, err := f.Fetch(context.Background(), "plan9", "amd64", "ffmpeg"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Fetch(plan9) error = %v; want ErrUnsupported", err)
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
// platform pins both binaries to a URL-safe file name and a SHA-256 digest.
func TestDefaultManifest(t *testing.T) {
	m, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if m.Version == "" || m.License == "" || m.BaseURL == "" || len(m.Platforms) == 0 {
		t.Fatalf("manifest is missing required fields: %+v", m)
	}
	for platform, assets := range m.Platforms {
		for _, name := range []string{"ffmpeg", "ffprobe"} {
			a, ok := assets[name]
			if !ok {
				t.Errorf("%s: no %s asset", platform, name)
				continue
			}
			if sum, err := hex.DecodeString(a.SHA256); err != nil || len(sum) != sha256.Size {
				t.Errorf("%s/%s: sha256 %q is not a SHA-256 hex digest", platform, name, a.SHA256)
			}
			if a.File == "" || filepath.Base(a.File) != a.File {
				t.Errorf("%s/%s: file %q is not a plain file name", platform, name, a.File)
			}
		}
	}
}
