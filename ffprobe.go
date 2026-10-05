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
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/cloudfra/ffmpegembed/proto"
)

// buildFfProbeArgs assembles the argument vector for an ffprobe invocation.
//
// Output is always requested as JSON (so the result can be parsed structurally)
// unless a different PrintFormat is requested. When no show_* flag is set,
// format and streams are shown by default. RawArgs (unstructured mode) are
// appended after the structured flags.
func buildFfProbeArgs(a *FfProbeArgs) []string {
	if a == nil {
		a = &FfProbeArgs{}
	}

	format := a.GetPrintFormat()
	if format == "" {
		format = "json"
	}

	out := []string{"-print_format", format}

	noneSet := !a.GetShowFormat() && !a.GetShowStreams() && !a.GetShowChapters() && !a.GetShowFrames()
	if noneSet {
		out = append(out, "-show_format", "-show_streams")
	} else {
		if a.GetShowFormat() {
			out = append(out, "-show_format")
		}
		if a.GetShowStreams() {
			out = append(out, "-show_streams")
		}
		if a.GetShowChapters() {
			out = append(out, "-show_chapters")
		}
		if a.GetShowFrames() {
			out = append(out, "-show_frames")
		}
	}

	if s := a.GetSelectStreams(); s != "" {
		out = append(out, "-select_streams", s)
	}

	if len(a.GetRawArgs()) > 0 {
		out = append(out, a.GetRawArgs()...)
	}
	if in := a.GetInput(); in != "" {
		out = append(out, in)
	}
	return out
}

// Ffprobe runs ffprobe against the input described by a and returns a fully
// structured FfProbeResult (format + streams + the full raw JSON).
func (f *Ffexec) Ffprobe(args *FfProbeArgs) (*FfProbeResult, error) {
	if args == nil {
		args = &FfProbeArgs{}
	}
	if args.GetInput() == "" && len(args.GetRawArgs()) == 0 {
		return nil, fmt.Errorf("ffprobe: %w (no input provided)", ErrInvalidArgs)
	}

	cmd := exec.CommandContext(context.Background(), f.ffprobePath, buildFfProbeArgs(args)...) //nolint:gosec // G204: ffprobe is a fixed, trusted binary; all args come from the library's own API
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, runError("ffprobe", f.ffprobePath, err, strings.TrimSpace(stderr.String()))
	}

	result, err := parseFfprobe(stdout.String())
	if err != nil {
		return nil, fmt.Errorf("ffprobe: parse output: %w", err)
	}
	return result, nil
}

// jsonStream mirrors a subset of ffprobe's JSON stream object.
type jsonStream struct {
	Index          *json.Number   `json:"index"`
	CodecName      string         `json:"codec_name"`
	CodecLongName  string         `json:"codec_long_name"`
	Profile        string         `json:"profile"`
	CodecType      string         `json:"codec_type"`
	CodecTag       string         `json:"codec_tag"`
	CodecTagString string         `json:"codec_tag_string"`
	BitRate        json.Number    `json:"bit_rate"`
	Width          int32          `json:"width"`
	Height         int32          `json:"height"`
	PixFmt         string         `json:"pix_fmt"`
	Level          json.Number    `json:"level"`
	RFrameRate     string         `json:"r_frame_rate"`
	AvgFrameRate   string         `json:"avg_frame_rate"`
	SampleRate     json.Number    `json:"sample_rate"`
	Channels       int32          `json:"channels"`
	ChannelLayout  string         `json:"channel_layout"`
	BitsPerSample  int32          `json:"bits_per_sample"`
	Settings       string         `json:"settings"`
	Tags           map[string]any `json:"tags"`
}

// jsonFormat mirrors a subset of ffprobe's JSON format object.
type jsonFormat struct {
	Filename       string         `json:"filename"`
	Size           json.Number    `json:"size"`
	FormatName     string         `json:"format_name"`
	FormatLongName string         `json:"format_long_name"`
	StartTime      string         `json:"start_time"`
	Duration       string         `json:"duration"`
	BitRate        json.Number    `json:"bit_rate"`
	Tags           map[string]any `json:"tags"`
}

func parseFfprobe(data string) (*FfProbeResult, error) {
	data = strings.TrimSpace(data)
	if data == "" {
		return &FfProbeResult{}, nil
	}

	var env struct {
		Format  *json.RawMessage  `json:"format"`
		Streams []json.RawMessage `json:"streams"`
	}
	if err := json.Unmarshal([]byte(data), &env); err != nil {
		return &FfProbeResult{RawJson: data}, nil
	}

	result := &proto.FfProbeResult{RawJson: data}

	if env.Format != nil {
		var jf jsonFormat
		if err := json.Unmarshal(*env.Format, &jf); err == nil {
			result.Format = jf.toProto(string(*env.Format))
		}
	}
	for i := range env.Streams {
		var js jsonStream
		if err := json.Unmarshal(env.Streams[i], &js); err == nil {
			result.Streams = append(result.Streams, js.toProto(string(env.Streams[i])))
		}
	}
	return result, nil
}

// toProto flattens a parsed stream into the FfProbeStream message.
func (s jsonStream) toProto(rawJSON string) *FfProbeStream {
	out := &FfProbeStream{
		Index:          numberToString(s.Index),
		CodecName:      s.CodecName,
		CodecLongName:  s.CodecLongName,
		Profile:        s.Profile,
		CodecType:      s.CodecType,
		CodecTag:       s.CodecTag,
		CodecTagString: s.CodecTagString,
		BitRate:        numberToInt64(s.BitRate),
		Width:          s.Width,
		Height:         s.Height,
		PixFmt:         s.PixFmt,
		Level:          numberToString(&s.Level),
		RFrameRate:     parseRateRatio(s.RFrameRate),
		AvgFrameRate:   parseRateRatio(s.AvgFrameRate),
		SampleRate:     numberToInt64(s.SampleRate),
		Channels:       s.Channels,
		ChannelLayout:  s.ChannelLayout,
		BitsPerSample:  s.BitsPerSample,
		Settings:       s.Settings,
		Tags:           toStringMap(s.Tags),
		RawJson:        rawJSON,
	}
	return out
}

// toProto flattens a parsed format into the FfProbeFormat message.
func (f jsonFormat) toProto(rawJSON string) *FfProbeFormat {
	return &FfProbeFormat{
		Filename:       f.Filename,
		FileSize:       numberToInt64(f.Size),
		FormatName:     f.FormatName,
		FormatLongName: f.FormatLongName,
		StartTime:      parseFloat(f.StartTime),
		Duration:       parseFloat(f.Duration),
		BitRate:        numberToInt64(f.BitRate),
		Tags:           toStringMap(f.Tags),
		RawJson:        rawJSON,
	}
}

// parseRateRatio converts ffprobe's "num/den" rate string (e.g. "3000001000/...")
// to a float. A bare number or empty/"0/0" yields 0.
func parseRateRatio(s string) float64 {
	if s == "" {
		return 0
	}
	if idx := strings.IndexByte(s, '/'); idx >= 0 {
		num, errN := strconv.ParseFloat(s[:idx], 64)
		den, errD := strconv.ParseFloat(s[idx+1:], 64)
		if errN != nil || errD != nil || den == 0 {
			return 0
		}
		return num / den
	}
	return parseFloat(s)
}

func numberToString(n *json.Number) string {
	if n == nil {
		return ""
	}
	return n.String()
}

func numberToInt64(n json.Number) int64 {
	if n == "" {
		return 0
	}
	if v, err := n.Int64(); err == nil {
		return v
	}
	if f, err := n.Float64(); err == nil {
		return int64(f)
	}
	return 0
}

func toStringMap(in map[string]any) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}
