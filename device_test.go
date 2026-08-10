package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseDiscDevices(t *testing.T) {
	output := `Vendor: Example
	   Type: CD-R                 Name: /dev/disk4
Other: value
	   Name: /dev/disk7
	   Name: /dev/disk4
`
	want := []string{"/dev/disk4", "/dev/disk7"}
	if got := parseDiscDevices(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDiscDevices() = %q, want %q", got, want)
	}
}

func TestParseDiscDevicesFromDrutilBlankDiscStatus(t *testing.T) {
	output := ` Vendor   Product           Rev
 Slimtype DVD A  DU8AESH    6P5M

           Type: CD-R                 Name: /dev/disk26
   Write Speeds: 10x, 16x, 20x, 24x
     Space Used:   00:00:00         blocks:        0 /   0.00MB /   0.00MiB
    Writability: appendable, blank, overwritable
`
	want := []string{"/dev/disk26"}
	if got := parseDiscDevices(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDiscDevices() = %q, want %q", got, want)
	}
}

func TestRawDiscDeviceUsesCharacterDevice(t *testing.T) {
	tests := map[string]string{
		"/dev/disk26":  "/dev/rdisk26",
		"/dev/rdisk26": "/dev/rdisk26",
	}
	for device, want := range tests {
		if got := rawDiscDevice(device); got != want {
			t.Errorf("rawDiscDevice(%q) = %q, want %q", device, got, want)
		}
	}
}

func TestPollForRawDiscDeviceRedetectsUntilRawNodeIsReady(t *testing.T) {
	runner := &fakeCommandRunner{outputStatuses: []string{
		"Type: No Media Inserted\n",
		"Name: /dev/disk5\n",
	}}
	readyCalls := 0

	got, err := pollForRawDiscDevice(
		context.Background(),
		runner,
		"drutil",
		0,
		func(path string) error {
			readyCalls++
			if path != "/dev/rdisk5" {
				t.Fatalf("raw device = %q, want /dev/rdisk5", path)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("pollForRawDiscDevice() error = %v", err)
	}
	if got != "/dev/disk5" {
		t.Fatalf("pollForRawDiscDevice() = %q, want /dev/disk5", got)
	}
	if runner.outputCalls != 2 || readyCalls != 1 {
		t.Fatalf("status calls = %d, readiness calls = %d, want 2 and 1", runner.outputCalls, readyCalls)
	}
}

func TestPollForRawDiscDeviceReportsLastReadinessFailureOnCancellation(t *testing.T) {
	runner := &fakeCommandRunner{status: "Name: /dev/disk4\n"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := pollForRawDiscDevice(ctx, runner, "drutil", time.Hour, func(string) error {
		cancel()
		return os.ErrNotExist
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pollForRawDiscDevice() error = %v, want context cancellation", err)
	}
	if !strings.Contains(err.Error(), os.ErrNotExist.Error()) {
		t.Fatalf("pollForRawDiscDevice() error = %v, want last readiness failure", err)
	}
}
