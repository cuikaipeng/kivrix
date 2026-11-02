#!/usr/bin/env python3
"""Release checks and packaging, using Python's standard library and pinned MyGo."""

import argparse
import base64
import hashlib
import io
import json
import os
from pathlib import Path
import plistlib
import re
import shutil
import struct
import subprocess
import tarfile
import tempfile
import urllib.error
import urllib.parse
import urllib.request

import licenses

ROOT = Path(__file__).resolve().parent.parent
TARGETS = ("darwin/universal", "linux/amd64", "linux/arm64", "windows/amd64")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def config(tag=""):
    cfg = json.loads((ROOT / "mygo.json").read_text(encoding="utf-8"))
    version = cfg["version"]
    require(re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", version),
            "Releases currently require a stable X.Y.Z version (no prerelease/build suffix).")
    app = (ROOT / "internal/app/update.go").read_text(encoding="utf-8")
    match = re.search(r'\bappVersion\s*=\s*"([^"]+)"', app)
    require(match and match[1] == version, "mygo.json and appVersion must match")
    require(not tag or tag == "v" + version, f"Expected tag v{version}, got {tag}")
    return cfg


def guard(tag):
    """Allow new releases and draft retries; never replace published assets."""
    config(tag)
    repo = os.environ["GITHUB_REPOSITORY"]
    url = f"https://api.github.com/repos/{repo}/releases/tags/{urllib.parse.quote(tag, safe='')}"
    request = urllib.request.Request(url, headers={
        "Authorization": "Bearer " + os.environ["GH_TOKEN"],
        "Accept": "application/vnd.github+json",
        "User-Agent": "kivrix-release",
    })
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            release = json.load(response)
    except urllib.error.HTTPError as error:
        if error.code == 404:
            return
        raise
    require(release["draft"], f"{tag} is already published; use a new version instead of overwriting it")


def asset_names(version, target):
    platform = target.replace("/", "-")
    base = f"kivrix-{version}-{platform}"
    if target.startswith("darwin/"):
        return [base + ".dmg"]
    if target.startswith("windows/"):
        return [base + ".exe"]
    return [base + ".deb", base + ".tar.gz", "install.sh"]


def update_names(version, target):
    platform = target.replace("/", "-")
    return [f"kivrix-{version}-{platform}.tar.gz", f"update-{platform}.json"]


def require_update_key(cfg):
    """Update signatures use a local Ed25519 key, not an OS certificate."""
    key = os.environ.get("MYGO_UPDATER_PRIVATE_KEY", "").strip()
    require(key, "Set MYGO_UPDATER_PRIVATE_KEY to the update signing key before packaging.")
    try:
        private = base64.b64decode(key, validate=True)
        public = base64.b64decode(cfg["updates"]["publicKey"], validate=True)
    except (ValueError, KeyError) as error:
        raise ValueError("Invalid update signing configuration") from error
    require(len(private) == 64 and len(public) == 32 and private[32:] == public,
            "The update signing key must match updates.publicKey in mygo.json")


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def configure_package(cfg, source, targets, signed, sign):
    mac = cfg.setdefault("macos", {})
    mac["signingIdentity"] = sign or mac.get("signingIdentity", "-")
    if profile := os.environ.get("KIVRIX_NOTARY_PROFILE"):
        mac["notarize"] = {"keychainProfile": profile}
        if keychain := os.environ.get("KIVRIX_KEYCHAIN"):
            mac["notarize"]["keychain"] = keychain
    windows = cfg.setdefault("windows", {})
    if certificate := os.environ.get("KIVRIX_WINDOWS_CERTIFICATE") or windows.get("certificate"):
        windows["certificate"] = str((ROOT / certificate).resolve())
    if signed:
        if "darwin/universal" in targets:
            require(mac["signingIdentity"].startswith("Developer ID Application:"),
                    "--signed macOS packages require a Developer ID Application identity")
            require(mac.get("notarize", {}).get("keychainProfile"),
                    "--signed macOS packages require a notarytool profile")
        if "windows/amd64" in targets:
            require(windows.get("certificate") or windows.get("signCommand"),
                    "--signed Windows packages require code signing")

    # Our phrase IDs use MyGo's runtime catalog, without its accessor generator.
    # Derive AppKit metadata from the same files, before the bundle is signed.
    languages = []
    for file in sorted((source / "internal/app/locales").glob("*.json")):
        catalog = json.loads(file.read_text(encoding="utf-8"))
        require(catalog["@@locale"] == file.stem, f"Locale tag does not match {file.name}")
        languages.append(file.stem)
        directory = source / "resources/darwin" / (file.stem + ".lproj")
        directory.mkdir(parents=True, exist_ok=True)
        strings = directory / "InfoPlist.strings"
        if not strings.exists():
            strings.write_bytes(plistlib.dumps({"CFBundleDisplayName": catalog.get("mygo.appName", cfg["name"])}))
    require("en" in languages, "The English source catalog is missing")
    mac.setdefault("infoPlist", {}).update({
        "CFBundleDevelopmentRegion": "en", "CFBundleLocalizations": languages,
    })
    return languages


def deb_members(file):
    data = file.read_bytes()
    require(data.startswith(b"!<arch>\n"), f"Invalid Debian package: {file}")
    offset, members = 8, {}
    while offset < len(data):
        header = data[offset:offset + 60]
        size = int(header[48:58])
        members[header[:16].decode().strip().rstrip("/")] = data[offset + 60:offset + 60 + size]
        offset += 60 + size + size % 2
    return members


def verify_archive_licenses(tar, prefix):
    expected = licenses.bundled_files()
    actual = {}
    for member in tar.getmembers():
        name = member.name.removeprefix("./")
        if member.isfile() and name.startswith(prefix):
            actual[name[len(prefix):]] = tar.extractfile(member).read()
    require(actual == expected, "Archive is missing or has changed third-party notices")


def verify_package(cfg, target, directory, languages, signed):
    name, version = cfg["name"], cfg["version"]
    goos, arch = target.split("/")
    if goos == "darwin":
        app = directory / (name + ".app")
        plist = plistlib.loads((app / "Contents/Info.plist").read_bytes())
        require(plist["CFBundleShortVersionString"] == version, "Wrong macOS bundle version")
        require(plist["CFBundleLocalizations"] == languages, "Missing macOS bundle languages")
        require(licenses.files_in(app / "Contents/Resources/licenses") == licenses.bundled_files(),
                "Missing or changed bundled notices")
        binary = app / "Contents/MacOS" / name
        arches = subprocess.check_output(["lipo", "-archs", str(binary)], text=True).split()
        require(set(arches) == {"arm64", "x86_64"}, "The macOS app must be universal")
        run("codesign", "--verify", "--deep", "--strict", str(app))
        dmg = directory / f"{name} {version}.dmg"
        run("hdiutil", "verify", str(dmg), stdout=subprocess.DEVNULL)
        if signed:
            run("xcrun", "stapler", "validate", str(dmg))
            run("spctl", "--assess", "--type", "execute", str(app))
        return [dmg]
    if goos == "windows":
        binary = directory / (name + ".exe")
        data = binary.read_bytes()
        pe = struct.unpack_from("<I", data, 0x3C)[0]
        require(data[pe:pe + 4] == b"PE\0\0" and struct.unpack_from("<H", data, pe + 4)[0] == 0x8664,
                "Expected a Windows amd64 executable")
        setup = directory / f"{name} Setup {version} {arch}.exe"
        require(setup.is_file() and setup.stat().st_size > 0, "Missing NSIS installer")
        require(licenses.files_in(directory / "licenses") == licenses.bundled_files(),
                "Missing or changed bundled notices")
        command = "if ((Get-Item -LiteralPath $env:VERIFY_EXE).VersionInfo.ProductVersion -ne $env:VERIFY_VERSION) { throw 'Wrong Windows version' }; "
        if signed:
            command += "foreach ($p in @($env:VERIFY_EXE, $env:VERIFY_SETUP)) { if ((Get-AuthenticodeSignature -LiteralPath $p).Status -ne 'Valid') { throw 'Invalid Windows signature' } }"
        run("powershell", "-NoProfile", "-NonInteractive", "-Command", command,
            env=dict(os.environ, VERIFY_EXE=str(binary), VERIFY_SETUP=str(setup), VERIFY_VERSION=version))
        return [setup]
    archive = directory / f"kivrix-{version}-linux-{arch}.tar.gz"
    with tarfile.open(archive) as tar:
        verify_archive_licenses(tar, "licenses/")
        binary = tar.extractfile("kivrix").read(20)
        require(binary[:5] == b"\x7fELF\x02" and struct.unpack_from("<H", binary, 18)[0] == {"amd64": 62, "arm64": 183}[arch],
                f"Wrong Linux architecture: {arch}")
    deb = directory / f"kivrix_{version}_{arch}.deb"
    members = deb_members(deb)
    with tarfile.open(fileobj=io.BytesIO(members["control.tar.gz"])) as tar:
        entry = next(m for m in tar.getmembers() if m.name.removeprefix("./") == "control")
        control = tar.extractfile(entry).read().decode()
        require(f"Version: {version}\n" in control and f"Architecture: {arch}\n" in control, "Wrong DEB metadata")
        require("libwebkit" not in control, "Native packages must not depend on WebKitGTK")
    with tarfile.open(fileobj=io.BytesIO(members["data.tar.gz"])) as tar:
        verify_archive_licenses(tar, "opt/kivrix/licenses/")
    installer = directory / "install.sh"
    run("sh", "-n", str(installer))
    return [deb, archive, installer]


def package(args):
    cfg = config()
    require_update_key(cfg)
    licenses.check()
    targets = args.platform.split(",")
    require(all(t in TARGETS for t in targets), f"Supported targets: {', '.join(TARGETS)}")
    output = (ROOT / args.out).resolve()
    require(output != ROOT, "Use a separate build output directory")
    # A temporary source snapshot keeps generated metadata, Windows resources,
    # and signing configuration out of the working tree, including on failure.
    with tempfile.TemporaryDirectory(prefix="kivrix-package-") as work:
        source = Path(work) / "source"
        ignored = {".git", "build", ".mygo", ".agents", "__pycache__", "mygo-update.key", ".env", ".env.*"}
        if output.is_relative_to(ROOT):
            ignored.add(output.relative_to(ROOT).parts[0])
        shutil.copytree(ROOT, source, ignore=shutil.ignore_patterns(*ignored))
        languages = configure_package(cfg, source, targets, args.signed, args.sign)
        (source / "mygo.json").write_text(json.dumps(cfg, indent=2) + "\n", encoding="utf-8")
        command = ["go", "tool", "mygo", "build", "-platform", args.platform, "-o", str(output)]
        if not args.signed:
            command.append("-skip-notarize")
        run(*command, cwd=source)
        assets = {}
        for target in targets:
            directory = output / target.replace("/", "-")
            paths = verify_package(cfg, target, directory, languages, args.signed)
            for filename, path in zip(asset_names(cfg["version"], target), paths):
                if filename in assets:
                    require(path.read_bytes() == assets[filename].read_bytes(), f"Conflicting asset: {filename}")
                assets[filename] = path
            for filename in update_names(cfg["version"], target):
                path = directory / filename
                require(path.is_file() and path.stat().st_size > 0, f"Missing signed update: {filename}")
                if filename.endswith(".tar.gz") and not target.startswith("linux/"):
                    prefix = f"{cfg['name']}.app/Contents/Resources/licenses/" if target.startswith("darwin/") else "licenses/"
                    with tarfile.open(path) as archive:
                        verify_archive_licenses(archive, prefix)
                assets[filename] = path
        for goos in {t.split("/")[0] for t in targets}:
            destination = output / "release" / goos
            if destination.exists():
                shutil.rmtree(destination)
            destination.mkdir(parents=True)
            names = {n for t in targets if t.startswith(goos + "/")
                     for n in asset_names(cfg["version"], t) + update_names(cfg["version"], t)}
            for filename in names:
                shutil.copy2(assets[filename], destination / filename)
        print(f"Verified release assets: {output / 'release'}")


def verify_assets(directory):
    cfg = config()
    version = cfg["version"]
    expected = {n for t in TARGETS for n in asset_names(version, t) + update_names(version, t)}
    files = [f for f in directory.rglob("*") if f.is_file() and f.name != "SHA256SUMS"]
    require(len(files) == len(expected) and {f.name for f in files} == expected,
            f"Expected exactly these release assets: {', '.join(sorted(expected))}")
    by_name = {f.name: f for f in files}
    for target in TARGETS:
        archive, manifest = update_names(version, target)
        metadata = json.loads(by_name[manifest].read_text(encoding="utf-8"))
        url = f"https://github.com/{cfg['updates']['github']}/releases/download/v{version}/{archive}"
        require(metadata.get("version") == version and metadata.get("url") == url,
                f"Wrong version or URL in {manifest}")
        require(metadata.get("size") == by_name[archive].stat().st_size, f"Wrong archive size in {manifest}")
        require(len(base64.b64decode(metadata.get("signature", ""), validate=True)) == 64,
                f"Missing update signature in {manifest}")
    lines = []
    for file in sorted(files, key=lambda f: f.name):
        require(file.stat().st_size > 0, f"Empty asset: {file}")
        digest = hashlib.sha256()
        with file.open("rb") as stream:
            while chunk := stream.read(1024 * 1024):
                digest.update(chunk)
        lines.append(f"{digest.hexdigest()}  {file.name}\n")
    (directory / "SHA256SUMS").write_text("".join(lines), encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    check = commands.add_parser("check")
    check.add_argument("--tag", default=os.environ.get("GITHUB_REF_NAME", "") if os.environ.get("GITHUB_REF_TYPE") == "tag" else "")
    publish = commands.add_parser("guard")
    publish.add_argument("--tag", required=True)
    build = commands.add_parser("package")
    build.add_argument("--platform", "-platform", required=True)
    build.add_argument("-o", "--out", default="build")
    build.add_argument("--signed", action="store_true", help="require certificate signing and macOS notarization (optional)")
    build.add_argument("--sign", "-sign", default=os.environ.get("KIVRIX_MACOS_SIGNING_IDENTITY", ""))
    verify = commands.add_parser("verify")
    verify.add_argument("directory", type=Path)
    args = parser.parse_args()
    if args.command == "check":
        print("Release version:", config(args.tag)["version"])
    elif args.command == "guard":
        guard(args.tag)
    elif args.command == "package":
        package(args)
    else:
        verify_assets(args.directory)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        raise SystemExit(str(error))
