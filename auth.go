package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LoadCredentials loads persisted credentials from file
func (s *Server) LoadCredentials() error {
	s.credMutex.Lock()
	defer s.credMutex.Unlock()

	data, err := os.ReadFile(s.credFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("ℹ️  Credentials file not found, will create on first authentication: %s", s.credFile)
			return nil
		}
		return fmt.Errorf("failed to read credentials file: %w", err)
	}

	if err := json.Unmarshal(data, s.credentials); err != nil {
		return fmt.Errorf("failed to parse credentials file: %w", err)
	}

	log.Printf("✓ Credentials loaded successfully (expires at: %s)", s.credentials.ExpiresAt.Format(time.RFC3339))
	return nil
}

// SaveCredentials persists credentials to file
func (s *Server) SaveCredentials() error {
	s.credMutex.RLock()
	defer s.credMutex.RUnlock()

	data, err := json.MarshalIndent(s.credentials, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(s.credFile)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create credentials directory: %w", err)
	}

	// Write with restricted permissions for security
	if err := os.WriteFile(s.credFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write credentials file: %w", err)
	}

	log.Printf("✓ Credentials saved to %s", s.credFile)
	return nil
}

// GenerateState creates a random state string for OAuth CSRF protection
func (s *Server) GenerateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	state := base64.URLEncoding.EncodeToString(b)

	s.statesMutex.Lock()
	defer s.statesMutex.Unlock()
	s.authStates[state] = AuthState{State: state, Created: time.Now()}

	// Clean old states (older than 10 minutes)
	for k, v := range s.authStates {
		if time.Since(v.Created) > 10*time.Minute {
			delete(s.authStates, k)
		}
	}

	return state, nil
}

// ValidateState checks if the provided state is valid and removes it
func (s *Server) ValidateState(state string) bool {
	s.statesMutex.Lock()
	defer s.statesMutex.Unlock()

	authState, exists := s.authStates[state]
	if !exists {
		return false
	}

	// Check if state is not too old (10 minutes)
	if time.Since(authState.Created) > 10*time.Minute {
		delete(s.authStates, state)
		return false
	}

	delete(s.authStates, state)
	return true
}

// HandleLogin initiates the OAuth flow
func (s *Server) HandleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := s.GenerateState()
	if err != nil {
		log.Printf("❌ Failed to generate state: %v", err)
		http.Error(w, "Failed to generate state", http.StatusInternalServerError)
		return
	}

	// Redirect to Joystick TV OAuth authorization endpoint
	authURL := fmt.Sprintf(
		"https://joystick.tv/api/oauth/authorize?client_id=%s&redirect_uri=%s&state=%s&response_type=code&scope=bot",
		s.clientID,
		s.redirectURL,
		state,
	)

	log.Printf("ℹ️  Redirecting to authorization endpoint with state: %s", state)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

// HandleCallback handles the OAuth callback from Joystick TV
func (s *Server) HandleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" {
		log.Printf("❌ Missing authorization code in callback")
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}

	if state == "" || !s.ValidateState(state) {
		log.Printf("❌ Invalid or missing state in callback")
		http.Error(w, "Invalid state parameter", http.StatusBadRequest)
		return
	}

	log.Printf("ℹ️  Received authorization code, exchanging for access token...")

	// Exchange authorization code for access token
	if err := s.ExchangeCodeForToken(code); err != nil {
		log.Printf("❌ Failed to exchange code for token: %v", err)
		http.Error(w, fmt.Sprintf("Failed to authenticate: %v", err), http.StatusInternalServerError)
		return
	}

	// Save credentials to file
	if err := s.SaveCredentials(); err != nil {
		log.Printf("⚠️  Credentials received but failed to persist: %v", err)
	}

	// Start WebSocket connection in background
	go func() {
		time.Sleep(1 * time.Second) // Give user time to see success page
		if err := s.ConnectToWebSocket(); err != nil {
			log.Printf("❌ WebSocket connection error: %v", err)
		}
	}()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `
		<!DOCTYPE html>
		<html>
		<head>
			<title>Authentication Successful</title>
			<style>
				body { font-family: Arial, sans-serif; margin: 50px; }
				.success { color: #27ae60; font-size: 24px; }
			</style>
		</head>
		<body>
			<div class="success">✓ Authentication Successful!</div>
			<p>Your credentials have been saved and will persist across service restarts.</p>
			<p>The bot is now listening for events. Check the server logs for incoming events.</p>
			<p><a href="/status">View Status</a></p>
		</body>
		</html>
	`)
}

// ExchangeCodeForToken exchanges an authorization code for an access token
func (s *Server) ExchangeCodeForToken(code string) error {
	basicAuth := base64.StdEncoding.EncodeToString(
		[]byte(s.clientID + ":" + s.clientSecret),
	)

	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", s.redirectURL)
	reqBody := data.Encode()

	req, err := http.NewRequest(
		"POST",
		"https://joystick.tv/api/oauth/token",
		strings.NewReader(reqBody),
	)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Basic "+basicAuth)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to request token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("token request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		TokenType    string `json:"token_type"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("failed to parse token response: %w", err)
	}

	s.credMutex.Lock()
	defer s.credMutex.Unlock()

	s.credentials.AccessToken = tokenResp.AccessToken
	s.credentials.RefreshToken = tokenResp.RefreshToken
	s.credentials.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	s.credentials.ClientID = s.clientID
	s.credentials.ClientSecret = s.clientSecret

	log.Printf("✓ Access token obtained, expires at: %s", s.credentials.ExpiresAt.Format(time.RFC3339))
	return nil
}

// HandleStatus returns the current authentication status
func (s *Server) HandleStatus(w http.ResponseWriter, r *http.Request) {
	s.credMutex.RLock()
	defer s.credMutex.RUnlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	isAuthenticated := s.credentials.AccessToken != ""
	isExpired := !s.credentials.ExpiresAt.IsZero() && time.Now().After(s.credentials.ExpiresAt)

	statusHTML := `
		<!DOCTYPE html>
		<html>
		<head>
			<title>Authentication Status</title>
			<style>
				body { font-family: Arial, sans-serif; margin: 50px; }
				.authenticated { color: #27ae60; }
				.expired { color: #e74c3c; }
				.status-box { border: 1px solid #ccc; padding: 20px; border-radius: 5px; margin: 20px 0; }
				a { color: #3498db; text-decoration: none; }
				a:hover { text-decoration: underline; }
			</style>
		</head>
		<body>
			<h1>Joystick TV Authentication Status</h1>
			<div class="status-box">
	`

	if !isAuthenticated {
		statusHTML += `
			<p><strong>Status:</strong> <span style="color: #95a5a6;">Not Authenticated</span></p>
			<p><a href="/login">Click here to authenticate</a></p>
		`
	} else if isExpired {
		statusHTML += `
			<p><strong>Status:</strong> <span class="expired">Token Expired</span></p>
			<p>Access token expired at: ` + s.credentials.ExpiresAt.Format(time.RFC3339) + `</p>
			<p><a href="/login">Re-authenticate</a></p>
		`
	} else {
		statusHTML += `
			<p><strong>Status:</strong> <span class="authenticated">✓ Authenticated</span></p>
			<p><strong>Expires At:</strong> ` + s.credentials.ExpiresAt.Format(time.RFC3339) + `</p>
			<p><strong>Client ID:</strong> ` + maskString(s.credentials.ClientID) + `</p>
		`
	}

	statusHTML += `
			</div>
		</body>
		</html>
	`

	fmt.Fprint(w, statusHTML)
}

// maskString masks a string for display (shows first 4 and last 4 chars)
func maskString(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}
