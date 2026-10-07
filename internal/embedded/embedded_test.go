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

package embedded

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cloudfra/ffmpegembed/internal/archive"
	"github.com/cloudfra/ffmpegembed/internal/manifest"
)

// embeddedPlatforms are the platforms with an archive committed under bin/.
// Keep in sync with the embed_<goos>_<goarch>.go files and the ffembed-update
// target in Makefile_ffmpeg.mk.
var embeddedPlatforms = []string{"linux_amd64", "windows_amd64"}

// xzMagic is the six-byte header every xz stream starts with.
var xzMagic = []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: a path under t.TempDir() or a fixed package path
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestCommittedArchivesMatchManifest extracts every committed archive and
// verifies that ffmpeg, ffprobe and the license it holds are exactly the files
// the module's manifest pins, so the embedded and the downloaded builds cannot
// drift apart and a corrupted or swapped archive is caught. It reads the
// archives from disk, so it covers every embedded platform regardless of the
// one the test runs on.
//
// Decoding the archives is slow, especially under the race detector, so the
// test is skipped in short mode.
func TestCommittedArchivesMatchManifest(t *testing.T) {
	if testing.Short() {
		t.Skip("decodes every committed archive; skipped in short mode")
	}
	m, err := manifest.Default()
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]string{archive.Ffmpeg: manifest.Ffmpeg, archive.Ffprobe: manifest.Ffprobe, archive.License: manifest.License}
	for _, platform := range embeddedPlatforms {
		t.Run(platform, func(t *testing.T) {
			pinned, ok := m.Platforms[platform]
			if !ok {
				t.Fatalf("the manifest has no platform %s", platform)
			}
			data, err := os.ReadFile(filepath.Join("bin", platform, "ffmpeg.tar.xz")) //nolint:gosec // G304: fixed paths within the package
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := archive.Extract(data, dir, func(entry string) string { return entry }); err != nil {
				t.Fatalf("Extract: %v", err)
			}
			for entry, assetName := range entries {
				asset, _ := pinned.Asset(assetName)
				if got := sha256File(t, filepath.Join(dir, entry)); got != asset.BinarySHA256 {
					t.Errorf("%s: archive entry has sha256 %s; manifest pins %s", entry, got, asset.BinarySHA256)
				}
			}
			// The plain LICENSE committed beside the archive is the same text.
			if got := sha256File(t, filepath.Join("bin", platform, "LICENSE")); got != pinned.License.BinarySHA256 {
				t.Errorf("committed LICENSE has sha256 %s; manifest pins %s", got, pinned.License.BinarySHA256)
			}
		})
	}
}

// TestCommittedArchivesAreXz is the cheap counterpart that still runs in short
// mode: every embedded platform has an xz archive committed.
func TestCommittedArchivesAreXz(t *testing.T) {
	for _, platform := range embeddedPlatforms {
		data, err := os.ReadFile(filepath.Join("bin", platform, "ffmpeg.tar.xz")) //nolint:gosec // G304: fixed paths within the package
		if err != nil {
			t.Errorf("%s: %v", platform, err)
			continue
		}
		if len(data) < len(xzMagic) || string(data[:len(xzMagic)]) != string(xzMagic) {
			t.Errorf("%s: ffmpeg.tar.xz is not an xz stream", platform)
		}
	}
}

// TestGet verifies Get exposes an archive exactly on the embedded platforms.
func TestGet(t *testing.T) {
	want := false
	for _, platform := range embeddedPlatforms {
		if platform == runtime.GOOS+"_"+runtime.GOARCH {
			want = true
		}
	}
	if got := len(Get()) > 0; got != want {
		t.Errorf("Get() returned an archive: %v on %s/%s; want %v", got, runtime.GOOS, runtime.GOARCH, want)
	}
}
