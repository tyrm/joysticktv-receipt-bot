package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"

	"tyr.codes/golib/receipt/template"
)

// HandleTippedEvent processes a tipped stream event and prints a receipt notification
func (s *Server) HandleTippedEvent(msg map[string]interface{}) {
	// Ensure we have a printer
	if s.printer == nil {
		return
	}

	// Extract the message object
	message, ok := msg["message"].(map[string]interface{})
	if !ok {
		return
	}

	// Extract metadata JSON string
	metadataStr, ok := message["metadata"].(string)
	if !ok || metadataStr == "" {
		return
	}

	// Parse metadata JSON
	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(metadataStr), &metadata); err != nil {
		log.Printf("⚠️  Failed to parse tip metadata: %v", err)
		return
	}

	// Require tip_menu_item to be populated
	tipMenuItem, ok := metadata["tip_menu_item"].(string)
	if !ok || tipMenuItem == "" {
		return // No tip menu item, skip notification
	}

	// Extract text field from message (the full tip message)
	messageText, ok := message["text"].(string)
	if !ok || messageText == "" {
		// Fallback to tip menu item if text is not available
		messageText = tipMenuItem
	}

	// Extract username from metadata
	username, ok := metadata["who"].(string)
	if !ok || username == "" {
		username = "Anonymous"
	}

	// Determine icon path based on tip menu item
	iconPath := getIconPath(tipMenuItem)

	// Create and print the notification
	notification := &template.StreamerNotification{
		Header:   "New Tip",
		Message:  messageText,
		IconPath: iconPath,
		Username: username,
	}

	if err := notification.Print(s.printer); err != nil {
		log.Printf("⚠️  Failed to print tip notification: %v", err)
		return
	}

	log.Printf("✓ Tip notification printed for %s: %s", username, messageText)
}

// getIconPath determines the icon file path for a tip menu item
// Returns the path to the icon file, or defaults to joysticktv.png if not found
func getIconPath(tipMenuItem string) string {
	// Create a filename from the tip menu item (sanitize it)
	// Replace spaces with underscores and convert to lowercase
	filename := strings.ToLower(strings.ReplaceAll(tipMenuItem, " ", "_"))
	filename = filename + ".png"

	// Check multiple possible icon locations
	possiblePaths := []string{
		filepath.Join(".", "icons", filename),
		filepath.Join(".", "assets", "icons", filename),
		filepath.Join(".", "static", "icons", filename),
	}

	// Try to find the icon file
	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	// Default to joysticktv.png
	possibleDefaults := []string{
		filepath.Join(".", "icons", "joysticktv.png"),
		filepath.Join(".", "assets", "icons", "joysticktv.png"),
		filepath.Join(".", "static", "icons", "joysticktv.png"),
		"joysticktv.png",
	}

	for _, path := range possibleDefaults {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	// Return default path even if it doesn't exist
	return "joysticktv.png"
}
