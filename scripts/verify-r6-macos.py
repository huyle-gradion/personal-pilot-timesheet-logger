#!/usr/bin/env python3
"""Run the remaining R6 UI checks. No owner config, credentials or live services.

Usage: python3 scripts/verify-r6-macos.py
Requires macOS, installed Go/Node/tmux, and the already installed pinned Pi tree.
Only temporary fixture/evidence files are written. No installation or source edit.
"""

import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import uuid

DAY = "2026-10-22"


def command(args, *, cwd=None, env=None, check=True, timeout=30):
    result = subprocess.run(args, cwd=cwd, env=env, text=True,
                            capture_output=True, stdin=subprocess.DEVNULL, timeout=timeout)
    if check and result.returncode:
        raise RuntimeError(f"{args[0]} exited {result.returncode}: {result.stderr.strip()}")
    return result


class UI:
    def __init__(self, repo, fixture, evidence, node, tmux, binary):
        self.repo, self.fixture, self.evidence = repo, fixture, evidence
        self.node, self.tmux, self.binary = node, tmux, binary
        self.socket = "r6-qa-" + uuid.uuid4().hex[:12]
        self.pids = set()
        self.sequence = 0

    def tm(self, *args, check=True):
        return command([self.tmux, "-L", self.socket, *args], check=check)

    def screen(self):
        return self.tm("capture-pane", "-p", "-t", "app:0.0").stdout

    def capture(self, name):
        text = self.screen()
        self.sequence += 1
        (self.evidence / f"{self.sequence:02d}-{name}.txt").write_text(text)
        return text

    def wait(self, predicate, name, seconds=15):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            text = self.screen()
            if predicate(text):
                return self.capture(name)
            time.sleep(0.1)
        self.capture("FAILED-" + name)
        raise RuntimeError("UI deadline: " + name + "; see terminal capture")

    def keys(self, *keys):
        self.tm("send-keys", "-t", "app:0.0", *keys)

    def text(self, text):
        self.tm("send-keys", "-t", "app:0.0", "-l", text)

    def start(self):
        args = ["env", "-i", "PATH=" + os.environ.get("PATH", "/usr/bin:/bin"),
                "HOME=" + str(self.fixture / "home"), "TERM=xterm-256color",
                self.node, str(self.repo / "pi/launch.mjs"),
                "--binary", str(self.binary), "--config", str(self.fixture / "config.json"),
                "--env-file", str(self.fixture / "empty.env"),
                "--profile-dir", str(self.fixture / "profile"),
                "--workdir", str(self.fixture / "work")]
        self.tm("-f", "/dev/null", "new-session", "-d", "-s", "app",
                "-x", "120", "-y", "42", shlex.join(args))
        self.wait(lambda s: str(self.fixture / "work") in s, "pi-started", 25)

    def open_day(self, day=DAY):
        self.text("/timesheet " + day)
        self.keys("Enter")
        self.wait(lambda s: "Timesheet " + day in s and "→" in s, "day-" + day)

    def review(self, confirmed, staged, *, dirty=None, day=DAY):
        def matches(s):
            totals = re.search(r"confirmed total .*?\((\d+)m\).*?staged total .*?\((\d+)m\)", s)
            if "Timesheet " + day not in s or not totals:
                return False
            if (int(totals[1]), int(totals[2])) != (confirmed, staged):
                return False
            return dirty is None or (("no unsaved changes" in s) == (not dirty))
        text = self.wait(matches, f"review-{confirmed}-{staged}")
        match = re.search(r"child pid (\d+)", text)
        if not match:
            raise RuntimeError("Missing real child PID in review")
        pid = int(match[1])
        self.pids.add(pid)
        return pid

    def action(self, label):
        for _ in range(24):
            text = self.screen()
            selected = re.search(r"^\s*→\s*(.+)$", text, re.M)
            if not selected:
                self.capture("FAILED-menu")
                raise RuntimeError("Action menu not focused: " + label)
            if selected[1].strip() == label:
                self.keys("Enter")
                return
            old = selected[1]
            self.keys("Down")
            self.wait(lambda s: (m := re.search(r"^\s*→\s*(.+)$", s, re.M))
                      is not None and m[1] != old, "menu-step", 3)
        raise RuntimeError("Action not available: " + label)

    def editor(self, text):
        self.wait(lambda s: "enter" in s.lower() and "cancel" in s.lower()
                  and "→" not in s, "interval-editor")
        self.keys("C-a", "C-k")
        self.text(text)
        self.keys("Enter")

    def add(self, end, description):
        self.action("Add interval")
        start = "09:00" if end == "10:00" else "10:00"
        self.editor(f"{DAY} {start}-{end} P1/DEV {description}")

    def cancel(self):
        self.action("Cancel staged changes")
        self.wait(lambda s: "Discard the staged draft" in s, "cancel-confirmation")
        self.keys("Enter")
        self.review(60, 60, dirty=False)

    def quit(self):
        self.keys("Escape")
        self.wait(lambda s: "Timesheet " + DAY not in s and "→" not in s,
                  "back-to-pi")
        self.keys("C-d")
        deadline = time.monotonic() + 12
        while time.monotonic() < deadline:
            if self.tm("has-session", "-t", "app", check=False).returncode:
                self.assert_exited()
                return
            time.sleep(0.1)
        self.capture("FAILED-normal-quit")
        raise RuntimeError("Normal Pi quit did not exit")

    def assert_exited(self):
        for pid in self.pids:
            try:
                os.kill(pid, 0)
            except ProcessLookupError:
                continue
            raise RuntimeError(f"Owned child {pid} still alive")

    def cleanup(self):
        self.tm("kill-server", check=False)
        # Only signal a captured PID if it still runs our unique fixture binary.
        for pid in self.pids:
            proc = command(["ps", "-p", str(pid), "-o", "comm="], check=False)
            if proc.stdout.strip() == str(self.binary):
                os.kill(pid, signal.SIGTERM)


def run(repo, root):
    """Driver can be exercised on another OS, but never labels that Darwin proof."""
    evidence = root / "evidence"
    fixture = root / "fixture"
    evidence.mkdir()
    fixture.mkdir()
    for name in ["home", "profile", "work"]:
        (fixture / name).mkdir(mode=0o700)
    config = {"timezone": "UTC", "state_path": str(fixture / "state.json"),
              "projects": [{"id": "P1", "name": "Synthetic QA",
                            "default_classification": "DEV"}]}
    (fixture / "config.json").write_text(json.dumps(config))
    (fixture / "empty.env").write_text("")
    tools = {name: shutil.which(name) for name in ["go", "node", "tmux"]}
    if not all(tools.values()):
        raise RuntimeError("Missing installed prerequisite: " +
                           ", ".join(k for k, v in tools.items() if not v))
    package = repo / "pi/node_modules/@earendil-works/pi-coding-agent/package.json"
    if not package.is_file() or json.loads(package.read_text())["version"] != "1.4.0":
        raise RuntimeError("Install the pinned Pi tree first: npm --prefix pi ci")
    binary = fixture / "timesheet"
    build_env = {k: os.environ[k] for k in ["HOME", "PATH"] if k in os.environ}
    build_env.update(GOENV="off", GOWORK="off", GOTOOLCHAIN="local", GOPROXY="off")
    command([tools["go"], "build", "-trimpath", "-o", str(binary), "./cmd/timesheet"],
            cwd=repo, env=build_env, timeout=120)
    version = command([str(binary), "version"]).stdout.strip()
    ui = UI(repo, fixture, evidence, tools["node"], tools["tmux"], binary)
    results = {"platform": platform.system(), "architecture": platform.machine(),
               "macOS_acceptance_eligible": platform.system() == "Darwin",
               "source_head": command(["git", "rev-parse", "HEAD"], cwd=repo).stdout.strip(),
               "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
               "version": version, "checks": {}, "status": "running"}
    (evidence / "results.json").write_text(json.dumps(results, indent=2) + "\n")
    try:
        ui.start()
        ui.open_day()
        pid = ui.review(0, 0)
        ui.add("10:00", "saved QA work")
        ui.review(0, 60, dirty=True)
        ui.action("Confirm staged work (local)")
        receipt = ui.wait(lambda s: "Confirm receipt" in s and "durable=false" in s,
                          "confirm-receipt")
        if "revision 1" not in receipt:
            raise RuntimeError("Wrong confirm revision")
        ui.keys("Enter")
        ui.review(60, 60)
        ui.action("Save local (durable)")
        ui.wait(lambda s: "Save receipt" in s and "saved 1" in s, "save-receipt")
        ui.keys("Enter")
        ui.review(60, 60, dirty=False)
        saved = (fixture / "state.json").read_bytes()
        results["checks"]["confirm_then_save"] = True
        ui.add("10:30", "cancel QA draft")
        ui.review(60, 90, dirty=True)
        ui.cancel()
        results["checks"]["cancel_returns_60"] = True
        ui.add("10:30", "unsaved QA draft")
        if ui.review(60, 90, dirty=True) != pid or ui.pids != {pid}:
            raise RuntimeError("Child changed during ordinary workflow")
        results["checks"]["same_child_across_workflow"] = pid
        ui.quit()
        ui.start()
        ui.open_day()
        restarted_pid = ui.review(60, 60, dirty=False)
        if restarted_pid == pid or (fixture / "state.json").read_bytes() != saved:
            raise RuntimeError("Restart did not preserve exact saved state")
        results["checks"]["restart_saved_60_not_staged_90"] = restarted_pid
        ui.action("Edit interval (staged)")
        ui.wait(lambda s: "Edit staged interval" in s, "edit-picker")
        ui.keys("Enter")
        ui.editor(f"{DAY} 09:00-10:15 P1/DEV edited QA draft")
        ui.review(60, 75, dirty=True)
        ui.cancel()
        results["checks"]["edit_then_cancel"] = True
        ui.keys("Escape")
        ui.wait(lambda s: "Timesheet " + DAY not in s and "→" not in s, "before-reload")
        ui.text("/reload")
        ui.keys("Enter")
        ui.wait(lambda s: "Reloaded" in s, "reloaded")
        ui.open_day("2026-10-23")
        reloaded_pid = ui.review(0, 0, day="2026-10-23")
        if reloaded_pid == restarted_pid:
            raise RuntimeError("Reload did not replace child")
        results["checks"]["reload_new_day"] = reloaded_pid
        ui.keys("Escape")
        ui.wait(lambda s: "Timesheet 2026-10-23" not in s and "→" not in s, "before-final-quit")
        ui.open_day()
        ui.review(60, 60, dirty=False)
        ui.quit()
        probe = subprocess.run(
            [str(binary), "serve", "--config", str(fixture / "config.json"),
             "--env-file", str(fixture / "empty.env")],
            input='{"id":"status","op":"status"}\n{"id":"close","op":"close"}\n',
            text=True, capture_output=True, timeout=10)
        frames = [json.loads(line) for line in probe.stdout.splitlines()]
        if (probe.returncode or [f.get("id") for f in frames] != ["status", "close"]
                or not all(f.get("ok") is True for f in frames)
                or frames[-1].get("result", {}).get("closed") is not True):
            raise RuntimeError("State lock not released after normal quit: " + probe.stderr)
        results["checks"]["normal_quit_and_children_exited"] = True
        results["checks"]["lock_released"] = True
        results["status"] = "passed"
    except Exception as exc:
        results["status"] = "failed"
        results["error"] = str(exc)
        try:
            ui.capture("failure")
        except Exception:
            pass
    finally:
        ui.cleanup()
        results["child_pids"] = sorted(ui.pids)
        results["fixture_retained"] = results["status"] != "passed"
        if results["status"] == "passed":
            shutil.rmtree(fixture)
        (evidence / "results.json").write_text(json.dumps(results, indent=2) + "\n")
        paths = sorted(p for p in evidence.iterdir() if p.is_file() and p.name != "SHA256SUMS")
        (evidence / "SHA256SUMS").write_text("".join(
            f"{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n" for p in paths))
    return results


def main():
    if platform.system() != "Darwin":
        print("BLOCKED: actual Darwin/macOS host required; no Linux rehearsal started.", file=sys.stderr)
        return 2
    repo = Path(__file__).resolve().parent.parent
    root = Path(tempfile.mkdtemp(prefix="r6-macos-ui-"))
    print("Evidence:", root / "evidence", flush=True)
    try:
        result = run(repo, root)
    except Exception as exc:
        print("BLOCKED:", exc, file=sys.stderr)
        return 2
    print(json.dumps({"status": result["status"], "platform": result["platform"],
                      "checks": result["checks"], "error": result.get("error"),
                      "evidence": str(root / "evidence")}, indent=2))
    return 0 if result["status"] == "passed" else 1


if __name__ == "__main__":
    sys.exit(main())
