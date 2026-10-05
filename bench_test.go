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
	"testing"

	"github.com/cloudfra/ffmpegembed/proto"
)

// The benchmarks below exercise the pure, in-package parsing and formatting
// helpers that run on every progress event and every probe. They launch no
// external ffmpeg/ffprobe binary, so they are stable on any platform and give
// the `make bench` / `make benchmark.html` targets a real dataset: vizb fails
// with "No dataset found" when a package ships no `func Benchmark*`. They also
// provide a stable reference for the per-event cost of the OnUpdate progress
// path and the ffprobe JSON parse.

// BenchmarkParseProgress measures converting one ffmpeg `-progress pipe:1`
// sample (a flat key=value map) into a structured proto.Progress — the hot
// path of the OnUpdate progress callback.
func BenchmarkParseProgress(b *testing.B) {
	sample := map[string]string{
		"frame":       "120",
		"fps":         "24.5",
		"bitrate":     "2313.6kbits/s",
		"speed":       "0.0862x",
		"out_time_us": "4000000",
		"total_size":  "1048576",
		"drop_frames": "0",
		"dup_frames":  "1",
		"out_time":    "00:00:24.473832",
	}
	var sink *proto.Progress
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sink = parseProgress(sample)
	}
	if sink.Frame == 0 {
		b.Fatal("parseProgress(frame) = 0; fixture broken")
	}
}

// BenchmarkParseFfprobe measures parsing a representative ffprobe JSON payload
// (the probeJSON fixture shared with the tests in ffmpegembed_test.go) into a
// structured FfProbeResult.
func BenchmarkParseFfprobe(b *testing.B) {
	var sink *FfProbeResult
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		res, err := parseFfprobe(probeJSON)
		if err != nil {
			b.Fatalf("parseFfprobe: %v", err)
		}
		sink = res
	}
	if len(sink.GetStreams()) != 2 {
		b.Fatalf("parseFfprobe: streams = %d, want 2", len(sink.GetStreams()))
	}
}

// BenchmarkClampFraction measures the completion-fraction math that maps
// out_time_us / total_duration_us to the Progress.Percent value (0.0..1.0).
func BenchmarkClampFraction(b *testing.B) {
	var sink float64
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sink = clampFraction(4_000_000, 9_000_000)
	}
	if sink <= 0 || sink >= 1 {
		b.Fatalf("clampFraction(4e6, 9e6) = %v; want (0, 1)", sink)
	}
}
