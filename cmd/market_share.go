package cmd

import (
	"fmt"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

var marketShareCmd = &cobra.Command{
	Use:   "share <code>",
	Short: "Show or change who a private market is shared with (Pro)",
	Long: `Show a private market's share link, pending access requests and members.

Pro lets you share a private market with up to 25 people: create a link,
send it to them, and approve each request.

  antistatic market share CODE                  link, requests and members
  antistatic market share CODE --create-link    a new link (replaces the old one)
  antistatic market share CODE --revoke-link    turn the link off
  antistatic market share CODE --approve ID     approve a request
  antistatic market share CODE --decline ID     decline a request
  antistatic market share CODE --remove USER_ID remove a member`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		code := args[0]
		create, _ := cmd.Flags().GetBool("create-link")
		revoke, _ := cmd.Flags().GetBool("revoke-link")
		approve, _ := cmd.Flags().GetInt("approve")
		decline, _ := cmd.Flags().GetInt("decline")
		remove, _ := cmd.Flags().GetInt("remove")
		yes, _ := cmd.Flags().GetBool("yes")

		destructive := func(prompt string) bool {
			return yes || !output.IsTTY() || confirm(prompt)
		}

		switch {
		case create:
			return printData(client.Post(api.Path("/markets/{code}/share-link", code), map[string]interface{}{}))
		case revoke:
			if !destructive("Turn off the share link?") {
				return nil
			}
			return printData(client.Delete(api.Path("/markets/{code}/share-link", code)))
		case approve > 0:
			return printData(client.Post(api.Path("/markets/{code}/access-requests/{id}/approve", code, approve), map[string]interface{}{}))
		case decline > 0:
			if !destructive("Decline this request?") {
				return nil
			}
			return printData(client.Post(api.Path("/markets/{code}/access-requests/{id}/decline", code, decline), map[string]interface{}{}))
		case remove > 0:
			if !destructive("Remove this member's access?") {
				return nil
			}
			return printData(client.Delete(api.Path("/markets/{code}/members/{user_id}", code, remove)))
		}

		for _, part := range []struct {
			label string
			get   func() (*api.Response, error)
		}{
			{"share-link", func() (*api.Response, error) { return client.Get(api.Path("/markets/{code}/share-link", code), nil) }},
			{"access-requests", func() (*api.Response, error) {
				return client.Get(api.Path("/markets/{code}/access-requests", code), nil)
			}},
			{"members", func() (*api.Response, error) { return client.Get(api.Path("/markets/{code}/members", code), nil) }},
		} {
			resp, err := part.get()
			if err != nil {
				return err
			}
			data, err := resp.Data()
			if err != nil {
				return err
			}
			fmt.Printf("%s:\n", part.label)
			output.JSON(data)
		}
		return nil
	},
}

func printData(resp *api.Response, err error) error {
	if err != nil {
		return err
	}
	data, err := resp.Data()
	if err != nil {
		return err
	}
	output.JSON(data)
	return nil
}

func init() {
	marketShareCmd.Flags().Bool("create-link", false, "Create a share link (replaces any previous one)")
	marketShareCmd.Flags().Bool("revoke-link", false, "Turn the share link off")
	marketShareCmd.Flags().Int("approve", 0, "Approve the access request with this id")
	marketShareCmd.Flags().Int("decline", 0, "Decline the access request with this id")
	marketShareCmd.Flags().Int("remove", 0, "Remove the member with this user id")
	marketShareCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompts")
	marketCmd.AddCommand(marketShareCmd)
}
