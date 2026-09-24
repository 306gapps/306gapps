# 306gapps

Build a custom Google apps package for a custom ROM: pick an Android release,
pick the apps you want, get a flashable zip.

Payloads are dumped from Google's own Pixel OTA images by
[306gapps-assets](https://github.com/306gapps/306gapps-assets), which publishes a
manifest per Pixel build. This tool reads that manifest, downloads only the
packages you selected, verifies every payload against its recorded digest, and
assembles the package locally. Nothing is built on a server.

## Install

```
go install github.com/306gapps/306gapps/cmd/306gapps@latest
```

## Use

```
306gapps                       # interactive picker
306gapps list                  # available releases
306gapps list 16               # packages in the newest Android 16 release
306gapps build -target module -packages gsa,photos,gboard
306gapps cache info
```

Useful flags:

| flag | meaning |
| --- | --- |
| `-source` | assets repo URL or a local mirror directory |
| `-release` | release ID, an Android version, or `latest` |
| `-packages` | comma-separated package IDs; omit for the release defaults |
| `-target` | `recovery`, `module`, or `ota` |
| `-out` | output path |

`GAPPS_SOURCE` and `GAPPS_CACHE` set the defaults for `-source` and `-cache`.

## Output formats

**`recovery`** — a recovery-flashable zip for TWRP or LineageOS recovery. No root
needed. The installer mounts the target partitions read-write (handling dynamic
`super` partitions), checks free space before writing anything, removes the AOSP
apps the selection supersedes, streams each payload straight out of the zip onto
the partition, applies build properties, and installs an `addon.d` script so the
gapps survive a ROM dirty-flash. It refuses to install on a ROM whose API level
does not match the package.

**`module`** — a Magisk or KernelSU module. Requires root. Files are overlaid
rather than written into the ROM, superseded AOSP apps are masked with `.replace`
markers instead of deleted, and build properties are applied with `resetprop`, so
disabling the module restores the stock ROM exactly. Much safer than the recovery
route, and it survives OTAs.

**`ota`** — a target-files overlay, **not a flashable zip**. See below.

### About the `ota` target

A sideloadable A/B package is verified by the device against the certificate in
`/system/etc/security/otacerts.zip`, so it has to be signed with the ROM's own
release key. Only whoever builds the ROM has that key — we cannot supply it and
neither could a build service, so no tool can hand you a finished sideloadable
gapps package.

What this target does instead is the part that *can* be automated: it lays the
payload out as a target-files overlay with the `filesystem_config` and
`file_contexts` records the AOSP image builder needs, and generates
`merge-and-sign.sh` with the exact `merge_target_files` → `add_img_to_target_files`
→ `sign_target_files_apks` → `ota_from_target_files` invocation. Run that inside
your ROM build tree, and you get a signed package your device will accept.

If you are not building your own ROM, use `recovery` or `module`.

## Layout

```
cmd/306gapps        CLI entry point
internal/manifest   release manifest schema and validation
internal/catalog    dependency and conflict resolution
internal/source     release index, manifest fetch, content-addressed cache
internal/stage      payload fetch and install-plan assembly
internal/build      the three output targets
internal/build/templates
                    installer shell scripts, embedded into the binary
internal/tui        interactive picker
test/installer      runs the real recovery installer against a fake ROM tree
```

## Guarantees

- **Nothing unverified is installed.** Every payload is checked against the
  sha256 in the manifest before it enters the cache, and a corrupt download
  leaves no cache entry behind. The recovery installer re-checks each file's size
  as it extracts.
- **Dependencies and conflicts are resolved before anything is downloaded**, so a
  bad selection fails in milliseconds rather than after a gigabyte.
- **Builds are reproducible.** The same selection produces a byte-identical zip:
  entries are path-sorted with a fixed timestamp, and already-compressed formats
  are stored rather than re-deflated.

## Testing

```
go test ./...
./test/installer/run.sh <path-to-306gapps-binary> <fixture-dir>
```

`test/installer` builds a real recovery zip, installs it into a fake ROM tree
under busybox `ash`, and asserts the result — including that it refuses a
mismatched Android version and a full partition.

## Licensing

This tool builds packages; it ships no Google software. The apps themselves come
from Google's Pixel images and are Google's, under Google's terms — the same
footing every other gapps distribution stands on. Redistributing them is not
something Google licenses, so run your own assets repo if that matters to you.
