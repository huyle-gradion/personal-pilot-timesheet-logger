#!/usr/bin/env python3
"""Record the day's model token usage for the timesheet skill.

Assignment 1 §3.6 evidence: scan local agent-session transcripts, select the
sessions that ran the timesheet skill (identified by a
`timesheet collect|draft|record|status` binary invocation in the transcript),
sum the usage records per calendar day, and append one row per
(date, session) to benchmarks/tokens.csv.

Transcript sources are runner-agnostic:

- `--claude-dir` (default `~/.claude`): Claude Code session JSONL under
  `projects/`, usage read from `message.usage`.
- `--pi-dir` (optional, no default): a directory of Pi agent session JSONL
  transcripts — typically the dedicated profile directory, for example
  `~/.timesheet/pi-profile`. Usage records are read from `message.usage` or
  a top-level `usage` object.

Idempotent: (date, session_id) pairs already in the CSV are skipped.

Standard library only; no network access; transcripts are read, never
modified or copied elsewhere.
"""

import argparse
import csv
import json
import os
import pathlib
import re
import sys

SKILL_MARKER = re.compile(r"timesheet (collect|draft|record|status)\b")
FIELDS = ["date", "session_id", "input_tokens", "output_tokens", "cache_tokens", "total_tokens"]


def iter_transcripts(root: pathlib.Path, subdirs):
    """Yield *.jsonl files under root, optionally only under named subdirs."""
    if not root.is_dir():
        return
    if subdirs:
        for sub in subdirs:
            yield from sorted((root / sub).glob("**/*.jsonl"))
        return
    yield from sorted(root.glob("**/*.jsonl"))


def usage_of(record):
    """Return the usage dict of one transcript record, runner-agnostic."""
    message = record.get("message")
    if isinstance(message, dict) and isinstance(message.get("usage"), dict):
        return message["usage"]
    if isinstance(record.get("usage"), dict):
        return record["usage"]
    return None


def scan_session(path: pathlib.Path):
    """Return (is_skill_session, {date: usage-dict}) for one transcript."""
    is_skill = False
    per_day = {}
    session_date = None
    with path.open("r", encoding="utf-8", errors="replace") as fh:
        for line in fh:
            if not is_skill and SKILL_MARKER.search(line):
                is_skill = True
            try:
                record = json.loads(line)
            except json.JSONDecodeError:
                continue
            ts = record.get("timestamp")
            if session_date is None and isinstance(ts, str) and len(ts) >= 10:
                session_date = ts[:10]
            usage = usage_of(record)
            if usage is None:
                continue
            day = session_date
            if isinstance(ts, str) and len(ts) >= 10:
                day = ts[:10]
            agg = per_day.setdefault(day, {"input": 0, "output": 0, "cache": 0})
            # Claude Code usage names (input_tokens, ...) and Pi usage names
            # (input, output, cacheRead, cacheWrite) both map into one row.
            agg["input"] += int(usage.get("input_tokens") or usage.get("input") or 0)
            agg["output"] += int(usage.get("output_tokens") or usage.get("output") or 0)
            agg["cache"] += int(
                (usage.get("cache_creation_input_tokens") or 0)
                + (usage.get("cache_read_input_tokens") or 0)
                + (usage.get("cacheRead") or 0)
                + (usage.get("cacheWrite") or 0)
            )
    return is_skill, per_day


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--claude-dir",
        default=os.path.expanduser("~/.claude"),
        help="Claude Code home (default: ~/.claude)",
    )
    parser.add_argument(
        "--pi-dir",
        default=None,
        help="Pi agent session/transcript directory (no default; e.g. ~/.timesheet/pi-profile)",
    )
    parser.add_argument(
        "--out",
        default="benchmarks/tokens.csv",
        help="CSV to append to (default: benchmarks/tokens.csv)",
    )
    args = parser.parse_args()

    out_path = pathlib.Path(args.out)
    existing = set()
    if out_path.exists():
        with out_path.open("r", newline="", encoding="utf-8") as fh:
            for row in csv.DictReader(fh):
                existing.add((row.get("date"), row.get("session_id")))

    sources = [("claude", pathlib.Path(args.claude_dir), ["projects"])]
    if args.pi_dir:
        sources.append(("pi", pathlib.Path(args.pi_dir), None))

    rows = []
    for runtime, root, subdirs in sources:
        for transcript in iter_transcripts(root, subdirs):
            session_id = f"{runtime}:{transcript.stem}"
            try:
                is_skill, per_day = scan_session(transcript)
            except OSError:
                continue
            if not is_skill:
                continue
            for day, usage in sorted(per_day.items()):
                if (day, session_id) in existing:
                    continue
                total = usage["input"] + usage["output"] + usage["cache"]
                rows.append(
                    {
                        "date": day,
                        "session_id": session_id,
                        "input_tokens": usage["input"],
                        "output_tokens": usage["output"],
                        "cache_tokens": usage["cache"],
                        "total_tokens": total,
                    }
                )
                existing.add((day, session_id))

    if not rows:
        print("tokens.py: no new skill sessions to record")
        if out_path.exists():
            with out_path.open("r", encoding="utf-8") as fh:
                tail = fh.read().splitlines()[-3:]
            for line in tail:
                print("  " + line)
        return 0

    out_path.parent.mkdir(parents=True, exist_ok=True)
    is_new = not out_path.exists()
    with out_path.open("a", newline="", encoding="utf-8") as fh:
        writer = csv.DictWriter(fh, fieldnames=FIELDS)
        if is_new:
            writer.writeheader()
        writer.writerows(rows)
    for row in rows:
        print(
            f"tokens.py: {row['date']} session {row['session_id'][:20]}… "
            f"in={row['input_tokens']} out={row['output_tokens']} "
            f"cache={row['cache_tokens']} total={row['total_tokens']}"
        )
    return 0


if __name__ == "__main__":
    sys.exit(main())
