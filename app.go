package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type application struct {
	commands       commandPaths
	runner         commandRunner
	stdout         io.Writer
	stderr         io.Writer
	waitBeforeBurn func(context.Context, io.Writer, int) error
}

func (app application) execute(ctx context.Context, image image, verifyOnly bool) error {
	device, err := detectDiscDevice(ctx, app.runner, app.commands.drutil)
	if err != nil {
		return err
	}
	if err := unmountDisc(ctx, app.runner, app.commands.diskutil, device, app.stderr); err != nil {
		return err
	}

	if !verifyOnly {
		waitBeforeBurn := app.waitBeforeBurn
		if waitBeforeBurn == nil {
			waitBeforeBurn = countdown
		}
		if err := waitBeforeBurn(ctx, app.stdout, 3); err != nil {
			return fmt.Errorf("burn cancelled: %w", err)
		}
		if err := app.runner.runInDir(
			ctx,
			filepath.Dir(image.cuePath),
			app.stdout,
			app.stderr,
			app.commands.cdrdao,
			"write",
			"-n",
			filepath.Base(image.cuePath),
		); err != nil {
			return fmt.Errorf("burn disc: %w", err)
		}
	}

	readbackComplete, verificationErr := app.readAndVerify(ctx, image)
	if !readbackComplete {
		return verificationErr
	}
	ejectErr := app.runner.run(ctx, app.stdout, app.stderr, app.commands.drutil, "eject")
	if ejectErr != nil {
		if verificationErr != nil {
			fmt.Fprintf(app.stderr, "psxburn: eject disc: %v\n", ejectErr)
			return verificationErr
		}
		return fmt.Errorf("eject disc: %w", ejectErr)
	}
	return verificationErr
}

func (app application) readAndVerify(ctx context.Context, image image) (bool, error) {
	tempDir, err := os.MkdirTemp("", "psxburn-")
	if err != nil {
		return false, fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	discPath := filepath.Join(tempDir, "disc.bin")
	tocPath := filepath.Join(tempDir, "disc.toc")
	if err := app.runner.run(
		ctx,
		app.stdout,
		app.stderr,
		app.commands.cdrdao,
		"read-cd",
		"--read-raw",
		"--paranoia-mode", "0",
		"--datafile", discPath,
		tocPath,
	); err != nil {
		return false, fmt.Errorf("read disc: %w", err)
	}

	start, err := readTOCStart(tocPath)
	if err != nil {
		return true, err
	}
	result, err := verifyReadback(discPath, image.rawHash, image.contentHash, image.byteLength, start)
	if err != nil {
		return true, err
	}

	fmt.Fprintf(app.stdout, "Source SHA-256: %x\n", image.rawHash)
	fmt.Fprintf(app.stdout, "Disc SHA-256:   %x\n", result.discRawHash)
	if result.matched {
		if result.rawMatched {
			fmt.Fprintf(app.stdout, "VERIFICATION PASSED (raw sectors matched at offset %d)\n", result.offset)
			return true, nil
		}
		fmt.Fprintf(app.stdout, "Source content SHA-256: %x\n", image.contentHash)
		fmt.Fprintf(app.stdout, "Disc content SHA-256:   %x\n", result.discContentHash)
		fmt.Fprintf(app.stdout, "VERIFICATION PASSED (sector content matched at offset %d; raw framing or integrity bytes differ)\n", result.offset)
		return true, nil
	}
	fmt.Fprintf(app.stdout, "Source content SHA-256: %x\n", image.contentHash)
	fmt.Fprintf(app.stdout, "Disc content SHA-256:   %x\n", result.discContentHash)
	fmt.Fprintln(app.stderr, "VERIFICATION FAILED")
	return true, errVerificationFailed
}
