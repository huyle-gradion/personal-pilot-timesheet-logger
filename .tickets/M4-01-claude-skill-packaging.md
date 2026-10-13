# [M4-01] Claude Code Skill Packaging with Compressed AI Editorial Prompt

## 🎯 Vertical Slice
Package the entire pipeline as a distributable Claude Code Skill in `.claude/skills/timesheet/`. Defines `SKILL.md` and `workflow.md`, strictly delineating deterministic script steps from minimal AI editorial refinement (< 600 tokens/day).

## 🔗 Blocking Edges
- **Blocked by**: `[M2-02]`, `[M3-01]`, `[M3-02]`
- **Blocks**: `[M4-02]` (E2E Verification)

## 📖 Context & Specification
- **Spec Reference**: Section 2 (Deliverables) & Section 3.5 (Design Question: Script vs. AI).
- **ADR Reference**: ADR 0001 (Section 5: Script vs. AI Boundary & Token Budget).
- **Workflow Steps in `workflow.md`**:
  1. `[Script]` `cmd/fetch-calendar`
  2. `[Script]` `cmd/fetch-git`
  3. `[Script]` `internal/timesheet/clusterer` (Assembles skeleton JSON)
  4. `[AI]` Editorial synthesis (Topic label + 1–2 concise sentences describing impact)
  5. `[Script]` `cmd/review-timesheet` (Visual collision review & prompt)
  6. `[Script]` `cmd/sync-timesheet` (Gradion API reconciliation)
  7. `[Script]` `cmd/benchmark -record` (Token & memory tracking)

## 📐 Pre-Agreed Seams & Interface Contracts
- **Skill Structure**:
  ```text
  .claude/skills/timesheet/
  ├── SKILL.md       # Entrypoint, trigger description, parameter instructions
  └── workflow.md    # Step-by-step orchestrator linking Go binaries and Claude instructions
  ```
- **Compressed AI Prompt Contract**:
  - Input: Raw commit list + meeting titles in JSON (< 300 tokens).
  - Output: JSON mapping block IDs to `{ "title": "Topic", "description": "1-2 sentences" }` (< 150 tokens).

## ✅ Acceptance Criteria (DoD)
- [ ] `SKILL.md` provides clear trigger phrases (`/timesheet`, `log timesheet`, `daily timesheet`).
- [ ] `workflow.md` explicitly annotates every step with `[Script]` or `[AI]`.
- [ ] LLM prompt contains strict token bounds and forbidding prose generation.
- [ ] Validated with `claude skill check` or manual invocation simulation.

## 🧪 Verification Commands
```bash
# Verify skill files exist and contain valid YAML frontmatter
cat .claude/skills/timesheet/SKILL.md
```
