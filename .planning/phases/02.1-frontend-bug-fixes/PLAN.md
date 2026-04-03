---
wave: 1
depends_on: []
files_modified:
  - web/static/js/pages/videos.js
  - .planning/ROADMAP.md
  - .planning/STATE.md
autonomous: true
requirements:
  - REQ-UI-1
  - REQ-UI-2
---

# Phase 2.1 — Frontend Bug Fixes: Plan

## Goal

Mark Phase 2.1 as complete. All five fixes (P1-8, P1-9, P1-10, REQ-UI-2 dashboard empty-state, REQ-UI-2 uploaders confirm dialog) are already in the codebase. The only remaining work is:
1. Add `// [FIXED: P1-8]` annotation in `videos.js` to explicitly mark the P1-8 fix
2. Update `ROADMAP.md` to show Phase 2.1 as complete
3. Update `STATE.md` to advance the current phase to 2.2

All three tasks are independent and run in Wave 1.

---

## Wave 1 — All Tasks (parallel)

### Task 1: Add `// [FIXED: P1-8]` comment in videos.js

<read_first>
- `web/static/js/pages/videos.js` lines 87–115 — the SSE download event handler containing the existing `// [FIXED: P1-5]` comment at line 106 and `setTimeout(load, 1000)` at line 107
</read_first>

<action>
In `web/static/js/pages/videos.js`, locate the block starting at line 106:

```js
        // [FIXED: P1-5] 完成/失败后延迟刷新，同步 file_path、detail_status 等后端字段
        setTimeout(load, 1000);
```

Append a second comment on the very next line (after line 106, before line 107's `setTimeout`), so the result looks like:

```js
        // [FIXED: P1-5] 完成/失败后延迟刷新，同步 file_path、detail_status 等后端字段
        // [FIXED: P1-8] setTimeout(load, 1000) 同时覆盖 P1-8：completed 事件后完整刷新列表，同步 file_path、thumb_path 等字段
        setTimeout(load, 1000);
```

No other changes to videos.js.
</action>

<acceptance_criteria>
- `web/static/js/pages/videos.js` contains the exact string `// [FIXED: P1-8]`
- `web/static/js/pages/videos.js` still contains `// [FIXED: P1-5]` (existing comment not removed)
- `web/static/js/pages/videos.js` still contains `setTimeout(load, 1000)` immediately after the two FIXED comments
- The file contains no new function definitions or logic changes — only the one comment line added
</acceptance_criteria>

---

### Task 2: Update ROADMAP.md — mark Phase 2.1 complete

<read_first>
- `.planning/ROADMAP.md` — the Phase 2.1 heading at line 113: `### Phase 2.1 — Frontend Bug Fixes`
</read_first>

<action>
In `.planning/ROADMAP.md`, change the Phase 2.1 heading from:

```
### Phase 2.1 — Frontend Bug Fixes
```

to:

```
### Phase 2.1 — Frontend Bug Fixes ✅ Complete (2026-04-03)
```

No other changes to ROADMAP.md.
</action>

<acceptance_criteria>
- `.planning/ROADMAP.md` contains the exact string `### Phase 2.1 — Frontend Bug Fixes ✅ Complete (2026-04-03)`
- `.planning/ROADMAP.md` does NOT contain a line with `### Phase 2.1 — Frontend Bug Fixes` that lacks the ✅ suffix (i.e., the old bare heading is gone)
- All other phase headings in ROADMAP.md are unchanged (Phase 1.1, 1.2, 1.3, 2.2, 2.3, 2.4 headings unmodified)
</acceptance_criteria>

---

### Task 3: Update STATE.md — advance to Phase 2.2

<read_first>
- `.planning/STATE.md` — current content showing Phase 2.1 as `⬜ Pending` in the phases table and `**Phase 2.1 — Frontend Bug Fixes** (next to execute)` as the current phase
</read_first>

<action>
In `.planning/STATE.md`, make exactly two changes:

**Change 1** — Update the Current Phase section.  
Find:
```
**Phase 2.1 — Frontend Bug Fixes** (next to execute)
```
Replace with:
```
**Phase 2.2 — Observability** (next to execute)
```

**Change 2** — Update the phases table row for 2.1.  
Find:
```
| 2.1 | Frontend Bug Fixes | ⬜ Pending |
```
Replace with:
```
| 2.1 | Frontend Bug Fixes | ✅ Complete (2026-04-03) |
```

No other changes to STATE.md.
</action>

<acceptance_criteria>
- `.planning/STATE.md` contains the exact string `**Phase 2.2 — Observability** (next to execute)`
- `.planning/STATE.md` does NOT contain `**Phase 2.1 — Frontend Bug Fixes** (next to execute)`
- `.planning/STATE.md` contains the exact string `| 2.1 | Frontend Bug Fixes | ✅ Complete (2026-04-03) |`
- `.planning/STATE.md` does NOT contain `| 2.1 | Frontend Bug Fixes | ⬜ Pending |`
- `.planning/STATE.md` still contains `| 2.2 | Observability | ⬜ Pending |` (Phase 2.2 row unchanged)
</acceptance_criteria>

---

## Verification

After all three tasks complete, run these checks:

```bash
grep -n "\[FIXED: P1-8\]" web/static/js/pages/videos.js
grep -n "Phase 2.1.*✅ Complete (2026-04-03)" .planning/ROADMAP.md
grep -n "Phase 2.2.*Observability.*next to execute" .planning/STATE.md
grep -n "2\.1.*Frontend Bug Fixes.*✅ Complete" .planning/STATE.md
```

All four commands must return at least one matching line.

---

## must_haves

Goal-backward verification — Phase 2.1 is only complete when ALL of the following are true:

- [ ] `videos.js` has `// [FIXED: P1-8]` comment adjacent to the `setTimeout(load, 1000)` line in the `completed` event handler
- [ ] `ROADMAP.md` Phase 2.1 heading includes `✅ Complete (2026-04-03)`
- [ ] `STATE.md` current phase is Phase 2.2
- [ ] `STATE.md` Phase 2.1 table row shows `✅ Complete (2026-04-03)`
- [ ] No existing code logic was modified (only comments and planning docs changed)
