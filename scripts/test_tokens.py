import csv
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]


class CaptureFixture(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(
            prefix="timesheet metrics ", dir="/tmp/opencode"
        )
        self.addCleanup(self.temp.cleanup)
        self.home = Path(self.temp.name)
        self.env = dict(
            os.environ,
            HOME=str(self.home),
            XDG_CONFIG_HOME=str(self.home / "active config"),
        )
        for name in [
            "PI_CODING_AGENT_DIR",
            "PIG_HOME",
            "PIG_CODING_AGENT_DIR",
            "PIG_USE_PI_DIRS",
        ]:
            self.env.pop(name, None)
        self.out = self.home / "output.csv"

    def transcript(
        self, path, usage, marker="/a path/bin/timesheet next --date 2026-10-20"
    ):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(
            json.dumps(
                {"type": "session", "version": 3, "timestamp": "2026-10-20T09:00:00Z"}
            )
            + "\n"
            + json.dumps(
                {
                    "message": {
                        "role": "assistant",
                        "content": [
                            {
                                "type": "toolCall",
                                "name": "bash",
                                "arguments": {"command": marker},
                            }
                        ],
                    }
                }
            )
            + "\n"
            + json.dumps(
                {
                    "timestamp": "2026-10-20T09:01:00Z",
                    "message": {"role": "assistant", "usage": usage},
                }
            )
            + "\n"
        )

    def capture(self, *args):
        return subprocess.run(
            ["python3", str(ROOT / "scripts/tokens.py"), "--out", str(self.out), *args],
            cwd=self.home,
            env=self.env,
            capture_output=True,
            text=True,
            check=True,
        )

    def rows(self):
        with self.out.open() as stream:
            return list(csv.DictReader(stream))


class CaptureTests(CaptureFixture):
    def test_runtime_defaults_refresh_growing_sessions_without_duplicates(self):
        paths = [
            self.home / ".pi/agent/sessions/pi.jsonl",
            self.home / "active config/pig/agent/sessions/pig.jsonl",
            self.home / ".config/pig/agent/sessions/fallback.jsonl",
            self.home / ".claude/projects/repo/claude.jsonl",
        ]
        for path in paths:
            self.transcript(
                path, {"input": 10, "output": 3, "cacheRead": 2, "cacheWrite": 1}
            )
        self.capture()
        self.assertEqual(
            {r["session_id"] for r in self.rows()},
            {"pi:pi", "pig:pig", "pig:fallback", "claude:claude"},
        )
        self.transcript(
            paths[1], {"input": 20, "output": 6, "cacheRead": 4, "cacheWrite": 2}
        )
        self.capture()
        rows = self.rows()
        self.assertEqual(len(rows), 4)
        self.assertEqual(
            next(r["total_tokens"] for r in rows if r["session_id"] == "pig:pig"), "32"
        )

    def test_override_malformed_records_unrelated_sessions_and_missing_usage(self):
        store = self.home / "override with spaces"
        self.transcript(
            store / "valid.jsonl",
            {"input_tokens": 7, "output_tokens": 2, "cache_read_input_tokens": 1},
            '"/binary with spaces/timesheet" next',
        )
        with (store / "valid.jsonl").open("a") as stream:
            stream.write(
                '\nnot json\n[]\nnull\n{"message":{"usage":{"input":"bad"}}}\n'
            )
        self.transcript(
            store / "unrelated.jsonl",
            {"input": 999},
            "discuss timesheet next in a design document",
        )
        self.transcript(store / "empty.jsonl", {})
        self.capture("--pig-dir", str(store))
        self.assertEqual(
            [(r["session_id"], r["total_tokens"]) for r in self.rows()],
            [("pig:valid", "10")],
        )

    def test_missing_stores_are_unavailable(self):
        result = self.capture()
        self.assertIn("unavailable", result.stdout)
        self.assertFalse(self.out.exists())

    def test_empty_runtime_directory_environment_uses_defaults(self):
        self.env.update(PI_CODING_AGENT_DIR="", XDG_CONFIG_HOME="")
        self.transcript(self.home / ".pi/agent/sessions/pi.jsonl", {"input": 2})
        self.transcript(
            self.home / "pig/agent/sessions/unrelated.jsonl", {"input": 999}
        )
        self.capture()
        self.assertEqual([r["session_id"] for r in self.rows()], ["pi:pi"])

    def test_pi_override_and_claude_native_usage_remain_supported(self):
        store = self.home / "custom pi sessions"
        self.transcript(store / "pi.jsonl", {"input": 11, "output": 4, "cacheRead": 2})
        path = self.home / ".claude/projects/repo/claude.jsonl"
        self.transcript(
            path,
            {
                "input_tokens": 7,
                "output_tokens": 2,
                "cache_creation_input_tokens": 3,
                "cache_read_input_tokens": 1,
            },
        )
        data = path.read_text().replace(
            '"type": "toolCall", "name": "bash", "arguments":',
            '"type": "tool_use", "name": "Bash", "input":',
        )
        path.write_text(data)
        self.capture("--pi-dir", str(store))
        self.assertEqual(
            {r["session_id"]: r["total_tokens"] for r in self.rows()},
            {"pi:pi": "17", "claude:claude": "13"},
        )

    def test_unreadable_transcript_does_not_block_valid_data(self):
        store = self.home / "pig sessions"
        self.transcript(store / "valid.jsonl", {"input": 2, "output": 1})
        (store / "unreadable.jsonl").symlink_to("/proc/self/mem")
        self.capture("--pig-dir", str(store))
        self.assertEqual([r["total_tokens"] for r in self.rows()], ["3"])

    def test_terminal_helper_runs_from_foreign_cwd_for_both_outcomes(self):
        repo = self.home / "repo with spaces"
        helper = repo / ".agents/skills/timesheet/scripts/ts-metrics"
        helper.parent.mkdir(parents=True)
        (repo / "scripts").mkdir()
        for name in ["ts-env", "ts-metrics"]:
            shutil.copy2(
                ROOT / ".agents/skills/timesheet/scripts" / name, helper.parent / name
            )
        shutil.copy2(ROOT / "scripts/tokens.py", repo / "scripts/tokens.py")
        self.transcript(
            self.home / ".pi/agent/sessions/pi.jsonl", {"input": 5, "output": 1}
        )
        terminal_step = (
            (ROOT / ".agents/skills/timesheet/SKILL.md")
            .read_text()
            .split("## Terminal capture", 1)[1]
        )
        command = re.search(r"```sh\n(.*?)\n```", terminal_step, re.S).group(1)
        env = dict(self.env, SKILL_DIR=str(helper.parent.parent))
        for outcome in ["ready", "blocked"]:
            with self.subTest(outcome=outcome):
                result = subprocess.run(
                    ["sh", "-c", command],
                    cwd=self.home,
                    env=env,
                    capture_output=True,
                    text=True,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("total=6", result.stdout)
        with (repo / "benchmarks/tokens.csv").open() as stream:
            self.assertEqual(len(list(csv.DictReader(stream))), 1)
        self.assertFalse((self.home / "benchmarks").exists())
        (repo / "scripts/tokens.py").unlink()
        result = subprocess.run(
            [str(helper), "blocked"],
            cwd=self.home,
            env=self.env,
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 0)
        self.assertIn("unavailable", result.stderr)


if __name__ == "__main__":
    unittest.main()
