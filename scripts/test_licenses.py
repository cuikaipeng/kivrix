import contextlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import licenses
import release


class LicenseTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        for name in ("LICENSE", "THIRD_PARTY_NOTICES.md"):
            (self.root / name).write_text(name)
        source = self.root / "upstream"
        (source / "nested").mkdir(parents=True)
        (source / "LICENSE").write_text("MIT license fixture")
        (source / "nested/NOTICE").write_text("Additional copyright holder")
        self.modules = [{"Path": "example.test/library", "Version": "v1.0.0", "Dir": str(source)}]
        self.used = {"example.test/library"}
        for name, value in [("ROOT", self.root), ("module_graph", lambda: self.modules), ("used_modules", lambda: self.used)]:
            mock = patch.object(licenses, name, value)
            mock.start()
            self.addCleanup(mock.stop)
        output = contextlib.redirect_stdout(io.StringIO())
        output.__enter__()
        self.addCleanup(output.__exit__, None, None, None)
        licenses.write()

    def test_nested_notices_and_bundled_copies_cannot_disappear(self):
        directory = self.root / "third_party/licenses"
        inventory = json.loads((directory / "inventory.json").read_text())
        module = inventory["modules"][0]
        self.assertIn("nested/NOTICE", module["sources"])
        self.assertIn("Additional copyright holder", (directory / module["file"]).read_text())
        licenses.check()
        bundled = self.root / "resources/licenses" / module["file"]
        bundled.unlink()
        with self.assertRaisesRegex(ValueError, "Packaged"):
            licenses.check()
        licenses.write()
        (directory / module["file"]).write_text("MIT license fixture")
        with self.assertRaisesRegex(ValueError, "copies changed"):
            licenses.check()

    def test_dependency_change_requires_new_review(self):
        self.modules[0]["Version"] = "v2.0.0"
        with self.assertRaisesRegex(ValueError, "Dependencies changed"):
            licenses.check()

    def test_refresh_cannot_silently_reuse_old_file_level_notices(self):
        notice = self.root / "third_party/licenses/go-redis-components.txt"
        notice.write_text("Additional notices for github.com/redis/go-redis/v9 v9.22.0\n")
        with self.assertRaisesRegex(ValueError, "file-level notices"):
            licenses.check_manual_notices([{"Path": "github.com/redis/go-redis/v9", "Version": "v9.23.0"}])

    def test_unlicensed_graph_only_module_cannot_enter_a_build(self):
        source = self.root / "unlicensed"
        source.mkdir()
        self.modules.append({"Path": "github.com/chzyer/logex", "Version": "v1.1.10", "Dir": str(source)})
        licenses.write()
        licenses.check()
        self.used.add("github.com/chzyer/logex")
        with self.assertRaisesRegex(ValueError, "Unlicensed dependency"):
            licenses.check()
        with self.assertRaisesRegex(ValueError, "Missing license"):
            licenses.write()

    def test_installers_and_updates_need_every_notice(self):
        expected = licenses.bundled_files()
        for prefix in ["licenses/", "Kivrix.app/Contents/Resources/licenses/", "opt/kivrix/licenses/"]:
            for complete in [True, False]:
                with self.subTest(prefix=prefix, complete=complete):
                    data = io.BytesIO()
                    with tarfile.open(fileobj=data, mode="w") as archive:
                        for name, content in expected.items():
                            if not complete and name != "LICENSE":
                                continue
                            entry = tarfile.TarInfo(prefix + name)
                            entry.size = len(content)
                            archive.addfile(entry, io.BytesIO(content))
                    data.seek(0)
                    with tarfile.open(fileobj=data) as archive:
                        if complete:
                            release.verify_archive_licenses(archive, prefix)
                        else:
                            with self.assertRaisesRegex(ValueError, "third-party notices"):
                                release.verify_archive_licenses(archive, prefix)


if __name__ == "__main__":
    unittest.main()
