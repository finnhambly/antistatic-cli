# antistatic

Command-line interface for [Antistatic Exchange](https://antistatic.exchange) — browse forecasting markets, view community forecasts, manage positions, and place trades from the terminal.

Works for both humans and AI agents. Outputs human-readable tables when run interactively, and raw JSON when piped or called with `--json`.

## Install

### Homebrew (macOS/Linux)

```sh
brew install finnhambly/tap/antistatic
```

### Go

```sh
go install github.com/finnhambly/antistatic-cli@latest
```

The binary is named `antistatic`.

### Download binary

Download a prebuilt binary from [Releases](https://github.com/finnhambly/antistatic-cli/releases) for your platform (macOS, Linux, Windows × amd64/arm64).

## Authenticate

Run browser OAuth login (recommended):

```sh
antistatic login
```

For headless/CI usage, use an API token:

```sh
export ANTISTATIC_TOKEN=axk_YOUR_TOKEN_HERE
```

Or pass a token directly:

```sh
antistatic login -t axk_YOUR_TOKEN_HERE
```

Check your auth status:

```sh
antistatic status
```

## Usage

### Browse markets

```sh
# List open markets (ordered by activity)
antistatic markets

# Search by keyword
antistatic search iran

# Show details for a specific market
antistatic show us-troops-iran

# Fuzzy code matching (prefix search)
antistatic show us-troop --fuzzy
```

### View forecasts

```sh
# Overview of a market's forecast (group index for large markets)
antistatic forecast us-troops-iran

# Filter by projection group
antistatic forecast us-troops-iran --group 2026-08

# Query a specific threshold
antistatic forecast us-troops-iran --for 5000 --group 2026-08

# Full submarket detail
antistatic forecast us-troops-iran --group 2026-08 --include full

# Force stable full response shape (no summary-index fallback)
antistatic forecast us-troops-iran --group 2026-08 --require-full --json

# Include submarket IDs for direct trading flows
antistatic forecast us-troops-iran --group 2026-08 --include-ids --json

# ASCII chart + monotonicity sanity check in terminal
antistatic forecast us-troops-iran --group 2026-08 --ascii
```

### Positions & P/L

```sh
# List all your positions
antistatic positions

# Positions for a specific market
antistatic positions us-troops-iran

# P&L scenarios (what you gain/lose under each outcome)
antistatic points us-troops-iran
```

### Recommended workflow for AI agents

```sh
# 1) Inspect current forecast and submarket IDs
antistatic forecast us-troops-iran --group 2026-08 --include-ids --json

# 2) Plan draft edits across contiguous groups (preview only by default)
antistatic draft us-troops-iran --threshold 5000 --probability 0.75 --next-groups 6 --interpolate-to 0.60

# 3) Optional: fit a full threshold distribution in one shot
antistatic draft us-troops-iran --distribution lognormal --median 3100 --sigma 0.35 --next-groups 6

# 4) For multicount markets, optionally fill/remove remainder in one group
antistatic draft eng-le --fill-remainder --multicount-group labour

# 5) Optional: estimate cost if needed
antistatic quote us-troops-iran --submarket-id 42 --probability 0.75

# 6) Submit directly from draft planning once approved
antistatic draft us-troops-iran --threshold 5000 --probability 0.75 --next-groups 6 --submit -y
```

### Trade

```sh
# Place a trade
antistatic trade us-troops-iran --updates '[{"submarket_id": 42, "probability": 0.75}]'

# Skip confirmation prompt
antistatic trade us-troops-iran --updates '[...]' -y

# Submit from draft preview JSON (stdin or --updates)
antistatic draft us-troops-iran --threshold 5000 --probability 0.75 --next-groups 6 --json \
  | antistatic trade us-troops-iran --from-draft -y

# Disable auto interpolation/monotonic shaping
antistatic trade us-troops-iran --updates '[...]' --no-auto-shape -y

# Multicount markets: fill/remove remainder while trading
antistatic trade eng-le --updates '[...]' --fill-remainder --multicount-group labour -y
```

### Pending edits

Pending edits (alias: `draft`) are probability changes saved server-side that persist across sessions, but aren't submitted as trades yet.

```sh
# View your pending edits
antistatic pending-edits us-troops-iran

# Update pending edits directly
antistatic draft us-troops-iran --updates '[{"submarket_id": 42, "probability": 0.6}]'

# Plan contiguous weekly edits (dry run)
antistatic draft us-troops-iran --threshold 70 --probability 0.20 --next-groups 6

# Interpolate linearly across a range and apply
antistatic draft us-troops-iran --threshold 70 --probability 0.35 --interpolate-to 0.20 --from-group 2026-W13 --to-group 2026-W18 --apply

# Parametric full distribution fit
antistatic draft us-troops-iran --distribution lognormal --median 3100 --sigma 0.35 --next-groups 6

# Plan and submit in one command
antistatic draft us-troops-iran --threshold 70 --probability 0.20 --next-groups 6 --submit -y

# Disable auto interpolation/monotonic shaping
antistatic draft us-troops-iran --updates '[...]' --no-auto-shape

# Multicount markets: fill/remove remainder for one entity group
antistatic draft eng-le --fill-remainder --multicount-group labour
antistatic draft eng-le --remove-remainder --multicount-group other

# Clear all pending edits
antistatic pending-edits us-troops-iran --clear
```

### Quote (optional)

Use quote when you need a cost estimate. Many agent workflows can skip this.

```sh
# Single update via flags
antistatic quote us-troops-iran --submarket-id 42 --probability 0.75

# Multiple updates via JSON
antistatic quote us-troops-iran --updates '[{"submarket_id": 42, "probability": 0.75}]'
```

### Comment

```sh
# Read comments with pagination and truncation controls
antistatic comments us-troops-iran --sort newest --limit 10 --max-comments 50 --max-body-chars 400

# Continue from next cursor
antistatic comments us-troops-iran --cursor-inserted-at "2026-03-25T10:00:00Z" --cursor-id 123

# Post a comment
antistatic comment us-troops-iran "I think the raid scenario is underpriced given recent deployments"
```

## Output format

By default, `antistatic` detects whether stdout is a terminal:

- **Terminal (TTY):** human-readable tables and key-value output
- **Piped/redirected:** raw JSON (same as `--json`)

Force JSON output with `--json`:

```sh
antistatic forecast nuke-det --json | jq '.forecast'
```

## Configuration

Config is stored in:

- macOS: `~/Library/Application Support/antistatic/config.json`
- Linux: `~/.config/antistatic/config.json`
- Windows: `%APPDATA%\antistatic\config.json`

Environment variables take precedence over the config file:

| Variable | Description |
|---|---|
| `ANTISTATIC_TOKEN` | API token (overrides saved OAuth/config token) |
| `ANTISTATIC_URL` | Base URL (default: `https://antistatic.exchange`) |
| `ANTISTATIC_NO_UPDATE_CHECK` | Set to `1` to disable daily update checks |

## For AI agents

The CLI is designed to work as a tool for AI coding agents and assistants. To give an agent access:

1. Generate a token at https://antistatic.exchange/users/settings#api-tokens
2. Set `ANTISTATIC_TOKEN` in the agent environment
3. Prefer `antistatic draft` for review-first workflows, then `antistatic trade` when approved.

When the agent pipes output or uses `--json`, it gets structured JSON it can parse directly.

## Release (maintainers)

Tag and push `vX.Y.Z` to publish binaries and update Homebrew.

The release workflow prefers a short-lived GitHub App token for pushing to `finnhambly/homebrew-tap`.

Configure in `finnhambly/antistatic-cli`:

- Repository variable: `HOMEBREW_TAP_APP_ID`
- Repository secret: `HOMEBREW_TAP_APP_PRIVATE_KEY`

Fallback (optional): if App credentials are not set, the workflow uses `HOMEBREW_TAP_GITHUB_TOKEN` if present.

## Development

```sh
go build -o antistatic .
go test ./...
go vet ./...
```

## License

MIT
