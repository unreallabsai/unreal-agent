import hashlib
import importlib.util
import io
import pathlib
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location(
    "generate_homebrew", pathlib.Path(__file__).with_name("generate-homebrew.py")
)
generator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(generator)


class ReleasePackagingTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = pathlib.Path(self.directory.name)
        self.dist = self.root / "dist"
        self.dist.mkdir()
        self.tap = self.root / "tap"
        self.create_release("0.3.0")

    def create_release(self, version, extra_member=None, executable=True):
        checksums = []
        for binary in ("unreal-agent-runner", "unreal-agent-tui"):
            for target_os in ("darwin", "linux"):
                for arch in ("amd64", "arm64"):
                    name = f"{binary}_{version}_{target_os}_{arch}.tar.gz"
                    archive = self.dist / name
                    with tarfile.open(archive, "w:gz") as contents:
                        names = [binary, "LICENSE"]
                        if extra_member:
                            names.append(extra_member)
                        for member_name in names:
                            member = tarfile.TarInfo(member_name)
                            member.mode = 0o755 if member_name == binary and executable else 0o644
                            member.size = 4
                            contents.addfile(member, io.BytesIO(b"test"))
                    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
                    checksums.append(f"{digest}  ./{name}\n")
        (self.dist / "SHA256SUMS").write_text("".join(checksums))

    def generate(self, tag="v0.3.0"):
        generator.generate(tag, self.dist, self.tap)

    def test_all_platforms_and_aliases_are_installable(self):
        self.generate()
        formula = (self.tap / "Formula/unreal-agent.rb").read_text()
        for target_os in ("darwin", "linux"):
            for arch in ("amd64", "arm64"):
                self.assertIn(f"unreal-agent-tui_0.3.0_{target_os}_{arch}.tar.gz", formula)
        self.assertIn('depends_on "unreallabsai/tap/unreal-agent-runner"', formula)
        for alias in ("unreal-agent-tui", "uat"):
            self.assertEqual(
                (self.tap / "Aliases" / alias).resolve(),
                (self.tap / "Formula/unreal-agent.rb").resolve(),
            )
        # Retrying a completed publication preserves exactly the same formula.
        self.generate()
        self.assertEqual(formula, (self.tap / "Formula/unreal-agent.rb").read_text())

    def test_corrupted_download_does_not_change_tap(self):
        self.generate()
        current = (self.tap / "Formula/unreal-agent.rb").read_bytes()
        archive = self.dist / "unreal-agent-tui_0.3.0_linux_arm64.tar.gz"
        archive.write_bytes(archive.read_bytes() + b"corrupted")
        with self.assertRaisesRegex(ValueError, "checksum mismatch"):
            self.generate()
        self.assertEqual(current, (self.tap / "Formula/unreal-agent.rb").read_bytes())

    def test_missing_platform_does_not_publish_partial_formula(self):
        (self.dist / "unreal-agent-tui_0.3.0_linux_arm64.tar.gz").unlink()
        with self.assertRaises(FileNotFoundError):
            self.generate()
        self.assertFalse(self.tap.exists())

    def test_unsafe_archive_is_rejected_even_with_valid_checksum(self):
        self.create_release("0.3.0", extra_member="../outside")
        with self.assertRaisesRegex(ValueError, "unexpected archive contents"):
            self.generate()
        self.assertFalse(self.tap.exists())

    def test_non_executable_binary_is_rejected(self):
        self.create_release("0.3.0", executable=False)
        with self.assertRaisesRegex(ValueError, "not executable"):
            self.generate()

    def test_duplicate_checksum_is_rejected(self):
        sums = self.dist / "SHA256SUMS"
        sums.write_text(sums.read_text() * 2)
        with self.assertRaisesRegex(ValueError, "duplicate"):
            self.generate()

    def test_late_old_release_cannot_downgrade_tap(self):
        self.generate()
        current = (self.tap / "Formula/unreal-agent.rb").read_bytes()
        self.create_release("0.2.0")
        with self.assertRaisesRegex(ValueError, "refusing to downgrade"):
            self.generate("v0.2.0")
        self.assertEqual(current, (self.tap / "Formula/unreal-agent.rb").read_bytes())

    def test_only_stable_tags_are_accepted(self):
        for tag in ("v0.3.0-beta.1", "v01.3.0", "0.3.0", 'v0.3.0"; puts "bad'):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                self.generate(tag)
        self.assertFalse(self.tap.exists())


if __name__ == "__main__":
    unittest.main()
