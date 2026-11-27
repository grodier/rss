---
name: Planned work
about: An issue for one PR, written for an implementer with no context from planning (see "Writing issues" in CLAUDE.md)
title: "<Area>: <what the reader, admin or app gets>"
---

<!--
Delete these comments as you fill the template in.
The rules are under "Writing issues" in CLAUDE.md; the reasons for this layout are under "Planning and docs" in docs/decisions.md.
One sentence per line, no hard wrapping.
-->

Part of Roadmap 2 (#770), wave <N>. **Depends on** #<N> (<what it needs from it>). **Unblocks** #<N> (<what it gives it>). Decisions are under "<Area>" in `docs/decisions.md`.

<!--
The line above: the roadmap (or the Admin Roadmap, #560), then what this waits for and what waits for it, each with why.
Say which dependencies have already merged ("#827 has merged"), and if only part of the work waits, say which part.
Add "Has owner steps." when the owner has to do something.
-->

## Why

**This PR <does what, in one sentence a reviewer could repeat>.**

<!--
The value, in a short paragraph: what's wrong or missing today, and what readers, the owner or later issues get once this merges.
End with what's true after it merges ("After this PR, …").
If something must be checked before building (a go or no-go), say so here, in bold.
-->

**What ships**
- <A deliverable a reviewer can find in the diff.>
- <Another.>
- <Anything left out on purpose, or temporary until another issue, said plainly.>

## Decisions (don't revisit them here)

<!--
Decisions already made, each with a one-line reason; group them under ### subheadings when there are many.
Look-and-feel choices not yet approved go under "Current thinking" instead.
Give new types and function signatures, SQL and user-facing strings verbatim, and point at code by path and symbol.
-->

- **<Decision.>** <Reason.>

## Changes

1.

## Current thinking (look and feel, not yet approved)

<!-- Only for an issue that changes how the app looks; delete otherwise. -->

## Design checkpoint

<!--
Either "None: <why>" (copy only, or an existing pattern reused exactly), or several options that differ in approach, the preferred one,
and the pages and states the screenshots must show (both article layouts for anything in an article row, Light and Dark for anything about color, desktop and phone widths).
-->

## Tests

<!-- Name each test and list its cases, including the failure cases; for a bug fix, how to confirm the test fails before the fix. -->

-

## Out of scope

- <What a reader of this issue might expect here, and the issue that does it.>

## Verification

<!-- How to see it working, then: -->
Then run the checks in CLAUDE.md's "Before pushing".

## Notes for parallel work

<!-- Only when another open issue touches the same files: which merges first. Delete otherwise. -->

## Suggested branch

`feat/<slug>`
