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

// Package manifest defines the JSON document that pins an ffmpeg release: its
// version and, for each platform, where ffmpeg, ffprobe and their license are
// downloaded from and the SHA-256 digests they must have.
//
//	{
//	  "version": "b6.1.1",
//	  "license": "GPL-3.0-or-later",
//	  "base_url": "https://github.com/eugeneware/ffmpeg-static/releases/download/b6.1.1",
//	  "platforms": {
//	    "linux_amd64": {
//	      "ffmpeg": {
//	        "file": "ffmpeg-linux-x64.gz",
//	        "sha256": "bfe8a8fc…",
//	        "binary_sha256": "e7e7fb…"
//	      },
//	      "ffprobe": {…},
//	      "license": {…}
//	    }
//	  }
//	}
//
// Every file is a single gzip-compressed file (not an archive), fetched from
// base_url + "/" + file. cmd/mkffmpegembed writes manifests; regenerate one
// rather than editing its digests by hand.
package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

// Names of a platform's assets.
const (
	Ffmpeg  = "ffmpeg"
	Ffprobe = "ffprobe"
	License = "license"
)

// Asset is one downloadable file of a release.
type Asset struct {
	// File is the asset's file name, appended to Manifest.BaseURL. It is a
	// single gzip-compressed file.
	File string `json:"file"`
	// SHA256 is the lowercase hex SHA-256 digest of File as downloaded.
	SHA256 string `json:"sha256"`
	// BinarySHA256 is the lowercase hex SHA-256 digest of File once
	// decompressed.
	BinarySHA256 string `json:"binary_sha256"`
}

// Platform holds the assets of one <goos>_<goarch>.
type Platform struct {
	Ffmpeg  Asset `json:"ffmpeg"`
	Ffprobe Asset `json:"ffprobe"`
	License Asset `json:"license"`
}

// Asset returns the asset called name (Ffmpeg, Ffprobe or License).
func (p Platform) Asset(name string) (Asset, bool) {
	switch name {
	case Ffmpeg:
		return p.Ffmpeg, true
	case Ffprobe:
		return p.Ffprobe, true
	case License:
		return p.License, true
	}
	return Asset{}, false
}

// Manifest pins one ffmpeg release.
type Manifest struct {
	// Version is the release tag.
	Version string `json:"version"`
	// License is the SPDX expression the builds are distributed under.
	License string `json:"license"`
	// BaseURL is the URL prefix every Asset.File is fetched from.
	BaseURL string `json:"base_url"`
	// Platforms maps "<goos>_<goarch>" (see Key) to its assets.
	Platforms map[string]Platform `json:"platforms"`
}

// Key returns the name a platform is listed under in Manifest.Platforms.
func Key(goos, goarch string) string { return goos + "_" + goarch }

// URL returns the address asset is downloaded from.
func (m *Manifest) URL(asset Asset) string {
	return strings.TrimRight(m.BaseURL, "/") + "/" + asset.File
}

// Keys returns the platforms in the manifest, sorted.
func (m *Manifest) Keys() []string {
	keys := make([]string, 0, len(m.Platforms))
	for k := range m.Platforms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Parse decodes and validates a JSON manifest. Unknown fields are rejected, so
// a misspelled key is an error rather than a silently ignored setting.
func Parse(data []byte) (*Manifest, error) {
	m := &Manifest{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if dec.More() {
		return nil, errors.New("parse manifest: unexpected data after the JSON document")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// Marshal validates the manifest and encodes it as indented JSON. Platforms
// are written in sorted order, so the output is stable.
func (m *Manifest) Marshal() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Validate reports everything that is wrong with the manifest: missing
// fields, file names that are not plain names, and malformed digests.
func (m *Manifest) Validate() error {
	var errs []error
	if m.Version == "" {
		errs = append(errs, errors.New("version is empty"))
	}
	if m.License == "" {
		errs = append(errs, errors.New("license is empty"))
	}
	if !strings.HasPrefix(m.BaseURL, "https://") && !strings.HasPrefix(m.BaseURL, "http://") {
		errs = append(errs, fmt.Errorf("base_url %q is not an http(s) URL", m.BaseURL))
	}
	if len(m.Platforms) == 0 {
		errs = append(errs, errors.New("platforms is empty"))
	}
	for _, key := range m.Keys() {
		p := m.Platforms[key]
		for _, name := range []string{Ffmpeg, Ffprobe, License} {
			a, _ := p.Asset(name)
			where := key + "." + name
			if a.File == "" || path.Base(a.File) != a.File || strings.ContainsAny(a.File, "\\?#") {
				errs = append(errs, fmt.Errorf("%s: file %q is not a plain file name", where, a.File))
			}
			if !isSHA256(a.SHA256) {
				errs = append(errs, fmt.Errorf("%s: sha256 %q is not a lowercase hex SHA-256 digest", where, a.SHA256))
			}
			if !isSHA256(a.BinarySHA256) {
				errs = append(errs, fmt.Errorf("%s: binary_sha256 %q is not a lowercase hex SHA-256 digest", where, a.BinarySHA256))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid manifest: %w", errors.Join(errs...))
	}
	return nil
}

func isSHA256(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && s == strings.ToLower(s)
}
