package main

import (
	"reflect"
	"testing"
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
