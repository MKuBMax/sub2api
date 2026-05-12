package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const kiroTokenRefreshSkew = 3 * time.Minute

type KiroTokenProvider struct {
	accountRepo  AccountRepository
	httpUpstream HTTPUpstream
}

func NewKiroTokenProvider(accountRepo AccountRepository, httpUpstream HTTPUpstream) *KiroTokenProvider {
	return &KiroTokenProvider{accountRepo: accountRepo, httpUpstream: httpUpstream}
}

func (p *KiroTokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformKiro || account.Type != AccountTypeKiro {
		return "", errors.New("not a kiro account")
	}
	accessToken := strings.TrimSpace(account.GetCredential("access_token"))
	expiresAt := account.GetCredentialAsTime("expires_at")
	if accessToken != "" && expiresAt != nil && time.Until(*expiresAt) > kiroTokenRefreshSkew {
		return accessToken, nil
	}
	if err := p.Refresh(ctx, account); err != nil {
		if accessToken != "" && expiresAt != nil && time.Now().Before(*expiresAt) {
			return accessToken, nil
		}
		return "", err
	}
	return strings.TrimSpace(account.GetCredential("access_token")), nil
}

func (p *KiroTokenProvider) Refresh(ctx context.Context, account *Account) error {
	authType := strings.TrimSpace(account.GetCredential("auth_type"))
	if authType == "" {
		authType = KiroAuthDesktop
	}
	var (
		url     string
		payload map[string]any
		headers = http.Header{
			"Accept":          []string{"application/json"},
			"Content-Type":    []string{"application/json"},
			"Connection":      []string{"close"},
			"Accept-Encoding": []string{"identity"},
		}
	)
	switch authType {
	case KiroAuthAWSSSOOIDC:
		clientID := strings.TrimSpace(account.GetCredential("client_id"))
		clientSecret := strings.TrimSpace(account.GetCredential("client_secret"))
		refreshToken := strings.TrimSpace(account.GetCredential("refresh_token"))
		if clientID == "" || clientSecret == "" || refreshToken == "" {
			return errors.New("kiro aws_sso_oidc credentials require client_id, client_secret, and refresh_token")
		}
		region := kiroAuthRegion(account)
		url = fmt.Sprintf("https://oidc.%s.amazonaws.com/token", region)
		payload = map[string]any{
			"grantType":    "refresh_token",
			"clientId":     clientID,
			"clientSecret": clientSecret,
			"refreshToken": refreshToken,
		}
		headers.Set("Host", fmt.Sprintf("oidc.%s.amazonaws.com", region))
		headers.Set("X-Amz-User-Agent", kiroXAmzUserAgent(account))
		headers.Set("User-Agent", kiroUserAgent(account))
		headers.Set("Amz-Sdk-Invocation-Id", uuid.NewString())
		headers.Set("Amz-Sdk-Request", "attempt=1; max=3")
	case KiroAuthDesktop:
		refreshToken := strings.TrimSpace(account.GetCredential("refresh_token"))
		if refreshToken == "" {
			return errors.New("kiro desktop credentials require refresh_token")
		}
		region := kiroAuthRegion(account)
		url = fmt.Sprintf("https://prod.%s.auth.desktop.kiro.dev/refreshToken", region)
		payload = map[string]any{"refreshToken": refreshToken}
		headers.Set("Host", fmt.Sprintf("prod.%s.auth.desktop.kiro.dev", region))
		headers.Set("User-Agent", fmt.Sprintf("KiroIDE-%s-%s", kiroVersion(account), kiroMachineID(account)))
	default:
		return fmt.Errorf("unsupported kiro auth_type %q", authType)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header = headers
	resp, err := p.do(ctx, account, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusBadRequest {
			if msg := kiroTokenRefreshBadRequestMessage(respBody); msg != "" {
				return errors.New(msg)
			}
		}
		return fmt.Errorf("kiro token refresh failed: status=%d body=%s", resp.StatusCode, truncateForError(respBody))
	}
	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("decode kiro token refresh response: %w", err)
	}
	newAccess := strings.TrimSpace(kiroFirstNonEmpty(
		kiroString(result["accessToken"]),
		kiroString(result["access_token"]),
	))
	if newAccess == "" {
		return fmt.Errorf("kiro token refresh response missing accessToken/access_token")
	}
	if account.Credentials == nil {
		account.Credentials = map[string]any{}
	}
	account.Credentials["access_token"] = newAccess
	if rt := strings.TrimSpace(kiroFirstNonEmpty(kiroString(result["refreshToken"]), kiroString(result["refresh_token"]))); rt != "" {
		account.Credentials["refresh_token"] = rt
	}
	if profileARN := strings.TrimSpace(kiroString(result["profileArn"])); profileARN != "" {
		account.Credentials["profile_arn"] = profileARN
	}
	expiresIn := int64(3600)
	switch v := firstKiroMapValue(result, "expiresIn", "expires_in").(type) {
	case float64:
		expiresIn = int64(v)
	case json.Number:
		if parsed, err := v.Int64(); err == nil {
			expiresIn = parsed
		}
	}
	ttl := expiresIn - 60
	if ttl < 60 {
		ttl = 60
	}
	account.Credentials["expires_at"] = time.Now().UTC().Add(time.Duration(ttl) * time.Second).Format(time.RFC3339)
	if p.accountRepo != nil {
		return persistAccountCredentials(ctx, p.accountRepo, account, account.Credentials)
	}
	return nil
}

func firstKiroMapValue(data map[string]any, keys ...string) any {
	for _, key := range keys {
		if v, ok := data[key]; ok {
			return v
		}
	}
	return nil
}

func (p *KiroTokenProvider) do(ctx context.Context, account *Account, req *http.Request) (*http.Response, error) {
	proxyURL := ""
	if account != nil && account.Proxy != nil && account.Proxy.IsActive() {
		proxyURL = account.Proxy.URL()
	}
	if p.httpUpstream != nil {
		return p.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	}
	return http.DefaultClient.Do(req)
}

func truncateForError(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 512 {
		return s[:512]
	}
	return s
}

func kiroTokenRefreshBadRequestMessage(body []byte) string {
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return ""
	}
	errorCode := strings.ToLower(kiroFirstNonEmpty(kiroString(data["error"]), kiroString(data["code"])))
	errorDescription := kiroFirstNonEmpty(kiroString(data["error_description"]), kiroString(data["message"]))
	if strings.Contains(errorCode, "invalid") || strings.Contains(strings.ToLower(errorDescription), "invalid") {
		return "kiro refresh token is invalid or has been replaced by a newer login; re-import the latest Kiro credentials"
	}
	return ""
}

func kiroAuthRegion(account *Account) string {
	if account != nil {
		if v := strings.TrimSpace(account.GetCredential("auth_region")); v != "" {
			return v
		}
		if v := strings.TrimSpace(account.GetCredential("region")); v != "" {
			return v
		}
	}
	return "us-east-1"
}

func kiroVersion(account *Account) string {
	if account != nil {
		if v := strings.TrimSpace(account.GetCredential("kiro_version")); v != "" {
			return v
		}
	}
	return "0.7.45"
}

func kiroMachineID(account *Account) string {
	if account != nil {
		if v := normalizeKiroMachineID(account.GetCredential("machine_id")); v != "" {
			return v
		}
		if v := strings.TrimSpace(account.GetCredential("refresh_token")); v != "" {
			return resolveKiroMachineID("", v)
		}
	}
	return resolveKiroMachineID("", uuid.NewString())
}

func kiroXAmzUserAgent(account *Account) string {
	return fmt.Sprintf("aws-sdk-js/1.0.34 KiroIDE-%s-%s", kiroVersion(account), kiroMachineID(account))
}

func kiroUserAgent(account *Account) string {
	return fmt.Sprintf("aws-sdk-js/1.0.34 ua/2.1 os/Windows_NT lang/js md/nodejs#24.0.0 api/codewhispererstreaming#1.0.34 m/E KiroIDE-%s-%s", kiroVersion(account), kiroMachineID(account))
}
