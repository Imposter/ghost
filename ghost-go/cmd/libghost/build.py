#!/usr/bin/env python3
"""Build libghost, the C shared library around ghost-go.

    python3 build.py                     # the host platform, into ./dist
    python3 build.py --os linux --arch arm64
    python3 build.py --docker            # linux/amd64 in golang:1.26, no local toolchain
    python3 build.py --os android --arch all --jnilibs app/android/src/main/jniLibs
    python3 build.py --out ../../../build/libghost

cgo needs a C toolchain for the target, so a cross build wants a cross
compiler in CC (see --cc). Without one, --docker builds the Linux library in
the official Go image. Android needs only the NDK: build.py finds it (--ndk,
ANDROID_NDK_HOME, ANDROID_NDK_ROOT, else the newest ndk/<version> in the SDK)
and calls its clang for each ABI at --android-api.

Output layout, which is what the Flutter side packages:

    <out>/include/ghost.h              the curated header (ffigen input)
    <out>/<os>-<arch>/libghost.so      linux
    <out>/<os>-<arch>/libghost.dylib   darwin
    <out>/<os>-<arch>/ghost.dll        windows
    <out>/android-<arch>/libghost.so   android (arm64, arm, amd64, 386)
    <out>/<os>-<arch>/libghost.h       the header cgo generated for that build
                                       (ghost.h on windows: cgo names it after
                                       the library)

--jnilibs DIR also copies each Android library to DIR/<abi>/libghost.so
(arm64-v8a, armeabi-v7a, x86_64, x86), the layout an app's src/main/jniLibs
wants.

Exit status is 0 only when the library and its header were produced and, for
Android, every LOAD segment is aligned for 16 KB pages.
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

# The oldest Android API level the Android libraries load on: 24, Android 7.0.
# NDK r27 and r28 still reach back to 21, but Flutter's minSdk is 24 and the
# three releases between add nothing worth supporting; an app with a higher
# minSdk may pass its own.
ANDROID_API = 24

# Each Android GOARCH: its clang target triple and its jniLibs ABI folder.
# "all" is the three ABIs Flutter ships (it has no 32-bit x86 engine); 386 can
# still be named on its own.
ANDROID_ABIS = {
    "arm64": ("aarch64-linux-android", "arm64-v8a"),
    "arm": ("armv7a-linux-androideabi", "armeabi-v7a"),
    "amd64": ("x86_64-linux-android", "x86_64"),
    "386": ("i686-linux-android", "x86"),
}
ANDROID_ALL = ("arm64", "arm", "amd64")

# Android 15 devices may run with 16 KB pages, and Google Play requires native
# libraries whose LOAD segments are aligned for them.
PAGE_ALIGN = 16384

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


def find_ndk(explicit: str | None) -> Path:
    """The NDK to build with: --ndk, the NDK variables, else the newest one
    in an SDK."""
    for candidate in (explicit, os.environ.get("ANDROID_NDK_HOME"), os.environ.get("ANDROID_NDK_ROOT")):
        if candidate:
            ndk = Path(candidate)
            if not (ndk / "toolchains" / "llvm").is_dir():
                raise SystemExit(f"build.py: {ndk} is not an NDK (no toolchains/llvm)")
            return ndk
    home = Path.home()
    sdks = [os.environ.get("ANDROID_HOME"), os.environ.get("ANDROID_SDK_ROOT")]
    sdks += {
        "Windows": [os.path.join(os.environ.get("LOCALAPPDATA", ""), "Android", "Sdk")],
        "Darwin": [str(home / "Library" / "Android" / "sdk")],
    }.get(platform.system(), [str(home / "Android" / "Sdk")])

    def version(p: Path) -> tuple[int, ...]:
        return tuple(int(x) if x.isdigit() else 0 for x in p.name.split("."))

    for sdk in filter(None, sdks):
        ndks = [p for p in (Path(sdk) / "ndk").glob("*") if (p / "toolchains" / "llvm").is_dir()]
        if ndks:
            return max(ndks, key=version)
    raise SystemExit("build.py: no Android NDK; pass --ndk or set ANDROID_NDK_HOME")


def ndk_tool(ndk: Path, name: str) -> str:
    """A program from the NDK's LLVM toolchain for this host."""
    host = {"Windows": "windows", "Darwin": "darwin", "Linux": "linux"}.get(platform.system(), "")
    # Every NDK host toolchain is x86_64, the macOS one included (universal).
    bin_dir = ndk / "toolchains" / "llvm" / "prebuilt" / f"{host}-x86_64" / "bin"
    exe = bin_dir / (name + ".exe" if host == "windows" else name)
    if not exe.exists():
        raise SystemExit(f"build.py: {ndk} has no {name} for this host ({exe})")
    return str(exe)


def build_android(goarch: str, ndk: Path, api: int, out: Path, extra: list[str]) -> Path:
    triple, _ = ANDROID_ABIS[goarch]
    target = out / f"android-{goarch}"
    target.mkdir(parents=True, exist_ok=True)
    lib = target / "libghost.so"

    env = dict(os.environ)
    env["CGO_ENABLED"] = "1"
    env["GOOS"] = "android"
    env["GOARCH"] = goarch
    if goarch == "arm":
        env["GOARM"] = "7"
    # The NDK's clang with an explicit target rather than its <triple><api>-clang
    # wrappers, which are .cmd scripts on Windows. Go splits CC on spaces and
    # honours quotes, for an NDK path that has spaces.
    env["CC"] = f'"{ndk_tool(ndk, "clang")}" --target={triple}{api}'

    # -checklinkname=0: github.com/wlynxg/anet, which pion's stdnet uses to list
    # interfaces on Android 11+ (where an app may not send RTM_GETLINK), reaches
    # net.zoneCache through go:linkname, and Go 1.23+ refuses that link without it.
    ldflags = f"-s -w -checklinkname=0 -extldflags=-Wl,-z,max-page-size={PAGE_ALIGN}"
    run(["go", "build", "-buildmode=c-shared", "-trimpath", f"-ldflags={ldflags}",
         *extra, "-o", str(lib), PACKAGE], env=env)
    check_alignment(ndk, lib)
    return lib


def check_alignment(ndk: Path, lib: Path) -> None:
    """Fail unless every LOAD segment of lib is aligned for 16 KB pages."""
    proc = subprocess.run([ndk_tool(ndk, "llvm-readelf"), "-lW", str(lib)], capture_output=True, text=True)
    if proc.returncode != 0:
        raise SystemExit(f"build.py: llvm-readelf {lib}: {proc.stderr.strip()}")
    aligns = [int(fields[-1], 16) for fields in map(str.split, proc.stdout.splitlines())
              if fields[:1] == ["LOAD"]]
    if not aligns or min(aligns) < PAGE_ALIGN:
        raise SystemExit(f"build.py: {lib} has LOAD segments aligned to {[hex(a) for a in aligns]}, "
                         f"not {PAGE_ALIGN:#x} (16 KB pages)")
    print(f"{lib}: LOAD segments aligned to {min(aligns):#x}", flush=True)


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
    p.add_argument("--ndk", default=None, help="Android NDK (default: ANDROID_NDK_HOME, else the SDK's newest)")
    p.add_argument("--android-api", type=int, default=ANDROID_API,
                   help=f"minimum Android API level (default: {ANDROID_API})")
    p.add_argument("--jnilibs", default=None,
                   help="also copy each Android library to DIR/<abi>/libghost.so")
    args = p.parse_args()

    if not args.goos:
        return fail(f"unsupported host {platform.system()}; pass --os")
    out = Path(args.out).resolve()
    extra = ["-tags", args.tags] if args.tags else []

    if args.goos == "android":
        if args.docker:
            return fail("--docker builds linux only")
        if args.cc:
            return fail("--cc does not apply to android: the NDK's clang builds each ABI (see --ndk)")
        arches = ANDROID_ALL if args.goarch == "all" else (args.goarch,)
        if unknown := [a for a in arches if a not in ANDROID_ABIS]:
            return fail(f"unknown android arch {unknown[0]!r} (want {', '.join(ANDROID_ABIS)} or all)")
        ndk = find_ndk(args.ndk)
        print(f"NDK {ndk}, API {args.android_api}", flush=True)
        libs = [build_android(a, ndk, args.android_api, out, extra) for a in arches]
        if args.jnilibs:
            for arch, lib in zip(arches, libs):
                abi_dir = Path(args.jnilibs) / ANDROID_ABIS[arch][1]
                abi_dir.mkdir(parents=True, exist_ok=True)
                shutil.copy2(lib, abi_dir / lib.name)
        return finish(out, libs)

    if args.docker:
        if args.goos != "linux":
            return fail("--docker builds linux only")
        lib = build_docker(args.goarch, out, extra)
    else:
        cc = args.cc or default_cc(args.goos, args.goarch)
        lib = build_native(args.goos, args.goarch, cc, out, extra)
    return finish(out, [lib])


def finish(out: Path, libs: list[Path]) -> int:
    """Check every library has its header, and put the curated one beside them."""
    for lib in libs:
        header = lib.with_suffix(".h")
        if not lib.exists() or not header.exists():
            return fail(f"expected {lib} and {header}")

    include = out / "include"
    include.mkdir(parents=True, exist_ok=True)
    shutil.copy2(HERE / "ghost.h", include / "ghost.h")

    print()
    for lib in libs:
        print(f"{lib}  ({lib.stat().st_size // 1024} KiB)")
        print(f"{lib.with_suffix('.h')}  (generated)")
    print(f"{include / 'ghost.h'}  (curated, for ffigen)")
    return 0


def fail(msg: str) -> int:
    print(f"build.py: {msg}", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
