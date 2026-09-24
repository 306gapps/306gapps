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
needed. The installer resolves the active A/B slot and the partition block
devices, mounts them read-write (clearing the read-only flag on dynamic `super`
partitions and confirming writability by writing), checks free space before
writing anything, removes the AOSP apps the selection supersedes, streams each
payload straight out of the zip onto the partition, verifies it by SHA-256,
recreates symlinks, applies build properties, and installs an `addon.d` script
so the gapps survive a ROM dirty-flash. It refuses to install on a ROM whose API
level does not match the package.

It bundles a static busybox so it runs against one predictable toolset rather
than whatever applets a given recovery happens to ship. If that binary will not
run, the installer falls back to the recovery's own tools rather than refusing.
Pass `-no-busybox` to leave it out. BusyBox is GPLv2 and the zip carries the
source offer alongside it.

**`module`** — a Magisk or KernelSU module. Requires root. Files are overlaid
rather than written into the ROM, superseded AOSP apps are masked with `.replace`
markers instead of deleted, and build properties are applied with `resetprop`, so
disabling the module restores the stock ROM exactly. Much safer than the recovery
route, and it survives OTAs.

**`ota`** — a sideloadable A/B package, signed with your own ROM keys. For people
who build and sign their own ROM. See below.

### The `ota` target

A sideloadable package is verified against the certificate in
`/system/etc/security/otacerts.zip`, so it has to be signed with the key the
device already trusts — the one you sign your ROM with. If you have that key,
this target does the whole job:

```
306gapps build -target ota \
  -packages gsa,gboard,photos \
  -ota-base  out/dist/aosp_cheetah-target_files-eng.zip \
  -ota-keys  ~/keys \
  -ota-tools out/host/linux-x86/bin \
  -out       gapps-ota.zip
```

It merges the selection into your target-files package and then runs
`add_img_to_target_files` → `sign_target_files_apks` → `ota_from_target_files`,
leaving you a zip to `adb sideload`.

Leave `-ota-keys` off and it stops at the merged target-files package, which you
can take through your own signing flow.

| flag | meaning |
| --- | --- |
| `-ota-base` | your ROM's `*-target_files-*.zip` (required) |
| `-ota-keys` | directory holding `releasekey.pk8` / `releasekey.x509.pem` etc |
| `-ota-package-key` | key signing the OTA itself, no extension (default `<keys>/releasekey`) |
| `-ota-tools` | otatools `bin` directory (default: `PATH`) |
| `-ota-grow` | raise a partition's size budget if the selection overflows it |

What the merge does to your target-files package:

- places payloads in `PRODUCT/`, `SYSTEM_EXT/`, `SYSTEM/`
- records ownership and mode in each `META/*_filesystem_config.txt`, including
  the parent directories, matching whichever path convention your package uses
- marks every Google apk `PRESIGNED` in `META/apkcerts.txt` — they are signed
  with Google's keys, and re-signing them would break GMS and Play Integrity
- deletes superseded AOSP apps along with their `filesystem_config` and
  `apkcerts` records
- drops `IMAGES/` and `META/care_map.pb` so the images rebuild from the new trees
- reports how much each partition grew against its budget in `misc_info.txt`

SELinux labels are deliberately left alone: every path written falls under your
ROM's existing generic `file_contexts` rules, which already resolve to
`system_file`.

The toolchain and keys are checked **before** the merge starts, so a missing
binary fails in a second rather than after copying a multi-gigabyte package.

If you do not build your own ROM, use `recovery` or `module`.

## Layout

```
cmd/306gapps        CLI entry point
internal/manifest   release manifest schema and validation
internal/catalog    dependency and conflict resolution
internal/source     release index, manifest fetch, content-addressed cache
internal/stage      payload fetch and install-plan assembly
internal/build      the three output targets
internal/ota        target-files merge and AOSP signing chain
internal/build/templates
                    installer shell scripts, embedded into the binary
internal/tui        interactive picker
test/installer      runs the real recovery installer against a fake ROM tree
test/ota            drives the ota target against a synthetic target-files package
```

## Guarantees

- **Nothing unverified is installed.** Every payload is checked against the
  sha256 in the manifest before it enters the cache, and a corrupt download
  leaves no cache entry behind. The recovery installer re-checks each payload by
  digest on the device.
- **Archives are opened, not just hashed.** A digest only proves the bytes are
  the ones the source recorded. If the source recorded a truncated file -- which
  is what an older erofs-utils silently produces -- every checksum in the chain
  agrees and the package installs a broken app. Every apk, apex and jar is opened
  before it is packed, and a build that finds one unreadable fails rather than
  shipping it.
- **Dependencies and conflicts are resolved before anything is downloaded**, so a
  bad selection fails in milliseconds rather than after a gigabyte.
- **Builds are reproducible.** The same selection produces a byte-identical zip:
  entries are path-sorted with a fixed timestamp, and already-compressed formats
  are stored rather than re-deflated.
- **Symlinks are preserved.** Real dumps link an app's native libraries in from
  the partition's `lib64`; copying the link as a regular file, or dropping it,
  leaves an app that will not start.

## Testing

```
go test ./...
python3 test/installer/make_fixture.py /tmp/fixture
./test/installer/run.sh /path/to/306gapps /tmp/fixture
./test/ota/run.sh       /path/to/306gapps /tmp/fixture
```

`test/installer` builds a real recovery zip, installs it into a fake ROM tree
under busybox `ash`, and asserts the result — including that it refuses a
mismatched Android version and a full partition.

`test/ota` merges into a synthetic target-files package and drives stand-in AOSP
binaries, asserting both the contents of the merge and the exact commands the
signing chain runs.

## Licensing

This tool builds packages; it ships no Google software. The apps themselves come
from Google's Pixel images and are Google's, under Google's terms — the same
footing every other gapps distribution stands on. Redistributing them is not
something Google licenses, so run your own assets repo if that matters to you.
