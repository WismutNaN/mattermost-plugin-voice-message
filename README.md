# Voice Message Plugin for Mattermost

Record, send, and transcribe voice messages in Mattermost.
Compatible with **Mattermost v9.5+ / v10 / v11**.

## Features

- **3 ways to record**: button in message toolbar (+), channel header icon, `/voice` or `/audiomsg` command
- **Real-time audio level visualization** — 32 animated bars while recording
- **Countdown timer** — shows remaining time, warning animation when <30s left
- **Custom player in chat** — organic waveform, seek, speed control (1× / 1.25× / 1.5× / 2×)
- **AI transcription** — Whisper-based speech-to-text via DeepInfra, OpenAI, or custom endpoint
- **Auto-transcribe** — optionally transcribe every voice message on send
- **Thread support** — voice messages respect thread context (root_id)
- **Small file size** — Opus/WebM ≈ 240 KB/min
- **Mobile recording** — dedicated mobile page with token-based auth for Android/iOS WebView
- **Role-based access** — restrict recording to admins only

## AI Transcription

The plugin supports three transcription providers:

| Provider | Endpoint | Field | Notes |
|----------|----------|-------|-------|
| **DeepInfra** (default) | `api.deepinfra.com/v1/inference/openai/whisper-large-v3-turbo` | `audio` | Model in URL, no `model` field sent |
| **OpenAI** | `api.openai.com/v1/audio/transcriptions` | `file` | Model `whisper-1` |
| **Custom** | Your URL | `file` | Any Whisper-compatible API |

**How it works:**

1. User clicks the transcribe button (📝) on a voice message in chat
2. Server reads the audio file, sends it to the configured Whisper API
3. Transcript is saved to `post.Props["voice_transcript"]` and cached
4. Subsequent requests return the cached transcript instantly
5. Automatic retry (up to 3 attempts) on 5xx/429/timeout errors

**Setup:**

1. System Console → Plugins → Voice Message
2. Enable Transcription → `true`
3. Transcription Provider → `DeepInfra` / `OpenAI` / `Custom`
4. API Key → your DeepInfra or OpenAI token
5. Language (optional) → ISO 639-1 code (`ru`, `en`, `kk`, etc.)
6. Auto-Transcribe (optional) → `true` to transcribe every message automatically

## Mobile Support

| Feature | Web / Desktop | Mobile Native App |
|---------|:---:|:---:|
| Record voice | ✅ audio level bars | ✅ via mobile page * |
| Play voice messages | ✅ custom player | ✅ standard audio player |
| `/voice` or `/audiomsg` | ✅ opens recorder | ℹ️ opens mobile recording page |
| Transcription | ✅ button in player | ✅ auto-transcribe only |
| Button in toolbar | ✅ | ❌ * |

\* Mattermost mobile apps do not support webapp plugins (platform limitation). The `/voice` and `/audiomsg` commands on mobile open a dedicated recording page in the browser with token-based authentication, live audio levels, and a combined record/stop button.

## Requirements

- **Go** ≥ 1.22
- **Node.js** ≥ 18 + npm
- **Make**

## Build

```bash
cd mattermost-plugin-voice-message

# Initialize Go modules
cd server && go mod tidy && cd ..

# Build everything
make dist
```

Output: `dist/com.scientia.voice-message-2.0.0.tar.gz`

## Install

**Option A — System Console:**

1. Go to **System Console → Plugins → Plugin Management**
2. Click **Upload Plugin** → select the `.tar.gz`
3. Click **Enable**

**Option B — API:**

```bash
export MM_SERVICESETTINGS_SITEURL=https://mattermost.example.com
export MM_ADMIN_TOKEN=your-token
make deploy
```

**Option C — File copy:**

```bash
tar -xzf dist/com.scientia.voice-message-2.0.0.tar.gz -C /opt/mattermost/plugins/
# Restart Mattermost
```

## Where to Find the Button

After enabling the plugin:

1. **"+" menu** next to the message input → "Voice Message" option
2. **Channel header** → microphone icon (top right)
3. Type **`/voice`** or **`/audiomsg`** in any channel

## Settings

In **System Console → Plugins → Voice Message**:

| Setting | Default | Description |
|---------|---------|-------------|
| Max Recording Duration | 600 sec | Maximum voice message length |
| Max File Size | 50 MB | Maximum audio file size |
| Mobile Token TTL | 900 sec | Lifetime of mobile recording tokens |
| Allowed Roles | all | Who can record: `all` or `admins` |
| Enable Transcription | false | Enable AI transcription feature |
| Transcription Provider | deepinfra | `deepinfra`, `openai`, or `custom` |
| Transcription API Key | — | API key for the transcription service |
| Transcription Service URL | — | Custom endpoint URL (for `custom` provider) |
| Transcription Model | openai/whisper-large-v3-turbo | Model ID (used by OpenAI/custom providers) |
| Transcription Language | — | ISO 639-1 hint (e.g. `ru`, `en`, `kk`) |
| Transcription Max Duration | 300 sec | Max audio length for transcription |
| Auto-Transcribe | false | Automatically transcribe on send |

## API Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/api/v1/config` | Session | Returns plugin config for frontend |
| POST | `/api/v1/upload` | Session | Upload voice message (desktop/web) |
| POST | `/api/v1/mobile/upload` | Token | Upload voice message (mobile page) |
| POST | `/api/v1/transcribe?post_id=...` | Session | Transcribe a voice message |
| GET | `/mobile/record?token=...` | Token | Mobile recording HTML page |

## Browser Compatibility

| Browser | Recording Format |
|---------|-----------------|
| Chrome / Edge | WebM + Opus ✅ |
| Firefox | OGG + Opus ✅ |
| Safari ≥ 14.1 | MP4 ✅ |
| Desktop App | WebM + Opus ✅ |

## Security

- Mobile tokens are one-time use, deleted after successful upload
- Token TTL configurable (default 15 minutes)
- Channel membership verified on upload and transcription
- API keys stored server-side, never exposed to browser
- API key stripped from error messages before sending to frontend
- Origin validation for mobile uploads
- `MaxBytesReader` prevents oversized uploads
- CSP headers on mobile recording page
- Role-based access control (all users or admins only)

## Project Structure

```
├── plugin.json                    # Plugin manifest and settings schema
├── server/
│   ├── plugin.go                  # All server logic (routes, transcription, mobile page)
│   └── main.go                    # Entry point
├── webapp/src/
│   ├── index.tsx                  # Plugin registration, slash command hooks
│   ├── RecorderPanel.tsx          # Recording modal with audio level bars
│   ├── VoicePost.tsx              # In-chat player with waveform and transcription
│   ├── useRecorder.ts             # Recording hook (MediaRecorder + AnalyserNode)
│   ├── api.ts                     # API helpers (config, upload, transcribe)
│   └── styles.css                 # All plugin styles
├── Makefile                       # Build system
└── assets/
    └── icon.svg                   # Plugin icon
```

## License

MIT
