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

// Package archive reads and writes the ffmpeg archive format ffmpegembed
// embeds: a tar.xz holding three entries, "ffmpeg", "ffprobe" and "LICENSE"
// (the license the two binaries are distributed under). The entry names carry
// no ".exe" suffix on any platform.
//
// Both binaries go in one archive because they share most of their code. Given
// a dictionary larger than one binary, xz finds the second binary's copy of
// that code in the first and stores it once.
package archive

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ulikunitz/xz"
)

// Names of the entries in an archive.
const (
	Ffmpeg  = "ffmpeg"
	Ffprobe = "ffprobe"
	License = "LICENSE"
)

// ErrNoXz is returned (wrapped) by Create when the xz program is not
// installed.
var ErrNoXz = errors.New("the xz program was not found on PATH (install xz-utils)")

const (
	// maxEntrySize bounds one extracted entry, so a corrupt archive cannot
	// fill the disk.
	maxEntrySize = 1 << 30

	mib = 1 << 20
	// minDict is the dictionary xz -9 uses by default; Create never goes
	// below it.
	minDict = 64 * mib
	// maxDict is the largest dictionary xz accepts for compression.
	maxDict = 1536 * mib
	// dictStep is the granularity dictionary sizes are rounded up to.
	dictStep = 16 * mib
)

// epoch is the modification time given to every entry, so that archives built
// from the same files are identical.
var epoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// Files names the three files that make up an archive.
type Files struct {
	Ffmpeg  string
	Ffprobe string
	License string
}

// DictSize returns the xz dictionary size, in bytes, Create uses for binaries
// of which the larger is binarySize bytes: the smallest multiple of 16 MiB
// that is larger than one binary, so the second binary in the archive can be
// matched against the first. It is also the memory needed to decompress the
// archive, which is why it is not simply the maximum.
func DictSize(binarySize int64) int64 {
	d := (binarySize/dictStep + 1) * dictStep
	return min(max(d, minDict), maxDict)
}

// Create writes a tar.xz archive of files to w.
//
// Compression is done by the xz program at its highest setting (-9e, with the
// dictionary from DictSize), because no pure-Go encoder comes close to it;
// Create fails with ErrNoXz when xz is not installed. Entries are written in a
// fixed order with fixed timestamps, ownership and permissions, so the result
// depends only on the file contents and the xz version.
func Create(ctx context.Context, w io.Writer, files Files) error {
	xzPath, err := exec.LookPath("xz")
	if err != nil {
		return ErrNoXz
	}

	entries := []struct {
		name string
		path string
		mode int64
	}{
		{Ffmpeg, files.Ffmpeg, 0o755},
		{Ffprobe, files.Ffprobe, 0o755},
		{License, files.License, 0o644},
	}
	var largest int64
	for _, e := range entries {
		fi, err := os.Stat(e.path)
		if err != nil {
			return fmt.Errorf("%s: %w", e.name, err)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%s: %s is not a regular file", e.name, e.path)
		}
		if fi.Size() == 0 {
			return fmt.Errorf("%s: %s is empty", e.name, e.path)
		}
		if e.name != License {
			largest = max(largest, fi.Size())
		}
	}

	dict := fmt.Sprintf("--lzma2=preset=9e,dict=%dMiB", DictSize(largest)/mib)
	cmd := exec.CommandContext(ctx, xzPath, "-9e", dict, "-T1", "-c") //nolint:gosec // G204: xz from PATH with fixed arguments
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stdout = w
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start xz: %w", err)
	}

	tarErr := writeTar(stdin, func(tw *tar.Writer) error {
		for _, e := range entries {
			if err := addFile(tw, e.name, e.path, e.mode); err != nil {
				return fmt.Errorf("%s: %w", e.name, err)
			}
		}
		return nil
	})
	if cerr := stdin.Close(); tarErr == nil {
		tarErr = cerr
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("xz: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return tarErr
}

// writeTar runs add against a tar writer on w and closes the tar stream.
func writeTar(w io.Writer, add func(*tar.Writer) error) error {
	tw := tar.NewWriter(w)
	if err := add(tw); err != nil {
		return err
	}
	return tw.Close()
}

// addFile appends the file at path to tw as a regular entry called name.
func addFile(tw *tar.Writer, name, path string, mode int64) error {
	f, err := os.Open(path) //nolint:gosec // G304: a path the caller asked to archive
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // read-only file
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     name,
		Size:     fi.Size(),
		Mode:     mode,
		ModTime:  epoch,
		Format:   tar.FormatUSTAR,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// Extract decompresses a tar.xz archive into dir. dest maps each entry name
// to the file name to write it as, or to "" to skip the entry; it is what
// keeps entry names from choosing where files land. Entries marked executable
// in the archive are written owner-executable, the rest owner-readable.
//
// Decoding is pure Go. The xz stream is decoded as a whole, even when entries
// are skipped, and the decoder holds the archive's dictionary in memory while
// it runs.
func Extract(archive []byte, dir string, dest func(entry string) string) error {
	zr, err := xz.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := dest(hdr.Name)
		if hdr.Typeflag != tar.TypeReg || name == "" {
			continue
		}
		perm := os.FileMode(0o600)
		if hdr.Mode&0o100 != 0 {
			perm = 0o700
		}
		if err := writeEntry(filepath.Join(dir, filepath.Base(name)), perm, tr); err != nil {
			return fmt.Errorf("extract %s: %w", hdr.Name, err)
		}
	}
}

// writeEntry writes src to a new file at path, removing it again on failure.
func writeEntry(path string, perm os.FileMode, src io.Reader) (err error) {
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm) //nolint:gosec // G304: path is dir joined with a base name chosen by the caller
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			err = errors.Join(err, os.Remove(path))
		}
	}()
	n, err := io.Copy(out, io.LimitReader(src, maxEntrySize+1))
	if err != nil {
		return err
	}
	if n > maxEntrySize {
		return fmt.Errorf("larger than %d bytes", maxEntrySize)
	}
	return nil
}
