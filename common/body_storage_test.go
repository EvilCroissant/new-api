package common

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewReplayableBodyReaderKeepsStorageLifecycleWithCaller(t *testing.T) {
	payload := []byte(`{"model":"test-model","input":"hello"}`)
	storage, err := CreateBodyStorage(payload)
	require.NoError(t, err)
	defer storage.Close()

	body := NewReplayableBodyReader(storage)
	assert.EqualValues(t, len(payload), body.Size())
	_, exposesCloser := any(body).(io.Closer)
	assert.False(t, exposesCloser, "the request body must not expose the storage closer")

	req, err := http.NewRequest(http.MethodPost, "https://example.com", body)
	require.NoError(t, err)
	require.NoError(t, req.Body.Close())

	replayBody, err := body.NewReader()
	require.NoError(t, err, "closing the HTTP request body must not close the storage")
	replay, err := io.ReadAll(replayBody)
	require.NoError(t, err)
	require.NoError(t, replayBody.Close())
	assert.Equal(t, payload, replay)

	require.NoError(t, storage.Close())
	_, err = body.NewReader()
	require.ErrorIs(t, err, ErrStorageClosed)
}

func TestCaptureRequestParameterSnapshotExcludesContentAndPreservesBody(t *testing.T) {
	payload := []byte(`{"model":"gpt-test","temperature":0,"messages":[{"role":"user","content":"private prompt"}],"api_key":"secret-key"}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = request
	defer CleanupBodyStorage(ctx)

	CaptureRequestParameterSnapshot(ctx)
	snapshot := GetRequestParameterSnapshot(ctx)
	require.NotNil(t, snapshot)
	assert.Equal(t, "gpt-test", snapshot.Parameters["model"])
	assert.Equal(t, float64(0), snapshot.Parameters["temperature"])
	assert.Equal(t, map[string]any{"items": 1}, snapshot.Content["messages"])
	assert.True(t, snapshot.Omitted)
	encoded, err := Marshal(snapshot)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "private prompt")
	assert.NotContains(t, string(encoded), "secret-key")

	body, err := io.ReadAll(ctx.Request.Body)
	require.NoError(t, err)
	assert.Equal(t, payload, body)
}
