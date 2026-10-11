package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/finnhambly/antistatic-cli/internal/api"
	"github.com/finnhambly/antistatic-cli/internal/config"
)

const testLimitsJSON = `{"data":{"schema_versions":[2],
 "manifest":{"max_part_bytes":2000000,"max_top_bytes":6000000,"max_parts":400,"max_items":1000,
   "max_checkpoints":5000,"max_bars":300000,"max_bars_per_item":3000,"max_bars_per_part":8},
 "fields":{"item.title":{"max":255},"curriculum.slug":{"min":2,"max":80,"pattern":"^[a-z0-9][a-z0-9_-]*$"}},
 "rules":[{"id":"T5","severity":"warning","summary":"hindsight"}],
 "hindsight_terms":["seminal"]}}`

// writeTestManifest writes a 6-item manifest (4 bars each, so 3 parts at 8
// bars per part) whose outcomes include a sealed string.
func writeTestManifest(t *testing.T, dirty bool) string {
	t.Helper()
	var items, disclosures []interface{}
	for i := 0; i < 6; i++ {
		key := fmt.Sprintf("b%d", i)
		items = append(items, map[string]interface{}{
			"key": key, "title": "Item " + key,
			"market": map[string]interface{}{"code": "q-" + key, "title": "Q " + key},
			"curves": []interface{}{map[string]interface{}{
				"keys": []string{key + "-1", key + "-2", key + "-3", key + "-4"}, "thresholds": []string{"2001-01-01", "2002-01-01", "2003-01-01", "2004-01-01"},
				"p0": []float64{0.1, 0.2, 0.3, 0.4}, "closes_at": "cp1", "outcomes": []interface{}{true, false, nil, "SEALED-OUTCOME"},
			}},
		})
		disclosures = append(disclosures, map[string]interface{}{"key": "d" + key, "item": key, "title": "News", "body_html": "<p>SEALED-NOTE</p>"})
	}
	manifest := map[string]interface{}{
		"schema_version": 2,
		"source":         map[string]interface{}{"commit": "abc123", "dirty": dirty},
		"curriculum":     map[string]interface{}{"title": "Widgets"},
		"checkpoints":    []interface{}{map[string]interface{}{"key": "cp1", "title": "One"}},
		"disclosures":    disclosures,
		"items":          items,
	}
	data, _ := json.Marshal(manifest)
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeSandbox is an in-memory server for one draft.
type fakeSandbox struct {
	mu          sync.Mutex
	t           *testing.T
	parts       map[string]bool
	manifestSHA *string
	state       string
	expected    int
	imported    int
	putParts    []string // sha of each part PUT, in order
	putManifest int
	failPartAt  int // fail the nth part PUT (1-based) with a 500; 0 = never
	hideFirst   bool
	firstPart   string
	limitsHits  int
}

func (f *fakeSandbox) status() map[string]interface{} {
	received := []string{}
	for sha := range f.parts {
		if f.hideFirst && sha == f.firstPart {
			continue
		}
		received = append(received, sha)
	}
	return map[string]interface{}{
		"slug": "widgets", "version": "1.1", "state": f.state, "manifest_sha256": f.manifestSHA,
		"source_commit": "abc123", "schema_version": 2, "parts_expected": f.expected,
		"parts_received": len(f.parts), "parts_imported": f.imported, "received": received, "error": nil,
	}
}

func (f *fakeSandbox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.t
	w.Header().Set("Content-Type", "application/json")
	reply := func(status int, data interface{}) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]interface{}{"data": data})
	}
	fail := func(status int, code string, extra map[string]interface{}) {
		e := map[string]interface{}{"code": code, "message": "nope"}
		for k, v := range extra {
			e[k] = v
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": e})
	}
	const prefix = "/api/v1/sandbox/curricula/widgets/drafts/1.1"
	body, _ := io.ReadAll(r.Body)
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/v1/sandbox/limits":
		f.limitsHits++
		io.WriteString(w, testLimitsJSON)
	case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, prefix+"/parts/"):
		sha := strings.TrimPrefix(r.URL.Path, prefix+"/parts/")
		f.putParts = append(f.putParts, sha)
		if f.failPartAt > 0 && len(f.putParts) == f.failPartAt {
			fail(500, "internal", nil)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("part Content-Type = %q", r.Header.Get("Content-Type"))
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != sha {
			fail(422, "sha256_mismatch", nil)
			return
		}
		if f.state == "" {
			f.state = "receiving"
		}
		if f.firstPart == "" {
			f.firstPart = sha
		}
		status := 201
		if f.parts[sha] {
			status = 200
		}
		f.parts[sha] = true
		reply(status, map[string]interface{}{"stored": status == 201, "sha256": sha, "items": 2, "bars": 8, "bytes": len(body)})
	case r.Method == "GET" && r.URL.Path == prefix+"/status":
		if f.state == "" {
			fail(404, "not_found", nil)
			return
		}
		if f.state == "importing" {
			f.imported++
			if f.imported >= f.expected {
				f.state = "imported"
			}
		}
		reply(200, f.status())
	case r.Method == "PUT" && r.URL.Path == prefix+"/manifest":
		f.putManifest++
		var m struct {
			ManifestSHA256 string  `json:"manifest_sha256"`
			BaseSHA256     *string `json:"base_sha256"`
			Parts          []string
			Top            map[string]interface{}
			Source         map[string]interface{}
		}
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatal(err)
		}
		if (m.BaseSHA256 == nil) != (f.manifestSHA == nil) || (m.BaseSHA256 != nil && *m.BaseSHA256 != *f.manifestSHA) {
			fail(409, "draft_changed", nil)
			return
		}
		if m.Source["commit"] != "abc123" || len(m.Top["checkpoints"].([]interface{})) != 1 {
			t.Errorf("manifest body: source %v", m.Source)
		}
		var missing []string
		for _, sha := range m.Parts {
			if !f.parts[sha] {
				missing = append(missing, sha)
			}
		}
		if len(missing) > 0 {
			fail(422, "missing_parts", map[string]interface{}{"parts": missing})
			return
		}
		if f.manifestSHA != nil && *f.manifestSHA == m.ManifestSHA256 && f.state != "failed" {
			reply(200, f.status())
			return
		}
		sha := m.ManifestSHA256
		f.manifestSHA, f.state, f.expected, f.imported = &sha, "importing", len(m.Parts), 0
		reply(202, f.status())
	case r.Method == "POST" && r.URL.Path == prefix+"/validate":
		if f.state != "imported" {
			fail(409, "not_imported", nil)
			return
		}
		reply(200, map[string]interface{}{"valid": true, "errors": []string{}, "warnings": []string{"items[b1]: something"},
			"unresolved_past_bar_count": 3, "lint": []interface{}{map[string]interface{}{"rule": "T5", "where": "items[b1].title", "term": "seminal"}},
			"lint_counts": map[string]int{"T5": 1}})
	default:
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}
}

func runSandbox(t *testing.T, server *httptest.Server, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("ANTISTATIC_URL", server.URL)
	t.Setenv("ANTISTATIC_TOKEN", "test-token")
	previous := client
	client = api.NewClient(&config.Config{})
	defer func() { client = previous }()
	var progress bytes.Buffer
	prevOut := progressOut
	progressOut = &progress
	defer func() { progressOut = prevOut }()
	command := newSandboxCmd("sandbox", false)
	command.SilenceErrors, command.SilenceUsage = true, true
	command.SetArgs(args)
	var err error
	out := captureStdout(t, func() { err = command.Execute() })
	return out, progress.String(), err
}

func setupSandboxTest(t *testing.T) (*fakeSandbox, *httptest.Server) {
	cacheDir := t.TempDir()
	prevDir, prevPoll := limitsCacheDir, sandboxPollInterval
	limitsCacheDir = func() (string, error) { return cacheDir, nil }
	sandboxPollInterval = time.Millisecond
	t.Cleanup(func() { limitsCacheDir, sandboxPollInterval = prevDir, prevPoll })
	fake := &fakeSandbox{t: t, parts: map[string]bool{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return fake, server
}

func TestSandboxPushResumes(t *testing.T) {
	fake, server := setupSandboxTest(t)
	path := writeTestManifest(t, false)
	push := []string{"push", path, "--curriculum", "widgets", "--version", "1.1"}

	// Run 1 fails on the second part.
	fake.failPartAt = 2
	_, _, err := runSandbox(t, server, push...)
	if err == nil || !strings.Contains(err.Error(), "part 2/3") {
		t.Fatalf("run 1 err = %v", err)
	}
	if len(fake.putParts) != 2 || fake.putManifest != 0 {
		t.Fatalf("run 1: %d part PUTs, %d manifest PUTs", len(fake.putParts), fake.putManifest)
	}
	firstRun := append([]string(nil), fake.putParts...)

	// Run 2 resumes: only the two parts not stored, then manifest, import, validate.
	fake.failPartAt = 0
	fake.putParts = nil
	out, progress, err := runSandbox(t, server, push...)
	if err != nil {
		t.Fatalf("run 2: %v\n%s", err, progress)
	}
	if len(fake.putParts) != 2 || fake.putParts[0] != firstRun[1] {
		t.Fatalf("run 2 PUT parts %v (run 1 PUT %v)", fake.putParts, firstRun)
	}
	if fake.putManifest != 1 || fake.state != "imported" {
		t.Fatalf("run 2: %d manifest PUTs, state %s", fake.putManifest, fake.state)
	}
	if !strings.Contains(progress, "3/3 parts imported") || !strings.Contains(progress, "1 already stored") {
		t.Errorf("progress = %s", progress)
	}
	var result struct {
		Status     sandboxStatus
		Validation sandboxValidation
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("output = %s", out)
	}
	if !result.Validation.Valid || result.Validation.UnresolvedPastBarCount != 3 || result.Status.State != "imported" {
		t.Errorf("result = %+v", result)
	}
	if limitsHits := fake.limitsHits; limitsHits != 1 {
		t.Errorf("limits fetched %d times; the cache should serve the second run", limitsHits)
	}

	// Run 3: already imported, so nothing is PUT.
	fake.putParts = nil
	_, progress, err = runSandbox(t, server, push...)
	if err != nil || len(fake.putParts) != 0 || fake.putManifest != 1 || !strings.Contains(progress, "already has this manifest") {
		t.Fatalf("run 3: err %v, %d parts, %d manifests\n%s", err, len(fake.putParts), fake.putManifest, progress)
	}

	for _, s := range []string{out, progress} {
		if strings.Contains(s, "SEALED") {
			t.Errorf("output leaks a sealed value:\n%s", s)
		}
	}
}

func TestSandboxPushResendsMissingParts(t *testing.T) {
	fake, server := setupSandboxTest(t)
	path := writeTestManifest(t, false)
	push := []string{"push", path, "--curriculum", "widgets", "--version", "1.1"}
	fake.failPartAt = 2
	runSandbox(t, server, push...)

	// The status hides a stored part, so it is re-sent (200, stored:false);
	// then the server drops another, and the manifest PUT's missing_parts
	// gets it sent again.
	fake.failPartAt = 0
	fake.hideFirst = true
	fake.putParts = nil
	_, progress, err := runSandbox(t, server, push...)
	if err != nil {
		t.Fatalf("%v\n%s", err, progress)
	}
	if len(fake.putParts) != 3 {
		t.Errorf("PUT parts = %d", len(fake.putParts))
	}

	// A draft that loses a part between the PUTs and the manifest.
	fake2, _ := setupSandboxTest(t)
	path2 := writeTestManifest(t, false)
	dropped := false
	wrapped := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/manifest") && !dropped {
			fake2.mu.Lock()
			for sha := range fake2.parts {
				delete(fake2.parts, sha)
				break
			}
			fake2.mu.Unlock()
			dropped = true
		}
		fake2.ServeHTTP(w, r)
	}))
	defer wrapped.Close()
	_, progress, err = runSandbox(t, wrapped, "push", path2, "--curriculum", "widgets", "--version", "1.1")
	if err != nil {
		t.Fatalf("%v\n%s", err, progress)
	}
	if fake2.putManifest != 2 || len(fake2.putParts) != 4 || !strings.Contains(progress, "missing 1 part") {
		t.Errorf("manifest PUTs %d, part PUTs %d\n%s", fake2.putManifest, len(fake2.putParts), progress)
	}
}

func TestSandboxPushRefusesDirty(t *testing.T) {
	_, server := setupSandboxTest(t)
	path := writeTestManifest(t, true)
	_, _, err := runSandbox(t, server, "push", path, "--curriculum", "widgets", "--version", "1.1")
	if err == nil || !strings.Contains(err.Error(), "--allow-dirty") {
		t.Fatalf("err = %v", err)
	}
	_, _, err = runSandbox(t, server, "push", path, "--curriculum", "widgets", "--version", "1")
	if err == nil {
		t.Fatal("a bad --version was accepted")
	}
}

func TestSandboxCheckCommand(t *testing.T) {
	_, server := setupSandboxTest(t)
	path := writeTestManifest(t, false)
	out, _, err := runSandbox(t, server, "check", path, "--curriculum", "Bad Slug")
	if err == nil {
		t.Fatal("expected an error exit")
	}
	var report struct {
		Counts map[string]int
		Errors []map[string]string
	}
	if json.Unmarshal([]byte(out), &report) != nil || report.Counts["parts"] != 3 || report.Counts["bars"] != 24 {
		t.Fatalf("output = %s", out)
	}
	if len(report.Errors) != 1 || report.Errors[0]["path"] != "--curriculum" || report.Errors[0]["code"] != "pattern" {
		t.Errorf("errors = %v", report.Errors)
	}
	if strings.Contains(out, "SEALED") || strings.Contains(out, "Bad Slug") {
		t.Errorf("output leaks a value: %s", out)
	}
}

func TestSandboxLimitsFallBackToStaleCache(t *testing.T) {
	_, server := setupSandboxTest(t)
	path := writeTestManifest(t, false)
	if _, _, err := runSandbox(t, server, "check", path); err != nil {
		t.Fatal(err)
	}
	dir, _ := limitsCacheDir()
	files, _ := filepath.Glob(filepath.Join(dir, "limits-*.json"))
	if len(files) != 1 {
		t.Fatalf("cache files = %v", files)
	}
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(files[0], old, old)
	server.Close()
	_, progress, err := runSandbox(t, server, "check", path)
	if err != nil || !strings.Contains(progress, "cached copy") {
		t.Fatalf("err %v, progress %s", err, progress)
	}
	os.Remove(files[0])
	if _, _, err := runSandbox(t, server, "check", path); err == nil {
		t.Fatal("expected an error with no cache and no server")
	}
}
