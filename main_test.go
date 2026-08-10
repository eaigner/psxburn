package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCaffeinateArgsFollowProcessLifetime(t *testing.T) {
	want := []string{"-i", "-w", "1234"}
	if got := caffeinateArgs(1234); !reflect.DeepEqual(got, want) {
		t.Fatalf("caffeinateArgs() = %q, want %q", got, want)
	}
}

func TestSelectCuePathUsesExplicitArgument(t *testing.T) {
	got, err := selectCuePath([]string{"elsewhere/game.cue"}, t.TempDir())
	if err != nil {
		t.Fatalf("selectCuePath() error = %v", err)
	}
	if got != "elsewhere/game.cue" {
		t.Fatalf("selectCuePath() = %q, want explicit argument", got)
	}
}

func TestSelectCuePathDiscoversSingleCueCaseInsensitively(t *testing.T) {
	dir := t.TempDir()
	cuePath := filepath.Join(dir, "Game.CUE")
	if err := os.WriteFile(cuePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Game.bin"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := selectCuePath(nil, dir)
	if err != nil {
		t.Fatalf("selectCuePath() error = %v", err)
	}
	if got != cuePath {
		t.Fatalf("selectCuePath() = %q, want %q", got, cuePath)
	}
}

func TestSelectCuePathRejectsAmbiguousDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.cue", "two.CUE"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	_, err := selectCuePath(nil, dir)
	if err == nil || !strings.Contains(err.Error(), "multiple CUE files") {
		t.Fatalf("selectCuePath() error = %v, want ambiguity error", err)
	}
}

func TestSelectCuePathRejectsMissingCue(t *testing.T) {
	_, err := selectCuePath(nil, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "none found") {
		t.Fatalf("selectCuePath() error = %v, want missing CUE error", err)
	}
}
