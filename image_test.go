package main

import (
	"bytes"
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
	if len(info.files) != 1 || info.files[0].name != "game.bin" || info.trackCount != 2 || !info.hasDataTrack {
		t.Fatalf("parseCue() = %+v", info)
	}
	if got := *info.files[0].tracks[1].index01; got != 3*60*75+12*75 {
		t.Fatalf("track 2 INDEX 01 = %d", got)
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

func TestInspectImageHashesMultipleFilesInCueOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "game.cue")
	content := `FILE "Track 1.bin" BINARY
  TRACK 01 MODE2/2352
	INDEX 01 00:00:00
FILE "Track 2.bin" BINARY
  TRACK 02 AUDIO
	INDEX 00 00:00:00
	INDEX 01 00:00:01
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	dataSector := mode2Form1Sector()
	pregapSector := bytes.Repeat([]byte{0x33}, int(rawSectorSize))
	audioSector := bytes.Repeat([]byte{0x5a}, int(rawSectorSize))
	if err := os.WriteFile(filepath.Join(dir, "Track 1.bin"), dataSector, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Track 2.bin"), append(pregapSector, audioSector...), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := inspectImage(path)
	if err != nil {
		t.Fatalf("inspectImage() error = %v", err)
	}
	combined := append(bytes.Clone(dataSector), audioSector...)
	wantRaw, wantContent, err := hashImageSectors(bytes.NewReader(combined), 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.byteLength != int64(len(combined)) || got.rawHash != wantRaw || got.contentHash != wantContent {
		t.Fatalf("inspectImage() = %+v, want length %d, raw %x, content %x", got, len(combined), wantRaw, wantContent)
	}
}

func TestInspectImageUsesFileDirectiveInsteadOfSameStemBin(t *testing.T) {
	dir := t.TempDir()
	cuePath := filepath.Join(dir, "game.cue")
	referencedPath := filepath.Join(dir, "actual.bin")
	content := `FILE "actual.bin" BINARY
  TRACK 01 MODE2/2352
	INDEX 01 00:00:00
`
	referencedData := bytes.Repeat([]byte{0x11}, int(rawSectorSize))
	sameStemData := bytes.Repeat([]byte{0x22}, int(rawSectorSize))
	if err := os.WriteFile(cuePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(referencedPath, referencedData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "game.bin"), sameStemData, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := inspectImage(cuePath)
	if err != nil {
		t.Fatalf("inspectImage() error = %v", err)
	}
	if got.rawHash != sha256.Sum256(referencedData) {
		t.Fatalf("inspectImage().rawHash = %x, want %x", got.rawHash, sha256.Sum256(referencedData))
	}
}

func TestParseCueAcceptsUnquotedFileAndWindowsSeparators(t *testing.T) {
	path := writeTestFile(t, "game.cue", "FILE tracks\\game.bin BINARY\n  TRACK 01 MODE2/2352\n    INDEX 01 00:00:00\n")
	info, err := parseCue(path)
	if err != nil {
		t.Fatalf("parseCue() error = %v", err)
	}
	if len(info.files) != 1 || info.files[0].name != `tracks\game.bin` {
		t.Fatalf("parseCue().files = %+v", info.files)
	}
	got := resolveCueFilePath(path, info.files[0].name)
	want := filepath.Join(filepath.Dir(path), "tracks", "game.bin")
	if got != want {
		t.Fatalf("resolveCueFilePath() = %q, want %q", got, want)
	}
}

func TestInspectImageOmitsPregapWithinSharedBin(t *testing.T) {
	dir := t.TempDir()
	cuePath := filepath.Join(dir, "game.cue")
	binPath := filepath.Join(dir, "game.bin")
	content := `FILE "game.bin" BINARY
  TRACK 01 MODE2/2352
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    INDEX 00 00:00:02
    INDEX 01 00:00:03
`
	if err := os.WriteFile(cuePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sectors := make([][]byte, 5)
	for index := range sectors {
		sectors[index] = bytes.Repeat([]byte{byte(index + 1)}, int(rawSectorSize))
	}
	allSectors := bytes.Join(sectors, nil)
	if err := os.WriteFile(binPath, allSectors, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := inspectImage(cuePath)
	if err != nil {
		t.Fatalf("inspectImage() error = %v", err)
	}
	wantData := bytes.Join([][]byte{sectors[0], sectors[1], sectors[3], sectors[4]}, nil)
	wantRaw, wantContent, err := hashImageSectors(bytes.NewReader(wantData), 4)
	if err != nil {
		t.Fatal(err)
	}
	if got.byteLength != int64(len(wantData)) || got.rawHash != wantRaw || got.contentHash != wantContent {
		t.Fatalf("inspectImage() = %+v, want length %d, raw %x, content %x", got, len(wantData), wantRaw, wantContent)
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
