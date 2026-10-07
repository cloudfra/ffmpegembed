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

package download

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func sha(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func gz(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func serve(t *testing.T, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/file" {
			http.NotFound(w, r)
			return
		}
		if _, err := w.Write(body); err != nil {
			t.Errorf("serve %s: %v", r.URL.Path, err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFile(t *testing.T) {
	body := []byte("downloaded content")
	url := serve(t, body)
	path := filepath.Join(t.TempDir(), "out")

	// With no expected digest the file is kept and its digest reported.
	got, err := File(context.Background(), nil, url+"/file", path, "")
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	if got != sha(body) {
		t.Errorf("File digest = %s; want %s", got, sha(body))
	}
	// With the right digest it succeeds.
	if _, err := File(context.Background(), nil, url+"/file", path, sha(body)); err != nil {
		t.Errorf("File with the correct digest: %v", err)
	}
	// With the wrong one it fails and leaves nothing behind.
	if _, err := File(context.Background(), nil, url+"/file", path, sha([]byte("other"))); !errors.Is(err, ErrChecksum) {
		t.Errorf("File with a wrong digest = %v; want ErrChecksum", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a rejected download was left at %s (stat error: %v)", path, err)
	}
	// An HTTP error is reported.
	if _, err := File(context.Background(), nil, url+"/missing", path, ""); err == nil {
		t.Error("File of a 404 URL succeeded; want an error")
	}
}

func TestGunzip(t *testing.T) {
	dir := t.TempDir()
	want := []byte("the binary")
	src := filepath.Join(dir, "in.gz")
	if err := os.WriteFile(src, gz(t, want), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out")
	digest, err := Gunzip(dst, src, 0o700)
	if err != nil {
		t.Fatalf("Gunzip: %v", err)
	}
	if digest != sha(want) {
		t.Errorf("Gunzip digest = %s; want %s", digest, sha(want))
	}
	got, err := os.ReadFile(dst) //nolint:gosec // G304: path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Gunzip wrote %q; want %q", got, want)
	}

	// A file that is not gzip is rejected.
	if err := os.WriteFile(src, []byte("plain"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Gunzip(filepath.Join(dir, "bad"), src, 0o600); err == nil {
		t.Error("Gunzip of a non-gzip file succeeded; want an error")
	}
}
