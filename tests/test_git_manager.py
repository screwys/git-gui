import subprocess
import tempfile
import unittest
from pathlib import Path

from core.git_manager import GitManager


class GitDiffTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.repo = Path(self.directory.name)
        self.git("init", "-q")
        self.manager = GitManager(str(self.repo))

    def git(self, *args):
        return subprocess.run(
            ["git", "-C", str(self.repo), *args],
            check=True, capture_output=True, text=True,
        ).stdout

    def test_large_untracked_binary_has_no_text_lines_or_patch(self):
        with (self.repo / "archive.zip").open("wb") as stream:
            stream.write(b"PK\0\x03\x04")
            stream.truncate(1024 * 1024 * 1024)
        items = self.manager.get_status_files()
        self.assertEqual([(i.path, i.additions) for i in items], [("archive.zip", 0)])
        for paths in (None, ["archive.zip"]):
            diff = self.manager.get_diff(paths)
            self.assertIn("Binary files /dev/null and b/archive.zip differ", diff)
            self.assertLess(len(diff), 200)

    def test_text_diffs_preserve_staged_unstaged_and_untracked_changes(self):
        tracked = self.repo / "tracked.txt"
        tracked.write_text("original\n")
        self.git("add", "tracked.txt")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com",
                 "commit", "-qm", "Initial")
        tracked.write_text("staged\n")
        self.git("add", "tracked.txt")
        tracked.write_text("staged\nworking\n")
        (self.repo / "new.txt").write_text("new\nlast")
        items = {i.path: i for i in self.manager.get_status_files()}
        self.assertEqual((items["new.txt"].additions, items["new.txt"].deletions), (2, 0))
        single = self.manager.get_diff(["tracked.txt"])
        for line in ("-original", "+staged", "+working"):
            self.assertIn(line, single)
        self.assertNotIn("new.txt", single)
        combined = self.manager.get_diff()
        self.assertIn(single, combined)
        self.assertIn("+new\n+last", combined)

    def test_line_counts_across_chunks_and_missing_final_newline(self):
        path = self.repo / "long.txt"
        for content, expected in ((b"", 0), (b"a\n", 1),
                                  (b"a" * 131072, 1),
                                  (b"a" * 65535 + b"\nlast", 2)):
            with self.subTest(expected=expected, size=len(content)):
                path.write_bytes(content)
                self.assertEqual(self.manager.get_status_files()[0].additions, expected)


if __name__ == "__main__":
    unittest.main()
