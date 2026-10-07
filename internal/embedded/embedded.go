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

// Package embedded holds the ffmpeg archive that ships with the module, and
// nothing else: it exposes the archive's bytes through Get and leaves reading
// them to internal/archive.
//
// The archive for each supported platform is committed under
// bin/<goos>_<goarch>/ffmpeg.tar.xz and linked in with go:embed by that
// platform's file in this package, so a plain `go get` of the module is
// enough to build with it. Platforms without one use a stub (embed_other.go).
package embedded

// Get returns the tar.xz archive of ffmpeg, ffprobe and their license embedded
// for the current GOOS/GOARCH, or nil when there is none.
func Get() []byte { return data }
