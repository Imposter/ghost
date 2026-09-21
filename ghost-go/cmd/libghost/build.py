#!/usr/bin/env python3
"""Build libghost, the C shared library around ghost-go.

    python3 build.py                     # the host platform, into ./dist
    python3 build.py --os linux --arch arm64
    python3 build.py --docker            # linux/amd64 in golang:1.26, no local toolchain
    python3 build.py --out ../../../build/libghost

cgo needs a C toolchain for the target, so a cross build wants a cross
compiler in CC (see --cc). Without one, --docker builds the Linux library in
the official Go image.

Output layout, which is what the Flutter side packages:

    <out>/include/ghost.h              the curated header (ffigen input)
    <out>/<os>-<arch>/libghost.so      linux
    <out>/<os>-<arch>/libghost.dylib   darwin
    <out>/<os>-<arch>/ghost.dll        windows
    <out>/<os>-<arch>/libghost.h       the header cgo generated for that build
                                       (ghost.h on windows: cgo names it after
                                       the library)

Exit status is 0 only when the library and its header were produced.
"""

from __future__ import annotations

import argparse
import os
import platform
import shutil
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
MODULE = HERE.parent.parent  # ghost-go
REPO = MODULE.parent
PACKAGE = "./cmd/libghost"

# The Go image used by --docker. It must match the go directive in go.mod.
DOCKER_IMAGE = "golang:1.26"

# Go's name for this machine's architecture.
HOST_ARCH = {
    "amd64": "amd64", "x86_64": "amd64", "AMD64": "amd64",
    "arm64": "arm64", "aarch64": "arm64", "ARM64": "arm64",
}.get(platform.machine(), platform.machine().lower())


def library_name(goos: str) -> str:
    if goos == "windows":
        return "ghost.dll"
    if goos == "darwin":
        return "libghost.dylib"
    return "libghost.so"


def run(cmd: list[str], env: dict[str, str] | None = None, cwd: Path = MODULE) -> None:
    printable = " ".join(cmd)
    print(f"$ {printable}", flush=True)
    proc = subprocess.run(cmd, cwd=str(cwd), env=env)
    if proc.returncode != 0:
        raise SystemExit(f"build.py: {cmd[0]} failed with status {proc.returncode}")


def default_cc(goos: str, goarch: str) -> str | None:
    """A cross compiler to try when the caller named none."""
    host_os = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}.get(platform.system(), "")
    if goos == host_os and goarch == HOST_ARCH:
        return None  # the host compiler, whatever Go finds
    if goos == "windows":
        return "x86_64-w64-mingw32-gcc" if goarch == "amd64" else "aarch64-w64-mingw32-gcc"
    if goos == "linux":
        return "x86_64-linux-gnu-gcc" if goarch == "amd64" else "aarch64-linux-gnu-gcc"
    return None


def check_toolchain(cc: str | None, goos: str) -> None:
    """Fail early, and with advice, when cgo has no C compiler."""
    name = cc or os.environ.get("CC") or ("clang" if goos == "darwin" else "gcc")
    if shutil.which(name):
        return
    hint = {
        "windows": "install mingw-w64 (CC=x86_64-w64-mingw32-gcc) or the MSVC build tools",
        "darwin": "install the Xcode command line tools",
        "linux": "install gcc, or pass --docker",
    }.get(goos, "install a C toolchain for the target")
    raise SystemExit(f"build.py: no C compiler {name!r} on PATH; {hint}")


def build_native(goos: str, goarch: str, cc: str | None, out: Path, extra: list[str]) -> Path:
    target = out / f"{goos}-{goarch}"
    target.mkdir(parents=True, exist_ok=True)
    lib = target / library_name(goos)

    env = dict(os.environ)
    env["CGO_ENABLED"] = "1"
    env["GOOS"] = goos
    env["GOARCH"] = goarch
    if cc:
        env["CC"] = cc
    check_toolchain(cc, goos)

    cmd = ["go", "build", "-buildmode=c-shared", "-trimpath", *extra, "-o", str(lib), PACKAGE]
    run(cmd, env=env)
    return lib


def build_docker(goarch: str, out: Path, extra: list[str]) -> Path:
    target = out / f"linux-{goarch}"
    target.mkdir(parents=True, exist_ok=True)
    # The output directory is mounted on its own, so --out may point anywhere,
    # inside the repository or not.
    inner = " ".join(
        ["go", "build", "-buildmode=c-shared", "-trimpath", *extra,
         "-o", "/out/libghost.so", PACKAGE]
    )
    run(
        ["docker", "run", "--rm",
         "-v", f"{REPO}:/src",
         "-v", f"{target.resolve()}:/out",
         "-w", "/src/ghost-go",
         "-e", "CGO_ENABLED=1",
         "-e", f"GOARCH={goarch}",
         DOCKER_IMAGE, "sh", "-c", inner],
        cwd=REPO,
    )
    return target / "libghost.so"


def main() -> int:
    host_os = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}.get(platform.system(), "")
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--os", dest="goos", default=host_os, help="GOOS (default: this machine)")
    p.add_argument("--arch", dest="goarch", default=HOST_ARCH, help="GOARCH (default: this machine)")
    p.add_argument("--cc", default=None, help="C compiler for cgo (default: the host's, or a cross compiler)")
    p.add_argument("--out", default=str(HERE / "dist"), help="output directory (default: ./dist)")
    p.add_argument("--docker", action="store_true", help=f"build linux in {DOCKER_IMAGE} instead of locally")
    p.add_argument("--tags", default="", help="comma-separated build tags")
    args = p.parse_args()

    if not args.goos:
        return fail(f"unsupported host {platform.system()}; pass --os")
    out = Path(args.out).resolve()
    extra = ["-tags", args.tags] if args.tags else []

    if args.docker:
        if args.goos != "linux":
            return fail("--docker builds linux only")
        lib = build_docker(args.goarch, out, extra)
    else:
        cc = args.cc or default_cc(args.goos, args.goarch)
        lib = build_native(args.goos, args.goarch, cc, out, extra)

    header = lib.with_suffix(".h")
    if not lib.exists() or not header.exists():
        return fail(f"expected {lib} and {header}")

    include = out / "include"
    include.mkdir(parents=True, exist_ok=True)
    shutil.copy2(HERE / "ghost.h", include / "ghost.h")

    print(f"\n{lib}  ({lib.stat().st_size // 1024} KiB)")
    print(f"{header}  (generated)")
    print(f"{include / 'ghost.h'}  (curated, for ffigen)")
    return 0


def fail(msg: str) -> int:
    print(f"build.py: {msg}", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
