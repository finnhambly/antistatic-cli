package cmd

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/finnhambly/antistatic-cli/internal/e2e"
)

// fakeSite is the site's OAuth endpoints and /mcp, enough for encrypt, call and status.
type fakeSite struct {
	t *testing.T
	sync.Mutex
	server     *httptest.Server
	setup      map[string]any // the stored key setup
	challenge  string
	access     string
	refresh    string
	refreshOK  bool
	calls      []map[string]any
	authorized int
}

func newFakeSite(t *testing.T) *fakeSite {
	f := &fakeSite{t: t, access: "access-1", refresh: "refresh-1", refreshOK: true}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeSite) handle(w http.ResponseWriter, r *http.Request) {
	f.Lock()
	defer f.Unlock()
	switch r.URL.Path {
	case "/oauth/authorize":
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != f.server.URL+"/oauth/clients/cli.json" {
			f.t.Errorf("authorize: %v", q)
		}
		f.challenge = q.Get("code_challenge")
		f.authorized++
		cb, _ := url.Parse(q.Get("redirect_uri"))
		cq := cb.Query()
		cq.Set("code", "code-1")
		cq.Set("state", q.Get("state"))
		cq.Set("iss", f.server.URL)
		cb.RawQuery = cq.Encode()
		http.Redirect(w, r, cb.String(), http.StatusFound)
	case "/oauth/token":
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
			if r.PostForm.Get("code") != "code-1" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
		case "refresh_token":
			if !f.refreshOK || r.PostForm.Get("refresh_token") != f.refresh {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"invalid_grant","error_description":"revoked"}`))
				return
			}
			f.access, f.refresh = f.access+"r", f.refresh+"r"
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": f.access, "refresh_token": f.refresh, "expires_in": 3600, "token_type": "Bearer"})
	case "/mcp":
		if r.Header.Get("Authorization") != "Bearer "+f.access {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"invalid_token"}`))
			return
		}
		var req struct {
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		args := req.Params.Arguments
		f.calls = append(f.calls, map[string]any{"tool": req.Params.Name, "args": args})
		var result map[string]any
		switch req.Params.Name {
		case "antistatic_private_setup":
			if args["salt"] != nil {
				f.setup = map[string]any{"user_id": 42, "version": args["version"], "salt": args["salt"], "wrapped": args["wrapped"], "check": args["check"]}
				result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "Set."}}}
				break
			}
			s := f.setup
			if s == nil {
				s = map[string]any{"user_id": 42, "version": nil, "salt": nil, "wrapped": nil, "check": nil}
			}
			text, _ := json.Marshal(s)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(text)}}, "structuredContent": s}
		case "antistatic_start_sandbox":
			title, _ := args["title"].(string)
			if title == "" {
				result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "A title is needed."}}}
				break
			}
			result = map[string]any{
				"content":           []any{map[string]any{"type": "text", "text": "Started " + title + ". Run 7."}},
				"structuredContent": map[string]any{"run_id": 7, "title": title},
			}
		case "antistatic_take_turn":
			result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "No such character: " + args["entries"].([]any)[0].(map[string]any)["text"].(string) + "."}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	default:
		w.WriteHeader(404)
	}
}

type cliEnv struct {
	t    *testing.T
	site *fakeSite
	dir  string
}

func newCLIEnv(t *testing.T) *cliEnv {
	site := newFakeSite(t)
	dir := t.TempDir()
	t.Setenv("ANTISTATIC_CONFIG_DIR", dir)
	t.Setenv("ANTISTATIC_URL", site.server.URL)
	t.Setenv("ANTISTATIC_TOKEN", "")

	prevOpen, prevTTY, prevOut, prevSecret, prevLine := openBrowser, stdinIsTerminal, stdoutIsTerminal, readSecret, readLine
	t.Cleanup(func() {
		openBrowser, stdinIsTerminal, stdoutIsTerminal, readSecret, readLine = prevOpen, prevTTY, prevOut, prevSecret, prevLine
	})
	// The browser: follow the authorize redirect to the CLI's callback.
	openBrowser = func(u string) error {
		go func() {
			resp, err := http.Get(u)
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	stdinIsTerminal = func() bool { return true }
	stdoutIsTerminal = func() bool { return true }
	return &cliEnv{t: t, site: site, dir: dir}
}

// run runs the CLI with args and stdin; it returns stdout and the error.
func (e *cliEnv) run(stdin string, args ...string) (string, error) {
	e.t.Helper()
	jsonOutput = false
	r, w, _ := os.Pipe()
	w.Write([]byte(stdin))
	w.Close()
	prevIn := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = prevIn }()
	var err error
	out := captureStdout(e.t, func() {
		rootCmd.SetArgs(args)
		err = rootCmd.Execute()
	})
	return out, err
}

func answers(list ...string) func(string) (string, error) {
	return func(string) (string, error) {
		if len(list) == 0 {
			return "", errors.New("no more answers")
		}
		a := list[0]
		list = list[1:]
		return a, nil
	}
}

func TestEncryptLogsInSetsUpAndCalls(t *testing.T) {
	e := newCLIEnv(t)

	out, _ := e.run("", "status", "--json")
	if !strings.Contains(out, `"state": "not_logged_in"`) {
		t.Fatalf("status: %s", out)
	}

	// encrypt: browser login (PKCE checked by the fake), a new phrase, words 2 and 5 retyped.
	var phrase string
	readLine = func(prompt string) (string, error) {
		words := strings.Split(phrase, " ")
		if strings.Contains(prompt, "2") {
			return strings.ToUpper(words[1]), nil
		}
		return words[4], nil
	}
	prevShow := showPhrase
	defer func() { showPhrase = prevShow }()
	showPhrase = func(p string) { phrase = p; prevShow(p) }
	stdout, err := e.run("", "encrypt")
	if err != nil {
		t.Fatal(err)
	}
	if e.site.authorized != 1 || e.site.setup == nil || !strings.Contains(stdout, "Set up and unlocked.") || !strings.Contains(stdout, "Save this somewhere safe") {
		t.Fatalf("encrypt: authorized %d, setup %v\n%s", e.site.authorized, e.site.setup, stdout)
	}
	for _, name := range []string{"config.json", "keys.json"} {
		info, err := os.Stat(filepath.Join(e.dir, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", name, info, err)
		}
	}

	out, _ = e.run("", "status", "--json")
	if !strings.Contains(out, `"state": "unlocked"`) {
		t.Fatalf("status: %s", out)
	}

	// call: the title goes encrypted, comes back decrypted.
	out, err = e.run(`{"title": "Bakery gamble", "premise": "I quit.", "private": true}`, "call", "antistatic_start_sandbox")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "Started Bakery gamble. Run 7." {
		t.Fatalf("call: %q", out)
	}
	sent := e.site.calls[len(e.site.calls)-1]["args"].(map[string]any)
	if !strings.HasPrefix(sent["title"].(string), "enc2:") || sent["private"] != true {
		t.Fatalf("sent %v", sent)
	}
	out, err = e.run(`{"title": "Bakery gamble"}`, "call", "antistatic_start_sandbox", "--json")
	if err != nil || !strings.Contains(out, `"title": "Bakery gamble"`) {
		t.Fatalf("call --json: %q %v", out, err)
	}

	// Refused arguments and tool errors have their own exit codes.
	_, err = e.run(`{"title": "x", "nope": 1}`, "call", "antistatic_start_sandbox")
	if ExitCode(err) != ExitUsage || !strings.Contains(err.Error(), "nope: not a field") {
		t.Fatalf("unknown field: %v", err)
	}
	_, err = e.run(`{}`, "call", "antistatic_post_comment")
	if ExitCode(err) != ExitUsage {
		t.Fatalf("unknown tool: %v", err)
	}
	_, err = e.run("", "call", "antistatic_start_sandbox", `{"title":"x"}`)
	if ExitCode(err) != ExitUsage {
		t.Fatalf("arguments on the command line: %v", err)
	}
	_, err = e.run(`{"title": ""}`, "call", "antistatic_start_sandbox")
	if ExitCode(err) != ExitToolError || err.Error() != "A title is needed." {
		t.Fatalf("tool error: %v", err)
	}
	_, err = e.run(`{"run_id": 7, "entries": [{"type": "action", "text": "In the cave."}]}`, "call", "antistatic_take_turn")
	if ExitCode(err) != ExitToolError || err.Error() != "No such character: In the cave." {
		t.Fatalf("tool error decrypted: %v", err)
	}

	// encrypt again: already unlocked.
	out, err = e.run("", "encrypt")
	if err != nil || !strings.Contains(out, "Already unlocked.") {
		t.Fatalf("encrypt again: %q %v", out, err)
	}

	// lock: locked; call says so (exit 5); unlock with the phrase (a wrong one first).
	if out, err = e.run("", "lock"); err != nil || strings.TrimSpace(out) != "Locked." {
		t.Fatal(out, err)
	}
	if out, _ = e.run("", "status", "--json"); !strings.Contains(out, `"state": "locked"`) {
		t.Fatalf("status: %s", out)
	}
	_, err = e.run(`{"title": "x"}`, "call", "antistatic_start_sandbox")
	if ExitCode(err) != ExitLocked || !strings.Contains(err.Error(), "antistatic encrypt") {
		t.Fatalf("locked call: %v", err)
	}
	other, _ := e2e.GeneratePhrase()
	readSecret = answers(other, strings.ReplaceAll(phrase, " ", "-"))
	if out, err = e.run("", "encrypt"); err != nil || !strings.Contains(out, "Unlocked.") {
		t.Fatalf("unlock: %q %v", out, err)
	}

	// The access token expires: the call refreshes it. A revoked login is exit 3.
	cfgPath := filepath.Join(e.dir, "config.json")
	data, _ := os.ReadFile(cfgPath)
	var saved map[string]any
	json.Unmarshal(data, &saved)
	saved["oauth_token_expiry"] = "2000-01-01T00:00:00Z"
	data, _ = json.Marshal(saved)
	os.WriteFile(cfgPath, data, 0o600)
	if _, err = e.run(`{"title": "x"}`, "call", "antistatic_start_sandbox"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	data, _ = os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "refresh-1r") {
		t.Fatalf("refreshed token not saved: %s", data)
	}
	e.site.access = "other"
	_, err = e.run(`{"title": "x"}`, "call", "antistatic_start_sandbox")
	if ExitCode(err) != ExitNotLoggedIn {
		t.Fatalf("access token refused: %v", err)
	}
	json.Unmarshal(data, &saved)
	saved["oauth_token_expiry"] = "2000-01-01T00:00:00Z"
	data, _ = json.Marshal(saved)
	os.WriteFile(cfgPath, data, 0o600)
	e.site.refreshOK = false
	_, err = e.run(`{"title": "x"}`, "call", "antistatic_start_sandbox")
	if ExitCode(err) != ExitNotLoggedIn {
		t.Fatalf("revoked: %v", err)
	}

	// The login is the server's it was made for: another server gets no token.
	e.site.refreshOK = true
	e.site.access = "access-1rr"
	site2 := newFakeSite(t)
	t.Setenv("ANTISTATIC_URL", site2.server.URL)
	_, err = e.run(`{"title": "x"}`, "call", "antistatic_start_sandbox")
	if ExitCode(err) != ExitNotLoggedIn || len(site2.calls) != 0 {
		t.Fatalf("other server: %v, %d calls", err, len(site2.calls))
	}
	t.Setenv("ANTISTATIC_URL", "http://example.com")
	if _, err = e.run("", "status"); ExitCode(err) != ExitUsage {
		t.Fatalf("plain http: %v", err)
	}
	t.Setenv("ANTISTATIC_URL", e.site.server.URL)

	// Usage errors are exit 2.
	if _, err = e.run("", "call"); ExitCode(err) != ExitUsage {
		t.Fatalf("no tool: %v", err)
	}
	if _, err = e.run("", "call", "antistatic_get_sandbox", "--nope"); ExitCode(err) != ExitUsage {
		t.Fatalf("unknown flag: %v", err)
	}

	// logout locks too.
	e.site.refreshOK = true
	if _, err = e.run("", "logout"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "keys.json")); !os.IsNotExist(err) {
		t.Fatal("keys left after logout")
	}
}

func TestEncryptWrongWordSavesNothing(t *testing.T) {
	e := newCLIEnv(t)
	readLine = answers("wrong", "words")
	_, err := e.run("", "encrypt")
	if err == nil || !strings.Contains(err.Error(), "nothing was saved") {
		t.Fatalf("got %v", err)
	}
	if e.site.setup != nil {
		t.Fatal("setup stored")
	}
	if _, err := os.Stat(filepath.Join(e.dir, "keys.json")); !os.IsNotExist(err) {
		t.Fatal("key saved")
	}
}

func TestEncryptNeedsATerminal(t *testing.T) {
	e := newCLIEnv(t)
	stdinIsTerminal = func() bool { return false }
	_, err := e.run("", "encrypt")
	if ExitCode(err) != ExitUsage || !strings.Contains(err.Error(), "antistatic encrypt") {
		t.Fatalf("got %v", err)
	}
	_, err = e.run(`{}`, "call", "antistatic_list_sandboxes")
	if ExitCode(err) != ExitNotLoggedIn {
		t.Fatalf("got %v", err)
	}
	_ = io.Discard
}
