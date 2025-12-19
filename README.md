# Joystick TV API Server

[![Claude Logo](https://img.shields.io/badge/Claude-D97757?label=generated%20with)](https://claude.ai/code)

A simple Go web server that authenticates with the Joystick TV API using OAuth2 and persists authentication credentials to a file for automatic recovery across service restarts.

## Features

- 🔐 OAuth2 authentication with Joystick TV
- 💾 Automatic credential persistence to `credentials.json`
- 🔄 Automatic credential recovery on startup
- 🛡️ CSRF protection with state validation
- 🕐 Token expiration tracking
- 🌐 Simple web UI for authentication and status checking
- 🔌 WebSocket connection for real-time event listening
- 📨 Automatic event output to logs (chat messages, follows, tips, user presence, etc.)
- 🖼️ Automatic profile thumbnail caching with SHA256 verification

## Prerequisites

- Go 1.16 or later
- Joystick TV Developer account with OAuth credentials
- Environment variables configured (see Setup section)

## Setup

### 1. Create a Joystick TV Bot Application

1. Visit [Joystick TV Developer Support](https://support.joystick.tv/developer_support/)
2. Create a new bot application
3. Note your **Client ID** and **Client Secret**
4. Set your Redirect URI to match your server (default: `http://localhost:8080/callback`)

### 2. Configure Environment Variables

Create a `.env` file or export these environment variables:

```bash
# Required
export JOYSTICK_CLIENT_ID="your_client_id_here"
export JOYSTICK_CLIENT_SECRET="your_client_secret_here"

# Optional (with defaults shown)
export JOYSTICK_REDIRECT_URL="http://localhost:8080/callback"
export PORT="8080"
export CREDENTIALS_FILE="./credentials.json"
```

### 3. Build and Run

```bash
# Build the server
go build -o joystick-server main.go

# Run the server
./joystick-server
```

Or run directly:

```bash
go run main.go
```

## Usage

### Web Interface

Once the server is running, open your browser:

- **Home Page:** http://localhost:8080/
- **Authenticate:** http://localhost:8080/login
- **Check Status:** http://localhost:8080/status

### Authentication Flow

1. Visit http://localhost:8080/login
2. You'll be redirected to Joystick TV to authorize the application
3. After granting permissions, you'll be redirected back to the server
4. Your credentials are automatically saved to `credentials.json`

### Credentials File

The `credentials.json` file stores:

```json
{
  "access_token": "your_jwt_token",
  "refresh_token": "your_refresh_token",
  "expires_at": "2025-01-18T12:34:56Z",
  "client_id": "your_client_id",
  "client_secret": "your_client_secret"
}
```

**⚠️ Security:** This file contains sensitive information. Keep it safe and never commit it to version control. The file is created with restricted permissions (0600).

## API Endpoints

### Root
- `GET /` - Home page with navigation links

### Authentication
- `GET /login` - Initiate OAuth2 flow
- `GET /callback` - OAuth2 callback endpoint (Joystick TV redirects here)

### Status
- `GET /status` - View current authentication status and credential expiration

## How Persistence Works

1. **On Startup:** The server attempts to load credentials from the configured `CREDENTIALS_FILE`
2. **After Authentication:** New credentials are automatically saved to the file with restricted permissions (mode 0600)
3. **On Restart:** Previously saved credentials are loaded, allowing the server to operate without re-authentication

## Environment Variables Reference

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `JOYSTICK_CLIENT_ID` | Yes | - | Your Joystick TV OAuth Client ID |
| `JOYSTICK_CLIENT_SECRET` | Yes | - | Your Joystick TV OAuth Client Secret |
| `JOYSTICK_REDIRECT_URL` | No | `http://localhost:8080/callback` | OAuth redirect URI |
| `PORT` | No | `8080` | Server port |
| `CREDENTIALS_FILE` | No | `./credentials.json` | Path to credentials file |

## Logging

The server provides verbose logging with clear indicators:
- ✓ Success messages
- ❌ Error messages (actionable information)
- ℹ️ Information messages
- ⚠️ Warning messages

## WebSocket Event Listening

Once authenticated, the bot automatically connects to the Joystick TV WebSocket API and starts listening for events. All events are logged to the console with the 📨 indicator.

**Event Types:**
- **Chat Messages** - User-generated chat with text, author info, and metadata
- **User Presence** - When users enter/leave chat (`enter_stream` or `leave_stream`)
- **Stream Events** - Tips, follows, device connections, stream start/stop
- **Ping Messages** - Connection heartbeats (unix timestamps)

The bot will automatically reconnect on startup if stored credentials exist.

## Thumbnail Cache

The bot automatically downloads and caches user profile thumbnails extracted from WebSocket events. All thumbnails are stored locally with a SQLite database tracking the cached files for efficient lookup and verification.

**Cache Directory Structure:**

```
./app.db                  # Application database (stores all app data including thumbnails)
./thumbcache/
├── a/                    # Subdirectory for usernames starting with 'a'
│   └── alice.png
├── t/
│   └── tyrm.png
├── u/
│   └── unknown.jpg
└── other/                # Non-alphabetic characters
    └── 用户123.png
```

**Features:**

- ✓ Thumbnails organized by first letter of username for efficient lookup
- ✓ Automatic SHA256 hash calculation for file integrity verification
- ✓ One thumbnail per user (no duplicate downloads)
- ✓ Automatic download on first event appearance
- ✓ SQLite database tracks cache metadata
- ✓ Persists across application restarts

**Database Schema:**

The `app.db` SQLite database is the application-wide database that stores all persistent data. It includes two main tables:

### Thumbnails Table

The thumbnails table stores cached profile images:

| Column | Type | Description |
|--------|------|-------------|
| `username` | TEXT (Primary Key) | Username of the cached profile |
| `sha256` | TEXT | SHA256 hash of the image file for integrity verification |
| `file_size` | INTEGER | File size in bytes |
| `download_timestamp` | INTEGER | Unix timestamp of when the image was downloaded |
| `image_url` | TEXT | Original signedPhotoThumbUrl from the event |
| `file_extension` | TEXT | File extension (.png, .jpg, etc.) detected from URL |

### Stream Events Table

The stream_events table stores **only StreamEvent** messages (tips, follows, device connections, etc.) for detailed logging and analysis:

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER (Primary Key) | Auto-incrementing unique identifier for the event record |
| `received_timestamp` | INTEGER | Unix timestamp of when the event was received |
| `event_type` | TEXT | Specific stream event type (tipped, Followed, DeviceConnected, StreamStarted, etc.) |
| `user_who_performed_action` | TEXT (Nullable) | Username of the user who triggered the event (from metadata.who) |
| `raw_json` | TEXT | Complete raw JSON message as received from the WebSocket |

**Indexes:**
- `idx_stream_events_timestamp` - For efficient time-based queries
- `idx_stream_events_type` - For filtering by event type
- `idx_stream_events_user` - For querying events by user

**What Gets Stored:**
- ✓ **Stream events only** (tipped, Followed, DeviceConnected, StreamStarted, StreamEnded, WheelSpinClaimed, etc.)

**What Does NOT Get Stored Here:**
- ✗ Chat messages (ChatMessage) - handled separately
- ✗ User presence changes (UserPresence) - handled separately
- ✗ Control messages (ping, welcome, subscriptions) - control flow only

**How It Works:**

1. When a WebSocket event arrives with an author's profile image URL, the bot checks if the thumbnail is already cached
2. If not cached, the image is downloaded in the background and saved to `./thumbcache/{first_letter}/{username}.{ext}`
3. The file is hashed with SHA256 and metadata is stored in the SQLite database
4. Subsequent events from the same user will:
   - If thumbnail is newer than 5 minutes: Skip download (marked as "already cached")
   - If thumbnail is older than 5 minutes: Re-download and replace the cached file to ensure it's up-to-date

**Configuration:**

- **Database location:** `./app.db` (relative to the application directory)
- **Cache location:** `./thumbcache` (relative to the application directory)

**Notes:**

- Thumbnails are downloaded asynchronously to avoid blocking event processing
- Failed downloads are logged with warnings but don't stop the bot
- The cache directory is excluded from version control (see `.gitignore`)
- Database uses WAL (Write-Ahead Logging) mode for better concurrent access
- **Refresh interval:** Thumbnails are automatically refreshed if they're older than 5 minutes
  - This ensures profile picture changes are captured while minimizing unnecessary downloads
  - Each refresh updates the SHA256 hash, file size, and timestamp in the database

## Next Steps

After successful authentication, the bot:

1. Automatically connects to the WebSocket endpoint
2. Starts listening for real-time events from your stream
3. Logs all events to the console for monitoring
4. Persists credentials for automatic recovery on restart

You can extend the bot by:
- Processing events programmatically
- Sending messages back to chat
- Implementing custom command handling
- Storing event data in a database

## API Endpoints (Joystick TV)

The bot automatically uses the WebSocket endpoint for real-time events:

- `wss://joystick.tv/cable?token=YOUR_BASIC_KEY` - **Automatically connected and listening** for chat, follows, tips, and presence events

You can also manually use other Joystick TV API endpoints:

- `GET/PATCH https://joystick.tv/api/users/stream-settings` - Manage streamer settings
- `GET https://joystick.tv/api/users/subscriptions` - Get subscriber lists

For REST API endpoints, use your `access_token` from `credentials.json` as a Bearer token in the `Authorization` header.

## Troubleshooting

### Missing credentials.json on startup
This is normal for first-time setup. Visit `/login` to authenticate and the file will be created.

### "Missing required environment variables"
Ensure both `JOYSTICK_CLIENT_ID` and `JOYSTICK_CLIENT_SECRET` are set.

### Token expired
Visit `/status` to see expiration time. Re-authenticate by visiting `/login` to get a fresh token.

### Credentials file permission denied
Make sure the application has write permissions to the directory specified by `CREDENTIALS_FILE`.

### WebSocket connection fails with "authentication failed"
The WebSocket uses basic auth (Client ID:Client Secret in Base64). Ensure your credentials are correct and the bot application is properly configured on Joystick TV.

### No events being logged
Check that:
1. The bot application has the necessary permissions configured in Joystick TV
2. Your stream is active and has activity (chat, follows, etc.)
3. The WebSocket is connected (you should see "✓ Connected to Joystick TV WebSocket API" in logs)

## Security Notes

- Credentials are stored with restricted file permissions (0600)
- OAuth state tokens are validated to prevent CSRF attacks
- State tokens expire after 10 minutes
- Always use HTTPS in production
- Never commit `credentials.json` to version control
- Consider using a `.gitignore` entry: `credentials.json`

## License

MIT

## Support

For issues with the Joystick TV API, visit [Joystick TV Developer Support](https://support.joystick.tv/developer_support/)
