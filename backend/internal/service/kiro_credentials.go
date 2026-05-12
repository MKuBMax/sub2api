package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	KiroSourceJSON         = "json"
	KiroSourceSQLite       = "sqlite"
	KiroSourceRefreshToken = "refresh_token"

	KiroAuthDesktop    = "kiro_desktop"
	KiroAuthAWSSSOOIDC = "aws_sso_oidc"
)

var (
	kiroSQLiteTokenKeys = []string{
		"kirocli:social:token",
		"kirocli:odic:token",
		"codewhisperer:odic:token",
	}
	kiroSQLiteRegistrationKeys = []string{
		"kirocli:odic:device-registration",
		"codewhisperer:odic:device-registration",
	}
	kiroProfileRegionRE = regexp.MustCompile(`^arn:aws[^:]*:[^:]+:([a-z0-9-]+):`)
)

type KiroNormalizedCredential struct {
	SourceType     string         `json:"source_type"`
	AuthType       string         `json:"auth_type"`
	DisplayName    string         `json:"display_name"`
	Credentials    map[string]any `json:"credentials"`
	Extra          map[string]any `json:"extra,omitempty"`
	WarningMessage string         `json:"warning_message,omitempty"`
}

type KiroCredentialImportRequest struct {
	SourceType    string `json:"source_type" binding:"required"`
	Content       string `json:"content"`
	ContentBase64 string `json:"content_base64"`
	RefreshToken  string `json:"refresh_token"`
	AccessToken   string `json:"access_token"`
	ProfileARN    string `json:"profile_arn"`
	Region        string `json:"region"`
	APIRegion     string `json:"api_region"`
	AuthRegion    string `json:"auth_region"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	MachineID     string `json:"machine_id"`
	KiroVersion   string `json:"kiro_version"`
	CompanionJSON string `json:"companion_json"`
	DefaultName   string `json:"default_name"`
}

type KiroCredentialImportResult struct {
	Accounts []KiroNormalizedCredential `json:"accounts"`
}

func ImportKiroCredentials(ctx context.Context, req KiroCredentialImportRequest) (*KiroCredentialImportResult, error) {
	switch strings.TrimSpace(req.SourceType) {
	case KiroSourceRefreshToken:
		accounts, err := NormalizeKiroRefreshTokens(req)
		if err != nil {
			return nil, err
		}
		return &KiroCredentialImportResult{Accounts: accounts}, nil
	case KiroSourceJSON:
		accounts, err := NormalizeKiroJSONCredentials([]byte(req.Content), []byte(req.CompanionJSON), req)
		if err != nil {
			return nil, err
		}
		return &KiroCredentialImportResult{Accounts: accounts}, nil
	case KiroSourceSQLite:
		sqliteBytes, err := decodeKiroSQLiteContent(req)
		if err != nil {
			return nil, err
		}
		if len(sqliteBytes) == 0 {
			return nil, errors.New("sqlite import requires uploaded file content")
		}
		tmp, err := os.CreateTemp("", "sub2api-kiro-*.sqlite3")
		if err != nil {
			return nil, err
		}
		tmpName := tmp.Name()
		defer os.Remove(tmpName)
		if _, err := tmp.Write(sqliteBytes); err != nil {
			_ = tmp.Close()
			return nil, err
		}
		if err := tmp.Close(); err != nil {
			return nil, err
		}
		cred, err := NormalizeKiroSQLiteCredential(ctx, tmpName, req)
		if err != nil {
			return nil, err
		}
		return &KiroCredentialImportResult{Accounts: []KiroNormalizedCredential{cred}}, nil
	default:
		return nil, fmt.Errorf("unsupported kiro credential source_type %q", req.SourceType)
	}
}

func decodeKiroSQLiteContent(req KiroCredentialImportRequest) ([]byte, error) {
	if strings.TrimSpace(req.ContentBase64) != "" {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.ContentBase64))
		if err != nil {
			return nil, fmt.Errorf("invalid sqlite content_base64: %w", err)
		}
		return b, nil
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, nil
	}
	return []byte(req.Content), nil
}

func NormalizeKiroRefreshTokens(req KiroCredentialImportRequest) ([]KiroNormalizedCredential, error) {
	raw := strings.TrimSpace(req.RefreshToken)
	if raw == "" {
		raw = strings.TrimSpace(req.Content)
	}
	if raw == "" {
		return nil, errors.New("refresh_token is required")
	}
	tokens := splitKiroRefreshTokens(raw)
	if len(tokens) == 0 {
		return nil, errors.New("refresh_token is required")
	}
	out := make([]KiroNormalizedCredential, 0, len(tokens))
	for i, token := range tokens {
		childReq := req
		childReq.RefreshToken = token
		if len(tokens) > 1 {
			childReq.DefaultName = fmt.Sprintf("%s %d", defaultKiroDisplayName(req.DefaultName, "Kiro Desktop"), i+1)
		}
		cred, err := NormalizeKiroRefreshToken(childReq)
		if err != nil {
			return nil, fmt.Errorf("refresh_token #%d: %w", i+1, err)
		}
		out = append(out, cred)
	}
	return out, nil
}

func NormalizeKiroRefreshToken(req KiroCredentialImportRequest) (KiroNormalizedCredential, error) {
	refreshToken := strings.TrimSpace(req.RefreshToken)
	if refreshToken == "" {
		refreshToken = strings.TrimSpace(req.Content)
	}
	if err := validateKiroRefreshToken(refreshToken); err != nil {
		return KiroNormalizedCredential{}, err
	}
	authRegion := defaultKiroRegion(kiroFirstNonEmpty(req.AuthRegion, req.Region))
	apiRegion := defaultKiroRegion(kiroFirstNonEmpty(req.APIRegion, detectRegionFromProfileARN(req.ProfileARN)))
	machineID := resolveKiroMachineID(req.MachineID, refreshToken)
	credentials := map[string]any{
		"source_type":   KiroSourceRefreshToken,
		"auth_type":     KiroAuthDesktop,
		"refresh_token": refreshToken,
		"region":        authRegion,
		"auth_region":   authRegion,
		"api_region":    apiRegion,
		"machine_id":    machineID,
	}
	if v := strings.TrimSpace(req.KiroVersion); v != "" {
		credentials["kiro_version"] = v
	}
	if v := strings.TrimSpace(req.AccessToken); v != "" {
		credentials["access_token"] = v
	}
	if v := strings.TrimSpace(req.ProfileARN); v != "" {
		credentials["profile_arn"] = v
	}
	return KiroNormalizedCredential{
		SourceType:  KiroSourceRefreshToken,
		AuthType:    KiroAuthDesktop,
		DisplayName: defaultKiroDisplayName(req.DefaultName, "Kiro Desktop"),
		Credentials: credentials,
		Extra:       map[string]any{"kiro_source_type": KiroSourceRefreshToken},
	}, nil
}

func NormalizeKiroJSONCredentials(raw, companionRaw []byte, req KiroCredentialImportRequest) ([]KiroNormalizedCredential, error) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("invalid kiro json credentials: %w", err)
	}
	companion, err := parseKiroCompanionJSON(companionRaw)
	if err != nil {
		return nil, err
	}
	entries := collectKiroCredentialEntries(root)
	if len(entries) == 0 {
		return nil, errors.New("kiro json does not contain supported refreshToken credentials")
	}
	out := make([]KiroNormalizedCredential, 0, len(entries))
	for idx, entry := range entries {
		mergeKiroCompanion(entry, companion)
		childReq := req
		childReq.DefaultName = defaultKiroDisplayName(req.DefaultName, "Kiro")
		if len(entries) > 1 {
			childReq.DefaultName = fmt.Sprintf("%s %d", childReq.DefaultName, idx+1)
		}
		cred, err := normalizeKiroCredentialMap(entry, KiroSourceJSON, childReq)
		if err != nil {
			return nil, fmt.Errorf("credential #%d: %w", idx+1, err)
		}
		out = append(out, cred)
	}
	return out, nil
}

func parseKiroCompanionJSON(companionRaw []byte) (map[string]any, error) {
	if len(companionRaw) > 0 && strings.TrimSpace(string(companionRaw)) != "" {
		var companion map[string]any
		if err := json.Unmarshal(companionRaw, &companion); err != nil {
			return nil, fmt.Errorf("invalid companion sso cache json: %w", err)
		}
		return companion, nil
	}
	return nil, nil
}

func collectKiroCredentialEntries(root any) []map[string]any {
	switch x := root.(type) {
	case []any:
		return collectKiroCredentialEntriesFromArray(x)
	case map[string]any:
		for _, key := range []string{"accounts", "credentials"} {
			if arr, ok := x[key].([]any); ok {
				return collectKiroCredentialEntriesFromArray(arr)
			}
		}
		if nested, ok := x["credential"].(map[string]any); ok {
			return []map[string]any{flattenKiroCredentialMap(nested)}
		}
		return []map[string]any{flattenKiroCredentialMap(x)}
	default:
		return nil
	}
}

func collectKiroCredentialEntriesFromArray(items []any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, flattenKiroCredentialMap(m))
		}
	}
	return out
}

func flattenKiroCredentialMap(data map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range data {
		out[k] = v
	}
	for _, key := range []string{"credential", "credentials", "token", "auth"} {
		if nested, ok := data[key].(map[string]any); ok {
			for k, v := range nested {
				if _, exists := out[k]; !exists {
					out[k] = v
				}
			}
		}
	}
	return out
}

func mergeKiroCompanion(data map[string]any, companion map[string]any) {
	if companion == nil {
		return
	}
	for _, pair := range [][2]string{
		{"clientId", "clientId"},
		{"client_id", "clientId"},
		{"clientSecret", "clientSecret"},
		{"client_secret", "clientSecret"},
		{"region", "region"},
		{"startUrl", "startUrl"},
	} {
		if data[pair[1]] == nil && data[pair[0]] == nil && companion[pair[0]] != nil {
			data[pair[1]] = companion[pair[0]]
		}
	}
}

func NormalizeKiroSQLiteCredential(ctx context.Context, path string, req KiroCredentialImportRequest) (KiroNormalizedCredential, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return KiroNormalizedCredential{}, err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return KiroNormalizedCredential{}, err
	}

	tokenJSON, tokenKey, err := firstSQLiteValue(ctx, db, "auth_kv", "key", "value", kiroSQLiteTokenKeys)
	if err != nil {
		return KiroNormalizedCredential{}, err
	}
	var tokenData map[string]any
	if err := json.Unmarshal([]byte(tokenJSON), &tokenData); err != nil {
		return KiroNormalizedCredential{}, fmt.Errorf("invalid sqlite token json: %w", err)
	}

	regJSON, regKey, _ := firstSQLiteValue(ctx, db, "auth_kv", "key", "value", kiroSQLiteRegistrationKeys)
	if regJSON != "" {
		var regData map[string]any
		if err := json.Unmarshal([]byte(regJSON), &regData); err == nil {
			if tokenData["client_id"] == nil && tokenData["clientId"] == nil {
				tokenData["client_id"] = kiroFirstNonEmpty(kiroString(regData["client_id"]), kiroString(regData["clientId"]))
			}
			if tokenData["client_secret"] == nil && tokenData["clientSecret"] == nil {
				tokenData["client_secret"] = kiroFirstNonEmpty(kiroString(regData["client_secret"]), kiroString(regData["clientSecret"]))
			}
			if tokenData["region"] == nil {
				tokenData["region"] = regData["region"]
			}
		}
	}
	if apiRegion := detectKiroAPIRegionFromSQLite(ctx, db); apiRegion != "" && req.APIRegion == "" {
		req.APIRegion = apiRegion
	}
	cred, err := normalizeKiroCredentialMap(tokenData, KiroSourceSQLite, req)
	if err != nil {
		return KiroNormalizedCredential{}, err
	}
	cred.Credentials["sqlite_token_key"] = tokenKey
	if regKey != "" {
		cred.Credentials["sqlite_registration_key"] = regKey
	}
	return cred, nil
}

func normalizeKiroCredentialMap(data map[string]any, sourceType string, req KiroCredentialImportRequest) (KiroNormalizedCredential, error) {
	refreshToken := kiroFirstNonEmpty(kiroString(data["refreshToken"]), kiroString(data["refresh_token"]), req.RefreshToken)
	if err := validateKiroRefreshToken(refreshToken); err != nil {
		return KiroNormalizedCredential{}, err
	}
	accessToken := kiroFirstNonEmpty(kiroString(data["accessToken"]), kiroString(data["access_token"]), req.AccessToken)
	profileARN := kiroFirstNonEmpty(kiroString(data["profileArn"]), kiroString(data["profile_arn"]), req.ProfileARN)
	region := defaultKiroRegion(kiroFirstNonEmpty(kiroString(data["region"]), kiroString(data["ssoRegion"]), kiroString(data["sso_region"]), req.Region))
	authRegion := defaultKiroRegion(kiroFirstNonEmpty(kiroString(data["authRegion"]), kiroString(data["auth_region"]), req.AuthRegion, region))
	apiRegion := defaultKiroRegion(kiroFirstNonEmpty(req.APIRegion, kiroString(data["apiRegion"]), kiroString(data["api_region"]), detectRegionFromProfileARN(profileARN), region))
	clientID := kiroFirstNonEmpty(kiroString(data["clientId"]), kiroString(data["client_id"]), req.ClientID)
	clientSecret := kiroFirstNonEmpty(kiroString(data["clientSecret"]), kiroString(data["client_secret"]), req.ClientSecret)
	clientIDHash := kiroFirstNonEmpty(kiroString(data["clientIdHash"]), kiroString(data["client_id_hash"]))
	authMethod := canonicalKiroAuthMethod(kiroFirstNonEmpty(kiroString(data["authMethod"]), kiroString(data["auth_method"])))
	authType := KiroAuthDesktop
	if authMethod == KiroAuthAWSSSOOIDC || clientID != "" && clientSecret != "" {
		authType = KiroAuthAWSSSOOIDC
	}
	if clientIDHash != "" && (clientID == "" || clientSecret == "") {
		return KiroNormalizedCredential{}, errors.New("enterprise Kiro JSON contains clientIdHash; upload the companion ~/.aws/sso/cache/{clientIdHash}.json or paste client_id/client_secret")
	}
	machineID := resolveKiroMachineID(kiroFirstNonEmpty(kiroString(data["machineId"]), kiroString(data["machine_id"]), req.MachineID), refreshToken)
	kiroVersion := kiroFirstNonEmpty(kiroString(data["kiroVersion"]), kiroString(data["kiro_version"]), req.KiroVersion)

	credentials := map[string]any{
		"source_type":   sourceType,
		"auth_type":     authType,
		"refresh_token": refreshToken,
		"region":        region,
		"auth_region":   authRegion,
		"api_region":    apiRegion,
		"machine_id":    machineID,
	}
	if kiroVersion != "" {
		credentials["kiro_version"] = kiroVersion
	}
	if accessToken != "" {
		credentials["access_token"] = accessToken
	}
	if profileARN != "" {
		credentials["profile_arn"] = profileARN
	}
	if clientID != "" {
		credentials["client_id"] = clientID
	}
	if clientSecret != "" {
		credentials["client_secret"] = clientSecret
	}
	if clientIDHash != "" {
		credentials["client_id_hash"] = clientIDHash
	}
	if expiresAt := kiroFirstNonEmpty(kiroString(data["expiresAt"]), kiroString(data["expires_at"])); expiresAt != "" {
		if t, err := parseKiroTime(expiresAt); err == nil {
			credentials["expires_at"] = t.Format(time.RFC3339)
		} else {
			credentials["expires_at"] = expiresAt
		}
	}
	if scopes, ok := data["scopes"]; ok {
		credentials["scopes"] = scopes
	}
	warning := ""
	if kiroBool(data["disabled"]) {
		warning = "source credential was marked disabled; imported as active so you can test it in sub2api"
		credentials["source_disabled"] = true
	}
	if email := kiroFirstNonEmpty(kiroString(data["email"]), kiroString(data["userEmail"]), kiroString(data["user_email"])); email != "" {
		credentials["email"] = email
	}

	return KiroNormalizedCredential{
		SourceType:     sourceType,
		AuthType:       authType,
		DisplayName:    defaultKiroDisplayName(req.DefaultName, "Kiro"),
		Credentials:    credentials,
		WarningMessage: warning,
		Extra: map[string]any{
			"kiro_source_type": sourceType,
			"kiro_auth_type":   authType,
		},
	}, nil
}

func firstSQLiteValue(ctx context.Context, db *sql.DB, table, keyCol, valueCol string, keys []string) (string, string, error) {
	for _, key := range keys {
		var value string
		q := fmt.Sprintf("SELECT %s FROM %s WHERE %s = ? LIMIT 1", valueCol, table, keyCol)
		err := db.QueryRowContext(ctx, q, key).Scan(&value)
		if err == nil {
			return value, key, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", "", err
		}
	}
	return "", "", errors.New("sqlite database does not contain supported Kiro token keys")
}

func detectKiroAPIRegionFromSQLite(ctx context.Context, db *sql.DB) string {
	var raw string
	err := db.QueryRowContext(ctx, "SELECT value FROM state WHERE key = ? LIMIT 1", "api.codewhisperer.profile").Scan(&raw)
	if err != nil || raw == "" {
		return ""
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err == nil {
		for _, key := range []string{"profileArn", "profile_arn", "arn"} {
			if region := detectRegionFromProfileARN(kiroString(data[key])); region != "" {
				return region
			}
		}
	}
	return detectRegionFromProfileARN(raw)
}

func detectRegionFromProfileARN(arn string) string {
	m := kiroProfileRegionRE.FindStringSubmatch(strings.TrimSpace(arn))
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

func splitKiroRefreshTokens(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if token := strings.TrimSpace(field); token != "" {
			out = append(out, token)
		}
	}
	return out
}

func validateKiroRefreshToken(refreshToken string) error {
	token := strings.TrimSpace(refreshToken)
	if token == "" {
		return errors.New("kiro credentials missing refreshToken/refresh_token")
	}
	if len(token) < 100 || strings.Contains(token, "...") || strings.HasSuffix(token, "…") {
		return errors.New("kiro refresh token looks truncated; paste the full token value, not a redacted preview")
	}
	return nil
}

func canonicalKiroAuthMethod(method string) string {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "idc", "iam", "builder-id", "builder_id", "aws_sso", "aws-sso", "aws_sso_oidc":
		return KiroAuthAWSSSOOIDC
	default:
		return KiroAuthDesktop
	}
}

func resolveKiroMachineID(configured, refreshToken string) string {
	if normalized := normalizeKiroMachineID(configured); normalized != "" {
		return normalized
	}
	sum := sha256.Sum256([]byte("KotlinNativeAPI/" + strings.TrimSpace(refreshToken)))
	return fmt.Sprintf("%x", sum[:])
}

func normalizeKiroMachineID(machineID string) string {
	trimmed := strings.TrimSpace(machineID)
	if len(trimmed) == 64 && isKiroHex(trimmed) {
		return strings.ToLower(trimmed)
	}
	withoutDashes := strings.ReplaceAll(trimmed, "-", "")
	if len(withoutDashes) == 32 && isKiroHex(withoutDashes) {
		return strings.ToLower(withoutDashes + withoutDashes)
	}
	return ""
}

func isKiroHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func parseKiroTime(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, errors.New("empty time")
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unsupported time format %q", raw)
}

func defaultKiroRegion(region string) string {
	if strings.TrimSpace(region) != "" {
		return strings.TrimSpace(region)
	}
	return "us-east-1"
}

func defaultKiroDisplayName(name, fallback string) string {
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return fallback
}

func kiroFirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func kiroString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	default:
		return ""
	}
}

func kiroBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(strings.TrimSpace(x), "true") || strings.TrimSpace(x) == "1"
	default:
		return false
	}
}
