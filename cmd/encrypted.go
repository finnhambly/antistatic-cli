package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/config"
	"github.com/finnhambly/antistatic-cli/internal/e2e"
	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Encrypted wargames: the recovery phrase and keys stay on this machine.
// encrypt logs in if needed, then sets up (a new phrase) or unlocks (the
// phrase typed); call encrypts a tool call's arguments with the saved key
// and decrypts what comes back.

const pluginInstall = "/plugin install antistatic --marketplace finnhambly/antistatic-plugin"

func init() {
	encryptCmd := &cobra.Command{
		Use:   "encrypt",
		Short: "Set up or unlock encrypted wargames on this machine",
		Long: `Set up or unlock encrypted wargames on this machine.

Logs in first if needed. The first time, it shows a new 6-word recovery
phrase and asks you to retype two of its words; after that, it asks for
the phrase.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: runEncrypt,
	}
	encryptCmd.Flags().Bool("no-browser", false, "Print the login URL instead of opening a browser")

	unlockCmd := &cobra.Command{
		Use:   "unlock",
		Short: "Unlock encrypted wargames with your recovery phrase",
		Args:  usageArgs(cobra.NoArgs),
		RunE:  runUnlock,
	}
	unlockCmd.Flags().Bool("no-browser", false, "Print the login URL instead of opening a browser")

	lockCmd := &cobra.Command{
		Use:   "lock",
		Short: "Delete this machine's key for encrypted wargames",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := forgetKeys(); err != nil {
				return err
			}
			fmt.Println("Locked.")
			return nil
		},
	}

	callCmd := &cobra.Command{
		Use:   "call <tool> [@args.json]",
		Short: "Call a tool in an encrypted wargame (JSON arguments on stdin)",
		Long: `Call an Antistatic tool for an encrypted wargame. The JSON arguments come
on stdin (or from @file), never the command line. Text fields are encrypted
with this machine's key; the result is printed decrypted (--json: its
structured data).

Exit codes: 2 bad arguments, 3 not logged in, 4 not set up, 5 locked,
6 the tool refused the call.`,
		Args: usageArgs(cobra.RangeArgs(1, 2)),
		RunE: runCall,
	}

	rootCmd.AddCommand(encryptCmd, unlockCmd, lockCmd, callCmd)
}

// ── keys on disk ────────────────────────────────────────────────────────

type savedKey struct {
	FP  string `json:"fp"`
	Enc string `json:"enc"`
	IV  string `json:"iv"`
}

// Saved keys by site and account: {"<url> <user id>": key}.
func keysPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "keys.json"), nil
}

func readKeys() (map[string]savedKey, error) {
	path, err := keysPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]savedKey{}, nil
	}
	if err != nil {
		return nil, err
	}
	keys := map[string]savedKey{}
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return keys, nil
}

func writeKeys(keys map[string]savedKey) error {
	path, err := keysPath()
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, _ := json.MarshalIndent(keys, "", "  ")
	return config.WritePrivate(path, append(data, '\n'))
}

func keySlot(userID string) string { return client.BaseURL() + " " + userID }

func saveKey(setup e2e.Setup, raw e2e.Raw) error {
	unlock, err := config.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	keys, err := readKeys()
	if err != nil {
		return err
	}
	keys[keySlot(setup.UserID)] = savedKey{FP: e2e.Fingerprint(setup.Check), Enc: e2e.ToB64(raw.Enc), IV: e2e.ToB64(raw.IV)}
	return writeKeys(keys)
}

// forgetKeys deletes the saved keys for this server.
func forgetKeys() error {
	unlock, err := config.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	return forgetKeysLocked()
}

// forgetKeysLocked is forgetKeys for a caller holding the config lock.
func forgetKeysLocked() error {
	keys, err := readKeys()
	if err != nil {
		return err
	}
	prefix := client.BaseURL() + " "
	for k := range keys {
		if strings.HasPrefix(k, prefix) {
			delete(keys, k)
		}
	}
	return writeKeys(keys)
}

// loadKeys is the saved key for the account, checked against its setup; errLocked when none opens it.
func loadKeys(setup e2e.Setup) (*e2e.Keys, error) {
	keys, err := readKeys()
	if err != nil {
		return nil, err
	}
	k, ok := keys[keySlot(setup.UserID)]
	if !ok || k.FP != e2e.Fingerprint(setup.Check) {
		return nil, errLocked
	}
	enc, err1 := e2e.FromB64(k.Enc)
	iv, err2 := e2e.FromB64(k.IV)
	if err1 != nil || err2 != nil {
		return nil, errLocked
	}
	out, err := e2e.ImportKeys(e2e.Raw{Enc: enc, IV: iv}, setup.UserID)
	if err != nil || !out.Verify(setup.Check) {
		return nil, errLocked
	}
	return out, nil
}

// ── the account's setup ─────────────────────────────────────────────────

// fetchSetup is the account's key setup; ok is false before set-up.
func fetchSetup() (setup e2e.Setup, ok bool, err error) {
	if !client.HasAuth() {
		return setup, false, errNotLoggedIn
	}
	res, err := client.CallTool("antistatic_private_setup", nil)
	if err != nil {
		return setup, false, toolErr(err)
	}
	if res.IsError {
		if strings.Contains(res.Text(), "Sign in") {
			return setup, false, errNotLoggedIn
		}
		return setup, false, errors.New(res.Text())
	}
	var s struct {
		UserID  json.Number `json:"user_id"`
		Version *int        `json:"version"`
		Salt    *string     `json:"salt"`
		Wrapped *string     `json:"wrapped"`
		Check   *string     `json:"check"`
	}
	body := res.StructuredContent
	if len(body) == 0 {
		body = []byte(res.Text())
	}
	if err := json.Unmarshal(body, &s); err != nil || s.UserID == "" {
		return setup, false, fmt.Errorf("unexpected setup response")
	}
	setup.UserID = s.UserID.String()
	if s.Salt == nil || s.Wrapped == nil || s.Check == nil {
		return setup, false, nil
	}
	if s.Version == nil || *s.Version != e2e.Format {
		return setup, false, fmt.Errorf("this account uses an encryption format this CLI doesn't know: brew upgrade finnhambly/tap/antistatic")
	}
	setup.Version, setup.Salt, setup.Wrapped, setup.Check = *s.Version, *s.Salt, *s.Wrapped, *s.Check
	return setup, true, nil
}

func toolErr(err error) error {
	if errors.Is(err, api.ErrLoginRequired) {
		return errNotLoggedIn
	}
	return err
}

// ── prompts ─────────────────────────────────────────────────────────────

var stdinIsTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// The phrase is shown on stdout: only to a terminal, never a file or pipe.
var stdoutIsTerminal = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// readSecret reads a line without echo; readLine with it.
var readSecret = func(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return string(b), err
}

var stdinLines = bufio.NewReader(os.Stdin)

var readLine = func(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	line, err := stdinLines.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func needTerminal() error {
	if !stdinIsTerminal() || !stdoutIsTerminal() {
		return exitErr(ExitUsage, "this asks for your recovery phrase: %s", encryptHint)
	}
	return nil
}

// ── commands ────────────────────────────────────────────────────────────

// loggedInSetup logs in with the browser when there is no usable login, then fetches the setup.
func loggedInSetup(cmd *cobra.Command) (e2e.Setup, bool, error) {
	setup, ok, err := fetchSetup()
	if !errors.Is(err, errNotLoggedIn) {
		return setup, ok, err
	}
	if os.Getenv("ANTISTATIC_TOKEN") != "" {
		return setup, ok, err
	}
	if err := runOAuthBrowserLogin(cmd); err != nil {
		return setup, false, err
	}
	resetClient()
	return fetchSetup()
}

func runEncrypt(cmd *cobra.Command, args []string) error {
	if err := needTerminal(); err != nil {
		return err
	}
	setup, ok, err := loggedInSetup(cmd)
	if err != nil {
		return err
	}
	if !ok {
		return setUp(setup.UserID)
	}
	if _, err := loadKeys(setup); err == nil {
		fmt.Println("Already unlocked.")
		return nil
	}
	return unlock(setup)
}

func runUnlock(cmd *cobra.Command, args []string) error {
	if err := needTerminal(); err != nil {
		return err
	}
	setup, ok, err := loggedInSetup(cmd)
	if err != nil {
		return err
	}
	if !ok {
		return errNotSetUp
	}
	return unlock(setup)
}

func unlock(setup e2e.Setup) error {
	for tries := 0; ; tries++ {
		phrase, err := readSecret("Recovery phrase: ")
		if err != nil {
			return err
		}
		raw, err := e2e.UnlockRaw(phrase, setup, e2e.Iterations)
		if err != nil {
			// Never echo what was typed (a word, or a suggestion near it).
			msg := "wrong recovery phrase"
			if !errors.Is(err, e2e.ErrWrongPhrase) {
				msg = "not a recovery phrase (6 words from the list)"
			}
			if tries < 2 {
				fmt.Fprintf(os.Stderr, "%s. Try again.\n", capitalize(msg))
				continue
			}
			return errors.New(msg)
		}
		if err := saveKey(setup, raw); err != nil {
			return err
		}
		fmt.Println("Unlocked.")
		printNext()
		return nil
	}
}

func setUp(userID string) error {
	phrase, setup, raw, err := e2e.NewSetup(userID, e2e.Iterations)
	if err != nil {
		return err
	}
	// The phrase first; nothing is stored until two of its words come back.
	showPhrase(phrase)
	words := strings.Split(phrase, " ")
	norm := func(w string) string { return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(w)), "-", "") }
	for _, n := range e2e.ConfirmWords {
		got, err := readLine(fmt.Sprintf("Retype word %d: ", n))
		if err != nil {
			return err
		}
		if norm(got) != norm(words[n-1]) {
			return exitErr(ExitFailure, "word %d doesn't match; nothing was saved. Run \"antistatic encrypt\" again for a new phrase", n)
		}
	}
	body := e2e.EncodeJSON(e2e.Object{
		{Key: "salt", Value: setup.Salt},
		{Key: "wrapped", Value: setup.Wrapped},
		{Key: "check", Value: setup.Check},
		{Key: "version", Value: setup.Version},
	}, 0)
	res, err := client.CallTool("antistatic_private_setup", []byte(body))
	if err != nil {
		return toolErr(err)
	}
	if res.IsError {
		return errors.New(res.Text())
	}
	if err := saveKey(setup, raw); err != nil {
		return err
	}
	fmt.Println("\nSet up and unlocked.")
	printNext()
	return nil
}

var showPhrase = func(phrase string) {
	fmt.Printf("\nRecovery phrase:\n\n    %s\n\nSave this somewhere safe. It unlocks your encrypted wargames on any\nmachine and on the site. Antistatic can't recover it.\n\n", phrase)
}

func printNext() {
	fmt.Printf("Next, ask Claude: Start an encrypted wargame about …\n(Claude Code needs the plugin: %s)\n", pluginInstall)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func runCall(cmd *cobra.Command, args []string) error {
	tool := args[0]
	var data []byte
	var err error
	switch {
	case len(args) == 2 && strings.HasPrefix(args[1], "@"):
		data, err = os.ReadFile(strings.TrimPrefix(args[1], "@"))
	case len(args) == 2:
		return exitErr(ExitUsage, "pass the JSON arguments on stdin (or @file), not on the command line")
	case term.IsTerminal(int(os.Stdin.Fd())):
		return exitErr(ExitUsage, "pass the JSON arguments on stdin (or @file)")
	default:
		data, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		return exitErr(ExitUsage, "reading arguments: %v", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		data = []byte("{}")
	}
	parsed, err := e2e.ParseJSON(data)
	if _, isObj := parsed.(e2e.Object); err != nil || !isObj {
		return exitErr(ExitUsage, "the arguments must be a JSON object")
	}
	if !e2e.HasTool(tool) {
		return exitErr(ExitUsage, "%s isn't available for encrypted runs", tool)
	}

	setup, ok, err := fetchSetup()
	if err != nil {
		return err
	}
	if !ok {
		return errNotSetUp
	}
	keys, err := loadKeys(setup)
	if err != nil {
		return err
	}
	sealed, err := e2e.EncryptCall(keys, tool, parsed)
	if err != nil {
		return exitErr(ExitUsage, "%v", err)
	}
	res, err := client.CallTool(tool, []byte(e2e.EncodeJSON(sealed, 0)))
	if err != nil {
		return toolErr(err)
	}
	if res.IsError {
		return exitErr(ExitToolError, "%s", keys.DecryptText(res.Text()))
	}
	if jsonOutput {
		var v any = nil
		if len(res.StructuredContent) > 0 {
			if v, err = e2e.ParseJSON(res.StructuredContent); err != nil {
				return err
			}
		}
		fmt.Println(e2e.EncodeJSON(keys.DecryptDeep(v), 2))
		return nil
	}
	fmt.Println(keys.DecryptResult(res.Text()))
	return nil
}

// encryptionState is not_logged_in, not_set_up, locked or unlocked.
func encryptionState() (string, error) {
	setup, ok, err := fetchSetup()
	switch {
	case errors.Is(err, errNotLoggedIn):
		return "not_logged_in", nil
	case err != nil:
		return "", err
	case !ok:
		return "not_set_up", nil
	}
	if _, err := loadKeys(setup); err != nil {
		return "locked", nil
	}
	return "unlocked", nil
}

func runStatus(cmd *cobra.Command, args []string) error {
	state, err := encryptionState()
	if err != nil {
		state = "unknown"
	}
	if jsonOutput || !stdoutIsTerminal() {
		out := e2e.Object{{Key: "server", Value: client.BaseURL()}, {Key: "state", Value: state}}
		if err != nil {
			out = append(out, e2e.Member{Key: "error", Value: err.Error()})
		}
		fmt.Println(e2e.EncodeJSON(out, 2))
		return nil
	}
	pairs := [][2]string{{"Server", client.BaseURL()}}
	if token := cfg.ResolveToken(); token != "" {
		source := "config file"
		if os.Getenv("ANTISTATIC_TOKEN") != "" {
			source = "ANTISTATIC_TOKEN env var"
		}
		pairs = append(pairs, [2]string{"Source", source})
		if source == "config file" {
			mode := "API token"
			if cfg.OAuthClientID != "" && cfg.OAuthRefreshToken != "" {
				mode = "OAuth session"
			}
			pairs = append(pairs, [2]string{"Mode", mode})
			if cfg.OAuthTokenExpiry != "" {
				pairs = append(pairs, [2]string{"Access token expiry", cfg.OAuthTokenExpiry})
			}
		}
	}
	pairs = append(pairs, [2]string{"State", strings.ReplaceAll(state, "_", " ")})
	if err != nil {
		pairs = append(pairs, [2]string{"Error", err.Error()})
	}
	output.KeyValue(pairs)
	return nil
}

// resetClient rebuilds the API client after a login changed the config.
func resetClient() {
	c := api.NewClient(cfg)
	c.Version = client.Version
	c.AsBot = client.AsBot
	client = c
}
