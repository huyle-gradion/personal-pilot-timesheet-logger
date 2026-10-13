---
name: "Tracer-Bullet Ticket (Matt Pocock Format)"
description: "A vertical-slice engineering ticket with explicit blocking edges and verification seams."
title: "[M#] "
labels: ["tracer-bullet"]
assignees: []
---

## 🎯 Vertical Slice
<!-- Describe the narrow end-to-end slice cutting across layers that this ticket delivers. -->

## 🔗 Blocking Edges
- **Blocked by**: None <!-- e.g., #1, #2 -->
- **Blocks**: None <!-- e.g., #4, #5 -->

## 📖 Context & Specification
<!-- Why does this work exist? Reference ADRs, spec sections, or notes. -->

## 📐 Pre-Agreed Seams & Interface Contracts
<!-- Code signatures, CLI flags, JSON structs, or HTTP request/response shapes. -->

## ✅ Acceptance Criteria (DoD)
- [ ] Task 1
- [ ] Task 2
- [ ] Conforms to TigerStyle safety rules (no unbounded loops, bounded memory)

## 🧪 Verification Commands
<!-- Exact, deterministic commands an engineer or AI agent runs to verify completion. -->
```bash
go test -v ./...
go test -race ./...
```
