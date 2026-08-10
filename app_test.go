package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeCommandRunner struct {
	status         string
	outputStatuses []string
	outputCalls    int
	runs           [][]string
	runDirs        []string
	onRun          func(name string, args []string) error
}

func (runner *fakeCommandRunner) output(context.Context, string, ...string) ([]byte, error) {
	status := runner.status
	if runner.outputCalls < len(runner.outputStatuses) {
		status = runner.outputStatuses[runner.outputCalls]
	}
	runner.outputCalls++
	return []byte(status), nil
}

func (runner *fakeCommandRunner) run(
	_ context.Context,
	_ io.Writer,
	_ io.Writer,
	name string,
	args ...string,
) error {
	return runner.recordRun("", name, args)
}

func (runner *fakeCommandRunner) runInDir(
	_ context.Context,
	directory string,
	_ io.Writer,
	_ io.Writer,
	name string,
	args ...string,
) error {
	return runner.recordRun(directory, name, args)
}

func (runner *fakeCommandRunner) recordRun(directory, name string, args []string) error {
	call := append([]string{name}, args...)
	runner.runs = append(runner.runs, call)
	runner.runDirs = append(runner.runDirs, directory)
	if runner.onRun != nil {
		return runner.onRun(name, args)
	}
	return nil
}

func TestVerifyWorkflowUnmountsReadsAndEjects(t *testing.T) {
	source := make([]byte, 2*rawSectorSize)
	for index := range source {
		source[index] = byte(index % 251)
	}
	runner := &fakeCommandRunner{status: "Name: /dev/disk4\n"}
	var readDevice string

	var stdout bytes.Buffer
	app := application{
		commands: commandPaths{cdrecord: "cdrecord", drutil: "drutil", diskutil: "diskutil"},
		runner:   runner,
		stdout:   &stdout,
		stderr:   io.Discard,
		readDisc: func(_ context.Context, device, destination string, _ []sectorRange) error {
			readDevice = device
			return os.WriteFile(destination, source, 0o600)
		},
	}
	err := app.execute(context.Background(), image{
		byteLength: int64(len(source)),
		rawHash:    sha256.Sum256(source),
		readRanges: []sectorRange{{count: 2}},
	}, true)
	if err != nil {
		t.Fatalf("execute() error = %v", err)
	}

	wantCommands := []string{"diskutil", "drutil"}
	if len(runner.runs) != len(wantCommands) {
		t.Fatalf("commands = %v, want %v", runner.runs, wantCommands)
	}
	for index, want := range wantCommands {
		if runner.runs[index][0] != want {
			t.Fatalf("command %d = %q, want %q", index, runner.runs[index][0], want)
		}
	}
	if readDevice != "/dev/rdisk4" {
		t.Fatalf("read device = %q, want /dev/rdisk4", readDevice)
	}
	if !strings.Contains(stdout.String(), "VERIFICATION PASSED (raw sectors matched at offset 0)") {
		t.Fatalf("stdout = %q, want successful verification", stdout.String())
	}
}

func TestVerificationFailureStillEjects(t *testing.T) {
	source := make([]byte, rawSectorSize)
	runner := &fakeCommandRunner{status: "Name: /dev/disk4\n"}

	app := application{
		commands: commandPaths{cdrecord: "cdrecord", drutil: "drutil", diskutil: "diskutil"},
		runner:   runner,
		stdout:   io.Discard,
		stderr:   io.Discard,
		readDisc: func(_ context.Context, _, destination string, _ []sectorRange) error {
			return os.WriteFile(destination, bytes.Repeat([]byte{0xff}, int(rawSectorSize)), 0o600)
		},
	}
	err := app.execute(context.Background(), image{
		byteLength: int64(len(source)),
		rawHash:    sha256.Sum256(source),
		readRanges: []sectorRange{{count: 1}},
	}, true)
	if !errors.Is(err, errVerificationFailed) {
		t.Fatalf("execute() error = %v, want verification failure", err)
	}
	if got := runner.runs[len(runner.runs)-1][0]; got != "drutil" {
		t.Fatalf("last command = %q, want drutil eject", got)
	}
}

func TestBurnWorkflowRedetectsAndUnmountsFinalizedDisc(t *testing.T) {
	source := make([]byte, rawSectorSize)
	cuePath := filepath.Join(t.TempDir(), "game.cue")
	runner := &fakeCommandRunner{status: "Name: /dev/disk4\n"}
	var readDevice string

	app := application{
		commands: commandPaths{cdrecord: "cdrecord", drutil: "drutil", diskutil: "diskutil"},
		runner:   runner,
		stdout:   io.Discard,
		stderr:   io.Discard,
		readDisc: func(_ context.Context, device, destination string, _ []sectorRange) error {
			readDevice = device
			return os.WriteFile(destination, source, 0o600)
		},
		waitForDisc: func(_ context.Context, _ commandRunner, drutil string) (string, error) {
			if drutil != "drutil" {
				t.Fatalf("drutil = %q, want drutil", drutil)
			}
			return "/dev/disk5", nil
		},
		waitBeforeBurn: func(context.Context, io.Writer, int) error {
			return nil
		},
	}
	err := app.execute(context.Background(), image{
		cuePath:    cuePath,
		byteLength: int64(len(source)),
		rawHash:    sha256.Sum256(source),
		readRanges: []sectorRange{{count: 1}},
	}, false)
	if err != nil {
		t.Fatalf("execute() error = %v", err)
	}

	wantCommands := []string{"diskutil", "cdrecord", "diskutil", "drutil"}
	if len(runner.runs) != len(wantCommands) {
		t.Fatalf("commands = %v, want %v", runner.runs, wantCommands)
	}
	for index, want := range wantCommands {
		if runner.runs[index][0] != want {
			t.Fatalf("command %d = %q, want %q", index, runner.runs[index][0], want)
		}
	}
	if got := runner.runs[1][1]; got != "-v" {
		t.Fatalf("cdrecord verbosity option = %q, want -v", got)
	}
	if got := runner.runs[1][2]; got != "-raw96r" {
		t.Fatalf("cdrecord mode = %q, want -raw96r", got)
	}
	if got := runner.runDirs[1]; got != filepath.Dir(cuePath) {
		t.Fatalf("burn working directory = %q, want %q", got, filepath.Dir(cuePath))
	}
	if got := runner.runs[1][3]; got != "cuefile="+filepath.Base(cuePath) {
		t.Fatalf("burn CUE argument = %q, want %q", got, "cuefile="+filepath.Base(cuePath))
	}
	if got := runner.runs[2]; !reflect.DeepEqual(got, []string{"diskutil", "unmountDisk", "/dev/disk5"}) {
		t.Fatalf("post-burn unmount = %q, want finalized device", got)
	}
	if readDevice != "/dev/rdisk5" {
		t.Fatalf("read device = %q, want /dev/rdisk5", readDevice)
	}
}

func TestBurnWorkflowStopsWhenFinalizedDiscDoesNotBecomeReady(t *testing.T) {
	cuePath := filepath.Join(t.TempDir(), "game.cue")
	runner := &fakeCommandRunner{status: "Name: /dev/disk4\n"}
	readCalled := false

	app := application{
		commands: commandPaths{cdrecord: "cdrecord", drutil: "drutil", diskutil: "diskutil"},
		runner:   runner,
		stdout:   io.Discard,
		stderr:   io.Discard,
		readDisc: func(context.Context, string, string, []sectorRange) error {
			readCalled = true
			return nil
		},
		waitForDisc: func(context.Context, commandRunner, string) (string, error) {
			return "", errors.New("raw device unavailable")
		},
		waitBeforeBurn: func(context.Context, io.Writer, int) error {
			return nil
		},
	}
	err := app.execute(context.Background(), image{cuePath: cuePath}, false)
	if err == nil || !strings.Contains(err.Error(), "wait for finalized disc") {
		t.Fatalf("execute() error = %v, want finalized disc error", err)
	}
	if readCalled {
		t.Fatal("readDisc() called before the finalized disc became ready")
	}
	wantCommands := []string{"diskutil", "cdrecord"}
	if len(runner.runs) != len(wantCommands) {
		t.Fatalf("commands = %v, want %v", runner.runs, wantCommands)
	}
	for index, want := range wantCommands {
		if runner.runs[index][0] != want {
			t.Fatalf("command %d = %q, want %q", index, runner.runs[index][0], want)
		}
	}
}

func TestReadFailureDoesNotEject(t *testing.T) {
	runner := &fakeCommandRunner{status: "Name: /dev/disk4\n"}

	app := application{
		commands: commandPaths{cdrecord: "cdrecord", drutil: "drutil", diskutil: "diskutil"},
		runner:   runner,
		stdout:   io.Discard,
		stderr:   io.Discard,
		readDisc: func(context.Context, string, string, []sectorRange) error {
			return errors.New("read failed")
		},
	}
	err := app.execute(context.Background(), image{}, true)
	if err == nil || !strings.Contains(err.Error(), "read disc") {
		t.Fatalf("execute() error = %v, want read disc error", err)
	}
	if got := runner.runs[len(runner.runs)-1][0]; got != "diskutil" {
		t.Fatalf("last command = %q, want unmount without eject", got)
	}
}

func TestReadRawDiscCopiesSelectedSectorRanges(t *testing.T) {
	sectors := make([]byte, 5*rawSectorSize)
	for sector := int64(0); sector < 5; sector++ {
		for offset := int64(0); offset < rawSectorSize; offset++ {
			sectors[sector*rawSectorSize+offset] = byte(sector + 1)
		}
	}

	directory := t.TempDir()
	source := filepath.Join(directory, "device.bin")
	destination := filepath.Join(directory, "readback.bin")
	if err := os.WriteFile(source, sectors, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := readRawDisc(context.Background(), source, destination, []sectorRange{
		{start: 0, count: 2},
		{start: 3, count: 2},
	}); err != nil {
		t.Fatalf("readRawDisc() error = %v", err)
	}

	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	want := append(bytes.Clone(sectors[:2*rawSectorSize]), sectors[3*rawSectorSize:]...)
	if !bytes.Equal(got, want) {
		t.Fatal("readRawDisc() did not concatenate the selected sector ranges")
	}
}

func TestReadRawDiscRejectsShortRead(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "device.bin")
	if err := os.WriteFile(source, make([]byte, rawSectorSize), 0o600); err != nil {
		t.Fatal(err)
	}

	err := readRawDisc(
		context.Background(),
		source,
		filepath.Join(directory, "readback.bin"),
		[]sectorRange{{count: 2}},
	)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("readRawDisc() error = %v, want unexpected EOF", err)
	}
}
