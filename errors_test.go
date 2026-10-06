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
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestErrInvalidArgs verifies that structurally invalid arguments surface the
// ErrInvalidArgs sentinel from both Ffprobe and Ffmpeg, so callers can detect
// this common failure with errors.Is.
func TestErrInvalidArgs(t *testing.T) {
	f := &Ffexec{} // no resolved binary needed: both calls fail before touching one

	if _, err := f.Ffprobe(nil); err == nil {
		t.Fatal("Ffprobe(nil) expected an error")
	} else if !errors.Is(err, ErrInvalidArgs) {
		t.Errorf("Ffprobe(nil): want errors.Is(ErrInvalidArgs), got %v", err)
	}

	if _, err := f.Ffmpeg(nil); err == nil {
		t.Fatal("Ffmpeg(nil) expected an error")
	} else if !errors.Is(err, ErrInvalidArgs) {
		t.Errorf("Ffmpeg(nil): want errors.Is(ErrInvalidArgs), got %v", err)
	}

	// Non-nil args with no inputs/raw_args must hit the same sentinel.
	if _, err := f.Ffmpeg(&FfmpegArgs{}); !errors.Is(err, ErrInvalidArgs) {
		t.Errorf("Ffmpeg(&FfmpegArgs{}): want errors.Is(ErrInvalidArgs), got %v", err)
	}
}

// TestErrFailedToStart verifies that a binary that cannot be started is
// surfaced as ErrFailedToStart and recoverable as a *RunError that has not
// started, with no exit code.
func TestErrFailedToStart(t *testing.T) {
	// A path inside a real directory but pointing at a file that does not
	// exist: exec fails to start, producing a non-ExitError.
	bogus := filepath.Join(t.TempDir(), "definitely-not-here")
	f := &Ffexec{ffprobePath: bogus}

	_, err := f.Ffprobe(&FfProbeArgs{Input: "movie.mp4"})
	if err == nil {
		t.Fatal("Ffprobe(bogus) expected an error")
	}
	if !errors.Is(err, ErrFailedToStart) {
		t.Errorf("want errors.Is(ErrFailedToStart), got %v", err)
	}
	if errors.Is(err, ErrRunFailed) {
		t.Error("a never-started process should not match ErrRunFailed")
	}

	var re *RunError
	if !errors.As(err, &re) {
		t.Fatalf("errors.As(*RunError) failed: %v", err)
	}
	if re.started {
		t.Error("bogus ffprobe should not be marked as started")
	}
	if re.ExitCode != -1 {
		t.Errorf("never-started process: ExitCode = %d, want -1", re.ExitCode)
	}
	if re.Name != "ffprobe" {
		t.Errorf("RunError.Name = %q, want %q", re.Name, "ffprobe")
	}
}

// TestRunErrorIsUnwrap exercises the typed error contract directly, independent
// of the OS, so it is deterministic on every platform.
func TestRunErrorIsUnwrap(t *testing.T) {
	inner := errors.New("underlying")

	started := &RunError{Name: "ffmpeg", Binary: "/x", ExitCode: 2, Err: inner, started: true}
	if !errors.Is(started, ErrRunFailed) {
		t.Error("started RunError should match ErrRunFailed")
	}
	if errors.Is(started, ErrFailedToStart) {
		t.Error("started RunError should not match ErrFailedToStart")
	}
	if !errors.Is(started, inner) {
		t.Error("started RunError should unwrap to the underlying error")
	}

	notStarted := startError("ffmpeg", "/x", inner)
	if !errors.Is(notStarted, ErrFailedToStart) {
		t.Error("not-started RunError should match ErrFailedToStart")
	}
	if errors.Is(notStarted, ErrRunFailed) {
		t.Error("not-started RunError should not match ErrRunFailed")
	}
	if !errors.Is(notStarted, inner) {
		t.Error("not-started RunError should unwrap to the underlying error")
	}
}

// TestExitErrorCapturesCode verifies that a process which started and then
// exited non-zero is recoverable as a *RunError carrying the exit code and
// matching ErrRunFailed. Skipped when the `false` command is not present (it is
// part of POSIX coreutils and the Windows toolchain).
func TestExitErrorCapturesCode(t *testing.T) {
	if _, err := exec.LookPath("false"); err != nil {
		t.Skip("skipping: `false` command not available")
	}

	// `false` always exits with status 1, yielding a real *exec.ExitError.
	runErr := exec.CommandContext(context.Background(), "false").Run()
	ee, ok := runErr.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T", runErr)
	}
	got := exitError("ffmpeg", "/bin/false", runErr, "some stderr")

	if !errors.Is(got, ErrRunFailed) {
		t.Error("exit-error RunError should match ErrRunFailed")
	}
	var re *RunError
	if !errors.As(got, &re) {
		t.Fatalf("errors.As(*RunError) failed: %v", got)
	}
	if re.ExitCode != int32(ee.ExitCode()) { //nolint:gosec // G115: process exit codes fit in int32
		t.Errorf("RunError.ExitCode = %d, want %d", re.ExitCode, ee.ExitCode())
	}
	if re.ExitCode != 1 {
		t.Errorf("`false` should exit 1; got RunError.ExitCode = %d", re.ExitCode)
	}
	if re.StdErr != "some stderr" {
		t.Errorf("RunError.StdErr = %q, want %q", re.StdErr, "some stderr")
	}
}

// TestSentinelsDistinct sanity-checks that the predefined errors are distinct,
// non-nil sentinels rather than aliases or nils.
func TestSentinelsDistinct(t *testing.T) {
	sentinels := map[string]error{
		"ErrNoBinary":      ErrNoBinary,
		"ErrInvalidArgs":   ErrInvalidArgs,
		"ErrFailedToStart": ErrFailedToStart,
		"ErrRunFailed":     ErrRunFailed,
		"ErrCancelled":     ErrCancelled,
	}
	seen := map[error]string{}
	for name, e := range sentinels {
		if e == nil {
			t.Fatalf("%s is nil", name)
		}
		if prev, dup := seen[e]; dup {
			t.Errorf("%s and %s are the same sentinel", name, prev)
		}
		seen[e] = name
	}
}

// TestCancelErrorIs verifies the typed-error contract for a cancelled run: it
// matches ErrCancelled (not ErrRunFailed or ErrFailedToStart), still unwraps
// to the underlying error, and carries its structured fields. Exercised
// directly so it is deterministic on every platform.
func TestCancelErrorIs(t *testing.T) {
	inner := errors.New("signal: killed")
	got := cancelError("ffmpeg", "/x", inner, "killing")

	if !errors.Is(got, ErrCancelled) {
		t.Error("cancelError should match ErrCancelled")
	}
	if errors.Is(got, ErrRunFailed) {
		t.Error("cancelError should not match ErrRunFailed")
	}
	if errors.Is(got, ErrFailedToStart) {
		t.Error("cancelError should not match ErrFailedToStart")
	}
	if !errors.Is(got, inner) {
		t.Error("cancelError should unwrap to the underlying error")
	}

	var re *RunError
	if !errors.As(got, &re) {
		t.Fatalf("errors.As(*RunError) failed: %v", got)
	}
	if !re.cancelled {
		t.Error("RunError.cancelled should be true")
	}
	if re.Name != "ffmpeg" {
		t.Errorf("RunError.Name = %q, want %q", re.Name, "ffmpeg")
	}
	if re.StdErr != "killing" {
		t.Errorf("RunError.StdErr = %q, want %q", re.StdErr, "killing")
	}
}

// TestCancelRunEndToEnd starts a deliberately long, slow ffmpeg encode and
// cancels it mid-flight, asserting that Wait returns an error matching
// ErrCancelled (and not ErrRunFailed). We encode 30s of 720p at the slowest
// preset straight from a lavfi source so the process is still running well
// after the cancel sleep, on every machine fast enough to run this test at all.
// Skipped when no ffmpeg/ffprobe binary is resolvable.
func TestCancelRunEndToEnd(t *testing.T) {
	f := newAndSkip(t)
	tmp := t.TempDir()
	outPath := filepath.Join(tmp, "out.mp4")

	run, err := f.Ffmpeg(&FfmpegArgs{RawArgs: []string{
		"-y",
		"-f", "lavfi", "-i", "testsrc=duration=30:size=1280x720:rate=25",
		"-an", "-c:v", "libx264", "-preset", "veryslow", "-crf", "28",
		"-f", "mp4", outPath,
	}})
	if err != nil {
		t.Fatalf("Ffmpeg(encode): %v", err)
	}

	// Let the encoder do a little work so it is comfortably past startup, then
	// stop it. A second call verifies idempotence (Cancel must not panic or
	// double-error).
	time.Sleep(250 * time.Millisecond)
	run.Cancel()
	run.Cancel()

	err = run.Wait()
	if err == nil {
		t.Fatal("Wait() after Cancel() should return an error")
	}
	if !errors.Is(err, ErrCancelled) {
		t.Errorf("want errors.Is(ErrCancelled); got %v", err)
	}
	if errors.Is(err, ErrRunFailed) {
		t.Error("cancelled run should not also match ErrRunFailed")
	}

	var re *RunError
	if !errors.As(err, &re) {
		t.Fatalf("errors.As(*RunError) failed: %v", err)
	}
	if !re.cancelled {
		t.Error("RunError.cancelled should be true after Cancel()")
	}
	t.Logf("err=%v name=%s exit=%d cancelled=%v", err, re.Name, re.ExitCode, re.cancelled)
}

// TestCancelAfterComplete verifies that Cancel() is a harmless no-op once the
// run has already finished — it must not introduce an error or panic.
func TestCancelAfterComplete(t *testing.T) {
	f := newAndSkip(t)
	tmp := t.TempDir()
	inPath := filepath.Join(tmp, "in.mov")
	outPath := filepath.Join(tmp, "out.mp4")

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

	run, err := f.Ffmpeg(&FfmpegArgs{
		Inputs:     []string{inPath},
		Output:     outPath,
		VideoCodec: "libx264",
		Overwrite:  true,
	})
	if err != nil {
		t.Fatalf("Ffmpeg(encode): %v", err)
	}
	if err := run.Wait(); err != nil {
		t.Fatalf("Ffmpeg(encode).Wait: %v", err)
	}

	// The run already finished successfully; Cancelling it now is a no-op.
	run.Cancel()
	if err := run.Wait(); err != nil {
		t.Errorf("second Wait() after Cancel of a completed run: %v", err)
	}
}

// TestRunResultGetter verifies the terminal result is stored on the run and
// gettable via Result() once Wait has returned, without needing to capture it
// from the OnUpdate event stream.
func TestRunResultGetter(t *testing.T) {
	f := newAndSkip(t)
	tmp := t.TempDir()
	inPath := filepath.Join(tmp, "in.mov")
	outPath := filepath.Join(tmp, "out.mp4")

	gen, err := f.Ffmpeg(&FfmpegArgs{RawArgs: []string{
		"-y", "-f", "lavfi", "-i", "testsrc=duration=1:size=160x120:rate=10",
		"-an", "-c:v", "libx264", "-preset", "veryfast", "-f", "mov", inPath,
	}})
	if err != nil {
		t.Fatalf("Ffmpeg(generate): %v", err)
	}
	if err := gen.Wait(); err != nil {
		t.Fatalf("Ffmpeg(generate).Wait: %v", err)
	}

	run, err := f.Ffmpeg(&FfmpegArgs{
		Inputs:     []string{inPath},
		Output:     outPath,
		VideoCodec: "libx264",
		Overwrite:  true,
	})
	if err != nil {
		t.Fatalf("Ffmpeg(encode): %v", err)
	}
	if err := run.Wait(); err != nil {
		t.Fatalf("Ffmpeg(encode).Wait: %v", err)
	}

	res := run.Result()
	if res == nil {
		t.Fatal("Result() after Wait should be non-nil")
	}
	if !res.GetSuccess() {
		t.Error("Result().Success should be true for a successful encode")
	}
	if res.GetOutput() != outPath {
		t.Errorf("Result().Output = %q, want %q", res.GetOutput(), outPath)
	}
}

// TestStartupHint verifies that the Windows STATUS_DLL_NOT_FOUND exit status
// (0xC0000135) is recognized as a loader failure with steps to fix it, and
// that ordinary exit codes and other platforms get no hint.
func TestStartupHint(t *testing.T) {
	hint := startupHint("windows", statusDLLNotFound)
	for _, want := range []string{"avicap32.dll", "msvfw32.dll", "ServerCore.AppCompatibility"} {
		if !strings.Contains(hint, want) {
			t.Errorf("startupHint(windows, 0xC0000135) = %q; want it to mention %q", hint, want)
		}
	}
	if got := startupHint("windows", 1); got != "" {
		t.Errorf("startupHint(windows, 1) = %q; want empty", got)
	}
	if got := startupHint("linux", statusDLLNotFound); got != "" {
		t.Errorf("startupHint(linux, 0xC0000135) = %q; want empty", got)
	}
}

// TestRunErrorHint verifies that a RunError's Hint is appended to its message
// after the raw underlying error, so both are visible to the caller.
func TestRunErrorHint(t *testing.T) {
	raw := errors.New("exit status 0xc0000135")
	err := &RunError{Name: "ffmpeg", ExitCode: statusDLLNotFound, Err: raw, Hint: "install the thing"}
	msg := err.Error()
	if !strings.Contains(msg, raw.Error()) || !strings.HasSuffix(msg, "; to fix: install the thing") {
		t.Errorf("Error() = %q; want the raw error followed by the hint", msg)
	}
	if !errors.Is(err, ErrFailedToStart) || !errors.Is(err, raw) {
		t.Errorf("hinted start failure should match ErrFailedToStart and unwrap to the raw error")
	}
}
