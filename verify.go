package main

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"strconv"
	"strings"
)

type verificationResult struct {
	discRawHash     [sha256.Size]byte
	discContentHash [sha256.Size]byte
	offset          int64
	matched         bool
	rawMatched      bool
}

func verifyReadback(
	discPath string,
	sourceRawHash [sha256.Size]byte,
	sourceContentHash [sha256.Size]byte,
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

		rawHash, err := hashSection(disc, candidate*rawSectorSize, sourceBytes)
		if err != nil {
			return verificationResult{}, fmt.Errorf("hash disc read-back at sector %d: %w", candidate, err)
		}
		if !tested {
			result.discRawHash = rawHash
			result.offset = candidate
			tested = true
		}
		if rawHash == sourceRawHash {
			result.discRawHash = rawHash
			result.offset = candidate
			result.matched = true
			result.rawMatched = true
			return result, nil
		}
	}
	if !tested {
		return verificationResult{}, errors.New("disc read-back does not contain enough sectors for the source image")
	}

	for _, candidate := range candidates {
		if candidate < 0 || candidate+sourceSectors > discSectors {
			continue
		}
		rawHash, contentHash, err := hashImageSectors(
			io.NewSectionReader(disc, candidate*rawSectorSize, sourceBytes),
			sourceSectors,
		)
		if err != nil {
			return verificationResult{}, fmt.Errorf("hash logical disc content at sector %d: %w", candidate, err)
		}
		if result.discContentHash == ([sha256.Size]byte{}) {
			result.discContentHash = contentHash
		}
		if contentHash == sourceContentHash {
			result.discRawHash = rawHash
			result.discContentHash = contentHash
			result.offset = candidate
			result.matched = true
			return result, nil
		}
	}
	return result, nil
}

func hashImageSectors(reader io.Reader, sectorCount int64) ([sha256.Size]byte, [sha256.Size]byte, error) {
	rawHasher := sha256.New()
	contentHasher := sha256.New()
	if err := hashImageSectorsInto(reader, sectorCount, 0, rawHasher, contentHasher); err != nil {
		return [sha256.Size]byte{}, [sha256.Size]byte{}, err
	}
	return hashSum(rawHasher), hashSum(contentHasher), nil
}

func hashImageSectorsInto(
	reader io.Reader,
	sectorCount int64,
	sectorOffset int64,
	rawHasher hash.Hash,
	contentHasher hash.Hash,
) error {
	buffered := bufio.NewReaderSize(reader, 1024*1024)
	sector := make([]byte, rawSectorSize)
	for sectorNumber := int64(0); sectorNumber < sectorCount; sectorNumber++ {
		if _, err := io.ReadFull(buffered, sector); err != nil {
			return fmt.Errorf("read sector %d: %w", sectorOffset+sectorNumber, err)
		}
		_, _ = rawHasher.Write(sector)
		hashSectorContent(contentHasher, sector)
	}
	return nil
}

func hashSectorContent(hasher hash.Hash, sector []byte) {
	mode := rawSectorMode(sector)
	switch mode {
	case 1:
		_, _ = hasher.Write([]byte{1})
		_, _ = hasher.Write(sector[16:2064])
	case 2:
		if (sector[18]^sector[22])&0x20 != 0 {
			_, _ = hasher.Write([]byte{0})
			_, _ = hasher.Write(sector)
			return
		}
		form2 := sector[18]&0x20 != 0
		if form2 {
			_, _ = hasher.Write([]byte{3})
			_, _ = hasher.Write(sector[16:2348])
			return
		}
		_, _ = hasher.Write([]byte{2})
		_, _ = hasher.Write(sector[16:2072])
	default:
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(sector)
	}
}

func rawSectorMode(sector []byte) byte {
	if len(sector) != int(rawSectorSize) || sector[0] != 0 || sector[11] != 0 {
		return 0
	}
	for _, value := range sector[1:11] {
		if value != 0xff {
			return 0
		}
	}
	if sector[15] != 1 && sector[15] != 2 {
		return 0
	}
	return sector[15]
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
