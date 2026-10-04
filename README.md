# ffmpegembed

<!-- markdownlint-disable-next-line MD033 -->
<img src="logo.png" alt="Logo" width="64" height="64" />

[![CI](https://github.com/cloudfra/ffmpegembed/actions/workflows/deploy.yaml/badge.svg)](https://github.com/cloudfra/ffmpegembed/actions/workflows/deploy.yaml) [![Go Reference](https://pkg.go.dev/badge/github.com/cloudfra/ffmpegembed.svg)](https://pkg.go.dev/github.com/cloudfra/ffmpegembed)

`ffmpegembed` is a Go library that embeds static `ffmpeg` and `ffprobe`
binaries into your application, so you can run `ffmpeg`/`ffprobe` commands
without requiring a copy installed on the host system.

## Features

- **Self-contained** — static `ffmpeg`/`ffprobe` binaries are linked in via
  `go:embed`; no runtime download or system dependency is required.
- **Optional external binary** — resolve a binary already on `PATH` first when
  you opt in, and only fall back to the embedded one otherwise.
- **Structured + unstructured input** — build commands from typed fields
  (inputs, output, codecs, …) *or* pass raw `ffmpeg`/`ffprobe` arguments
  verbatim. Output is always structured.
- **Progress monitoring** — parse `ffmpeg -progress pipe:1` and stream
  structured progress events to a callback while the job runs.
- **Multi-platform** — shipped for `linux/{amd64,arm64}` and
  `windows/{amd64,arm64}` (see [Platforms](#supported-platforms)).

## Install

```bash
go get github.com/cloudfra/ffmpegembed
```

For a **consumer** binary the `ffmpeg`/`ffprobe` executables are resolved from
`PATH` (with `UseExternalIfAvailable`) or supplied inline — no embedded binary
is required on the consuming platform to just *link* the library. To embed a
binary for your target platform at build time, run `make ffembed` (or
`make ffembed-host`) before building.

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
# Build for the current platform (pulses the embedded hosts binary in first).
go build -o ffrun ./cmd/ffrun

# Probe (structured flags).
./ffrun probe -input movie.mp4

# Encode (structured flags).
./ffrun run -input in.mov -output out.mp4 -video_codec libx264 -crf 23

# Raw pass-through: everything after `--` is passed to ffmpeg verbatim.
./ffrun run -- -i in.mov -c:v libx264 -crf 28 out.mp4
```

See `./ffrun help` for the full flag reference.

## Supported platforms

| OS      | Arch  | Binary                       | Source asset (BtbN)            |
| ------- | ----- | ---------------------------- | ------------------------------ |
| linux   | amd64 | `ffmpeg`, `ffprobe`          | `…-linux64-gpl-<ver>.tar.xz`  |
| linux   | arm64 | `ffmpeg`, `ffprobe`          | `…-linuxarm64-gpl-<ver>.tar.xz` |
| windows | amd64 | `ffmpeg.exe`, `ffprobe.exe`  | `…-win64-gpl-<ver>.zip`       |
| windows | arm64 | `ffmpeg.exe`, `ffprobe.exe`  | `…-winarm64-gpl-<ver>.zip`    |

The binaries are pulled from
[BtbN/FFmpeg-Builds](https://github.com/BtbN/FFmpeg-Builds) at build time and
embedded under `internal/embedded/bin/<os>_<arch>/` — that directory is in
`.gitignore`; only the Go sources are checked in.

## Building

| Target                    | Description                                                        |
| ------------------------- | ------------------------------------------------------------------ |
| `make` / `make all`       | Build the `ffrun` binary for the host (pulls the host's binary first) |
| `make ffembed`            | Fetch + embed `ffmpeg`/`ffprobe` for all four target platforms      |
| `make ffembed-host`       | Fetch + embed for the current host platform only                    |
| `make ffembed-clean`      | Remove the embedded binary blobs (re-download on next build)        |
| `make ffembed-list`       | Print the resolved asset URLs                                       |
| `make test`               | Run the test suite                                                  |
| `make protos`             | Regenerate `proto/*.pb.go` from `proto/*.proto` (needs protoc)      |
| `make release-binaries`   | Build release artifacts for all platforms                           |
| `make images`             | Build multi-arch Docker images for apps under `cmd/`                |
| `make clean`              | Remove build outputs                                                |

Override the fetched build via environment variables:

```bash
make ffembed FF_VERSION=9.0 FF_LICENSE=lgpl
```

## Project layout

```
.
├── ffmpegembed.go     Package: Ffexec, New/Close, binary resolution
├── ffmpeg.go          FfmpegRun, progress reader, event dispatch
├── ffprobe.go         Ffprobe structured parse (format + streams)
├── progress.go        Parse ffmpeg -progress samples → proto.Progress
├── proto/             Protobuf messages (ffrun.proto + checked-in .pb.go)
├── internal/embedded/ go:embed per-platform binaries
└── cmd/ffrun/         Showcase CLI
```

## License

Licensed under the [Apache License, Version 2.0](LICENSE).
