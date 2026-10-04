// Copyright 2026 Cloudfra
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed by the user in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package embedded carries the ffmpeg/ffprobe binaries that ffmpegembed ships
// with the Go module. The actual binary bytes are not committed; the Makefile
// downloads a matching static build (see Makefile_ffmpeg.mk) from
// BtbN/FFmpeg-Builds into bin/<goos>_<goarch>/ before the package is compiled,
// and go:embed (in the per-platform files in this package) picks them up.
//
// Builds for a GOOS/GOARCH pair that has no embedded binary fall back to a
// stub (embed_other.go) where HasEmbedded() is false; on such platforms the
// library still works, but requires an ffmpeg/ffprobe found on the system
// (see Args.UseExternalIfAvailable) or an inline binary override.
package embedded

// Version is the FFmpeg release tag the embedded binaries were built from,
// injected at build time when known (e.g. "n9.0"). Empty when unknown.
var Version = "latest"

// hasEmbedded reports whether this build has linked-in ffmpeg/ffprobe bytes.
// Set by the matching per-platform file, or left false by embed_other.go.
var hasEmbedded = false

// Ffmpeg returns the embedded ffmpeg binary, or nil when none is available for
// the current GOOS/GOARCH.
func Ffmpeg() []byte {
	if !hasEmbedded {
		return nil
	}
	return ffmpeg
}

// Ffprobe returns the embedded ffprobe binary, or nil when none is available.
func Ffprobe() []byte {
	if !hasEmbedded {
		return nil
	}
	return ffprobe
}

// HasEmbedded reports whether embedded binary bytes are linked into this build.
func HasEmbedded() bool {
	return hasEmbedded
}
