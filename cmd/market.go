package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var marketCmd = &cobra.Command{
	Use:   "market",
	Short: "Create markets from specs and request public listings",
	Long: `Create a market by describing it as a spec: whether it asks when or how
much, its horizon or periods, the title, how it resolves, the base rates
behind its starting probabilities and a few anchor probabilities. The server
builds the bars, fits the starting curve and checks the house rules.

  antistatic market recipe              the rules, settings and examples
  antistatic market preview spec.json   check a (partial) spec; shows issues
                                        and the settings that apply next
  antistatic market create spec.json    create it, private to you
  antistatic market request-public CODE --reason "..."
                                        ask for a public listing (--withdraw
                                        to take the request back)
  antistatic market listing CODE        visibility and request status

Specs are JSON; pass a file path or - for stdin.`,
}

var marketRecipeCmd = &cobra.Command{
	Use:   "recipe",
	Short: "Show the market recipe: settings, rules and examples",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := client.Get("/market-recipe", nil)
		if err != nil {
			return err
		}
		data, err := resp.Data()
		if err != nil {
			return err
		}
		output.JSON(data)
		return nil
	},
}

var marketPreviewCmd = &cobra.Command{
	Use:   "preview <spec.json|->",
	Short: "Preview a market spec: derived market, issues and next options",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		spec, err := readSpec(args[0])
		if err != nil {
			return err
		}
		resp, err := client.Post("/market-specs/preview", spec)
		if err != nil {
			return err
		}
		data, err := resp.Data()
		if err != nil {
			return err
		}
		if jsonOutput || !output.IsTTY() {
			output.JSON(data)
			return nil
		}
		return printSpecPreview(data)
	},
}

var marketCreateCmd = &cobra.Command{
	Use:   "create <spec.json|->",
	Short: "Create a market from a spec, private to you",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		spec, err := readSpec(args[0])
		if err != nil {
			return err
		}
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes && output.IsTTY() && !confirm("Create this market (private to you)?") {
			fmt.Println("Cancelled.")
			return nil
		}
		resp, err := client.Post("/market-specs", spec)
		if err != nil {
			return specErrorDetails(err)
		}
		data, err := resp.Data()
		if err != nil {
			return err
		}
		if jsonOutput || !output.IsTTY() {
			output.JSON(data)
			return nil
		}
		var created struct {
			Code string `json:"code"`
			URL  string `json:"url"`
			Note string `json:"note"`
		}
		_ = json.Unmarshal(data, &created)
		fmt.Printf("Created %s: %s\n%s\n", created.Code, created.URL, created.Note)
		return nil
	},
}

var marketRequestPublicCmd = &cobra.Command{
	Use:   "request-public <code>",
	Short: "Ask for a public listing of your private market (or --withdraw it)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		path := "/markets/" + args[0] + "/publication-request"
		withdraw, _ := cmd.Flags().GetBool("withdraw")

		var resp *api.Response
		var err error
		if withdraw {
			yes, _ := cmd.Flags().GetBool("yes")
			if !yes && output.IsTTY() && !confirm("Withdraw the pending request?") {
				fmt.Println("Cancelled.")
				return nil
			}
			resp, err = client.Delete(path)
		} else {
			reason, _ := cmd.Flags().GetString("reason")
			if strings.TrimSpace(reason) == "" {
				return fmt.Errorf("--reason is required: say why others should be able to forecast on it")
			}
			resp, err = client.Post(path, map[string]interface{}{"message": reason})
		}
		if err != nil {
			return err
		}
		data, err := resp.Data()
		if err != nil {
			return err
		}
		output.JSON(data)
		return nil
	},
}

var marketListingCmd = &cobra.Command{
	Use:   "listing <code>",
	Short: "Show a market's visibility and public-listing request",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		resp, err := client.Get("/markets/"+args[0]+"/publication-request", nil)
		if err != nil {
			return err
		}
		data, err := resp.Data()
		if err != nil {
			return err
		}
		output.JSON(data)
		return nil
	},
}

func readSpec(source string) (map[string]interface{}, error) {
	var raw []byte
	var err error
	if source == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(source)
	}
	if err != nil {
		return nil, fmt.Errorf("reading spec: %w", err)
	}
	var spec map[string]interface{}
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("spec is not a JSON object: %w", err)
	}
	if inner, ok := spec["spec"].(map[string]interface{}); ok {
		spec = inner
	}
	return spec, nil
}

type specIssue struct {
	Field    string `json:"field"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Fix      string `json:"fix"`
}

func printSpecPreview(data json.RawMessage) error {
	var preview struct {
		Valid       bool        `json:"valid"`
		Issues      []specIssue `json:"issues"`
		NextOptions []struct {
			Setting  string      `json:"setting"`
			Required bool        `json:"required"`
			Set      bool        `json:"set"`
			Values   interface{} `json:"values"`
			Why      string      `json:"why"`
		} `json:"next_options"`
		Curves []struct {
			Curve string `json:"curve"`
			Bars  []struct {
				Label       string   `json:"label"`
				Threshold   *float64 `json:"threshold"`
				Probability float64  `json:"starting_probability"`
			} `json:"bars"`
		} `json:"curves"`
	}
	if err := json.Unmarshal(data, &preview); err != nil {
		output.JSON(data)
		return nil
	}

	if preview.Valid {
		fmt.Println("Valid: ready to create.")
	} else {
		fmt.Println("Not valid yet.")
	}
	printSpecIssues(preview.Issues)

	pending := [][]string{}
	for _, opt := range preview.NextOptions {
		if opt.Set {
			continue
		}
		need := "optional"
		if opt.Required {
			need = "required"
		}
		pending = append(pending, []string{opt.Setting, need, opt.Why})
	}
	if len(pending) > 0 {
		fmt.Println("\nStill to set:")
		output.Table([]string{"SETTING", "", "WHY"}, pending)
	}

	for _, curve := range preview.Curves {
		if len(curve.Bars) == 0 {
			continue
		}
		first, last := curve.Bars[0], curve.Bars[len(curve.Bars)-1]
		fmt.Printf("\nCurve %s: %d bars, %s %.1f%% … %s %.1f%%\n",
			curve.Curve, len(curve.Bars),
			barName(first.Label, first.Threshold), first.Probability*100,
			barName(last.Label, last.Threshold), last.Probability*100)
	}
	return nil
}

func barName(label string, threshold *float64) string {
	if threshold != nil {
		return fmt.Sprintf("≥%g", *threshold)
	}
	return label
}

func printSpecIssues(issues []specIssue) {
	if len(issues) == 0 {
		return
	}
	rows := make([][]string, 0, len(issues))
	for _, issue := range issues {
		text := issue.Message
		if issue.Fix != "" {
			text += " " + issue.Fix
		}
		rows = append(rows, []string{issue.Severity, issue.Field, text})
	}
	fmt.Println()
	output.Table([]string{"", "FIELD", "ISSUE"}, rows)
}

// specErrorDetails points at preview for the full list of issues.
func specErrorDetails(err error) error {
	if apiErr, ok := err.(*api.APIError); ok && apiErr.Code == "invalid_spec" {
		return fmt.Errorf("%w\nRun antistatic market preview on the same spec to see each issue and its fix", err)
	}
	return err
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	var answer string
	fmt.Scanln(&answer)
	return strings.ToLower(strings.TrimSpace(answer)) == "y"
}

func init() {
	marketCreateCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	marketRequestPublicCmd.Flags().String("reason", "", "Why others should be able to forecast on it (sent to the admins)")
	marketRequestPublicCmd.Flags().Bool("withdraw", false, "Withdraw your pending request")
	marketRequestPublicCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt when withdrawing")
	marketCmd.AddCommand(marketRecipeCmd, marketPreviewCmd, marketCreateCmd, marketRequestPublicCmd, marketListingCmd)
	rootCmd.AddCommand(marketCmd)
}
