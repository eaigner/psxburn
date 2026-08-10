# psxburn

`psxburn` burns a RAW/2352 PlayStation disc image with `cdrdao`, reads the
disc back, and verifies the raw sectors with SHA-256. It holds a macOS idle
sleep assertion for the lifetime of the process.

Verification first checks the complete raw sector stream. If a drive has
regenerated Mode 1 or Mode 2 framing, EDC, or ECC bytes, `psxburn` falls back
to hashing the sector modes, subheaders, and user payloads. Audio sectors are
always compared in full. CUE `INDEX 00` pregaps are excluded from the source
hash because `cdrdao read-cd` represents them in its TOC rather than copying
them into the read-back image.

## Requirements

- macOS
- `cdrdao`, `drutil`, and `diskutil` available in `PATH`
- A CUE whose `FILE` directives refer to raw BIN files

Supported track modes are `MODE1/2352`, `MODE2/2352`, and `AUDIO`. At least
one data track is required. Single- and multi-BIN CUE images are supported;
relative `FILE` paths are resolved from the CUE's directory.

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
