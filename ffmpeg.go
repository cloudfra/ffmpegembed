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
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudfra/ffmpegembed/proto"
)

// ffmpegProgressFlags are injected into every Ffmpeg run so that progress can
// be parsed from stdout and stderr stays quiet (only real errors), matching the
// monitoring model in the original progress.go.
var ffmpegProgressFlags = []string{
	"-nostats",
	"-progress", "pipe:1",
	"-loglevel", "error",
}

// buildFfmpegArgs assembles the argument vector for an ffmpeg invocation.
//
// When FfmpegArgs.RawArgs is non-empty it is used verbatim (unstructured mode);
// otherwise the structured fields (inputs, output, codecs, crf, preset, bitrate,
// faststart, overwrite) are expanded into a conventional ffmpeg command line.
func buildFfmpegArgs(a *FfmpegArgs) []string {
	if a == nil {
		return nil
	}

	if len(a.GetRawArgs()) > 0 {
		return a.GetRawArgs()
	}

	out := make([]string, 0, 8+len(a.GetInputs())*2)
	if a.GetOverwrite() {
		out = append(out, "-y")
	}
	for _, in := range a.GetInputs() {
		out = append(out, "-i", in)
	}
	if c := a.GetVideoCodec(); c != "" {
		out = append(out, "-c:v", c)
	}
	if c := a.GetAudioCodec(); c != "" {
		out = append(out, "-c:a", c)
	}
	if n := a.GetCrf(); n > 0 {
		out = append(out, "-crf", strconv.FormatInt(int64(n), 10))
	}
	if p := a.GetPreset(); p != "" {
		out = append(out, "-preset", p)
	}
	if b := a.GetBitrate(); b != "" {
		out = append(out, "-b", b)
	}
	if a.GetFaststart() {
		out = append(out, "-movflags", "faststart")
	}
	if o := a.GetOutput(); o != "" {
		out = append(out, o)
	}
	return out
}

// FfmpegRun is a handle to a running ffmpeg process, returned by Ffexec.Ffmpeg.
//
// The zero value is not valid; use Ffexec.Ffmpeg to obtain one.
type FfmpegRun struct {
	c *Ffexec
	// The resolved ffmpeg path used to execute.
	cmd    *exec.Cmd
	stderr *bytes.Buffer

	events   chan *Event
	runDone  chan struct{}
	onUpdate func(*Event)
	finalErr error
	outFile  string
	mu       sync.RWMutex

	// Total input duration in microseconds, as pre-probed by Ffmpeg. Zero when
	// the input has no known duration (e.g. live / RTP) or when the probe
	// failed — in either case Progress.Percent is reported as 0.0.
	totalDurationUs int64
}

// Ffmpeg starts an ffmpeg run described by a and returns a handle. The
// process runs in the background; register OnUpdate to observe progress and
// call Wait to block until it finishes.
func (f *Ffexec) Ffmpeg(args *FfmpegArgs) (*FfmpegRun, error) {
	if args == nil {
		args = &FfmpegArgs{}
	}

	userArgs := buildFfmpegArgs(args)
	if len(userArgs) == 0 {
		return nil, fmt.Errorf("ffmpeg: no inputs or raw_args provided")
	}

	full := append(append([]string{}, ffmpegProgressFlags...), userArgs...)
	cmd := exec.CommandContext(context.Background(), f.ffmpegPath, full...) //nolint:gosec // G204: ffmpeg is a fixed, trusted binary; all args come from the library's own API

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: stdout pipe: %w", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	// Best-effort probe of the first input's total duration. On failure (e.g.
	// a live/RTP source, a URL that cannot be probed, or a probe timeout) the
	// probe returns 0 and Percent will be reported as 0.0 for every sample.
	durationUs := probeInputDurationUs(f, args.GetInputs())

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ffmpeg: start: %w", err)
	}

	run := &FfmpegRun{
		c:       f,
		cmd:     cmd,
		stderr:  stderr,
		events:  make(chan *Event, 256),
		runDone: make(chan struct{}),
		outFile: args.GetOutput(),

		totalDurationUs: durationUs,
	}

	go run.readProgress(stdout)
	go run.dispatch()
	return run, nil
}

// OnUpdate registers the callback invoked for every progress and terminal
// event. It may be called before or after Ffmpeg returned but before the run
// is complete; events emitted before it was set are still delivered.
func (r *FfmpegRun) OnUpdate(cb func(*Event)) {
	r.mu.Lock()
	r.onUpdate = cb
	r.mu.Unlock()
}

// Wait blocks until the ffmpeg process has finished and all events have been
// dispatched, then returns nil if it exited successfully, or an error
// (including a tail of stderr) on failure.
func (r *FfmpegRun) Wait() error {
	<-r.runDone
	return r.finalErr
}

// dispatch drains the event channel and forwards each event to the registered
// callback.
func (r *FfmpegRun) dispatch() {
	defer close(r.runDone)
	for ev := range r.events {
		r.mu.RLock()
		cb := r.onUpdate
		r.mu.RUnlock()
		if cb != nil {
			cb(ev)
		}
	}
}

// readProgress reads ffmpeg's progress stream, emitting a Progress event for
// each sample, then reaps the process and emits a terminal state event.
func (r *FfmpegRun) readProgress(stdout io.Reader) {
	defer close(r.events)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)

	sample := map[string]string{}
	progressCount := 0
	maxFps := float64(0)
	lastFrame := int64(0)
	emit := func() {
		if len(sample) == 0 {
			return
		}
		p := parseProgress(sample)
		p.Percent = clampFraction(p.OutTimeUs, r.totalDurationUs)
		progressCount++
		if p.Fps > maxFps {
			maxFps = p.Fps
		}
		if p.Frame > lastFrame {
			lastFrame = p.Frame
		}
		r.send(&Event{Kind: &proto.Event_Progress{Progress: p}})
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := line[:eq]
		val := line[eq+1:]
		if key == "progress" {
			emit()
			sample = map[string]string{}
			if val == "end" {
				break
			}
			continue
		}
		sample[key] = val
	}

	// Process has reached EOF; reap it.
	werr := r.cmd.Wait()
	succ := werr == nil

	result := &FfmpegResult{
		Success:        succ,
		Output:         r.outFile,
		ProgressEvents: int64(progressCount),
		MaxFps:         maxFps,
		LastFrame:      lastFrame,
	}
	if werr != nil {
		if ee, ok := werr.(*exec.ExitError); ok {
			result.ExitCode = int32(ee.ExitCode()) //nolint:gosec // G115: process exit codes fit in int32
		}
		if r.stderr != nil {
			result.Error = tail(r.stderr.String(), 4096)
		}
	}

	state := proto.FfmpegState_FFMPEG_STATE_COMPLETED
	if !succ {
		state = proto.FfmpegState_FFMPEG_STATE_FAILED
	}
	r.send(&Event{
		Kind:   &proto.Event_State{State: state},
		Result: result,
	})

	if !succ {
		r.finalErr = fmt.Errorf("ffmpeg exited with error: %w", werr)
	}
}

func (r *FfmpegRun) send(ev *Event) {
	select {
	case r.events <- ev:
	default:
		// Never block the reader goroutine on a slow consumer; drop the event.
	}
}

// clampFraction maps an observed output time to a 0.0..1.0 completion
// fraction. Returns 0.0 when the total is unknown (0 or negative) or when the
// observed time is 0 or negative — the caller can treat 0.0 as "undetermined".
// Values beyond 1.0 are clamped (happens when out_time exceeds duration due to
// container quirks).
func clampFraction(nowUs, totalUs int64) float64 {
	if nowUs <= 0 || totalUs <= 0 {
		return 0.0
	}
	pct := float64(nowUs) / float64(totalUs)
	if pct <= 0.0 {
		return 0.0
	}
	if pct >= 1.0 {
		return 1.0
	}
	return pct
}

// probeInputTimeout bounds the pre-launch probe so a slow or unresponsive
// input cannot stall Ffmpeg indefinitely.
const probeInputTimeout = 2 * time.Second

// probeInputDurationUs pre-probes the first input's total duration (in
// microseconds) so that FfmpegRun.readProgress can compute the Progress
// Percent field. Any failure — no inputs, unparseable probe, non-finite
// duration, probe timeout — yields 0, which is reported to users as an
// "undetermined" percent (0.0).
func probeInputDurationUs(f *Ffexec, inputs []string) int64 {
	if len(inputs) == 0 {
		return 0
	}
	in := inputs[0]
	if in == "" {
		return 0
	}
	if f == nil || f.ffprobePath == "" {
		return 0
	}

	type res struct {
		sec float64
	}
	out := make(chan res, 1)
	go func() {
		// Ffprobe does not take a context; we race against a timeout below
		// so a slow or hung probe cannot block Ffmpeg. The orphaned
		// ffprobe (if any) will terminate on its own and produce no harm.
		pr, err := f.Ffprobe(&FfProbeArgs{Input: in, ShowFormat: true, ShowStreams: false})
		if err != nil || pr == nil || pr.GetFormat() == nil {
			out <- res{}
			return
		}
		out <- res{sec: pr.GetFormat().GetDuration()}
	}()

	var sec float64
	select {
	case v := <-out:
		sec = v.sec
	case <-time.After(probeInputTimeout):
		return 0
	}

	if sec <= 0 || math.IsInf(sec, 0) || math.IsNaN(sec) {
		return 0
	}
	return int64(sec * 1e6)
}

// tail keeps the last n bytes of s, prefixing truncated output with an ellipsis.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
