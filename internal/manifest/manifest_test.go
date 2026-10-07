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

package manifest

import (
	"strings"
	"testing"
)

const digest = "bfe8a8fc511530457b528c48d77b5737527b504a3797a9bc4866aeca69c2dffa"

func valid() *Manifest {
	asset := func(file string) Asset { return Asset{File: file, SHA256: digest, BinarySHA256: digest} }
	return &Manifest{
		Version: "v1",
		License: "GPL-3.0-or-later",
		BaseURL: "https://example.com/releases/v1",
		Platforms: map[string]Platform{
			"linux_amd64":  {Ffmpeg: asset("ffmpeg.gz"), Ffprobe: asset("ffprobe.gz"), License: asset("LICENSE.gz")},
			"darwin_arm64": {Ffmpeg: asset("ffmpeg-mac.gz"), Ffprobe: asset("ffprobe-mac.gz"), License: asset("LICENSE.gz")},
		},
	}
}

func TestMarshalParseRoundTrip(t *testing.T) {
	m := valid()
	data, err := m.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Platforms are written sorted, so output is stable.
	if d, l := strings.Index(string(data), "darwin_arm64"), strings.Index(string(data), "linux_amd64"); d < 0 || l < d {
		t.Errorf("platforms are not in sorted order:\n%s", data)
	}
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Version != m.Version || got.License != m.License || got.BaseURL != m.BaseURL || len(got.Platforms) != 2 {
		t.Errorf("round trip changed the manifest: %+v", got)
	}
	if got.Platforms["linux_amd64"] != m.Platforms["linux_amd64"] {
		t.Errorf("round trip changed linux_amd64: %+v", got.Platforms["linux_amd64"])
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	data, err := valid().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	misspelled := strings.Replace(string(data), `"version"`, `"verison"`, 1)
	if _, err := Parse([]byte(misspelled)); err == nil {
		t.Error("Parse accepted a manifest with a misspelled key")
	}
	if _, err := Parse(append(data, data...)); err == nil {
		t.Error("Parse accepted two documents in one manifest")
	}
}

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(m *Manifest)
		want   string // substring of the error
	}{
		{"no version", func(m *Manifest) { m.Version = "" }, "version is empty"},
		{"no license", func(m *Manifest) { m.License = "" }, "license is empty"},
		{"bad base url", func(m *Manifest) { m.BaseURL = "ftp://example.com" }, "base_url"},
		{"no platforms", func(m *Manifest) { m.Platforms = nil }, "platforms is empty"},
		{"path in file", func(m *Manifest) {
			p := m.Platforms["linux_amd64"]
			p.Ffmpeg.File = "../ffmpeg.gz"
			m.Platforms["linux_amd64"] = p
		}, "linux_amd64.ffmpeg: file"},
		{"short digest", func(m *Manifest) {
			p := m.Platforms["linux_amd64"]
			p.Ffprobe.SHA256 = "abc"
			m.Platforms["linux_amd64"] = p
		}, "linux_amd64.ffprobe: sha256"},
		{"missing binary digest", func(m *Manifest) {
			p := m.Platforms["linux_amd64"]
			p.License.BinarySHA256 = ""
			m.Platforms["linux_amd64"] = p
		}, "linux_amd64.license: binary_sha256"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := valid()
			tc.mutate(m)
			err := m.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate = %v; want an error mentioning %q", err, tc.want)
			}
		})
	}
	if err := valid().Validate(); err != nil {
		t.Errorf("Validate of a valid manifest: %v", err)
	}
}

func TestLookups(t *testing.T) {
	m := valid()
	p := m.Platforms[Key("linux", "amd64")]
	if a, ok := p.Asset(Ffprobe); !ok || a.File != "ffprobe.gz" {
		t.Errorf("Asset(ffprobe) = %+v, %v", a, ok)
	}
	if _, ok := p.Asset("ffplay"); ok {
		t.Error("Asset(ffplay) reported an asset")
	}
	m.BaseURL += "/"
	if got, want := m.URL(p.Ffmpeg), "https://example.com/releases/v1/ffmpeg.gz"; got != want {
		t.Errorf("URL = %q; want %q", got, want)
	}
	if got := strings.Join(m.Keys(), ","); got != "darwin_arm64,linux_amd64" {
		t.Errorf("Keys = %q", got)
	}
}

// TestDefault verifies the manifest compiled into the module parses and
// validates, and covers the platforms that have an embedded archive.
func TestDefault(t *testing.T) {
	m, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	for _, key := range []string{"linux_amd64", "windows_amd64"} {
		if _, ok := m.Platforms[key]; !ok {
			t.Errorf("the default manifest has no platform %s", key)
		}
	}
}
