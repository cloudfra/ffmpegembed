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

package archive

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// writeFiles creates the three input files in a temp dir and returns them
// with their contents by entry name.
func writeFiles(t *testing.T) (Files, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	// The binaries share most of their content, like the real ones.
	shared := bytes.Repeat([]byte("libavcodec "), 4096)
	contents := map[string]string{
		Ffmpeg:  "ffmpeg main\n" + string(shared),
		Ffprobe: "ffprobe main\n" + string(shared),
		License: "license text\n",
	}
	for name, data := range contents {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Files{
		Ffmpeg:  filepath.Join(dir, Ffmpeg),
		Ffprobe: filepath.Join(dir, Ffprobe),
		License: filepath.Join(dir, License),
	}, contents
}

func requireXz(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("xz"); err != nil {
		t.Skip("xz is not installed")
	}
}

func TestCreateExtractRoundTrip(t *testing.T) {
	requireXz(t)
	files, contents := writeFiles(t)
	var buf bytes.Buffer
	if err := Create(context.Background(), &buf, files); err != nil {
		t.Fatalf("Create: %v", err)
	}

	dir := t.TempDir()
	if err := Extract(buf.Bytes(), dir, func(entry string) string { return entry + ".out" }); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for name, want := range contents {
		path := filepath.Join(dir, name+".out")
		got, err := os.ReadFile(path) //nolint:gosec // G304: path under t.TempDir()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s: extracted content differs from the input", name)
		}
		if runtime.GOOS == "windows" {
			continue
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if gotExec, wantExec := fi.Mode()&0o100 != 0, name != License; gotExec != wantExec {
			t.Errorf("%s: mode %v; want executable=%v", name, fi.Mode(), wantExec)
		}
	}
}

// TestCreateIsDeterministic verifies an archive depends only on the contents
// of its inputs, not on when or where they were written.
func TestCreateIsDeterministic(t *testing.T) {
	requireXz(t)
	var archives [2]bytes.Buffer
	for i := range archives {
		files, _ := writeFiles(t)
		if err := Create(context.Background(), &archives[i], files); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	if !bytes.Equal(archives[0].Bytes(), archives[1].Bytes()) {
		t.Error("two archives of identical inputs differ")
	}
}

func TestCreateRejectsBadInput(t *testing.T) {
	requireXz(t)
	files, _ := writeFiles(t)
	missing := files
	missing.Ffprobe = filepath.Join(t.TempDir(), "absent")
	if err := Create(context.Background(), &bytes.Buffer{}, missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Create with a missing ffprobe = %v; want a not-exist error", err)
	}
	empty := files
	empty.License = filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty.License, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Create(context.Background(), &bytes.Buffer{}, empty); err == nil {
		t.Error("Create with an empty license succeeded; want an error")
	}
}

func TestCreateWithoutXz(t *testing.T) {
	files, _ := writeFiles(t)
	t.Setenv("PATH", t.TempDir())
	if err := Create(context.Background(), &bytes.Buffer{}, files); !errors.Is(err, ErrNoXz) {
		t.Errorf("Create without xz on PATH = %v; want ErrNoXz", err)
	}
}

func TestExtractSkipsUnmappedEntries(t *testing.T) {
	requireXz(t)
	files, _ := writeFiles(t)
	var buf bytes.Buffer
	if err := Create(context.Background(), &buf, files); err != nil {
		t.Fatalf("Create: %v", err)
	}
	dir := t.TempDir()
	err := Extract(buf.Bytes(), dir, func(entry string) string {
		if entry == License {
			// A path is reduced to its base name: entries cannot leave dir.
			return "../../the-license.txt"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "the-license.txt" {
		t.Errorf("Extract wrote %v; want only the-license.txt", entries)
	}
}

func TestExtractRejectsGarbage(t *testing.T) {
	if err := Extract([]byte("not an archive"), t.TempDir(), func(e string) string { return e }); err == nil {
		t.Error("Extract of garbage succeeded; want an error")
	}
}

func TestDictSize(t *testing.T) {
	for _, tc := range []struct {
		binary int64
		want   int64
	}{
		{binary: 1, want: 64 * mib},            // never below the xz -9 default
		{binary: 80 * mib, want: 96 * mib},     // next 16 MiB step above the binary
		{binary: 96 * mib, want: 112 * mib},    // strictly larger than the binary
		{binary: 5000 * mib, want: 1536 * mib}, // capped at what xz accepts
	} {
		if got := DictSize(tc.binary); got != tc.want {
			t.Errorf("DictSize(%d MiB) = %d MiB; want %d MiB", tc.binary/mib, got/mib, tc.want/mib)
		}
	}
}
