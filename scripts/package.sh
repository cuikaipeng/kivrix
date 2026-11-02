#!/usr/bin/env bash
set -euo pipefail

# No certificates needed by default; --signed enables optional certificate checks.
OS="$(go env GOOS)"
ARCH="$(go env GOARCH)"
if [ "$OS" = darwin ]; then ARCH=universal; fi
PLATFORM="${PLATFORM:-$OS/$ARCH}"
exec python3 "$(dirname "${BASH_SOURCE[0]}")/release.py" package --platform "$PLATFORM" "$@"
