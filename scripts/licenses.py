#!/usr/bin/env python3
"""Check reviewed dependency notices and their packaged copies; --write refreshes them."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
TARGETS = ("darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64")
NOTICE_NAME = re.compile(r"^(license|licence|copying|notice|copyright)([._-].*)?$|^.*[._-]license$", re.I)
# This old, unused transitive module has no license in its selected revision.
# Never treat absence of a license as permission to compile or redistribute it.
UNLICENSED_UNUSED = {"github.com/chzyer/logex@v1.1.10"}
ALIASES = {
    "github.com/alicebob/miniredis/v2": "miniredis", "github.com/andybalholm/brotli": "brotli",
    "github.com/cespare/xxhash/v2": "xxhash", "github.com/ebitengine/purego": "purego",
    "github.com/egoist/mygo": "mygo", "github.com/elliotchance/phpserialize": "phpserialize",
    "github.com/go-text/typesetting": "go-text-typesetting", "github.com/klauspost/compress": "klauspost-compress",
    "github.com/nlpodyssey/gopickle": "gopickle", "github.com/pierrec/lz4/v4": "lz4",
    "github.com/redis/go-redis/v9": "go-redis", "github.com/vmihailenco/msgpack/v5": "msgpack",
    "github.com/vmihailenco/tagparser/v2": "tagparser", "github.com/yuin/gopher-lua": "gopher-lua",
    "go.uber.org/atomic": "uber-atomic", "golang.org/x/crypto": "x-crypto",
    "golang.org/x/image": "x-image", "golang.org/x/sys": "x-sys", "golang.org/x/text": "x-text",
    "gopkg.in/yaml.v3": "yaml",
}


def require(ok, message):
    if not ok:
        raise ValueError(message)


def go(*args, **kwargs):
    return subprocess.check_output(["go", *args], cwd=kwargs.pop("cwd", ROOT), text=True, **kwargs)


def module_graph():
    raw = go("list", "-m", "-json", "all")
    decoder, modules = json.JSONDecoder(), []
    while raw.strip():
        module, end = decoder.raw_decode(raw.lstrip())
        raw = raw.lstrip()[end:]
        require(not module.get("Replace"), "Review module replacements before generating notices")
        if not module.get("Main"):
            modules.append(module)
    return modules


def used_modules():
    used = set()
    for target in TARGETS:
        goos, arch = target.split("/")
        env = dict(os.environ, CGO_ENABLED="0", GOOS=goos, GOARCH=arch)
        used.update(go("list", "-deps", "-test", "-f", "{{if .Module}}{{.Module.Path}}{{end}}",
                       "./...", "github.com/egoist/mygo/cmd/mygo", env=env).splitlines())
    return used


def digest(data):
    return hashlib.sha256(data).hexdigest()


def save(path, data):
    if path.is_file():
        if path.read_bytes() == data:
            return
        path.chmod(0o644)  # older copies inherited the module cache's read-only mode
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)


def files_in(directory):
    return {p.relative_to(directory).as_posix(): p.read_bytes() for p in directory.rglob("*") if p.is_file()}


def bundled_files():
    files = files_in(ROOT / "third_party/licenses")
    files.update({name: (ROOT / name).read_bytes() for name in ("LICENSE", "THIRD_PARTY_NOTICES.md")})
    return files


def check_manual_notices(modules):
    for module in modules:
        if module["Path"] == "github.com/redis/go-redis/v9":
            header = f"Additional notices for {module['Path']} {module['Version']}\n"
            notice = ROOT / "third_party/licenses/go-redis-components.txt"
            require(notice.read_text(encoding="utf-8").startswith(header),
                    "Review go-redis file-level notices for the selected version before refreshing")


def check():
    directory = ROOT / "third_party/licenses"
    manifest = json.loads((directory / "inventory.json").read_text())
    modules = module_graph()
    check_manual_notices(modules)
    selected = {m["Path"]: m["Version"] for m in modules}
    require(selected == {m["path"]: m["version"] for m in manifest["modules"]},
            "Dependencies changed: review their licenses, then run python3 scripts/licenses.py --write")
    used = used_modules()
    for module in manifest["modules"]:
        require(module["file"] or module["path"] not in used,
                "Unlicensed dependency entered a build: " + module["path"])
    current = files_in(directory)
    require({name: digest(data) for name, data in current.items() if name != "inventory.json"} == manifest["files"],
            "License copies changed or are missing; review and regenerate the inventory")
    require(bundled_files() == files_in(ROOT / "resources/licenses"),
            "Packaged license copies are stale; run python3 scripts/licenses.py --write")
    print(f"License checks passed: {len(modules)} selected modules; five desktop targets; bundled notices match")


def write():
    directory = ROOT / "third_party/licenses"
    (directory / "modules").mkdir(parents=True, exist_ok=True)
    modules, used, records, generated = module_graph(), used_modules(), [], set()
    check_manual_notices(modules)
    rows = ["# Module license copies\n\n",
            "Generated by `python3 scripts/licenses.py --write`. This covers the selected Go module graph, "
            "including unused upstream dependencies. A notice copy does not mean that module or all its subcomponents "
            "are shipped. See `THIRD_PARTY_NOTICES.md` for the application's actual dependencies.\n\n",
            "| Module | Version | Upstream notice files | Copy |\n|---|---|---|---|\n"]
    for module in modules:
        path, version = module["Path"], module["Version"]
        source = module.get("Dir")
        if not source:
            # Do not add sums for unused modules to the application's go.sum.
            with tempfile.TemporaryDirectory(prefix="kivrix-license-source-") as temporary:
                download = json.loads(go("mod", "download", "-json", path + "@" + version, cwd=temporary,
                                         env=dict(os.environ, GOWORK="off")))
            require(not download.get("Error"), f"Cannot read license source: {path}")
            source = download["Dir"]
        source = Path(source)
        notices = sorted((p for p in source.rglob("*") if p.is_file() and NOTICE_NAME.fullmatch(p.name)),
                         key=lambda p: p.relative_to(source).as_posix())
        record = {"path": path, "version": version, "sources": [p.relative_to(source).as_posix() for p in notices], "file": ""}
        if not notices:
            require(path + "@" + version in UNLICENSED_UNUSED and path not in used, "Missing license: " + path)
            record["reason"] = "No license in this revision; not used by the app, its tests or the build tool. Use is blocked."
            copy = record["reason"]
        else:
            filename = "modules/" + path.replace("/", "_") + "_" + version + ".txt"
            data = b"\n\n".join(("--- " + p.relative_to(source).as_posix() + " ---\n\n").encode() + p.read_bytes() for p in notices)
            save(directory / filename, data)
            record["file"] = filename
            generated.add(filename)
            copy = f"[{filename}]({filename})"
            if path in ALIASES:
                primary = next(p for p in notices if p.parent == source and p.name.upper().startswith("LICENSE"))
                save(directory / (ALIASES[path] + ".txt"), primary.read_bytes())
        records.append(record)
        rows.append(f"| `{path}` | `{version}` | {len(notices)} | {copy} |\n")
    # Only remove outdated generated copies, never manually maintained notices.
    for file in (directory / "modules").glob("*.txt"):
        if file.relative_to(directory).as_posix() not in generated:
            file.unlink()
    save(directory / "MODULES.md", "".join(rows).encode())
    manifest = {"modules": records, "files": {name: digest(data) for name, data in sorted(files_in(directory).items()) if name != "inventory.json"}}
    save(directory / "inventory.json", (json.dumps(manifest, indent=2) + "\n").encode())
    bundle = ROOT / "resources/licenses"
    expected = bundled_files()
    for name, data in expected.items():
        file = bundle / name
        save(file, data)
    for name in files_in(bundle).keys() - expected.keys():
        (bundle / name).unlink()
    print("Refreshed license copies. Review the diff; this does not decide license compatibility.")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--write", action="store_true", help="refresh after manually reviewing new/changed licenses")
    args = parser.parse_args()
    try:
        write() if args.write else check()
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"License check failed: {error}\n")
