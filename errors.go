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
	"errors"
	"fmt"
	"os/exec"
)

// Predefined sentinel errors let callers detect common failure modes with
// errors.Is without string-matching on the message. Errors returned by this
// package wrap these values, so they are always recoverable:
//
//	if errors.Is(err, ffmpegembed.ErrFailedToStart) {
//		// the binary could not run at all (e.g. a missing dependency)
//	}
//
//	var re *ffmpegembed.RunError
//	if errors.As(err, &re) {
//		fmt.Println(re.Name, re.ExitCode, re.StdErr)
//	}
var (
	// ErrNoBinary is returned (wrapped) by New when no ffmpeg or ffprobe binary
	// can be resolved for the current platform: no inline bytes supplied, no
	// on-PATH binary found or permitted by UseExternalIfAvailable, and no
	// embedded binary present.
	ErrNoBinary = errors.New("ffmpegembed: no ffmpeg/ffprobe binary available")

	// ErrInvalidArgs is returned (wrapped) when the arguments provided are
	// structurally invalid — for example Ffprobe with no input, or Ffmpeg with
	// neither inputs nor raw_args.
	ErrInvalidArgs = errors.New("ffmpegembed: invalid or missing arguments")

	// ErrFailedToStart is returned (wrapped) when the resolved binary could not
	// be started at all, so the process never ran. It is distinct from
	// ErrRunFailed: a binary that starts and then exits non-zero matches
	// ErrRunFailed instead. A common cause is a missing dynamic-link
	// dependency at startup (on Windows the loader aborts before the process
	// begins, reported as exit status 0xC0000135 / STATUS_DLL_NOT_FOUND, e.g.
	// a missing VC++ runtime).
	ErrFailedToStart = errors.New("ffmpegembed: failed to start binary")

	// ErrRunFailed is returned (wrapped, as a *RunError) when an ffmpeg/ffprobe
	// process started but did not complete successfully — a non-zero exit code,
	// or termination by a signal.
	ErrRunFailed = errors.New("ffmpegembed: process started but did not complete successfully")

	// ErrCancelled is returned (wrapped, as a *RunError) when a running Ffmpeg
	// was explicitly stopped via FfmpegRun.Cancel. It is distinct from
	// ErrRunFailed so callers can tell "we stopped it" apart from "it failed
	// on its own", without parsing the exit code or stderr.
	ErrCancelled = errors.New("ffmpegembed: run cancelled")
)

// RunError describes a single failed ffmpeg or ffprobe invocation. It is safe
// for concurrent use. Callers can recover its structured fields with
// errors.As and react to them programmatically — for example inspecting
// ExitCode to distinguish a missing-dependency start failure from an
// out-of-range-codec failure — rather than parsing the message.
type RunError struct {
	// Name is the logical name of the binary that failed ("ffmpeg" or
	// "ffprobe").
	Name string

	// Binary is the resolved path of the executable that was run.
	Binary string

	// ExitCode is the process exit code, or -1 when it could not be obtained
	// because the process was killed by a signal or never started.
	ExitCode int32

	// StdErr is a bounded tail of the process's stderr, when available.
	StdErr string

	// Err is the underlying error reported by os/exec, if any — for example
	// *exec.ExitError for a non-zero exit, or a *fs.PathError for a failed
	// start.
	Err error

	// started is true when the process began and then failed (ErrRunFailed);
	// false when it could not be started at all (ErrFailedToStart).
	started bool

	// cancelled is true when the run was stopped by FfmpegRun.Cancel; it takes
	// precedence in Is so a cancelled run does not leak to ErrRunFailed.
	cancelled bool
}

// Error implements the error interface.
func (e *RunError) Error() string {
	s := "ffmpegembed: "
	switch {
	case e.cancelled:
		s += fmt.Sprintf("%s was cancelled", e.Name)
	case e.started:
		s += fmt.Sprintf("%s exited with code %d", e.Name, e.ExitCode)
	default:
		s += fmt.Sprintf("%s could not be started", e.Name)
	}
	if e.Err != nil {
		s += fmt.Sprintf(": %v", e.Err)
	}
	if e.StdErr != "" {
		s += "; stderr: " + e.StdErr
	}
	return s
}

// Unwrap returns the underlying os/exec error, if any, so errors.Is and
// errors.As can see through this RunError to the concrete error (e.g.
// *exec.ExitError).
func (e *RunError) Unwrap() error { return e.Err }

// Is reports whether this RunError should be treated as the given sentinel:
// ErrCancelled when the run was stopped via Cancel, otherwise ErrRunFailed
// for a process that started and then failed, and ErrFailedToStart for one
// that never started. This is what makes errors.Is(err, ffmpegembed.Err*)
// work on the errors returned by Ffmpeg/Ffprobe.
func (e *RunError) Is(target error) bool {
	if e.cancelled {
		return target == ErrCancelled
	}
	if e.started {
		return target == ErrRunFailed
	}
	return target == ErrFailedToStart
}

// startError wraps an os/exec error from a failed start (cmd.Start, or a
// run() in which the process never ran) as a *RunError matching ErrFailedToStart.
func startError(name, path string, err error) error {
	return &RunError{Name: name, Binary: path, ExitCode: -1, Err: err, started: false}
}

// exitError wraps an os/exec error from a process that did start but failed to
// complete (cmd.Wait or cmd.Run), producing a *RunError matching ErrRunFailed
// and carrying the exit code when one was reported.
func exitError(name, path string, err error, stderr string) error {
	r := &RunError{Name: name, Binary: path, ExitCode: -1, StdErr: tail(stderr, 4096), Err: err, started: true}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.ExitCode = int32(ee.ExitCode()) //nolint:gosec // G115: OS exit statuses fit in int32
	}
	return r
}

// cancelError wraps an os/exec error from a run stopped by FfmpegRun.Cancel as
// a *RunError matching ErrCancelled, carrying the exit code when one was
// reported. It mirrors exitError but flags the interruption as intentional.
func cancelError(name, path string, err error, stderr string) error {
	r := &RunError{Name: name, Binary: path, ExitCode: -1, StdErr: tail(stderr, 4096), Err: err, cancelled: true}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.ExitCode = int32(ee.ExitCode()) //nolint:gosec // G115: OS exit statuses fit in int32
	}
	return r
}

// runError wraps an os/exec error from a single-shot command (cmd.Run /
// cmd.Output) as a *RunError, classifying it as a start failure when the
// process never ran or a non-zero exit otherwise.
func runError(name, path string, err error, stderr string) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return exitError(name, path, err, stderr)
	}
	return &RunError{Name: name, Binary: path, ExitCode: -1, StdErr: tail(stderr, 4096), Err: err, started: false}
}
