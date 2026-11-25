# git-stat — improvement plan

Working document for the 6-batch improvement effort. **One batch per fresh context**: start a new session, read this file, do one batch, commit/leave working tree clean, then clear context and repeat. Do not mix batches in one session.

## Current state (as of plan creation)

- Go TUI app (bubbletea v2 + lipgloss v2): pick a git repo → interactive chart of LOC over time per language/contributor, with tables and day cursor.
- Layout: `cmd/app/main.go` (repo picker + entry), `cmd/app/chart.go` (chart TUI), `internal/parser/*.go` (git history parsing, ignore filtering, ownership computation).
- **Pre-existing uncommitted changes in the working tree — preserve them**: `main.go` now takes a root arg instead of hardcoded `~/Documents`; `walker.go` dropped dead code (`HasGitRepo`, home-dir expansion). Do not revert or "fix" these.
- No tests currently. No README.

## Batch 1 — Dead code & parser cleanup (foundation)

Delete: `internal/parser/pool.go` (ProcessFiles), `internal/parser/counter.go` (CountLines, FileLineCounter), `GetFileType` in walker.go, the non-progress `UserLocOverTime` wrapper in userloc.go (only `UserLocOverTimeProgress` is used).

Switch display dates from "DD-MM-YYYY" to ISO "YYYY-MM-DD" everywhere; drop `flipDate` (util.go) and its call sites. Sorting/parsing becomes trivial.

In `loctimeline.go`: flush the day snapshot only when the commit's date actually changes (currently flushes on every commit — wasteful but correct). Keep rename handling as-is.

## Batch 2 — Owner computation performance (the big one)

Current problem: `UserLocOverTimeProgress` runs `git ls-tree` + one `git blame --porcelain <commit> -- <file>` **per file per day** → O(days × files) git processes. Also `blameFile` uses `bufio.Scanner` with a 64KB line limit (silently breaks on long/minified lines — real bug).

Redesign:
- One `git log --first-parent --reverse --numstat --date=short --format=C%H|%ad` pass → days, last commit per day, and **dirty sets** (paths touched by commits on each day; rename old+new paths both count as dirty).
- Phase 1: compute the LAST day first (full blame of included files at that commit, parallel across files with a bounded worker pool) so the all-time table fills instantly — keep this behavior from today.
- Phase 2: remaining days ASCENDING with a rolling cache: for each file in `lsTree(commit_D)` filtered by `filter.Included`, re-blame only if it was touched on some day since the last snapshot (track per-path sorted touch days; binary search or incremental dirty set — O(1)/O(log k) per file). Emit each snapshot via `onDay` as its day completes.
- Fix `blameFile`: replace Scanner with a size-unbounded reader loop (e.g. `bufio.Reader.ReadBytes('\n')`); keep streaming output.
- Keep the public contract: `UserLocOverTimeProgress(repo, filter, onDay func(done, total int, idx int, stats UserDayStats)) ([]UserDayStats, error)` — snapshots arrive in completion order, returned slice is chronological.

## Batch 3 — Chart rendering rewrite (visualization)

Replace the polyline chart with a **stacked bar chart**: each column = one sampled bucket of visible days; height = total lines; segments colored by series (language or contributor), bottom-up stacking. Much more readable for composition over time than N crossing lines.

Key design points:
- Position columns by **absolute day index** within the visible range — sparse owner data shows honest gaps instead of compressing missing days into a continuous line (current bug in `ownerArrivedSnaps`).
- Cursor guide: marker row under the chart + bold date label for the selected day; breakdown header already shows exact numbers.
- Segment-based row building (one lipgloss Render per color run, not per cell — current code renders every cell individually).
- Precompute parsed dates once at load time (currently `time.Parse` runs on every frame via `visibleStart`/`parseDay`).
- Delete the old grid machinery: `cell`, `drawLine`, `niceTicks`, `formatTick`, `renderChart` polyline logic — replaced by the stacked renderer. Keep `sortedByTotal`, colorsFor, palette (maybe extend to ~11 entries).
- Date labels below chart at evenly spaced columns; highlight selected day's label.

## Batch 4 — UI polish & wonkiness fixes

- Tables show the **selected day** with Δ vs previous active day (instead of all-time cumulative, which made ←/→ keys do nothing in table view). Keep share % and total row.
- Legend: cap at ~6 items + "+N more"; title truncated to terminal width.
- Fix name-truncation overflow bug in tables/breakdowns (`%-*s` with a 28-char cap but full names printed → misaligned columns) — truncate the *name* itself, e.g. `…` ellipsis.
- Thousands separators for line counts everywhere (helper `formatInt`).
- Breakdown: cap at top ~10 entries + "+N more"; show Δ vs previous day per entry.
- Footer hints reflect actual keys per view; title shows repo basename + range label + view name.
- Empty/error states styled (not plain text): files error, no commits, all files filtered out.

## Batch 5 — Repo picker polish

- Sort repos alphabetically by path (currently filesystem order — jumps around between runs).
- Windowed scrolling for long lists (repos beyond screen height are currently unreachable!) + `WindowSizeMsg` handling in the picker model.
- lipgloss styling: title bar, highlighted selected row, dimmed footer with hints; empty state message.
- main.go: friendly error handling instead of `panic(err)` (print to stderr, exit non-zero); validate root arg exists before launching the TUI.
- `GetGitDirPaths`: use the `.git` callback directly (skip descending into it via SkipDir) instead of ReadDir on every directory — faster for large trees. Keep behavior otherwise identical.

## Batch 6 — Tests & docs

Unit tests (same package, no terminal needed):
- Pure chart helpers: `formatInt`, bucket/column math, stacked segment computation, name truncation, visibleStart logic with precomputed times.
- Parser: `parseRename` cases; integration test for `LocOverTime` on a temp repo (skip if git unavailable) — verify per-day totals across commits and rename handling.
- Keep tests modest but real; no snapshot testing of the TUI itself.

README.md: usage, config file format (`.gitstatconfig` with `languages` + `exclude_dirs`), key bindings, architecture overview.

## Constraints / notes for future contexts

- Preserve pre-existing uncommitted changes in main.go and walker.go (see above) — do not revert them.
- Keep the public parser API stable where possible; chart model is internal to `cmd/app`.
- Don't add dependencies beyond what's already in go.mod unless clearly justified.
- Each batch must leave the app building and working (`go build ./...`) before finishing.
