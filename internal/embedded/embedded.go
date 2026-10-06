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

// Package embedded carries the ffmpeg/ffprobe binaries that ffmpegembed ships
// with the Go module. The binaries are committed gzip-compressed under
// bin/<goos>_<goarch>/ and linked in with go:embed by the per-platform files in
// this package, so a plain `go get` of the module is enough to build with them.
//
// They are the same release assets, byte for byte, that internal/download
// fetches for platforms without an embedded binary; scripts/update-ffmpeg.sh
// pins both to one release.
//
// Builds for a GOOS/GOARCH pair that has no embedded binary use a stub
// (embed_other.go) where HasEmbedded() is false.
package embedded

// Ffmpeg returns the embedded ffmpeg binary, gzip-compressed, or nil when none
// is available for the current GOOS/GOARCH.
func Ffmpeg() []byte { return ffmpegGz }

// Ffprobe returns the embedded ffprobe binary, gzip-compressed, or nil when
// none is available for the current GOOS/GOARCH.
func Ffprobe() []byte { return ffprobeGz }

// HasEmbedded reports whether embedded binaries are linked into this build.
func HasEmbedded() bool { return len(ffmpegGz) > 0 && len(ffprobeGz) > 0 }
