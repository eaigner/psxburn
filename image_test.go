package main

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCueAcceptsRawDataAndAudioTracks(t *testing.T) {
	path := writeTestFile(t, "game.cue", `FILE "game.bin" BINARY
  TRACK 01 MODE2/2352
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    INDEX 01 03:12:00
`)

	info, err := parseCue(path)
	if err != nil {
		t.Fatalf("parseCue() error = %v", err)
	}
	if info.fileCount != 1 || info.trackCount != 2 || !info.hasDataTrack {
		t.Fatalf("parseCue() = %+v", info)
	}
}

func TestParseCueRejectsNonRawTrack(t *testing.T) {
	path := writeTestFile(t, "game.cue", `FILE "game.bin" BINARY
  TRACK 01 MODE1/2048
`)

	_, err := parseCue(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported track mode") {
		t.Fatalf("parseCue() error = %v, want unsupported track mode", err)
	}
}

func TestInspectImageRejectsMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "game.cue")
	content := `FILE "track1.bin" BINARY
  TRACK 01 MODE2/2352
FILE "track2.bin" BINARY
  TRACK 02 AUDIO
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := inspectImage(path)
	if err == nil || !strings.Contains(err.Error(), "exactly one FILE") {
		t.Fatalf("inspectImage() error = %v, want exactly one FILE", err)
	}
}

func TestInspectImageUsesSameStemBinInsteadOfFileDirective(t *testing.T) {
	dir := t.TempDir()
	cuePath := filepath.Join(dir, "game.cue")
	binPath := filepath.Join(dir, "game.bin")
	content := `FILE "a-different-name.bin" BINARY
  TRACK 01 MODE2/2352
`
	data := make([]byte, rawSectorSize)
	if err := os.WriteFile(cuePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := inspectImage(cuePath)
	if err != nil {
		t.Fatalf("inspectImage() error = %v", err)
	}
	if got.rawHash != sha256.Sum256(data) {
		t.Fatalf("inspectImage().rawHash = %x, want %x", got.rawHash, sha256.Sum256(data))
	}
}

func TestResolveBinPathPrefersLowercaseExtension(t *testing.T) {
	dir := t.TempDir()
	cuePath := filepath.Join(dir, "game.cue")
	lowerPath := filepath.Join(dir, "game.bin")
	upperPath := filepath.Join(dir, "game.BIN")
	for _, path := range []string{lowerPath, upperPath} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := resolveBinPath(cuePath)
	if err != nil {
		t.Fatalf("resolveBinPath() error = %v", err)
	}
	if got != lowerPath {
		t.Fatalf("resolveBinPath() = %q, want %q", got, lowerPath)
	}
}

func writeTestFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
