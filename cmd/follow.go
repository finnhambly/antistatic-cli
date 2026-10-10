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
	followCmd   = newMarketToggleCmd("follow", "Follow a market", "following", follow)
	unfollowCmd = newMarketToggleCmd("unfollow", "Stop following a market", "following", unfollow)
	watchCmd    = newMarketToggleCmd("watch", "Get notified of a market's comments", "subscribed", watch)
	unwatchCmd  = newMarketToggleCmd("unwatch", "Stop comment notifications for a market", "subscribed", unwatch)
)

func follow(code string) (*api.Response, error) {
	return client.Put(api.Path("/markets/{code}/follow", code), nil)
}

func unfollow(code string) (*api.Response, error) {
	return client.Delete(api.Path("/markets/{code}/follow", code))
}

func watch(code string) (*api.Response, error) {
	return client.Put(api.Path("/markets/{code}/comment-subscription", code), nil)
}

func unwatch(code string) (*api.Response, error) {
	return client.Delete(api.Path("/markets/{code}/comment-subscription", code))
}

func newMarketToggleCmd(name, short, field string, send func(code string) (*api.Response, error)) *cobra.Command {
	return &cobra.Command{
		Use:   name + " <code>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			code := args[0]
			resp, err := send(code)
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
