# Kandev — Windows native build

This directory's `Makefile.windows` builds kandev binaries natively on
Windows (no WSL / cross-compile). The full release bundle is assembled by
`scripts/release/build-pkg-windows.sh`. This README documents the one-time
environment setup.

## Prerequisites

| Tool | Version | Source |
|---|---|---|
| **MSYS2** | latest | <https://www.msys2.org/> |
| **mingw-w64-ucrt-x86_64-toolchain** | GCC ≤ 15.x | via pacman (see below) |
| **Node.js** | 22 LTS | <https://nodejs.org/> Windows `.msi` |
| **pnpm** | 10.x | `npm install -g pnpm@10` |
| **Go** | 1.21+ | <https://go.dev/dl/> Windows `.msi` |

> **Versions matter.** See [Gotchas](#gotchas) — picking newer versions of
> some of these will break the build.

## Setup

### 1. Install MSYS2

Run the installer from <https://www.msys2.org/>, accept defaults. After
install, open the **MSYS2 UCRT64** shell from Start menu (not the plain
`MSYS` one — UCRT64 produces native Windows binaries with the modern
runtime).

```bash
pacman -Syu                                       # update package db
pacman -S mingw-w64-ucrt-x86_64-toolchain         # gcc, binutils, gdb
pacman -S mingw-w64-ucrt-x86_64-make rsync        # make + rsync for build scripts
gcc --version                                     # verify GCC ≤ 15.x
```

> **Do NOT install `mingw-w64-ucrt-x86_64-nodejs` via pacman.** MSYS2's
> Node is compiled against MinGW; Rust-based npm packages (Next.js's
> `@swc/core`, etc.) ship prebuilt `.node` files compiled against MSVC
> Node and crash with `Node-API symbol has not been loaded` at install
> time. Use the official Windows Node installer instead (next step).

### 2. Install Node 22 LTS

Download the Windows `.msi` from <https://nodejs.org/dist/latest-v22.x/>
and run it. The installer adds `C:\Program Files\nodejs\` to system PATH.

### 3. Make MSYS2 see the Windows-installed Node

By default MSYS2 UCRT64 shells use `MSYS2_PATH_TYPE=minimal`, which
filters out user-PATH entries like `C:\Program Files\nodejs\`. Add to
your `~/.bashrc`:

```bash
export MSYS2_PATH_TYPE=inherit
```

Restart the UCRT64 shell. Verify:

```bash
which node            # /c/Program Files/nodejs/node
node --version        # v22.x
```

### 4. Install pnpm 10

```bash
npm install -g pnpm@10
pnpm --version        # 10.x
```

> **Why not pnpm 11?** The repo's `pnpm-lock.yaml` is generated with
> pnpm 10 (`lockfileVersion: '9.0'`). pnpm 11 reading it under
> `--frozen-lockfile` complains
> `ERR_PNPM_LOCKFILE_CONFIG_MISMATCH` on the `overrides` field even
> though the values are identical (internal hash differs). Using
> `--no-frozen-lockfile` regenerates the lock under format 10.0,
> diverging from upstream.

### 5. Install Go

Download the Windows `.msi` from <https://go.dev/dl/> and run it. PATH
is set automatically. Verify in UCRT64 shell:

```bash
go version            # go1.21+ on windows/amd64
```

## Build

From the repo root in MSYS2 UCRT64 shell:

```bash
# 1. Install JS deps (run once, or after pulling)
pnpm -C apps install --frozen-lockfile

# 2. Build native bundle
cd apps/backend
make -f Makefile.windows build-pkg
```

Output: `dist/kandev-windows-amd64/`. Layout:
- `bin/kandev.exe`, `bin/agentctl.exe` — Go binaries
- `web/server.js` — Next.js standalone
- `cli/dist/cli.bundle.js` — esbuild self-contained CLI
- `start.cmd` — launcher
- `README.txt` — end-user readme

Run `dist/kandev-windows-amd64/start.cmd` to launch.

## Gotchas

### GCC 16+ breaks Go cgo

w64devkit's current release ships GCC 16, which promotes object files to
COFF Big Object format (`ANON_OBJECT_HEADER_BIGOBJ`) when section count
exceeds 32k. Go's cgo parser (`debug/pe` package) does not recognize
this format and bails with:

```
cgo: cannot parse gcc output _cgo_N.o as ELF, Mach-O, PE, XCOFF object
```

There is no `--no-bigobj` assembler flag. **Workaround:** use MSYS2
UCRT64, which currently ships GCC 14.x/15.x.

If pacman ever updates UCRT64 to GCC 16, options are:
- pin the GCC package via `pacman -U` of an older `.pkg.tar.zst` from
  the [MSYS2 mingw archive](https://repo.msys2.org/mingw/ucrt64/);
- or migrate `mattn/go-sqlite3` → `modernc.org/sqlite` to drop CGO
  entirely (~1–2 hours, eliminates the whole problem class).

### MSYS2 nodejs vs upstream Node — ABI mismatch

Don't `pacman -S mingw-w64-ucrt-x86_64-nodejs`. Use Node from
nodejs.org. Reason: many JS packages with Rust-based native addons
(`@swc/core`, `@tailwindcss/oxide`, `@biomejs/biome`, `@parcel/watcher`,
…) ship prebuilt `.node` binaries compiled against the upstream
MSVC-built Node ABI. Loading them into MinGW-built MSYS2-Node panics:

```
Node-API symbol has not been loaded
```

### Bleeding-edge Node breaks prebuilt napi modules

Node 24/25 prebuilts may not exist for all napi packages. Stick with
Node 22 LTS until 24+ has full ecosystem coverage.

### `core.autocrlf=true` corrupts shell scripts

If `build-pkg-windows.sh` fails with `bad interpreter: No such file or
directory`, check `git config --get core.autocrlf` — it should be
`input` or `false`, not `true`. Otherwise `.sh` files get CRLF endings
on checkout and bash chokes on the shebang.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `cgo: cannot parse gcc output ... as ELF, Mach-O, PE, XCOFF object` | GCC 16+ from MSYS2/w64devkit | Pin GCC ≤ 15.x; see [GCC 16 gotcha](#gcc-16-breaks-go-cgo) |
| `Node-API symbol has not been loaded` during `pnpm install` | MSYS2 nodejs (ABI mismatch) or bleeding-edge Node | Use Node 22 LTS from nodejs.org |
| `ERR_PNPM_LOCKFILE_CONFIG_MISMATCH` | pnpm 11 reading pnpm 10 lockfile | `npm install -g pnpm@10` |
| `which node` returns nothing in UCRT64 shell | `MSYS2_PATH_TYPE=minimal` | `export MSYS2_PATH_TYPE=inherit` in `~/.bashrc`, restart shell |
| `bad interpreter: /usr/bin/env: No such file or directory` on `.sh` files | git autocrlf converted LF→CRLF | `git config --global core.autocrlf input`, re-checkout `.sh` files |
