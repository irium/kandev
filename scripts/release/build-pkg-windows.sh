#!/usr/bin/env bash
# Assemble a self-contained Windows runtime bundle from cross-compiled binaries.
#
# Layout matches the release tarball / npm runtime package, so the existing
# kandev CLI launcher works against it via KANDEV_BUNDLE_DIR.
#
# Prerequisites:
#   * Cross-compiled Windows binaries:
#       apps/backend/bin/kandev-windows-amd64.exe
#       apps/backend/bin/agentctl-windows-amd64.exe
#     (produced by `make -f Makefile.windows build` in apps/backend/)
#   * pnpm available on PATH (used to build web + cli)
#
# Usage (from repo root or any subdir):
#   bash scripts/release/build-pkg-windows.sh
#
# Output:
#   dist/kandev-windows-amd64/
#     bin/kandev.exe, bin/agentctl.exe
#     web/...                 (Next.js standalone + static + public)
#     cli/bin/cli.js          (Node entrypoint, requires bundled dist)
#     cli/dist/cli.bundle.js  (esbuild self-contained bundle)
#     cli/package.json
#     start.cmd               (Windows launcher: sets KANDEV_BUNDLE_DIR, runs CLI)
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BACKEND_DIR="$ROOT_DIR/apps/backend"
CLI_DIR="$ROOT_DIR/apps/cli"
DIST_NAME="kandev-windows-amd64"
DIST_DIR="$ROOT_DIR/dist/$DIST_NAME"

KANDEV_EXE="$BACKEND_DIR/bin/kandev-windows-amd64.exe"
AGENTCTL_EXE="$BACKEND_DIR/bin/agentctl-windows-amd64.exe"

if [[ ! -f "$KANDEV_EXE" || ! -f "$AGENTCTL_EXE" ]]; then
  echo "Missing Windows binaries. Run: make -C apps/backend -f Makefile.windows build" >&2
  exit 1
fi

echo "→ Building Next.js standalone (web)..."
pnpm -C "$ROOT_DIR/apps" --filter @kandev/web build

echo "→ Building + bundling CLI..."
pnpm -C "$ROOT_DIR/apps" --filter kandev build
pnpm -C "$CLI_DIR" bundle

echo "→ Packaging web bundle (uses package-web.sh)..."
bash "$ROOT_DIR/scripts/release/package-web.sh"

echo "→ Assembling $DIST_DIR..."
rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR/bin" "$DIST_DIR/cli/bin" "$DIST_DIR/cli/dist"

cp "$KANDEV_EXE" "$DIST_DIR/bin/kandev.exe"
cp "$AGENTCTL_EXE" "$DIST_DIR/bin/agentctl.exe"

# package-web.sh writes to dist/web/ — move into our bundle
cp -R "$ROOT_DIR/dist/web" "$DIST_DIR/web"
rm -rf "$ROOT_DIR/dist/web"

# CLI: use the esbuild self-contained bundle (no node_modules needed)
cat > "$DIST_DIR/cli/bin/cli.js" <<'EOF'
#!/usr/bin/env node
require("../dist/cli.bundle.js");
EOF
cp "$CLI_DIR/dist/cli.bundle.js" "$DIST_DIR/cli/dist/cli.bundle.js"
cp "$CLI_DIR/package.json" "$DIST_DIR/cli/package.json"

# Windows launcher. Invokes `kandev` with no subcommand → CLI takes the
# release-bundle path (runRelease), which honors KANDEV_BUNDLE_DIR. Avoid
# `kandev start` here: that's the in-repo dev path and would walk up to find
# apps/backend/bin/ which is unrelated to the bundle.
# CRLF for cmd; %% is a literal % in printf format string.
printf '@echo off\r\nsetlocal\r\nset "ROOT=%%~dp0"\r\nset "KANDEV_BUNDLE_DIR=%%ROOT:~0,-1%%"\r\n\r\nset KANDEV_HEALTH_TIMEOUT_MS=120000\r\nnode "%%ROOT%%cli\\bin\\cli.js" --verbose %%*\r\n' \
  > "$DIST_DIR/start.cmd"

cat > "$DIST_DIR/README.txt" <<EOF
Kandev — local Windows runtime bundle.

Run:
  start.cmd

This launches the backend (kandev.exe) and the Next.js web frontend (via Node),
opens http://localhost:<webport> in your default browser. State (SQLite DB,
worktrees) lives in %USERPROFILE%\\.kandev by default; override with
KANDEV_HOME_DIR.

Requires: Node.js on PATH (for Next.js standalone).
EOF

echo
echo "✓ Bundle ready: $DIST_DIR"
echo "  Launch on Windows:  $DIST_NAME\\start.cmd"
