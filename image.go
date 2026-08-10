package main

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const rawSectorSize int64 = 2352

type image struct {
	cuePath     string
	byteLength  int64
	rawHash     [sha256.Size]byte
	contentHash [sha256.Size]byte
	readRanges  []sectorRange
}

type sectorRange struct {
	start int64
	count int64
}

type cueInfo struct {
	files        []cueFile
	trackCount   int
	hasDataTrack bool
}

type cueFile struct {
	name   string
	tracks []cueTrack
}

type cueTrack struct {
	number  int
	mode    string
	index00 *int64
	index01 *int64
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
	if len(info.files) == 0 {
		return image{}, errors.New("CUE contains no FILE directives")
	}
	if info.trackCount == 0 {
		return image{}, errors.New("CUE contains no TRACK directives")
	}
	if !info.hasDataTrack {
		return image{}, errors.New("CUE contains no MODE1/2352 or MODE2/2352 data track")
	}

	rawHash, contentHash, byteLength, readRanges, err := hashCueImage(absCuePath, info)
	if err != nil {
		return image{}, err
	}

	return image{
		cuePath:     absCuePath,
		byteLength:  byteLength,
		rawHash:     rawHash,
		contentHash: contentHash,
		readRanges:  readRanges,
	}, nil
}

func parseCue(path string) (cueInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return cueInfo{}, fmt.Errorf("open CUE: %w", err)
	}
	defer file.Close()

	var info cueInfo
	fileIndex := -1
	trackIndex := -1
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
			name, err := parseCueFile(line)
			if err != nil {
				return cueInfo{}, fmt.Errorf("CUE line %d: %w", lineNumber, err)
			}
			info.files = append(info.files, cueFile{name: name})
			fileIndex = len(info.files) - 1
			trackIndex = -1
		case "TRACK":
			if len(fields) != 3 {
				return cueInfo{}, fmt.Errorf("CUE line %d: malformed TRACK directive", lineNumber)
			}
			if fileIndex < 0 {
				return cueInfo{}, fmt.Errorf("CUE line %d: TRACK appears before FILE", lineNumber)
			}
			number, err := strconv.Atoi(fields[1])
			if err != nil || number < 1 || number > 99 {
				return cueInfo{}, fmt.Errorf("CUE line %d: invalid track number %q", lineNumber, fields[1])
			}
			mode := strings.ToUpper(fields[2])
			switch mode {
			case "MODE1/2352", "MODE2/2352":
				info.hasDataTrack = true
			case "AUDIO":
			default:
				return cueInfo{}, fmt.Errorf("CUE line %d: unsupported track mode %q; expected MODE1/2352, MODE2/2352, or AUDIO", lineNumber, fields[2])
			}
			info.files[fileIndex].tracks = append(info.files[fileIndex].tracks, cueTrack{
				number: number,
				mode:   mode,
			})
			trackIndex = len(info.files[fileIndex].tracks) - 1
			info.trackCount++
		case "INDEX":
			if len(fields) != 3 {
				return cueInfo{}, fmt.Errorf("CUE line %d: malformed INDEX directive", lineNumber)
			}
			if fileIndex < 0 || trackIndex < 0 {
				return cueInfo{}, fmt.Errorf("CUE line %d: INDEX appears before TRACK", lineNumber)
			}
			indexNumber, err := strconv.Atoi(fields[1])
			if err != nil || indexNumber < 0 || indexNumber > 99 {
				return cueInfo{}, fmt.Errorf("CUE line %d: invalid index number %q", lineNumber, fields[1])
			}
			sector, err := parseMSF(fields[2])
			if err != nil {
				return cueInfo{}, fmt.Errorf("CUE line %d: %w", lineNumber, err)
			}
			track := &info.files[fileIndex].tracks[trackIndex]
			switch indexNumber {
			case 0:
				if track.index00 != nil {
					return cueInfo{}, fmt.Errorf("CUE line %d: duplicate INDEX 00", lineNumber)
				}
				track.index00 = &sector
			case 1:
				if track.index01 != nil {
					return cueInfo{}, fmt.Errorf("CUE line %d: duplicate INDEX 01", lineNumber)
				}
				track.index01 = &sector
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return cueInfo{}, fmt.Errorf("read CUE: %w", err)
	}
	for _, cueFile := range info.files {
		if len(cueFile.tracks) == 0 {
			return cueInfo{}, fmt.Errorf("CUE FILE %q contains no tracks", cueFile.name)
		}
		for _, track := range cueFile.tracks {
			if track.index01 == nil {
				return cueInfo{}, fmt.Errorf("CUE track %02d contains no INDEX 01", track.number)
			}
			if track.index00 != nil && *track.index00 > *track.index01 {
				return cueInfo{}, fmt.Errorf("CUE track %02d has INDEX 00 after INDEX 01", track.number)
			}
		}
	}
	return info, nil
}

func parseCueFile(line string) (string, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 || !strings.EqualFold(fields[0], "FILE") {
		return "", errors.New("malformed FILE directive")
	}

	remainder := strings.TrimSpace(line[len(fields[0]):])
	var name string
	switch {
	case remainder == "":
		return "", errors.New("malformed FILE directive")
	case remainder[0] == '"':
		closingQuote := strings.IndexByte(remainder[1:], '"')
		if closingQuote < 0 {
			return "", errors.New("malformed FILE directive: unterminated filename")
		}
		closingQuote++
		name = remainder[1:closingQuote]
		remainder = strings.TrimSpace(remainder[closingQuote+1:])
	default:
		end := strings.IndexAny(remainder, " \t")
		if end < 0 {
			return "", errors.New("malformed FILE directive")
		}
		name = remainder[:end]
		remainder = strings.TrimSpace(remainder[end:])
	}

	typeFields := strings.Fields(remainder)
	if name == "" || len(typeFields) != 1 {
		return "", errors.New("malformed FILE directive")
	}
	if !strings.EqualFold(typeFields[0], "BINARY") {
		return "", errors.New("FILE type must be BINARY")
	}
	return name, nil
}

func resolveCueFilePath(cuePath, name string) string {
	name = filepath.FromSlash(strings.ReplaceAll(name, `\`, "/"))
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}
	return filepath.Join(filepath.Dir(cuePath), name)
}

func hashCueImage(cuePath string, info cueInfo) (
	[sha256.Size]byte,
	[sha256.Size]byte,
	int64,
	[]sectorRange,
	error,
) {
	rawHasher := sha256.New()
	contentHasher := sha256.New()
	var byteLength int64
	var sectorOffset int64
	var discSectorOffset int64
	var readRanges []sectorRange
	discOrigin := *info.files[0].tracks[0].index01

	for _, cueFile := range info.files {
		path := resolveCueFilePath(cuePath, cueFile.name)
		file, err := os.Open(path)
		if err != nil {
			return [sha256.Size]byte{}, [sha256.Size]byte{}, 0, nil, fmt.Errorf("open BIN %q: %w", path, err)
		}

		stat, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return [sha256.Size]byte{}, [sha256.Size]byte{}, 0, nil, fmt.Errorf("inspect BIN %q: %w", path, err)
		}
		if stat.IsDir() {
			_ = file.Close()
			return [sha256.Size]byte{}, [sha256.Size]byte{}, 0, nil, fmt.Errorf("BIN %q is a directory", path)
		}
		if stat.Size()%rawSectorSize != 0 {
			_ = file.Close()
			return [sha256.Size]byte{}, [sha256.Size]byte{}, 0, nil, fmt.Errorf(
				"BIN %q size %d is not a multiple of %d bytes",
				path,
				stat.Size(),
				rawSectorSize,
			)
		}

		fileSectors := stat.Size() / rawSectorSize
		for trackIndex, track := range cueFile.tracks {
			start := *track.index01
			end := fileSectors
			if trackIndex+1 < len(cueFile.tracks) {
				nextTrack := cueFile.tracks[trackIndex+1]
				end = *nextTrack.index01
				if nextTrack.index00 != nil {
					end = *nextTrack.index00
				}
			}
			if start > end {
				_ = file.Close()
				return [sha256.Size]byte{}, [sha256.Size]byte{}, 0, nil, fmt.Errorf(
					"CUE track %02d starts at sector %d after its end at sector %d in BIN %q",
					track.number,
					start,
					end,
					path,
				)
			}
			if end > fileSectors {
				_ = file.Close()
				return [sha256.Size]byte{}, [sha256.Size]byte{}, 0, nil, fmt.Errorf(
					"CUE track %02d ends at sector %d beyond BIN %q length of %d sectors",
					track.number,
					end,
					path,
					fileSectors,
				)
			}

			sectorCount := end - start
			err = hashImageSectorsInto(
				io.NewSectionReader(file, start*rawSectorSize, sectorCount*rawSectorSize),
				sectorCount,
				sectorOffset,
				rawHasher,
				contentHasher,
			)
			if err != nil {
				_ = file.Close()
				return [sha256.Size]byte{}, [sha256.Size]byte{}, 0, nil, fmt.Errorf("hash BIN %q: %w", path, err)
			}
			if sectorCount > 0 {
				readStart := discSectorOffset + start - discOrigin
				if readStart < 0 {
					_ = file.Close()
					return [sha256.Size]byte{}, [sha256.Size]byte{}, 0, nil, fmt.Errorf(
						"CUE track %02d maps before the readable disc origin",
						track.number,
					)
				}
				readRanges = append(readRanges, sectorRange{
					start: readStart,
					count: sectorCount,
				})
			}
			byteLength += sectorCount * rawSectorSize
			sectorOffset += sectorCount
		}
		closeErr := file.Close()
		if closeErr != nil {
			return [sha256.Size]byte{}, [sha256.Size]byte{}, 0, nil, fmt.Errorf("close BIN %q: %w", path, closeErr)
		}
		discSectorOffset += fileSectors
	}

	return hashSum(rawHasher), hashSum(contentHasher), byteLength, readRanges, nil
}

func hashSum(hasher hash.Hash) [sha256.Size]byte {
	var sum [sha256.Size]byte
	copy(sum[:], hasher.Sum(nil))
	return sum
}
