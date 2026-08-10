package main

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type verificationResult struct {
	discHash [sha256.Size]byte
	offset   int64
	matched  bool
}

func verifyReadback(
	discPath string,
	sourceHash [sha256.Size]byte,
	sourceBytes int64,
	start int64,
) (verificationResult, error) {
	disc, err := os.Open(discPath)
	if err != nil {
		return verificationResult{}, fmt.Errorf("open disc read-back: %w", err)
	}
	defer disc.Close()

	stat, err := disc.Stat()
	if err != nil {
		return verificationResult{}, fmt.Errorf("inspect disc read-back: %w", err)
	}
	if stat.Size()%rawSectorSize != 0 {
		return verificationResult{}, fmt.Errorf("disc read-back size %d is not a multiple of %d bytes", stat.Size(), rawSectorSize)
	}

	sourceSectors := sourceBytes / rawSectorSize
	discSectors := stat.Size() / rawSectorSize
	candidates := candidateOffsets(start, discSectors-sourceSectors, 0, 150)

	var result verificationResult
	var tested bool
	for _, candidate := range candidates {
		if candidate < 0 || candidate+sourceSectors > discSectors {
			continue
		}

		hash, err := hashSection(disc, candidate*rawSectorSize, sourceBytes)
		if err != nil {
			return verificationResult{}, fmt.Errorf("hash disc read-back at sector %d: %w", candidate, err)
		}
		if !tested {
			result.discHash = hash
			result.offset = candidate
			tested = true
		}
		if hash == sourceHash {
			result.discHash = hash
			result.offset = candidate
			result.matched = true
			return result, nil
		}
	}
	if !tested {
		return verificationResult{}, errors.New("disc read-back does not contain enough sectors for the source image")
	}
	return result, nil
}

func candidateOffsets(offsets ...int64) []int64 {
	seen := make(map[int64]bool, len(offsets))
	unique := make([]int64, 0, len(offsets))
	for _, offset := range offsets {
		if !seen[offset] {
			seen[offset] = true
			unique = append(unique, offset)
		}
	}
	return unique
}

func hashSection(file *os.File, offset, length int64) ([sha256.Size]byte, error) {
	hasher := sha256.New()
	written, err := io.Copy(hasher, io.NewSectionReader(file, offset, length))
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	if written != length {
		return [sha256.Size]byte{}, io.ErrUnexpectedEOF
	}
	var sum [sha256.Size]byte
	copy(sum[:], hasher.Sum(nil))
	return sum, nil
}

func readTOCStart(path string) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open read-back TOC: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || !strings.EqualFold(fields[0], "START") {
			continue
		}
		sectors, err := parseMSF(fields[1])
		if err != nil {
			return 0, fmt.Errorf("read-back TOC line %d: %w", lineNumber, err)
		}
		return sectors, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read read-back TOC: %w", err)
	}
	return 0, nil
}

func parseMSF(value string) (int64, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid MSF value %q", value)
	}
	values := make([]int64, 3)
	for index, part := range parts {
		parsed, err := strconv.ParseInt(part, 10, 64)
		if err != nil || parsed < 0 {
			return 0, fmt.Errorf("invalid MSF value %q", value)
		}
		values[index] = parsed
	}
	if values[1] >= 60 || values[2] >= 75 {
		return 0, fmt.Errorf("invalid MSF value %q", value)
	}
	return values[0]*60*75 + values[1]*75 + values[2], nil
}
