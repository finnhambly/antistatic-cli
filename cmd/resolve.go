package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

// Market admin for owners (their private markets) and admins: resolve
// outcomes, close, open, and reopen (undo a resolution). All go through
// PUT /markets/:code/status. Outcomes are named by threshold label, resolved
// to submarket ids with the same lookup `trade --updates` uses.

var (
	resolveCmd = newResolveCmd()
	reopenCmd  = newReopenCmd()
	closeCmd   = newStatusCmd("close", "Close a market to trading", "closed", "Closed")
	openCmd    = newStatusCmd("open", "Open a market to trading", "open", "Opened")
)

type submarketOutcome struct {
	ref       string
	resolveTo bool
}

func newResolveCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "resolve <code>",
		Short: "Resolve a market's outcomes",
		Long: `Resolve outcomes by threshold label (or sm_ID).

  antistatic resolve CODE --yes 2026-05 --no 2026-06
  antistatic resolve CODE --outcome 2026-05=yes --known-at 2026-05-14
  antistatic resolve CODE --yes 2026-05 --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			code := args[0]
			outcomes, err := resolveOutcomeFlags(cmd)
			if err != nil {
				return err
			}
			knownAt, _ := cmd.Flags().GetString("known-at")
			if knownAt, err = normaliseKnownAt(knownAt); err != nil {
				return err
			}
			group, _ := cmd.Flags().GetString("group")

			refs := make([]string, len(outcomes))
			for i, outcome := range outcomes {
				refs[i] = outcome.ref
			}
			ids, err := resolveSubmarketRefs(code, refs, group)
			if err != nil {
				return err
			}
			entries := make([]map[string]interface{}, len(outcomes))
			for i, outcome := range outcomes {
				entries[i] = map[string]interface{}{"submarket_id": ids[i], "resolved_yes": outcome.resolveTo}
			}
			payload := map[string]interface{}{"status": "resolved", "submarket_outcomes": entries}
			if knownAt != "" {
				payload["known_at"] = knownAt
			}

			if dry, _ := cmd.Flags().GetBool("dry-run"); dry {
				return printPayload(payload)
			}
			force, _ := cmd.Flags().GetBool("force")
			if !force && output.IsTTY() && !jsonOutput && !confirm(fmt.Sprintf("Resolve %d outcome(s) on %s?", len(entries), code)) {
				return nil
			}
			resp, err := client.Put("/markets/"+code+"/status", payload)
			if err != nil {
				return err
			}
			return printStatusResult(resp, fmt.Sprintf("Resolved %d outcome(s) on %s.", len(entries), code))
		},
	}
	command.Flags().StringArray("yes", nil, "Resolve this threshold YES (repeatable)")
	command.Flags().StringArray("no", nil, "Resolve this threshold NO (repeatable)")
	command.Flags().StringArray("outcome", nil, "LABEL=yes or LABEL=no (repeatable)")
	command.Flags().String("known-at", "", "When the outcome became known (RFC3339 or YYYY-MM-DD)")
	command.Flags().String("group", "", "Group for labels that repeat across groups")
	command.Flags().Bool("dry-run", false, "Print the request without sending it")
	command.Flags().BoolP("force", "f", false, "Skip the confirmation prompt")
	return command
}

func newReopenCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "reopen <code>",
		Short: "Undo a resolution",
		Long: `Undo the resolution of thresholds, by label (or sm_ID).

  antistatic reopen CODE --threshold 2026-05 --threshold 2026-06`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			code := args[0]
			labels, _ := cmd.Flags().GetStringArray("threshold")
			if len(labels) == 0 {
				return fmt.Errorf("name at least one --threshold to reopen")
			}
			group, _ := cmd.Flags().GetString("group")
			ids, err := resolveSubmarketRefs(code, labels, group)
			if err != nil {
				return err
			}
			entries := make([]map[string]interface{}, len(ids))
			for i, id := range ids {
				entries[i] = map[string]interface{}{"submarket_id": id, "reopen": true}
			}
			payload := map[string]interface{}{"status": "resolved", "submarket_outcomes": entries}
			if dry, _ := cmd.Flags().GetBool("dry-run"); dry {
				return printPayload(payload)
			}
			resp, err := client.Put("/markets/"+code+"/status", payload)
			if err != nil {
				var apiErr *api.APIError
				if errors.As(err, &apiErr) && apiErr.Code == "undo_window_passed" {
					return fmt.Errorf("too late to reopen: the undo window has passed (undo_window_passed)")
				}
				return err
			}
			return printStatusResult(resp, fmt.Sprintf("Reopened %d threshold(s) on %s.", len(entries), code))
		},
	}
	command.Flags().StringArray("threshold", nil, "Threshold label or sm_ID to reopen (repeatable)")
	command.Flags().String("group", "", "Group for labels that repeat across groups")
	command.Flags().Bool("dry-run", false, "Print the request without sending it")
	return command
}

func newStatusCmd(name, short, status, done string) *cobra.Command {
	return &cobra.Command{
		Use:   name + " <code>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			resp, err := client.Put("/markets/"+args[0]+"/status", map[string]interface{}{"status": status})
			if err != nil {
				return err
			}
			return printStatusResult(resp, done+" "+args[0]+".")
		},
	}
}

func resolveOutcomeFlags(cmd *cobra.Command) ([]submarketOutcome, error) {
	yes, _ := cmd.Flags().GetStringArray("yes")
	no, _ := cmd.Flags().GetStringArray("no")
	pairs, _ := cmd.Flags().GetStringArray("outcome")

	outcomes := make([]submarketOutcome, 0, len(yes)+len(no)+len(pairs))
	for _, label := range yes {
		outcomes = append(outcomes, submarketOutcome{ref: strings.TrimSpace(label), resolveTo: true})
	}
	for _, label := range no {
		outcomes = append(outcomes, submarketOutcome{ref: strings.TrimSpace(label), resolveTo: false})
	}
	for _, pair := range pairs {
		i := strings.LastIndex(pair, "=")
		if i <= 0 {
			return nil, fmt.Errorf("--outcome expects LABEL=yes or LABEL=no, got %q", pair)
		}
		var value bool
		switch strings.ToLower(strings.TrimSpace(pair[i+1:])) {
		case "yes":
			value = true
		case "no":
			value = false
		default:
			return nil, fmt.Errorf("--outcome expects LABEL=yes or LABEL=no, got %q", pair)
		}
		outcomes = append(outcomes, submarketOutcome{ref: strings.TrimSpace(pair[:i]), resolveTo: value})
	}

	if len(outcomes) == 0 {
		return nil, fmt.Errorf("name at least one outcome with --yes, --no or --outcome")
	}
	seen := make(map[string]bool)
	for _, outcome := range outcomes {
		if outcome.ref == "" {
			return nil, fmt.Errorf("empty threshold label")
		}
		key := normalizeLookupKey(outcome.ref)
		if seen[key] {
			return nil, fmt.Errorf("threshold %q given more than once", outcome.ref)
		}
		seen[key] = true
	}
	return outcomes, nil
}

// resolveSubmarketRefs maps labels (or sm_ID refs) to submarket ids, in order.
func resolveSubmarketRefs(code string, refs []string, group string) ([]int, error) {
	updates := make([]interface{}, len(refs))
	for i, ref := range refs {
		entry := map[string]interface{}{}
		if _, ok := parseSubmarketRef(ref); ok {
			entry["submarket"] = ref
		} else {
			entry["label"] = ref
			if group != "" {
				entry["group"] = group
			}
		}
		updates[i] = entry
	}
	body := map[string]interface{}{"updates": updates}
	if err := resolveUpdateLabelsInBody(code, body); err != nil {
		if strings.Contains(err.Error(), "ambiguous") {
			return nil, fmt.Errorf("%s", strings.Replace(err.Error(), "add group/projection_group in that update", "pass --group", 1))
		}
		return nil, err
	}
	resolved, _ := body["updates"].([]interface{})
	ids := make([]int, len(refs))
	for i := range refs {
		entry, _ := resolved[i].(map[string]interface{})
		id, ok := parseSubmarketRef(entry["submarket"])
		if !ok {
			return nil, fmt.Errorf("could not resolve threshold %q", refs[i])
		}
		ids[i] = id
	}
	return ids, nil
}

func normaliseKnownAt(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	return "", fmt.Errorf("--known-at must be RFC3339 or YYYY-MM-DD, got %q", value)
}

func printPayload(payload map[string]interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	output.JSON(data)
	return nil
}

func printStatusResult(resp *api.Response, message string) error {
	if jsonOutput || !output.IsTTY() {
		output.JSON(resp.Body)
		return nil
	}
	fmt.Println(message)
	var payload struct {
		Resolution *struct {
			VoidedTrades   int `json:"voided_trades"`
			RestoredTrades int `json:"restored_trades"`
		} `json:"resolution"`
	}
	if json.Unmarshal(resp.Body, &payload) == nil && payload.Resolution != nil {
		if payload.Resolution.VoidedTrades > 0 {
			fmt.Printf("Voided trades: %d\n", payload.Resolution.VoidedTrades)
		}
		if payload.Resolution.RestoredTrades > 0 {
			fmt.Printf("Restored trades: %d\n", payload.Resolution.RestoredTrades)
		}
	}
	return nil
}

func init() {
	rootCmd.AddCommand(resolveCmd, reopenCmd, closeCmd, openCmd)
}
