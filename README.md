# antistatic

Command-line interface for [Antistatic Exchange](https://antistatic.exchange).

Browse markets, view odds, manage positions, and place trades from the terminal.

## Quick setup

### 1) Install

```sh
brew install finnhambly/tap/antistatic
```

Alternative install methods:

- `go install github.com/finnhambly/antistatic-cli@latest`
- Download a prebuilt binary from [Releases](https://github.com/finnhambly/antistatic-cli/releases)

### 2) Authenticate

Browser OAuth (recommended):

```sh
antistatic login
```

Headless/CI token auth:

```sh
export ANTISTATIC_TOKEN=axk_YOUR_TOKEN_HERE
```

Check auth status:

```sh
antistatic status
```

### 3) Discover commands

```sh
antistatic --help
antistatic <command> --help
```

## Common usage

```sh
# Browse markets
antistatic markets
antistatic search iran
antistatic show us-troops-iran

# Market probabilities (community odds curve)
antistatic odds us-troops-iran
antistatic odds us-troops-iran --group 2026-08
antistatic odds us-troops-iran --group 2026-08 --include-ids --json

# Your own holdings and payoff scenarios
antistatic positions
antistatic points us-troops-iran

# Plan a cross-group ramp (preview by default)
antistatic draft us-troops-iran --threshold 5000 --probability 0.75 --interpolate-to 0.60 --next-groups 6

# Direct trade
antistatic trade us-troops-iran --updates '[{"submarket":"sm_42","probability":"0.75"}]' -y

# Comments
antistatic comments us-troops-iran --limit 20
antistatic comment us-troops-iran "Example comment"
```

## Interpolation examples

Count market (threshold planner + cross-group interpolation):

```sh
# Preview only
antistatic draft anthro-arr --threshold 30 --probability 0.84 --interpolate-to 0.60 --from-group 2026-08-31T23:59:59Z --to-group 2027-02-28T23:59:59Z

# Persist as pending edits
antistatic draft anthro-arr --threshold 30 --probability 0.84 --interpolate-to 0.60 --from-group 2026-08-31T23:59:59Z --to-group 2027-02-28T23:59:59Z --apply
```

Date market (sparse anchors + auto-shape interpolation):

```sh
# Set two anchor points; auto-shape interpolates between them
antistatic draft taiwan-inv --updates '[{"label":"By Dec 2028","probability":"0.35"},{"label":"By Dec 2030","probability":"0.55"}]'
```

## What these mean

- `odds`: market probability data (the current priced odds across outcomes/submarkets).
- `positions`: your personal exposure in those markets (what you've bought/sold, with net shares/cost).

## Output behavior

- Terminal (TTY): human-readable output
- Piped/redirected: JSON output by default
- Force JSON anytime with `--json`

Example:

```sh
antistatic odds nuke-det --json | jq '.forecast'
```

## For AI agents

Recommended default workflow:

1. Read market context (`odds`, `comments`).
2. Plan with `draft` and get approval.
3. Submit via `draft --submit` or `trade`.
4. Only use `comment` when explicitly instructed.
