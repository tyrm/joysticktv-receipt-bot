package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// StreamEvent represents an event stored in the database
type StreamEvent struct {
	ID                   int64
	ReceivedTimestamp    time.Time
	EventType            string
	UserWhoPerformedAction *string
	RawJSON              string
}

// StreamEventStore handles storing events in the database
type StreamEventStore struct {
	db *sql.DB
}

// NewStreamEventStore creates a new stream event store with a database connection
func NewStreamEventStore(db *sql.DB) *StreamEventStore {
	return &StreamEventStore{
		db: db,
	}
}

// ExtractEventInfo extracts event type and user information from a WebSocket message
// Returns event type, user (if available), and whether extraction was successful
func ExtractEventInfo(msg map[string]interface{}) (string, *string, bool) {
	var eventType string
	var user *string

	// Try to extract event information from nested message structure
	if message, ok := msg["message"].(map[string]interface{}); ok {
		// Check for "type" field inside message (specific event type like "tipped", "ChatMessage", "enter_stream")
		if typeField, ok := message["type"].(string); ok && typeField != "" {
			eventType = typeField
		}

		// Try to extract user information from author object
		if author, ok := message["author"].(map[string]interface{}); ok {
			// Prefer slug, fallback to username
			if slug, ok := author["slug"].(string); ok && slug != "" {
				user = &slug
			} else if username, ok := author["username"].(string); ok && username != "" {
				user = &username
			}
		}

		// If no user from author, try to extract from metadata JSON (for StreamEvents)
		if user == nil {
			if metadata, ok := message["metadata"].(string); ok && metadata != "" {
				var metadataObj map[string]interface{}
				if err := json.Unmarshal([]byte(metadata), &metadataObj); err == nil {
					if who, ok := metadataObj["who"].(string); ok && who != "" {
						user = &who
					}
				}
			}
		}
	}

	// If event type was found, return it
	if eventType != "" {
		return eventType, user, true
	}

	// Check for top-level "type" field (welcome, ping, subscription confirmations)
	if typeField, ok := msg["type"].(string); ok {
		return typeField, nil, true
	}

	// Check for "event" field as fallback (alternative message structure)
	if event, ok := msg["event"].(string); ok {
		return event, user, true
	}

	return "", nil, false
}

// IsStreamEvent checks if a message is a StreamEvent
func IsStreamEvent(msg map[string]interface{}) bool {
	if message, ok := msg["message"].(map[string]interface{}); ok {
		if event, ok := message["event"].(string); ok {
			return event == "StreamEvent"
		}
	}
	return false
}

// StoreEvent stores a stream event in the database
// Only stores StreamEvent messages; other event types are handled separately
func (ses *StreamEventStore) StoreEvent(msg map[string]interface{}) error {
	// Only store StreamEvent messages
	if !IsStreamEvent(msg) {
		return nil // Silently skip non-StreamEvent messages
	}

	// Extract event type and user information
	eventType, user, ok := ExtractEventInfo(msg)
	if !ok {
		return fmt.Errorf("unable to extract event information from message")
	}

	// Convert message to JSON for storage
	rawJSON, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message to JSON: %w", err)
	}

	// Store in database
	timestamp := time.Now().Unix()

	_, err = ses.db.Exec(`
		INSERT INTO stream_events (received_timestamp, event_type, user_who_performed_action, raw_json)
		VALUES (?, ?, ?, ?)
	`,
		timestamp,
		eventType,
		user,
		string(rawJSON),
	)

	if err != nil {
		return fmt.Errorf("failed to insert stream event: %w", err)
	}

	return nil
}

// GetEventsByType retrieves events of a specific type from the database
func (ses *StreamEventStore) GetEventsByType(eventType string, limit int) ([]StreamEvent, error) {
	rows, err := ses.db.Query(`
		SELECT id, received_timestamp, event_type, user_who_performed_action, raw_json
		FROM stream_events
		WHERE event_type = ?
		ORDER BY received_timestamp DESC
		LIMIT ?
	`, eventType, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query events: %w", err)
	}
	defer rows.Close()

	var events []StreamEvent
	for rows.Next() {
		var event StreamEvent
		var timestamp int64

		err := rows.Scan(
			&event.ID,
			&timestamp,
			&event.EventType,
			&event.UserWhoPerformedAction,
			&event.RawJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}

		event.ReceivedTimestamp = time.Unix(timestamp, 0)
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating events: %w", err)
	}

	return events, nil
}

// GetEventsByUser retrieves events performed by a specific user
func (ses *StreamEventStore) GetEventsByUser(user string, limit int) ([]StreamEvent, error) {
	rows, err := ses.db.Query(`
		SELECT id, received_timestamp, event_type, user_who_performed_action, raw_json
		FROM stream_events
		WHERE user_who_performed_action = ?
		ORDER BY received_timestamp DESC
		LIMIT ?
	`, user, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query events: %w", err)
	}
	defer rows.Close()

	var events []StreamEvent
	for rows.Next() {
		var event StreamEvent
		var timestamp int64

		err := rows.Scan(
			&event.ID,
			&timestamp,
			&event.EventType,
			&event.UserWhoPerformedAction,
			&event.RawJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}

		event.ReceivedTimestamp = time.Unix(timestamp, 0)
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating events: %w", err)
	}

	return events, nil
}

// GetRecentEvents retrieves the most recent events
func (ses *StreamEventStore) GetRecentEvents(limit int) ([]StreamEvent, error) {
	rows, err := ses.db.Query(`
		SELECT id, received_timestamp, event_type, user_who_performed_action, raw_json
		FROM stream_events
		ORDER BY received_timestamp DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query events: %w", err)
	}
	defer rows.Close()

	var events []StreamEvent
	for rows.Next() {
		var event StreamEvent
		var timestamp int64

		err := rows.Scan(
			&event.ID,
			&timestamp,
			&event.EventType,
			&event.UserWhoPerformedAction,
			&event.RawJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}

		event.ReceivedTimestamp = time.Unix(timestamp, 0)
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating events: %w", err)
	}

	return events, nil
}
