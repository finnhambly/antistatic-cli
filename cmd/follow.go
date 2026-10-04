package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

// Following a market and watching its comments are separate switches on the
// exchange: /follow and /comment-subscription, each PUT to turn on and
// DELETE to turn off.

var (
	followCmd   = newMarketToggleCmd("follow", "Follow a market", "/follow", "following", true)
	unfollowCmd = newMarketToggleCmd("unfollow", "Stop following a market", "/follow", "following", false)
	watchCmd    = newMarketToggleCmd("watch", "Get notified of a market's comments", "/comment-subscription", "subscribed", true)
	unwatchCmd  = newMarketToggleCmd("unwatch", "Stop comment notifications for a market", "/comment-subscription", "subscribed", false)
)

func newMarketToggleCmd(name, short, path, field string, on bool) *cobra.Command {
	return &cobra.Command{
		Use:   name + " <code>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			code := args[0]
			url := "/markets/" + code + path
			var resp *api.Response
			var err error
			if on {
				resp, err = client.Put(url, nil)
			} else {
				resp, err = client.Delete(url)
			}
			if err != nil {
				return err
			}
			raw := unwrapData(resp)
			if jsonOutput || !output.IsTTY() {
				output.JSON(raw)
				return nil
			}
			var state map[string]interface{}
			if err := json.Unmarshal(raw, &state); err != nil {
				return fmt.Errorf("parsing response: %w", err)
			}
			value, _ := state[field].(bool)
			fmt.Println(toggleMessage(field, value, code))
			return nil
		},
	}
}

func toggleMessage(field string, value bool, code string) string {
	switch {
	case field == "following" && value:
		return "Following " + code + "."
	case field == "following":
		return "Not following " + code + "."
	case value:
		return "Watching comments on " + code + "."
	default:
		return "Not watching comments on " + code + "."
	}
}

func init() {
	rootCmd.AddCommand(followCmd, unfollowCmd, watchCmd, unwatchCmd)
}
