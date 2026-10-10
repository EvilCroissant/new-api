package common

import (
	"io"
	"mime"
	"mime/multipart"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
)

const requestSnapshotKey = "request_parameter_snapshot"
const requestSnapshotLimit = 8 << 20

// RequestParameterSnapshot deliberately excludes conversation text, credentials,
// URLs, tool definitions and binary payloads. It describes the client request,
// before channel overrides and protocol conversion, not the upstream wire body.
type RequestParameterSnapshot struct {
	Parameters map[string]any `json:"parameters"`
	Content    map[string]any `json:"content,omitempty"`
	Omitted    bool           `json:"omitted,omitempty"`
	Truncated  bool           `json:"truncated,omitempty"`
}

func GetRequestParameterSnapshot(c *gin.Context) *RequestParameterSnapshot {
	if c == nil {
		return nil
	}
	v, _ := c.Get(requestSnapshotKey)
	snapshot, _ := v.(*RequestParameterSnapshot)
	return snapshot
}

// CaptureRequestParameterSnapshot preserves the shared body's cursor. Oversized
// requests are not read a second time just for diagnostics.
func CaptureRequestParameterSnapshot(c *gin.Context) {
	if c == nil || c.Request == nil || c.Request.Method != "POST" || GetRequestParameterSnapshot(c) != nil {
		return
	}
	s := &RequestParameterSnapshot{Parameters: map[string]any{}, Content: map[string]any{}}
	c.Set(requestSnapshotKey, s)
	stored, _ := c.Get(KeyBodyStorage)
	body, exists := stored.(BodyStorage)
	var err error
	if !exists {
		body, err = GetBodyStorage(c)
		if err == nil {
			c.Request.Body = io.NopCloser(body)
		}
	}
	if err != nil {
		s.Omitted = true
		return
	}
	reader, err := body.NewReader()
	if err != nil {
		s.Omitted = true
		return
	}
	defer reader.Close()
	if body.Size() > requestSnapshotLimit {
		s.Truncated = true
		s.Omitted = true
		return
	}
	contentType, params, _ := mime.ParseMediaType(c.Request.Header.Get("Content-Type"))
	values := map[string]any{}
	if contentType == "multipart/form-data" {
		reader := multipart.NewReader(io.LimitReader(reader, requestSnapshotLimit+1), params["boundary"])
		for i := range 128 {
			part, readErr := reader.NextPart()
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				s.Omitted = true
				break
			}
			if i == 127 {
				s.Truncated = true
				break
			}
			if part.FileName() != "" {
				s.Omitted = true
				size, _ := io.Copy(io.Discard, part)
				count, _ := s.Content["files"].(int)
				s.Content["files"] = count + 1
				total, _ := s.Content["file_bytes"].(int64)
				s.Content["file_bytes"] = total + size
				continue
			}
			data, readErr := io.ReadAll(io.LimitReader(part, 1025))
			if readErr != nil || len(data) > 1024 {
				s.Truncated = true
				continue
			}
			values[part.FormName()] = string(data)
		}
	} else if err := DecodeJson(io.LimitReader(reader, requestSnapshotLimit+1), &values); err != nil {
		s.Omitted = true
		return
	}
	s.capture(values)
}

func (s *RequestParameterSnapshot) capture(values map[string]any) {
	for key, value := range values {
		switch key {
		case "model", "stream", "temperature", "top_p", "top_k", "max_tokens", "max_output_tokens", "max_completion_tokens", "presence_penalty", "frequency_penalty", "seed", "n", "reasoning_effort", "service_tier", "size", "quality", "duration", "seconds", "fps", "aspect_ratio", "resolution", "response_modalities":
			if safeSnapshotScalar(value) {
				s.Parameters[key] = value
			} else {
				s.Omitted = true
			}
		case "reasoning", "text", "response_format", "stream_options", "thinking", "generationConfig", "generation_config":
			children, ok := value.(map[string]any)
			if !ok {
				s.Omitted = true
				continue
			}
			out := map[string]any{}
			for child, v := range children {
				if slices.Contains([]string{"effort", "summary", "verbosity", "type", "budget_tokens", "include_usage", "temperature", "topP", "topK", "maxOutputTokens", "candidateCount", "responseMimeType"}, child) && safeSnapshotScalar(v) {
					out[child] = v
				} else {
					s.Omitted = true
				}
			}
			s.Parameters[key] = out
		case "messages", "input", "prompt", "contents", "instructions", "system", "tools", "images", "image", "audio", "video", "files":
			s.Omitted = true
			switch v := value.(type) {
			case string:
				s.Content[key] = map[string]any{"bytes": len(v)}
			case []any:
				s.Content[key] = map[string]any{"items": len(v)}
			case map[string]any:
				s.Content[key] = map[string]any{"fields": len(v)}
			default:
				s.Content[key] = map[string]any{"present": true}
			}
		default:
			s.Omitted = true
		}
	}
}

func safeSnapshotScalar(value any) bool {
	switch v := value.(type) {
	case nil, bool, float64:
		return true
	case string:
		// Business enums/model names do not need arbitrary free text or URLs.
		return len(v) <= 128 && !strings.ContainsAny(v, "\r\n\t") && !strings.Contains(v, "://") && !strings.HasPrefix(v, "data:") && !strings.HasPrefix(v, "sk-") && !strings.Contains(strings.ToLower(v), "bearer ")
	default:
		return false
	}
}
