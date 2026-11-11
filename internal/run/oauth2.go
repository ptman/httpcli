package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"httpcli/internal/vars"
)

// token fetches and caches a client-credentials access token.
func (r *Runner) token(ctx context.Context, scope *vars.Scope) (string, error) {
	endpoint := lookupString(scope, "oauth2_tokenEndpoint")
	if endpoint == "" {
		return "", errors.New("oauth2: missing variable oauth2_tokenEndpoint")
	}
	clientID := lookupString(scope, "oauth2_clientId")
	if clientID == "" {
		return "", errors.New("oauth2: missing variable oauth2_clientId")
	}
	secret := lookupString(scope, "oauth2_clientSecret")
	scopeName := lookupString(scope, "oauth2_scope")

	// The token depends on the credentials and the requested scope, so all of
	// them are part of the cache key. Tokens live for the whole run; refresh is
	// not implemented.
	key := strings.Join([]string{endpoint, clientID, secret, scopeName}, "\x00")
	if token, ok := r.tokens[key]; ok {
		return token, nil
	}

	form := url.Values{"grant_type": {"client_credentials"}}
	if scopeName != "" {
		form.Set("scope", scopeName)
	}
	if secret == "" {
		form.Set("client_id", clientID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("oauth2: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if secret != "" {
		req.SetBasicAuth(clientID, secret)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth2 token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("oauth2 token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("oauth2 token endpoint returned %s: %s", resp.Status, snippet(body))
	}

	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("oauth2 token response: %w", err)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("oauth2 token response has no access_token: %s", snippet(body))
	}

	r.tokens[key] = payload.AccessToken
	return payload.AccessToken, nil
}

func lookupString(scope *vars.Scope, name string) string {
	value, ok := scope.Lookup(name)
	if !ok {
		return ""
	}
	return vars.Format(value)
}

func snippet(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 200 {
		return text[:200] + "..."
	}
	return text
}
