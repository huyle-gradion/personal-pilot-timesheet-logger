import csv
import subprocess
import unittest

import test_tokens


class PigSourceTests(test_tokens.CaptureFixture):
    def test_pig_root_precedence_matches_official_configuration(self):
        cases = [
            ({}, ".pig/agent/sessions"),
            (
                {
                    "PIG_HOME": str(self.home / "pig home"),
                    "XDG_CONFIG_HOME": str(self.home / "xdg"),
                },
                "pig home/agent/sessions",
            ),
            (
                {
                    "PIG_CODING_AGENT_DIR": str(self.home / "agent override"),
                    "PIG_HOME": str(self.home / "pig home"),
                },
                "agent override/sessions",
            ),
            (
                {"PIG_HOME": "", "PIG_CODING_AGENT_DIR": "", "XDG_CONFIG_HOME": ""},
                ".pig/agent/sessions",
            ),
            ({"PIG_HOME": "~/pig home"}, "pig home/agent/sessions"),
            ({"PIG_CODING_AGENT_DIR": "~/agent override"}, "agent override/sessions"),
            ({"XDG_CONFIG_HOME": "~/xdg root"}, "xdg root/pig/agent/sessions"),
            ({"PIG_HOME": "~"}, "agent/sessions"),
            ({"PIG_USE_PI_DIRS": "true"}, ".pig/agent/sessions"),
        ]
        for index, (variables, selected) in enumerate(cases):
            with self.subTest(variables=variables):
                env = dict(self.env)
                env.pop("XDG_CONFIG_HOME", None)
                env.update(variables)
                out = self.home / f"precedence-{index}.csv"
                roots = [
                    ".pig/agent/sessions",
                    "pig home/agent/sessions",
                    "agent override/sessions",
                    "xdg/pig/agent/sessions",
                    "xdg root/pig/agent/sessions",
                    "agent/sessions",
                ]
                for root in roots:
                    self.transcript(
                        self.home / root / "fixture.jsonl",
                        {"input": roots.index(root) + 1},
                    )
                subprocess.run(
                    [
                        "python3",
                        str(test_tokens.ROOT / "scripts/tokens.py"),
                        "--out",
                        str(out),
                    ],
                    cwd=self.home,
                    env=env,
                    capture_output=True,
                    text=True,
                    check=True,
                )
                with out.open() as stream:
                    rows = list(csv.DictReader(stream))
                self.assertEqual(
                    [(r["session_id"], r["input_tokens"]) for r in rows],
                    [("pig:fixture", str(roots.index(selected) + 1))],
                )

    def test_explicit_pig_directory_overrides_environment_and_legacy_fallback(self):
        self.env.update(
            PIG_HOME="~/pig home",
            PIG_CODING_AGENT_DIR="~/agent override",
            PIG_USE_PI_DIRS="1",
        )
        for root in [
            "pig home/agent/sessions",
            "agent override/sessions",
            ".config/pig/agent/sessions",
            "explicit sessions",
        ]:
            self.transcript(
                self.home / root / "fixture.jsonl",
                {"input": 5 if root == "explicit sessions" else 999},
            )
        self.capture("--pig-dir", str(self.home / "explicit sessions"))
        self.assertEqual(
            [(r["session_id"], r["input_tokens"]) for r in self.rows()],
            [("pig:fixture", "5")],
        )

    def test_shared_pi_store_is_honest_and_counted_once_when_sessions_grow(self):
        for custom in [False, True]:
            with self.subTest(custom_pi_agent=custom):
                self.out = self.home / f"shared-{custom}.csv"
                for name in [
                    "PIG_USE_PI_DIRS",
                    "PI_CODING_AGENT_DIR",
                    "PIG_HOME",
                    "PIG_CODING_AGENT_DIR",
                ]:
                    self.env.pop(name, None)
                if custom:
                    self.env["PI_CODING_AGENT_DIR"] = "~/pi shared agent"
                root = (
                    self.home
                    / ("pi shared agent" if custom else ".pi/agent")
                    / "sessions"
                )
                self.transcript(root / "shared.jsonl", {"input": 5})
                self.capture()
                self.env.update(
                    PIG_USE_PI_DIRS="1",
                    PIG_HOME="~/ignored home",
                    PIG_CODING_AGENT_DIR="~/ignored agent",
                )
                self.transcript(
                    self.home / "ignored agent/sessions/ignored.jsonl", {"input": 999}
                )
                self.capture()
                self.transcript(root / "shared.jsonl", {"input": 7})
                self.capture()
                self.assertEqual(
                    [(r["session_id"], r["input_tokens"]) for r in self.rows()],
                    [("pi+pig-shared:shared", "7")],
                )


if __name__ == "__main__":
    unittest.main()
