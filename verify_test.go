package main

import (
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

	result, err := verifyReadback(discPath, sha256.Sum256(source), int64(len(source)), 150)
	if err != nil {
		t.Fatalf("verifyReadback() error = %v", err)
	}
	if !result.matched || result.offset != 150 {
		t.Fatalf("verifyReadback() = %+v, want match at offset 150", result)
	}
}
