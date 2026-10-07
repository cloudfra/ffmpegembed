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

// Command mkffmpegembed builds the two artifacts ffmpegembed is fed with:
//
//   - an archive: the tar.xz of ffmpeg, ffprobe and their license that a Go
//     program embeds (go:embed) and hands to ffmpegembed;
//   - a manifest: the JSON file that pins an ffmpeg release, recording where
//     each platform's files are downloaded from and their SHA-256 digests.
//
// Examples:
//
//	# An archive from files you already have.
//	mkffmpegembed archive -ffmpeg ./ffmpeg -ffprobe ./ffprobe -license ./LICENSE -o ffmpeg.tar.xz
//
//	# A manifest for a release, hashing every file it lists.
//	mkffmpegembed manifest -version b6.1.1 -license GPL-3.0-or-later \
//	    -base-url https://github.com/eugeneware/ffmpeg-static/releases/download/{version} \
//	    -platform linux_amd64=ffmpeg-linux-x64.gz,ffprobe-linux-x64.gz,linux-x64.LICENSE.gz \
//	    -o manifest.json
//
//	# The same manifest moved to another release.
//	mkffmpegembed manifest -from manifest.json -version b7.0 -o manifest.json
//
//	# An archive of what a manifest pins for one platform (downloaded and verified).
//	mkffmpegembed archive -manifest manifest.json -platform linux_amd64 -o ffmpeg.tar.xz
//
// Archives are compressed by the xz program at its highest setting, so xz must
// be installed.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cloudfra/ffmpegembed/internal/archive"
	"github.com/cloudfra/ffmpegembed/internal/download"
	"github.com/cloudfra/ffmpegembed/internal/manifest"
)

const usage = `mkffmpegembed builds the inputs of ffmpegembed.

Usage:
  mkffmpegembed archive  [flags]   Build a tar.xz archive of ffmpeg, ffprobe and their license.
  mkffmpegembed manifest [flags]   Build a JSON manifest pinning a release's downloads.

Run "mkffmpegembed <command> -h" for the flags of a command.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkffmpegembed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		say(stderr, "%s", usage)
		return errors.New("missing command (archive|manifest)")
	}
	switch args[0] {
	case "archive":
		return cmdArchive(ctx, args[1:], stdout, stderr)
	case "manifest":
		return cmdManifest(ctx, args[1:], stdout, stderr)
	case "help", "-h", "-help", "--help":
		say(stdout, "%s", usage)
		return nil
	}
	say(stderr, "%s", usage)
	return fmt.Errorf("unknown command %q", args[0])
}

// say prints progress or a result to w.
func say(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, format, args...) //nolint:errcheck // nothing useful to do when the terminal is gone
}

// parse parses args into fs, reporting a request for help as done rather than
// as an error.
func parse(fs *flag.FlagSet, args []string) (done bool, err error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, nil
		}
		return true, err
	}
	if fs.NArg() > 0 {
		return true, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return false, nil
}

func cmdArchive(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mkffmpegembed archive", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ffmpeg := fs.String("ffmpeg", "", "path of the ffmpeg binary to archive")
	ffprobe := fs.String("ffprobe", "", "path of the ffprobe binary to archive")
	license := fs.String("license", "", "path of the license text the binaries are distributed under")
	manifestPath := fs.String("manifest", "", "instead of -ffmpeg/-ffprobe/-license: download and verify the files this manifest pins")
	platform := fs.String("platform", manifest.Key(runtime.GOOS, runtime.GOARCH), "with -manifest: the <goos>_<goarch> to archive")
	out := fs.String("o", "ffmpeg.tar.xz", "archive to write")
	if done, err := parse(fs, args); done {
		return err
	}

	files := archive.Files{Ffmpeg: *ffmpeg, Ffprobe: *ffprobe, License: *license}
	local := *ffmpeg != "" || *ffprobe != "" || *license != ""
	switch {
	case *manifestPath != "" && local:
		return errors.New("use either -manifest or -ffmpeg/-ffprobe/-license, not both")
	case *manifestPath != "":
		work, err := os.MkdirTemp("", "mkffmpegembed-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(work) //nolint:errcheck // best-effort cleanup of a temp dir
		if files, err = fetchPlatform(ctx, *manifestPath, *platform, work, stderr); err != nil {
			return err
		}
	case *ffmpeg == "" || *ffprobe == "" || *license == "":
		return errors.New("-ffmpeg, -ffprobe and -license are all required (or use -manifest)")
	}

	// Write beside the destination and rename, so a failed run leaves no
	// partial archive behind.
	tmp, err := os.CreateTemp(filepath.Dir(*out), filepath.Base(*out)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // a no-op once the file has been renamed
	say(stderr, "compressing with xz -9e (this can take a minute or two)\n")
	sum := sha256.New()
	err = archive.Create(ctx, io.MultiWriter(tmp, sum), files)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { //nolint:gosec // G302: an archive meant to be committed and shared
		return err
	}
	if err := os.Rename(tmp.Name(), *out); err != nil {
		return err
	}
	fi, err := os.Stat(*out)
	if err != nil {
		return err
	}
	say(stdout, "%s  %d bytes  sha256:%s\n", *out, fi.Size(), hex.EncodeToString(sum.Sum(nil)))
	return nil
}

// fetchPlatform downloads the files the manifest at manifestPath pins for
// platform into dir, verifying every digest, and returns where they are.
func fetchPlatform(ctx context.Context, manifestPath, platform, dir string, stderr io.Writer) (archive.Files, error) {
	data, err := os.ReadFile(manifestPath) //nolint:gosec // G304: a path given on the command line
	if err != nil {
		return archive.Files{}, err
	}
	m, err := manifest.Parse(data)
	if err != nil {
		return archive.Files{}, err
	}
	p, ok := m.Platforms[platform]
	if !ok {
		return archive.Files{}, fmt.Errorf("%s has no platform %q (it has: %s)", manifestPath, platform, strings.Join(m.Keys(), ", "))
	}
	paths := map[string]string{}
	for _, name := range []string{manifest.Ffmpeg, manifest.Ffprobe, manifest.License} {
		asset, _ := p.Asset(name)
		say(stderr, "downloading %s\n", m.URL(asset))
		gz := filepath.Join(dir, name+".gz")
		if _, err := download.File(ctx, http.DefaultClient, m.URL(asset), gz, asset.SHA256); err != nil {
			return archive.Files{}, err
		}
		paths[name] = filepath.Join(dir, name)
		digest, err := download.Gunzip(paths[name], gz, 0o600)
		if err != nil {
			return archive.Files{}, err
		}
		if digest != asset.BinarySHA256 {
			return archive.Files{}, fmt.Errorf("%s: %w: decompressed sha256 is %s, manifest pins %s", asset.File, download.ErrChecksum, digest, asset.BinarySHA256)
		}
	}
	return archive.Files{Ffmpeg: paths[manifest.Ffmpeg], Ffprobe: paths[manifest.Ffprobe], License: paths[manifest.License]}, nil
}

// platformFlag collects repeated -platform <goos>_<goarch>=ffmpeg,ffprobe,license flags.
type platformFlag map[string]manifest.Platform

func (p platformFlag) String() string { return "" }

func (p platformFlag) Set(v string) error {
	key, files, ok := strings.Cut(v, "=")
	names := strings.Split(files, ",")
	if !ok || key == "" || len(names) != 3 {
		return errors.New("want <goos>_<goarch>=<ffmpeg file>,<ffprobe file>,<license file>")
	}
	p[key] = manifest.Platform{
		Ffmpeg:  manifest.Asset{File: strings.TrimSpace(names[0])},
		Ffprobe: manifest.Asset{File: strings.TrimSpace(names[1])},
		License: manifest.Asset{File: strings.TrimSpace(names[2])},
	}
	return nil
}

func cmdManifest(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mkffmpegembed manifest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "existing manifest to start from; flags below override what it says")
	version := fs.String("version", "", "release tag to pin")
	baseURL := fs.String("base-url", "", "URL prefix of the release's files; {version} is replaced by -version")
	license := fs.String("license", "", "SPDX expression of the license the builds are distributed under")
	platforms := platformFlag{}
	fs.Var(platforms, "platform", "<goos>_<goarch>=<ffmpeg file>,<ffprobe file>,<license file>; each a single gzip-compressed file under -base-url (repeatable)")
	out := fs.String("o", "manifest.json", "manifest to write")
	if done, err := parse(fs, args); done {
		return err
	}

	m := &manifest.Manifest{Platforms: map[string]manifest.Platform{}}
	if *from != "" {
		data, err := os.ReadFile(*from) //nolint:gosec // G304: a path given on the command line
		if err != nil {
			return err
		}
		if m, err = manifest.Parse(data); err != nil {
			return err
		}
	}
	oldVersion := m.Version
	if *version != "" {
		m.Version = *version
	}
	if *license != "" {
		m.License = *license
	}
	switch {
	case *baseURL != "":
		m.BaseURL = strings.ReplaceAll(*baseURL, "{version}", m.Version)
	case oldVersion != "" && oldVersion != m.Version:
		// Moving an existing manifest to another release: its URL names the
		// old one.
		if !strings.Contains(m.BaseURL, oldVersion) {
			return fmt.Errorf("base_url %q does not contain the old version %q; pass -base-url", m.BaseURL, oldVersion)
		}
		m.BaseURL = strings.ReplaceAll(m.BaseURL, oldVersion, m.Version)
	}
	for key, p := range platforms {
		m.Platforms[key] = p
	}
	if m.Version == "" || m.License == "" || m.BaseURL == "" || len(m.Platforms) == 0 {
		return errors.New("-version, -license, -base-url and at least one -platform are required (or start -from a manifest)")
	}

	work, err := os.MkdirTemp("", "mkffmpegembed-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work) //nolint:errcheck // best-effort cleanup of a temp dir

	// Every digest is recomputed from a fresh download; none is carried over.
	hash := func(asset manifest.Asset) (manifest.Asset, error) {
		say(stderr, "hashing %s\n", m.URL(asset))
		gz := filepath.Join(work, "download.gz")
		var err error
		if asset.SHA256, err = download.File(ctx, http.DefaultClient, m.URL(asset), gz, ""); err != nil {
			return asset, err
		}
		asset.BinarySHA256, err = download.Gunzip(filepath.Join(work, "download"), gz, 0o600)
		return asset, err
	}
	for _, key := range m.Keys() {
		p := m.Platforms[key]
		if p.Ffmpeg, err = hash(p.Ffmpeg); err != nil {
			return err
		}
		if p.Ffprobe, err = hash(p.Ffprobe); err != nil {
			return err
		}
		if p.License, err = hash(p.License); err != nil {
			return err
		}
		m.Platforms[key] = p
	}

	data, err := m.Marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil { //nolint:gosec // G306: a manifest meant to be committed and shared
		return err
	}
	say(stdout, "%s  version %s, %d platform(s)\n", *out, m.Version, len(m.Platforms))
	return nil
}
