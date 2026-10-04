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

// Package ffmpegembed embeds static ffmpeg and ffprobe binaries into a Go
// binary so that applications can run ffmpeg/ffprobe without requiring a
// system-installed copy.
//
// Typical usage:
//
//	ffexec, err := ffmpegembed.New(&ffmpegembed.Args{UseExternalIfAvailable: true})
//	if err != nil { return err }
//	defer ffexec.Close()
//
//	probe, err := ffexec.Ffprobe(&ffmpegembed.FfProbeArgs{Input: "movie.mp4"})
//	if err != nil { return err }
//
//	run, err := ffexec.Ffmpeg(&ffmpegembed.FfmpegArgs{
//		Inputs: []string{"movie.mp4"},
//		Output: "out.mp4",
//		VideoCodec: "libx264",
//	})
//	if err != nil { return err }
//	run.OnUpdate(func(ev *ffmpegembed.Event) { /* react to progress */ })
//	if err := run.Wait(); err != nil { return err }
//
// The ffmpeg/ffprobe used are resolved, in priority order, per binary:
//  1. an inline override supplied via Args (FfmpegBinary / FfprobeBinary);
//  2. a binary found on the system PATH (only when UseExternalIfAvailable);
//  3. the embedded binary linked into this build.
//
// When bytes had to be written to disk (cases 1 and 3), they are extracted to
// a private temp directory that Close() removes. If Args.WorkDir is set, that
// directory is used instead and Close() leaves it in place.
package ffmpegembed

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cloudfra/ffmpegembed/internal/embedded"
	"github.com/cloudfra/ffmpegembed/proto"
)

// Public type aliases over the generated protobuf messages, so that callers can
// write ffmpegembed.Args, ffmpegembed.FfmpegArgs, etc. without importing the
// proto package directly.
type (
	Args          = proto.Args
	FfProbeArgs   = proto.FfProbeArgs
	FfProbeResult = proto.FfProbeResult
	FfProbeStream = proto.FfProbeStream
	FfProbeFormat = proto.FfProbeFormat
	FfmpegArgs    = proto.FfmpegArgs
	FfmpegResult  = proto.FfmpegResult
	Progress      = proto.Progress
	Event         = proto.Event
	FfmpegState   = proto.FfmpegState
)

// Ffexec is a handle to a resolved ffmpeg/ffprobe, created by New. It is
// safe to call Ffprobe and Ffmpeg concurrently from multiple goroutines.
type Ffexec struct {
	ffmpegPath  string
	ffprobePath string
	cleanup     func() error
}

// New resolves the ffmpeg and ffprobe executables to use and returns a handle.
// See the package documentation for the resolution priority.
func New(args *Args) (*Ffexec, error) {
	if args == nil {
		args = &Args{}
	}

	dir := strings.TrimSpace(args.GetWorkDir())
	createdDir := false

	ensureDir := func() (string, error) {
		if dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", err
			}
			return dir, nil
		}
		d, err := os.MkdirTemp("", "ffmpegembed-")
		if err != nil {
			return "", err
		}
		dir, createdDir = d, true
		return dir, nil
	}

	writeToDir := func(name string, data []byte) (string, error) {
		d, err := ensureDir()
		if err != nil {
			return "", err
		}
		p := filepath.Join(d, exeName(name))
		if err := os.WriteFile(p, data, 0o755); err != nil {
			return "", err
		}
		return p, nil
	}

	resolve := func(name string, inline []byte) (string, error) {
		// 1. Inline override.
		if len(inline) > 0 {
			return writeToDir(name, inline)
		}
		// 2. External (only if requested) — a binary found on PATH.
		if args.GetUseExternalIfAvailable() {
			if p, err := exec.LookPath(name); err == nil {
				return p, nil
			}
		}
		// 3. Embedded.
		var b []byte
		switch name {
		case "ffmpeg":
			b = embedded.Ffmpeg()
		case "ffprobe":
			b = embedded.Ffprobe()
		default:
			return "", fmt.Errorf("unknown binary %q", name)
		}
		if len(b) == 0 {
			return "", fmt.Errorf("no %s available for %s/%s: set UseExternalIfAvailable, supply %s bytes inline, or build with an embedded binary",
				name, runtime.GOOS, runtime.GOARCH, name)
		}
		return writeToDir(name, b)
	}

	ffmpeg, err := resolve("ffmpeg", args.GetFfmpegBinary())
	if err != nil {
		return nil, err
	}
	ffprobe, err := resolve("ffprobe", args.GetFfprobeBinary())
	if err != nil {
		return nil, err
	}

	f := &Ffexec{ffmpegPath: ffmpeg, ffprobePath: ffprobe}
	if createdDir {
		f.cleanup = func() error { return os.RemoveAll(dir) }
	}
	return f, nil
}

// FfmpegPath returns the resolved path to the ffmpeg executable.
func (f *Ffexec) FfmpegPath() string { return f.ffmpegPath }

// FfprobePath returns the resolved path to the ffprobe executable.
func (f *Ffexec) FfprobePath() string { return f.ffprobePath }

// Close releases the resolved resources, removing any temp directory created
// during New. It is safe to call more than once and from deferred calls.
func (f *Ffexec) Close() error {
	if f.cleanup != nil {
		err := f.cleanup()
		f.cleanup = nil
		return err
	}
	return nil
}

// exeName appends the platform executable suffix (".exe" on Windows).
func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
