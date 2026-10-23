package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/config"
	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

// Your bot account: a second user you act as with --as-bot when forecasting
// and commenting. Created with POST /me/bot; renamed and paused through the
// "bot" key of /me/settings. These commands always act as you, so they never
// send the bot header.
//
// "bot register" gives an agent a key that acts only as your bot (POST
// /me/bot/key, made with your login; the bot is created if you have none).
// Bots can't sign up without a person's account.

type botInfo struct {
	Username   string `json:"username"`
	Paused     bool   `json:"paused"`
	ProfileURL string `json:"profile_url,omitempty"`
}

func newBotCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "bot",
		Short: "Show your bot account",
		Long: `Show your bot account. Use --as-bot on trade, comment and other
forecasting commands to act as it.

  antistatic bot
  antistatic bot create [--username NAME]
  antistatic bot register [--username NAME] [--save]
  antistatic bot rename NAME
  antistatic bot pause | resume`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return botRequest(func() (*api.Response, error) { return client.Get("/me/bot", nil) }, false)
		},
	}

	create := &cobra.Command{
		Use:   "create",
		Short: "Create your bot",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]interface{}{}
			if name, _ := cmd.Flags().GetString("username"); name != "" {
				body["username"] = name
			}
			return botRequest(func() (*api.Response, error) { return client.Post("/me/bot", body) }, false)
		},
	}
	create.Flags().String("username", "", "Bot username (default <you>-bot)")

	rename := &cobra.Command{
		Use:   "rename NAME",
		Short: "Rename your bot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return patchBot(map[string]interface{}{"username": args[0]})
		},
	}
	pause := &cobra.Command{
		Use:   "pause",
		Short: "Pause your bot",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return patchBot(map[string]interface{}{"paused": true})
		},
	}
	resume := &cobra.Command{
		Use:   "resume",
		Short: "Resume your bot",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return patchBot(map[string]interface{}{"paused": false})
		},
	}
	register := &cobra.Command{
		Use:   "register",
		Short: "Make a key that acts only as your bot",
		Long: `Make an API key that acts only as your bot: it forecasts and comments as
the bot and can't do anything else as you. Creates the bot first if you have
none. Signs you in through the browser if you aren't already.

--save keeps the key as this machine's login (replacing yours), so an agent
here acts only as the bot. Revoke it under API tokens in your settings.`,
		Args: cobra.NoArgs,
		RunE: runBotRegister,
	}
	register.Flags().String("username", "", "Bot username, if it's being created (default <you>-bot)")
	register.Flags().Bool("save", false, "Save the key as this machine's login, replacing yours")
	register.Flags().Bool("no-browser", false, "When signing in, print the login URL instead of opening a browser")

	command.AddCommand(create, rename, pause, resume, register)
	return command
}

// botKey is POST /me/bot/key.
type botKey struct {
	APIKey string  `json:"api_key"`
	Bot    botInfo `json:"bot"`
}

func runBotRegister(cmd *cobra.Command, args []string) error {
	if !client.HasAuth() {
		if err := runOAuthBrowserLogin(cmd); err != nil {
			return err
		}
		client = api.NewClient(cfg)
	}
	body := map[string]interface{}{}
	if name, _ := cmd.Flags().GetString("username"); name != "" {
		body["username"] = name
	}
	previous := client.AsBot
	client.AsBot = false
	resp, err := client.Post("/me/bot/key", body)
	client.AsBot = previous
	if err != nil {
		return err
	}
	var key botKey
	if err := resp.RawInto(&key); err != nil {
		return fmt.Errorf("parsing bot key: %w", err)
	}

	save, _ := cmd.Flags().GetBool("save")
	if save {
		unlock, err := config.Lock()
		if err != nil {
			return err
		}
		cfg.Token = key.APIKey
		cfg.ClearOAuthState()
		cfg.AuthServer = config.NormalizeURL(cfg.ResolveBaseURL())
		err = cfg.Save()
		unlock()
		if err != nil {
			return fmt.Errorf("saving config: %w", err)
		}
	}

	if jsonOutput || !output.IsTTY() {
		output.JSON(json.RawMessage(resp.Body))
		return nil
	}
	output.KeyValue([][2]string{{"bot", key.Bot.Username}, {"api key", key.APIKey}})
	if save {
		fmt.Println("\nSaved: commands here now act as the bot.")
	} else {
		fmt.Println("\nGive the key to your agent as ANTISTATIC_TOKEN. It is shown once.")
	}
	return nil
}

func patchBot(changes map[string]interface{}) error {
	return botRequest(func() (*api.Response, error) {
		return client.Patch("/me/settings", map[string]interface{}{"bot": changes})
	}, true)
}

// botRequest runs a request as the owner and prints the bot it returns,
// from {"bot": ...} or, for settings, {"settings": {"bot": ...}}.
func botRequest(send func() (*api.Response, error), fromSettings bool) error {
	if err := requireAuth(); err != nil {
		return err
	}
	previous := client.AsBot
	client.AsBot = false
	resp, err := send()
	client.AsBot = previous
	if err != nil {
		return err
	}

	var payload struct {
		Bot      json.RawMessage `json:"bot"`
		Settings struct {
			Bot json.RawMessage `json:"bot"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(unwrapData(resp), &payload); err != nil {
		return fmt.Errorf("parsing bot: %w", err)
	}
	raw := payload.Bot
	if fromSettings {
		raw = payload.Settings.Bot
	}
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}

	if jsonOutput || !output.IsTTY() {
		output.JSON(json.RawMessage(`{"bot":` + string(raw) + `}`))
		return nil
	}
	var bot *botInfo
	if err := json.Unmarshal(raw, &bot); err != nil {
		return fmt.Errorf("parsing bot: %w", err)
	}
	if bot == nil {
		fmt.Println("No bot. Create one with \"antistatic bot create\".")
		return nil
	}
	status := "active"
	if bot.Paused {
		status = "paused"
	}
	pairs := [][2]string{{"username", bot.Username}, {"status", status}}
	if bot.ProfileURL != "" {
		pairs = append(pairs, [2]string{"profile", bot.ProfileURL})
	}
	output.KeyValue(pairs)
	return nil
}

func init() {
	rootCmd.AddCommand(newBotCmd())
}
