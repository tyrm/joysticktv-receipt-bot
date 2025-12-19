package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
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
	db           *AppDatabase
	thumbCache   *ThumbnailCache
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

	// Check for author photo thumbnail and cache it
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

				// Download and cache thumbnail in background to avoid blocking event processing
				if s.thumbCache != nil {
					go func() {
						if err := s.thumbCache.DownloadAndStore(thumbURL, username); err != nil {
							log.Printf("⚠️  Thumbnail cache error for user %s: %v", username, err)
						}
					}()
				}
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

	// Initialize application database
	appDB, err := NewAppDatabase("./app.db")
	if err != nil {
		log.Fatalf("❌ Failed to initialize database: %v", err)
	}
	server.db = appDB
	defer appDB.Close()
	log.Printf("✓ Application database initialized")

	// Initialize thumbnail cache with database connection
	thumbCache, err := NewThumbnailCache(appDB.GetDB(), "./thumbcache")
	if err != nil {
		log.Fatalf("❌ Failed to initialize thumbnail cache: %v", err)
	}
	server.thumbCache = thumbCache
	log.Printf("✓ Thumbnail cache initialized")

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
