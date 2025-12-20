package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"

	"tyr.codes/golib/receipt"
	"tyr.codes/golib/receipt/template"
)

// HandleTippedEvent processes a tipped stream event and prints a receipt notification
func (s *Server) HandleTippedEvent(msg map[string]interface{}) {
	// Ensure we have a printer address configured
	if s.printerAddr == "" {
		log.Printf("ℹ️  No printer address configured, skipping tip notification")
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

	// Extract username from author field first
	var username string
	var img image.Image
	if author, ok := message["author"].(map[string]interface{}); ok {
		if slug, ok := author["slug"].(string); ok && slug != "" {
			username = slug
		} else if usr, ok := author["username"].(string); ok && usr != "" {
			username = usr
		}

		// Try to get the user's cached profile thumbnail
		if username != "" && s.thumbCache != nil {
			if thumbInfo, err := s.thumbCache.GetThumbnailInfo(username); err == nil && thumbInfo != nil && thumbInfo.FileExtension != "" {
				thumbPath := s.thumbCache.GetFilePath(username, thumbInfo.FileExtension)
				if file, err := os.Open(thumbPath); err == nil {
					defer file.Close()
					if decodedImg, err := png.Decode(file); err == nil {
						img = decodedImg
					}
				}
			}
		}
	}

	// Fallback to metadata username if author field didn't work
	if username == "" {
		username, _ = metadata["who"].(string)
	}
	if username == "" {
		username = "Anonymous"
	}

	// If we don't have a cached thumbnail, decode the embedded joysticktv.png
	if img == nil {
		if decodedImg, err := png.Decode(bytes.NewReader(joysticktv)); err == nil {
			img = decodedImg
		} else {
			log.Printf("⚠️  Failed to decode embedded image: %v", err)
		}
	}

	// Connect to printer, print notification, then disconnect
	printer := receipt.NewPrinter(s.printerAddr)
	if err := printer.Connect(); err != nil {
		log.Printf("❌ Failed to connect to printer: %v", err)
		return
	}
	defer printer.Disconnect()

	// Create and print the notification
	notification := &template.StreamerNotification{
		Header:   "New Tip",
		Message:  messageText,
		Image:    img,
		Username: username,
	}

	if err := notification.Print(printer); err != nil {
		log.Printf("⚠️  Failed to print tip notification: %v", err)
		return
	}

	log.Printf("✓ Tip notification printed for %s: %s", username, messageText)
}

// HandleFollowedEvent processes a followed stream event and prints a receipt notification
func (s *Server) HandleFollowedEvent(msg map[string]interface{}) {
	// Ensure we have a printer address configured
	if s.printerAddr == "" {
		log.Printf("ℹ️  No printer address configured, skipping follower notification")
		return
	}

	// Extract the message object
	message, ok := msg["message"].(map[string]interface{})
	if !ok {
		return
	}

	// Extract username from author field
	var username string
	var img image.Image
	if author, ok := message["author"].(map[string]interface{}); ok {
		if slug, ok := author["slug"].(string); ok && slug != "" {
			username = slug
		} else if usr, ok := author["username"].(string); ok && usr != "" {
			username = usr
		}

		// Try to get the user's cached profile thumbnail
		if username != "" && s.thumbCache != nil {
			if thumbInfo, err := s.thumbCache.GetThumbnailInfo(username); err == nil && thumbInfo != nil && thumbInfo.FileExtension != "" {
				thumbPath := s.thumbCache.GetFilePath(username, thumbInfo.FileExtension)
				if file, err := os.Open(thumbPath); err == nil {
					defer file.Close()
					if decodedImg, err := png.Decode(file); err == nil {
						img = decodedImg
					}
				}
			}
		}
	}

	// If we couldn't get username from author, try metadata
	if username == "" {
		metadataStr, ok := message["metadata"].(string)
		if ok && metadataStr != "" {
			var metadata map[string]interface{}
			if err := json.Unmarshal([]byte(metadataStr), &metadata); err == nil {
				if who, ok := metadata["who"].(string); ok && who != "" {
					username = who
				}
			}
		}
	}

	// Default to Anonymous if we still don't have a username
	if username == "" {
		username = "Anonymous"
	}

	// If we don't have a cached thumbnail, decode the embedded joysticktv.png
	if img == nil {
		if decodedImg, err := png.Decode(bytes.NewReader(joysticktv)); err == nil {
			img = decodedImg
		} else {
			log.Printf("⚠️  Failed to decode embedded image: %v", err)
		}
	}

	// Connect to printer, print notification, then disconnect
	printer := receipt.NewPrinter(s.printerAddr)
	if err := printer.Connect(); err != nil {
		log.Printf("❌ Failed to connect to printer: %v", err)
		return
	}
	defer printer.Disconnect()

	// Create and print the notification
	notification := &template.StreamerNotification{
		Header:   "New Follower",
		Message:  "Welcome!",
		Image:    img,
		Username: username,
	}

	if err := notification.Print(printer); err != nil {
		log.Printf("⚠️  Failed to print follower notification: %v", err)
		return
	}

	log.Printf("✓ Follower notification printed for %s", username)
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
