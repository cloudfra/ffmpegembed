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

package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfra/ffmpegembed/internal/archive"
	"github.com/cloudfra/ffmpegembed/internal/manifest"
)

// release serves a fake release: under /<version>/ it has ffmpeg.gz,
// ffprobe.gz and LICENSE.gz whose decompressed content names the version.
func release(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version, file, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		name, isGz := strings.CutSuffix(file, ".gz")
		if !ok || !isGz || (name != "ffmpeg" && name != "ffprobe" && name != "LICENSE") {
			http.NotFound(w, r)
			return
		}
		zw := gzip.NewWriter(w)
		if _, err := zw.Write([]byte(name + " of " + version)); err != nil {
			t.Errorf("serve %s: %v", r.URL.Path, err)
		}
		if err := zw.Close(); err != nil {
			t.Errorf("serve %s: %v", r.URL.Path, err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), args, &stdout, &stderr); err != nil {
		t.Fatalf("mkffmpegembed %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String()
}

func readManifest(t *testing.T, path string) *manifest.Manifest {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestManifestAndArchive(t *testing.T) {
	url := release(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	// Create a manifest from scratch.
	mustRun(t, "manifest", "-version", "v1", "-license", "GPL-3.0-or-later",
		"-base-url", url+"/{version}",
		"-platform", "linux_amd64=ffmpeg.gz,ffprobe.gz,LICENSE.gz",
		"-platform", "windows_amd64=ffmpeg.gz,ffprobe.gz,LICENSE.gz",
		"-o", path)
	v1 := readManifest(t, path)
	if v1.Version != "v1" || v1.BaseURL != url+"/v1" || len(v1.Platforms) != 2 {
		t.Fatalf("unexpected manifest: %+v", v1)
	}

	// Move it to another release: platforms are kept, the URL follows the
	// version, and every digest is recomputed.
	mustRun(t, "manifest", "-from", path, "-version", "v2", "-o", path)
	v2 := readManifest(t, path)
	if v2.Version != "v2" || v2.BaseURL != url+"/v2" || len(v2.Platforms) != 2 {
		t.Fatalf("unexpected manifest after moving to v2: %+v", v2)
	}
	if v1.Platforms["linux_amd64"].Ffmpeg.BinarySHA256 == v2.Platforms["linux_amd64"].Ffmpeg.BinarySHA256 {
		t.Error("digests were not recomputed for the new version")
	}

	if _, err := exec.LookPath("xz"); err != nil {
		t.Skip("xz is not installed; skipping the archive half")
	}

	// Build an archive of what the manifest pins.
	out := filepath.Join(dir, "ffmpeg.tar.xz")
	mustRun(t, "archive", "-manifest", path, "-platform", "linux_amd64", "-o", out)
	data, err := os.ReadFile(out) //nolint:gosec // G304: path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	extracted := t.TempDir()
	if err := archive.Extract(data, extracted, func(e string) string { return e }); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for _, name := range []string{archive.Ffmpeg, archive.Ffprobe, archive.License} {
		got, err := os.ReadFile(filepath.Join(extracted, name)) //nolint:gosec // G304: path under t.TempDir()
		if err != nil {
			t.Fatal(err)
		}
		if want := name + " of v2"; string(got) != want {
			t.Errorf("archive entry %s = %q; want %q", name, got, want)
		}
	}

	// A manifest whose digest does not match what is served is refused, and
	// no archive is written.
	p := v2.Platforms["linux_amd64"]
	p.Ffprobe.BinarySHA256 = v1.Platforms["linux_amd64"].Ffprobe.BinarySHA256
	v2.Platforms["linux_amd64"] = p
	tampered, err := v2.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.tar.xz")
	var stderr bytes.Buffer
	err = run(context.Background(), []string{"archive", "-manifest", path, "-platform", "linux_amd64", "-o", bad}, &bytes.Buffer{}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("archive from a manifest with a wrong digest = %v; want a checksum mismatch", err)
	}
	if _, err := os.Stat(bad); err == nil {
		t.Error("an archive was written despite the checksum mismatch")
	}
}

func TestArchiveFromLocalFiles(t *testing.T) {
	if _, err := exec.LookPath("xz"); err != nil {
		t.Skip("xz is not installed")
	}
	dir := t.TempDir()
	for _, name := range []string{"ffmpeg", "ffprobe", "LICENSE"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("local "+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "out.tar.xz")
	stdout := mustRun(t, "archive",
		"-ffmpeg", filepath.Join(dir, "ffmpeg"), "-ffprobe", filepath.Join(dir, "ffprobe"),
		"-license", filepath.Join(dir, "LICENSE"), "-o", out)
	if !strings.Contains(stdout, "sha256:") {
		t.Errorf("archive did not report a digest: %q", stdout)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Errorf("output dir has %d entries; want the 3 inputs and the archive (no temp file)", len(entries))
	}
}

func TestUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no command", nil, "missing command"},
		{"unknown command", []string{"frobnicate"}, "unknown command"},
		{"archive without inputs", []string{"archive"}, "are all required"},
		{"archive with both sources", []string{"archive", "-manifest", "m.json", "-ffmpeg", "f"}, "not both"},
		{"archive with a stray argument", []string{"archive", "extra"}, "unexpected argument"},
		{"manifest without inputs", []string{"manifest"}, "are required"},
		{"manifest with a malformed platform", []string{"manifest", "-platform", "linux_amd64=a,b"}, "invalid value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(context.Background(), tc.args, &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("run(%q) = %v; want an error mentioning %q", tc.args, err, tc.want)
			}
		})
	}
}
