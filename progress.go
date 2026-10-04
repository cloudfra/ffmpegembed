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
	"strconv"
	"strings"

	"github.com/cloudfra/ffmpegembed/proto"
)

// parseProgress converts one sample of ffmpeg's `-progress pipe:1` output
// (a flat key=value map) into a structured proto.Progress. It reuses the field
// names ffmpeg emits (frame, fps, bitrate, speed, out_time_us, total_size,
// drop_frames, dup_frames, out_time).
func parseProgress(sample map[string]string) *proto.Progress {
	p := &proto.Progress{
		Fields:     map[string]string{},
		Time:       parseOutTime(sample["out_time"]),
		Frame:      parseInt(sample["frame"]),
		Fps:        parseFloat(sample["fps"]),
		Bitrate:    parseBitrate(sample["bitrate"]),
		OutTimeUs:  parseInt(sample["out_time_us"]),
		TotalSize:  parseInt(sample["total_size"]),
		DropFrames: int32(parseInt(sample["drop_frames"])), //nolint:gosec // G115: frame counters are small in practice
		DupFrames:  int32(parseInt(sample["dup_frames"])),  //nolint:gosec // G115: frame counters are small in practice
		OutputTime: parseOutTime(sample["out_time"]),
	}

	p.Speed = parseSpeed(sample["speed"])

	for k, v := range sample {
		p.Fields[k] = v
	}
	return p
}

// parseBitrate extracts the numeric portion from "2313.6kbits/s" style values.
// Returns 0 for empty input or "N/A".
func parseBitrate(s string) float64 {
	if s == "" || s == "N/A" {
		return 0
	}
	s = strings.TrimSuffix(s, "kbits/s")
	s = strings.TrimSpace(s)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseSpeed extracts the numeric portion from "0.0862x" style values.
func parseSpeed(s string) float64 {
	if s == "" || s == "N/A" {
		return 0
	}
	s = strings.TrimSuffix(s, "x")
	s = strings.TrimSpace(s)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseOutTime converts an ffmpeg out_time value like "00:00:24.473832" to
// seconds. A bare numeric string (already seconds) is returned as-is.
func parseOutTime(s string) float64 {
	if s == "" {
		return 0
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return parseFloatPtr(&s)
	}
	// HH:MM:SS.uuuuuu
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 {
		return 0
	}
	return parseFloat(parts[0])*3600 + parseFloat(parts[1])*60 + parseFloat(parts[2])
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseInt(s string) int64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		if f, ferr := strconv.ParseFloat(s, 64); ferr == nil {
			return int64(f)
		}
		return 0
	}
	return v
}

func parseFloatPtr(p *string) float64 { return parseFloat(*p) }
