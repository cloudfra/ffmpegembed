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

package ffmpegembed

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cloudfra/ffmpegembed/internal/download"
)

// gzipBytes returns data gzip-compressed.
func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withEmbedded replaces the embedded binaries for the duration of the test;
// nil simulates a platform that has none.
func withEmbedded(t *testing.T, gz []byte) {
	t.Helper()
	old := embeddedBinary
	embeddedBinary = func(string) []byte { return gz }
	t.Cleanup(func() { embeddedBinary = old })
}

// withInstalled makes PATH hold exactly one directory, containing placeholder
// ffmpeg and ffprobe executables, and returns it. With install false the
// directory is empty, simulating a machine with no ffmpeg installed.
func withInstalled(t *testing.T, install bool) string {
	t.Helper()
	dir := t.TempDir()
	if install {
		for _, name := range []string{"ffmpeg", "ffprobe"} {
			if err := os.WriteFile(filepath.Join(dir, exeName(name)), []byte("installed"), 0o700); err != nil { //nolint:gosec // G306: a placeholder executable in t.TempDir()
				t.Fatal(err)
			}
		}
	}
	t.Setenv("PATH", dir)
	return dir
}

// withDownload serves a pinned build containing data for the current platform
// and points the downloader at it, returning the cache directory it fills.
func withDownload(t *testing.T, data []byte) string {
	t.Helper()
	body := gzipBytes(t, data)
	sum := sha256.Sum256(body)
	asset := download.Asset{File: "bin.gz", SHA256: hex.EncodeToString(sum[:])}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Errorf("serve %s: %v", r.URL.Path, err)
		}
	}))
	t.Cleanup(srv.Close)

	cache := t.TempDir()
	old := newFetcher
	newFetcher = func() (*download.Fetcher, error) {
		return &download.Fetcher{
			CacheDir: cache,
			Manifest: &download.Manifest{
				Version: "test",
				BaseURL: srv.URL,
				Platforms: map[string]map[string]download.Asset{
					runtime.GOOS + "_" + runtime.GOARCH: {"ffmpeg": asset, "ffprobe": asset},
				},
			},
		}, nil
	}
	t.Cleanup(func() { newFetcher = old })
	return cache
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: a path resolved by the code under test
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestResolveOrder verifies the priority New resolves binaries in: embedded,
// then installed, then downloaded, with UseExternalIfAvailable promoting the
// installed binary.
func TestResolveOrder(t *testing.T) {
	tests := []struct {
		name      string
		embedded  bool
		installed bool
		args      *Args
		want      string // content of the resolved ffmpeg
	}{
		{name: "embedded wins by default", embedded: true, installed: true, args: &Args{}, want: "embedded"},
		{name: "installed preferred on request", embedded: true, installed: true, args: &Args{UseExternalIfAvailable: true}, want: "installed"},
		{name: "embedded when preferred install is absent", embedded: true, args: &Args{UseExternalIfAvailable: true}, want: "embedded"},
		{name: "installed when nothing is embedded", installed: true, args: &Args{}, want: "installed"},
		{name: "download as the last resort", args: &Args{}, want: "downloaded"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.embedded {
				withEmbedded(t, gzipBytes(t, []byte("embedded")))
			} else {
				withEmbedded(t, nil)
			}
			withInstalled(t, tc.installed)
			withDownload(t, []byte("downloaded"))

			f, err := New(tc.args)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer f.Close() //nolint:errcheck // test cleanup
			if got := readFile(t, f.FfmpegPath()); got != tc.want {
				t.Errorf("resolved ffmpeg is the %q binary; want %q", got, tc.want)
			}
			if got := readFile(t, f.FfprobePath()); got != tc.want {
				t.Errorf("resolved ffprobe is the %q binary; want %q", got, tc.want)
			}
		})
	}
}

// TestDownloadIsCachedAndSurvivesClose verifies a downloaded binary lives in
// the cache, not the per-session temp dir, so Close does not remove it.
func TestDownloadIsCachedAndSurvivesClose(t *testing.T) {
	withEmbedded(t, nil)
	withInstalled(t, false)
	cache := withDownload(t, []byte("downloaded"))

	f, err := New(&Args{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	path := f.FfmpegPath()
	if !strings.HasPrefix(path, cache) {
		t.Errorf("downloaded ffmpeg is at %q; want it under the cache dir %q", path, cache)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("downloaded ffmpeg was removed by Close: %v", err)
	}
}

// TestDisableDownload verifies that with no embedded or installed binary,
// DisableDownload makes New fail with ErrNoBinary instead of downloading, and
// that the error says how to fix it.
func TestDisableDownload(t *testing.T) {
	withEmbedded(t, nil)
	withInstalled(t, false)
	cache := withDownload(t, []byte("downloaded"))

	_, err := New(&Args{DisableDownload: true})
	if !errors.Is(err, ErrNoBinary) {
		t.Fatalf("New error = %v; want ErrNoBinary", err)
	}
	for _, want := range []string{"Args.DisableDownload", "to fix:", "PATH"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("DisableDownload still wrote %d entries to the cache", len(entries))
	}
}

// TestDownloadFailureIsErrNoBinary verifies a failed download surfaces as
// ErrNoBinary while keeping the underlying cause visible.
func TestDownloadFailureIsErrNoBinary(t *testing.T) {
	withEmbedded(t, nil)
	withInstalled(t, false)
	old := newFetcher
	newFetcher = func() (*download.Fetcher, error) {
		return &download.Fetcher{CacheDir: t.TempDir(), Manifest: &download.Manifest{Version: "test"}}, nil
	}
	t.Cleanup(func() { newFetcher = old })

	_, err := New(&Args{})
	if !errors.Is(err, ErrNoBinary) || !errors.Is(err, download.ErrUnsupported) {
		t.Fatalf("New error = %v; want ErrNoBinary wrapping download.ErrUnsupported", err)
	}
}
