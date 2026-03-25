package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

type quoteUpdate struct {
	SubmarketID     int
	Probability     float64
	FromProbability *float64
}

type quoteLine struct {
	SubmarketID        int      `json:"submarket_id"`
	FromProbability    *float64 `json:"from_probability,omitempty"`
	ToProbability      float64  `json:"to_probability"`
	Cost               float64  `json:"cost"`
	SharesYes          float64  `json:"shares_yes"`
	PointsInvested     float64  `json:"points_invested"`
	PointsCashedOut    float64  `json:"points_cashed_out"`
	PointsWonLostIfYes float64  `json:"points_won_lost_if_yes"`
	PointsWonLostIfNo  float64  `json:"points_won_lost_if_no"`
	LiquidityBUsed     float64  `json:"b_used"`
}

type quotePayload struct {
	Market            string      `json:"market"`
	Count             int         `json:"count"`
	TotalCost         float64     `json:"total_cost"`
	TotalPointsIn     float64     `json:"total_points_invested"`
	TotalPointsOut    float64     `json:"total_points_cashed_out"`
	IndependentQuotes bool        `json:"independent_quotes"`
	Quotes            []quoteLine `json:"quotes"`
}

var quoteCmd = &cobra.Command{
	Use:   "quote <code>",
	Short: "Estimate cost for probability moves (optional)",
	Long: `Estimate the point cost of moving one or more submarket probabilities.

Most agent workflows can skip quote and either:
1) submit probability updates directly with "trade", or
2) stage a human-reviewable draft with "draft" / "pending-edits".

For quote, pass one update with --submarket-id/--probability, or provide
JSON via --updates (object, array, or {"updates":[...]}), or via stdin.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		code := args[0]
		updates, err := collectQuoteUpdates(cmd)
		if err != nil {
			return err
		}

		lines := make([]quoteLine, 0, len(updates))
		payload := quotePayload{
			Market:            code,
			Count:             len(updates),
			IndependentQuotes: len(updates) > 1,
		}

		for _, update := range updates {
			line, err := requestSingleQuote(code, update)
			if err != nil {
				return fmt.Errorf("quote failed for submarket %d: %w", update.SubmarketID, err)
			}
			lines = append(lines, line)
			payload.TotalCost += line.Cost
			payload.TotalPointsIn += line.PointsInvested
			payload.TotalPointsOut += line.PointsCashedOut
		}
		payload.Quotes = lines

		if jsonOutput || !output.IsTTY() {
			raw, err := json.Marshal(payload)
			if err != nil {
				return fmt.Errorf("encoding quote output: %w", err)
			}
			output.JSON(raw)
			return nil
		}

		headers := []string{"SUBMARKET ID", "TO", "COST", "SHARES YES", "P/L YES", "P/L NO"}
		rows := make([][]string, len(lines))
		for i, line := range lines {
			rows[i] = []string{
				strconv.Itoa(line.SubmarketID),
				fmt.Sprintf("%.1f%%", line.ToProbability*100),
				fmt.Sprintf("%.4f", line.Cost),
				fmt.Sprintf("%.4f", line.SharesYes),
				fmt.Sprintf("%.4f", line.PointsWonLostIfYes),
				fmt.Sprintf("%.4f", line.PointsWonLostIfNo),
			}
		}
		output.Table(headers, rows)
		fmt.Printf("\nTotal estimated cost: %.4f points\n", payload.TotalCost)
		if payload.IndependentQuotes {
			fmt.Println("Note: multiple updates are quoted independently.")
		}

		return nil
	},
}

func collectQuoteUpdates(cmd *cobra.Command) ([]quoteUpdate, error) {
	hasSubmarket := cmd.Flags().Changed("submarket-id")
	hasProbability := cmd.Flags().Changed("probability")
	hasFrom := cmd.Flags().Changed("from-probability")

	if hasSubmarket || hasProbability || hasFrom {
		if !hasSubmarket || !hasProbability {
			return nil, fmt.Errorf("when using direct flags, both --submarket-id and --probability are required")
		}

		submarketID, _ := cmd.Flags().GetInt("submarket-id")
		probability, _ := cmd.Flags().GetFloat64("probability")
		if submarketID <= 0 {
			return nil, fmt.Errorf("--submarket-id must be positive")
		}
		if probability < 0 || probability > 1 {
			return nil, fmt.Errorf("--probability must be between 0 and 1")
		}

		update := quoteUpdate{
			SubmarketID: submarketID,
			Probability: probability,
		}
		if hasFrom {
			from, _ := cmd.Flags().GetFloat64("from-probability")
			if from < 0 || from > 1 {
				return nil, fmt.Errorf("--from-probability must be between 0 and 1")
			}
			update.FromProbability = &from
		}
		return []quoteUpdate{update}, nil
	}

	if updatesJSON, _ := cmd.Flags().GetString("updates"); strings.TrimSpace(updatesJSON) != "" {
		return parseQuoteUpdates([]byte(updatesJSON))
	}

	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		stdinData, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
		return parseQuoteUpdates(stdinData)
	}

	return nil, fmt.Errorf("provide quote input via --submarket-id/--probability, --updates JSON, or stdin JSON")
}

func parseQuoteUpdates(raw []byte) ([]quoteUpdate, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("empty quote input")
	}

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
		if updatesVal, ok := obj["updates"]; ok {
			return parseQuoteUpdatesValue(updatesVal)
		}
		if _, hasID := obj["submarket_id"]; hasID {
			update, err := parseQuoteUpdateMap(obj)
			if err != nil {
				return nil, err
			}
			return []quoteUpdate{update}, nil
		}
	}

	var arr []interface{}
	if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
		return parseQuoteUpdatesValue(arr)
	}

	return nil, fmt.Errorf("invalid quote JSON; expected update object, update array, or {\"updates\":[...]}")
}

func parseQuoteUpdatesValue(value interface{}) ([]quoteUpdate, error) {
	switch typed := value.(type) {
	case []interface{}:
		if len(typed) == 0 {
			return nil, fmt.Errorf("updates array is empty")
		}
		updates := make([]quoteUpdate, 0, len(typed))
		for idx, entry := range typed {
			entryMap, ok := entry.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("updates[%d] must be an object", idx)
			}
			update, err := parseQuoteUpdateMap(entryMap)
			if err != nil {
				return nil, fmt.Errorf("updates[%d]: %w", idx, err)
			}
			updates = append(updates, update)
		}
		return updates, nil
	default:
		return nil, fmt.Errorf("updates must be an array")
	}
}

func parseQuoteUpdateMap(m map[string]interface{}) (quoteUpdate, error) {
	submarketID, err := parseIntField(m, "submarket_id")
	if err != nil {
		return quoteUpdate{}, err
	}
	if submarketID <= 0 {
		return quoteUpdate{}, fmt.Errorf("submarket_id must be positive")
	}

	probability, err := parseFloatField(m, "probability")
	if err != nil {
		return quoteUpdate{}, err
	}
	if probability < 0 || probability > 1 {
		return quoteUpdate{}, fmt.Errorf("probability must be between 0 and 1")
	}

	update := quoteUpdate{
		SubmarketID: submarketID,
		Probability: probability,
	}

	if rawFrom, exists := m["from_probability"]; exists {
		from, err := parseFloatAny(rawFrom, "from_probability")
		if err != nil {
			return quoteUpdate{}, err
		}
		if from < 0 || from > 1 {
			return quoteUpdate{}, fmt.Errorf("from_probability must be between 0 and 1")
		}
		update.FromProbability = &from
	} else if rawFrom, exists := m["from_p"]; exists {
		from, err := parseFloatAny(rawFrom, "from_p")
		if err != nil {
			return quoteUpdate{}, err
		}
		if from < 0 || from > 1 {
			return quoteUpdate{}, fmt.Errorf("from_p must be between 0 and 1")
		}
		update.FromProbability = &from
	}

	return update, nil
}

func parseIntField(m map[string]interface{}, name string) (int, error) {
	value, ok := m[name]
	if !ok {
		return 0, fmt.Errorf("missing %s", name)
	}
	return parseIntAny(value, name)
}

func parseIntAny(value interface{}, name string) (int, error) {
	switch v := value.(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", name)
		}
		return int(i), nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", name)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("%s must be an integer", name)
	}
}

func parseFloatField(m map[string]interface{}, name string) (float64, error) {
	value, ok := m[name]
	if !ok {
		return 0, fmt.Errorf("missing %s", name)
	}
	return parseFloatAny(value, name)
}

func parseFloatAny(value interface{}, name string) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, fmt.Errorf("%s must be a number", name)
		}
		return f, nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("%s must be a number", name)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("%s must be a number", name)
	}
}

func requestSingleQuote(code string, update quoteUpdate) (quoteLine, error) {
	query := url.Values{}
	query.Set("submarket_id", strconv.Itoa(update.SubmarketID))
	query.Set("to_p", fmt.Sprintf("%.12g", update.Probability))
	if update.FromProbability != nil {
		query.Set("from_p", fmt.Sprintf("%.12g", *update.FromProbability))
	}

	resp, err := client.Get("/markets/"+code+"/quote", query)
	if err != nil {
		return quoteLine{}, err
	}

	data, err := resp.Data()
	if err != nil {
		return quoteLine{}, err
	}

	var raw struct {
		Cost               float64 `json:"cost"`
		SharesYes          float64 `json:"shares_yes"`
		PointsInvested     float64 `json:"points_invested"`
		PointsCashedOut    float64 `json:"points_cashed_out"`
		PointsWonLostIfYes float64 `json:"points_won_lost_if_yes"`
		PointsWonLostIfNo  float64 `json:"points_won_lost_if_no"`
		BUsed              float64 `json:"b_used"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return quoteLine{}, fmt.Errorf("parsing quote response: %w", err)
	}

	return quoteLine{
		SubmarketID:        update.SubmarketID,
		FromProbability:    update.FromProbability,
		ToProbability:      update.Probability,
		Cost:               raw.Cost,
		SharesYes:          raw.SharesYes,
		PointsInvested:     raw.PointsInvested,
		PointsCashedOut:    raw.PointsCashedOut,
		PointsWonLostIfYes: raw.PointsWonLostIfYes,
		PointsWonLostIfNo:  raw.PointsWonLostIfNo,
		LiquidityBUsed:     raw.BUsed,
	}, nil
}

func init() {
	quoteCmd.Flags().Int("submarket-id", 0, "Submarket ID for a single quote")
	quoteCmd.Flags().Float64("probability", 0, "Target probability for a single quote (0..1)")
	quoteCmd.Flags().Float64("from-probability", 0, "Optional starting probability override for a single quote (0..1)")
	quoteCmd.Flags().String("updates", "", "Quote updates as JSON object/array (or {\"updates\":[...]})")
	rootCmd.AddCommand(quoteCmd)
}
