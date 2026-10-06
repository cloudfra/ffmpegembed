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

	"github.com/cloudfra/ffmpegembed/internal/download"
)

// embeddedPlatforms are the platforms with binaries committed under bin/. Keep
// in sync with the embed_<goos>_<goarch>.go files and scripts/update-ffmpeg.sh.
var embeddedPlatforms = []string{"linux_amd64", "windows_amd64"}

// TestCommittedBinariesMatchManifest verifies every committed binary is the
// exact release asset the download manifest pins, so the embedded and the
// downloaded builds cannot drift apart and a corrupted or swapped blob is
// caught. It reads the files from disk, so it covers every embedded platform
// regardless of the one the test runs on.
func TestCommittedBinariesMatchManifest(t *testing.T) {
	m, err := download.Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, platform := range embeddedPlatforms {
		for _, name := range []string{"ffmpeg", "ffprobe"} {
			asset, ok := m.Platforms[platform][name]
			if !ok {
				t.Errorf("%s/%s: not in the download manifest", platform, name)
				continue
			}
			data, err := os.ReadFile(filepath.Join("bin", platform, name+".gz")) //nolint:gosec // G304: fixed paths within the package
			if err != nil {
				t.Errorf("%s/%s: %v", platform, name, err)
				continue
			}
			sum := sha256.Sum256(data)
			if got := hex.EncodeToString(sum[:]); got != asset.SHA256 {
				t.Errorf("%s/%s: committed binary has sha256 %s; manifest pins %s", platform, name, got, asset.SHA256)
			}
		}
	}
}

// TestCommittedBinariesUseMaximumCompression verifies every committed binary
// is a gzip stream written at the maximum compression level, which the gzip
// header records in its XFL byte (RFC 1952: 2 means "maximum compression,
// slowest algorithm"). These blobs are downloaded by everyone who fetches the
// module, so a release compressed at a lower level should not be committed
// as is.
func TestCommittedBinariesUseMaximumCompression(t *testing.T) {
	const (
		xflOffset         = 8
		xflMaxCompression = 2
	)
	for _, platform := range embeddedPlatforms {
		for _, name := range []string{"ffmpeg", "ffprobe"} {
			data, err := os.ReadFile(filepath.Join("bin", platform, name+".gz")) //nolint:gosec // G304: fixed paths within the package
			if err != nil {
				t.Errorf("%s/%s: %v", platform, name, err)
				continue
			}
			if len(data) <= xflOffset || data[0] != 0x1f || data[1] != 0x8b {
				t.Errorf("%s/%s: not a gzip stream", platform, name)
				continue
			}
			if got := data[xflOffset]; got != xflMaxCompression {
				t.Errorf("%s/%s: gzip XFL = %d; want %d (maximum compression)", platform, name, got, xflMaxCompression)
			}
		}
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
