package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const (
	pluginID = "com.scientia.voice-message"

	commandVoice = "voice"
	commandVM    = "audiomsg"

	defaultMaxRecordingDurationSeconds = 600
	defaultMobileTokenTTLSeconds       = 15 * 60
	defaultMaxFileSizeMB               = 50
	defaultTranscriptionMaxDurSec      = 300

	kvMobileTokenPrefix = "vm_mobile_token_"
)

type mobileToken struct {
	UserID          string `json:"user_id"`
	ChannelID       string `json:"channel_id"`
	RootID          string `json:"root_id,omitempty"`
	EphemeralPostID string `json:"ephemeral_post_id,omitempty"`
	ExpiresAt       int64  `json:"expires_at"`
}

// Plugin implements plugin.MattermostPlugin.
type Plugin struct {
	plugin.MattermostPlugin
	configLock       sync.RWMutex
	configuration    *Configuration
	transcribeSem    chan struct{} // limits concurrent auto-transcribe goroutines
}

// Configuration from System Console settings.
type Configuration struct {
	MaxRecordingDurationSeconds    string `json:"MaxRecordingDurationSeconds"`
	MaxFileSizeMB                  string `json:"MaxFileSizeMB"`
	MobileTokenTTLSeconds          string `json:"MobileTokenTTLSeconds"`
	AllowedRoles                   string `json:"AllowedRoles"`
	EnableTranscription            bool   `json:"EnableTranscription"`
	TranscriptionProvider          string `json:"TranscriptionProvider"`
	TranscriptionAPIKey            string `json:"TranscriptionAPIKey"`
	TranscriptionServiceURL        string `json:"TranscriptionServiceURL"`
	TranscriptionModel             string `json:"TranscriptionModel"`
	TranscriptionLanguage          string `json:"TranscriptionLanguage"`
	TranscriptionMaxDurationSeconds string `json:"TranscriptionMaxDurationSeconds"`
	AutoTranscribe                 bool   `json:"AutoTranscribe"`
}

func intFromCfg(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return def
	}
	return v
}

func (c *Configuration) getMaxDurationSeconds() int {
	if c == nil {
		return defaultMaxRecordingDurationSeconds
	}
	return intFromCfg(c.MaxRecordingDurationSeconds, defaultMaxRecordingDurationSeconds)
}

func (c *Configuration) getMobileTokenTTLSeconds() int {
	if c == nil {
		return defaultMobileTokenTTLSeconds
	}
	return intFromCfg(c.MobileTokenTTLSeconds, defaultMobileTokenTTLSeconds)
}

func (c *Configuration) getMaxFileSizeBytes() int64 {
	if c == nil {
		return int64(defaultMaxFileSizeMB) << 20
	}
	mb := intFromCfg(c.MaxFileSizeMB, defaultMaxFileSizeMB)
	if mb <= 0 {
		mb = defaultMaxFileSizeMB
	}
	return int64(mb) << 20
}

func (c *Configuration) getTranscriptionMaxDur() int {
	if c == nil {
		return defaultTranscriptionMaxDurSec
	}
	return intFromCfg(c.TranscriptionMaxDurationSeconds, defaultTranscriptionMaxDurSec)
}

func (c *Configuration) getTranscriptionURL() string {
	if c == nil {
		return ""
	}
	provider := strings.TrimSpace(c.TranscriptionProvider)
	switch provider {
	case "deepinfra":
		return "https://api.deepinfra.com/v1/inference/openai/whisper-large-v3-turbo"
	case "openai":
		return "https://api.openai.com/v1/audio/transcriptions"
	case "custom":
		return strings.TrimSpace(c.TranscriptionServiceURL)
	default:
		return "https://api.deepinfra.com/v1/inference/openai/whisper-large-v3-turbo"
	}
}

func (c *Configuration) getTranscriptionModel() string {
	if c == nil || strings.TrimSpace(c.TranscriptionModel) == "" {
		return "openai/whisper-large-v3-turbo"
	}
	return strings.TrimSpace(c.TranscriptionModel)
}

func (p *Plugin) getConfig() *Configuration {
	p.configLock.RLock()
	defer p.configLock.RUnlock()
	if p.configuration == nil {
		return &Configuration{
			MaxRecordingDurationSeconds: strconv.Itoa(defaultMaxRecordingDurationSeconds),
			AllowedRoles:               "all",
		}
	}
	return p.configuration
}

func (p *Plugin) OnConfigurationChange() error {
	var cfg Configuration
	if err := p.API.LoadPluginConfiguration(&cfg); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	p.configLock.Lock()
	p.configuration = &cfg
	p.configLock.Unlock()
	return nil
}

func (p *Plugin) OnActivate() error {
	if err := p.OnConfigurationChange(); err != nil {
		return err
	}
	if err := p.registerSlashCommands(); err != nil {
		return err
	}
	p.transcribeSem = make(chan struct{}, 2) // max 2 concurrent auto-transcriptions
	p.API.LogInfo("Voice Message plugin activated", "version", "2.0.0")
	return nil
}

func (p *Plugin) OnDeactivate() error {
	for _, trig := range []string{commandVoice, commandVM} {
		_ = p.API.UnregisterCommand("", trig)
	}
	return nil
}

func (p *Plugin) registerSlashCommands() error {
	for _, trig := range []string{commandVoice, commandVM} {
		_ = p.API.UnregisterCommand("", trig)
		cmd := &model.Command{
			Trigger:          trig,
			AutoComplete:     true,
			AutoCompleteDesc: "Record a voice message",
			AutoCompleteHint: "",
			DisplayName:      "Voice Message",
		}
		if err := p.API.RegisterCommand(cmd); err != nil {
			return fmt.Errorf("failed to register /%s command: %w", trig, err)
		}
	}
	return nil
}

// ExecuteCommand handles the /voice and /vm slash commands.
func (p *Plugin) ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	split := strings.Fields(args.Command)
	if len(split) == 0 {
		return &model.CommandResponse{}, nil
	}
	trigger := strings.TrimPrefix(split[0], "/")
	if trigger != commandVoice && trigger != commandVM {
		return &model.CommandResponse{}, nil
	}

	if !p.isUserAllowed(args.UserId) {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "⛔ You don't have permission to send voice messages.",
			ChannelId:    args.ChannelId,
		}, nil
	}

	rootID := args.RootId
	tok, err := p.issueMobileToken(args.UserId, args.ChannelId, rootID)
	if err != nil {
		p.API.LogError("failed to issue mobile token", "err", err.Error())
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "Failed to prepare recording. Check server logs.",
			ChannelId:    args.ChannelId,
		}, nil
	}

	recURL := p.buildMobileRecordURL(tok, args.ChannelId, rootID)
	maxDur := p.getConfig().getMaxDurationSeconds()
	maxMin := maxDur / 60
	ttlMin := p.getConfig().getMobileTokenTTLSeconds() / 60

	text := fmt.Sprintf("🎤 **Voice Message**\n\nOpen the recording page:\n%s\n\n*Recording limit: %d min. Link valid for ~%d min (one-time use).*", recURL, maxMin, ttlMin)

	ep := &model.Post{
		UserId:    args.UserId,
		ChannelId: args.ChannelId,
		Message:   text,
	}
	sent := p.API.SendEphemeralPost(args.UserId, ep)
	if sent != nil && sent.Id != "" {
		_ = p.setMobileTokenEphemeralPostID(tok, sent.Id)
	}

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         "",
		GotoLocation: recURL,
		ChannelId:    args.ChannelId,
	}, nil
}

// isUserAllowed checks if the user can use voice messages based on AllowedRoles config.
func (p *Plugin) isUserAllowed(userID string) bool {
	cfg := p.getConfig()
	if cfg.AllowedRoles == "" || cfg.AllowedRoles == "all" {
		return true
	}
	user, appErr := p.API.GetUser(userID)
	if appErr != nil {
		return false
	}
	roles := strings.ToLower(user.Roles)
	return strings.Contains(roles, "system_admin") || strings.Contains(roles, "team_admin")
}

// ServeHTTP routes API requests.
func (p *Plugin) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/api/v1/config"):
		p.handleConfig(w, r)
	case strings.HasPrefix(path, "/api/v1/mobile/upload"):
		p.handleMobileUpload(w, r)
	case strings.HasPrefix(path, "/api/v1/upload"):
		p.handleUpload(w, r)
	case strings.HasPrefix(path, "/api/v1/transcribe"):
		p.handleTranscribe(w, r)
	case strings.HasPrefix(path, "/mobile/record"):
		p.handleMobileRecord(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (p *Plugin) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	cfg := p.getConfig()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"maxDurationSeconds":      cfg.getMaxDurationSeconds(),
		"enableTranscription":     cfg.EnableTranscription,
		"autoTranscribe":          cfg.AutoTranscribe,
		"transcriptionMaxDuration": cfg.getTranscriptionMaxDur(),
	})
}

func (p *Plugin) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if !p.isUserAllowed(userID) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	channelID := r.URL.Query().Get("channel_id")
	if channelID == "" {
		http.Error(w, "channel_id required", http.StatusBadRequest)
		return
	}

	if _, err := p.API.GetChannelMember(channelID, userID); err != nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	rootID := r.URL.Query().Get("root_id")
	durationStr := r.URL.Query().Get("duration")
	if durationStr == "" {
		durationStr = "0"
	}
	if _, err := strconv.ParseFloat(durationStr, 64); err != nil {
		durationStr = "0"
	}

	cfg := p.getConfig()
	r.Body = http.MaxBytesReader(w, r.Body, cfg.getMaxFileSizeBytes())
	data, err := io.ReadAll(r.Body)
	if err != nil || len(data) == 0 {
		http.Error(w, "Failed to read audio data", http.StatusBadRequest)
		return
	}

	ct := r.Header.Get("Content-Type")
	filename := fmt.Sprintf("voice_%s%s", time.Now().Format("20060102_150405"), extForContentType(ct))

	fileInfo, appErr := p.API.UploadFile(data, channelID, filename)
	if appErr != nil {
		p.API.LogError("Upload failed", "err", appErr.Error())
		http.Error(w, "Upload failed", http.StatusInternalServerError)
		return
	}

	post := &model.Post{
		UserId:    userID,
		ChannelId: channelID,
		RootId:    rootID,
		Message:   "",
		FileIds:   []string{fileInfo.Id},
		Type:      "custom_voice_message",
		Props: model.StringInterface{
			"voice_duration":  durationStr,
			"voice_mime_type": ct,
		},
	}

	created, appErr := p.API.CreatePost(post)
	if appErr != nil {
		p.API.LogError("CreatePost failed", "err", appErr.Error())
		http.Error(w, "Failed to create post", http.StatusInternalServerError)
		return
	}

	// Auto-transcribe if configured
	if cfg.EnableTranscription && cfg.AutoTranscribe {
		go p.autoTranscribe(created.Id, fileInfo.Id, data, ct)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"post_id": created.Id,
		"file_id": fileInfo.Id,
	})
}

// handleTranscribe transcribes a voice message via the configured Whisper API.
func (p *Plugin) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	cfg := p.getConfig()
	if !cfg.EnableTranscription {
		http.Error(w, "Transcription is disabled", http.StatusForbidden)
		return
	}

	postID := r.URL.Query().Get("post_id")
	if postID == "" {
		http.Error(w, "post_id required", http.StatusBadRequest)
		return
	}

	post, appErr := p.API.GetPost(postID)
	if appErr != nil {
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	if post.Type != "custom_voice_message" || len(post.FileIds) == 0 {
		http.Error(w, "Not a voice message", http.StatusBadRequest)
		return
	}

	// Verify the requesting user has access to the channel where the voice message was posted.
	if _, appErr := p.API.GetChannelMember(post.ChannelId, userID); appErr != nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Check if already transcribed
	if t, ok := post.Props["voice_transcript"]; ok && t != nil && t != "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"transcript": t,
			"cached":     true,
		})
		return
	}

	// Check duration limit
	durStr, _ := post.Props["voice_duration"].(string)
	dur, _ := strconv.ParseFloat(durStr, 64)
	maxDur := cfg.getTranscriptionMaxDur()
	if maxDur > 0 && dur > float64(maxDur) {
		http.Error(w, fmt.Sprintf("Voice message too long for transcription (%.0fs > %ds limit)", dur, maxDur), http.StatusBadRequest)
		return
	}

	// Get file data
	fileData, appErr := p.API.GetFile(post.FileIds[0])
	if appErr != nil {
		p.API.LogError("GetFile failed", "err", appErr.Error())
		http.Error(w, "Failed to read audio file", http.StatusInternalServerError)
		return
	}

	mimeType := ""
	if m, ok := post.Props["voice_mime_type"].(string); ok {
		mimeType = m
	}

	// Call Whisper API
	transcript, err := p.callWhisperAPI(fileData, mimeType, cfg.TranscriptionProvider)
	if err != nil {
		errStr := err.Error()
		p.API.LogError("Transcription failed", "post_id", postID, "err", errStr)

		userMsg := "Transcription failed."
		switch {
		case strings.HasPrefix(errStr, "config:"):
			userMsg = "Transcription not configured properly."
		case strings.HasPrefix(errStr, "input:"):
			userMsg = "Audio file is empty or unreadable."
		case strings.HasPrefix(errStr, "network:"):
			userMsg = "Could not reach transcription service."
		case strings.Contains(errStr, "status 401") || strings.Contains(errStr, "status 403"):
			userMsg = "Transcription API auth failed."
		case strings.Contains(errStr, "status 429"):
			userMsg = "Rate limit exceeded. Try again later."
		case strings.Contains(errStr, "status 5"):
			userMsg = "Transcription service error."
		case strings.HasPrefix(errStr, "parse_error:"):
			userMsg = "Unexpected response from transcription service."
		}

		// Return as JSON with detail for debugging.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		// Sanitize: strip API key if it leaked into error string.
		safeErr := errStr
		if apiKey := strings.TrimSpace(cfg.TranscriptionAPIKey); apiKey != "" && len(apiKey) > 8 {
			safeErr = strings.ReplaceAll(safeErr, apiKey, "***")
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":  userMsg,
			"detail": safeErr,
		})
		return
	}

	// Save transcript to post props
	post.Props["voice_transcript"] = transcript
	if _, appErr := p.API.UpdatePost(post); appErr != nil {
		p.API.LogError("UpdatePost failed after transcription", "err", appErr.Error())
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"transcript": transcript,
		"cached":     false,
	})
}

// autoTranscribe is called in a goroutine after upload if AutoTranscribe is enabled.
// Uses a semaphore to limit concurrent transcriptions and prevent OOM.
func (p *Plugin) autoTranscribe(postID, fileID string, data []byte, mimeType string) {
	// Non-blocking acquire: if too many transcriptions in flight, skip.
	select {
	case p.transcribeSem <- struct{}{}:
		// acquired
	default:
		p.API.LogWarn("Auto-transcribe skipped: too many in flight", "post_id", postID)
		return
	}
	defer func() { <-p.transcribeSem }()

	time.Sleep(500 * time.Millisecond)

	cfg := p.getConfig()
	if !cfg.EnableTranscription || strings.TrimSpace(cfg.TranscriptionAPIKey) == "" {
		return
	}

	transcript, err := p.callWhisperAPI(data, mimeType, cfg.TranscriptionProvider)
	// Release audio data from this goroutine's scope immediately.
	data = nil

	if err != nil {
		p.API.LogError("Auto-transcription failed", "post_id", postID, "err", err.Error())
		return
	}

	post, appErr := p.API.GetPost(postID)
	if appErr != nil {
		return
	}
	post.Props["voice_transcript"] = transcript
	if _, appErr := p.API.UpdatePost(post); appErr != nil {
		p.API.LogError("UpdatePost failed after auto-transcription", "err", appErr.Error())
	}
}

// callWhisperAPI sends audio data to a Whisper-compatible endpoint and returns the transcript text.
// Retries up to 2 times on transient (5xx / timeout) errors.
func (p *Plugin) callWhisperAPI(audioData []byte, mimeType string, provider string) (string, error) {
	cfg := p.getConfig()
	apiURL := cfg.getTranscriptionURL()
	apiKey := strings.TrimSpace(cfg.TranscriptionAPIKey)
	modelName := cfg.getTranscriptionModel()
	language := strings.TrimSpace(cfg.TranscriptionLanguage)

	if apiURL == "" {
		return "", fmt.Errorf("config: transcription URL not configured")
	}
	if apiKey == "" {
		return "", fmt.Errorf("config: transcription API key not configured")
	}
	if len(audioData) == 0 {
		return "", fmt.Errorf("input: audio data is empty")
	}

	ext := extForContentType(mimeType)
	if ext == ".bin" {
		ext = ".webm"
	}
	filename := "voice" + ext

	isDeepInfra := strings.TrimSpace(provider) == "deepinfra"

	// DeepInfra inference endpoint uses "audio" field; OpenAI-compatible endpoints use "file".
	fieldName := "file"
	if isDeepInfra {
		fieldName = "audio"
	}

	p.API.LogDebug("Transcription request",
		"provider", provider,
		"url", apiURL,
		"field", fieldName,
		"filename", filename,
		"audio_bytes", len(audioData),
		"mime", mimeType,
	)

	var lastErr error
	maxAttempts := 2

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			delay := time.Duration(attempt) * time.Second
			p.API.LogInfo("Transcription retry", "attempt", attempt, "delay", delay.String())
			time.Sleep(delay)
		}

		transcript, retryable, err := p.doWhisperRequest(apiURL, apiKey, fieldName, filename, modelName, language, audioData, isDeepInfra)
		if err == nil {
			return transcript, nil
		}
		lastErr = err
		p.API.LogWarn("Transcription attempt failed",
			"attempt", attempt,
			"retryable", retryable,
			"err", err.Error(),
		)
		if !retryable {
			break
		}
	}

	return "", lastErr
}

// doWhisperRequest performs a single Whisper API call.
// Returns (transcript, retryable, error).
func (p *Plugin) doWhisperRequest(apiURL, apiKey, fieldName, filename, modelName, language string, audioData []byte, isDeepInfra bool) (string, bool, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// CreateFormFile always sets Content-Type: application/octet-stream.
	// DeepInfra needs the real audio MIME type, so we create the part manually.
	partHeader := textproto.MIMEHeader{}
	partHeader.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, filename))
	partHeader.Set("Content-Type", mimeForFilename(filename))

	part, err := writer.CreatePart(partHeader)
	if err != nil {
		return "", false, fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(audioData); err != nil {
		return "", false, fmt.Errorf("write audio data: %w", err)
	}

	// DeepInfra inference endpoint has model in URL; OpenAI-compatible endpoints need these fields.
	if !isDeepInfra {
		_ = writer.WriteField("model", modelName)
		_ = writer.WriteField("response_format", "json")
	}
	if language != "" {
		_ = writer.WriteField("language", language)
	}
	writer.Close()

	req, err := http.NewRequest(http.MethodPost, apiURL, &buf)
	if err != nil {
		return "", false, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// EOF means the server closed connection — likely down, don't retry.
		errMsg := err.Error()
		retryable := !strings.Contains(errMsg, "EOF")
		return "", retryable, fmt.Errorf("network: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", true, fmt.Errorf("read response body: %w", err)
	}

	p.API.LogDebug("Transcription API response",
		"status", resp.StatusCode,
		"body_len", len(body),
		"body_preview", truncate(string(body), 500),
	)

	if resp.StatusCode != http.StatusOK {
		retryable := resp.StatusCode >= 500 || resp.StatusCode == 429
		return "", retryable, fmt.Errorf("api_error: status %d, body: %s", resp.StatusCode, truncate(string(body), 300))
	}

	// Parse response — try "text" field first (standard), then look for segments.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", false, fmt.Errorf("parse_error: invalid JSON: %w (body: %s)", err, truncate(string(body), 200))
	}

	// Try top-level "text" field.
	if textRaw, ok := raw["text"]; ok {
		var text string
		if err := json.Unmarshal(textRaw, &text); err == nil && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text), false, nil
		}
	}

	// Fallback: build text from "segments" array (DeepInfra sometimes returns text="" with segments filled).
	if segRaw, ok := raw["segments"]; ok {
		var segments []struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(segRaw, &segments); err == nil && len(segments) > 0 {
			var parts []string
			for _, seg := range segments {
				if t := strings.TrimSpace(seg.Text); t != "" {
					parts = append(parts, t)
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, " "), false, nil
			}
		}
	}

	return "", false, fmt.Errorf("parse_error: no transcript text found in response (body: %s)", truncate(string(body), 300))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func (p *Plugin) handleMobileRecord(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}

	mt, err := p.getMobileToken(token)
	if err != nil {
		http.Error(w, "token invalid or expired", http.StatusUnauthorized)
		return
	}

	mmUser := r.Header.Get("Mattermost-User-Id")
	if mmUser != "" && mmUser != mt.UserID {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	cfg := p.getConfig()
	maxSeconds := cfg.getMaxDurationSeconds()
	basePath := p.getBasePathFromSiteURL()
	uploadURL := fmt.Sprintf("%s/plugins/%s/api/v1/mobile/upload?token=%s", basePath, pluginID, url.QueryEscape(token))

	channelDisplay := mt.ChannelID
	if ch, appErr := p.API.GetChannel(mt.ChannelID); appErr == nil && ch != nil && ch.DisplayName != "" {
		channelDisplay = ch.DisplayName
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; media-src 'self' blob: data:;")
	_, _ = w.Write([]byte(renderMobileRecordHTML(channelDisplay, mt.ChannelID, mt.RootID, uploadURL, maxSeconds)))
}

func (p *Plugin) handleMobileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}

	mt, err := p.getMobileToken(token)
	if err != nil {
		http.Error(w, "token invalid or expired", http.StatusUnauthorized)
		return
	}

	mmUser := r.Header.Get("Mattermost-User-Id")
	if mmUser != "" && mmUser != mt.UserID {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if mmUser == "" {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" && !p.isAllowedOrigin(origin) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	if _, appErr := p.API.GetChannelMember(mt.ChannelID, mt.UserID); appErr != nil {
		http.Error(w, "not a channel member", http.StatusForbidden)
		return
	}

	cfg := p.getConfig()
	r.Body = http.MaxBytesReader(w, r.Body, cfg.getMaxFileSizeBytes())
	defer r.Body.Close()

	data, err := io.ReadAll(r.Body)
	if err != nil || len(data) == 0 {
		http.Error(w, "Failed to read audio data", http.StatusBadRequest)
		return
	}

	ct := r.Header.Get("Content-Type")
	filename := fmt.Sprintf("voice_%s%s", time.Now().Format("20060102_150405"), extForContentType(ct))

	fileInfo, appErr := p.API.UploadFile(data, mt.ChannelID, filename)
	if appErr != nil {
		p.API.LogError("Upload failed", "err", appErr.Error())
		http.Error(w, "Upload failed", http.StatusInternalServerError)
		return
	}

	post := &model.Post{
		UserId:    mt.UserID,
		ChannelId: mt.ChannelID,
		RootId:    mt.RootID,
		Message:   "",
		FileIds:   []string{fileInfo.Id},
		Type:      "custom_voice_message",
		Props: model.StringInterface{
			"voice_duration":  "0",
			"voice_mime_type": ct,
		},
	}

	created, appErr := p.API.CreatePost(post)
	if appErr != nil {
		p.API.LogError("CreatePost failed", "err", appErr.Error())
		http.Error(w, "Failed to create post", http.StatusInternalServerError)
		return
	}

	_ = p.API.KVDelete(kvMobileTokenPrefix + token)

	if mt.EphemeralPostID != "" {
		successMsg := "✅ Voice message sent."
		if pl := p.buildPostPermalink(created.Id); pl != "" {
			successMsg = successMsg + "\n" + pl
		}
		p.API.UpdateEphemeralPost(mt.UserID, &model.Post{
			Id:        mt.EphemeralPostID,
			UserId:    mt.UserID,
			ChannelId: mt.ChannelID,
			Message:   successMsg,
		})
		go func(uid, pid string) {
			time.Sleep(6 * time.Second)
			p.API.DeleteEphemeralPost(uid, pid)
		}(mt.UserID, mt.EphemeralPostID)
	}

	// Auto-transcribe for mobile uploads too
	if cfg.EnableTranscription && cfg.AutoTranscribe {
		go p.autoTranscribe(created.Id, fileInfo.Id, data, ct)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"post_id":   created.Id,
		"file_id":   fileInfo.Id,
		"permalink": p.buildPostPermalink(created.Id),
	})
}

// ----- Token & URL helpers -----

func (p *Plugin) issueMobileToken(userID, channelID, rootID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	exp := time.Now().Add(time.Duration(p.getConfig().getMobileTokenTTLSeconds()) * time.Second).Unix()
	mt := &mobileToken{UserID: userID, ChannelID: channelID, RootID: rootID, ExpiresAt: exp}
	payload, err := json.Marshal(mt)
	if err != nil {
		return "", err
	}
	if appErr := p.API.KVSet(kvMobileTokenPrefix+tok, payload); appErr != nil {
		return "", fmt.Errorf("KVSet: %s", appErr.Error())
	}
	return tok, nil
}

func (p *Plugin) setMobileTokenEphemeralPostID(token, postID string) error {
	if strings.TrimSpace(token) == "" || strings.TrimSpace(postID) == "" {
		return nil
	}
	mt, err := p.getMobileToken(token)
	if err != nil {
		return err
	}
	mt.EphemeralPostID = postID
	payload, err := json.Marshal(mt)
	if err != nil {
		return err
	}
	if appErr := p.API.KVSet(kvMobileTokenPrefix+token, payload); appErr != nil {
		return fmt.Errorf("KVSet: %s", appErr.Error())
	}
	return nil
}

func (p *Plugin) isAllowedOrigin(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return true
	}
	su := p.getSiteURL()
	if su == "" {
		return true
	}
	site, err := url.Parse(su)
	if err != nil || site.Host == "" {
		return true
	}
	o, err := url.Parse(origin)
	if err != nil || o.Host == "" {
		return true
	}
	return strings.EqualFold(o.Host, site.Host)
}

func (p *Plugin) getMobileToken(token string) (*mobileToken, error) {
	b, appErr := p.API.KVGet(kvMobileTokenPrefix + token)
	if appErr != nil {
		return nil, fmt.Errorf("KVGet: %s", appErr.Error())
	}
	if b == nil {
		return nil, fmt.Errorf("not found")
	}
	var mt mobileToken
	if err := json.Unmarshal(b, &mt); err != nil {
		return nil, err
	}
	if mt.UserID == "" || mt.ChannelID == "" {
		return nil, fmt.Errorf("invalid")
	}
	if time.Now().Unix() >= mt.ExpiresAt {
		_ = p.API.KVDelete(kvMobileTokenPrefix + token)
		return nil, fmt.Errorf("expired")
	}
	return &mt, nil
}

func (p *Plugin) getSiteURL() string {
	cfg := p.API.GetConfig()
	if cfg == nil || cfg.ServiceSettings.SiteURL == nil {
		return ""
	}
	return strings.TrimSpace(*cfg.ServiceSettings.SiteURL)
}

func (p *Plugin) getBasePathFromSiteURL() string {
	su := p.getSiteURL()
	if su == "" {
		return ""
	}
	u, err := url.Parse(su)
	if err != nil {
		return ""
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "/" {
		return ""
	}
	return path
}

func (p *Plugin) buildMobileRecordURL(token, channelID, rootID string) string {
	basePath := p.getBasePathFromSiteURL()
	path := fmt.Sprintf("%s/plugins/%s/mobile/record?token=%s", basePath, pluginID, url.QueryEscape(token))
	if channelID != "" {
		path += "&channel_id=" + url.QueryEscape(channelID)
	}
	if rootID != "" {
		path += "&root_id=" + url.QueryEscape(rootID)
	}
	su := p.getSiteURL()
	if su == "" {
		return path
	}
	u, err := url.Parse(su)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return path
	}
	return fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, path)
}

func (p *Plugin) buildPostPermalink(postID string) string {
	basePath := p.getBasePathFromSiteURL()
	path := fmt.Sprintf("%s/pl/%s", basePath, postID)
	su := p.getSiteURL()
	if su == "" {
		return path
	}
	u, err := url.Parse(su)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return path
	}
	return fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, path)
}

func extForContentType(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "" {
		return ".bin"
	}
	base := strings.TrimSpace(strings.Split(ct, ";")[0])
	switch base {
	case "audio/webm":
		return ".webm"
	case "audio/ogg", "application/ogg":
		return ".ogg"
	case "audio/mp4", "video/mp4":
		return ".m4a"
	case "audio/mpeg":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	default:
		return ".bin"
	}
}

func mimeForFilename(fn string) string {
	fn = strings.ToLower(fn)
	switch {
	case strings.HasSuffix(fn, ".webm"):
		return "audio/webm"
	case strings.HasSuffix(fn, ".ogg"):
		return "audio/ogg"
	case strings.HasSuffix(fn, ".m4a"), strings.HasSuffix(fn, ".mp4"):
		return "audio/mp4"
	case strings.HasSuffix(fn, ".mp3"):
		return "audio/mpeg"
	case strings.HasSuffix(fn, ".wav"):
		return "audio/wav"
	case strings.HasSuffix(fn, ".flac"):
		return "audio/flac"
	default:
		return "application/octet-stream"
	}
}

// renderMobileRecordHTML returns the full HTML for the mobile recording page.
func renderMobileRecordHTML(channelDisplay, channelID, rootID, uploadURL string, maxSeconds int) string {
	maxMin := maxSeconds / 60
	maxSec := maxSeconds % 60

	threadLine := ""
	if rootID != "" {
		threadLine = `<span class="badge badge--thread">Thread reply</span>`
	}

	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"/>
<title>Voice Message</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
:root{
  --bg:#0c1017;--surface:#131a27;--surface2:#182236;
  --border:#1e2d44;--text:#e8edf4;--muted:#8899ad;
  --accent:#3b82f6;--accent-glow:rgba(59,130,246,.25);
  --red:#ef4444;--red-glow:rgba(239,68,68,.2);
  --green:#22c55e;--green-glow:rgba(34,197,94,.15);
  --radius:16px;
}
html,body{height:100%%}
body{
  font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;
  background:var(--bg);color:var(--text);
  padding:env(safe-area-inset-top,16px) 16px env(safe-area-inset-bottom,16px);
  display:flex;align-items:flex-start;justify-content:center;
  min-height:100%%;
}
.container{width:100%%;max-width:460px;margin:16px auto}
.card{
  background:var(--surface);border:1px solid var(--border);
  border-radius:var(--radius);overflow:hidden;
  box-shadow:0 8px 32px rgba(0,0,0,.4);
}
.card-header{
  padding:16px 20px;border-bottom:1px solid var(--border);
  display:flex;align-items:center;gap:12px;
}
.card-header svg{color:var(--accent);flex-shrink:0}
.card-header h1{font-size:17px;font-weight:600;flex:1}
.badge{
  font-size:11px;padding:3px 10px;border-radius:99px;
  background:var(--surface2);border:1px solid var(--border);color:var(--muted);
  white-space:nowrap;
}
.badge--thread{color:var(--accent);border-color:rgba(59,130,246,.3)}
.meta{
  padding:12px 20px;font-size:13px;color:var(--muted);
  border-bottom:1px solid var(--border);line-height:1.5;
  display:flex;align-items:center;gap:8px;flex-wrap:wrap;
}
.meta b{color:var(--text)}

.rec-area{padding:32px 20px;display:flex;flex-direction:column;align-items:center;gap:20px}

.timer{font-size:48px;font-weight:200;font-variant-numeric:tabular-nums;letter-spacing:2px;transition:color .3s}
.timer--rec{color:var(--red)}
.timer-limit{font-size:12px;color:var(--muted);margin-top:-12px}

.rec-btn-wrap{position:relative;display:flex;align-items:center;justify-content:center}
.rec-pulse{
  position:absolute;width:120px;height:120px;border-radius:999px;
  background:var(--red-glow);opacity:0;transform:scale(.8);
  transition:opacity .3s,transform .3s;pointer-events:none;
}
.rec-pulse.active{animation:pulse 1.6s ease-in-out infinite}
@keyframes pulse{
  0%%{transform:scale(.85);opacity:.7}
  50%%{transform:scale(1.2);opacity:0}
  100%%{transform:scale(.85);opacity:0}
}
.rec-btn{
  width:88px;height:88px;border-radius:999px;border:none;
  display:flex;align-items:center;justify-content:center;
  cursor:pointer;position:relative;z-index:1;
  transition:transform .15s,background .3s,box-shadow .3s;
  -webkit-tap-highlight-color:transparent;
}
.rec-btn:active{transform:scale(.92)}
.rec-btn--idle{background:var(--accent);box-shadow:0 4px 20px var(--accent-glow)}
.rec-btn--idle:hover{box-shadow:0 4px 30px rgba(59,130,246,.4)}
.rec-btn--recording{background:var(--red);box-shadow:0 4px 20px var(--red-glow)}
.rec-btn svg{color:#fff;width:28px;height:28px}
.rec-btn .stop-icon{width:22px;height:22px;border-radius:4px;background:#fff}

.level-bars{display:flex;align-items:center;gap:3px;height:40px}
.level-bar{width:4px;border-radius:2px;background:var(--accent);opacity:.3;transition:height .08s}
.level-bar.active{opacity:1}

.actions{display:flex;gap:12px;align-items:center;justify-content:center;flex-wrap:wrap}
.btn{
  display:inline-flex;align-items:center;gap:8px;
  padding:12px 20px;border-radius:12px;border:1px solid var(--border);
  background:var(--surface2);color:var(--text);font-size:14px;font-weight:500;
  cursor:pointer;transition:all .15s;-webkit-tap-highlight-color:transparent;
  white-space:nowrap;
}
.btn:active{transform:scale(.96)}
.btn:disabled{opacity:.4;pointer-events:none}
.btn--primary{background:var(--accent);border-color:var(--accent);color:#fff}
.btn--primary:hover{background:#2563eb}
.btn--danger{border-color:rgba(239,68,68,.4);color:var(--red)}
.btn--danger:hover{background:var(--red-glow)}
.btn--send{background:var(--green);border-color:var(--green);color:#fff}
.btn--send:hover{background:#16a34a}
.btn svg{width:18px;height:18px}

.preview{width:100%%;padding:0 20px}
.preview audio{width:100%%;height:40px;border-radius:8px}

.status-bar{
  margin:0 20px;padding:12px 16px;border-radius:12px;
  border:1px solid var(--border);background:var(--surface2);
  font-size:13px;color:var(--muted);line-height:1.4;
  transition:all .3s;
}
.status-bar.ok{border-color:rgba(34,197,94,.5);color:var(--green);background:var(--green-glow)}
.status-bar.err{border-color:rgba(239,68,68,.5);color:#fca5a5;background:var(--red-glow)}

.divider{height:1px;background:var(--border);margin:0 20px}
.fallback{padding:16px 20px;display:flex;flex-direction:column;gap:8px}
.fallback-hint{font-size:12px;color:var(--muted);line-height:1.4}

.progress-wrap{width:100%%;padding:0 20px}
.progress-bar{height:4px;border-radius:2px;background:var(--surface2);overflow:hidden}
.progress-fill{height:100%%;width:0;background:var(--accent);border-radius:2px;transition:width .3s}

.sent-screen{padding:40px 20px;text-align:center;display:flex;flex-direction:column;align-items:center;gap:16px}
.sent-icon{width:64px;height:64px;border-radius:999px;background:var(--green-glow);display:flex;align-items:center;justify-content:center}
.sent-icon svg{width:32px;height:32px;color:var(--green)}
.sent-text{font-size:16px;font-weight:500}
.sent-sub{font-size:13px;color:var(--muted)}
</style>
</head>
<body>
<div class="container">
<div class="card">
  <div class="card-header">
    <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z"/><path d="M19 10v2a7 7 0 0 1-14 0v-2"/><line x1="12" y1="19" x2="12" y2="22"/></svg>
    <h1>Voice Message</h1>
    <span class="badge">mobile</span>
    %s
  </div>
  <div class="meta">Channel: <b>%s</b> &middot; Limit: <b>%02d:%02d</b></div>

  <div id="mainArea">
    <div class="rec-area">
      <div class="timer" id="timer">00:00</div>
      <div class="timer-limit" id="timerLimit">/ %02d:%02d</div>

      <div class="level-bars" id="levelBars"></div>

      <div class="rec-btn-wrap">
        <div class="rec-pulse" id="pulse"></div>
        <button class="rec-btn rec-btn--idle" id="recBtn">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z"/><path d="M19 10v2a7 7 0 0 1-14 0v-2"/><line x1="12" y1="19" x2="12" y2="22"/></svg>
        </button>
      </div>

      <div class="actions" id="actionsRow">
        <!-- Buttons injected by JS based on state -->
      </div>
    </div>

    <div class="preview" id="previewWrap" style="display:none">
      <audio id="preview" controls></audio>
    </div>

    <div class="progress-wrap" id="progressWrap" style="display:none">
      <div class="progress-bar"><div class="progress-fill" id="progressFill"></div></div>
    </div>

    <div style="height:12px"></div>
    <div class="status-bar" id="status">Tap the microphone button to start recording.</div>
    <div style="height:12px"></div>

    <div class="divider"></div>
    <div class="fallback">
      <button class="btn" id="btnNative">
        <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
        Use system recorder
      </button>
      <input id="fileInput" type="file" accept="audio/*" capture="microphone" style="display:none"/>
      <div class="fallback-hint">If browser recording doesn't work (common in Android WebView), use the system recorder as a fallback.</div>
    </div>
  </div>

  <div id="sentScreen" class="sent-screen" style="display:none">
    <div class="sent-icon">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
    </div>
    <div class="sent-text">Voice message sent!</div>
    <div class="sent-sub">You can close this tab now.</div>
    <a id="sentLink" class="btn btn--primary" style="display:none" href="#">Open message</a>
  </div>
</div>
</div>

<script>
(function(){
  var uploadUrl = %q;
  var maxSeconds = %d;
  var state = 'idle';
  var stream = null, rec = null, chunks = [], blob = null;
  var startedAt = 0, tmr = null, analyser = null, dataArr = null;

  var elTimer = document.getElementById('timer');
  var elTimerLimit = document.getElementById('timerLimit');
  var elStatus = document.getElementById('status');
  var elPreview = document.getElementById('preview');
  var elPreviewWrap = document.getElementById('previewWrap');
  var recBtn = document.getElementById('recBtn');
  var elPulse = document.getElementById('pulse');
  var elActions = document.getElementById('actionsRow');
  var elLevelBars = document.getElementById('levelBars');
  var elProgress = document.getElementById('progressWrap');
  var elProgressFill = document.getElementById('progressFill');
  var elMainArea = document.getElementById('mainArea');
  var elSentScreen = document.getElementById('sentScreen');
  var elSentLink = document.getElementById('sentLink');
  var btnNative = document.getElementById('btnNative');
  var fileInput = document.getElementById('fileInput');

  // Create level bars
  var NUM_BARS = 24;
  for(var i=0;i<NUM_BARS;i++){
    var b = document.createElement('div');
    b.className = 'level-bar';
    b.style.height = '4px';
    elLevelBars.appendChild(b);
  }
  var barEls = elLevelBars.children;

  function pad(n){return String(n).padStart(2,'0')}
  function fmtTime(s){return pad(Math.floor(s/60))+':'+pad(Math.floor(s)%%60)}
  function setStatus(msg,kind){
    elStatus.className='status-bar';
    if(kind)elStatus.classList.add(kind);
    elStatus.innerHTML=msg;
  }

  function renderActions(){
    elActions.innerHTML='';
    if(state==='idle') return;
    if(state==='recording'){
      var bs=document.createElement('button');bs.className='btn btn--danger';bs.textContent='Stop';
      bs.onclick=function(){stopRecording(false)};
      elActions.appendChild(bs);
    }
    if(state==='ready'){
      var br=document.createElement('button');br.className='btn';br.textContent='Re-record';
      br.onclick=resetAll;
      var bd=document.createElement('button');bd.className='btn btn--danger';bd.textContent='Discard';
      bd.onclick=function(){resetAll()};
      var bsnd=document.createElement('button');bsnd.className='btn btn--send';bsnd.textContent='Send';
      bsnd.onclick=send;
      elActions.appendChild(br);
      elActions.appendChild(bd);
      elActions.appendChild(bsnd);
    }
  }

  function setState(next){
    state=next;
    elPreviewWrap.style.display='none';
    elProgress.style.display='none';
    elLevelBars.style.display='none';

    if(state==='idle'){
      recBtn.className='rec-btn rec-btn--idle';
      recBtn.innerHTML='<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z"/><path d="M19 10v2a7 7 0 0 1-14 0v-2"/><line x1="12" y1="19" x2="12" y2="22"/></svg>';
      recBtn.disabled=false;
      elPulse.classList.remove('active');
      elTimer.className='timer';
      elTimer.textContent='00:00';
      elTimerLimit.style.display='';
      setStatus('Tap the microphone button to start recording.',null);
    }
    if(state==='recording'){
      recBtn.className='rec-btn rec-btn--recording';
      recBtn.innerHTML='<div class="stop-icon"></div>';
      recBtn.disabled=false;
      elPulse.classList.add('active');
      elTimer.className='timer timer--rec';
      elLevelBars.style.display='flex';
      setStatus('Recording… Tap stop or wait for limit.',null);
    }
    if(state==='ready'){
      recBtn.className='rec-btn rec-btn--idle';
      recBtn.innerHTML='<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z"/><path d="M19 10v2a7 7 0 0 1-14 0v-2"/><line x1="12" y1="19" x2="12" y2="22"/></svg>';
      recBtn.disabled=true;
      elPulse.classList.remove('active');
      elTimer.className='timer';
      if(blob){
        elPreview.src=URL.createObjectURL(blob);
        elPreviewWrap.style.display='block';
      }
      setStatus('Recording ready. Listen and tap Send.','ok');
    }
    if(state==='uploading'){
      recBtn.disabled=true;
      elProgress.style.display='block';
      setStatus('Uploading…',null);
    }
    if(state==='sent'){
      elMainArea.style.display='none';
      elSentScreen.style.display='flex';
    }
    renderActions();
  }

  function pickMime(){
    var c=['audio/webm;codecs=opus','audio/ogg;codecs=opus','audio/webm','audio/ogg','audio/mp4'];
    for(var i=0;i<c.length;i++){
      try{if(window.MediaRecorder&&MediaRecorder.isTypeSupported(c[i]))return c[i]}catch(e){}
    }
    return '';
  }

  function getCookie(n){
    var a='; '+document.cookie;var p=a.split('; '+n+'=');
    if(p.length<2)return '';return p.pop().split(';').shift()||'';
  }

  function updateTimer(){
    var s=Math.max(0,Math.floor((Date.now()-startedAt)/1000));
    elTimer.textContent=fmtTime(s);
    if(s>=maxSeconds)stopRecording(true);
  }

  function updateLevels(){
    if(!analyser||state!=='recording')return;
    analyser.getByteFrequencyData(dataArr);
    var step=Math.floor(dataArr.length/NUM_BARS);
    for(var i=0;i<NUM_BARS;i++){
      var sum=0;for(var j=0;j<step;j++)sum+=dataArr[i*step+j];
      var avg=sum/step/255;
      var h=Math.max(4,avg*36);
      barEls[i].style.height=h+'px';
      barEls[i].className=avg>.08?'level-bar active':'level-bar';
    }
    if(state==='recording')requestAnimationFrame(updateLevels);
  }

  function startRecording(){
    blob=null;chunks=[];
    navigator.mediaDevices.getUserMedia({audio:true}).then(function(s){
      stream=s;
      var actx=new(window.AudioContext||window.webkitAudioContext)();
      var src=actx.createMediaStreamSource(s);
      analyser=actx.createAnalyser();analyser.fftSize=256;
      src.connect(analyser);
      dataArr=new Uint8Array(analyser.frequencyBinCount);

      var mime=pickMime();
      rec=new MediaRecorder(s,mime?{mimeType:mime}:undefined);
      rec.ondataavailable=function(ev){if(ev.data&&ev.data.size>0)chunks.push(ev.data)};
      rec.onstop=function(){
        try{
          blob=new Blob(chunks,{type:rec.mimeType||(chunks[0]&&chunks[0].type)||'application/octet-stream'});
          cleanup();setState('ready');
        }catch(e){cleanup();setStatus('Failed to build audio: '+e.message,'err');setState('idle')}
      };
      rec.start(250);
      startedAt=Date.now();updateTimer();
      tmr=setInterval(updateTimer,250);
      setState('recording');
      requestAnimationFrame(updateLevels);
    }).catch(function(e){
      cleanup();setStatus('Microphone error: '+(e.message||e),'err');setState('idle');
    });
  }

  function stopRecording(auto){
    if(!rec)return;
    try{rec.stop()}catch(e){}
    if(stream)try{stream.getTracks().forEach(function(t){t.stop()})}catch(e){}
    if(tmr){clearInterval(tmr);tmr=null}
    if(auto)setStatus('Recording limit reached.', null);
  }

  function cleanup(){
    if(stream)try{stream.getTracks().forEach(function(t){t.stop()})}catch(e){}
    stream=null;rec=null;analyser=null;dataArr=null;
    if(tmr){clearInterval(tmr);tmr=null}
  }

  function resetAll(){
    cleanup();chunks=[];blob=null;setState('idle');
  }

  function send(){
    if(!blob){setStatus('No recording.','err');return}
    setState('uploading');
    elProgressFill.style.width='30%%';

    var csrf=getCookie('MMCSRF');
    var h={'Content-Type':blob.type||'application/octet-stream','X-Requested-With':'XMLHttpRequest'};
    if(csrf)h['X-CSRF-Token']=csrf;

    fetch(uploadUrl,{method:'POST',body:blob,credentials:'include',headers:h}).then(function(res){
      elProgressFill.style.width='90%%';
      return res.text().then(function(txt){return{ok:res.ok,status:res.status,txt:txt}});
    }).then(function(r){
      elProgressFill.style.width='100%%';
      if(!r.ok){
        setStatus('Upload error: '+r.status,'err');
        setState('ready');return;
      }
      var data=null;try{data=JSON.parse(r.txt)}catch(e){}
      if(data&&data.permalink){
        elSentLink.href=data.permalink;elSentLink.style.display='inline-flex';
      }
      setState('sent');
    }).catch(function(e){
      setStatus('Network error: '+(e.message||e),'err');setState('ready');
    });
  }

  recBtn.addEventListener('click',function(){
    if(state==='recording'){stopRecording(false);return}
    if(state==='idle')startRecording();
  });

  btnNative.addEventListener('click',function(){try{fileInput.click()}catch(e){}});
  fileInput.addEventListener('change',function(){
    var f=fileInput.files&&fileInput.files[0];if(!f)return;
    blob=f;chunks=[];cleanup();setState('ready');
  });

  setState('idle');
})();
</script>
</body>
</html>`,
		threadLine,
		channelDisplay,
		maxMin, maxSec,
		maxMin, maxSec,
		uploadURL,
		maxSeconds,
	)
}
