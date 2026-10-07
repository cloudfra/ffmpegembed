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

//go:build !((linux || windows) && amd64)

package embedded

// This platform has no embedded archive: data is left nil, so Get returns
// nil, and the resolver in the root package falls back to an ffmpeg/ffprobe on
// the system PATH or to a verified download.
var data []byte
