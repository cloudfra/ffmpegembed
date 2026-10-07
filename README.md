# ffmpegembed

<!-- markdownlint-disable-next-line MD033 -->
<img src="logo.png" alt="Logo" width="64" height="64" />

[![CI](https://github.com/cloudfra/ffmpegembed/actions/workflows/deploy.yaml/badge.svg)](https://github.com/cloudfra/ffmpegembed/actions/workflows/deploy.yaml) [![Go Reference](https://pkg.go.dev/badge/github.com/cloudfra/ffmpegembed.svg)](https://pkg.go.dev/github.com/cloudfra/ffmpegembed)

`ffmpegembed` is a Go library that embeds static `ffmpeg` and `ffprobe`
binaries into your application, so you can run `ffmpeg`/`ffprobe` commands
without requiring a copy installed on the host system.

## Features

- **Self-contained** — static `ffmpeg`/`ffprobe` binaries ship with the module
  and are linked in via `go:embed`; a plain `go get` is all it takes.
- **Works everywhere else too** — where no binary is embedded, an installed
  `ffmpeg` is used, and failing that a pinned build is downloaded and verified
  against a SHA-256 checksum (see [Binary resolution](#binary-resolution)).
- **Structured + unstructured input** — build commands from typed fields
  (inputs, output, codecs, …) *or* pass raw `ffmpeg`/`ffprobe` arguments
  verbatim. Output is always structured.
- **Progress monitoring** — parse `ffmpeg -progress pipe:1` and stream
  structured progress events to a callback while the job runs.
- **Multi-platform** — embedded for `linux/amd64` and `windows/amd64`;
  downloadable for the other linux and darwin targets (see
  [Platforms](#supported-platforms)).

## Install

```bash
go get github.com/cloudfra/ffmpegembed
```

That is the whole setup: the binaries are part of the module, so there is no
post-install step or build script to run.

## Binary resolution

`New` picks the `ffmpeg` and `ffprobe` to run in this order:

1. **Embedded** — the static binary linked into your build (`linux/amd64`,
   `windows/amd64`), decompressed from an xz archive when `New` is called.
2. **Installed** — an `ffmpeg`/`ffprobe` found on `PATH`.
3. **Downloaded** — a pinned static build, fetched once into the user cache
   directory (`os.UserCacheDir()/ffmpegembed/<version>/`) and used only if it
   matches the SHA-256 digest compiled into the library.

Two `Args` fields change this:

| Field                    | Effect                                                               |
| ------------------------ | -------------------------------------------------------------------- |
| `UseExternalIfAvailable` | Prefer an installed binary over the embedded one.                    |
| `DisableDownload`        | Never download; `New` returns `ErrNoBinary` if steps 1 and 2 fail.   |

The embedded and downloaded builds are licensed **GPL-3.0-or-later** (see
[License](#license)). Leaving `DisableDownload` unset accepts that license for
the downloaded build; set it to refuse both the download and the license.
`Args.FfmpegBinary`/`FfprobeBinary` override all of the above with bytes you
supply.

### Embedding your own ffmpeg

The embedded source is an interface with a single method, so you can replace
the build that ships with this module with your own:

```go
type Embed interface {
	// Get returns a tar.xz archive of ffmpeg, ffprobe and LICENSE for the
	// current platform, or nil when there is none.
	Get() []byte
}
```

Build an archive with [`mkffmpegembed`](#the-mkffmpegembed-tool), embed it, and
pass it to `New`. `Archive` is the ready-made `Embed` for bytes you hold:

```go
//go:embed ffmpeg.tar.xz
var myFfmpeg []byte

ffexec, err := ffmpegembed.New(nil, ffmpegembed.WithEmbed(ffmpegembed.Archive(myFfmpeg)))
```

`WithEmbed(nil)` turns the embedded step off. `WithManifest(json)` does the
same for the download step: it takes a manifest written by
`mkffmpegembed manifest` and downloads from that release instead of the pinned
one.

The license text of the embedded or downloaded build is always written next to
`ffmpeg` and `ffprobe`, as `ffmpeg-LICENSE.txt`.

Extracting the embedded binaries decodes the whole archive: expect `New` to
take a second or two and to use about 220 MB of memory while it runs.

## Quick start

```go
package main

import (
	"log"

	"github.com/cloudfra/ffmpegembed"
)

func transcode(in, out string) error {
	ffexec, err := ffmpegembed.New(&ffmpegembed.Args{UseExternalIfAvailable: true})
	if err != nil {
		return err
	}
	defer ffexec.Close()

	run, err := ffexec.Ffmpeg(&ffmpegembed.FfmpegArgs{
		Inputs:     []string{in},
		Output:     out,
		VideoCodec: "libx264",
		Crf:        23,
	})
	if err != nil {
		return err
	}

	// Optional: react to progress events as they arrive.
	run.OnUpdate(func(ev *ffmpegembed.Event) {
		if p := ev.GetProgress(); p != nil {
			log.Printf("frame=%d fps=%.2f speed=%.2fx", p.GetFrame(), p.GetFps(), p.GetSpeed())
		}
	})

	return run.Wait() // blocks until the process exits and all events are sent
}
```

### Probing

```go
probe, err := ffexec.Ffprobe(&ffmpegembed.FfProbeArgs{Input: "movie.mp4"})
// probe.GetFormat(), probe.GetStreams(), probe.GetRawJson()
```

### Two input modes

Every command accepts either a **structured** field (which maps to a conventional
`ffmpeg`/`ffprobe` flag) or **raw** arguments passed through verbatim
(`RawArgs`). When `RawArgs` is non-empty it takes full precedence. The result is
always the library's structured message (`FfmpegResult` / `FfProbeResult`).

| Want                          | Structured                                        | Raw                                                |
| ----------------------------- | ------------------------------------------------- | -------------------------------------------------- |
| video codec `libx264`         | `VideoCodec: "libx264"`                           | `RawArgs: []string{"-c:v", "libx264"}`             |
| probe streams                 | `ShowStreams: true`                               | `RawArgs: []string{"-show_streams"}`               |

## Progress monitoring

`ffmpeg` runs are launched with `-nostats -progress pipe:1 -loglevel error`.
Each `key=value` sample between the `progress=continue` and `progress=end`
markers becomes a structured `Progress` event delivered to the `OnUpdate`
callback. `Wait()` blocks until the process has exited and all events have been
dispatched, then reports success/failure (with a tail of stderr on failure).

## The `ffrun` showcase CLI

`cmd/ffrun` is a small command-line app demonstrating the library. It accepts
structured flags *or* raw arguments (anything after a `--` terminator), and
always prints the library's structured result as JSON.

```bash
# Build for the current platform.
go build -o ffrun ./cmd/ffrun

# Probe (structured flags).
./ffrun probe -input movie.mp4

# Encode (structured flags).
./ffrun run -input in.mov -output out.mp4 -video_codec libx264 -crf 23

# Raw pass-through: everything after `--` is passed to ffmpeg verbatim.
./ffrun run -- -i in.mov -c:v libx264 -crf 28 out.mp4
```

See `./ffrun help` for the full flag reference.

## The `mkffmpegembed` tool

`cmd/mkffmpegembed` builds the two inputs an embedded ffmpeg needs. Anyone can
use it to package their own build of ffmpeg.

```bash
go install github.com/cloudfra/ffmpegembed/cmd/mkffmpegembed@latest
```

**An archive** is a `.tar.xz` holding `ffmpeg`, `ffprobe` and the `LICENSE`
they are distributed under:

```bash
mkffmpegembed archive -ffmpeg ./ffmpeg -ffprobe ./ffprobe -license ./LICENSE -o ffmpeg.tar.xz
```

Both binaries go in one archive because they share most of their code: with a
dictionary larger than one binary, xz stores that code once. Compression is
done by the `xz` program at its highest setting (`-9e`), so `xz` must be
installed; archives are decompressed in pure Go.

**A manifest** is a JSON file that pins a release: where each platform's files
are downloaded from, and the SHA-256 digests they must have.

```bash
mkffmpegembed manifest -version b6.1.1 -license GPL-3.0-or-later \
    -base-url 'https://github.com/eugeneware/ffmpeg-static/releases/download/{version}' \
    -platform linux_amd64=ffmpeg-linux-x64.gz,ffprobe-linux-x64.gz,linux-x64.LICENSE.gz \
    -platform windows_amd64=ffmpeg-win32-x64.gz,ffprobe-win32-x64.gz,win32-x64.LICENSE.gz \
    -o manifest.json
```

Each `-platform` names the ffmpeg, ffprobe and license files of one
`<goos>_<goarch>`; every file must be a single gzip-compressed file under the
base URL. The tool downloads each one and records its digests:

```json
{
  "version": "b6.1.1",
  "license": "GPL-3.0-or-later",
  "base_url": "https://github.com/eugeneware/ffmpeg-static/releases/download/b6.1.1",
  "platforms": {
    "linux_amd64": {
      "ffmpeg": {
        "file": "ffmpeg-linux-x64.gz",
        "sha256": "bfe8a8fc511530457b528c48d77b5737527b504a3797a9bc4866aeca69c2dffa",
        "binary_sha256": "e7e7fb30477f717e6f55f9180a70386c62677ef8a4d4d1a5d948f4098aa3eb99"
      }
    }
  }
}
```

To move an existing manifest to another release, start from it; the platforms
are kept and every digest is recomputed:

```bash
mkffmpegembed manifest -from manifest.json -version b7.0 -o manifest.json
```

An archive can also be built straight from a manifest, which downloads the
platform's files and verifies them first:

```bash
mkffmpegembed archive -manifest manifest.json -platform linux_amd64 -o ffmpeg.tar.xz
```

## Supported platforms

| OS      | Arch  | Embedded | Downloadable | Release asset (`ffmpeg-`/`ffprobe-`…) |
| ------- | ----- | -------- | ------------ | ------------------------------------- |
| linux   | amd64 | yes      | yes          | `linux-x64.gz`                        |
| windows | amd64 | yes      | yes          | `win32-x64.gz`                        |
| linux   | arm64 | no       | yes          | `linux-arm64.gz`                      |
| linux   | 386   | no       | yes          | `linux-ia32.gz`                       |
| linux   | arm   | no       | yes          | `linux-arm.gz`                        |
| darwin  | amd64 | no       | yes          | `darwin-x64.gz`                       |
| darwin  | arm64 | no       | yes          | `darwin-arm64.gz`                     |

Any other platform works with an installed `ffmpeg` or an inline binary.

Every build comes from one pinned release of
[eugeneware/ffmpeg-static](https://github.com/eugeneware/ffmpeg-static)
(currently `b6.1.1`), one of the static-build hosts linked from
[ffmpeg.org's download page](https://ffmpeg.org/download.html). The Linux
builds are fully static, so they also run on musl-based and libc-free images
such as Alpine and `distroless/static`.

Each embedded platform is one archive, `internal/embedded/bin/<os>_<arch>/ffmpeg.tar.xz`,
holding `ffmpeg`, `ffprobe` and their license. It is compressed with xz at its
highest level, with a dictionary larger than one binary so that the code the
two programs share is stored once: about 23 MB per platform instead of 160 MB
uncompressed. It is decoded by the pure-Go
[ulikunitz/xz](https://github.com/ulikunitz/xz).

Only two platforms are embedded to keep the module small (46 MB for both); a Go
module may not exceed 500 MB. The archives are plain git blobs rather than Git
LFS objects: the Go module proxy does not resolve LFS pointers, so `go get`
would receive pointer files instead of binaries.

### Updating the pinned ffmpeg release

`internal/manifest/default.json` maps the pinned version to each platform's
download URL and SHA-256 digests; the embedded archives are built from the same
files. To move to another release:

```bash
make ffembed-update FF_VERSION=b6.1.1
```

This runs [`mkffmpegembed`](#the-mkffmpegembed-tool) to re-download every
platform's build, rewrite the manifest, and rebuild the committed archives (it
needs `xz`). A test fails if the contents of the archives and the manifest ever
disagree.

## Building

| Target                    | Description                                                        |
| ------------------------- | ------------------------------------------------------------------ |
| `make` / `make all`       | Build the `ffrun` binary for the host                              |
| `make ffembed-update`     | Pin a new ffmpeg release (manifest + embedded binaries)            |
| `make test`               | Run the test suite                                                  |
| `make protos`             | Regenerate `proto/*.pb.go` from `proto/*.proto` (needs protoc)      |
| `make release-binaries`   | Build release artifacts for all platforms                           |
| `make images`             | Build multi-arch Docker images for apps under `cmd/`                |
| `make clean`              | Remove build outputs                                                |

### Reporting the ffmpeg version

`Ffexec` reports which build is actually in use:

```go
v,  _ := ffexec.Version()          // e.g. "ffmpeg version 7.0.2-static …"
pv, _ := ffexec.FfprobeVersion()   // the matching ffprobe line
```

The `ffrun` CLI exposes the same: `ffrun version`.

## Project layout

```text
.
├── ffmpegembed.go     Package: Ffexec, New/Close, binary resolution
├── ffmpeg.go          FfmpegRun, progress reader, event dispatch
├── ffprobe.go         Ffprobe structured parse (format + streams)
├── progress.go        Parse ffmpeg -progress samples → proto.Progress
├── proto/             Protobuf messages (ffrun.proto + checked-in .pb.go)
├── internal/embedded/ go:embed per-platform archives (committed, tar.xz)
├── internal/archive/  The tar.xz archive format: create + extract
├── internal/manifest/ The JSON release manifest (+ the pinned default)
├── internal/download/ Verified downloader for the pinned release
├── cmd/ffrun/         Showcase CLI
└── cmd/mkffmpegembed/ Builds archives and manifests
```

## License

The library's own source code is licensed under the
[Apache License, Version 2.0](LICENSE).

The `ffmpeg` and `ffprobe` binaries it embeds and downloads are separate
programs built with GPL components and are licensed under the
**GNU General Public License, version 3 or later**; the license text is in
`internal/embedded/bin/<os>_<arch>/LICENSE`, and is written next to the
binaries wherever they are extracted or downloaded. A program built with this module
for `linux/amd64` or `windows/amd64` contains those binaries, so distributing it
means distributing GPL software and meeting the GPL's terms for it (such as
offering the corresponding ffmpeg source).
