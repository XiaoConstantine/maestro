package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/llms/oauth"
)

func testAccountJWT(accountID string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`))
	return "header." + payload + ".signature"
}

func TestLoginOpenAIUsesIndependentStateAndDoesNotPrintTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv("MAESTRO_CREDENTIALS_PATH", path)
	previousPKCE, previousState := generatePKCE, generateState
	previousURL, previousExchange := authorizationURL, exchangeOpenAICode
	previousBrowser := openBrowserURL
	t.Cleanup(func() {
		generatePKCE, generateState = previousPKCE, previousState
		authorizationURL, exchangeOpenAICode = previousURL, previousExchange
		openBrowserURL = previousBrowser
	})
	generatePKCE = func() (string, string, error) { return "secret-verifier", "challenge", nil }
	generateState = func() (string, error) { return "csrf-state", nil }
	authorizationURL = func(challenge, state string) string {
		if challenge != "challenge" || state != "csrf-state" {
			t.Fatalf("authorization args = %q, %q", challenge, state)
		}
		return "https://auth.example/authorize?state=" + state
	}
	exchangeOpenAICode = func(_ context.Context, code, verifier string) (*oauth.OpenAITokenResponse, error) {
		if code != "auth-code" || verifier != "secret-verifier" {
			t.Fatalf("exchange args = %q, %q", code, verifier)
		}
		return &oauth.OpenAITokenResponse{
			AccessToken: "oat-secret", IDToken: testAccountJWT("account-1"),
			RefreshToken: "refresh-secret", ExpiresIn: 3600,
		}, nil
	}
	openBrowserURL = func(string) error {
		go func() {
			client := &http.Client{Timeout: 100 * time.Millisecond}
			for i := 0; i < 40; i++ {
				// A forged callback must not terminate the legitimate flow.
				resp, err := client.Get("http://127.0.0.1:1455/auth/callback?code=forged&state=wrong")
				if err == nil {
					resp.Body.Close()
					resp, err = client.Get("http://127.0.0.1:1455/auth/callback?code=auth-code&state=csrf-state")
					if err == nil {
						resp.Body.Close()
						return
					}
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()
		return nil
	}

	var output bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := LoginOpenAI(ctx, &output); err != nil {
		t.Fatalf("LoginOpenAI() error = %v", err)
	}
	for _, secret := range []string{"secret-verifier", "oat-secret", "refresh-secret"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("LoginOpenAI() output leaked secret: %q", output.String())
		}
	}
	credentials, err := OpenAICredentials(context.Background(), "")
	if err != nil {
		t.Fatalf("OpenAICredentials() error = %v", err)
	}
	if credentials.AccessToken != "oat-secret" || credentials.AccountID != "account-1" {
		t.Fatalf("credentials = %#v", credentials)
	}
}

func TestStoredOpenAICredentialIsProtectedAndLoaded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv("MAESTRO_CREDENTIALS_PATH", path)
	credentialMu.Lock()
	err := saveOpenAILocked(&openAICredential{AccessToken: "oat-secret", RefreshToken: "refresh", AccountID: "account"})
	credentialMu.Unlock()
	if err != nil {
		t.Fatalf("saveOpenAILocked() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("credential mode = %o, want 600", got)
	}
	credentials, err := OpenAICredentials(context.Background(), "")
	if err != nil || credentials.AccessToken != "oat-secret" || credentials.AccountID != "account" {
		t.Fatalf("OpenAICredentials() = %#v, %v", credentials, err)
	}
}

func TestHasOpenAICredentialsDoesNotRefreshExpiredToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv("MAESTRO_CREDENTIALS_PATH", path)
	credentialMu.Lock()
	err := saveOpenAILocked(&openAICredential{
		AccessToken: "expired", RefreshToken: "refresh", AccountID: "account",
		ExpiresAt: time.Now().Add(-time.Minute),
	})
	credentialMu.Unlock()
	if err != nil {
		t.Fatalf("saveOpenAILocked() error = %v", err)
	}
	previous := refreshOpenAIToken
	refreshOpenAIToken = func(context.Context, string) (*oauth.OpenAITokenResponse, error) {
		t.Fatal("credential presence check performed network refresh")
		return nil, nil
	}
	t.Cleanup(func() { refreshOpenAIToken = previous })
	if err := HasOpenAICredentials(); err != nil {
		t.Fatalf("HasOpenAICredentials() error = %v", err)
	}
}

func TestOpenAICredentialsRefreshesAndRotatesCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv("MAESTRO_CREDENTIALS_PATH", path)
	credentialMu.Lock()
	err := saveOpenAILocked(&openAICredential{
		AccessToken: "expired", RefreshToken: "old-refresh", AccountID: "old-account",
		ExpiresAt: time.Now().Add(-time.Minute),
	})
	credentialMu.Unlock()
	if err != nil {
		t.Fatalf("saveOpenAILocked() error = %v", err)
	}

	previous := refreshOpenAIToken
	refreshOpenAIToken = func(_ context.Context, refresh string) (*oauth.OpenAITokenResponse, error) {
		if refresh != "old-refresh" {
			t.Fatalf("refresh token = %q, want old-refresh", refresh)
		}
		return &oauth.OpenAITokenResponse{
			AccessToken: "fresh", IDToken: testAccountJWT("new-account"),
			RefreshToken: "new-refresh", ExpiresIn: 3600,
		}, nil
	}
	t.Cleanup(func() { refreshOpenAIToken = previous })

	credentials, err := OpenAICredentials(context.Background(), "")
	if err != nil {
		t.Fatalf("OpenAICredentials() error = %v", err)
	}
	if credentials.AccessToken != "fresh" || credentials.AccountID != "new-account" {
		t.Fatalf("credentials = %#v", credentials)
	}
	credentialMu.Lock()
	file, err := loadCredentialsLocked()
	credentialMu.Unlock()
	if err != nil {
		t.Fatalf("loadCredentialsLocked() error = %v", err)
	}
	if file.OpenAI.RefreshToken != "new-refresh" {
		t.Fatalf("refresh token = %q, want new-refresh", file.OpenAI.RefreshToken)
	}
}

func TestOpenAICredentialsDoesNotRefreshAlreadyRotatedToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv("MAESTRO_CREDENTIALS_PATH", path)
	credentialMu.Lock()
	err := saveOpenAILocked(&openAICredential{
		AccessToken: "new-token", RefreshToken: "new-refresh", AccountID: "account",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	credentialMu.Unlock()
	if err != nil {
		t.Fatalf("saveOpenAILocked() error = %v", err)
	}
	previous := refreshOpenAIToken
	refreshOpenAIToken = func(context.Context, string) (*oauth.OpenAITokenResponse, error) {
		t.Fatal("refresh called after another request already rotated the rejected token")
		return nil, nil
	}
	t.Cleanup(func() { refreshOpenAIToken = previous })

	credentials, err := OpenAICredentials(context.Background(), "old-rejected-token")
	if err != nil {
		t.Fatalf("OpenAICredentials() error = %v", err)
	}
	if credentials.AccessToken != "new-token" {
		t.Fatalf("access token = %q, want new-token", credentials.AccessToken)
	}
}

func TestCredentialRefreshPreservesAccountWhenTokenOmitsClaim(t *testing.T) {
	credential, err := credentialFromTokens(&oauth.OpenAITokenResponse{
		AccessToken: "opaque-refreshed-token", RefreshToken: "new-refresh", ExpiresIn: 3600,
	}, "old-refresh", "stored-account")
	if err != nil {
		t.Fatalf("credentialFromTokens() error = %v", err)
	}
	if credential.AccountID != "stored-account" {
		t.Fatalf("account id = %q, want stored-account", credential.AccountID)
	}
}

func TestCredentialLockHonorsContextCancellation(t *testing.T) {
	t.Setenv("MAESTRO_CREDENTIALS_PATH", filepath.Join(t.TempDir(), "credentials.json"))
	unlock, err := acquireCredentialLock(context.Background())
	if err != nil {
		t.Fatalf("first acquireCredentialLock() error = %v", err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := acquireCredentialLock(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquireCredentialLock() error = %v, want deadline exceeded", err)
	}
}

func TestLogoutOpenAIRemovesStoredToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv("MAESTRO_CREDENTIALS_PATH", path)
	credentialMu.Lock()
	err := saveOpenAILocked(&openAICredential{AccessToken: "oat-secret", AccountID: "account"})
	credentialMu.Unlock()
	if err != nil {
		t.Fatalf("saveOpenAILocked() error = %v", err)
	}
	if err := LogoutOpenAI(); err != nil {
		t.Fatalf("LogoutOpenAI() error = %v", err)
	}
	if _, err := OpenAICredentials(context.Background(), ""); !os.IsNotExist(err) {
		t.Fatalf("OpenAICredentials() error = %v, want os.ErrNotExist", err)
	}
}
