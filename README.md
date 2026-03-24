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

Generate an API token at https://antistatic.exchange/users/settings#api-tokens, then either:

**Option A** — paste the one-liner shown on the settings page:

```sh
antistatic auth login -t axk_YOUR_TOKEN_HERE
```

**Option B** — set an environment variable (useful for CI, scripts, and AI agents):

```sh
export ANTISTATIC_TOKEN=axk_YOUR_TOKEN_HERE
```

**Option C** — run `antistatic auth login` and paste interactively.

Check your auth status:

```sh
antistatic auth status
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

### Trade

```sh
# Get a cost quote
antistatic quote us-troops-iran --updates '[{"submarket_id": 42, "probability": 0.75}]'

# Place a trade
antistatic trade us-troops-iran --updates '[{"submarket_id": 42, "probability": 0.75}]'

# Skip confirmation prompt
antistatic trade us-troops-iran --updates '[...]' -y
```

### Pending edits

Pending edits are draft probability changes saved server-side that persist across sessions, but aren't submitted as trades yet.

```sh
# View your pending edits
antistatic pending-edits us-troops-iran

# Update pending edits
antistatic pending-edits us-troops-iran --updates '[{"submarket_id": 42, "probability": 0.6}]'

# Clear all pending edits
antistatic pending-edits us-troops-iran --clear
```

### Comment

```sh
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

Config is stored in `~/.config/antistatic/config.json` (macOS/Linux) or `%APPDATA%\antistatic\config.json` (Windows).

Environment variables take precedence over the config file:

| Variable | Description |
|---|---|
| `ANTISTATIC_TOKEN` | API token (overrides saved token) |
| `ANTISTATIC_URL` | Base URL (default: `https://antistatic.exchange`) |

## For AI agents

The CLI is designed to work as a tool for AI coding agents and assistants. To give an agent access:

1. Generate a token at https://antistatic.exchange/users/settings#api-tokens
2. Set `ANTISTATIC_TOKEN` in the agent's environment
3. The agent can then run commands like `antistatic search`, `antistatic forecast`, `antistatic trade`, etc.

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
