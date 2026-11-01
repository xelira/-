#!/usr/bin/env bash
# Local-only Android build. Uses the toolchain under /mnt/storage/mobile-dev.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MOBILE_DEV_ROOT="${MOBILE_DEV_ROOT:-/mnt/storage/mobile-dev}"
# The repository directory contains '&', which ndk-build/make treats as a shell
# separator. Build from a clean mirror path without special characters.
MIRROR="${AND_ANDROID_MIRROR:-$MOBILE_DEV_ROOT/and-build}"
VERSION="${AND_VERSION:-2.0.0}"
NDK_VERSION="${AND_NDK_VERSION:-27.2.12479018}"

if [[ -f "$MOBILE_DEV_ROOT/env.sh" ]]; then
  # shellcheck disable=SC1091
  source "$MOBILE_DEV_ROOT/env.sh"
fi

: "${JAVA_HOME:?JAVA_HOME is not set}"
: "${ANDROID_HOME:?ANDROID_HOME is not set}"
export GRADLE_USER_HOME="${GRADLE_USER_HOME:-$MOBILE_DEV_ROOT/gradle}"
export ANDROID_USER_HOME="${ANDROID_USER_HOME:-$MOBILE_DEV_ROOT/android-user-home}"

NDK="$ANDROID_HOME/ndk/$NDK_VERSION"
if [[ ! -d "$NDK" ]]; then
  echo "NDK not found: $NDK" >&2
  echo "Install it with: sdkmanager --install 'ndk;$NDK_VERSION'" >&2
  exit 1
fi

TOOLCHAIN="$NDK/toolchains/llvm/prebuilt/linux-x86_64/bin"
if [[ ! -x "$TOOLCHAIN/aarch64-linux-android26-clang" ]]; then
  echo "NDK toolchain is incomplete: $TOOLCHAIN" >&2
  exit 1
fi

mkdir -p "$MIRROR"
rsync -a --delete \
  --exclude '.git' \
  --exclude '/data' \
  --exclude '/dist' \
  --exclude 'android/app/build' \
  --exclude 'android/.gradle' \
  "$ROOT/" "$MIRROR/"

cd "$MIRROR"
mkdir -p android/app/src/main/jniLibs

build_go() {
  local abi="$1" arch="$2" goarm="$3" cc="$4"
  local out="android/app/src/main/jniLibs/$abi"
  mkdir -p "$out"
  echo "==> Go $abi"
  env \
    CGO_ENABLED=1 \
    GOOS=android \
    GOARCH="$arch" \
    ${goarm:+GOARM="$goarm"} \
    CC="$TOOLCHAIN/$cc" \
    CXX="$TOOLCHAIN/${cc/clang/clang++}" \
    go build -buildmode=c-shared -trimpath \
      -ldflags "-s -w -X main.Version=$VERSION -extldflags=-Wl,-z,max-page-size=16384" \
      -o "$out/liband.so" ./cmd/and
  rm -f "$out/liband.h"
}

build_go arm64-v8a arm64 "" aarch64-linux-android26-clang
build_go armeabi-v7a arm 7 armv7a-linux-androideabi26-clang
build_go x86_64 amd64 "" x86_64-linux-android26-clang

if [[ ! -f android/local.properties ]]; then
  printf 'sdk.dir=%s\n' "$ANDROID_HOME" > android/local.properties
fi

if [[ ! -f android/keystore.properties ]]; then
  echo "android/keystore.properties is missing; release build would use the debug key." >&2
fi

cd android
gradle --no-daemon --console=plain assembleRelease

APK="app/build/outputs/apk/release/app-release.apk"
mkdir -p "$MOBILE_DEV_ROOT/artifacts"
cp "$APK" "$MOBILE_DEV_ROOT/artifacts/and-$VERSION.apk"
mkdir -p "$ROOT/dist"
cp "$APK" "$ROOT/dist/and-$VERSION.apk"
echo "APK: $MOBILE_DEV_ROOT/artifacts/and-$VERSION.apk"
echo "Copy: $ROOT/dist/and-$VERSION.apk"
