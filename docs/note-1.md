# Note No.1

## Benchmarking

1. Token usage
2. Time complexity (Big O + Run time)
3. Space complexity (Data consumed)

## Step

> Script steps: fixed, repeatable, no judgement involved. Real scripts from a programming language or executable program

> AI steps: anything that needs judgment. Keep this one as minimized as possible

1. (Script) Collect git commits + pull requests (opened, reviewed, merged) and format data that matched basic structure of a simple entry
2. (Script) Collect events from google calendar and format data that matched basic structure of a simple entry
3. (AI) Format collected data into meaning and compacted entries
4. (Script) Create a new daily log which grouped by topic and time block
5. (Script) Ask for user review. Must display the output of the daily log and the collision of the blocks
6. (Script) (Update and) submit the log to timesheet

## In

- git commits
- pull requests
- google calendar events

## Out

- Multiple entries per day
- Entry content by topic
- Combine small tasks into one short topic
- PR entry need inline reference
- Entry include: date, start time, end time, grouped topic summary, PR list, and the source (repo, commit messages, calendar event title)
- Reference the "Edit entry panel of the real timesheet": Date, Start time, End time, and a short Description (w Grouped topic), PRs
- Log files for benchmarking

## Definition of Done

- [ ] Skill folder with `SKILL.md` + `workflow.md`
- [ ] `workflow.md` marks every step **[Script]** or **[AI]**
- [ ] Dedicated tool pulls commits, pull requests, and calendar events
- [ ] Entries match the shape in [[Out]]
- [ ] Git discipline
- [ ] Code lives in git, with a commit history
- [ ] You can be able to explain the project and how it works
- [ ] `workflow.md` states a target token budget for one day
- [ ] Capture daily token token usage

## Script for benchmarking

### Step

0. Collect all information of space and time complexity and usage of the tools/modules for the pilot (include collecting git commits + pull requests and collecting events)
1. Collect all session transcripts
2. Pull the token usage for each run
3. Add these up into a single `token used per day`, add if their are multiple runs
4. Create a chart to represent the improvement of the project by time
