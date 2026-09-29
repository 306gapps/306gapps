#!/bin/sh
# binds ../mobile into app/libs/core.aar. rerun after touching any go code.
set -eu
cd "$(dirname "$0")/.."
: "${ANDROID_HOME:?set ANDROID_HOME}"
export ANDROID_NDK_HOME="${ANDROID_NDK_HOME:-$ANDROID_HOME/ndk/29.0.13113456}"

gomobile=gomobile
if [ "$(uname -s)-$(uname -m)" = Linux-aarch64 ]; then
  # gomobile panics on linux/arm64 hosts: it looks for an arm64 ndk that google
  # doesn't ship. an x86_64 gomobile (under qemu) finds the x86_64 one.
  gomobile=android/build/gomobile-amd64
  GOARCH=amd64 go build -o "$gomobile" golang.org/x/mobile/cmd/gomobile
fi

mkdir -p android/app/libs
"$gomobile" bind -target=android/arm64 -androidapi 28 -javapkg app.gapps306 \
  -ldflags="-s -w" -o android/app/libs/core.aar ./mobile
