# git-stat

An interactive TUI for exploring how a repository's line count evolves over time — per language or per contributor, with tables and a day cursor.

## Usage

Requires Go (see `go.mod`) and `git` on PATH.

```sh
go build -o git-stat ./cmd/app   # or: go run ./cmd/app [directory]
git-stat [directory]             # defaults to the current directory
```

The app opens a repo picker listing every git repository found under `directory` (sorted alphabetically by path; nested repos are not listed). Select one with ↑/↓ and Enter — the chart TUI launches for that repo. Press `q` or Ctrl-C to quit at any point.

## Configuration: `.gitstatconfig`

Optional JSON file at the repository root. A missing or invalid file means defaults (all languages, `.gitignore` rules only).

```json
{
  "languages": ["go", "ts"],
  "exclude_dirs": ["vendor", "dist"]
}
```

- `languages` — file extensions to count (with or without a leading dot; case-insensitive). Empty or absent → all languages are included.
- `exclude_dirs` — directory names excluded in addition to the repo's `.gitignore` rules, matched as path segments at any depth (e.g. `"vendor"` excludes everything under any `vendor/` directory).

The repository's own `.gitignore` is always respected via `git check-ignore --no-index`, so files that were committed but match ignore patterns are excluded from history stats too.

## Key bindings

Repo picker:

| key | action |
| --- | --- |
| ↑ / ↓ | move selection (windowed scrolling for long lists) |
| Enter | select repo and launch the chart |
| q / Ctrl-C | quit |

Chart views — `tab` cycles language chart → contributor chart → language table → contributor table; `shift+tab` goes back:

| key | action |
| --- | --- |
| ← / → (or h / l) | move the day cursor one active day |
| w / W | seek forward / back one week |
| m / M | seek forward / back one month |
| y / Y | seek forward / back one year |
| 1–5 | range: 7d / 30d / 90d / 365d / all time |
| tab / v | next view |
| shift+tab | previous view |
| q / Ctrl-C | quit |

## Architecture

```
cmd/app/main.go            repo picker + entry point (arg validation, friendly errors)
cmd/app/chart.go           chart TUI: stacked bar rendering, tables, breakdowns, legend, footer
internal/parser/config.go     .gitstatconfig loading
internal/parser/filter.go     language allowlist + exclude-dir filtering
internal/parser/gitignore.go  git check-ignore wrapper (cached)
internal/parser/loctimeline.go LocOverTime: single `git log --numstat` pass → per-day cumulative line counts by extension
internal/parser/userloc.go     UserLocOverTimeProgress: per-author ownership via parallel blame with a rolling cache
internal/parser/walker.go      repo discovery under a root directory
internal/parser/util.go         shared path helpers (extension extraction)
```

Data flow: `main` picks the repo → loads config + filter → the chart model runs two background computations in parallel: fast single-pass file stats (fills immediately) and per-author ownership (streams day snapshots as they complete, newest first). The UI renders from whatever has arrived so far; tables and breakdowns show the selected day with Δ vs the previous active day.

## Development

```sh
go build ./...   # must stay green
go test ./...    # unit + integration tests (integration skips when git is unavailable)
```
