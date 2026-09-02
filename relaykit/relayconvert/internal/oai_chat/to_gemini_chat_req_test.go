package oaichat

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	relaymedia "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/media"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIChatRequestToGeminiAcceptsGIFImage(t *testing.T) {
	const gifData = "R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"

	relaymedia.SetMediaResolver(relaymedia.MediaResolver{
		GetBase64Data: func(context.Context, types.FileSource, ...string) (string, string, error) {
			return gifData, "image/gif", nil
		},
	})
	t.Cleanup(func() {
		relaymedia.SetMediaResolver(relaymedia.MediaResolver{})
	})

	var request dto.GeneralOpenAIRequest
	require.NoError(t, kitutil.Unmarshal([]byte(`{
		"model":"gemini-3.7-flash",
		"messages":[{
			"role":"user",
			"content":[{
				"type":"image_url",
				"image_url":{"url":"data:image/gif;base64,`+gifData+`"}
			}]
		}]
	}`), &request))

	converted, err := OpenAIChatRequestToGeminiGenerateContent(context.Background(), request, nil)

	require.NoError(t, err)
	require.Len(t, converted.Contents, 1)
	require.Len(t, converted.Contents[0].Parts, 1)
	require.NotNil(t, converted.Contents[0].Parts[0].InlineData)
	assert.Equal(t, "image/gif", converted.Contents[0].Parts[0].InlineData.MimeType)
	assert.Equal(t, gifData, converted.Contents[0].Parts[0].InlineData.Data)
}
