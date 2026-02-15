# Voice Message Plugin for Mattermost

Record and send voice messages in Mattermost.  
Compatible with **Mattermost v9.5+ / v10 / v11**.

## Features

- **3 ways to record**: button in message toolbar (+), channel header icon, `/voice` command
- **Preview before sending** — listen, re-record, or discard
- **Custom player in chat** — waveform, seek, speed control (1×/1.5×/2×)
- **Microphone selection** — choose input device (PC, headset, etc.)
- **Small file size** — Opus/WebM ≈ 240 KB/min
- **Mobile playback** — voice messages play on mobile as standard audio attachments
- **Prepared for transcription** — settings reserved for future Whisper API integration

## Mobile Support

| Feature | Web / Desktop | Mobile Native App |
|---------|:---:|:---:|
| Record voice | ✅ | ❌ * |
| Play voice messages | ✅ custom player | ✅ standard audio player |
| `/voice` command | ✅ opens recorder | ℹ️ shows instruction |
| Button in toolbar | ✅ | ❌ * |

\* Mattermost mobile apps do not support webapp plugins (this is a platform limitation, not a plugin issue). Users on mobile will see voice messages as standard audio file attachments and can play them normally. To **record**, use the web browser or desktop app.

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

Output: `dist/com.scientia.voice-message-1.0.0.tar.gz`

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
tar -xzf dist/com.scientia.voice-message-1.0.0.tar.gz -C /opt/mattermost/plugins/
# Restart Mattermost
```

## Where to Find the Button

After enabling the plugin, you will see:

1. **"+" menu** next to the message input → "Voice Message" option (most visible!)
2. **Channel header** → microphone icon (top right area)
3. Type **`/voice`** in any channel

## Settings

In **System Console → Plugins → Voice Message**:

| Setting | Default | Description |
|---------|---------|-------------|
| Max Recording Duration | 300 sec | Max voice message length |
| Enable Transcription | false | Reserved for future |
| Transcription Service URL | — | Reserved for Whisper API |

## Browser Compatibility

| Browser | Recording Format |
|---------|-----------------|
| Chrome / Edge | WebM + Opus ✅ |
| Firefox | OGG + Opus ✅ |
| Safari ≥ 14.1 | MP4 ✅ |
| Desktop App | WebM + Opus ✅ |

## Future: Transcription

Server code has `EnableTranscription` and `TranscriptionServiceURL` settings. To implement:

1. Add `MessageHasBeenPosted` hook in `server/plugin.go`
2. Download the audio file from the attachment
3. Send to Whisper API (OpenAI or self-hosted whisper.cpp)
4. Store transcription in `post.Props["voice_transcription"]`
5. Display text under the player in `VoicePost.tsx`

## License

MIT
"# mattermost-plugin-voice-message" 
