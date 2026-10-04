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

For separate accounts, set `ANTISTATIC_CONFIG_DIR` to a dedicated directory.
OAuth login and refresh use that directory; the default login is unchanged.
`ANTISTATIC_TOKEN` still overrides the selected saved login.

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
antistatic odds us-troops-iran --group 2026-08 --json

# Your own holdings and payoff scenarios
antistatic positions
antistatic points us-troops-iran

# Plan a cross-group ramp (preview by default)
antistatic draft us-troops-iran --threshold 5000 --probability 0.75 --interpolate-to 0.60 --group-count 6

# Direct trade
antistatic trade us-troops-iran --updates '[{"submarket":"sm_42","probability":"0.75"}]' -y

# Comments
antistatic comments us-troops-iran --limit 20
antistatic comment us-troops-iran "Example comment"
antistatic comment us-troops-iran --reply-to 123 --body "Additional context for comment 123"
antistatic comment-edit us-troops-iran 123 --body "Updated comment"

# Settings, following and comment notifications
antistatic settings
antistatic settings set email.replies=false auto_double_down=true
antistatic follow us-troops-iran          # unfollow to undo
antistatic watch us-troops-iran           # comment notifications; unwatch to undo

# Your own markets: resolve, close, open, undo a resolution, edit
antistatic resolve my-market --yes 2026-05 --no 2026-06 --dry-run
antistatic resolve my-market --outcome 2026-05=yes --known-at 2026-05-14
antistatic close my-market
antistatic open my-market
antistatic reopen my-market --threshold 2026-05
antistatic market edit my-market --title "..." --unit MPs --item lab=Labour:#e4003b --dry-run

# Your bot: a second account you forecast and comment as
antistatic bot create
antistatic --as-bot comment us-troops-iran --body "..."   # or ANTISTATIC_AS_BOT=1
antistatic bot pause

# Retrocasting: forecast the past under a simulated clock
antistatic retro status
antistatic retro advance biology
```

Replies use the parent comment ID returned by `comments`; the parent must belong
to the same market. Both comments and replies accept Markdown via `--body` or
stdin. Omit `--reply-to` to create a top-level comment. `comment-edit` updates
an existing comment without changing its place in the thread.

## Retrocasting

`antistatic retro status` shows where your retrocasting runs stand: the simulated
date you are at, the questions open there, and anything still needing a forecast.
`antistatic retro advance` moves a run on one checkpoint.

The next checkpoint is shown only as available or complete. Even its date can
reveal the timing of a selected paper or event.

There is no command here for submitting a retrocasting forecast, and no API for
it either: an agent may move you through time, but the forecasting is yours to
do. The server also refuses to advance while a question closing at the next
checkpoint has neither a forecast nor an explicit skip.

```sh
antistatic retro status --json          # for agents
antistatic retro advance --run 3 -y     # when you have several runs
```

## Interpolation examples

Count market (threshold planner + cross-group interpolation):

```sh
# Preview only
antistatic draft anthro-arr --threshold 30 --probability 0.84 --interpolate-to 0.60 --from-group 2026/27 --group-count 2

# Persist as pending edits
antistatic draft anthro-arr --threshold 30 --probability 0.84 --interpolate-to 0.60 --from-group 2026/27 --group-count 2 --apply
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
