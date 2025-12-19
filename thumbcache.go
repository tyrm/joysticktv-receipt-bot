package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
)

// ThumbnailCache manages the thumbnail file storage and database operations
type ThumbnailCache struct {
	db       *sql.DB
	cacheDir string
	mu       sync.RWMutex
}

// ThumbnailRecord represents a cached thumbnail in the database
type ThumbnailRecord struct {
	Username          string
	SHA256            string
	FileSize          int64
	DownloadTimestamp time.Time
	ImageURL          string
	FileExtension     string
}

// NewThumbnailCache initializes a new thumbnail cache with an existing database connection
func NewThumbnailCache(db *sql.DB, cacheDir string) (*ThumbnailCache, error) {
	// Create cache directory if it doesn't exist
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory %s: %w", cacheDir, err)
	}

	tc := &ThumbnailCache{
		db:       db,
		cacheDir: cacheDir,
	}

	return tc, nil
}

// ThumbnailExists checks if a thumbnail is already cached for the given username
func (tc *ThumbnailCache) ThumbnailExists(username string) (bool, error) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	var exists bool
	err := tc.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM thumbnails WHERE username = ?)",
		username,
	).Scan(&exists)

	if err != nil && err != sql.ErrNoRows {
		return false, fmt.Errorf("database query error: %w", err)
	}

	return exists, nil
}

// GetThumbnailInfo retrieves the full record for a cached thumbnail
func (tc *ThumbnailCache) GetThumbnailInfo(username string) (*ThumbnailRecord, error) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	record := &ThumbnailRecord{}
	var timestamp int64

	err := tc.db.QueryRow(`
		SELECT username, sha256, file_size, download_timestamp, image_url, file_extension
		FROM thumbnails
		WHERE username = ?
	`, username).Scan(
		&record.Username,
		&record.SHA256,
		&record.FileSize,
		&timestamp,
		&record.ImageURL,
		&record.FileExtension,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("database query error: %w", err)
	}

	record.DownloadTimestamp = time.Unix(timestamp, 0)
	return record, nil
}

// getSubdirectory extracts the first letter of the username for directory organization
// Returns lowercase first letter, or "other" for edge cases
func (tc *ThumbnailCache) getSubdirectory(username string) string {
	if username == "" {
		return "other"
	}

	// Handle unicode characters by getting the first rune
	runes := []rune(username)
	if len(runes) == 0 {
		return "other"
	}

	firstChar := strings.ToLower(string(runes[0]))

	// Check if it's ASCII letter
	if (firstChar >= "a" && firstChar <= "z") {
		return firstChar
	}

	// Non-ASCII or non-letter characters go to "other"
	return "other"
}

// extractExtension parses the image URL to extract the file extension
// Falls back to Content-Type header parsing if URL doesn't have extension
// Defaults to .png if no extension is found
func extractExtension(imageURL string) string {
	// Parse the URL
	parsed, err := url.Parse(imageURL)
	if err != nil {
		return ".png"
	}

	// Get the path and extract the file
	path := parsed.Path
	if path == "" {
		return ".png"
	}

	// Get the last component of the path
	lastPart := filepath.Base(path)

	// Look for a file extension
	if strings.Contains(lastPart, ".") {
		parts := strings.Split(lastPart, ".")
		ext := parts[len(parts)-1]
		// Limit extension length to reasonable size (e.g., 5 chars)
		if len(ext) > 0 && len(ext) <= 5 {
			// Common image extensions
			ext = strings.ToLower(ext)
			switch ext {
			case "jpg", "jpeg", "png", "gif", "webp", "bmp", "tiff", "svg":
				return "." + ext
			}
		}
	}

	return ".png"
}

// calculateSHA256 computes the SHA256 hash of a file and returns it as hex string
func calculateSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// GetFilePath constructs the full file path for a thumbnail
func (tc *ThumbnailCache) GetFilePath(username, extension string) string {
	subdir := tc.getSubdirectory(username)
	return filepath.Join(tc.cacheDir, subdir, username+extension)
}

// insertThumbnail stores a thumbnail record in the database
func (tc *ThumbnailCache) insertThumbnail(record *ThumbnailRecord) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	timestamp := record.DownloadTimestamp.Unix()

	result, err := tc.db.Exec(`
		INSERT INTO thumbnails (username, sha256, file_size, download_timestamp, image_url, file_extension)
		VALUES (?, ?, ?, ?, ?, ?)
	`,
		record.Username,
		record.SHA256,
		record.FileSize,
		timestamp,
		record.ImageURL,
		record.FileExtension,
	)

	if err != nil {
		// Check if it's a constraint violation (duplicate username)
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("thumbnail already exists for user %s", record.Username)
		}
		return fmt.Errorf("database insert error: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("no rows inserted for user %s", record.Username)
	}

	return nil
}

// DownloadAndStore downloads a thumbnail image and stores it in the cache
// It skips download if the thumbnail already exists
func (tc *ThumbnailCache) DownloadAndStore(imageURL, username string) error {
	// Sanitize username
	if username == "" {
		username = "unknown"
	}

	// Check if thumbnail already exists
	exists, err := tc.ThumbnailExists(username)
	if err != nil {
		return fmt.Errorf("failed to check if thumbnail exists: %w", err)
	}

	if exists {
		log.Printf("ℹ️  Thumbnail already cached for user %s", username)
		return nil
	}

	// Extract extension from URL
	extension := extractExtension(imageURL)

	// Determine the subdirectory path
	subdir := tc.getSubdirectory(username)
	subdirPath := filepath.Join(tc.cacheDir, subdir)

	// Create subdirectory if it doesn't exist
	if err := os.MkdirAll(subdirPath, 0755); err != nil {
		return fmt.Errorf("failed to create subdirectory %s: %w", subdirPath, err)
	}

	// Get full file path
	filePath := filepath.Join(subdirPath, username+extension)

	// Download the image
	resp, err := http.Get(imageURL)
	if err != nil {
		return fmt.Errorf("failed to download image from %s: %w", imageURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("image download failed with HTTP status %d from %s", resp.StatusCode, imageURL)
	}

	// Create the file
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filePath, err)
	}
	defer file.Close()

	// Copy the response body to the file
	if _, err := io.Copy(file, resp.Body); err != nil {
		// Clean up the partial file on failure
		os.Remove(filePath)
		return fmt.Errorf("failed to write image to file: %w", err)
	}

	// Get file info for size
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		os.Remove(filePath)
		return fmt.Errorf("failed to get file info: %w", err)
	}

	// Calculate SHA256 hash
	sha256Hash, err := calculateSHA256(filePath)
	if err != nil {
		os.Remove(filePath)
		return fmt.Errorf("failed to calculate SHA256: %w", err)
	}

	// Create database record
	record := &ThumbnailRecord{
		Username:          username,
		SHA256:            sha256Hash,
		FileSize:          fileInfo.Size(),
		DownloadTimestamp: time.Now(),
		ImageURL:          imageURL,
		FileExtension:     extension,
	}

	// Insert into database
	if err := tc.insertThumbnail(record); err != nil {
		// Clean up the downloaded file if database insert fails
		os.Remove(filePath)
		return err
	}

	log.Printf("✓ Thumbnail saved: %s (SHA256: %s)", filePath, sha256Hash[:16]+"...")
	return nil
}
