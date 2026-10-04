package cmd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseItemFlag(t *testing.T) {
	cases := []struct {
		spec    string
		want    map[string]interface{}
		wantErr string
	}{
		{spec: "lab=Labour:#E4003B", want: map[string]interface{}{"key": "lab", "label": "Labour", "color": "#e4003b"}},
		{spec: "lab=Labour", want: map[string]interface{}{"key": "lab", "label": "Labour"}},
		{spec: "con=:0087dc", want: map[string]interface{}{"key": "con", "color": "#0087dc"}},
		{spec: "e1=Vote: first round", want: map[string]interface{}{"key": "e1", "label": "Vote: first round"}},
		{spec: "nokey", wantErr: "expected KEY=LABEL"},
		{spec: "lab=", wantErr: "give a label"},
	}
	for _, tc := range cases {
		got, err := parseItemFlag(tc.spec)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%q: error %v, want %q", tc.spec, err, tc.wantErr)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %v (%v), want %v", tc.spec, got, err, tc.want)
		}
	}
}

func TestMarketEditSendsPatch(t *testing.T) {
	dir := t.TempDir()
	criteria := filepath.Join(dir, "criteria.html")
	if err := os.WriteFile(criteria, []byte("Resolves YES if ..."), 0o600); err != nil {
		t.Fatal(err)
	}
	requests, err := runAgainstServer(t, newMarketEditCmd(),
		[]string{"~12", "--title", "Seats won", "--unit", "MPs", "--item", "lab=Labour:#e4003b", "--resolution-file", criteria},
		func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
			w.Write([]byte(`{"data":{"code":"~12","title":"Seats won"}}`))
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0].method != "PATCH" || requests[0].path != "/api/v1/markets/~12" {
		t.Fatalf("requests = %+v", requests)
	}
	got, _ := json.Marshal(requests[0].body)
	want := `{"items":[{"color":"#e4003b","key":"lab","label":"Labour"}],"resolution_criteria":"Resolves YES if ...","title":"Seats won","unit":"MPs"}`
	if string(got) != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestMarketEditDryRunAndEmpty(t *testing.T) {
	requests, err := runAgainstServer(t, newMarketEditCmd(), []string{"m", "--title", "x", "--dry-run"},
		func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {})
	if err != nil || len(requests) != 0 {
		t.Fatalf("dry run sent %d request(s), err %v", len(requests), err)
	}
	_, err = runAgainstServer(t, newMarketEditCmd(), []string{"m"},
		func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {})
	if err == nil || !strings.Contains(err.Error(), "nothing to change") {
		t.Fatalf("err = %v", err)
	}
}

func TestMarketEditServerRefusal(t *testing.T) {
	_, err := runAgainstServer(t, newMarketEditCmd(), []string{"pub", "--background-file", writeTemp(t, "<p>x</p>")},
		func(w http.ResponseWriter, r *http.Request, body map[string]interface{}) {
			w.WriteHeader(403)
			w.Write([]byte(`{"error":{"message":"Resolution criteria and background can be edited only on private markets; ask an admin.","code":"description_requires_private"}}`))
		})
	if err == nil || !strings.Contains(err.Error(), "only on private markets") {
		t.Fatalf("err = %v", err)
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.html")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
