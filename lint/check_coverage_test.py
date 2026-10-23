import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


CHECKER = Path(__file__).with_name("check_coverage.py")
SOURCE = "package sample\n\nfunc value() int {\n\tx := 1\n\tx++\n\tx++\n\tx++\n\treturn x\n}\n"


class CoverageCheckTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.git("init", "-q")
        (self.root / "go.mod").write_text("module sample\n\ngo 1.27.1\n")
        self.commit()
        self.base = self.git("rev-parse", "HEAD").stdout.strip()

    def git(self, *arguments):
        return subprocess.run(
            ["git", "-c", "user.name=Coverage", "-c", "user.email=coverage@example.invalid",
             "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", *arguments],
            cwd=self.root, text=True, capture_output=True, check=True,
        )

    def commit(self):
        self.git("add", ".")
        self.git("commit", "-qm", "fixture")

    def profile(self, hits, name="sample.go"):
        (self.root / name).write_text(SOURCE)
        entries = [f"sample/{name}:{line}.1,{line + 1}.1 1 {hit}"
                   for line, hit in zip(range(4, 9), hits)]
        (self.root / "coverage.out").write_text("mode: set\n" + "\n".join(entries) + "\n")

    def check(self, base=None):
        return subprocess.run(
            [sys.executable, str(CHECKER), "--base", base or self.base],
            cwd=self.root, text=True, capture_output=True,
            env={**os.environ, "GITHUB_STEP_SUMMARY": str(self.root / "summary.md")},
        )

    def test_rejects_uncovered_new_code(self):
        self.profile([0, 0, 0, 0, 0])
        result = self.check()
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("0.00%", result.stdout)
        self.assertIn("sample.go:4", result.stdout)

    def test_rejects_exactly_eighty_percent(self):
        self.profile([1, 1, 1, 1, 0])
        result = self.check()
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("80.00%", result.stdout)

    def test_accepts_covered_untracked_file_and_publishes_summary(self):
        self.profile([1, 1, 1, 1, 1])
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("100.00%", result.stdout)
        self.assertIn("100.00%", (self.root / "summary.md").read_text())

    def test_only_changed_lines_affect_the_threshold(self):
        self.profile([0, 0, 0, 0, 1])
        self.commit()
        self.base = self.git("rev-parse", "HEAD").stdout.strip()
        (self.root / "sample.go").write_text(SOURCE.replace("return x", "return x + 1"))
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("100.00%", result.stdout)

    def test_staged_changes_are_checked(self):
        self.profile([0, 0, 0, 0, 0])
        self.git("add", "sample.go")
        result = self.check()
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("0.00%", result.stdout)

    def test_includes_all_commits_since_base(self):
        self.profile([0, 0, 0, 0, 0])
        self.commit()
        (self.root / "README.md").write_text("Later documentation change\n")
        self.commit()
        result = self.check()
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("0.00%", result.stdout)

    def test_comment_only_changes_do_not_require_coverage(self):
        source = SOURCE.replace("\tx := 1", "\t// original comment\n\tx := 1")
        (self.root / "sample.go").write_text(source)
        self.commit()
        self.base = self.git("rev-parse", "HEAD").stdout.strip()
        (self.root / "sample.go").write_text(source.replace("original", "changed"))
        (self.root / "coverage.out").write_text("mode: set\nsample/sample.go:3.18,10.2 5 0\n")
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("no executable Go lines changed", result.stdout)

    def test_test_files_and_non_go_changes_do_not_require_coverage(self):
        self.profile([0, 0, 0, 0, 0], "sample_test.go")
        (self.root / "README.md").write_text("Documentation\n")
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("no executable Go lines changed", result.stdout)

    def test_file_names_with_spaces(self):
        self.profile([1, 1, 1, 1, 1], "file with spaces.go")
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("100.00%", result.stdout)

    def test_combines_repeated_blocks_without_double_counting(self):
        self.profile([1, 1, 1, 1, 1])
        with (self.root / "coverage.out").open("a") as profile:
            profile.write("sample/sample.go:4.1,5.1 1 0\n")
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("5/5 executable lines", result.stdout)

    def test_real_go_profile_including_cross_package_coverage(self):
        self.profile([1, 1, 1, 1, 1])
        (self.root / "sample.go").write_text(
            SOURCE.replace("func value", "func Value") + "\nfunc unused() int { return 0 }\n"
        )
        (self.root / "integration").mkdir()
        (self.root / "integration/check_test.go").write_text(
            'package integration\nimport ("testing"; "sample")\n'
            'func TestValue(t *testing.T) { if sample.Value() != 4 { t.Fatal("value") } }\n'
        )
        subprocess.run(
            ["go", "test", "-coverpkg=./...", "-coverprofile=coverage.out", "./..."],
            cwd=self.root, capture_output=True, text=True, check=True,
        )
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("83.33%", result.stdout)
        self.assertIn("sample.go:11", result.stdout)

    def test_missing_or_malformed_profiles_fail(self):
        for contents in [None, "not a profile\n", "mode: set\ninvalid record\n"]:
            with self.subTest(contents=contents):
                profile = self.root / "coverage.out"
                if contents is not None:
                    profile.write_text(contents)
                result = self.check()
                self.assertEqual(result.returncode, 2, result.stdout)
                self.assertIn("Coverage check failed", result.stderr)

    def test_unknown_baseline_fails(self):
        self.profile([1, 1, 1, 1, 1])
        result = self.check("missing-reference")
        self.assertEqual(result.returncode, 2, result.stdout)
        self.assertIn("Coverage check failed", result.stderr)


if __name__ == "__main__":
    unittest.main()
