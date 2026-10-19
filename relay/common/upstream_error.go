package common

import (
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/tidwall/gjson"
)

// UpstreamErrorDetails contains only bounded, masked diagnostics. Protocol
// error codes are open-ended; they do not determine routing or billing here.
type UpstreamErrorDetails struct {
	Code       string `json:"code,omitempty"`
	Type       string `json:"type,omitempty"`
	Message    string `json:"message,omitempty"`
	Param      string `json:"param,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
}

// ParseUpstreamError recognizes OpenAI-compatible HTTP bodies and SSE payloads.
// eventType is the SSE event field, or "error" for a bare HTTP error object.
func ParseUpstreamError(data, eventType string, secrets ...string) *UpstreamErrorDetails {
	if !gjson.Valid(data) {
		return nil
	}
	event := gjson.Parse(data)
	if !event.IsObject() {
		return nil
	}
	errObject := event.Get("error")
	flatError := false
	if !errObject.IsObject() && errObject.Type != gjson.String {
		errObject = event.Get("response.error")
	}
	if !errObject.IsObject() && errObject.Type != gjson.String {
		payloadType := event.Get("type").String()
		switch payloadType {
		case "error", "response.error", "upstream_error", "response.failed", "response.done", "response.completed":
			eventType = payloadType
		}
		switch eventType {
		case "error", "response.error", "upstream_error", "response.failed":
			errObject = event
		case "response.done", "response.completed":
			if event.Get("response.status").String() != "failed" {
				return nil
			}
			errObject = event
		default:
			return nil
		}
		flatError = true
	}
	details := &UpstreamErrorDetails{}
	if errObject.Type == gjson.String {
		details.Message = errObject.String()
	} else {
		if code := errObject.Get("code"); code.Type == gjson.String || code.Type == gjson.Number {
			details.Code = code.String()
		}
		if errorType := errObject.Get("type"); errorType.Type == gjson.String {
			details.Type = errorType.String()
		}
		if message := errObject.Get("message"); message.Type == gjson.String {
			details.Message = message.String()
		}
		if param := errObject.Get("param"); param.Type == gjson.String {
			details.Param = param.String()
		}
	}
	if flatError {
		switch details.Type {
		case "error", "response.error", "upstream_error", "response.failed", "response.done", "response.completed":
			details.Type = ""
		}
	}
	// Stream failures usually arrive over HTTP 200. Record only an explicit
	// error status from the payload, without inferring one from a code or text.
	for _, status := range []gjson.Result{errObject.Get("status_code"), errObject.Get("status"), event.Get("status_code"), event.Get("status")} {
		if status.Type == gjson.Number && status.Int() >= 400 && status.Int() <= 599 && status.Float() == float64(status.Int()) {
			details.StatusCode = int(status.Int())
			break
		}
	}
	for _, field := range []*string{&details.Code, &details.Type, &details.Message, &details.Param} {
		text := *field
		for _, secret := range secrets {
			if secret != "" {
				text = strings.ReplaceAll(text, secret, "***")
			}
		}
		text = strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, common.MaskSensitiveInfo(text, secrets...)))
		if runes := []rune(text); len(runes) > 2048 {
			text = string(runes[:2048]) + "…"
		}
		*field = text
	}
	if details.Code == "" && details.Type == "" && details.Message == "" && details.Param == "" && details.StatusCode == 0 {
		return nil
	}
	return details
}
