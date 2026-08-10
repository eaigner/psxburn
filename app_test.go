package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeCommandRunner struct {
	status  string
	runs    [][]string
	runDirs []string
	onRun   func(name string, args []string) error
}

func (runner *fakeCommandRunner) output(context.Context, string, ...string) ([]byte, error) {
	return []byte(runner.status), nil
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
	runner.onRun = func(name string, args []string) error {
		if name != "cdrdao" {
			return nil
		}
		dataPath, tocPath := readbackPaths(t, args)
		readback := append(make([]byte, 150*rawSectorSize), source...)
		if err := os.WriteFile(dataPath, readback, 0o600); err != nil {
			return err
		}
		return os.WriteFile(tocPath, []byte("START 00:02:00\n"), 0o600)
	}

	var stdout bytes.Buffer
	app := application{
		commands: commandPaths{cdrecord: "cdrecord", cdrdao: "cdrdao", drutil: "drutil", diskutil: "diskutil"},
		runner:   runner,
		stdout:   &stdout,
		stderr:   io.Discard,
	}
	err := app.execute(context.Background(), image{
		byteLength: int64(len(source)),
		rawHash:    sha256.Sum256(source),
	}, true)
	if err != nil {
		t.Fatalf("execute() error = %v", err)
	}

	wantCommands := []string{"diskutil", "cdrdao", "drutil"}
	if len(runner.runs) != len(wantCommands) {
		t.Fatalf("commands = %v, want %v", runner.runs, wantCommands)
	}
	for index, want := range wantCommands {
		if runner.runs[index][0] != want {
			t.Fatalf("command %d = %q, want %q", index, runner.runs[index][0], want)
		}
	}
	if !strings.Contains(stdout.String(), "VERIFICATION PASSED (raw sectors matched at offset 150)") {
		t.Fatalf("stdout = %q, want successful verification", stdout.String())
	}
}

func TestVerificationFailureStillEjects(t *testing.T) {
	source := make([]byte, rawSectorSize)
	runner := &fakeCommandRunner{status: "Name: /dev/disk4\n"}
	runner.onRun = func(name string, args []string) error {
		if name != "cdrdao" {
			return nil
		}
		dataPath, tocPath := readbackPaths(t, args)
		if err := os.WriteFile(dataPath, bytes.Repeat([]byte{0xff}, int(rawSectorSize)), 0o600); err != nil {
			return err
		}
		return os.WriteFile(tocPath, []byte("START 00:00:00\n"), 0o600)
	}

	app := application{
		commands: commandPaths{cdrecord: "cdrecord", cdrdao: "cdrdao", drutil: "drutil", diskutil: "diskutil"},
		runner:   runner,
		stdout:   io.Discard,
		stderr:   io.Discard,
	}
	err := app.execute(context.Background(), image{
		byteLength: int64(len(source)),
		rawHash:    sha256.Sum256(source),
	}, true)
	if !errors.Is(err, errVerificationFailed) {
		t.Fatalf("execute() error = %v, want verification failure", err)
	}
	if got := runner.runs[len(runner.runs)-1][0]; got != "drutil" {
		t.Fatalf("last command = %q, want drutil eject", got)
	}
}

func TestBurnWorkflowUnmountsOnlyAtStart(t *testing.T) {
	source := make([]byte, rawSectorSize)
	cuePath := filepath.Join(t.TempDir(), "game.cue")
	runner := &fakeCommandRunner{status: "Name: /dev/disk4\n"}
	runner.onRun = func(name string, args []string) error {
		if name != "cdrdao" {
			return nil
		}
		dataPath, tocPath := readbackPaths(t, args)
		if err := os.WriteFile(dataPath, source, 0o600); err != nil {
			return err
		}
		return os.WriteFile(tocPath, []byte("START 00:00:00\n"), 0o600)
	}

	app := application{
		commands: commandPaths{cdrecord: "cdrecord", cdrdao: "cdrdao", drutil: "drutil", diskutil: "diskutil"},
		runner:   runner,
		stdout:   io.Discard,
		stderr:   io.Discard,
		waitBeforeBurn: func(context.Context, io.Writer, int) error {
			return nil
		},
	}
	err := app.execute(context.Background(), image{
		cuePath:    cuePath,
		byteLength: int64(len(source)),
		rawHash:    sha256.Sum256(source),
	}, false)
	if err != nil {
		t.Fatalf("execute() error = %v", err)
	}

	wantCommands := []string{"diskutil", "cdrecord", "cdrdao", "drutil"}
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
	if got := runner.runs[2][1]; got != "read-cd" {
		t.Fatalf("cdrdao command = %q, want read-cd", got)
	}
}

func TestReadFailureDoesNotEject(t *testing.T) {
	runner := &fakeCommandRunner{status: "Name: /dev/disk4\n"}
	runner.onRun = func(name string, _ []string) error {
		if name == "cdrdao" {
			return errors.New("read failed")
		}
		return nil
	}

	app := application{
		commands: commandPaths{cdrecord: "cdrecord", cdrdao: "cdrdao", drutil: "drutil", diskutil: "diskutil"},
		runner:   runner,
		stdout:   io.Discard,
		stderr:   io.Discard,
	}
	err := app.execute(context.Background(), image{}, true)
	if err == nil || !strings.Contains(err.Error(), "read disc") {
		t.Fatalf("execute() error = %v, want read disc error", err)
	}
	if got := runner.runs[len(runner.runs)-1][0]; got != "cdrdao" {
		t.Fatalf("last command = %q, want failed cdrdao read without eject", got)
	}
}

func readbackPaths(t *testing.T, args []string) (string, string) {
	t.Helper()
	if len(args) != 7 || args[0] != "read-cd" || args[4] != "--datafile" {
		t.Fatalf("cdrdao args = %q", args)
	}
	return args[5], args[6]
}
