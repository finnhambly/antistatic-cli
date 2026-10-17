package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Exit codes agents act on.
const (
	ExitFailure     = 1 // anything else
	ExitUsage       = 2 // bad arguments, or a prompt with no terminal
	ExitNotLoggedIn = 3
	ExitNotSetUp    = 4
	ExitLocked      = 5
	ExitToolError   = 6 // the tool refused the call; its message is on stderr
)

// ExitError carries an exit code with its message.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

func exitErr(code int, format string, args ...any) error {
	return &ExitError{Code: code, Err: fmt.Errorf(format, args...)}
}

// ExitCode is the process exit code for an error from Execute.
func ExitCode(err error) int {
	var e *ExitError
	if errors.As(err, &e) {
		return e.Code
	}
	if strings.HasPrefix(err.Error(), "unknown command") {
		return ExitUsage
	}
	return ExitFailure
}

// usageArgs makes an argument check's error a usage error (exit 2).
func usageArgs(check cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := check(cmd, args); err != nil {
			return &ExitError{Code: ExitUsage, Err: err}
		}
		return nil
	}
}

const encryptHint = `run "antistatic encrypt" in your terminal`

var (
	errNotLoggedIn = exitErr(ExitNotLoggedIn, "not logged in: %s", encryptHint)
	errNotSetUp    = exitErr(ExitNotSetUp, "encryption not set up: %s", encryptHint)
	errLocked      = exitErr(ExitLocked, `locked: run "antistatic unlock" in your terminal`)
)
