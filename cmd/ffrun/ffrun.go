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

// Command ffrun is a small showcase of the ffmpegembed library. It offers two
// sub-commands, each of which accepts either structured flags or raw ffmpeg /
// ffprobe arguments; in every case the output is the library's structured
// result rendered as JSON.
//
//	$ ffrun probe -input movie.mp4
//	$ ffrun probe -show_streams movie.mp4        # structured flags
//	$ ffrun probe -show_frames -select_streams v:0 movie.mp4
//
//	$ ffrun run -input in.mov -output out.mp4 -video-codec libx264 -crf 23
//	$ ffrun run in.mov -c:v libx264 -crf 28 out.mp4   # raw args (unstructured)
//
// Positional (non-flag) arguments after the flags are treated as raw ffmpeg /
// ffprobe arguments and passed through verbatim (the "unstructured" mode).
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/cloudfra/ffmpegembed"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// strSlice is a flag.Value collecting repeated `-input` style arguments.
type strSlice []string

func (s *strSlice) String() string { return strings.Join(*s, ", ") }
func (s *strSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

const usageText = `ffrun - a showcase for the ffmpegembed library.

Usage:
  ffrun [global flags] <subcommand> [subcommand flags] [raw args...]

Sub-commands:
  probe    Run ffprobe and print the structured FfProbeResult as JSON.
  run      Run an ffmpeg conversion and print the structured FfmpegResult as JSON
           (progress is streamed to stderr).
  version  Print the version of the ffmpeg/ffprobe in use.

Global flags:
  -external     Prefer an ffmpeg/ffprobe found on PATH (default: true); fall
                back to the embedded binary otherwise.
  -workdir DIR  Directory to extract the embedded binaries into. If empty a
                temp dir is used and removed on exit.

Structured vs unstructured input:
  Structured flags map 1:1 to the library's protobuf Arg messages. Any
  positional argument after the flags is appended to the command verbatim
  (RawArgs / "unstructured" mode). When RawArgs are present they are used as-is
  by ffmpeg. Output is always the library's structured result, as JSON.

Run "ffrun <subcommand> -help" for a sub-command's flags.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		// Errors are printed without a stack; exit non-zero.
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// --- Global flags -----------------------------------------------------
	g := flag.NewFlagSet("ffrun", flag.ContinueOnError)
	g.SetOutput(os.Stderr)
	g.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
	external := g.Bool("external", true, "prefer ffmpeg/ffprobe on PATH")
	workDir := g.String("workdir", "", "directory to extract embedded binaries")

	if err := g.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	rest := g.Args()
	if len(rest) == 0 {
		g.Usage()
		return fmt.Errorf("missing subcommand (probe|run)")
	}

	sub, subArgs := rest[0], rest[1:]

	ffexec, err := ffmpegembed.New(&ffmpegembed.Args{
		UseExternalIfAvailable: *external,
		WorkDir:                *workDir,
	})
	if err != nil {
		return fmt.Errorf("resolve ffmpeg/ffprobe: %w", err)
	}
	defer ffexec.Close()

	switch sub {
	case "probe":
		return cmdProbe(ffexec, subArgs)
	case "run":
		return cmdRun(ffexec, subArgs)
	case "version":
		return cmdVersion(ffexec)
	case "probe-path", "ffmpeg", "ffprobe":
		// Convenience: report which binary the resolved handle points at.
		fmt.Println(ffexec.FfmpegPath())
		return nil
	case "help", "-h", "--help":
		fmt.Print(usageText)
		return nil
	default:
		g.Usage()
		return fmt.Errorf("unknown subcommand %q (want probe|run)", sub)
	}
}

func cmdProbe(ffexec *ffmpegembed.Ffexec, args []string) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	input := fs.String("input", "", "input file to probe (structured)")
	showFormat := fs.Bool("show_format", false, "request -show_format")
	showStreams := fs.Bool("show_streams", false, "request -show_streams")
	showChapters := fs.Bool("show_chapters", false, "request -show_chapters")
	showFrames := fs.Bool("show_frames", false, "request -show_frames")
	selectStreams := fs.String("select_streams", "", "-select_streams value")
	printFormat := fs.String("print_format", "json", "-print_format value")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	// Positional args become RawArgs (unstructured mode) and are appended
	// after the structured flags.
	raw := fs.Args()

	pa := &ffmpegembed.FfProbeArgs{
		Input:         *input,
		ShowFormat:    *showFormat,
		ShowStreams:   *showStreams,
		ShowChapters:  *showChapters,
		ShowFrames:    *showFrames,
		SelectStreams: *selectStreams,
		PrintFormat:   *printFormat,
		RawArgs:       raw,
	}

	result, err := ffexec.Ffprobe(pa)
	if err != nil {
		return err
	}
	return emitJSON(result)
}

func cmdRun(ffexec *ffmpegembed.Ffexec, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var inputs strSlice
	fs.Var(&inputs, "input", "input file (-i); may be repeated")
	output := fs.String("output", "", "output file")
	videoCodec := fs.String("video_codec", "", "-c:v codec")
	audioCodec := fs.String("audio_codec", "", "-c:a codec")
	crf := fs.Int("crf", 0, "-crf value")
	preset := fs.String("preset", "", "-preset value")
	bitrate := fs.String("bitrate", "", "-b value")
	faststart := fs.Bool("faststart", false, "-movflags faststart")
	overwrite := fs.Bool("overwrite", true, "-y (overwrite output)")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	// Positional args become RawArgs (unstructured); when present these fully
	// describe the ffmpeg command and the structured flags are ignored.
	raw := fs.Args()

	fa := &ffmpegembed.FfmpegArgs{
		Inputs:     inputs,
		Output:     *output,
		VideoCodec: *videoCodec,
		AudioCodec: *audioCodec,
		Crf:        int32(*crf),
		Preset:     *preset,
		Bitrate:    *bitrate,
		Faststart:  *faststart,
		Overwrite:  *overwrite,
		RawArgs:    raw,
	}

	run, err := ffexec.Ffmpeg(fa)
	if err != nil {
		return err
	}

	var lastResult *ffmpegembed.FfmpegResult
	done := make(chan struct{})
	go func() {
		run.OnUpdate(func(ev *ffmpegembed.Event) {
			switch {
			case ev.GetProgress() != nil:
				p := ev.GetProgress()
				fmt.Fprintf(os.Stderr, "\rffmpeg  frame=%d  fps=%.2f  speed=%.4gx  time=%ss", p.GetFrame(), p.GetFps(), p.GetSpeed(), duration(p.GetTime()))
			case ev.GetResult() != nil:
				lastResult = ev.GetResult()
			}
		})
		if err := run.Wait(); err != nil {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "run failed:", err)
		}
		close(done)
	}()
	<-done
	fmt.Fprintln(os.Stderr) // newline after progress line

	result := lastResult
	if result == nil {
		result = &ffmpegembed.FfmpegResult{Output: fa.Output}
	}
	return emitJSON(result)
}

// cmdVersion prints the version of the ffmpeg (and ffprobe) the resolved
// handle is using.
func cmdVersion(ffexec *ffmpegembed.Ffexec) error {
	ver, err := ffexec.Version()
	if err != nil {
		return err
	}
	fmt.Println(ver)
	if pver, perr := ffexec.FfprobeVersion(); perr == nil {
		fmt.Println(pver)
	}
	return nil
}

// emitJSON marshals a proto message to indented protoJSON on stdout.
func emitJSON(m proto.Message) error {
	b, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(m)
	if err != nil {
		return err
	}
	_, werr := os.Stdout.Write(append(b, '\n'))
	return werr
}

// duration formats a number of seconds as "12.346s" (compact).
func duration(seconds float64) string {
	if seconds <= 0 {
		return "-.----"
	}
	if seconds < 100 {
		return fmt.Sprintf("%.4f", seconds)
	}
	return fmt.Sprintf("%.1f", seconds)
}
