package cmd

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

// Future sandboxes (wargames): commands mirroring the MCP sandbox tools, each
// a thin layer over one REST route.

// sandboxLongTimeout covers a turn's planning and narration (up to ~4 minutes).
const sandboxLongTimeout = 6 * time.Minute

func futureSandboxCommands() []*cobra.Command {
	return []*cobra.Command{
		newSandboxListCmd(), newSandboxShowCmd(), newSandboxStartCmd(), newSandboxSetupCmd(),
		newSandboxTurnCmd(), newSandboxAskCmd(), newSandboxOrdersCmd(), newSandboxWaitCmd(),
		newSandboxCorrectCmd(), newSandboxGoalCmd(), newSandboxForkCmd(), newSandboxRenameCmd(),
		newSandboxArchiveCmd(), newSandboxWrapupCmd(), newSandboxImportCmd(), newSandboxPromoteCmd(),
		newSandboxSourcesCmd(), newSandboxLibraryCmd(), newSandboxQuestionCmd(), newSandboxItemsCmd(),
		newSandboxPlannerCmd(),
	}
}

// ----- helpers -----

type jsonMap = map[string]interface{}

func parseRunID(s string) (int, error) {
	id, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("run id must be a positive integer, got %q", s)
	}
	return id, nil
}

// readJSONSource reads JSON from a file path, or stdin for "-".
func readJSONSource(source string) (interface{}, error) {
	var raw []byte
	var err error
	if source == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(source)
	}
	if err != nil {
		return nil, err
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("%s is not JSON: %w", source, err)
	}
	return v, nil
}

func readJSONArray(source, what string) ([]interface{}, error) {
	v, err := readJSONSource(source)
	if err != nil {
		return nil, err
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON array", what)
	}
	return arr, nil
}

func readJSONObject(source, what string) (jsonMap, error) {
	v, err := readJSONSource(source)
	if err != nil {
		return nil, err
	}
	obj, ok := v.(jsonMap)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object", what)
	}
	return obj, nil
}

// get walks nested objects; nil when any step is missing.
func get(v interface{}, path ...string) interface{} {
	for _, key := range path {
		m, ok := v.(jsonMap)
		if !ok {
			return nil
		}
		v = m[key]
	}
	return v
}

func str(v interface{}, path ...string) string {
	switch x := get(v, path...).(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

func list(v interface{}, path ...string) []interface{} {
	arr, _ := get(v, path...).([]interface{})
	return arr
}

func day(v interface{}, path ...string) string {
	return formatSandboxDate(str(v, path...))
}

// sandboxData decodes a response's data and prints it: JSON when piped or
// with --json, else through human.
func sandboxData(resp *api.Response, human func(d jsonMap)) error {
	raw, err := resp.Data()
	if err != nil {
		return err
	}
	if sandboxWantJSON() || human == nil {
		output.JSON(raw)
		return nil
	}
	var d jsonMap
	if err := json.Unmarshal(raw, &d); err != nil {
		output.JSON(raw)
		return nil
	}
	human(d)
	return nil
}

func runArgCmd(use, short string, nargs int, run func(cmd *cobra.Command, id int, args []string) error) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(nargs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			id, err := parseRunID(args[0])
			if err != nil {
				return err
			}
			return run(cmd, id, args[1:])
		},
	}
}

func putString(cmd *cobra.Command, body jsonMap, flag, key string) {
	if cmd.Flags().Changed(flag) {
		v, _ := cmd.Flags().GetString(flag)
		body[key] = v
	}
}

// addEntryFlags adds the flags that build a list of turn entries.
func addEntryFlags(command *cobra.Command, types ...string) {
	command.Flags().String("entries", "", "Entries as a JSON array, from FILE or - (stdin)")
	for _, t := range types {
		switch t {
		case "action":
			command.Flags().StringArray("action", nil, "An attempt, which can fail (repeatable)")
		case "decree":
			command.Flags().StringArray("decree", nil, "Happens as stated (repeatable)")
		case "feed":
			command.Flags().StringArray("feed", nil, "A binding correction to the world (repeatable)")
		case "info":
			command.Flags().StringArray("info", nil, "A question about what you'd already know (repeatable)")
		case "answer":
			command.Flags().StringArray("answer", nil, "ID=TEXT: an answer to a question put to you (repeatable)")
		}
	}
}

// entriesFromFlags builds entries from --entries and the per-type flags;
// character, when set, goes on action and decree entries.
func entriesFromFlags(cmd *cobra.Command, character string) ([]interface{}, error) {
	var entries []interface{}
	if src, _ := cmd.Flags().GetString("entries"); src != "" {
		arr, err := readJSONArray(src, "--entries")
		if err != nil {
			return nil, err
		}
		entries = append(entries, arr...)
	}
	for _, t := range []string{"action", "decree", "feed", "info"} {
		if cmd.Flags().Lookup(t) == nil {
			continue
		}
		texts, _ := cmd.Flags().GetStringArray(t)
		for _, text := range texts {
			entry := jsonMap{"type": t, "text": text}
			if character != "" && (t == "action" || t == "decree") {
				entry["character"] = character
			}
			entries = append(entries, entry)
		}
	}
	if cmd.Flags().Lookup("answer") != nil {
		answers, _ := cmd.Flags().GetStringArray("answer")
		for _, a := range answers {
			id, text, ok := strings.Cut(a, "=")
			if !ok || strings.TrimSpace(id) == "" || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("--answer %q: expected ID=TEXT", a)
			}
			entries = append(entries, jsonMap{"type": "feed", "text": text, "answers": strings.TrimSpace(id)})
		}
	}
	return entries, nil
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ----- list / show -----

func newSandboxListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Your sandbox runs, templates, curricula and AI budget",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			resp, err := client.Get("/sandbox", nil)
			if err != nil {
				return err
			}
			return sandboxData(resp, printSandboxList)
		},
	}
}

func printSandboxList(d jsonMap) {
	runs := list(d, "runs")
	if len(runs) > 0 {
		rows := [][]string{}
		for _, r := range runs {
			flags := []string{}
			if get(r, "complete") == true {
				flags = append(flags, "complete")
			}
			if get(r, "archived") == true {
				flags = append(flags, "archived")
			}
			if f := str(r, "forked_from_run_id"); f != "" {
				flags = append(flags, "fork of "+f)
			}
			title := str(r, "curriculum", "title")
			if n := str(r, "name"); n != "" {
				title = n + " · " + title
			}
			rows = append(rows, []string{str(r, "run_id"), truncateSandbox(title, 50), day(r, "clock"), strings.Join(flags, ", ")})
		}
		output.Table([]string{"RUN", "SANDBOX", "CLOCK", ""}, rows)
	} else {
		fmt.Println("No runs.")
	}
	if templates := list(d, "templates"); len(templates) > 0 {
		fmt.Println()
		rows := [][]string{}
		for _, t := range templates {
			rows = append(rows, []string{str(t, "key"), str(t, "title")})
		}
		output.Table([]string{"TEMPLATE", "TITLE"}, rows)
	}
	if curricula := list(d, "curricula"); len(curricula) > 0 {
		fmt.Println()
		rows := [][]string{}
		for _, c := range curricula {
			run := "—"
			if r := str(c, "run_id"); r != "" {
				run = r
			}
			rows = append(rows, []string{str(c, "slug"), truncateSandbox(str(c, "title"), 50), run})
		}
		output.Table([]string{"CURRICULUM", "TITLE", "RUN"}, rows)
	}
	if b, ok := get(d, "future_sandboxes", "ai_budget").(jsonMap); ok {
		fmt.Println()
		if b["remaining_usd"] == nil {
			fmt.Printf("AI budget (server planning): uncapped; $%s used this month.\n", str(b, "used_usd"))
		} else {
			fmt.Printf("AI budget (server planning): $%s of $%s left, resets %s.\n", str(b, "remaining_usd"), str(b, "allowance_usd"), str(b, "resets_on"))
		}
	}
}

func newSandboxShowCmd() *cobra.Command {
	command := runArgCmd("show RUN", "Show a sandbox run", 1, func(cmd *cobra.Command, id int, _ []string) error {
		query := url.Values{}
		if c, _ := cmd.Flags().GetString("character"); c != "" {
			query.Set("character", c)
		}
		audit, _ := cmd.Flags().GetBool("audit")
		if audit {
			query.Set("include_audit", "true")
		}
		resp, err := client.Get(api.Path("/sandbox/runs/{id}", id), query)
		if err != nil {
			return err
		}
		return sandboxData(resp, func(d jsonMap) {
			if str(d, "kind") != "future" {
				var run sandboxRun
				raw, _ := json.Marshal(d)
				_ = json.Unmarshal(raw, &run)
				printSandboxRun(run)
				return
			}
			printFutureRun(d)
			if audit {
				fmt.Println("\nAudit:")
				raw, _ := json.Marshal(d["audit"])
				output.JSON(raw)
			}
		})
	})
	command.Flags().String("character", "", "Your character's key, when you have several")
	command.Flags().Bool("audit", false, "Include how each turn was decided")
	return command
}

func printFutureRun(d jsonMap) {
	fmt.Printf("%s · run %s · clock %s · %s turns\n", str(d, "template", "title"), str(d, "run_id"), day(d, "clock"), str(d, "turns"))
	s := get(d, "settings")
	pairs := [][2]string{
		{"Mode", str(s, "mode")},
		{"Planner", str(s, "planner")},
		{"Pace", str(s, "pace")},
		{"Model", str(s, "model")},
	}
	if e := str(s, "ends_on"); e != "" {
		pairs = append(pairs, [2]string{"Ends on", e})
	}
	output.KeyValue(pairs)
	if ts, ok := get(d, "turn_status").(jsonMap); ok {
		fmt.Println()
		printTurnStatus(ts)
	}
	history := list(d, "history")
	if len(history) > 3 {
		history = history[len(history)-3:]
	}
	for _, t := range history {
		fmt.Printf("\nTurn %s (to %s)\n", str(t, "turn", "index"), day(t, "clock"))
		for _, o := range list(t, "observations") {
			fmt.Printf("  - %s: %s (%s)\n", day(o, "at"), str(o, "text"), str(o, "id"))
		}
	}
	if qs := list(d, "questions"); len(qs) > 0 {
		fmt.Println()
		rows := [][]string{}
		for _, q := range qs {
			rows = append(rows, []string{str(q, "market_code"), truncateSandbox(str(q, "title"), 60)})
		}
		output.Table([]string{"CODE", "QUESTION"}, rows)
	}
	if u := str(d, "url"); u != "" {
		fmt.Println("\n" + u)
	}
}

func printTurnStatus(s jsonMap) {
	length := "the planner picks"
	if l, ok := s["length"].(jsonMap); ok && str(l, "state") != "unset" {
		span := str(l, "span")
		if span == "" {
			span = str(l, "label")
		}
		if str(l, "until") == "auto" {
			span = "the planner picks"
		}
		length = fmt.Sprintf("%s (%s)", span, str(l, "state"))
	}
	line := fmt.Sprintf("Turn %s", str(s, "turn"))
	if s["multiplayer"] == true {
		line += fmt.Sprintf(": %s/%s submitted", str(s, "submitted"), str(s, "expected"))
	}
	line += " · next: " + length
	if b := str(s, "busy"); b != "" {
		line += " · turn " + b
	}
	fmt.Println(line)
	if s["multiplayer"] == true {
		for _, p := range list(s, "players") {
			name := str(p, "name")
			if name == "" {
				name = str(p, "key")
			}
			if get(p, "you") == true {
				name += " (you)"
			}
			fmt.Printf("  - %s: %s\n", name, str(p, "status"))
		}
	}
	if qs := list(s, "open_questions"); len(qs) > 0 {
		fmt.Println("The planner asks:")
		for _, q := range qs {
			fmt.Printf("  - (%s) %s\n", str(q, "id"), str(q, "question"))
		}
	}
	if n := str(s, "next", "text"); n != "" {
		fmt.Printf("Next (%s): %s\n", str(s, "next", "action"), n)
	}
}

// ----- start / setup -----

func addCharactersFlag(command *cobra.Command) {
	command.Flags().String("characters", "", "Characters as a JSON array of {name,tagline,bio}, from FILE or -")
}

func readCharacters(cmd *cobra.Command, body jsonMap) error {
	if src, _ := cmd.Flags().GetString("characters"); src != "" {
		arr, err := readJSONArray(src, "--characters")
		if err != nil {
			return err
		}
		body["characters"] = arr
	}
	return nil
}

func newSandboxStartCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "start (--template KEY | --premise TEXT | --curriculum SLUG)",
		Short: "Start a sandbox run",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			template, _ := cmd.Flags().GetString("template")
			premise, _ := cmd.Flags().GetString("premise")
			curriculum, _ := cmd.Flags().GetString("curriculum")
			body := jsonMap{}
			switch {
			case curriculum != "":
				if template != "" || premise != "" {
					return errors.New("give --curriculum alone")
				}
				body["curriculum"] = curriculum
			case template != "" || premise != "":
				if template != "" {
					body["template"] = template
				}
				for _, f := range []struct{ flag, key string }{
					{"premise", "premise"}, {"title", "title"}, {"bio", "bio"}, {"model", "model"},
					{"planner", "planner"}, {"sample-from", "sample_from"}, {"mode", "mode"},
				} {
					putString(cmd, body, f.flag, f.key)
				}
				if err := readCharacters(cmd, body); err != nil {
					return err
				}
			default:
				return errors.New("give --template, --premise or --curriculum (antistatic sandbox list shows templates)")
			}
			client.SetTimeout(sandboxLongTimeout)
			resp, err := client.Post("/sandbox/runs", body)
			if err != nil {
				return err
			}
			return sandboxData(resp, func(d jsonMap) {
				title := str(d, "setup", "template", "title")
				if title == "" {
					title = str(d, "template", "title")
				}
				if title == "" {
					title = str(d, "curriculum", "title")
				}
				fmt.Printf("Started run %s (%s), clock %s.\n", str(d, "run_id"), title, day(d, "clock"))
				if p := str(d, "setup", "bio_prompt"); p != "" {
					fmt.Println("Bio prompt: " + p)
				}
				if p := str(d, "setup", "pacing"); p != "" {
					fmt.Println("Turn lengths: " + p)
				}
				if get(d, "settings", "complete") == false {
					fmt.Println("Set it up before the first turn: antistatic sandbox setup " + str(d, "run_id") + " --bio ...")
				}
			})
		},
	}
	f := command.Flags()
	f.String("template", "", "Template key (antistatic sandbox list)")
	f.String("premise", "", "Your own scenario's premise")
	f.String("title", "", "Scenario title")
	f.String("curriculum", "", "Curriculum slug (a past sandbox)")
	f.String("bio", "", "Who you play")
	f.String("planner", "", "server or client")
	f.String("mode", "", "mortal or god")
	f.String("sample-from", "", "house or user (god mode)")
	f.String("model", "", "opus, sonnet or haiku")
	addCharactersFlag(command)
	return command
}

func newSandboxSetupCmd() *cobra.Command {
	command := runArgCmd("setup RUN", "Change a future run's setup", 1, func(cmd *cobra.Command, id int, _ []string) error {
		body := jsonMap{}
		for _, f := range []struct{ flag, key string }{
			{"title", "title"}, {"premise", "premise"}, {"bio", "bio"}, {"modifications", "modifications"},
			{"pace", "pace"}, {"model", "model"}, {"planner", "planner"}, {"mode", "mode"}, {"sample-from", "sample_from"},
		} {
			putString(cmd, body, f.flag, f.key)
		}
		if cmd.Flags().Changed("ends-on") {
			v, _ := cmd.Flags().GetString("ends-on")
			if strings.EqualFold(v, "none") || v == "" {
				body["ends_on"] = nil
			} else {
				body["ends_on"] = v
			}
		}
		if cmd.Flags().Changed("scout") {
			v, _ := cmd.Flags().GetBool("scout")
			body["scout"] = v
		}
		if err := readCharacters(cmd, body); err != nil {
			return err
		}
		if len(body) == 0 {
			return errors.New("nothing to change")
		}
		client.SetTimeout(sandboxLongTimeout)
		resp, err := client.Patch(api.Path("/sandbox/runs/{id}/setup", id), body)
		if err != nil {
			return err
		}
		return sandboxData(resp, printFutureRun)
	})
	f := command.Flags()
	f.String("title", "", "Scenario title")
	f.String("premise", "", "Premise")
	f.String("bio", "", "Who you play")
	f.String("modifications", "", "Changes to the world")
	f.String("pace", "", "Default turn length")
	f.String("model", "", "opus, sonnet or haiku")
	f.String("planner", "", "server or client")
	f.String("mode", "", "mortal or god")
	f.String("sample-from", "", "house or user (god mode)")
	f.String("ends-on", "", "Planned end, YYYY-MM-DD, or none")
	f.Bool("scout", true, "The world-market scout")
	addCharactersFlag(command)
	return command
}

// ----- turn -----

func newSandboxTurnCmd() *cobra.Command {
	command := runArgCmd("turn RUN", "Take a turn of a future run", 1, func(cmd *cobra.Command, id int, _ []string) error {
		character, _ := cmd.Flags().GetString("character")
		entries, err := entriesFromFlags(cmd, character)
		if err != nil {
			return err
		}
		turnID, _ := cmd.Flags().GetString("turn-id")
		if turnID == "" {
			turnID = newUUID()
			fmt.Fprintf(progressOut, "turn id %s (retry with --turn-id %s)\n", turnID, turnID)
		}
		body := jsonMap{"turn_id": turnID}
		if len(entries) > 0 {
			body["entries"] = entries
		}
		putString(cmd, body, "until", "until")
		client.SetTimeout(sandboxLongTimeout)
		resp, err := client.Post(api.Path("/sandbox/runs/{id}/turns", id), body)
		if err != nil {
			return err
		}
		return sandboxData(resp, func(d jsonMap) { printTurn(id, turnID, d) })
	})
	addEntryFlags(command, "action", "decree", "feed", "info", "answer")
	command.Flags().String("character", "", "Your character's key, for --action and --decree")
	command.Flags().String("until", "", "End of the turn: a date, or auto")
	command.Flags().String("turn-id", "", "Turn key; reuse it to retry the same turn")
	return command
}

func printTurn(runID int, turnID string, d jsonMap) {
	retry := fmt.Sprintf("antistatic sandbox turn %d --turn-id %s", runID, turnID)
	switch str(d, "status") {
	case "planner_job":
		fmt.Printf("Planner job %s (expires %s); nothing has happened yet.\n", str(d, "job_id"), str(d, "expires_at"))
		fmt.Printf("Hand the job id to the run's planner (antistatic sandbox planner job %s), then: %s, with the same entries.\n", str(d, "job_id"), retry)
		return
	case "questions":
		fmt.Println("The turn hasn't happened: answer these first (the clock hasn't moved).")
		for _, q := range list(d, "questions_for_player") {
			fmt.Printf("- %s (id %s; assumed meanwhile: %s)\n", str(q, "question"), str(q, "id"), str(q, "assumption"))
		}
		fmt.Printf("Then: %s, the same entries, and --answer ID=TEXT for each. Without answers a retry proceeds on the assumptions.\n", retry)
		return
	case "questions_needed":
		fmt.Println("No turn yet; the clock hasn't moved. " + str(d, "next"))
		fmt.Println("Then: " + retry)
		return
	}
	if script := str(d, "script"); script != "" {
		fmt.Println(script)
	} else {
		fmt.Printf("Turn %s: %s to %s. Clock now %s.\n", str(d, "turn", "index"), day(d, "turn", "from"), day(d, "turn", "to"), day(d, "clock"))
		for _, o := range list(d, "observations") {
			fmt.Printf("- %s: %s\n", day(o, "at"), str(o, "text"))
		}
	}
	if a := str(d, "answers"); a != "" {
		fmt.Println("\n" + a)
	}
	if obs := list(d, "observations"); len(obs) > 0 {
		ids := []string{}
		for _, o := range obs {
			ids = append(ids, str(o, "id"))
		}
		fmt.Println("\nObservation ids: " + strings.Join(ids, ", "))
	}
	if qs := list(d, "questions_for_player"); len(qs) > 0 {
		fmt.Println("\nQuestions for you (answer next turn with --answer ID=TEXT):")
		for _, q := range qs {
			fmt.Printf("- %s (id %s)\n", str(q, "question"), str(q, "id"))
		}
	}
	if codes := list(d, "show", "market_codes"); len(codes) > 0 {
		cs := []string{}
		for _, c := range codes {
			cs = append(cs, fmt.Sprint(c))
		}
		fmt.Printf("\nMarkets with an event: %s (antistatic show CODE --run %d)\n", strings.Join(cs, ", "), runID)
	}
	if get(d, "wrapup_due") == true {
		fmt.Printf("\nThe game has reached its planned end: antistatic sandbox wrapup %d\n", runID)
	}
}

// ----- ask / orders / wait -----

func newSandboxAskCmd() *cobra.Command {
	command := runArgCmd("ask RUN", "Ask what you'd already know, or record a feed, without time passing", 1, func(cmd *cobra.Command, id int, _ []string) error {
		entries, err := entriesFromFlags(cmd, "")
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return errors.New("give --info, --feed or --entries")
		}
		body := jsonMap{"entries": entries}
		putString(cmd, body, "character", "character")
		client.SetTimeout(sandboxLongTimeout)
		resp, err := client.Post(api.Path("/sandbox/runs/{id}/info", id), body)
		if err != nil {
			return err
		}
		return sandboxData(resp, func(d jsonMap) {
			if n := str(d, "feeds_recorded"); n != "" && n != "0" {
				fmt.Printf("Recorded %s feed(s); no time passed.\n", n)
			}
			if a := str(d, "answer"); a != "" {
				fmt.Println(a)
			}
		})
	})
	addEntryFlags(command, "info", "feed", "answer")
	command.Flags().String("character", "", "Your character's key")
	return command
}

func newSandboxOrdersCmd() *cobra.Command {
	command := runArgCmd("orders RUN", "Show or file your orders for the next turn of a multiplayer run", 1, func(cmd *cobra.Command, id int, _ []string) error {
		character, _ := cmd.Flags().GetString("character")
		entries, err := entriesFromFlags(cmd, "")
		if err != nil {
			return err
		}
		var resp *api.Response
		if len(entries) == 0 {
			query := url.Values{}
			if character != "" {
				query.Set("character", character)
			}
			resp, err = client.Get(api.Path("/sandbox/runs/{id}/orders", id), query)
		} else {
			body := jsonMap{"entries": entries}
			if character != "" {
				body["character"] = character
			}
			if draft, _ := cmd.Flags().GetBool("draft"); draft {
				body["submit"] = false
			}
			resp, err = client.Put(api.Path("/sandbox/runs/{id}/orders", id), body)
		}
		if err != nil {
			return err
		}
		return sandboxData(resp, func(d jsonMap) {
			who := str(d, "character")
			if who == "" {
				who = "Host"
			}
			state := "draft"
			if str(d, "submitted_at") != "" {
				state = "submitted"
			}
			fmt.Printf("%s: %d order(s), %s.\n", who, len(list(d, "entries")), state)
			for _, e := range list(d, "entries") {
				fmt.Printf("- %s: %s\n", str(e, "type"), str(e, "text"))
			}
		})
	})
	addEntryFlags(command, "action", "decree")
	command.Flags().String("character", "", "Your character's key")
	command.Flags().Bool("draft", false, "Save without submitting")
	return command
}

func newSandboxWaitCmd() *cobra.Command {
	command := runArgCmd("wait RUN", "Show the next turn's status, waiting for a change", 1, func(cmd *cobra.Command, id int, _ []string) error {
		query := url.Values{}
		seconds, _ := cmd.Flags().GetInt("seconds")
		if seconds > 0 {
			query.Set("wait_seconds", strconv.Itoa(seconds))
			client.SetTimeout(time.Duration(seconds+30) * time.Second)
		}
		for _, f := range []string{"since", "character"} {
			if v, _ := cmd.Flags().GetString(f); v != "" {
				query.Set(f, v)
			}
		}
		resp, err := client.Get(api.Path("/sandbox/runs/{id}/turn_status", id), query)
		if err != nil {
			return err
		}
		return sandboxData(resp, func(d jsonMap) {
			printTurnStatus(d)
			fmt.Println("version " + str(d, "version"))
		})
	})
	command.Flags().Int("seconds", 30, "Seconds to wait for a change, 0-60")
	command.Flags().String("since", "", "The version you last saw")
	command.Flags().String("character", "", "Your character's key")
	return command
}

// ----- correct / goal -----

func newSandboxCorrectCmd() *cobra.Command {
	command := runArgCmd("correct RUN (--fact TEXT | --strike OBS_ID --reason TEXT)", "Add an established fact, or strike an observation", 1, func(cmd *cobra.Command, id int, _ []string) error {
		fact, _ := cmd.Flags().GetString("fact")
		strike, _ := cmd.Flags().GetString("strike")
		reason, _ := cmd.Flags().GetString("reason")
		if fact == "" && strike == "" {
			return errors.New("give --fact, or --strike with --reason")
		}
		if strike != "" && reason == "" {
			return errors.New("--strike needs --reason")
		}
		var resp *api.Response
		var err error
		if fact != "" {
			if resp, err = client.Post(api.Path("/sandbox/runs/{id}/corrections", id), jsonMap{"fact": fact}); err != nil {
				return err
			}
		}
		if strike != "" {
			if resp, err = client.Post(api.Path("/sandbox/runs/{id}/observations/{observation_id}/strike", id, strike), jsonMap{"reason": reason}); err != nil {
				return err
			}
		}
		return sandboxData(resp, func(d jsonMap) {
			for _, c := range list(d, "corrections") {
				fmt.Println("- " + str(c, "text"))
			}
			for _, s := range list(d, "struck_observations") {
				fmt.Printf("- struck %s: %s\n", str(s, "id"), str(s, "reason"))
			}
		})
	})
	command.Flags().String("fact", "", "A fact to establish")
	command.Flags().String("strike", "", "An observation id to strike")
	command.Flags().String("reason", "", "Why the observation is struck")
	return command
}

func newSandboxGoalCmd() *cobra.Command {
	command := runArgCmd("goal RUN CODE (--liquidity N | --remove)", "Set or remove a scored goal on a run question", 2, func(cmd *cobra.Command, id int, args []string) error {
		body := jsonMap{}
		if remove, _ := cmd.Flags().GetBool("remove"); remove {
			body["remove"] = true
		} else if cmd.Flags().Changed("liquidity") {
			n, _ := cmd.Flags().GetInt("liquidity")
			body["liquidity"] = n
		} else {
			return errors.New("give --liquidity N (100-1000) or --remove")
		}
		resp, err := client.Put(api.Path("/sandbox/runs/{id}/goals/{code}", id, args[0]), body)
		if err != nil {
			return err
		}
		return sandboxData(resp, printScore)
	})
	command.Flags().Int("liquidity", 0, "Goal liquidity, 100-1000")
	command.Flags().Bool("remove", false, "Remove the goal")
	return command
}

func printScore(d jsonMap) {
	score := get(d, "score")
	fmt.Println("Mode: " + str(score, "mode"))
	for _, g := range list(score, "goals") {
		fmt.Printf("- %s %s: liquidity %s, total %s\n", str(g, "market"), str(g, "title"), str(g, "liquidity"), str(g, "total"))
	}
	if t := str(score, "total", "total"); t != "" {
		fmt.Println("Score: " + t)
	}
}

// ----- fork / rename / archive / delete / wrapup -----

func newSandboxForkCmd() *cobra.Command {
	command := runArgCmd("fork RUN", "Fork a run, from a turn or checkpoint", 1, func(cmd *cobra.Command, id int, _ []string) error {
		body := jsonMap{}
		if cmd.Flags().Changed("turn") {
			n, _ := cmd.Flags().GetInt("turn")
			body["turn"] = n
		}
		putString(cmd, body, "checkpoint", "checkpoint")
		putString(cmd, body, "model", "model")
		client.SetTimeout(sandboxLongTimeout)
		resp, err := client.Post(api.Path("/sandbox/runs/{id}/forks", id), body)
		if err != nil {
			return err
		}
		return sandboxData(resp, func(d jsonMap) {
			fmt.Printf("Forked run %d as run %s, clock %s.\n", id, str(d, "run_id"), day(d, "clock"))
		})
	})
	command.Flags().Int("turn", 0, "Fork from after this turn")
	command.Flags().String("checkpoint", "", "Fork from this checkpoint (past runs)")
	command.Flags().String("model", "", "opus, sonnet or haiku")
	return command
}

func newSandboxRenameCmd() *cobra.Command {
	return runArgCmd("rename RUN NAME", "Name a run (\"\" clears it)", 2, func(cmd *cobra.Command, id int, args []string) error {
		var name interface{} = args[0]
		if args[0] == "" {
			name = nil
		}
		resp, err := client.Patch(api.Path("/sandbox/runs/{id}", id), jsonMap{"name": name})
		if err != nil {
			return err
		}
		return sandboxData(resp, func(d jsonMap) {
			if n := str(d, "name"); n != "" {
				fmt.Printf("Renamed run %d to %q.\n", id, n)
			} else {
				fmt.Printf("Cleared run %d's name.\n", id)
			}
		})
	})
}

func newSandboxArchiveCmd() *cobra.Command {
	command := runArgCmd("archive RUN", "Archive a run (--undo to bring it back)", 1, func(cmd *cobra.Command, id int, _ []string) error {
		undo, _ := cmd.Flags().GetBool("undo")
		resp, err := client.Patch(api.Path("/sandbox/runs/{id}", id), jsonMap{"archived": !undo})
		if err != nil {
			return err
		}
		return sandboxData(resp, func(d jsonMap) {
			if get(d, "archived") == true {
				fmt.Printf("Archived run %d.\n", id)
			} else {
				fmt.Printf("Run %d is back in your list.\n", id)
			}
		})
	})
	command.Flags().Bool("undo", false, "Unarchive")
	return command
}

func runSandboxRunDelete(cmd *cobra.Command, arg string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	id, err := parseRunID(arg)
	if err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if !yes {
		if !output.IsTTY() {
			return errors.New("deleting a run can't be undone; pass -y")
		}
		if !confirm(fmt.Sprintf("Delete sandbox run %d for good?", id)) {
			fmt.Println("Cancelled.")
			return nil
		}
	}
	resp, err := client.Delete(api.Path("/sandbox/runs/{id}", id), jsonMap{"confirm": true})
	if err != nil {
		return err
	}
	return sandboxData(resp, func(jsonMap) { fmt.Printf("Deleted run %d.\n", id) })
}

func newSandboxWrapupCmd() *cobra.Command {
	return runArgCmd("wrapup RUN", "Write the run's wrap-up", 1, func(cmd *cobra.Command, id int, _ []string) error {
		client.SetTimeout(sandboxLongTimeout)
		resp, err := client.Post(api.Path("/sandbox/runs/{id}/wrapup", id), jsonMap{})
		if err != nil {
			return err
		}
		return sandboxData(resp, func(d jsonMap) {
			if job := str(d, "job_id"); job != "" {
				fmt.Printf("Planner job %s (expires %s): hand it to the run's planner to write the arc; antistatic sandbox show %d then shows the wrap-up.\n", job, str(d, "expires_at"), id)
				return
			}
			w := get(d, "wrapup")
			fmt.Printf("%s to %s, %s turns.\n\n%s\n", day(w, "period", "from"), day(w, "period", "to"), str(w, "period", "turns"), str(w, "arc"))
			for _, r := range list(w, "resolutions") {
				what := str(r, "outcome")
				if what == "" {
					what = "resolved"
				}
				fmt.Printf("- %s: %s: %s\n", day(r, "date"), str(r, "title"), what)
			}
			if b := str(w, "forecasts", "resolved_bars"); b != "" && b != "0" {
				fmt.Printf("Forecasts: %s of %s right; Brier %s vs house %s.\n", str(w, "forecasts", "right"), b, str(w, "forecasts", "brier"), str(w, "forecasts", "house_brier"))
			}
		})
	})
}

// ----- questions: import / promote / question / items -----

func newSandboxImportCmd() *cobra.Command {
	command := runArgCmd("import RUN [--from RUN --codes a,b]", "List or import questions from your other future runs", 1, func(cmd *cobra.Command, id int, _ []string) error {
		from, _ := cmd.Flags().GetInt("from")
		codes, _ := cmd.Flags().GetStringSlice("codes")
		if from == 0 && len(codes) == 0 {
			resp, err := client.Get(api.Path("/sandbox/runs/{id}/importable-questions", id), nil)
			if err != nil {
				return err
			}
			return sandboxData(resp, func(d jsonMap) {
				qs := list(d, "questions")
				if len(qs) == 0 {
					fmt.Println("Nothing to import.")
					return
				}
				rows := [][]string{}
				for _, q := range qs {
					here := ""
					if get(q, "already_here") == true {
						here = "already here"
					}
					rows = append(rows, []string{str(q, "run_id"), str(q, "code"), truncateSandbox(str(q, "title"), 60), here})
				}
				output.Table([]string{"RUN", "CODE", "QUESTION", ""}, rows)
			})
		}
		if from == 0 || len(codes) == 0 {
			return errors.New("give --from and --codes together")
		}
		resp, err := client.Post(api.Path("/sandbox/runs/{id}/questions/import", id), jsonMap{"from_run_id": from, "codes": codes})
		if err != nil {
			return err
		}
		return sandboxData(resp, func(d jsonMap) {
			for _, i := range list(d, "imported") {
				fmt.Printf("Imported %s as %s.\n", str(i, "from"), str(i, "code"))
			}
		})
	})
	command.Flags().Int("from", 0, "Run to import from")
	command.Flags().StringSlice("codes", nil, "Question codes to import")
	return command
}

func newSandboxPromoteCmd() *cobra.Command {
	command := runArgCmd("promote RUN CODE [--spec FILE] [--create]", "Turn a run question into a real market (preview unless --create)", 2, func(cmd *cobra.Command, id int, args []string) error {
		create, _ := cmd.Flags().GetBool("create")
		body := jsonMap{"create": create}
		if src, _ := cmd.Flags().GetString("spec"); src != "" {
			spec, err := readSpec(src)
			if err != nil {
				return err
			}
			body["spec"] = spec
		}
		resp, err := client.Post(api.Path("/sandbox/runs/{id}/questions/{code}/promote", id, args[0]), body)
		if err != nil {
			return specErrorDetails(err)
		}
		return sandboxData(resp, func(d jsonMap) {
			if code := str(d, "code"); code != "" {
				fmt.Printf("Created %s, private to you: %s\n%s\n", code, str(d, "url"), str(d, "note"))
				return
			}
			raw, _ := json.Marshal(d["preview"])
			_ = printSpecPreview(raw)
			if n := str(d, "note"); n != "" {
				fmt.Println("\n" + n)
			}
		})
	})
	command.Flags().String("spec", "", "Spec overrides, from FILE or -")
	command.Flags().Bool("create", false, "Create the market")
	return command
}

func newSandboxQuestionCmd() *cobra.Command {
	command := runArgCmd("question RUN SPEC.json", "Create a question in a run (a market spec; - for stdin)", 2, func(cmd *cobra.Command, id int, args []string) error {
		spec, err := readSpec(args[0])
		if err != nil {
			return err
		}
		body := jsonMap{"spec": spec}
		if preview, _ := cmd.Flags().GetBool("preview"); preview {
			resp, err := client.Post(api.Path("/sandbox/runs/{id}/questions/preview", id), body)
			if err != nil {
				return err
			}
			return sandboxData(resp, func(d jsonMap) {
				raw, _ := json.Marshal(d)
				_ = printSpecPreview(raw)
			})
		}
		resp, err := client.Post(api.Path("/sandbox/runs/{id}/questions", id), body)
		if err != nil {
			return specErrorDetails(err)
		}
		return sandboxData(resp, func(d jsonMap) {
			fmt.Printf("Created %s in run %d: %s\n%s\n", str(d, "code"), id, str(d, "url"), str(d, "note"))
			if w := str(d, "warning"); w != "" {
				output.Warn(w)
			}
		})
	})
	command.Flags().Bool("preview", false, "Check the spec without creating it")
	return command
}

func newSandboxItemsCmd() *cobra.Command {
	command := runArgCmd("items RUN CODE --items FILE", "Add or rename items on a run question", 2, func(cmd *cobra.Command, id int, args []string) error {
		src, _ := cmd.Flags().GetString("items")
		if src == "" {
			return errors.New("--items is required: a JSON array of {key,label,anchors,basis}")
		}
		items, err := readJSONArray(src, "--items")
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.New("--items is empty")
		}
		also, _ := cmd.Flags().GetIntSlice("also-runs")
		var resp *api.Response
		for _, item := range items {
			m, ok := item.(jsonMap)
			if !ok {
				return errors.New("each item must be an object")
			}
			body := jsonMap{}
			for _, k := range []string{"key", "label", "anchors", "basis"} {
				if v, ok := m[k]; ok {
					body[k] = v
				}
			}
			if len(also) > 0 {
				body["also_runs"] = also
			}
			if resp, err = client.Post(api.Path("/sandbox/runs/{id}/questions/{code}/items", id, args[0]), body); err != nil {
				return fmt.Errorf("item %s: %w", str(m, "key"), err)
			}
		}
		return sandboxData(resp, func(d jsonMap) {
			keys := []string{}
			for _, i := range list(d, "items") {
				keys = append(keys, fmt.Sprintf("%s (%s)", str(i, "key"), str(i, "label")))
			}
			fmt.Printf("%s items: %s\n", args[0], strings.Join(keys, ", "))
		})
	})
	command.Flags().String("items", "", "Items as a JSON array, from FILE or -")
	command.Flags().IntSlice("also-runs", nil, "Other runs to make the same edit in")
	return command
}

// ----- sources / library -----

func newSandboxSourcesCmd() *cobra.Command {
	command := runArgCmd("sources RUN", "A past run's briefing, disclosures and feed", 1, func(cmd *cobra.Command, id int, _ []string) error {
		query := url.Values{}
		if v, _ := cmd.Flags().GetString("since"); v != "" {
			query.Set("since", v)
		}
		if n, _ := cmd.Flags().GetInt("limit"); n > 0 {
			query.Set("limit", strconv.Itoa(n))
		}
		resp, err := client.Get(api.Path("/sandbox/runs/{id}/sources", id), query)
		if err != nil {
			return err
		}
		return sandboxData(resp, func(d jsonMap) {
			fmt.Printf("%s, clock %s\n", str(d, "briefing", "title"), day(d, "clock"))
			fmt.Println("Checkpoint: " + str(d, "briefing", "checkpoint", "title"))
			for _, x := range list(d, "disclosures") {
				fmt.Printf("- %s · %s · %s %s: %s\n", day(x, "at"), str(x, "kind"), str(x, "question_code"), str(x, "title"), truncateSandbox(str(x, "text"), 600))
			}
			switch str(d, "feed", "status") {
			case "ok":
				for _, x := range list(d, "feed", "items") {
					fmt.Printf("- %s · %s · %s [%s]\n", day(x, "date"), str(x, "source"), str(x, "title"), str(x, "doc_id"))
				}
			case "":
			default:
				fmt.Println("Feed: " + str(d, "feed", "status"))
			}
		})
	})
	command.Flags().String("since", "", "previous, start or a date")
	command.Flags().Int("limit", 0, "Maximum feed items")
	return command
}

func newSandboxLibraryCmd() *cobra.Command {
	command := runArgCmd("library RUN QUERY", "Search a run's library, as of its clock", 2, func(cmd *cobra.Command, id int, args []string) error {
		query := url.Values{"q": {args[0]}}
		for _, f := range []string{"limit", "offset"} {
			if n, _ := cmd.Flags().GetInt(f); n > 0 {
				query.Set(f, strconv.Itoa(n))
			}
		}
		for _, f := range []string{"after", "sort"} {
			if v, _ := cmd.Flags().GetString(f); v != "" {
				query.Set(f, v)
			}
		}
		resp, err := client.Get(api.Path("/sandbox/runs/{id}/library", id), query)
		if err != nil {
			return err
		}
		return sandboxData(resp, func(d jsonMap) {
			results := list(d, "results")
			fmt.Printf("%d results dated before %s\n", len(results), day(d, "cutoff"))
			for _, r := range results {
				fmt.Printf("%s. %s — %s, %s [%s]\n   %s\n", str(r, "n"), str(r, "title"), str(r, "source"), str(r, "date"), str(r, "doc_id"),
					truncateSandbox(strings.Join(strings.Fields(str(r, "snippet")), " "), 400))
			}
			if get(d, "more") == true {
				fmt.Printf("More with --offset %s.\n", str(d, "next_offset"))
			}
		})
	})
	command.Flags().Int("limit", 0, "Results per page")
	command.Flags().Int("offset", 0, "Skip this many results")
	command.Flags().String("after", "", "Only documents after this date")
	command.Flags().String("sort", "", "Sort order")
	return command
}

// readTextSource reads a file, or stdin for "-".
func readTextSource(source string) (string, error) {
	var raw []byte
	var err error
	if source == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(source)
	}
	return string(raw), err
}
