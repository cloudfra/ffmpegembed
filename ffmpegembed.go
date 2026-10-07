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
//  2. the embedded static binary: by default the archive that ships with this
//     module (linux/amd64 and windows/amd64), or the one given to WithEmbed;
//  3. a binary installed on the system PATH;
//  4. a pinned static build, downloaded once into the user cache directory and
//     verified against the SHA-256 digests of a manifest: by default the one
//     compiled into this module, or the one given to WithManifest.
//
// Args.UseExternalIfAvailable moves the installed binary (3) ahead of the
// embedded one (2). Args.DisableDownload turns off (4); leaving it enabled
// accepts the license of the downloaded build (GPL-3.0-or-later).
//
// When bytes had to be written to disk (cases 1 and 2), they are extracted to
// a private temp directory that Close() removes. If Args.WorkDir is set, that
// directory is used instead and Close() leaves it in place.
//
// The embedded and downloaded builds are GPL-licensed; their license text is
// always written next to the binaries as "ffmpeg-LICENSE.txt".
//
// Both the archive and the manifest can be replaced with your own, built by
// the mkffmpegembed tool (cmd/mkffmpegembed):
//
//	//go:embed ffmpeg.tar.xz
//	var myFfmpeg []byte
//
//	ffexec, err := ffmpegembed.New(nil, ffmpegembed.WithEmbed(ffmpegembed.Archive(myFfmpeg)))
package ffmpegembed

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cloudfra/ffmpegembed/internal/archive"
	"github.com/cloudfra/ffmpegembed/internal/download"
	"github.com/cloudfra/ffmpegembed/internal/embedded"
	"github.com/cloudfra/ffmpegembed/internal/manifest"
	"github.com/cloudfra/ffmpegembed/proto"
)

type (
	// Args is an alias for the proto package's Args message.
	Args = proto.Args
	// FfProbeArgs is an alias for the proto package's FfProbeArgs message.
	FfProbeArgs = proto.FfProbeArgs
	// FfProbeResult is an alias for the proto package's FfProbeResult message.
	FfProbeResult = proto.FfProbeResult
	// FfProbeStream is an alias for the proto package's FfProbeStream message.
	FfProbeStream = proto.FfProbeStream
	// FfProbeFormat is an alias for the proto package's FfProbeFormat message.
	FfProbeFormat = proto.FfProbeFormat
	// FfmpegArgs is an alias for the proto package's FfmpegArgs message.
	FfmpegArgs = proto.FfmpegArgs
	// FfmpegResult is an alias for the proto package's FfmpegResult message.
	FfmpegResult = proto.FfmpegResult
	// Progress is an alias for the proto package's Progress message.
	Progress = proto.Progress
	// Event is an alias for the proto package's Event message.
	Event = proto.Event
	// FfmpegState is an alias for the proto package's FfmpegState type.
	FfmpegState = proto.FfmpegState
)

// downloadTimeout bounds the download of one binary when New has to fetch it.
const downloadTimeout = 10 * time.Minute

// userCacheDir locates the per-user cache directory downloads are kept in. It
// is a variable so tests can redirect it.
var userCacheDir = os.UserCacheDir

// Embed provides an embedded ffmpeg archive: the source New extracts ffmpeg
// and ffprobe from when the caller does not supply or prefer something else.
//
// Implement it to embed your own build of ffmpeg; Archive is the
// implementation for bytes you already hold, such as a go:embed variable.
type Embed interface {
	// Get returns a tar.xz archive holding the entries "ffmpeg", "ffprobe"
	// and "LICENSE" for the platform the program is running on, as built by
	// the mkffmpegembed tool. It returns nil when there is no archive for
	// this platform, in which case New moves on to an installed or downloaded
	// binary.
	Get() []byte
}

// Archive is an Embed backed by the bytes of a tar.xz archive.
type Archive []byte

// Get returns the archive.
func (a Archive) Get() []byte { return a }

// builtinEmbed is the Embed for the archive that ships with this module.
type builtinEmbed struct{}

func (builtinEmbed) Get() []byte { return embedded.Get() }

// Option customizes New.
type Option func(*options)

type options struct {
	embed    Embed
	manifest []byte
}

// WithEmbed makes New take the embedded ffmpeg and ffprobe from e instead of
// the archive that ships with this module. A nil e means there is no embedded
// binary at all.
func WithEmbed(e Embed) Option {
	return func(o *options) {
		if e == nil {
			e = Archive(nil)
		}
		o.embed = e
	}
}

// WithManifest makes New download from the release described by a JSON
// manifest, as written by "mkffmpegembed manifest", instead of the one this
// module is pinned to. It only matters when New gets as far as downloading.
func WithManifest(json []byte) Option {
	return func(o *options) { o.manifest = json }
}

// downloadBinary fetches the build of name the manifest pins for the current
// platform (or returns its cached path). An empty manifestJSON means the
// module's own manifest.
func downloadBinary(name string, manifestJSON []byte) (string, error) {
	m, err := manifest.Default()
	if len(manifestJSON) > 0 {
		m, err = manifest.Parse(manifestJSON)
	}
	if err != nil {
		return "", err
	}
	cache, err := userCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	f := &download.Fetcher{Manifest: m, CacheDir: filepath.Join(cache, "ffmpegembed")}
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()
	return f.Fetch(ctx, runtime.GOOS, runtime.GOARCH, name)
}

// Ffexec is a handle to a resolved ffmpeg/ffprobe, created by New. It is
// safe to call Ffprobe and Ffmpeg concurrently from multiple goroutines.
type Ffexec struct {
	ffmpegPath  string
	ffprobePath string
	cleanup     func() error
}

// New resolves the ffmpeg and ffprobe executables to use and returns a handle.
// See the package documentation for the resolution priority.
func New(args *Args, opts ...Option) (*Ffexec, error) {
	if args == nil {
		args = &Args{}
	}
	o := options{embed: builtinEmbed{}}
	for _, opt := range opts {
		opt(&o)
	}

	dir := strings.TrimSpace(args.GetWorkDir())
	createdDir := false

	ensureDir := func() (string, error) {
		if dir != "" {
			if err := os.MkdirAll(dir, 0o750); err != nil {
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
		if err := os.WriteFile(p, data, 0o700); err != nil { //nolint:gosec // G306: extract an executable (owner-only, 0700) into a private temp dir
			return "", err
		}
		return p, nil
	}

	// extractEmbedded unpacks the embedded archive into the work directory
	// the first time it is called. The archive holds both binaries and is
	// decoded as a whole, so one call serves ffmpeg and ffprobe; a binary
	// already resolved some other way (and so listed in resolved) is left
	// alone. The license text is always written alongside.
	resolved := map[string]bool{}
	extracted := false
	extractEmbedded := func(data []byte) (string, error) {
		d, err := ensureDir()
		if err != nil {
			return "", err
		}
		if extracted {
			return d, nil
		}
		err = archive.Extract(data, d, func(entry string) string {
			switch entry {
			case archive.Ffmpeg, archive.Ffprobe:
				if resolved[entry] {
					return ""
				}
				return exeName(entry)
			case archive.License:
				return download.LicenseFile
			}
			return ""
		})
		if err != nil {
			return "", fmt.Errorf("extract embedded ffmpeg: %w", err)
		}
		extracted = true
		return d, nil
	}

	resolve := func(name string, inline []byte) (string, error) {
		// 1. Inline override.
		if len(inline) > 0 {
			resolved[name] = true
			return writeToDir(name, inline)
		}
		// An installed binary goes ahead of the embedded one only on request.
		if args.GetUseExternalIfAvailable() {
			if p, err := exec.LookPath(name); err == nil {
				resolved[name] = true
				return p, nil
			}
		}
		// 2. Embedded.
		if data := o.embed.Get(); len(data) > 0 {
			d, err := extractEmbedded(data)
			if err != nil {
				return "", err
			}
			return filepath.Join(d, exeName(name)), nil
		}
		// 3. Installed.
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
		// 4. Download.
		const fix = "install ffmpeg so that %[1]s is on PATH, or supply it via Args.FfmpegBinary/FfprobeBinary"
		if args.GetDisableDownload() {
			return "", fmt.Errorf("%[1]s: %[2]w for %[3]s/%[4]s: none is embedded for this platform, none was found on PATH, and Args.DisableDownload is set; to fix: "+fix+", or allow the download",
				name, ErrNoBinary, runtime.GOOS, runtime.GOARCH)
		}
		p, err := downloadBinary(name, o.manifest)
		if err != nil {
			return "", fmt.Errorf("%[1]s: %[2]w for %[3]s/%[4]s: none is embedded for this platform, none was found on PATH, and downloading one failed: %[5]w; to fix: "+fix,
				name, ErrNoBinary, runtime.GOOS, runtime.GOARCH, err)
		}
		return p, nil
	}

	// removeIfCreated removes the auto-created temp directory if resolution is
	// about to fail, so it is not left behind (Close() would normally remove it,
	// but no *Ffexec is returned on an error path). It returns the removal error
	// so the caller can surface it alongside the resolution failure.
	removeIfCreated := func() error {
		if createdDir {
			return os.RemoveAll(dir)
		}
		return nil
	}

	ffmpeg, err := resolve("ffmpeg", args.GetFfmpegBinary())
	if err != nil {
		if rerr := removeIfCreated(); rerr != nil {
			err = errors.Join(err, rerr)
		}
		return nil, err
	}
	ffprobe, err := resolve("ffprobe", args.GetFfprobeBinary())
	if err != nil {
		if rerr := removeIfCreated(); rerr != nil {
			err = errors.Join(err, rerr)
		}
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

// FfmpegVersion runs the resolved ffmpeg with -version and returns the first
// line of its output (for example "ffmpeg version 7.1.1 ...", or a rolling
// master "N-12XXXX-<gitsha>-<date>" string). It is a cheap way to report which
// ffmpeg build is in use; it errors if the binary cannot be executed.
func (f *Ffexec) FfmpegVersion() (string, error) {
	out, err := exec.CommandContext(context.Background(), f.ffmpegPath, "-version").Output() //nolint:gosec // G204: fixed, trusted binary with a constant -version argument
	if err != nil {
		return "", runError("ffmpeg", f.ffmpegPath, err, "")
	}
	return firstLine(string(out)), nil
}

// FfprobeVersion is analogous to FfmpegVersion for ffprobe.
func (f *Ffexec) FfprobeVersion() (string, error) {
	out, err := exec.CommandContext(context.Background(), f.ffprobePath, "-version").Output() //nolint:gosec // G204: fixed, trusted binary with a constant -version argument
	if err != nil {
		return "", runError("ffprobe", f.ffprobePath, err, "")
	}
	return firstLine(string(out)), nil
}

// Version reports the version of the ffmpeg in use. It is a convenience alias
// for FfmpegVersion.
func (f *Ffexec) Version() (string, error) { return f.FfmpegVersion() }

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

// firstLine returns the first non-empty, trimmed line of s (or "" if there is
// none); it is used to extract the leading "ffmpeg version ..." line from the
// output of `ffmpeg -version`.
func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimRight(ln, "\r"); strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t)
		}
	}
	return ""
}
