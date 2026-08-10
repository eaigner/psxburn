# psxburn

`psxburn` burns a RAW/2352 PlayStation disc image with `cdrecord` in RAW/96R
mode, reads the disc back directly from macOS's raw optical-disc device, and
verifies the raw sectors with SHA-256. It holds a macOS idle sleep assertion for
the lifetime of the process.

After fixation, `psxburn` waits for macOS to publish the finalized disc's raw
device, re-detects its disk identifier, and unmounts it again before read-back.

Verification first checks the complete raw sector stream. If a drive has
regenerated Mode 1 or Mode 2 framing, EDC, or ECC bytes, `psxburn` falls back
to hashing the sector modes, subheaders, and user payloads. Audio sectors are
always compared in full. CUE `INDEX 00` pregaps are excluded from the source
hash. Direct read-back skips the corresponding physical sector ranges so the
disc and source streams have the same layout during comparison.

Verification covers the 2352-byte main channel. It does not verify P-W
subchannels or LibCrypt data.

## Requirements

- macOS
- `drutil` and `diskutil` available in `PATH`
- `cdrecord` available in `PATH` when burning
- A CUE whose `FILE` directives refer to raw BIN files

Install the external dependencies with Homebrew:

```sh
brew install cdrtools
```

`cdrecord` is provided by `cdrtools`. `drutil`, `diskutil`, and `caffeinate` are
included with macOS.

Supported track modes are `MODE1/2352`, `MODE2/2352`, and `AUDIO`. At least
one data track is required. Single- and multi-BIN CUE images are supported;
relative `FILE` paths are resolved from the CUE's directory.

Burning uses `cdrecord -raw96r` so the drive receives the existing 2352-byte
main-channel sectors instead of regenerating Mode 2 XA EDC/ECC. The optical
drive must support RAW/R96R writing. `cdrecord` auto-selects the burner, so only
one optical writer should be connected.

## Install psxburn

```sh
go install
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
