package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/finnhambly/antistatic-cli/internal/config"
)

// Client is a thin wrapper around the Antistatic REST API.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates an API client from the loaded config.
func NewClient(cfg *config.Config) *Client {
	return &Client{
		baseURL: strings.TrimRight(cfg.ResolveBaseURL(), "/"),
		token:   cfg.ResolveToken(),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// HasAuth returns true if a token is configured.
func (c *Client) HasAuth() bool {
	return c.token != ""
}

// APIError represents a structured error from the API.
type APIError struct {
	StatusCode int
	Message    string
	Code       string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s (%s)", e.Message, e.Code)
	}
	return e.Message
}

// Response wraps a successful API response with the raw JSON body.
type Response struct {
	StatusCode int
	Body       []byte
}

// Data returns the parsed "data" field from the response.
func (r *Response) Data() (json.RawMessage, error) {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(r.Body, &envelope); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return envelope.Data, nil
}

// DataInto unmarshals the "data" field into the given target.
func (r *Response) DataInto(v interface{}) error {
	data, err := r.Data()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// RawInto unmarshals the entire response body into the given target.
func (r *Response) RawInto(v interface{}) error {
	return json.Unmarshal(r.Body, v)
}

// Get performs a GET request to the given API path with optional query params.
func (c *Client) Get(path string, query url.Values) (*Response, error) {
	return c.do("GET", path, query, nil)
}

// Post performs a POST request with a JSON body.
func (c *Client) Post(path string, body interface{}) (*Response, error) {
	return c.do("POST", path, nil, body)
}

// Put performs a PUT request with a JSON body.
func (c *Client) Put(path string, body interface{}) (*Response, error) {
	return c.do("PUT", path, nil, body)
}

// Delete performs a DELETE request.
func (c *Client) Delete(path string) (*Response, error) {
	return c.do("DELETE", path, nil, nil)
}

func (c *Client) do(method, path string, query url.Values, body interface{}) (*Response, error) {
	u := c.baseURL + "/api/v1" + path
	if query != nil && len(query) > 0 {
		u += "?" + query.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request body: %w", err)
		}
		bodyReader = strings.NewReader(string(data))
	}

	req, err := http.NewRequest(method, u, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode >= 400 {
		apiErr := &APIError{StatusCode: resp.StatusCode}

		var errResp struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
			apiErr.Message = errResp.Error.Message
			apiErr.Code = errResp.Error.Code
		} else {
			apiErr.Message = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		}

		return nil, apiErr
	}

	return &Response{StatusCode: resp.StatusCode, Body: respBody}, nil
}
