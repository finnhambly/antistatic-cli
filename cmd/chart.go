package cmd

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

// The chart card's resource, as the MCP server serves it to chat apps.
const chartCardURI = "ui://antistatic/chart.html"

// maxChartMarkets is antistatic_show_markets' limit.
const maxChartMarkets = 6

//go:embed chart_page.html
var chartPage string

func newChartCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "chart <code> [code...]",
		Short: "Open a market's chart in the browser",
		Long: `Draw a market's chart, the card Claude and ChatGPT show, as a page on
this computer and open it in the browser. With several codes (up to 6), one
card shows them all.

The page is written once, from your login, and calls nothing afterwards: the
data stays in the file. It goes to your cache directory unless --out says
where; its path is printed. For coding agents without chart cards (a
terminal), this is the way to show a market.`,
		Args: cobra.RangeArgs(1, maxChartMarkets),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID, _ := cmd.Flags().GetInt("run")
			crowd, _ := cmd.Flags().GetBool("crowd")
			out, _ := cmd.Flags().GetString("out")
			noOpen, _ := cmd.Flags().GetBool("no-open")

			tool, input := "antistatic_show_market", map[string]interface{}{"market_code": args[0]}
			if len(args) > 1 {
				tool, input = "antistatic_show_markets", map[string]interface{}{"market_codes": args}
			} else if crowd {
				input["show_crowd"] = true
			}
			if runID > 0 {
				input["run_id"] = runID
			}

			result, err := client.RPC("tools/call", map[string]interface{}{"name": tool, "arguments": input})
			if err != nil {
				return err
			}
			var shown struct {
				IsError bool `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(result, &shown); err != nil {
				return err
			}
			if shown.IsError {
				var parts []string
				for _, c := range shown.Content {
					parts = append(parts, c.Text)
				}
				return fmt.Errorf("%s", strings.Join(parts, "\n"))
			}

			card, err := chartCard()
			if err != nil {
				return err
			}
			page, err := buildChartPage(card, tool, input, result, chartTitle(shown.Content, args))
			if err != nil {
				return err
			}

			path := out
			if path == "" {
				if path, err = chartPath(args, time.Now()); err != nil {
					return err
				}
			}
			if err := os.WriteFile(path, []byte(page), 0o600); err != nil {
				return err
			}

			opened := false
			if !noOpen {
				opened = openBrowser("file://"+filepath.ToSlash(path)) == nil
			}
			if jsonOutput || !output.IsTTY() {
				encoded, _ := json.Marshal(map[string]interface{}{"path": path, "opened": opened})
				output.JSON(encoded)
				return nil
			}
			if opened {
				fmt.Printf("Opened %s\n", path)
			} else {
				fmt.Printf("Wrote %s\n", path)
			}
			return nil
		},
	}
	command.Flags().Int("run", 0, "Read as of a sandbox run's clock (its run id)")
	command.Flags().Bool("crowd", false, "Also chart the crowd's forecast (one market)")
	command.Flags().String("out", "", "Write the page here instead of the cache directory")
	command.Flags().Bool("no-open", false, "Write the page without opening it")
	return command
}

func chartCard() (string, error) {
	raw, err := client.RPC("resources/read", map[string]string{"uri": chartCardURI})
	if err != nil {
		return "", err
	}
	var res struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	if len(res.Contents) == 0 || res.Contents[0].Text == "" {
		return "", fmt.Errorf("the site sent no chart card")
	}
	return res.Contents[0].Text, nil
}

// buildChartPage fills the host page. JSON is safe inside its script: Go
// escapes <, > and & in it.
func buildChartPage(card, tool string, input interface{}, result json.RawMessage, title string) (string, error) {
	values := map[string]interface{}{"{{CARD}}": card, "{{TOOL}}": tool, "{{INPUT}}": input, "{{RESULT}}": result}
	page := chartPage
	for key, v := range values {
		encoded, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		page = strings.ReplaceAll(page, key, string(encoded))
	}
	page = strings.ReplaceAll(page, "{{TITLE}}", html.EscapeString(title))
	return page, nil
}

var chartBold = regexp.MustCompile(`^\*\*(.+?)\*\*`)

// chartTitle is the market's title, from the start of the tool's text.
func chartTitle(content []struct {
	Text string `json:"text"`
}, codes []string) string {
	if len(codes) == 1 && len(content) > 0 {
		if m := chartBold.FindStringSubmatch(content[0].Text); m != nil {
			return m[1] + " · Antistatic"
		}
	}
	return strings.Join(codes, ", ") + " · Antistatic"
}

var chartUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func chartPath(codes []string, when time.Time) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "antistatic", "charts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := chartUnsafe.ReplaceAllString(strings.Join(codes, "+"), "-")
	return filepath.Join(dir, fmt.Sprintf("%s-%s.html", strings.Trim(name, "-"), when.Format("20060102-150405"))), nil
}

func init() {
	rootCmd.AddCommand(newChartCmd())
}
