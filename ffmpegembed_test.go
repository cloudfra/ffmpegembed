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
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestBuildFfmpegArgsStructured(t *testing.T) {
	a := &FfmpegArgs{
		Inputs:     []string{"in.mov"},
		Output:     "out.mp4",
		VideoCodec: "libx264",
		AudioCodec: "aac",
		Crf:        23,
		Preset:     "fast",
		Faststart:  true,
		Overwrite:  true,
	}
	got := buildFfmpegArgs(a)
	want := []string{"-y", "-i", "in.mov", "-c:v", "libx264", "-c:a", "aac", "-crf", "23", "-preset", "fast", "-movflags", "faststart", "out.mp4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("structured args\n got: %#v\nwant: %#v", got, want)
	}
}

func TestBuildFfmpegArgsRaw(t *testing.T) {
	raw := []string{"-i", "a.wav", "-c:v", "copy", "b.mp4"}
	got := buildFfmpegArgs(&FfmpegArgs{RawArgs: raw, Inputs: []string{"ignored"}, Output: "ignored"})
	if !reflect.DeepEqual(got, raw) {
		t.Fatalf("raw args not used verbatim: %#v", got)
	}
}

func TestBuildFfmpegArgsEmpty(t *testing.T) {
	if got := buildFfmpegArgs(nil); got != nil {
		t.Fatalf("expected nil for nil args, got %#v", got)
	}
}

func TestBuildFfProbeArgsDefaults(t *testing.T) {
	got := buildFfProbeArgs(&FfProbeArgs{Input: "movie.mp4"})
	want := []string{"-print_format", "json", "-show_format", "-show_streams", "movie.mp4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default probe args\n got: %#v\nwant: %#v", got, want)
	}
}

func TestBuildFfProbeArgsExplicitShow(t *testing.T) {
	got := buildFfProbeArgs(&FfProbeArgs{Input: "m.mp4", ShowChapters: true, SelectStreams: "v:0"})
	want := []string{"-print_format", "json", "-show_chapters", "-select_streams", "v:0", "m.mp4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("explicit probe args\n got: %#v\nwant: %#v", got, want)
	}
}

func TestBuildFfProbeArgsRawAppended(t *testing.T) {
	got := buildFfProbeArgs(&FfProbeArgs{Input: "m.mp4", RawArgs: []string{"-count_packets"}})
	want := []string{"-print_format", "json", "-show_format", "-show_streams", "-count_packets", "m.mp4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("raw probe args\n got: %#v\nwant: %#v", got, want)
	}
}

func TestParseProgressSample(t *testing.T) {
	p := parseProgress(map[string]string{
		"frame":       "120",
		"fps":         "24.5",
		"bitrate":     "2313.6kbits/s",
		"speed":       "0.0862x",
		"out_time_us": "4000000",
		"total_size":  "1048576",
		"drop_frames": "0",
		"dup_frames":  "1",
		"out_time":    "00:00:24.473832",
	})
	if p.Frame != 120 {
		t.Errorf("frame: got %d want 120", p.Frame)
	}
	if p.Fps != 24.5 {
		t.Errorf("fps: got %v want 24.5", p.Fps)
	}
	if p.Bitrate != 2313.6 {
		t.Errorf("bitrate: got %v want 2313.6", p.Bitrate)
	}
	if p.Speed != 0.0862 {
		t.Errorf("speed: got %v want 0.0862", p.Speed)
	}
	if p.OutTimeUs != 4000000 {
		t.Errorf("out_time_us: got %d want 4000000", p.OutTimeUs)
	}
	if p.DupFrames != 1 {
		t.Errorf("dup_frames: got %d want 1", p.DupFrames)
	}
	if p.Time < 24.4 || p.Time > 24.5 {
		t.Errorf("out_time seconds: got %v want ~24.47", p.Time)
	}
	if p.Fields["fps"] != "24.5" {
		t.Errorf("fields not preserved: %#v", p.Fields)
	}
}

func TestParseOutTime(t *testing.T) {
	if got := parseOutTime("00:01:30.5"); got < 90.49 || got > 90.51 {
		t.Errorf("parseOutTime(00:01:30.5): got %v want ~90.5", got)
	}
	if got := parseOutTime("12.5"); got != 12.5 {
		t.Errorf("parseOutTime(12.5): got %v want 12.5", got)
	}
	if got := parseOutTime(""); got != 0 {
		t.Errorf("parseOutTime(): got %v want 0", got)
	}
}

const probeJSON = `{
  "format": {
    "filename": "movie.mp4",
    "size": "1048576",
    "format_name": "mov,mp4,m4a,3gp,3g2,mj2",
    "duration": "12.34",
    "bit_rate": "2500000",
    "tags": {"encoder": "Libav"}
  },
  "streams": [
    {
      "index": 0,
      "codec_name": "h264",
      "codec_type": "video",
      "width": 1920,
      "height": 1080,
      "pix_fmt": "yuv420p",
      "level": -99,
      "r_frame_rate": "30000/1001",
      "avg_frame_rate": "30000/1001",
      "bit_rate": "2000000"
    },
    {
      "index": 1,
      "codec_name": "aac",
      "codec_type": "audio",
      "sample_rate": "48000",
      "channels": 2,
      "channel_layout": "stereo"
    }
  ]
}`

func TestParseFfprobe(t *testing.T) {
	res, err := parseFfprobe(probeJSON)
	if err != nil {
		t.Fatalf("parseFfprobe: %v", err)
	}
	if res.GetFormat().GetFormatName() != "mov,mp4,m4a,3gp,3g2,mj2" {
		t.Errorf("format_name: got %q", res.GetFormat().GetFormatName())
	}
	if res.GetFormat().GetFileSize() != 1048576 {
		t.Errorf("size: got %d want 1048576", res.GetFormat().GetFileSize())
	}
	if res.GetFormat().GetDuration() < 12.33 || res.GetFormat().GetDuration() > 12.35 {
		t.Errorf("duration: got %v want ~12.34", res.GetFormat().GetDuration())
	}
	if len(res.GetStreams()) != 2 {
		t.Fatalf("streams: got %d want 2", len(res.GetStreams()))
	}
	vid := res.GetStreams()[0]
	if vid.GetCodecName() != "h264" || vid.GetWidth() != 1920 || vid.GetHeight() != 1080 {
		t.Errorf("video stream fields wrong: %#v", vid)
	}
	if vid.GetRFrameRate() < 29.9 || vid.GetRFrameRate() > 30.1 {
		t.Errorf("r_frame_rate: got %v want ~29.97", vid.GetRFrameRate())
	}
	if vid.GetLevel() != "-99" {
		t.Errorf("level: got %q want %q", vid.GetLevel(), "-99")
	}
	aud := res.GetStreams()[1]
	if aud.GetSampleRate() != 48000 || aud.GetChannels() != 2 {
		t.Errorf("audio stream fields wrong: %#v", aud)
	}
}

func TestParseFfprobeEmptyAndUnstructured(t *testing.T) {
	if res, err := parseFfprobe(""); err != nil || res == nil {
		t.Fatalf("empty: err=%v res=%v", err, res)
	}
	res, err := parseFfprobe("not valid json {")
	if err != nil {
		t.Fatalf("unstructured should not error: %v", err)
	}
	if res.GetRawJson() != "not valid json {" {
		t.Fatalf("raw json not preserved when unparseable: %q", res.GetRawJson())
	}
}

// TestNewClose resolves via the embedded binary (present for the host platform)
// and verifies the extracted binaries are created and cleaned up on Close.
func TestNewEmbeddedResolutionAndClose(t *testing.T) {
	if testing.Short() {
		// Decoding the embedded xz archive takes many seconds under the race
		// detector; the deflake run repeats the suite in short mode.
		t.Skip("extracts the embedded archive; skipped in short mode")
	}
	f, err := New(&Args{}) // no external preference, no inline bytes
	if err != nil {
		t.Skipf("no embedded binary on host; skipping: %v", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	}()

	for _, p := range []string{f.FfmpegPath(), f.FfprobePath()} {
		if p == "" {
			t.Fatal("resolved path empty")
		}
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("extracted binary missing: %v", err)
		}
		// Executability is the Unix mode's execute bit on POSIX, but on
		// Windows it comes from the file extension / ACL, so the 0o100 bit is
		// absent by design. Assert the .exe suffix there instead of the
		// (meaningless-on-Windows) exec bit.
		if runtime.GOOS == "windows" {
			if !strings.HasSuffix(p, ".exe") {
				t.Errorf("extracted binary should be a .exe on Windows: %s", p)
			}
		} else if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("extracted binary not executable: %s", p)
		}
	}

	dir := filepath.Dir(f.FfmpegPath())
	before := dir
	if cerr := f.Close(); cerr != nil {
		t.Fatalf("Close: %v", cerr)
	}
	if _, err := os.Stat(before); err == nil {
		t.Errorf("temp dir not removed on Close: %s", before)
	}
	// Second Close is a no-op.
	if err := f.Close(); err != nil {
		t.Errorf("double Close error: %v", err)
	}
}

// TestFfexecVersion verifies Ffexec.Version()/FfprobeVersion() return a
// non-empty "…version…" line for the resolved binaries.
func TestFfexecVersion(t *testing.T) {
	f := newAndSkip(t)

	ver, err := f.Version()
	if err != nil {
		t.Fatalf("Version() error: %v", err)
	}
	if ver == "" || !strings.Contains(ver, "version") {
		t.Errorf("Version() did not look like a version line: %q", ver)
	}

	pver, perr := f.FfprobeVersion()
	if perr != nil {
		t.Logf("FfprobeVersion() error (non-fatal): %v", perr)
	} else if pver == "" || !strings.Contains(pver, "version") {
		t.Errorf("FfprobeVersion() did not look like a version line: %q", pver)
	}

	t.Logf("ffmpeg:  %s", ver)
	t.Logf("ffprobe: %s", pver)
}

// TestClampFraction covers the pure completion-fraction helper that turns raw
// out_time_us / total_duration_us into the Progress.Percent value (a 0.0-1.0
// fraction).
func TestClampFraction(t *testing.T) {
	cases := []struct {
		name         string
		nowUs, total int64
		want         float64
	}{
		{"zero now", 0, 1_000_000, 0.0},
		{"negative now", -100, 1_000_000, 0.0},
		{"unknown total", 1_000_000, 0, 0.0},
		{"negative total", 1_000_000, -1, 0.0},
		{"one tenth", 100_000, 1_000_000, 0.1},
		{"halfway", 500_000, 1_000_000, 0.5},
		{"exactly done", 1_000_000, 1_000_000, 1.0},
		{"overrun clamps to one", 1_500_000, 1_000_000, 1.0},
		{"both zero", 0, 0, 0.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := clampFraction(c.nowUs, c.total); got != c.want {
				t.Errorf("clampFraction(now=%d, total=%d) = %v, want %v",
					c.nowUs, c.total, got, c.want)
			}
		})
	}
}

// shared is the Ffexec handed out by newAndSkip. Resolving one means
// decompressing the embedded ffmpeg and ffprobe, which is expensive (far more
// so under the race detector), so the end-to-end tests share a single handle
// that TestMain closes instead of each resolving their own.
var shared struct {
	once sync.Once
	f    *Ffexec
	err  error
}

// newAndSkip returns the shared Ffexec, resolving it on first use, and skips
// the test when no ffmpeg/ffprobe can be resolved on the host. Tests must not
// Close it.
func newAndSkip(t *testing.T) *Ffexec {
	t.Helper()
	shared.once.Do(func() { shared.f, shared.err = New(&Args{}) })
	if shared.err != nil {
		t.Skipf("no ffmpeg/ffprobe resolvable on host; skip: %v", shared.err)
	}
	return shared.f
}

// TestMain closes the shared Ffexec, removing its temp directory, once every
// test has run.
func TestMain(m *testing.M) {
	code := m.Run()
	if shared.f != nil {
		if err := shared.f.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "closing shared Ffexec: %v\n", err)
			code = 1
		}
	}
	os.Exit(code)
}

// TestExampleFfprobe is an end-to-end example: generate one second of lavfi
// test video on disk via Ffmpeg, then probe it and assert the structured
// Format/Streams fields land as expected. This doubles as an example of how
// to use New + Ffprobe + Close in a single test.
func TestExampleFfprobe(t *testing.T) {
	f := newAndSkip(t)
	tmp := t.TempDir()
	inPath := filepath.Join(tmp, "in.mov")

	gen, err := f.Ffmpeg(&FfmpegArgs{RawArgs: []string{
		"-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=160x120:rate=10",
		"-an", "-c:v", "libx264", "-preset", "veryfast",
		"-f", "mov", inPath,
	}})
	if err != nil {
		t.Fatalf("Ffmpeg(generate): %v", err)
	}
	if err := gen.Wait(); err != nil {
		t.Fatalf("Ffmpeg(generate).Wait: %v", err)
	}

	res, err := f.Ffprobe(&FfProbeArgs{Input: inPath})
	if err != nil {
		t.Fatalf("Ffprobe: %v", err)
	}
	if got := res.GetFormat().GetDuration(); got <= 0 || got > 5 {
		t.Errorf("Format.Duration = %v; want (0, 5]", got)
	}
	if len(res.GetStreams()) < 1 {
		t.Errorf("len(Format.Streams) = %d; want >= 1", len(res.GetStreams()))
	}
	name := res.GetFormat().GetFormatName()
	if !strings.Contains(name, "mov") && !strings.Contains(name, "mp4") && !strings.Contains(name, "qt") {
		t.Errorf("Format.FormatName = %q; expected mov/mp4/qt family", name)
	}
	t.Logf("input=%s duration=%.3fs streams=%d fmt=%q",
		inPath, res.GetFormat().GetDuration(), len(res.GetStreams()), name)
}

// TestExampleFfmpegEncodeProgress is an end-to-end example that exercises the
// new Progress.Percent field: it generates a short test video, re-encodes it
// while collecting progress via OnUpdate, and asserts that:
//
//   - at least one Progress event is delivered
//   - every Percent lies in [0.0, 1.0]
//   - at least one Percent is strictly > 0 (the generated input has a known
//     duration, so Percent must be computable)
//   - the final Percent is the maximum observed (non-decreasing)
func TestExampleFfmpegEncodeProgress(t *testing.T) {
	f := newAndSkip(t)
	tmp := t.TempDir()
	inPath := filepath.Join(tmp, "in.mov")
	outPath := filepath.Join(tmp, "out.mp4")

	gen, err := f.Ffmpeg(&FfmpegArgs{RawArgs: []string{
		"-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=160x120:rate=10",
		"-an", "-c:v", "libx264", "-preset", "veryfast",
		"-f", "mov", inPath,
	}})
	if err != nil {
		t.Fatalf("Ffmpeg(generate): %v", err)
	}
	if err := gen.Wait(); err != nil {
		t.Fatalf("Ffmpeg(generate).Wait: %v", err)
	}

	var (
		mu        sync.Mutex
		percents  []float64
		lastFrame int64
		succeeded bool
	)
	run, err := f.Ffmpeg(&FfmpegArgs{
		Inputs:     []string{inPath},
		Output:     outPath,
		VideoCodec: "libx264",
		Overwrite:  true,
	})
	if err != nil {
		t.Fatalf("Ffmpeg(encode): %v", err)
	}
	run.OnUpdate(func(ev *Event) {
		if p := ev.GetProgress(); p != nil {
			mu.Lock()
			percents = append(percents, p.Percent)
			lastFrame = p.Frame
			mu.Unlock()
		}
		if r := ev.GetResult(); r != nil && r.GetSuccess() {
			mu.Lock()
			succeeded = true
			mu.Unlock()
		}
	})
	if err := run.Wait(); err != nil {
		t.Fatalf("Ffmpeg(encode).Wait: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if !succeeded {
		t.Fatalf("encode did not report success; cannot assert on progress")
	}
	if len(percents) == 0 {
		t.Fatalf("no Progress events delivered; expected >= 1")
	}
	for i, p := range percents {
		if p < 0.0 || p > 1.0 {
			t.Errorf("percents[%d] = %v — out of [0.0, 1.0]", i, p)
		}
	}
	posCount := 0
	for _, p := range percents {
		if p > 0.0 {
			posCount++
		}
	}
	if posCount == 0 {
		t.Errorf("all percents are 0.0; expected > 0 for an input with a known duration")
	}
	for i := 1; i < len(percents); i++ {
		if percents[i] < percents[i-1] {
			t.Fatalf("percents not non-decreasing at i=%d: %v < %v (sample=%v)",
				i, percents[i], percents[i-1], percents)
		}
	}
	if percents[len(percents)-1] <= 0.0 {
		t.Errorf("last percent = %v; want > 0", percents[len(percents)-1])
	}
	if lastFrame <= 0 {
		t.Errorf("last frame = %d; want > 0", lastFrame)
	}
	t.Logf("progress: %d events, final percent=%.3f, final frame=%d",
		len(percents), percents[len(percents)-1], lastFrame)
}

// TestEncodeFormats is an end-to-end check that the resolved ffmpeg can
// actually produce the formats this library is expected to support. It
// generates a minimal one-second source clip, encodes it to each format using
// only the structured FfmpegArgs fields, and probes every output to confirm it
// holds a video stream of the requested codec at the source dimensions.
func TestEncodeFormats(t *testing.T) {
	f := newAndSkip(t)
	tmp := t.TempDir()
	inPath := filepath.Join(tmp, "in.mov")

	gen, err := f.Ffmpeg(&FfmpegArgs{RawArgs: []string{
		"-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=160x120:rate=10",
		"-an", "-c:v", "libx264", "-preset", "veryfast",
		"-f", "mov", inPath,
	}})
	if err != nil {
		t.Fatalf("Ffmpeg(generate): %v", err)
	}
	if err := gen.Wait(); err != nil {
		t.Fatalf("Ffmpeg(generate).Wait: %v", err)
	}

	tests := []struct {
		name      string
		codec     string // FfmpegArgs.VideoCodec
		output    string // output file name; the extension selects the container
		wantCodec string // codec_name ffprobe reports for the result
	}{
		{name: "h264", codec: "libx264", output: "h264.mp4", wantCodec: "h264"},
		{name: "h265", codec: "libx265", output: "h265.mp4", wantCodec: "hevc"},
		{name: "av1", codec: "libaom-av1", output: "av1.mkv", wantCodec: "av1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			outPath := filepath.Join(tmp, tc.output)
			run, err := f.Ffmpeg(&FfmpegArgs{
				Inputs:     []string{inPath},
				Output:     outPath,
				VideoCodec: tc.codec,
				Overwrite:  true,
			})
			if err != nil {
				t.Fatalf("Ffmpeg(%s): %v", tc.codec, err)
			}
			if err := run.Wait(); err != nil {
				t.Fatalf("Ffmpeg(%s).Wait: %v", tc.codec, err)
			}

			res, err := f.Ffprobe(&FfProbeArgs{Input: outPath, SelectStreams: "v:0"})
			if err != nil {
				t.Fatalf("Ffprobe(%s): %v", tc.output, err)
			}
			if len(res.GetStreams()) != 1 {
				t.Fatalf("%s has %d video streams; want 1", tc.output, len(res.GetStreams()))
			}
			vid := res.GetStreams()[0]
			if vid.GetCodecName() != tc.wantCodec {
				t.Errorf("%s codec = %q; want %q", tc.output, vid.GetCodecName(), tc.wantCodec)
			}
			if vid.GetWidth() != 160 || vid.GetHeight() != 120 {
				t.Errorf("%s is %dx%d; want 160x120", tc.output, vid.GetWidth(), vid.GetHeight())
			}
			if d := res.GetFormat().GetDuration(); d <= 0 || d > 5 {
				t.Errorf("%s duration = %v; want (0, 5]", tc.output, d)
			}
		})
	}
}
