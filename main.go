package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var errVerificationFailed = errors.New("verification failed")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := holdSleepAssertion(os.Getpid()); err != nil {
		fmt.Fprintf(os.Stderr, "psxburn: prevent system sleep: %v\n", err)
		os.Exit(1)
	}

	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return
	case errors.Is(err, errVerificationFailed):
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "psxburn: %v\n", err)
		os.Exit(1)
	}
}

func holdSleepAssertion(pid int) error {
	cmd := exec.Command("/usr/bin/caffeinate", caffeinateArgs(pid)...)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

func caffeinateArgs(pid int) []string {
	return []string{"-i", "-w", strconv.Itoa(pid)}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("psxburn", flag.ContinueOnError)
	flags.SetOutput(stderr)
	verifyOnly := flags.Bool("verify", false, "verify an already-burned disc without burning")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: psxburn [-verify] [image.cue]")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return err
	}
	cuePath, err := selectCuePath(flags.Args(), ".")
	if err != nil {
		flags.Usage()
		return err
	}
	if runtime.GOOS != "darwin" {
		return errors.New("psxburn requires macOS")
	}

	image, err := inspectImage(cuePath)
	if err != nil {
		return err
	}

	commands, err := findCommands(*verifyOnly)
	if err != nil {
		return err
	}

	app := application{
		commands: commands,
		runner:   systemCommandRunner{},
		stdout:   stdout,
		stderr:   stderr,
	}
	return app.execute(ctx, image, *verifyOnly)
}

func selectCuePath(args []string, directory string) (string, error) {
	switch len(args) {
	case 1:
		return args[0], nil
	case 0:
	default:
		return "", errors.New("at most one CUE file may be specified")
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", fmt.Errorf("search current directory for a CUE file: %w", err)
	}
	var matches []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".cue") {
			matches = append(matches, entry.Name())
		}
	}

	switch len(matches) {
	case 0:
		return "", errors.New("no CUE file specified and none found in the current directory")
	case 1:
		return filepath.Join(directory, matches[0]), nil
	default:
		return "", fmt.Errorf("multiple CUE files found in the current directory: %s", strings.Join(matches, ", "))
	}
}

type commandPaths struct {
	cdrecord string
	cdrdao   string
	drutil   string
	diskutil string
}

func findCommands(verifyOnly bool) (commandPaths, error) {
	find := func(name string) (string, error) {
		path, err := exec.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("required command %q was not found in PATH", name)
		}
		return path, nil
	}

	var cdrecord string
	var err error
	if !verifyOnly {
		cdrecord, err = find("cdrecord")
		if err != nil {
			return commandPaths{}, err
		}
	}
	cdrdao, err := find("cdrdao")
	if err != nil {
		return commandPaths{}, err
	}
	drutil, err := find("drutil")
	if err != nil {
		return commandPaths{}, err
	}
	diskutil, err := find("diskutil")
	if err != nil {
		return commandPaths{}, err
	}

	return commandPaths{
		cdrecord: cdrecord,
		cdrdao:   cdrdao,
		drutil:   drutil,
		diskutil: diskutil,
	}, nil
}

type commandRunner interface {
	run(context.Context, io.Writer, io.Writer, string, ...string) error
	runInDir(context.Context, string, io.Writer, io.Writer, string, ...string) error
	output(context.Context, string, ...string) ([]byte, error)
}

type systemCommandRunner struct{}

func (runner systemCommandRunner) run(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	name string,
	args ...string,
) error {
	return runner.runInDir(ctx, "", stdout, stderr, name, args...)
}

func (systemCommandRunner) runInDir(
	ctx context.Context,
	directory string,
	stdout io.Writer,
	stderr io.Writer,
	name string,
	args ...string,
) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = directory
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func (systemCommandRunner) output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func countdown(ctx context.Context, output io.Writer, seconds int) error {
	for remaining := seconds; remaining > 0; remaining-- {
		fmt.Fprintf(output, "Burning in %d...\n", remaining)
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return context.Cause(ctx)
		case <-timer.C:
		}
	}
	return nil
}
