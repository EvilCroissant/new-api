package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIChatRequestToGeminiLoadsGIFDataURL(t *testing.T) {
	const gifData = "R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"

	var request dto.GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"gemini-3.7-flash",
		"messages":[{
			"role":"user",
			"content":[{
				"type":"image_url",
				"image_url":{"url":"data:image/gif;base64,`+gifData+`"}
			}]
		}]
	}`), &request))

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	converted, err := relayconvert.OpenAIChatRequestToGeminiGenerateContent(ctx, request, nil)

	require.NoError(t, err)
	require.Len(t, converted.Contents, 1)
	require.Len(t, converted.Contents[0].Parts, 1)
	require.NotNil(t, converted.Contents[0].Parts[0].InlineData)
	assert.Equal(t, "image/gif", converted.Contents[0].Parts[0].InlineData.MimeType)
	assert.Equal(t, gifData, converted.Contents[0].Parts[0].InlineData.Data)
}
