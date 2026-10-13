package cmd

import "github.com/spf13/cobra"

// addRunHeaderFlag gives a market command --run: it then acts inside that
// future sandbox run (X-Antistatic-Run).
func addRunHeaderFlag(command *cobra.Command) {
	command.Flags().Int("run", 0, "Act inside this future sandbox run")
}

// applyRunHeader sends --run, when given, on the command's requests.
func applyRunHeader(cmd *cobra.Command) {
	if run, _ := cmd.Flags().GetInt("run"); run > 0 {
		client.Run = run
	}
}
