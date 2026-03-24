package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
)

// IsTTY returns true if stdout is a terminal (not piped/redirected).
func IsTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// JSON prints the raw JSON data, pretty-printed.
func JSON(data json.RawMessage) {
	var pretty json.RawMessage
	if err := json.Unmarshal(data, &pretty); err != nil {
		fmt.Fprintln(os.Stderr, "Error formatting JSON:", err)
		os.Stdout.Write(data)
		fmt.Println()
		return
	}
	out, _ := json.MarshalIndent(pretty, "", "  ")
	os.Stdout.Write(out)
	fmt.Println()
}

// Table prints rows in an aligned table format.
func Table(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	fmt.Fprintln(w, strings.Repeat("─\t", len(headers)))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	w.Flush()
}

// KeyValue prints a list of key-value pairs.
func KeyValue(pairs [][2]string) {
	maxKey := 0
	for _, p := range pairs {
		if len(p[0]) > maxKey {
			maxKey = len(p[0])
		}
	}
	for _, p := range pairs {
		fmt.Printf("%-*s  %s\n", maxKey, p[0]+":", p[1])
	}
}

// Warn prints a warning to stderr.
func Warn(msg string) {
	fmt.Fprintf(os.Stderr, "Warning: %s\n", msg)
}

// Error prints an error to stderr.
func Error(msg string) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
}
