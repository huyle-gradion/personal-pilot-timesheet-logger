#!/usr/bin/env python3
"""
Google Calendar Event Fetcher via Private iCal (.ics) Feed.
Step 2 in Timesheet Pipeline:
Collects events from Google Calendar and formats data matching the basic
structure of a Simple Entry (Section 3.2).
"""

import argparse
import json
import os
import re
import sys
import urllib.request
from datetime import date, datetime, time, timedelta, timezone
from pathlib import Path
from zoneinfo import ZoneInfo

import icalendar
import recurring_ical_events


def load_env(env_path: Path):
    """Simple parser for .env file if it exists."""
    if not env_path.exists():
        return
    with open(env_path, "r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            k = k.strip()
            v = v.strip().strip("\"'")
            if k not in os.environ:
                os.environ[k] = v


def fetch_ics_data(url_or_path: str, cache_file: Path | None = None, max_age_seconds: int = 300) -> bytes:
    """Fetch ics data from a URL or local file, optionally caching HTTP responses."""
    if url_or_path.startswith("http://") or url_or_path.startswith("https://"):
        if cache_file and cache_file.exists():
            mtime = datetime.fromtimestamp(cache_file.stat().st_mtime, tz=timezone.utc)
            if (datetime.now(timezone.utc) - mtime).total_seconds() < max_age_seconds:
                return cache_file.read_bytes()

        req = urllib.request.Request(
            url_or_path,
            headers={"User-Agent": "Personal-Pilot-Timesheet/1.0"}
        )
        with urllib.request.urlopen(req) as resp:
            content = resp.read()

        if cache_file:
            cache_file.parent.mkdir(parents=True, exist_ok=True)
            cache_file.write_bytes(content)
        return content
    else:
        path = Path(url_or_path).expanduser().resolve()
        if not path.exists():
            raise FileNotFoundError(f"File not found: {path}")
        return path.read_bytes()


def parse_events_as_simple_entries(ics_bytes: bytes, target_date: date, local_tz: ZoneInfo) -> list[dict]:
    """
    Parse ics data and format each event to match the Simple Entry structure
    specified in Section 3.2.
    """
    cal = icalendar.Calendar.from_ical(ics_bytes)

    # Start and end boundaries in local timezone
    start_of_day = datetime.combine(target_date, time.min, tzinfo=local_tz)
    end_of_day = datetime.combine(target_date + timedelta(days=1), time.min, tzinfo=local_tz)

    events_between = recurring_ical_events.of(cal).between(start_of_day, end_of_day)
    simple_entries = []

    for event in events_between:
        summary = str(event.get("SUMMARY", "(No Title)")).strip()
        dtstart_prop = event.get("DTSTART")
        dtend_prop = event.get("DTEND")

        if not dtstart_prop:
            continue

        raw_start = dtstart_prop.dt
        all_day = not isinstance(raw_start, datetime)

        if all_day:
            start_time_str = "00:00"
            end_time_str = "23:59"
            duration_mins = 24 * 60
        else:
            start_dt = raw_start.astimezone(local_tz)
            if dtend_prop:
                end_dt = dtend_prop.dt.astimezone(local_tz)
            else:
                end_dt = start_dt + timedelta(hours=1)

            start_time_str = start_dt.strftime("%H:%M")
            end_time_str = end_dt.strftime("%H:%M")
            duration_mins = int((end_dt - start_dt).total_seconds() / 60)

        raw_description = str(event.get("DESCRIPTION", "")).strip()
        location = str(event.get("LOCATION", "")).strip()

        # Check for any PR mentions in summary or description (e.g., #1182)
        pr_matches = sorted(list(set(re.findall(r"#(\d+)", f"{summary} {raw_description}"))), key=int)
        prs = [f"#{num}" for num in pr_matches]

        # Description follows 3.2 format: grouped topics + inline PRs list
        desc_body = summary
        if raw_description:
            # Clean up escape sequences common in ics descriptions
            cleaned_desc = raw_description.replace("\\n", " ").replace("\n", " ").strip()
            desc_body = f"{summary}: {cleaned_desc}"

        if prs:
            formatted_description = f"{desc_body}. PRs: {', '.join(prs)}"
        else:
            formatted_description = desc_body

        # Simple Entry Schema matching Section 3.2
        entry = {
            "date": target_date.isoformat(),
            "start_time": start_time_str,
            "end_time": end_time_str,
            "topic": summary,
            "description": formatted_description,
            "prs": prs,
            "sources": {
                "calendar_event": summary,
                "repo": None,
                "commits": []
            },
            "metadata": {
                "duration_minutes": duration_mins,
                "all_day": all_day,
                "location": location if location else None
            }
        }
        simple_entries.append(entry)

    # Sort chronologically by start time
    simple_entries.sort(key=lambda x: x["start_time"])
    return simple_entries


def format_as_entry_markdown(entries: list[dict]) -> str:
    """Format entries according to the Section 3.2 'Edit entry' panel fields."""
    if not entries:
        return "(No calendar entries found for this date)"

    blocks = []
    for idx, e in enumerate(entries, 1):
        pr_str = ", ".join(e["prs"]) if e["prs"] else "None"
        block = (
            f"### Entry {idx}: {e['start_time']} - {e['end_time']}\n"
            f"- **Date**: {e['date']}\n"
            f"- **Start time**: {e['start_time']}\n"
            f"- **End time**: {e['end_time']}\n"
            f"- **Description**: {e['description']}\n"
            f"- **PRs**: {pr_str}\n"
            f"- **Sources**:\n"
            f"  - Calendar: \"{e['sources']['calendar_event']}\""
        )
        blocks.append(block)
    return "\n\n".join(blocks)


def main():
    script_dir = Path(__file__).resolve().parent
    workspace_root = script_dir.parent if script_dir.name == "scripts" else script_dir
    load_env(workspace_root / ".env")

    parser = argparse.ArgumentParser(description="Fetch Google Calendar events and format as Simple Entries")
    parser.add_argument("--url", default=os.getenv("GCAL_ICS_URL"), help="Secret iCal URL (or set GCAL_ICS_URL env var)")
    parser.add_argument("--file", help="Local .ics file for testing")
    parser.add_argument("--date", default=date.today().isoformat(), help="Target date YYYY-MM-DD (default: today)")
    parser.add_argument("--tz", default="Asia/Ho_Chi_Minh", help="Local timezone name (default: Asia/Ho_Chi_Minh)")
    parser.add_argument(
        "--format",
        choices=["json", "entry"],
        default="json",
        help="Output format: 'json' (default Simple Entry array for Step 3 AI) or 'entry' (Section 3.2 markdown format)"
    )
    parser.add_argument("--minified", action="store_true", help="Output single-line JSON without indentation to minimize tokens")
    parser.add_argument("--no-cache", action="store_true", help="Bypass cached .ics file")

    args = parser.parse_args()

    source = args.file or args.url
    if not source:
        print(
            "Error: No iCal URL provided.\n"
            "Please provide --url or create a .env file with GCAL_ICS_URL=...\n"
            "To get your secret iCal URL:\n"
            "  1. Go to Google Calendar -> Settings\n"
            "  2. Click your calendar on the left\n"
            "  3. Scroll to 'Integrate calendar'\n"
            "  4. Copy 'Secret address in iCal format'",
            file=sys.stderr
        )
        sys.exit(1)

    target_date = datetime.strptime(args.date, "%Y-%m-%d").date()
    local_tz = ZoneInfo(args.tz)
    cache_path = (workspace_root / ".cache" / "calendar_cache.ics") if not args.no_cache else None

    try:
        ics_data = fetch_ics_data(source, cache_file=cache_path)
        entries = parse_events_as_simple_entries(ics_data, target_date, local_tz)
    except Exception as e:
        print(f"Error fetching/parsing calendar events: {e}", file=sys.stderr)
        sys.exit(1)

    if args.format == "json":
        if args.minified:
            print(json.dumps(entries, separators=(",", ":"), ensure_ascii=False))
        else:
            print(json.dumps(entries, indent=2, ensure_ascii=False))
    elif args.format == "entry":
        print(format_as_entry_markdown(entries))


if __name__ == "__main__":
    main()
