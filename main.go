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
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Credentials stores the OAuth token information
type Credentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret"`
}

// AuthState stores temporary OAuth state for CSRF protection
type AuthState struct {
	State   string
	Created time.Time
}

// Server holds the web server configuration
type Server struct {
	clientID     string
	clientSecret string
	redirectURL  string
	credFile     string
	credentials  *Credentials
	credMutex    sync.RWMutex
	authStates   map[string]AuthState
	statesMutex  sync.RWMutex
}

// NewServer creates a new server instance
func NewServer(clientID, clientSecret, redirectURL, credFile string) *Server {
	return &Server{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  redirectURL,
		credFile:     credFile,
		credentials:  &Credentials{},
		authStates:   make(map[string]AuthState),
	}
}

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

// ConnectToWebSocket connects to the Joystick TV WebSocket API and listens for events
func (s *Server) ConnectToWebSocket() error {
	s.credMutex.RLock()
	clientID := s.credentials.ClientID
	clientSecret := s.credentials.ClientSecret
	s.credMutex.RUnlock()

	if clientID == "" || clientSecret == "" {
		return fmt.Errorf("missing credentials for WebSocket connection")
	}

	// Create basic auth token (Client ID:Client Secret in Base64)
	basicAuth := base64.StdEncoding.EncodeToString([]byte(clientID + ":" + clientSecret))

	// Connect to WebSocket
	wsURL := fmt.Sprintf("wss://joystick.tv/cable?token=%s", basicAuth)
	dialer := websocket.Dialer{
		HandshakeTimeout: 45 * time.Second,
	}

	ws, _, err := dialer.Dial(wsURL, http.Header{
		"Sec-WebSocket-Protocol": []string{"actioncable-v1-json"},
	})
	if err != nil {
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}
	defer ws.Close()

	log.Printf("✓ Connected to Joystick TV WebSocket API")

	// Subscribe to GatewayChannel
	subscribeMsg := map[string]string{
		"command":    "subscribe",
		"identifier": "{\"channel\":\"GatewayChannel\"}",
	}
	if err := ws.WriteJSON(subscribeMsg); err != nil {
		return fmt.Errorf("failed to send subscribe command: %w", err)
	}

	log.Printf("ℹ️  Sent subscription request to GatewayChannel")

	// Listen for events
	for {
		var msg map[string]interface{}
		if err := ws.ReadJSON(&msg); err != nil {
			log.Printf("⚠️  WebSocket connection closed: %v", err)
			return err
		}

		// Output all events
		s.outputEvent(msg)
	}
}

// downloadThumbnail downloads and saves a signed thumbnail URL to disk
func (s *Server) downloadThumbnail(imageURL, username string) error {
	// Create thumbnails directory if it doesn't exist
	thumbDir := "./thumbnails"
	if err := os.MkdirAll(thumbDir, 0755); err != nil {
		return fmt.Errorf("failed to create thumbnails directory: %w", err)
	}

	// Generate filename with timestamp to handle multiple images per user
	timestamp := time.Now().Format("20060102_150405")
	filename := filepath.Join(thumbDir, fmt.Sprintf("%s_%s.png", username, timestamp))

	// Download the image
	resp, err := http.Get(imageURL)
	if err != nil {
		return fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("image download failed with status %d", resp.StatusCode)
	}

	// Save to file
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return fmt.Errorf("failed to write image to file: %w", err)
	}

	log.Printf("✓ Thumbnail saved: %s", filename)
	return nil
}

// outputEvent formats and outputs received events
func (s *Server) outputEvent(msg map[string]interface{}) {
	// Check message type
	msgType, ok := msg["type"].(string)
	if ok {
		switch msgType {
		case "confirm_subscription":
			log.Printf("✓ Successfully subscribed to GatewayChannel")
			return
		case "reject_subscription":
			log.Printf("❌ Subscription rejected - authentication failed")
			return
		case "ping":
			// Silently ignore ping messages (connection heartbeats)
			return
		}
	}

	// Check for author photo thumbnail and download it
	if message, ok := msg["message"].(map[string]interface{}); ok {
		if author, ok := message["author"].(map[string]interface{}); ok {
			if thumbURL, ok := author["signedPhotoThumbUrl"].(string); ok && thumbURL != "" {
				var username string
				if slug, ok := author["slug"].(string); ok {
					username = slug
				} else if usr, ok := author["username"].(string); ok {
					username = usr
				} else {
					username = "unknown"
				}

				// Download thumbnail in background to avoid blocking event processing
				go func() {
					if err := s.downloadThumbnail(thumbURL, username); err != nil {
						log.Printf("⚠️  Failed to save thumbnail: %v", err)
					}
				}()
			}
		}
	}

	// Output raw event
	eventJSON, err := json.MarshalIndent(msg, "", "  ")
	if err != nil {
		log.Printf("❌ Failed to marshal event: %v", err)
		return
	}

	log.Printf("📨 Event received:\n%s", string(eventJSON))
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

// HandleRoot serves a simple home page
func (s *Server) HandleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `
		<!DOCTYPE html>
		<html>
		<head>
			<title>Joystick TV API Server</title>
			<style>
				body { font-family: Arial, sans-serif; margin: 50px; }
				h1 { color: #333; }
				a { color: #3498db; margin-right: 20px; text-decoration: none; }
				a:hover { text-decoration: underline; }
			</style>
		</head>
		<body>
			<h1>🎮 Joystick TV API Server</h1>
			<p>Welcome to the Joystick TV authentication server.</p>
			<p>
				<a href="/login">Authenticate</a>
				<a href="/status">View Status</a>
			</p>
		</body>
		</html>
	`)
}

func main() {
	// Get configuration from environment variables
	clientID := os.Getenv("JOYSTICK_CLIENT_ID")
	clientSecret := os.Getenv("JOYSTICK_CLIENT_SECRET")
	redirectURL := os.Getenv("JOYSTICK_REDIRECT_URL")
	port := os.Getenv("PORT")
	credFile := os.Getenv("CREDENTIALS_FILE")

	// Set defaults
	if port == "" {
		port = "8080"
	}
	if redirectURL == "" {
		redirectURL = "http://localhost:" + port + "/callback"
	}
	if credFile == "" {
		credFile = "./credentials.json"
	}

	// Validate required configuration
	if clientID == "" || clientSecret == "" {
		log.Fatal("❌ Missing required environment variables: JOYSTICK_CLIENT_ID and JOYSTICK_CLIENT_SECRET")
	}

	log.Printf("🚀 Starting Joystick TV API Server")
	log.Printf("ℹ️  Port: %s", port)
	log.Printf("ℹ️  Redirect URL: %s", redirectURL)
	log.Printf("ℹ️  Credentials File: %s", credFile)

	// Create server instance
	server := NewServer(clientID, clientSecret, redirectURL, credFile)

	// Load existing credentials if available
	if err := server.LoadCredentials(); err != nil {
		log.Printf("⚠️  Failed to load credentials: %v", err)
	}

	// Check if credentials exist and connect to WebSocket
	server.credMutex.RLock()
	hasCredentials := server.credentials.AccessToken != "" && server.credentials.ClientID != ""
	server.credMutex.RUnlock()

	if hasCredentials {
		log.Printf("ℹ️  Stored credentials found, connecting to WebSocket API...")
		go func() {
			time.Sleep(500 * time.Millisecond)
			if err := server.ConnectToWebSocket(); err != nil {
				log.Printf("❌ WebSocket connection error: %v", err)
			}
		}()
	}

	// Register HTTP handlers
	http.HandleFunc("/", server.HandleRoot)
	http.HandleFunc("/login", server.HandleLogin)
	http.HandleFunc("/callback", server.HandleCallback)
	http.HandleFunc("/status", server.HandleStatus)

	// Start server
	addr := ":" + port
	log.Printf("✓ Server listening on http://localhost:%s", port)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("❌ Server failed: %v", err)
	}
}
