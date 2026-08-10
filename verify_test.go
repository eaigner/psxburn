package main

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseMSF(t *testing.T) {
	got, err := parseMSF("01:02:03")
	if err != nil {
		t.Fatalf("parseMSF() error = %v", err)
	}
	want := int64(1*60*75 + 2*75 + 3)
	if got != want {
		t.Fatalf("parseMSF() = %d, want %d", got, want)
	}

	for _, value := range []string{"1:60:00", "1:00:75", "bad"} {
		if _, err := parseMSF(value); err == nil {
			t.Errorf("parseMSF(%q) unexpectedly succeeded", value)
		}
	}
}

func TestCandidateOffsetsDeduplicatesInOrder(t *testing.T) {
	want := []int64{150, 10, 0}
	if got := candidateOffsets(150, 10, 0, 150); !reflect.DeepEqual(got, want) {
		t.Fatalf("candidateOffsets() = %v, want %v", got, want)
	}
}

func TestVerifyReadbackFindsRawSectorOffset(t *testing.T) {
	source := make([]byte, 2*rawSectorSize)
	for index := range source {
		source[index] = byte(index % 251)
	}
	prefix := make([]byte, 150*rawSectorSize)
	for index := range prefix {
		prefix[index] = 0xff
	}
	disc := append(prefix, source...)

	discPath := filepath.Join(t.TempDir(), "disc.bin")
	if err := os.WriteFile(discPath, disc, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := verifyReadback(discPath, sha256.Sum256(source), [sha256.Size]byte{}, int64(len(source)), 150)
	if err != nil {
		t.Fatalf("verifyReadback() error = %v", err)
	}
	if !result.matched || result.offset != 150 {
		t.Fatalf("verifyReadback() = %+v, want match at offset 150", result)
	}
	if !result.rawMatched {
		t.Fatal("verifyReadback() reported a logical match, want raw match")
	}
}

func TestVerifyReadbackAcceptsRegeneratedMode2IntegrityBytes(t *testing.T) {
	source := mode2Form1Sector()
	disc := bytes.Clone(source)
	for offset := 2128; offset < len(disc); offset++ {
		disc[offset] ^= byte(offset)
	}

	_, sourceContentHash, err := hashImageSectors(bytes.NewReader(source), 1)
	if err != nil {
		t.Fatal(err)
	}
	discPath := filepath.Join(t.TempDir(), "disc.bin")
	if err := os.WriteFile(discPath, disc, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := verifyReadback(
		discPath,
		sha256.Sum256(source),
		sourceContentHash,
		int64(len(source)),
		0,
	)
	if err != nil {
		t.Fatalf("verifyReadback() error = %v", err)
	}
	if !result.matched || result.rawMatched {
		t.Fatalf("verifyReadback() = %+v, want logical-only match", result)
	}
	if result.discContentHash != sourceContentHash {
		t.Fatalf("disc content hash = %x, want %x", result.discContentHash, sourceContentHash)
	}
}

func TestVerifyReadbackRejectsMode2PayloadDifference(t *testing.T) {
	source := mode2Form1Sector()
	disc := bytes.Clone(source)
	disc[24] ^= 0xff

	_, sourceContentHash, err := hashImageSectors(bytes.NewReader(source), 1)
	if err != nil {
		t.Fatal(err)
	}
	discPath := filepath.Join(t.TempDir(), "disc.bin")
	if err := os.WriteFile(discPath, disc, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := verifyReadback(
		discPath,
		sha256.Sum256(source),
		sourceContentHash,
		int64(len(source)),
		0,
	)
	if err != nil {
		t.Fatalf("verifyReadback() error = %v", err)
	}
	if result.matched {
		t.Fatalf("verifyReadback() = %+v, want mismatch", result)
	}
}

func TestContentHashIgnoresDataSectorIntegrityRegions(t *testing.T) {
	mode1 := mode1Sector()
	mode2Form1 := mode2Form1Sector()
	mode2Form2 := mode2Form1Sector()
	mode2Form2[18] |= 0x20
	mode2Form2[22] |= 0x20
	for offset := 2072; offset < 2348; offset++ {
		mode2Form2[offset] = byte(offset % 239)
	}

	tests := []struct {
		name           string
		sector         []byte
		integrityStart int
	}{
		{name: "mode1", sector: mode1, integrityStart: 2064},
		{name: "mode2 form1", sector: mode2Form1, integrityStart: 2072},
		{name: "mode2 form2", sector: mode2Form2, integrityStart: 2348},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			disc := bytes.Clone(test.sector)
			for offset := test.integrityStart; offset < len(disc); offset++ {
				disc[offset] ^= 0x5a
			}

			sourceRaw, sourceContent, err := hashImageSectors(bytes.NewReader(test.sector), 1)
			if err != nil {
				t.Fatal(err)
			}
			discRaw, discContent, err := hashImageSectors(bytes.NewReader(disc), 1)
			if err != nil {
				t.Fatal(err)
			}
			if sourceRaw == discRaw {
				t.Fatal("raw hashes unexpectedly match")
			}
			if sourceContent != discContent {
				t.Fatalf("content hashes differ: source=%x disc=%x", sourceContent, discContent)
			}
		})
	}
}

func mode2Form1Sector() []byte {
	sector := make([]byte, rawSectorSize)
	for offset := 1; offset <= 10; offset++ {
		sector[offset] = 0xff
	}
	sector[12] = 0x12
	sector[13] = 0x34
	sector[14] = 0x56
	sector[15] = 2
	copy(sector[16:20], []byte{0, 0, 0x08, 0})
	copy(sector[20:24], sector[16:20])
	for offset := 24; offset < 2072; offset++ {
		sector[offset] = byte(offset % 251)
	}
	for offset := 2072; offset < len(sector); offset++ {
		sector[offset] = byte(offset)
	}
	return sector
}

func mode1Sector() []byte {
	sector := make([]byte, rawSectorSize)
	for offset := 1; offset <= 10; offset++ {
		sector[offset] = 0xff
	}
	sector[12] = 0x12
	sector[13] = 0x34
	sector[14] = 0x56
	sector[15] = 1
	for offset := 16; offset < 2064; offset++ {
		sector[offset] = byte(offset % 251)
	}
	for offset := 2064; offset < len(sector); offset++ {
		sector[offset] = byte(offset)
	}
	return sector
}
