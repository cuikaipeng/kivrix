import base64
import hashlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import urllib.error

import release


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        work = tempfile.TemporaryDirectory()
        self.addCleanup(work.cleanup)
        self.root = Path(work.name)
        self.cfg = {"name": "Kivrix", "version": "0.2.0", "updates": {
            "github": "example/kivrix", "publicKey": base64.b64encode(bytes(range(32))).decode(),
        }}
        (self.root / "mygo.json").write_text(json.dumps(self.cfg))
        (self.root / "internal/app/locales").mkdir(parents=True)
        (self.root / "internal/app/update.go").write_text('const appVersion = "0.2.0"')
        for tag in ["en", "zh-Hans", "ja"]:
            (self.root / f"internal/app/locales/{tag}.json").write_text(json.dumps({"@@locale": tag}))
        root_patch = patch.object(release, "ROOT", self.root)
        root_patch.start()
        self.addCleanup(root_patch.stop)
        env_patch = patch.dict(release.os.environ, {}, clear=True)
        env_patch.start()
        self.addCleanup(env_patch.stop)

    def test_release_version_gate(self):
        self.assertEqual(release.config("v0.2.0")["version"], "0.2.0")
        for tag in ["v0.3.0", "v0.2.0-rc.1", "vpreview", "0.2.0"]:
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                release.config(tag)
        (self.root / "internal/app/update.go").write_text('const appVersion = "0.1.0"')
        with self.assertRaisesRegex(ValueError, "must match"):
            release.config("v0.2.0")

    def test_metadata_discovers_new_languages(self):
        languages = release.configure_package(self.cfg, self.root, ["darwin/universal"], False, "")
        self.assertEqual(languages, ["en", "ja", "zh-Hans"])
        self.assertEqual(self.cfg["macos"]["infoPlist"]["CFBundleLocalizations"], languages)
        for tag in languages:
            self.assertTrue((self.root / f"resources/darwin/{tag}.lproj/InfoPlist.strings").is_file())

    def test_default_packaging_needs_no_certificates(self):
        release.configure_package(self.cfg, self.root, release.TARGETS, False, "")
        self.assertEqual(self.cfg["macos"]["signingIdentity"], "-")
        self.assertNotIn("notarize", self.cfg["macos"])
        self.assertNotIn("certificate", self.cfg["windows"])
        self.assertNotIn("signCommand", self.cfg["windows"])

    def test_explicit_signing_does_not_fall_back_to_unsigned(self):
        for target in ["darwin/universal", "windows/amd64"]:
            with self.subTest(target=target), self.assertRaises(ValueError):
                release.configure_package(dict(self.cfg), self.root, [target], True, "")
        self.cfg["macos"] = {"signingIdentity": "Developer ID Application: Example (TEAM)"}
        with self.assertRaisesRegex(ValueError, "notarytool"):
            release.configure_package(self.cfg, self.root, ["darwin/universal"], True, "")

    def test_complete_asset_set_and_checksums(self):
        assets = self.root / "artifacts"
        assets.mkdir()
        expected = {n for t in release.TARGETS for n in release.asset_names("0.2.0", t) + release.update_names("0.2.0", t)}
        for name in expected:
            (assets / name).write_bytes(b"installer fixture")
        for target in release.TARGETS:
            archive, manifest = release.update_names("0.2.0", target)
            (assets / manifest).write_text(json.dumps({
                "version": "0.2.0",
                "url": f"https://github.com/example/kivrix/releases/download/v0.2.0/{archive}",
                "size": (assets / archive).stat().st_size,
                "signature": base64.b64encode(bytes(64)).decode(),
            }))
        release.verify_assets(assets)
        lines = (assets / "SHA256SUMS").read_text().splitlines()
        self.assertEqual(len(lines), 13)
        for line in lines:
            digest, name = line.split("  ")
            self.assertEqual(digest, hashlib.sha256((assets / name).read_bytes()).hexdigest())
        manifest = assets / "update-darwin-universal.json"
        original = manifest.read_text()
        for field, value in [("version", "0.1.0"), ("url", "https://example.test/wrong.tar.gz"), ("size", 0), ("signature", "")]:
            broken = json.loads(original)
            broken[field] = value
            manifest.write_text(json.dumps(broken))
            with self.subTest(field=field), self.assertRaises(ValueError):
                release.verify_assets(assets)
        manifest.write_text(original)
        (assets / "install.sh").unlink()
        with self.assertRaises(ValueError):
            release.verify_assets(assets)
        (assets / "install.sh").write_bytes(b"installer fixture")
        (assets / "old-version.dmg").write_bytes(b"old")
        with self.assertRaises(ValueError):
            release.verify_assets(assets)

    def test_update_key_required_and_must_match(self):
        with self.assertRaisesRegex(ValueError, "MYGO_UPDATER_PRIVATE_KEY"):
            release.require_update_key(self.cfg)
        private = bytes(32) + bytes(range(32))
        release.os.environ["MYGO_UPDATER_PRIVATE_KEY"] = base64.b64encode(private).decode()
        release.require_update_key(self.cfg)
        for invalid in ["bad key", base64.b64encode(bytes(64)).decode()]:
            release.os.environ["MYGO_UPDATER_PRIVATE_KEY"] = invalid
            with self.assertRaises(ValueError):
                release.require_update_key(self.cfg)

    def test_published_release_and_api_errors_block(self):
        release.os.environ.update(GH_TOKEN="fixture", GITHUB_REPOSITORY="example/kivrix")
        for draft in [True, False]:
            with patch.object(release.urllib.request, "urlopen", return_value=io.BytesIO(json.dumps({"draft": draft}).encode())):
                if draft:
                    release.guard("v0.2.0")
                else:
                    with self.assertRaisesRegex(ValueError, "already published"):
                        release.guard("v0.2.0")
        for status in [404, 403, 500]:
            error = urllib.error.HTTPError("https://example.test", status, "fixture", {}, None)
            with patch.object(release.urllib.request, "urlopen", side_effect=error):
                if status == 404:
                    release.guard("v0.2.0")
                else:
                    with self.assertRaises(urllib.error.HTTPError):
                        release.guard("v0.2.0")


if __name__ == "__main__":
    unittest.main()
