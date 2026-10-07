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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/cloudfra/ffmpegembed/internal/manifest"
)

// release serves ffmpeg.gz and LICENSE.gz and returns a Fetcher for a
// manifest that pins them for linux/amd64, plus a counter of requests served.
// The manifest's ffprobe points at a file the server does not have.
func release(t *testing.T, ffmpeg, license []byte) (*Fetcher, *atomic.Int32) {
	t.Helper()
	files := map[string][]byte{"/ffmpeg.gz": gz(t, ffmpeg), "/LICENSE.gz": gz(t, license)}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if _, err := w.Write(body); err != nil {
			t.Errorf("serve %s: %v", r.URL.Path, err)
		}
	}))
	t.Cleanup(srv.Close)
	asset := func(file string, raw []byte) manifest.Asset {
		return manifest.Asset{File: file, SHA256: sha(files["/"+file]), BinarySHA256: sha(raw)}
	}
	return &Fetcher{
		CacheDir: t.TempDir(),
		Manifest: &manifest.Manifest{
			Version: "v1",
			License: "GPL-3.0-or-later",
			BaseURL: srv.URL,
			Platforms: map[string]manifest.Platform{
				"linux_amd64": {
					Ffmpeg:  asset("ffmpeg.gz", ffmpeg),
					Ffprobe: manifest.Asset{File: "missing.gz", SHA256: sha(nil), BinarySHA256: sha(nil)},
					License: asset("LICENSE.gz", license),
				},
			},
		},
	}, &hits
}

func TestFetchDownloadsVerifiesAndCaches(t *testing.T) {
	want := []byte("pretend this is ffmpeg")
	f, hits := release(t, want, []byte("the license"))

	path, err := f.Fetch(context.Background(), "linux", "amd64", manifest.Ffmpeg)
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
	license, err := os.ReadFile(filepath.Join(filepath.Dir(path), LicenseFile)) //nolint:gosec // G304: path under t.TempDir()
	if err != nil {
		t.Fatalf("license was not stored beside the binary: %v", err)
	}
	if string(license) != "the license" {
		t.Errorf("license = %q; want %q", license, "the license")
	}

	// A second Fetch is served from the cache without touching the network.
	if _, err := f.Fetch(context.Background(), "linux", "amd64", manifest.Ffmpeg); err != nil {
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

// TestFetchRejectsWrongDigests verifies a download is refused, and nothing is
// cached, when either the file or its decompressed content does not have the
// digest the manifest pins.
func TestFetchRejectsWrongDigests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(a *manifest.Asset)
	}{
		{"downloaded file", func(a *manifest.Asset) { a.SHA256 = sha([]byte("something else")) }},
		{"decompressed content", func(a *manifest.Asset) { a.BinarySHA256 = sha([]byte("something else")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := release(t, []byte("ffmpeg"), []byte("the license"))
			p := f.Manifest.Platforms["linux_amd64"]
			tc.tamper(&p.Ffmpeg)
			f.Manifest.Platforms["linux_amd64"] = p

			path, err := f.Fetch(context.Background(), "linux", "amd64", manifest.Ffmpeg)
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
		})
	}
}

func TestFetchUnsupported(t *testing.T) {
	f, hits := release(t, []byte("ffmpeg"), []byte("the license"))
	if _, err := f.Fetch(context.Background(), "plan9", "amd64", manifest.Ffmpeg); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Fetch(plan9) error = %v; want ErrUnsupported", err)
	}
	// The license is fetched with a binary, never as one.
	if _, err := f.Fetch(context.Background(), "linux", "amd64", manifest.License); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Fetch(license) error = %v; want ErrUnsupported", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("server saw %d requests for unsupported fetches; want 0", n)
	}
}

func TestFetchHTTPError(t *testing.T) {
	f, _ := release(t, []byte("ffmpeg"), []byte("the license"))
	if _, err := f.Fetch(context.Background(), "linux", "amd64", manifest.Ffprobe); err == nil {
		t.Error("Fetch of a 404 asset succeeded; want an error")
	}
}
