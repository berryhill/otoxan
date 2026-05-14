// Package secrets provides a client for fetching secrets from Infisical.
// It is the only package that ever holds the long-lived admin credential;
// all other packages receive short-lived bundles via Xander.
package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Client talks to the Infisical REST API.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client

	// Admin credentials — the single long-lived secret on this machine.
	ClientID     string
	ClientSecret string

	// tokenCache holds the last fetched access token and its expiry.
	tokenCache   string
	tokenExpires time.Time
}

// NewClient builds a Client with the given admin credentials.
func NewClient(baseURL, clientID, clientSecret string) *Client {
	if baseURL == "" {
		baseURL = "https://app.infisical.com"
	}
	return &Client{
		BaseURL:      baseURL,
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}
}

// tokenResponse is the JSON returned by Infisical's OAuth token endpoint.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// fetchAccessToken exchanges client_id+client_secret for a short-lived Bearer token.
// It caches the token and reuses it until 30 seconds before expiry.
func (c *Client) fetchAccessToken(ctx context.Context) (string, error) {
	// Return cached token if it is still valid with a 30-second safety margin.
	if c.tokenCache != "" && time.Now().Before(c.tokenExpires.Add(-30*time.Second)) {
		return c.tokenCache, nil
	}

	if c.ClientID == "" || c.ClientSecret == "" {
		return "", fmt.Errorf("infisical: client_id and client_secret required")
	}

	data := url.Values{}
	data.Set("client_id", c.ClientID)
	data.Set("client_secret", c.ClientSecret)
	data.Set("grant_type", "client_credentials")

	reqURL := c.BaseURL + "/api/v1/auth/token/oauth"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("infisical token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("infisical token fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("infisical token endpoint returned %d", resp.StatusCode)
	}

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("infisical token decode: %w", err)
	}

	if tr.AccessToken == "" {
		return "", fmt.Errorf("infisical token response missing access_token")
	}

	c.tokenCache = tr.AccessToken
	c.tokenExpires = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return tr.AccessToken, nil
}

// rawSecretResponse is the JSON shape of /api/v3/secrets/raw/{name}.
type rawSecretResponse struct {
	Secret struct {
		SecretValue string `json:"secretValue"`
	} `json:"secret"`
}

// Get fetches a raw secret by name, scoped to workspaceId and environment.
// It first obtains an access token via client_id+client_secret, then calls
// the secrets endpoint.
func (c *Client) Get(ctx context.Context, name, workspaceID, environment string) (string, error) {
	if workspaceID == "" {
		return "", fmt.Errorf("infisical: workspaceID required")
	}
	if environment == "" {
		environment = "dev"
	}

	token, err := c.fetchAccessToken(ctx)
	if err != nil {
		return "", err
	}

	u, err := url.Parse(c.BaseURL + "/api/v3/secrets/raw/" + url.PathEscape(name))
	if err != nil {
		return "", fmt.Errorf("infisical: bad URL: %w", err)
	}
	q := u.Query()
	q.Set("workspaceId", workspaceID)
	q.Set("environment", environment)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("infisical secret request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("infisical secret fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("infisical secret endpoint returned %d", resp.StatusCode)
	}

	var payload rawSecretResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("infisical secret decode: %w", err)
	}

	return payload.Secret.SecretValue, nil
}
