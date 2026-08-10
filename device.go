package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

var discDevicePattern = regexp.MustCompile(`\bName:\s*(/dev/(?:r)?disk[0-9]+)\b`)

const (
	finalizedDiscTimeout = 30 * time.Second
	discDevicePollDelay  = 250 * time.Millisecond
)

func detectDiscDevice(ctx context.Context, runner commandRunner, drutil string) (string, error) {
	output, commandErr := runner.output(ctx, drutil, "status")
	devices := parseDiscDevices(string(output))
	if len(devices) == 0 {
		if commandErr != nil {
			return "", fmt.Errorf("inspect optical drive: %w: %s", commandErr, strings.TrimSpace(string(output)))
		}
		return "", fmt.Errorf("no optical disc device with inserted media was found")
	}
	if commandErr != nil {
		return "", fmt.Errorf("inspect optical drive: %w: %s", commandErr, strings.TrimSpace(string(output)))
	}
	if len(devices) > 1 {
		return "", fmt.Errorf("multiple optical disc devices found (%s); leave media in only one drive", strings.Join(devices, ", "))
	}
	return devices[0], nil
}

func parseDiscDevices(output string) []string {
	matches := discDevicePattern.FindAllStringSubmatch(output, -1)
	seen := make(map[string]bool, len(matches))
	devices := make([]string, 0, len(matches))
	for _, match := range matches {
		device := match[1]
		if !seen[device] {
			seen[device] = true
			devices = append(devices, device)
		}
	}
	return devices
}

func rawDiscDevice(device string) string {
	if strings.HasPrefix(device, "/dev/disk") {
		return "/dev/rdisk" + strings.TrimPrefix(device, "/dev/disk")
	}
	return device
}

func waitForRawDiscDevice(ctx context.Context, runner commandRunner, drutil string) (string, error) {
	return pollForRawDiscDevice(ctx, runner, drutil, discDevicePollDelay, func(path string) error {
		_, err := os.Stat(path)
		return err
	})
}

func pollForRawDiscDevice(
	ctx context.Context,
	runner commandRunner,
	drutil string,
	retryDelay time.Duration,
	ready func(string) error,
) (string, error) {
	var lastErr error
	for {
		if err := context.Cause(ctx); err != nil {
			return "", finalizedDiscWaitError(err, lastErr)
		}

		device, err := detectDiscDevice(ctx, runner, drutil)
		if err == nil {
			err = ready(rawDiscDevice(device))
			if err == nil {
				return device, nil
			}
		}
		lastErr = err

		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return "", finalizedDiscWaitError(context.Cause(ctx), lastErr)
		case <-timer.C:
		}
	}
}

func finalizedDiscWaitError(cause, lastErr error) error {
	if lastErr == nil {
		return cause
	}
	return fmt.Errorf("%w (last readiness check: %v)", cause, lastErr)
}

func unmountDisc(
	ctx context.Context,
	runner commandRunner,
	diskutil string,
	device string,
	stderr io.Writer,
) error {
	if err := runner.run(ctx, io.Discard, stderr, diskutil, "unmountDisk", device); err != nil {
		return fmt.Errorf("unmount %s: %w", device, err)
	}
	return nil
}
