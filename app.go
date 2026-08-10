package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type application struct {
	commands       commandPaths
	runner         commandRunner
	stdout         io.Writer
	stderr         io.Writer
	waitBeforeBurn func(context.Context, io.Writer, int) error
	readDisc       func(context.Context, string, string, []sectorRange) error
	waitForDisc    func(context.Context, commandRunner, string) (string, error)
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
			app.commands.cdrecord,
			"-v",
			"-raw96r",
			"cuefile="+filepath.Base(image.cuePath),
		); err != nil {
			return fmt.Errorf("burn disc: %w", err)
		}

		fmt.Fprintln(app.stdout, "Waiting for finalized disc device...")
		waitForDisc := app.waitForDisc
		if waitForDisc == nil {
			waitForDisc = waitForRawDiscDevice
		}
		waitCtx, cancelWait := context.WithTimeout(ctx, finalizedDiscTimeout)
		device, err = waitForDisc(waitCtx, app.runner, app.commands.drutil)
		cancelWait()
		if err != nil {
			return fmt.Errorf("wait for finalized disc: %w", err)
		}
		if err := unmountDisc(ctx, app.runner, app.commands.diskutil, device, app.stderr); err != nil {
			return err
		}
	}

	readbackComplete, verificationErr := app.readAndVerify(ctx, image, device)
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

func (app application) readAndVerify(ctx context.Context, image image, device string) (bool, error) {
	tempDir, err := os.MkdirTemp("", "psxburn-")
	if err != nil {
		return false, fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	discPath := filepath.Join(tempDir, "disc.bin")
	readDisc := app.readDisc
	if readDisc == nil {
		readDisc = readRawDisc
	}
	rawDevice := rawDiscDevice(device)
	fmt.Fprintf(app.stdout, "Reading %d sectors from %s...\n", image.byteLength/rawSectorSize, rawDevice)
	if err := readDisc(ctx, rawDevice, discPath, image.readRanges); err != nil {
		return false, fmt.Errorf("read disc: %w", err)
	}
	fmt.Fprintln(app.stdout, "Disc read-back complete.")

	result, err := verifyReadback(discPath, image.rawHash, image.contentHash, image.byteLength, 0)
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

func readRawDisc(ctx context.Context, device, destination string, ranges []sectorRange) (returnErr error) {
	source, err := syscall.Open(device, syscall.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open raw device %s: %w", device, err)
	}
	defer func() {
		if err := syscall.Close(source); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("close raw device %s: %w", device, err)
		}
	}()

	disc, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create disc read-back: %w", err)
	}
	defer func() {
		if err := disc.Close(); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("close disc read-back: %w", err)
		}
	}()

	// Darwin raw disk reads require a page-aligned destination buffer.
	buffer, err := syscall.Mmap(
		-1,
		0,
		int(rawSectorSize),
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_ANON|syscall.MAP_PRIVATE,
	)
	if err != nil {
		return fmt.Errorf("allocate aligned read buffer: %w", err)
	}
	defer func() {
		if err := syscall.Munmap(buffer); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("release aligned read buffer: %w", err)
		}
	}()

	for _, sectorRange := range ranges {
		if sectorRange.start < 0 || sectorRange.count < 0 {
			return fmt.Errorf("invalid disc sector range %d+%d", sectorRange.start, sectorRange.count)
		}
		if _, err := syscall.Seek(source, sectorRange.start*rawSectorSize, io.SeekStart); err != nil {
			return fmt.Errorf("seek raw device to sector %d: %w", sectorRange.start, err)
		}

		for sector := int64(0); sector < sectorRange.count; sector++ {
			if err := context.Cause(ctx); err != nil {
				return err
			}
			read, err := syscall.Read(source, buffer)
			if err != nil {
				return fmt.Errorf("read raw device at sector %d: %w", sectorRange.start+sector, err)
			}
			if read != len(buffer) {
				return fmt.Errorf(
					"read raw device at sector %d: got %d of %d bytes: %w",
					sectorRange.start+sector,
					read,
					len(buffer),
					io.ErrUnexpectedEOF,
				)
			}
			written, err := disc.Write(buffer)
			if err != nil {
				return fmt.Errorf("write disc read-back at sector %d: %w", sectorRange.start+sector, err)
			}
			if written != len(buffer) {
				return fmt.Errorf(
					"write disc read-back at sector %d: wrote %d of %d bytes: %w",
					sectorRange.start+sector,
					written,
					len(buffer),
					io.ErrShortWrite,
				)
			}
		}
	}
	return nil
}
