import hashlib
from pathlib import Path
import tarfile
import tempfile
import unittest

import release


class ReleaseTests(unittest.TestCase):
    def test_versions_reject_tag_and_code_injection(self):
        for version in ("v0.1.0", "01.0.0", "0.1", "0.1.0-rc1", '0.1.0"', "../1.2.3"):
            with self.assertRaises(ValueError):
                release.validate_version(version)
        self.assertEqual(release.validate_version("0.1.0"), "0.1.0")

    def test_archives_are_reproducible_and_cask_matches_artifacts(self):
        with tempfile.TemporaryDirectory() as temp:
            dist = Path(temp)
            for arch in ("amd64", "arm64"):
                directory = dist / arch
                directory.mkdir()
                for name in release.FILES:
                    (directory / name).write_bytes((name + arch).encode())
            release.package("0.1.0", dist)
            first = {p.name: p.read_bytes() for p in dist.iterdir() if p.is_file()}
            release.package("0.1.0", dist)
            self.assertEqual(first, {p.name: p.read_bytes() for p in dist.iterdir() if p.is_file()})
            cask = (dist / "shortcut-cli.rb").read_text()
            for arch in ("amd64", "arm64"):
                path = dist / f"sc_0.1.0_darwin_{arch}.tar.gz"
                digest = hashlib.sha256(path.read_bytes()).hexdigest()
                self.assertIn(digest, cask)
                self.assertIn(f"{digest}  {path.name}", (dist / "checksums.txt").read_text())
                with tarfile.open(path) as archive:
                    self.assertEqual(archive.getnames(), list(release.FILES))
                    self.assertEqual(archive.getmember("sc").mode, 0o755)
                    self.assertTrue(all(m.uid == 0 and m.mtime == 0 for m in archive.getmembers()))
            self.assertIn('binary "sc"', cask)
            self.assertIn('depends_on macos: :ventura', cask)


if __name__ == "__main__":
    unittest.main()
