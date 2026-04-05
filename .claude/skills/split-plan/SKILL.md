---
name: split-plan
description: Split a large PR plan into smaller, human-reviewable sub-plans. Use when a plan is too large for a single PR, when user wants to break down a plan, or mentions "split plan".
argument-hint: [plan-file-path]
---

Split a PR plan into smaller, human-reviewable sub-plans. Each sub-plan should be a PR that a human can review in one sitting.

## Process

### 1. Read the plan

Read the plan file from `$ARGUMENTS`. If no argument was provided, ask the user for the path.

### 2. Explore the codebase

Read the actual source files referenced in the plan — the files that will be created, modified, or deleted. Understand:

- Current code complexity and size of each file
- Dependencies between the proposed changes
- Existing test coverage
- Which changes are migrations of existing code vs. pure additions

Do NOT skip this step. The plan text alone does not tell you how intertwined the changes are. Good splits require understanding real code.

### 3. Identify natural split boundaries

Group changes into sub-PRs by applying these heuristics:

- **Single theme**: Each sub-PR has one clear purpose (e.g., "core types", "integration", "new additions")
- **Always shippable**: Each sub-PR leaves tests green and the codebase in a working state
- **Human-reviewable**: ~3-5 files changed per sub-PR, reviewable in one sitting
- **Foundation first**: Infrastructure/types come before the code that uses them
- **Separate migrations from additions**: Changing existing code is a different review concern than adding new code
- **Tests travel with code**: Test additions are bundled with the production code they test

### 4. Propose the breakdown to the user

Present the proposed split as a numbered list. For each sub-PR show:

- **Letter + Title** (e.g., "a: Core structured error type")
- **Theme**: one sentence describing what this sub-PR does and why it's grouped this way
- **Scope covered**: which items from the original plan's Scope section this addresses
- **Builds on**: which prior sub-PR(s) must be merged first (if any)

Ask the user:
- Does the granularity feel right? Too coarse? Too fine?
- Should any sub-PRs be merged or split further?

**Do NOT write any files until the user approves the breakdown.** Iterate until approved.

### 5. Write the sub-plan files

For each approved sub-PR, create a plan file that matches the structure of the original plan. Use the same section headers (Context, Scope, Files Changed, Verification, etc.).

**Naming convention**: Append a letter suffix to the original filename.
- Original: `auth-pr-02-error-format.md`
- Sub-plans: `auth-pr-02a-error-format.md`, `auth-pr-02b-error-format.md`, `auth-pr-02c-error-format.md`

Each sub-plan's Context section should note what it builds on (e.g., "Builds on PR 02a").

### 6. Convert the original plan to an index

Replace the original plan's content with:
- The original title, appended with "(Index)"
- A table linking to each sub-plan with a short description
- A merge order note
- A reference back to the parent plan (if one is mentioned in the original)
