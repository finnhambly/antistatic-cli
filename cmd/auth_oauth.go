package cmd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/finnhambly/antistatic-cli/internal/output"
	"github.com/spf13/cobra"
)

const (
	oauthDefaultScope    = "read write comment"
	oauthCallbackPath    = "/callback"
	oauthLoginTimeout    = 5 * time.Minute
	oauthHTTPTimeout     = 20 * time.Second
	oauthClientName      = "Antistatic CLI"
	oauthTokenSafetySkew = 30 * time.Second
)

type oauthRegistrationResponse struct {
	ClientID string `json:"client_id"`
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

type oauthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type oauthCallbackResult struct {
	Code             string
	State            string
	Error            string
	ErrorDescription string
}

func runOAuthBrowserLogin(cmd *cobra.Command) error {
	baseURL := strings.TrimRight(cfg.ResolveBaseURL(), "/")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("starting OAuth callback listener: %w", err)
	}

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		listener.Close()
		return fmt.Errorf("unexpected callback listener address type: %T", listener.Addr())
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", addr.Port, oauthCallbackPath)

	registration, err := registerOAuthClient(baseURL, redirectURI)
	if err != nil {
		listener.Close()
		return err
	}

	state, err := randomURLSafe(24)
	if err != nil {
		listener.Close()
		return fmt.Errorf("creating OAuth state: %w", err)
	}

	codeVerifier, err := randomURLSafe(64)
	if err != nil {
		listener.Close()
		return fmt.Errorf("creating PKCE verifier: %w", err)
	}

	codeChallenge := pkceChallenge(codeVerifier)
	authURL := buildAuthorizeURL(baseURL, registration.ClientID, redirectURI, state, codeChallenge)

	resultCh := make(chan oauthCallbackResult, 1)
	httpServer := &http.Server{
		Handler: callbackHandler(resultCh),
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- httpServer.Serve(listener)
	}()

	noBrowser, _ := cmd.Flags().GetBool("no-browser")
	if noBrowser {
		fmt.Println("Open this URL to continue login:")
		fmt.Println(authURL)
	} else {
		if err := openBrowser(authURL); err != nil {
			output.Warn(fmt.Sprintf("Could not open browser automatically: %v", err))
			fmt.Println("Open this URL to continue login:")
			fmt.Println(authURL)
		} else {
			fmt.Println("Opened browser for OAuth login.")
			fmt.Println("If your browser did not open, use this URL:")
			fmt.Println(authURL)
		}
	}

	fmt.Println("Waiting for OAuth callback...")

	var callback oauthCallbackResult
	select {
	case callback = <-resultCh:
	case <-time.After(oauthLoginTimeout):
		_ = shutdownHTTPServer(httpServer)
		<-serverDone
		return fmt.Errorf("timed out waiting for OAuth callback")
	}

	_ = shutdownHTTPServer(httpServer)
	serveErr := <-serverDone
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("OAuth callback server failed: %w", serveErr)
	}

	if callback.Error != "" {
		if callback.ErrorDescription != "" {
			return fmt.Errorf("authorization failed: %s (%s)", callback.ErrorDescription, callback.Error)
		}
		return fmt.Errorf("authorization failed: %s", callback.Error)
	}

	if callback.State == "" || callback.State != state {
		return fmt.Errorf("authorization failed: state mismatch")
	}

	if callback.Code == "" {
		return fmt.Errorf("authorization failed: missing code")
	}

	tokenPayload, err := exchangeOAuthCode(
		baseURL,
		registration.ClientID,
		redirectURI,
		callback.Code,
		codeVerifier,
	)
	if err != nil {
		return err
	}

	if tokenPayload.AccessToken == "" {
		return fmt.Errorf("token exchange succeeded but no access_token was returned")
	}

	if tokenPayload.RefreshToken == "" {
		output.Warn("OAuth response did not include a refresh token; you may need to log in again when the access token expires.")
	}

	cfg.Token = tokenPayload.AccessToken
	cfg.OAuthClientID = registration.ClientID
	cfg.OAuthRefreshToken = tokenPayload.RefreshToken
	cfg.OAuthTokenExpiry = tokenExpiryString(tokenPayload.ExpiresIn)
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Println("Logged in with OAuth.")
	return nil
}

func registerOAuthClient(baseURL, redirectURI string) (*oauthRegistrationResponse, error) {
	payload := map[string]any{
		"client_name":                oauthClientName,
		"redirect_uris":              []string{redirectURI},
		"token_endpoint_auth_method": "none",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding OAuth registration payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/oauth/register", strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("creating OAuth registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: oauthHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registering OAuth client: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading OAuth registration response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("registering OAuth client failed: %s", oauthHTTPError(resp.StatusCode, body))
	}

	var out oauthRegistrationResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parsing OAuth registration response: %w", err)
	}
	if out.ClientID == "" {
		return nil, fmt.Errorf("OAuth registration response missing client_id")
	}

	return &out, nil
}

func exchangeOAuthCode(baseURL, clientID, redirectURI, code, codeVerifier string) (*oauthTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", clientID)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", codeVerifier)

	req, err := http.NewRequest(http.MethodPost, baseURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: oauthHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchanging OAuth code: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("OAuth token exchange failed: %s", oauthHTTPError(resp.StatusCode, body))
	}

	var out oauthTokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}

	return &out, nil
}

func callbackHandler(resultCh chan<- oauthCallbackResult) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(oauthCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		result := oauthCallbackResult{
			Code:             q.Get("code"),
			State:            q.Get("state"),
			Error:            q.Get("error"),
			ErrorDescription: q.Get("error_description"),
		}

		select {
		case resultCh <- result:
		default:
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if result.Error != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, "<html><body><h2>Authentication failed.</h2><p>You can close this tab and return to the terminal.</p></body></html>")
			return
		}
		_, _ = fmt.Fprint(w, "<html><body><h2>Authentication complete.</h2><p>You can close this tab and return to the terminal.</p></body></html>")
	})
	return mux
}

func buildAuthorizeURL(baseURL, clientID, redirectURI, state, codeChallenge string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", oauthDefaultScope)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")

	return baseURL + "/oauth/authorize?" + q.Encode()
}

func tokenExpiryString(expiresIn int) string {
	if expiresIn <= 0 {
		return ""
	}
	return time.Now().UTC().Add(time.Duration(expiresIn)*time.Second - oauthTokenSafetySkew).Format(time.RFC3339)
}

func randomURLSafe(byteCount int) (string, error) {
	buf := make([]byte, byteCount)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func openBrowser(targetURL string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", targetURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}

	return cmd.Start()
}

func shutdownHTTPServer(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

func oauthHTTPError(statusCode int, body []byte) string {
	var oauthErr oauthErrorResponse
	if err := json.Unmarshal(body, &oauthErr); err == nil && oauthErr.Error != "" {
		if oauthErr.ErrorDescription != "" {
			return fmt.Sprintf("%s (%s)", oauthErr.ErrorDescription, oauthErr.Error)
		}
		return oauthErr.Error
	}

	return fmt.Sprintf("HTTP %d", statusCode)
}
