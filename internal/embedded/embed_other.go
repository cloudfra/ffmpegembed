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

//go:build !((linux && (amd64 || arm64)) || (windows && (amd64 || arm64)))

package embedded

// This platform has no embedded binary. ffmpeg/ffprobe are left nil and
// HasEmbedded() stays false; the library can still run if an ffmpeg/ffprobe is
// available on the system (or supplied inline via Args), which the resolver in
// the root package handles.
var (
	ffmpeg  []byte
	ffprobe []byte
)
