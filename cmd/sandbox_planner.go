package cmd

import (
	"errors"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

// Planner jobs: a client-planned run's turns wait for the player's own agent
// to draw, plan and script them. Output is always JSON.

func newSandboxPlannerCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "planner",
		Short: "Plan a client-planned run's turn from its job id (JSON, for agents)",
	}
	command.AddCommand(
		plannerCmd("job JOB_ID", "Show a planner job", "", func(job string, _ interface{}) (*api.Response, error) {
			return client.Get(api.Path("/sandbox/planner_jobs/{job_id}", job), nil)
		}),
		plannerCmd("draw JOB_ID --events FILE", "Run draws for a job", "events", func(job string, v interface{}) (*api.Response, error) {
			return client.Post(api.Path("/sandbox/planner_jobs/{job_id}/draws", job), jsonMap{"events": v})
		}),
		plannerCmd("plan JOB_ID --plan FILE", "Commit a job's plan", "plan", func(job string, v interface{}) (*api.Response, error) {
			return client.Post(api.Path("/sandbox/planner_jobs/{job_id}/plan", job), jsonMap{"plan": v})
		}),
		plannerCmd("script JOB_ID --script FILE", "Store a job's script (or wrap-up)", "script", func(job string, v interface{}) (*api.Response, error) {
			return client.Post(api.Path("/sandbox/planner_jobs/{job_id}/script", job), jsonMap{"script": v})
		}),
		plannerCmd("notes JOB_ID [--set FILE]", "Read or replace the planner's notes", "set", func(job string, v interface{}) (*api.Response, error) {
			if v == nil {
				return client.Get(api.Path("/sandbox/planner_jobs/{job_id}/notes", job), nil)
			}
			return client.Put(api.Path("/sandbox/planner_jobs/{job_id}/notes", job), jsonMap{"notes": v})
		}),
	)
	return command
}

// plannerCmd builds one planner subcommand. flag names its input (from FILE
// or -): events and plan are JSON; script and notes are text.
func plannerCmd(use, short, flag string, send func(job string, v interface{}) (*api.Response, error)) *cobra.Command {
	command := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			var value interface{}
			if flag != "" {
				src, _ := cmd.Flags().GetString(flag)
				switch {
				case src == "" && flag != "set":
					return errors.New("--" + flag + " is required")
				case src == "":
				case flag == "script" || flag == "set":
					text, err := readTextSource(src)
					if err != nil {
						return err
					}
					value = text
				default:
					v, err := readJSONSource(src)
					if err != nil {
						return err
					}
					value = v
				}
			}
			client.SetTimeout(sandboxLongTimeout)
			resp, err := send(args[0], value)
			if err != nil {
				return err
			}
			raw, err := resp.Data()
			if err != nil {
				return err
			}
			output.JSON(raw)
			return nil
		},
	}
	if flag != "" {
		command.Flags().String(flag, "", "From FILE or - (stdin)")
	}
	return command
}
