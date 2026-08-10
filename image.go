package main

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const rawSectorSize int64 = 2352

type image struct {
	cuePath     string
	byteLength  int64
	rawHash     [sha256.Size]byte
	contentHash [sha256.Size]byte
}

type cueInfo struct {
	fileCount    int
	trackCount   int
	hasDataTrack bool
}

func inspectImage(cuePath string) (image, error) {
	absCuePath, err := filepath.Abs(cuePath)
	if err != nil {
		return image{}, fmt.Errorf("resolve CUE path: %w", err)
	}

	info, err := parseCue(absCuePath)
	if err != nil {
		return image{}, err
	}
	if info.fileCount != 1 {
		return image{}, fmt.Errorf("CUE must contain exactly one FILE directive; found %d", info.fileCount)
	}
	if info.trackCount == 0 {
		return image{}, errors.New("CUE contains no TRACK directives")
	}
	if !info.hasDataTrack {
		return image{}, errors.New("CUE contains no MODE1/2352 or MODE2/2352 data track")
	}

	binPath, err := resolveBinPath(absCuePath)
	if err != nil {
		return image{}, err
	}
	rawHash, contentHash, byteLength, err := hashImageFile(binPath)
	if err != nil {
		return image{}, fmt.Errorf("hash BIN: %w", err)
	}

	return image{
		cuePath:     absCuePath,
		byteLength:  byteLength,
		rawHash:     rawHash,
		contentHash: contentHash,
	}, nil
}

func parseCue(path string) (cueInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return cueInfo{}, fmt.Errorf("open CUE: %w", err)
	}
	defer file.Close()

	var info cueInfo
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		switch strings.ToUpper(fields[0]) {
		case "REM":
			continue
		case "FILE":
			if len(fields) < 3 {
				return cueInfo{}, fmt.Errorf("CUE line %d: malformed FILE directive", lineNumber)
			}
			if !strings.EqualFold(fields[len(fields)-1], "BINARY") {
				return cueInfo{}, fmt.Errorf("CUE line %d: FILE type must be BINARY", lineNumber)
			}
			info.fileCount++
		case "TRACK":
			if len(fields) != 3 {
				return cueInfo{}, fmt.Errorf("CUE line %d: malformed TRACK directive", lineNumber)
			}
			mode := strings.ToUpper(fields[2])
			switch mode {
			case "MODE1/2352", "MODE2/2352":
				info.hasDataTrack = true
			case "AUDIO":
			default:
				return cueInfo{}, fmt.Errorf("CUE line %d: unsupported track mode %q; expected MODE1/2352, MODE2/2352, or AUDIO", lineNumber, fields[2])
			}
			info.trackCount++
		}
	}
	if err := scanner.Err(); err != nil {
		return cueInfo{}, fmt.Errorf("read CUE: %w", err)
	}
	return info, nil
}

func resolveBinPath(cuePath string) (string, error) {
	base := strings.TrimSuffix(cuePath, filepath.Ext(cuePath))
	candidates := []string{base + ".bin", base + ".BIN"}
	for _, candidate := range candidates {
		stat, err := os.Stat(candidate)
		switch {
		case err == nil && !stat.IsDir():
			return candidate, nil
		case err == nil:
			continue
		case !errors.Is(err, os.ErrNotExist):
			return "", fmt.Errorf("inspect BIN %q: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("BIN not found; expected %q or %q", candidates[0], candidates[1])
}

func hashImageFile(path string) ([sha256.Size]byte, [sha256.Size]byte, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, [sha256.Size]byte{}, 0, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return [sha256.Size]byte{}, [sha256.Size]byte{}, 0, err
	}
	if stat.Size()%rawSectorSize != 0 {
		return [sha256.Size]byte{}, [sha256.Size]byte{}, 0, fmt.Errorf("BIN size %d is not a multiple of %d bytes", stat.Size(), rawSectorSize)
	}

	rawHash, contentHash, err := hashImageSectors(file, stat.Size()/rawSectorSize)
	return rawHash, contentHash, stat.Size(), err
}
