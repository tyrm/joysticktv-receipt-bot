package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

// AppDatabase manages the application database and all its tables
type AppDatabase struct {
	db *sql.DB
}

// NewAppDatabase initializes a new application database
func NewAppDatabase(dbPath string) (*AppDatabase, error) {
	// Open or create SQLite database
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database %s: %w", dbPath, err)
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Set pragmas for better concurrency and reliability
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		log.Printf("⚠️  Warning: Could not set WAL mode: %v", err)
	}
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		log.Printf("⚠️  Warning: Could not set synchronous mode: %v", err)
	}

	appDB := &AppDatabase{
		db: db,
	}

	// Initialize database schema
	if err := appDB.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return appDB, nil
}

// initSchema creates all application tables if they don't exist
func (ad *AppDatabase) initSchema() error {
	schema := `
	-- Thumbnails table for caching user profile images
	CREATE TABLE IF NOT EXISTS thumbnails (
		username TEXT PRIMARY KEY NOT NULL,
		sha256 TEXT NOT NULL,
		file_size INTEGER NOT NULL,
		download_timestamp INTEGER NOT NULL,
		image_url TEXT NOT NULL,
		file_extension TEXT NOT NULL DEFAULT '.png'
	);

	CREATE INDEX IF NOT EXISTS idx_download_timestamp ON thumbnails(download_timestamp);

	-- Stream events table for storing all received WebSocket events
	CREATE TABLE IF NOT EXISTS stream_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		received_timestamp INTEGER NOT NULL,
		event_type TEXT NOT NULL,
		user_who_performed_action TEXT,
		raw_json TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_stream_events_timestamp ON stream_events(received_timestamp);
	CREATE INDEX IF NOT EXISTS idx_stream_events_type ON stream_events(event_type);
	CREATE INDEX IF NOT EXISTS idx_stream_events_user ON stream_events(user_who_performed_action);
	`

	if _, err := ad.db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	return nil
}

// GetDB returns the underlying database connection for use by other components
func (ad *AppDatabase) GetDB() *sql.DB {
	return ad.db
}

// Close gracefully closes the database connection
func (ad *AppDatabase) Close() error {
	if ad.db != nil {
		return ad.db.Close()
	}
	return nil
}
