# 306gapps for Android

Same builder as the desktop app, on the phone. Pick a release and apps, get a
signed recovery zip, save it somewhere recovery can see it, flash.

Recovery zip only for now. No module or OTA target.

## Layout

- `../mobile` is the Go side: picker state, dependency resolution, download,
  pack, sign. It's the same `internal/` code the desktop app uses, exposed
  through gomobile. Anything structured crosses as JSON.
- `app/` is the Kotlin/Compose UI plus a foreground service that runs the
  build, since a `stock` selection is ~2 GB of downloads.

## Building

Needs Go, gomobile/gobind (`go install golang.org/x/mobile/cmd/{gomobile,gobind}@latest`),
the Android SDK and NDK 29.

```
export ANDROID_HOME=~/android-sdk
./build-core.sh              # go -> app/libs/core.aar, rerun after any go change
./gradlew assembleDebug
```

`core.aar` is arm64 only. That covers every phone a current gapps package
makes sense on.

On a linux/arm64 host gomobile panics with `unsupported GOARCH: arm64`, since it
looks for an NDK Google doesn't ship. `build-core.sh` works around it by building
an x86_64 gomobile, which needs qemu-user/binfmt to run.

## Tests

```
go test ../mobile          # picker state and a full build against a local fixture
./gradlew testDebugUnitTest
```

## Where things go on the device

- downloads: the app's cache dir. Android may clear it when space runs low; it's
  just a cache.
- signing key: app files dir. Uninstalling the app loses it, which only means
  the next package is signed by a new key.
- the zip: app files dir until you save or share it. Each build replaces the last.

## Updating

The app checks GitHub for a newer release on launch and offers it in a strip
above the picker. Tapping through downloads the apk and hands it to the system
installer, which asks the user to confirm -- Android has no way for an app to
install itself. The first time, it also has to be allowed as an install source.

Updates install over the running build because every release apk is signed
with the same key, which is why the release workflow refuses to publish a tag
without one. A dev build reports no update: it is ahead of the last tag.
