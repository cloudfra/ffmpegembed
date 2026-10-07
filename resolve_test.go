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
	"archive/tar"
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

	"github.com/ulikunitz/xz"

	"github.com/cloudfra/ffmpegembed/internal/archive"
	"github.com/cloudfra/ffmpegembed/internal/download"
	"github.com/cloudfra/ffmpegembed/internal/manifest"
)

// tarXz returns a tar.xz archive laid out like the ones mkffmpegembed builds:
// ffmpeg and ffprobe, both holding content, and a LICENSE.
func tarXz(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	for _, e := range []struct {
		name string
		mode int64
		data string
	}{
		{archive.Ffmpeg, 0o755, content},
		{archive.Ffprobe, 0o755, content},
		{archive.License, 0o644, "embedded license"},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
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

// withDownload serves a release for the current platform whose ffmpeg and
// ffprobe hold content, redirects the download cache to a temp directory, and
// returns the option that points New at that release along with the cache
// directory.
func withDownload(t *testing.T, content string) (Option, string) {
	t.Helper()
	asset := func(file, data string) (manifest.Asset, []byte) {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		gz, raw := sha256.Sum256(buf.Bytes()), sha256.Sum256([]byte(data))
		return manifest.Asset{File: file, SHA256: hex.EncodeToString(gz[:]), BinarySHA256: hex.EncodeToString(raw[:])}, buf.Bytes()
	}
	binary, binaryGz := asset("bin.gz", content)
	license, licenseGz := asset("LICENSE.gz", "downloaded license")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := binaryGz
		if r.URL.Path == "/LICENSE.gz" {
			body = licenseGz
		}
		if _, err := w.Write(body); err != nil {
			t.Errorf("serve %s: %v", r.URL.Path, err)
		}
	}))
	t.Cleanup(srv.Close)

	m := &manifest.Manifest{
		Version: "test",
		License: "GPL-3.0-or-later",
		BaseURL: srv.URL,
		Platforms: map[string]manifest.Platform{
			manifest.Key(runtime.GOOS, runtime.GOARCH): {Ffmpeg: binary, Ffprobe: binary, License: license},
		},
	}
	data, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	cache := t.TempDir()
	old := userCacheDir
	userCacheDir = func() (string, error) { return cache, nil }
	t.Cleanup(func() { userCacheDir = old })
	return WithManifest(data), cache
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
			embed := WithEmbed(nil)
			if tc.embedded {
				embed = WithEmbed(Archive(tarXz(t, "embedded")))
			}
			withInstalled(t, tc.installed)
			release, _ := withDownload(t, "downloaded")

			f, err := New(tc.args, embed, release)
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

// customEmbed is an Embed that is not an Archive, standing in for a caller's
// own implementation.
type customEmbed struct{ data []byte }

func (c customEmbed) Get() []byte { return c.data }

// TestWithEmbedUsesCallerArchive verifies New extracts from whatever Embed the
// caller supplies, including one of their own type.
func TestWithEmbedUsesCallerArchive(t *testing.T) {
	withInstalled(t, false)
	f, err := New(&Args{DisableDownload: true}, WithEmbed(customEmbed{tarXz(t, "custom build")}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer f.Close() //nolint:errcheck // test cleanup
	if got := readFile(t, f.FfmpegPath()); got != "custom build" {
		t.Errorf("resolved ffmpeg holds %q; want the caller's build", got)
	}
}

// TestEmbeddedLicenseIsExtracted verifies the license text shipped in the
// embedded archive is written next to the extracted binaries.
func TestEmbeddedLicenseIsExtracted(t *testing.T) {
	withInstalled(t, false)

	f, err := New(&Args{}, WithEmbed(Archive(tarXz(t, "embedded"))))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer f.Close() //nolint:errcheck // test cleanup
	license := filepath.Join(filepath.Dir(f.FfmpegPath()), download.LicenseFile)
	if got := readFile(t, license); got != "embedded license" {
		t.Errorf("%s = %q; want the embedded license text", license, got)
	}
	if filepath.Dir(f.FfprobePath()) != filepath.Dir(f.FfmpegPath()) {
		t.Errorf("ffmpeg (%s) and ffprobe (%s) were extracted to different directories", f.FfmpegPath(), f.FfprobePath())
	}
}

// TestInlineBinaryIsNotOverwrittenByEmbedded verifies that when one binary is
// supplied inline and the other comes from the embedded archive, extracting
// the archive leaves the inline one in place.
func TestInlineBinaryIsNotOverwrittenByEmbedded(t *testing.T) {
	withInstalled(t, false)
	embed := WithEmbed(Archive(tarXz(t, "embedded")))

	for _, tc := range []struct {
		name                    string
		args                    *Args
		wantFfmpeg, wantFfprobe string
	}{
		{name: "inline ffmpeg", args: &Args{FfmpegBinary: []byte("inline")}, wantFfmpeg: "inline", wantFfprobe: "embedded"},
		{name: "inline ffprobe", args: &Args{FfprobeBinary: []byte("inline")}, wantFfmpeg: "embedded", wantFfprobe: "inline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := New(tc.args, embed)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer f.Close() //nolint:errcheck // test cleanup
			if got := readFile(t, f.FfmpegPath()); got != tc.wantFfmpeg {
				t.Errorf("ffmpeg is the %q binary; want %q", got, tc.wantFfmpeg)
			}
			if got := readFile(t, f.FfprobePath()); got != tc.wantFfprobe {
				t.Errorf("ffprobe is the %q binary; want %q", got, tc.wantFfprobe)
			}
		})
	}
}

// TestDownloadIsCachedAndSurvivesClose verifies a downloaded binary lives in
// the cache with its license, not in the per-session temp dir, so Close does
// not remove it.
func TestDownloadIsCachedAndSurvivesClose(t *testing.T) {
	withInstalled(t, false)
	release, cache := withDownload(t, "downloaded")

	f, err := New(&Args{}, WithEmbed(nil), release)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	path := f.FfmpegPath()
	if !strings.HasPrefix(path, cache) {
		t.Errorf("downloaded ffmpeg is at %q; want it under the cache dir %q", path, cache)
	}
	if got := readFile(t, filepath.Join(filepath.Dir(path), download.LicenseFile)); got != "downloaded license" {
		t.Errorf("license beside the downloaded ffmpeg = %q; want the downloaded license text", got)
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
	withInstalled(t, false)
	release, cache := withDownload(t, "downloaded")

	_, err := New(&Args{DisableDownload: true}, WithEmbed(nil), release)
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
	withInstalled(t, false)
	_, _ = withDownload(t, "downloaded") // redirects the cache; its release is not used

	// A manifest that only covers some other platform has nothing for this one.
	digest := strings.Repeat("0", 64)
	asset := manifest.Asset{File: "f.gz", SHA256: digest, BinarySHA256: digest}
	elsewhere := &manifest.Manifest{
		Version:   "test",
		License:   "GPL-3.0-or-later",
		BaseURL:   "https://example.invalid",
		Platforms: map[string]manifest.Platform{"plan9_mips": {Ffmpeg: asset, Ffprobe: asset, License: asset}},
	}
	data, err := elsewhere.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(&Args{}, WithEmbed(nil), WithManifest(data))
	if !errors.Is(err, ErrNoBinary) || !errors.Is(err, download.ErrUnsupported) {
		t.Errorf("New error = %v; want ErrNoBinary wrapping download.ErrUnsupported", err)
	}

	// A manifest that does not parse is reported as such.
	_, err = New(&Args{}, WithEmbed(nil), WithManifest([]byte(`{"not": "a manifest"`)))
	if !errors.Is(err, ErrNoBinary) || !strings.Contains(err.Error(), "manifest") {
		t.Errorf("New error = %v; want ErrNoBinary naming the manifest", err)
	}
}
