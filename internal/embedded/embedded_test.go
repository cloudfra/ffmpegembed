// Copyright 2026 Cloudfra
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed by the user in writing, software
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

	"github.com/cloudfra/ffmpegembed/internal/download"
)

// embeddedPlatforms are the platforms with an archive committed under bin/.
// Keep in sync with the embed_<goos>_<goarch>.go files and
// scripts/update-ffmpeg.sh.
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
// the download manifest pins, so the embedded and the downloaded builds cannot
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
	m, err := download.Default()
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]string{Ffmpeg: "ffmpeg", Ffprobe: "ffprobe", License: "license"}
	for _, platform := range embeddedPlatforms {
		t.Run(platform, func(t *testing.T) {
			archive, err := os.ReadFile(filepath.Join("bin", platform, "ffmpeg.tar.xz")) //nolint:gosec // G304: fixed paths within the package
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := Extract(archive, dir, func(entry string) string { return entry }); err != nil {
				t.Fatalf("Extract: %v", err)
			}
			for entry, assetName := range entries {
				asset, ok := m.Platforms[platform][assetName]
				if !ok || asset.BinarySHA256 == "" {
					t.Errorf("%s: no binary_sha256 for %s in the download manifest", platform, assetName)
					continue
				}
				if got := sha256File(t, filepath.Join(dir, entry)); got != asset.BinarySHA256 {
					t.Errorf("%s/%s: archive entry has sha256 %s; manifest pins %s", platform, entry, got, asset.BinarySHA256)
				}
			}
			if runtime.GOOS != "windows" {
				for entry, wantExec := range map[string]bool{Ffmpeg: true, Ffprobe: true, License: false} {
					fi, err := os.Stat(filepath.Join(dir, entry))
					if err != nil {
						t.Fatal(err)
					}
					if gotExec := fi.Mode()&0o100 != 0; gotExec != wantExec {
						t.Errorf("%s/%s: mode %v; want executable=%v", platform, entry, fi.Mode(), wantExec)
					}
				}
			}
			// The plain LICENSE committed beside the archive is the same text.
			if got, want := sha256File(t, filepath.Join("bin", platform, "LICENSE")), m.Platforms[platform]["license"].BinarySHA256; got != want {
				t.Errorf("%s: committed LICENSE has sha256 %s; manifest pins %s", platform, got, want)
			}
		})
	}
}

// TestCommittedArchivesAreXz is the cheap counterpart that still runs in short
// mode: every embedded platform has an xz archive committed.
func TestCommittedArchivesAreXz(t *testing.T) {
	for _, platform := range embeddedPlatforms {
		f, err := os.Open(filepath.Join("bin", platform, "ffmpeg.tar.xz")) //nolint:gosec // G304: fixed paths within the package
		if err != nil {
			t.Errorf("%s: %v", platform, err)
			continue
		}
		head := make([]byte, len(xzMagic))
		_, err = f.Read(head)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil || string(head) != string(xzMagic) {
			t.Errorf("%s: ffmpeg.tar.xz is not an xz stream (read error: %v)", platform, err)
		}
	}
}

func TestExtractSkipsUnwantedEntries(t *testing.T) {
	if !HasEmbedded() {
		t.Skip("no embedded archive on this platform")
	}
	if testing.Short() {
		t.Skip("decodes the embedded archive; skipped in short mode")
	}
	dir := t.TempDir()
	err := Extract(Archive(), dir, func(entry string) string {
		if entry == License {
			return "the-license.txt"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != "the-license.txt" {
		t.Errorf("Extract wrote %v; want only the-license.txt", files)
	}
}

func TestHasEmbedded(t *testing.T) {
	want := false
	for _, platform := range embeddedPlatforms {
		if platform == runtime.GOOS+"_"+runtime.GOARCH {
			want = true
		}
	}
	if got := HasEmbedded(); got != want {
		t.Errorf("HasEmbedded() = %v on %s/%s; want %v", got, runtime.GOOS, runtime.GOARCH, want)
	}
}
