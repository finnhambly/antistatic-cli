package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/finnhambly/antistatic-cli/internal/timelines"
	"github.com/spf13/cobra"
)

// Manifests carry sealed answers (outcomes, resolution notes), so nothing in
// these commands prints a manifest's values: problems are a path and a code.

// progressOut receives progress lines and warnings (stderr; a variable for tests).
var progressOut io.Writer = os.Stderr

// timelinesPollInterval is how often push polls the import (a variable for tests).
var timelinesPollInterval = 2 * time.Second

type timelinesStatus struct {
	Slug           string   `json:"slug"`
	Version        string   `json:"version"`
	State          string   `json:"state"`
	ManifestSHA256 *string  `json:"manifest_sha256"`
	SourceCommit   *string  `json:"source_commit"`
	SchemaVersion  *int     `json:"schema_version"`
	PartsExpected  int      `json:"parts_expected"`
	PartsReceived  int      `json:"parts_received"`
	PartsImported  int      `json:"parts_imported"`
	Received       []string `json:"received"`
	Error          *struct {
		Code string `json:"code"`
		Path string `json:"path"`
	} `json:"error"`
}

type timelinesPartStored struct {
	Stored bool   `json:"stored"`
	SHA256 string `json:"sha256"`
	Items  int    `json:"items"`
	Bars   int    `json:"bars"`
	Bytes  int    `json:"bytes"`
}

// timelinesValidation's errors, warnings and lint are lists for owners and
// editors and counts for learners, so they stay raw.
type timelinesValidation struct {
	Valid                  bool            `json:"valid"`
	Errors                 json.RawMessage `json:"errors"`
	Warnings               json.RawMessage `json:"warnings"`
	UnresolvedPastBarCount int             `json:"unresolved_past_bar_count"`
	Lint                   json.RawMessage `json:"lint"`
	LintCounts             map[string]int  `json:"lint_counts"`
}

type timelinesPublished struct {
	Slug           string `json:"slug"`
	Version        string `json:"version"`
	Status         string `json:"status"`
	PublishedAt    string `json:"published_at"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

type timelinesMember struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

var timelinesVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)

func timelinesWantJSON() bool { return jsonOutput || !output.IsTTY() }

func newTimelinesCmd() *cobra.Command {
	command := &cobra.Command{
		Use:     "timelines",
		Aliases: []string{"timeline"},
		Short:   "Author curricula: check, push and publish timeline manifests",
		Long: `Author timeline curricula from a compact (schema_version 2) manifest.

  antistatic timelines check manifest.json [--curriculum SLUG]
  antistatic timelines push manifest.json --curriculum SLUG --version 1.2
  antistatic timelines status --curriculum SLUG --version 1.2
  antistatic timelines validate --curriculum SLUG --version 1.2
  antistatic timelines publish --curriculum SLUG --version 1.2
  antistatic timelines grant USER --curriculum SLUG --role learner|editor|owner
  antistatic timelines grant --revoke USER_ID --curriculum SLUG
  antistatic timelines delete --curriculum SLUG --version 1.2

Manifests contain sealed answers, so these commands never print a manifest's
values: each problem is shown as a path and a code.`,
	}
	command.AddCommand(newTimelinesCheckCmd(), newTimelinesPushCmd(), newTimelinesStatusCmd(),
		newTimelinesValidateCmd(), newTimelinesPublishCmd(), newTimelinesGrantCmd(), newTimelinesDeleteCmd())
	return command
}

func init() {
	rootCmd.AddCommand(newTimelinesCmd())
}

// curriculumVersion reads and checks --curriculum and --version.
func curriculumVersion(cmd *cobra.Command, needVersion bool) (string, string, error) {
	slug, _ := cmd.Flags().GetString("curriculum")
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return "", "", errors.New("--curriculum is required")
	}
	if !needVersion {
		return slug, "", nil
	}
	version, _ := cmd.Flags().GetString("version")
	version = strings.TrimSpace(version)
	if !timelinesVersionPattern.MatchString(version) {
		return "", "", errors.New("--version must be major.minor, e.g. 1.2")
	}
	return slug, version, nil
}

// timelinesError keeps an API error to its message, code, path and parts.
func timelinesError(err error) error {
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	var b strings.Builder
	b.WriteString(apiErr.Message)
	var extra []string
	if apiErr.Code != "" {
		extra = append(extra, apiErr.Code)
	}
	if apiErr.Path != "" {
		extra = append(extra, "path "+apiErr.Path)
	}
	if n := len(apiErr.Parts); n > 0 {
		shown := apiErr.Parts
		if n > 3 {
			shown = shown[:3]
		}
		s := fmt.Sprintf("%d part(s): %s", n, strings.Join(shown, ", "))
		if n > 3 {
			s += ", ..."
		}
		extra = append(extra, s)
	}
	if len(extra) > 0 {
		b.WriteString(" (" + strings.Join(extra, "; ") + ")")
	}
	return errors.New(b.String())
}

func isAPIStatus(err error, status int, code string) bool {
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == status && (code == "" || apiErr.Code == code)
}

// ----- check -----

func newTimelinesCheckCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "check <manifest.json>",
		Short: "Check a manifest against the server's limits, locally",
		Long: `Check a compact manifest against the server's limits without uploading
anything: schema version, shape, field lengths and patterns, curve lengths,
counts, part and top sizes, and hindsight wording (rule T5, a warning).

The limits come from GET /api/v1/timelines/limits, cached for an hour.
Problems print as "path: code"; values are never printed. Exits non-zero when
there are errors (warnings alone are fine).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, _ := cmd.Flags().GetString("curriculum")
			_, report, err := checkManifestFile(args[0], strings.TrimSpace(slug))
			if err != nil {
				return err
			}
			if timelinesWantJSON() {
				raw, _ := json.Marshal(report)
				output.JSON(raw)
			} else {
				printCheckReport(os.Stdout, report)
			}
			if !report.OK() {
				return fmt.Errorf("the manifest has %d error(s)", len(report.Errors))
			}
			return nil
		},
	}
	command.Flags().String("curriculum", "", "Curriculum slug, to check it too")
	return command
}

func checkManifestFile(path, slug string) (*timelines.Manifest, *timelines.Report, error) {
	manifest, err := timelines.Load(path)
	if err != nil {
		return nil, nil, err
	}
	limits, err := loadLimits()
	if err != nil {
		return nil, nil, err
	}
	report, err := timelines.Check(manifest, limits, slug)
	if err != nil {
		return nil, nil, err
	}
	return manifest, report, nil
}

func printCheckReport(w io.Writer, r *timelines.Report) {
	c := r.Counts
	fmt.Fprintf(w, "checkpoints %d, items %d, bars %d, parts %d, bytes %d (top %d)\n",
		c.Checkpoints, c.Items, c.Bars, c.Parts, c.Bytes, c.TopBytes)
	if len(r.Errors) > 0 {
		fmt.Fprintf(w, "%d error(s):\n", len(r.Errors))
		for _, v := range r.Errors {
			fmt.Fprintln(w, "  "+v.String())
		}
	}
	if len(r.Warnings) > 0 {
		fmt.Fprintf(w, "%d warning(s):\n", len(r.Warnings))
		for _, v := range r.Warnings {
			fmt.Fprintln(w, "  "+v.String())
		}
	}
	if len(r.Errors) == 0 && len(r.Warnings) == 0 {
		fmt.Fprintln(w, "no problems found")
	}
}

// ----- push -----

func newTimelinesPushCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "push <manifest.json> --curriculum SLUG --version MAJOR.MINOR",
		Short: "Upload a manifest as a draft, wait for the import and validate it",
		Long: `Upload a manifest as a curriculum version's draft: check it, upload the parts
the server doesn't have yet, send the manifest, wait for the import and
validate the draft. Re-running after any failure resumes: stored parts are
skipped. push never publishes; use "antistatic timelines publish".

A manifest built from uncommitted changes (source.dirty) or with no
source.commit is refused unless --allow-dirty.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireTimelinesAuth(); err != nil {
				return err
			}
			slug, version, err := curriculumVersion(cmd, true)
			if err != nil {
				return err
			}
			allowDirty, _ := cmd.Flags().GetBool("allow-dirty")
			timeout, _ := cmd.Flags().GetDuration("timeout")
			return runTimelinesPush(args[0], slug, version, allowDirty, timeout)
		},
	}
	command.Flags().String("curriculum", "", "Curriculum slug (required)")
	command.Flags().String("version", "", "Version, major.minor (required)")
	command.Flags().Bool("allow-dirty", false, "Push a manifest built from uncommitted changes or with no source commit")
	command.Flags().Duration("timeout", 30*time.Minute, "How long to wait for the import")
	return command
}

func runTimelinesPush(path, slug, version string, allowDirty bool, timeout time.Duration) error {
	manifest, err := timelines.Load(path)
	if err != nil {
		return err
	}
	src := manifest.Source()
	if src.Dirty && !allowDirty {
		return errors.New("the manifest was built from uncommitted changes (source.dirty); commit and rebuild, or pass --allow-dirty")
	}
	if src.Commit == "" && !allowDirty {
		return errors.New("the manifest has no source.commit; build it from a commit, or pass --allow-dirty")
	}

	limits, err := loadLimits()
	if err != nil {
		return err
	}
	report, err := timelines.Check(manifest, limits, slug)
	if err != nil {
		return err
	}
	printCheckReport(progressOut, report)
	if !report.OK() {
		return fmt.Errorf("the manifest has %d error(s); nothing was uploaded", len(report.Errors))
	}
	split := report.Split

	status, err := getTimelinesStatus(slug, version)
	if err != nil {
		return err
	}
	var base interface{}
	received := map[string]bool{}
	if status != nil {
		if status.ManifestSHA256 != nil {
			base = *status.ManifestSHA256
		}
		for _, s := range status.Received {
			received[s] = true
		}
		same := status.ManifestSHA256 != nil && *status.ManifestSHA256 == manifest.SHA256
		if same && (status.State == "imported" || status.State == "published") {
			fmt.Fprintf(progressOut, "%s %s already has this manifest (%s); nothing to push.\n", slug, version, status.State)
			printTimelinesStatus(status)
			return nil
		}
		if status.State == "published" {
			return fmt.Errorf("%s %s is published; push to a new version", slug, version)
		}
	}

	parts := map[string]timelines.Part{}
	shas := make([]string, 0, len(split.Parts))
	for _, p := range split.Parts {
		parts[p.SHA256] = p
		shas = append(shas, p.SHA256)
	}
	putPart := func(p timelines.Part) error {
		resp, err := client.PutRaw(api.Path("/timelines/curricula/{slug}/drafts/{version}/parts/{sha256}", slug, version, p.SHA256), p.Body)
		if err != nil {
			return timelinesError(err)
		}
		var stored timelinesPartStored
		if err := resp.DataInto(&stored); err != nil {
			return err
		}
		received[p.SHA256] = true
		return nil
	}
	skipped, sent := 0, 0
	for i, p := range split.Parts {
		if received[p.SHA256] {
			skipped++
			continue
		}
		fmt.Fprintf(progressOut, "part %d/%d: %d items, %d bars, %d bytes\n", i+1, len(split.Parts), p.Items, p.Bars, len(p.Body))
		if err := putPart(p); err != nil {
			return fmt.Errorf("part %d/%d: %w", i+1, len(split.Parts), err)
		}
		sent++
	}
	fmt.Fprintf(progressOut, "parts: %d sent, %d already stored\n", sent, skipped)

	source := map[string]interface{}{"commit": nil, "dirty": true}
	if src.Present {
		source["dirty"] = src.Dirty
		if src.Commit != "" {
			source["commit"] = src.Commit
		}
	}
	body, err := timelines.Canonical(map[string]interface{}{
		"schema_version":  manifest.Root["schema_version"],
		"source":          source,
		"manifest_sha256": manifest.SHA256,
		"base_sha256":     base,
		"top":             split.TopValue,
		"parts":           shas,
	})
	if err != nil {
		return err
	}
	putManifest := func() (*api.Response, error) {
		return client.PutRaw(api.Path("/timelines/curricula/{slug}/drafts/{version}/manifest", slug, version), body)
	}
	resp, err := putManifest()
	if err != nil && isAPIStatus(err, 422, "missing_parts") {
		var apiErr *api.APIError
		errors.As(err, &apiErr)
		fmt.Fprintf(progressOut, "the server is missing %d part(s); sending them\n", len(apiErr.Parts))
		for _, sha := range apiErr.Parts {
			p, ok := parts[sha]
			if !ok {
				return fmt.Errorf("the server asked for part %s, which this manifest doesn't have", sha)
			}
			if err := putPart(p); err != nil {
				return err
			}
		}
		resp, err = putManifest()
	}
	if err != nil {
		if isAPIStatus(err, 409, "draft_changed") {
			return fmt.Errorf("%w\nThe draft changed while pushing (someone else pushed?); check \"antistatic timelines status\" and push again", timelinesError(err))
		}
		return timelinesError(err)
	}
	var st timelinesStatus
	if err := resp.DataInto(&st); err != nil {
		return err
	}

	final, err := waitForImport(slug, version, &st, timeout)
	if err != nil {
		return err
	}

	validation, err := validateTimelinesDraft(slug, version)
	if err != nil {
		return err
	}
	if timelinesWantJSON() {
		raw, _ := json.Marshal(map[string]interface{}{"check": report, "status": final, "validation": validation})
		output.JSON(raw)
	} else {
		printTimelinesStatus(final)
		fmt.Println()
		printTimelinesValidation(validation)
	}
	if !validation.Valid {
		return fmt.Errorf("%s %s imported but is not valid; fix the errors and push again", slug, version)
	}
	return nil
}

// getTimelinesStatus returns nil for a version that doesn't exist yet.
func getTimelinesStatus(slug, version string) (*timelinesStatus, error) {
	resp, err := client.Get(api.Path("/timelines/curricula/{slug}/drafts/{version}/status", slug, version), nil)
	if err != nil {
		if isAPIStatus(err, 404, "") {
			return nil, nil
		}
		return nil, timelinesError(err)
	}
	var st timelinesStatus
	if err := resp.DataInto(&st); err != nil {
		return nil, err
	}
	return &st, nil
}

func waitForImport(slug, version string, st *timelinesStatus, timeout time.Duration) (*timelinesStatus, error) {
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		line := fmt.Sprintf("%s: %d/%d parts imported", st.State, st.PartsImported, st.PartsExpected)
		if line != last {
			fmt.Fprintln(progressOut, line)
			last = line
		}
		switch st.State {
		case "imported", "published":
			return st, nil
		case "failed":
			msg := "the import failed"
			if st.Error != nil {
				msg += ": " + st.Error.Code
				if st.Error.Path != "" {
					msg += " at " + st.Error.Path
				}
			}
			return nil, errors.New(msg + "; fix the manifest and push again")
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("still %s after %s; run push again to keep waiting", st.State, timeout)
		}
		time.Sleep(timelinesPollInterval)
		next, err := getTimelinesStatus(slug, version)
		if err != nil {
			return nil, err
		}
		if next == nil {
			return nil, errors.New("the draft disappeared while importing")
		}
		st = next
	}
}

func validateTimelinesDraft(slug, version string) (*timelinesValidation, error) {
	resp, err := client.Post(api.Path("/timelines/curricula/{slug}/drafts/{version}/validate", slug, version), nil)
	if err != nil {
		return nil, timelinesError(err)
	}
	var v timelinesValidation
	if err := resp.DataInto(&v); err != nil {
		return nil, err
	}
	return &v, nil
}

func printTimelinesStatus(st *timelinesStatus) {
	str := func(p *string) string {
		if p == nil || *p == "" {
			return "—"
		}
		return *p
	}
	schema := "—"
	if st.SchemaVersion != nil {
		schema = strconv.Itoa(*st.SchemaVersion)
	}
	pairs := [][2]string{
		{"Curriculum", st.Slug},
		{"Version", st.Version},
		{"State", st.State},
		{"Manifest", str(st.ManifestSHA256)},
		{"Commit", str(st.SourceCommit)},
		{"Schema", schema},
		{"Parts", fmt.Sprintf("%d expected, %d received, %d imported", st.PartsExpected, st.PartsReceived, st.PartsImported)},
	}
	if st.Error != nil {
		e := st.Error.Code
		if st.Error.Path != "" {
			e += " at " + st.Error.Path
		}
		pairs = append(pairs, [2]string{"Error", e})
	}
	output.KeyValue(pairs)
}

// listOrCount reads a field that is a list (owners, editors) or a count (learners).
func listOrCount(raw json.RawMessage) ([]json.RawMessage, int) {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		return list, len(list)
	}
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return nil, n
	}
	return nil, 0
}

func printTimelinesValidation(v *timelinesValidation) {
	valid := "no"
	if v.Valid {
		valid = "yes"
	}
	errs, nErr := listOrCount(v.Errors)
	warns, nWarn := listOrCount(v.Warnings)
	lint, _ := listOrCount(v.Lint)
	output.KeyValue([][2]string{
		{"Valid", valid},
		{"Errors", strconv.Itoa(nErr)},
		{"Warnings", strconv.Itoa(nWarn)},
		{"Unresolved past bars", strconv.Itoa(v.UnresolvedPastBarCount)},
	})
	if len(v.LintCounts) > 0 {
		rules := make([]string, 0, len(v.LintCounts))
		for r := range v.LintCounts {
			rules = append(rules, r)
		}
		sort.Strings(rules)
		var parts []string
		for _, r := range rules {
			parts = append(parts, fmt.Sprintf("%s %d", r, v.LintCounts[r]))
		}
		fmt.Println("Lint: " + strings.Join(parts, ", "))
	}
	printRawList := func(title string, items []json.RawMessage) {
		if len(items) == 0 {
			return
		}
		fmt.Printf("\n%s:\n", title)
		for _, item := range items {
			var s string
			if json.Unmarshal(item, &s) == nil {
				fmt.Println("  " + s)
				continue
			}
			var l struct {
				Rule  string `json:"rule"`
				Where string `json:"where"`
				Term  string `json:"term"`
			}
			if json.Unmarshal(item, &l) == nil && l.Rule != "" {
				line := l.Where + ": " + l.Rule
				if l.Term != "" {
					line += fmt.Sprintf(" %q", l.Term)
				}
				fmt.Println("  " + line)
				continue
			}
			fmt.Println("  " + string(item))
		}
	}
	printRawList("Errors", errs)
	printRawList("Warnings", warns)
	printRawList("Lint", lint)
}

// ----- status, validate, publish, delete -----

func newTimelinesStatusCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "status --curriculum SLUG --version MAJOR.MINOR",
		Short: "Show a curriculum version's draft status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireTimelinesAuth(); err != nil {
				return err
			}
			slug, version, err := curriculumVersion(cmd, true)
			if err != nil {
				return err
			}
			resp, err := client.Get(api.Path("/timelines/curricula/{slug}/drafts/{version}/status", slug, version), nil)
			if err != nil {
				if isAPIStatus(err, 404, "") {
					return fmt.Errorf("%s has no version %s", slug, version)
				}
				return timelinesError(err)
			}
			var st timelinesStatus
			if err := resp.DataInto(&st); err != nil {
				return err
			}
			if timelinesWantJSON() {
				raw, _ := resp.Data()
				output.JSON(raw)
				return nil
			}
			printTimelinesStatus(&st)
			return nil
		},
	}
	addCurriculumVersionFlags(command)
	return command
}

func newTimelinesValidateCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "validate --curriculum SLUG --version MAJOR.MINOR",
		Short: "Validate an imported draft (as push does after importing)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireTimelinesAuth(); err != nil {
				return err
			}
			slug, version, err := curriculumVersion(cmd, true)
			if err != nil {
				return err
			}
			v, err := validateTimelinesDraft(slug, version)
			if err != nil {
				return err
			}
			if timelinesWantJSON() {
				raw, _ := json.Marshal(v)
				output.JSON(raw)
			} else {
				printTimelinesValidation(v)
			}
			if !v.Valid {
				return fmt.Errorf("%s %s is not valid", slug, version)
			}
			return nil
		},
	}
	addCurriculumVersionFlags(command)
	return command
}

func newTimelinesPublishCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "publish --curriculum SLUG --version MAJOR.MINOR",
		Short: "Publish an imported draft (validates first)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireTimelinesAuth(); err != nil {
				return err
			}
			slug, version, err := curriculumVersion(cmd, true)
			if err != nil {
				return err
			}
			resp, err := client.Post(api.Path("/timelines/curricula/{slug}/drafts/{version}/publish", slug, version), nil)
			if err != nil {
				if isAPIStatus(err, 422, "invalid_manifest") {
					return fmt.Errorf("%w\nRun \"antistatic timelines validate --curriculum %s --version %s\" for the errors", timelinesError(err), slug, version)
				}
				return timelinesError(err)
			}
			var pub timelinesPublished
			if err := resp.DataInto(&pub); err != nil {
				return err
			}
			if timelinesWantJSON() {
				raw, _ := json.Marshal(pub)
				output.JSON(raw)
				return nil
			}
			output.KeyValue([][2]string{
				{"Curriculum", pub.Slug},
				{"Version", pub.Version},
				{"Status", pub.Status},
				{"Published", pub.PublishedAt},
				{"Manifest", pub.ManifestSHA256},
			})
			return nil
		},
	}
	addCurriculumVersionFlags(command)
	return command
}

func newTimelinesDeleteCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "delete --curriculum SLUG --version MAJOR.MINOR",
		Short: "Delete a draft and its stored parts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireTimelinesAuth(); err != nil {
				return err
			}
			slug, version, err := curriculumVersion(cmd, true)
			if err != nil {
				return err
			}
			yes, _ := cmd.Flags().GetBool("yes")
			if !yes && output.IsTTY() {
				fmt.Printf("Delete the %s %s draft and its parts? [y/N] ", slug, version)
				var answer string
				fmt.Scanln(&answer)
				if !strings.EqualFold(strings.TrimSpace(answer), "y") {
					fmt.Println("Cancelled.")
					return nil
				}
			}
			resp, err := client.Delete(api.Path("/timelines/curricula/{slug}/drafts/{version}", slug, version))
			if err != nil {
				return timelinesError(err)
			}
			if timelinesWantJSON() {
				raw, _ := resp.Data()
				output.JSON(raw)
				return nil
			}
			fmt.Printf("Deleted the %s %s draft.\n", slug, version)
			return nil
		},
	}
	addCurriculumVersionFlags(command)
	command.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	return command
}

func addCurriculumVersionFlags(command *cobra.Command) {
	command.Flags().String("curriculum", "", "Curriculum slug (required)")
	command.Flags().String("version", "", "Version, major.minor (required)")
}

// ----- grant -----

func newTimelinesGrantCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "grant <username-or-email> --curriculum SLUG --role learner|editor|owner",
		Short: "Give someone a role on a curriculum, or remove them (--revoke USER_ID)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireTimelinesAuth(); err != nil {
				return err
			}
			slug, _, err := curriculumVersion(cmd, false)
			if err != nil {
				return err
			}
			revoke, _ := cmd.Flags().GetInt("revoke")
			if revoke > 0 {
				if len(args) > 0 {
					return errors.New("--revoke takes a user id and no other argument")
				}
				resp, err := client.Delete(api.Path("/timelines/curricula/{slug}/members/{user_id}", slug, revoke))
				if err != nil {
					return timelinesError(err)
				}
				if timelinesWantJSON() {
					raw, _ := resp.Data()
					output.JSON(raw)
					return nil
				}
				fmt.Printf("Removed user %d from %s.\n", revoke, slug)
				return nil
			}
			if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
				return errors.New("name a username or email (or --revoke USER_ID)")
			}
			role, _ := cmd.Flags().GetString("role")
			switch role {
			case "learner", "editor", "owner":
			default:
				return errors.New("--role must be learner, editor or owner")
			}
			resp, err := client.Post(api.Path("/timelines/curricula/{slug}/members", slug),
				map[string]string{"user": strings.TrimSpace(args[0]), "role": role})
			if err != nil {
				return timelinesError(err)
			}
			var member timelinesMember
			if err := resp.DataInto(&member); err != nil {
				return err
			}
			if timelinesWantJSON() {
				raw, _ := json.Marshal(member)
				output.JSON(raw)
				return nil
			}
			fmt.Printf("%s (user %d) is now %s on %s.\n", member.Username, member.UserID, member.Role, slug)
			return nil
		},
	}
	command.Flags().String("curriculum", "", "Curriculum slug (required)")
	command.Flags().String("role", "", "learner, editor or owner")
	command.Flags().Int("revoke", 0, "Remove this user id from the curriculum instead")
	return command
}

// requireTimelinesAuth checks for a token. Authoring acts as the owner, never
// as their bot, so --as-bot is ignored here.
func requireTimelinesAuth() error {
	client.AsBot = false
	return requireAuth()
}
