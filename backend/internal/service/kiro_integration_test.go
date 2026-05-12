package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeKiroJSONCredentialsArrayWithCompanion(t *testing.T) {
	refreshToken := strings.Repeat("r", 128)
	raw := `{
		"accounts": [{
			"email": "user@example.com",
			"credentials": {
				"refreshToken": "` + refreshToken + `",
				"authMethod": "idc",
				"clientIdHash": "abc123",
				"profileArn": "arn:aws:codewhisperer:eu-west-1:123456789012:profile/test",
				"region": "us-east-1",
				"machineId": "2582956e-cc88-4669-b546-07adbffcb894"
			}
		}]
	}`
	companion := `{"clientId":"client-1","clientSecret":"secret-1"}`

	result, err := NormalizeKiroJSONCredentials([]byte(raw), []byte(companion), KiroCredentialImportRequest{DefaultName: "Kiro Test"})
	require.NoError(t, err)
	require.Len(t, result, 1)

	cred := result[0]
	require.Equal(t, KiroAuthAWSSSOOIDC, cred.AuthType)
	require.Equal(t, "Kiro Test", cred.DisplayName)
	require.Equal(t, "client-1", cred.Credentials["client_id"])
	require.Equal(t, "secret-1", cred.Credentials["client_secret"])
	require.Equal(t, "us-east-1", cred.Credentials["auth_region"])
	require.Equal(t, "eu-west-1", cred.Credentials["api_region"])
	require.Equal(t, "2582956ecc884669b54607adbffcb8942582956ecc884669b54607adbffcb894", cred.Credentials["machine_id"])
	require.Equal(t, "user@example.com", cred.Credentials["email"])
}

func TestNormalizeKiroRefreshTokensRejectsTruncatedPreview(t *testing.T) {
	_, err := NormalizeKiroRefreshTokens(KiroCredentialImportRequest{
		RefreshToken: "eyJhbGciOiJIUzI1NiJ9...",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "looks truncated")
}

func TestBuildKiroPayloadAlternatesAndKeepsImages(t *testing.T) {
	imageData := "data:image/png;base64," + strings.Repeat("a", 120)
	payload := buildKiroPayload("auto-kiro", "system note", []map[string]any{
		{"role": "assistant", "content": "previous assistant"},
		{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "first user"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageData}},
		}},
		{"role": "user", "content": "second user"},
	}, nil)

	state := payload["conversationState"].(map[string]any)
	history := state["history"].([]any)
	require.Len(t, history, 4)
	require.Contains(t, history[0].(map[string]any), "userInputMessage")
	require.Contains(t, history[1].(map[string]any), "assistantResponseMessage")
	require.Contains(t, history[2].(map[string]any), "userInputMessage")
	require.Contains(t, history[3].(map[string]any), "assistantResponseMessage")

	firstUser := history[2].(map[string]any)["userInputMessage"].(map[string]any)
	require.Equal(t, "auto", firstUser["modelId"])
	require.Len(t, firstUser["images"], 1)

	current := state["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	require.Equal(t, "auto", current["modelId"])
	require.Equal(t, "system note\n\nsecond user", current["content"])
}

func TestKiroResolveModelAliases(t *testing.T) {
	require.Equal(t, "auto", kiroResolveModel("auto-kiro"))
	require.Equal(t, "claude-haiku-4.5", kiroResolveModel("claude-haiku-4-5-latest"))
	require.Equal(t, "CLAUDE_3_7_SONNET_20250219_V1_0", kiroResolveModel("claude-3-7-sonnet-20250219"))
	require.Equal(t, "claude-opus-4.5", kiroResolveModel("claude-4.5-opus-high"))
	require.Equal(t, "claude-sonnet-4.5", kiroResolveModel("claude-sonnet-4-5-20250929"))
	require.Equal(t, "claude-opus-4.5", kiroResolveModel("us.anthropic.claude-opus-4-5-20251101-v1:0"))
	require.Equal(t, "claude-opus-4.7", kiroResolveModel("claude-opus-4-7"))
}

func TestKiroTestModelFallsBackForUnsupportedClaudeModels(t *testing.T) {
	require.Equal(t, "auto-kiro", kiroTestModel(""))
	require.Equal(t, "claude-opus-4.7", kiroTestModel("claude-opus-4-7"))
	require.Equal(t, "claude-sonnet-4.5", kiroTestModel("claude-sonnet-4-5-20250929"))
	require.Equal(t, "claude-opus-4.5", kiroTestModel("us.anthropic.claude-opus-4-5-20251101-v1:0"))
	require.Equal(t, "auto-kiro", kiroTestModel("claude-unknown-5"))
}

func TestKiroDefaultModelsUseKiroModelIDs(t *testing.T) {
	var ids []string
	for _, model := range KiroDefaultModels() {
		ids = append(ids, model.ID)
	}

	require.Contains(t, ids, "auto-kiro")
	require.Contains(t, ids, "claude-sonnet-4.5")
	require.Contains(t, ids, "claude-opus-4.7")
	require.Contains(t, ids, "claude-opus-4.6")
	require.NotContains(t, ids, "claude-opus-4-7")
	require.NotContains(t, ids, "claude-sonnet-4-6")
}
