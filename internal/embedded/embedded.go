// Copyright 2026 Cloudfra
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed by the user in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package embedded carries the ffmpeg/ffprobe binaries that ffmpegembed ships
// with the Go module. They are committed under bin/<goos>_<goarch>/ as one
// ffmpeg.tar.xz per platform and linked in with go:embed by the per-platform
// files in this package, so a plain `go get` of the module is enough to build
// with them.
//
// Each archive holds three entries: ffmpeg, ffprobe and the LICENSE they are
// distributed under. The binaries are the ones internal/download fetches for
// platforms without an embedded copy; scripts/update-ffmpeg.sh pins both to
// one release and builds the archives.
//
// Builds for a GOOS/GOARCH pair that has no embedded binary use a stub
// (embed_other.go) where HasEmbedded() is false.
package embedded

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ulikunitz/xz"
)

// Names of the entries in an archive.
const (
	Ffmpeg  = "ffmpeg"
	Ffprobe = "ffprobe"
	License = "LICENSE"
)

// maxEntrySize bounds one extracted entry, so a corrupt archive cannot fill
// the disk.
const maxEntrySize = 1 << 30

// Archive returns the embedded tar.xz archive, or nil when none is available
// for the current GOOS/GOARCH.
func Archive() []byte { return archive }

// HasEmbedded reports whether embedded binaries are linked into this build.
func HasEmbedded() bool { return len(archive) > 0 }

// Extract decompresses a tar.xz archive into dir. dest maps each entry name
// to the file name to write it as, or to "" to skip the entry; it is what
// keeps entry names from choosing where files land. Entries marked executable
// in the archive are written owner-executable, the rest owner-readable.
//
// The xz stream is decoded as a whole, even when entries are skipped, and the
// decoder holds the archive's dictionary (about 100 MB) in memory while it
// runs.
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
