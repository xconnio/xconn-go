#!/bin/bash
set -euo pipefail

REPO="xconnio/xconn-go"
BIN_NAME="nxt"

OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
    Linux)
        GO_OS="linux"
        ARCHIVE_EXT="tar.gz"
        ;;
    Darwin)
        GO_OS="darwin"
        ARCHIVE_EXT="tar.gz"
        ;;
    MINGW*|MSYS*|CYGWIN*)
        GO_OS="windows"
        ARCHIVE_EXT="zip"
        ;;
    *)
        echo "Unsupported operating system: $OS"
        exit 1
        ;;
esac

case "$ARCH" in
    x86_64|amd64)
        GO_ARCH="amd64"
        ;;
    aarch64|arm64)
        GO_ARCH="arm64"
        ;;
    *)
        echo "Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

if [ "$GO_OS" = "windows" ]; then
    BIN_DIR="${BIN_DIR:-$HOME/bin}"
    INSTALLED_BIN="$BIN_DIR/${BIN_NAME}.exe"
else
    BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
    INSTALLED_BIN="$BIN_DIR/$BIN_NAME"
fi

mkdir -p "$BIN_DIR"

download() {
    local url="$1"
    local out="$2"

    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" -o "$out"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$out" "$url"
    else
        echo "Error: neither curl nor wget is installed."
        echo "Install curl: https://curl.se/ or wget: https://www.gnu.org/software/wget/"
        exit 1
    fi
}

if [ "$ARCHIVE_EXT" = "zip" ]; then
    command -v unzip >/dev/null 2>&1 || { echo "Error: unzip is required but not installed."; exit 1; }
else
    command -v tar >/dev/null 2>&1 || { echo "Error: tar is required but not installed."; exit 1; }
fi

echo "Resolving latest release..."
VERSION="$(download "https://api.github.com/repos/$REPO/releases/latest" - | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')"

if [ -z "$VERSION" ]; then
    echo "Failed to determine latest release version."
    exit 1
fi

VERSION_NO_V="${VERSION#v}"
ARCHIVE="${BIN_NAME}_${VERSION_NO_V}_${GO_OS}_${GO_ARCH}.${ARCHIVE_EXT}"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/$VERSION/$ARCHIVE"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Downloading $ARCHIVE from $DOWNLOAD_URL..."
download "$DOWNLOAD_URL" "$TMP_DIR/$ARCHIVE"

echo "Extracting archive..."
if [ "$ARCHIVE_EXT" = "zip" ]; then
    unzip -q "$TMP_DIR/$ARCHIVE" -d "$TMP_DIR"
    EXTRACTED_BIN="$TMP_DIR/${BIN_NAME}.exe"
else
    tar -xzf "$TMP_DIR/$ARCHIVE" -C "$TMP_DIR"
    EXTRACTED_BIN="$TMP_DIR/$BIN_NAME"
fi

if [ ! -f "$EXTRACTED_BIN" ]; then
    echo "Release archive does not contain $BIN_NAME."
    exit 1
fi

mv "$EXTRACTED_BIN" "$INSTALLED_BIN"

if [ "$GO_OS" != "windows" ]; then
    chmod 755 "$INSTALLED_BIN"
fi

echo "Installed $BIN_NAME $VERSION to $INSTALLED_BIN"
