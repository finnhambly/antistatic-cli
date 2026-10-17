package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrLoginRequired means there is no usable login: none saved, or the
// server refused it (expired or revoked).
var ErrLoginRequired = errors.New("not logged in")

// MCPTimeout bounds one tool call; a turn's planning can take minutes.
var MCPTimeout = 10 * time.Minute

// MCPResult is a tools/call result.
type MCPResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

// Text is the result's text content.
func (r *MCPResult) Text() string {
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// CallTool calls an MCP tool at /mcp with the saved login. args is the
// arguments object, already encoded.
func (c *Client) CallTool(tool string, args []byte) (*MCPResult, error) {
	if err := c.ensureToken(); err != nil {
		return nil, err
	}
	if c.token == "" {
		return nil, ErrLoginRequired
	}
	if len(args) == 0 {
		args = []byte("{}")
	}
	name, _ := json.Marshal(tool)
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%s,"arguments":%s}}`, name, args)

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/mcp", bytes.NewReader([]byte(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", c.UserAgent())

	resp, err := NoRedirectClient(MCPTimeout).Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: request to %s failed: %w", tool, c.baseURL, err)
	}
	defer resp.Body.Close()
	if err := redirectError(resp); err != nil {
		return nil, fmt.Errorf("%s: %w", tool, err)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: reading response: %w", tool, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrLoginRequired
	}

	var envelope struct {
		Result *MCPResult `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
		OAuthError string `json:"error_description"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("%s: HTTP %d: %s", tool, resp.StatusCode, truncate(string(data), 300))
	}
	if resp.StatusCode >= 400 {
		msg := envelope.OAuthError
		if msg == "" {
			msg = truncate(string(data), 300)
		}
		return nil, fmt.Errorf("%s: HTTP %d: %s", tool, resp.StatusCode, msg)
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("%s: %s", tool, envelope.Error.Message)
	}
	if envelope.Result == nil {
		return nil, fmt.Errorf("%s: empty result", tool)
	}
	return envelope.Result, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
