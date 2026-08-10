# psxburn

`psxburn` burns a RAW/2352 PlayStation disc image with `cdrdao`, reads the
disc back, and verifies the raw sectors with SHA-256. It holds a macOS idle
sleep assertion for the lifetime of the process.

## Requirements

- macOS
- `cdrdao`, `drutil`, and `diskutil` available in `PATH`
- A CUE with exactly one `FILE` directive
- A same-stem `.bin` or `.BIN` file next to the CUE

Supported track modes are `MODE1/2352`, `MODE2/2352`, and `AUDIO`. At least
one data track is required.

## Build

```sh
go build .
```

## Usage

Burn and verify:

```sh
psxburn image.cue
```

When the current directory contains exactly one `.cue` file, the argument may
be omitted:

```sh
psxburn
```

Verify an existing disc without burning:

```sh
psxburn -verify image.cue
```

The CUE argument may also be omitted in verification mode when discovery is
unambiguous.
