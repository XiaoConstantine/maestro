package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/llms/oauth"
	"github.com/gofrs/flock"
)

const (
	credentialVersion = 1
	refreshSkew       = 5 * time.Minute
)

var (
	credentialMu       sync.Mutex
	generatePKCE       = oauth.GeneratePKCE
	generateState      = oauth.GenerateState
	authorizationURL   = oauth.GetOpenAIAuthorizationURLWithState
	exchangeOpenAICode = oauth.ExchangeOpenAICodeContext
	refreshOpenAIToken = oauth.RefreshOpenAIAccessTokenContext
	openBrowserURL     = openBrowser
)

type openAICredential struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	AccountID    string    `json:"account_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type credentialFile struct {
	Version int               `json:"version"`
	OpenAI  *openAICredential `json:"openai,omitempty"`
}

func CredentialPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("MAESTRO_CREDENTIALS_PATH")); path != "" {
		return filepath.Abs(path)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".maestro", "credentials.json"), nil
}

func LoginOpenAI(ctx context.Context, output io.Writer) error {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return fmt.Errorf("generate OpenAI PKCE challenge: %w", err)
	}
	state, err := generateState()
	if err != nil {
		return fmt.Errorf("generate OpenAI OAuth state: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:1455")
	if err != nil {
		return fmt.Errorf("listen for OpenAI OAuth callback on 127.0.0.1:1455: %w", err)
	}
	defer listener.Close()

	type callbackResult struct {
		code string
		err  error
	}
	result := make(chan callbackResult, 1)
	var complete sync.Once
	publish := func(value callbackResult) {
		complete.Do(func() { result <- value })
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("state") != state {
			http.Error(w, "OAuth state mismatch. Return to Maestro.", http.StatusBadRequest)
			return
		}
		if oauthErr := strings.TrimSpace(query.Get("error")); oauthErr != "" {
			publish(callbackResult{err: fmt.Errorf("OpenAI authorization failed: %s", oauthErr)})
			http.Error(w, "OpenAI authorization failed. Return to Maestro.", http.StatusBadRequest)
			return
		}
		code := strings.TrimSpace(query.Get("code"))
		if code == "" {
			publish(callbackResult{err: errors.New("OpenAI OAuth callback did not include a code")})
			http.Error(w, "Missing authorization code. Return to Maestro.", http.StatusBadRequest)
			return
		}
		publish(callbackResult{code: code})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html><body><h1>Maestro connected</h1><p>You can close this window and return to Maestro.</p></body></html>")
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serveDone := make(chan error, 1)
	go func() {
		serveErr := server.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serveDone <- serveErr
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = server.Shutdown(shutdownCtx)
		cancel()
	}()

	authURL := authorizationURL(challenge, state)
	if output != nil {
		fmt.Fprintln(output, "Opening OpenAI authorization in your browser.")
		fmt.Fprintf(output, "If it does not open, visit:\n%s\n", authURL)
	}
	_ = openBrowserURL(authURL)

	var code string
	select {
	case callback := <-result:
		if callback.err != nil {
			return callback.err
		}
		code = callback.code
	case serveErr := <-serveDone:
		return fmt.Errorf("serve OpenAI OAuth callback: %w", serveErr)
	case <-ctx.Done():
		return fmt.Errorf("wait for OpenAI authorization: %w", ctx.Err())
	}

	tokens, err := exchangeOpenAICode(ctx, code, verifier)
	if err != nil {
		return fmt.Errorf("exchange OpenAI authorization code: %w", err)
	}
	credential, err := credentialFromTokens(tokens, "", "")
	if err != nil {
		return err
	}
	credentialMu.Lock()
	defer credentialMu.Unlock()
	unlock, err := acquireCredentialLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err := saveOpenAILocked(credential); err != nil {
		return err
	}
	if output != nil {
		path, _ := CredentialPath()
		fmt.Fprintf(output, "OpenAI ChatGPT subscription connected. Credentials saved to %s.\n", path)
	}
	return nil
}

// HasOpenAICredentials checks the atomically stored credential without refreshing
// or waiting for the cross-process refresh lock.
func HasOpenAICredentials() error {
	file, err := loadCredentialsLocked()
	if err != nil {
		return err
	}
	if file.OpenAI == nil || strings.TrimSpace(file.OpenAI.AccessToken) == "" || strings.TrimSpace(file.OpenAI.AccountID) == "" {
		return os.ErrNotExist
	}
	return nil
}

// OpenAICredentials resolves current subscription credentials for a DSPy-Go
// openai-codex request. rejectedAccessToken correlates an authentication failure
// with the token that failed, avoiding duplicate concurrent refresh rotation.
func OpenAICredentials(ctx context.Context, rejectedAccessToken string) (llms.OpenAICodexCredentials, error) {
	credentialMu.Lock()
	defer credentialMu.Unlock()

	unlock, err := acquireCredentialLock(ctx)
	if err != nil {
		return llms.OpenAICodexCredentials{}, err
	}
	defer unlock()

	file, err := loadCredentialsLocked()
	if err != nil {
		return llms.OpenAICodexCredentials{}, err
	}
	credential := file.OpenAI
	if credential == nil || credential.AccessToken == "" || credential.AccountID == "" {
		return llms.OpenAICodexCredentials{}, os.ErrNotExist
	}
	// Refresh after a 401 only if storage still contains the rejected token.
	// Another thread or process may already have rotated it while this request
	// was in flight.
	needsRefresh := !credential.ExpiresAt.IsZero() && time.Until(credential.ExpiresAt) <= refreshSkew
	if rejectedAccessToken != "" && credential.AccessToken == rejectedAccessToken {
		needsRefresh = true
	}
	if needsRefresh {
		if credential.RefreshToken == "" {
			return llms.OpenAICodexCredentials{}, errors.New("stored OpenAI OAuth token expired and has no refresh token; run `maestro login openai` again")
		}
		tokens, refreshErr := refreshOpenAIToken(ctx, credential.RefreshToken)
		if refreshErr != nil {
			return llms.OpenAICodexCredentials{}, fmt.Errorf("refresh OpenAI OAuth token: %w", refreshErr)
		}
		credential, err = credentialFromTokens(tokens, credential.RefreshToken, credential.AccountID)
		if err != nil {
			return llms.OpenAICodexCredentials{}, err
		}
		file.OpenAI = credential
		if err := writeCredentialsLocked(file); err != nil {
			return llms.OpenAICodexCredentials{}, err
		}
	}
	return llms.OpenAICodexCredentials{AccessToken: credential.AccessToken, AccountID: credential.AccountID}, nil
}

func LogoutOpenAI() error {
	credentialMu.Lock()
	defer credentialMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := acquireCredentialLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	file, err := loadCredentialsLocked()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if file.OpenAI == nil {
		return nil
	}
	file.OpenAI = nil
	return writeCredentialsLocked(file)
}

func credentialFromTokens(tokens *oauth.OpenAITokenResponse, previousRefresh, previousAccountID string) (*openAICredential, error) {
	if tokens == nil || strings.TrimSpace(tokens.AccessToken) == "" {
		return nil, errors.New("OpenAI OAuth response did not include an access token")
	}
	refresh := strings.TrimSpace(tokens.RefreshToken)
	if refresh == "" {
		refresh = previousRefresh
	}
	accountToken := strings.TrimSpace(tokens.IDToken)
	if accountToken == "" {
		accountToken = strings.TrimSpace(tokens.AccessToken)
	}
	accountID, err := llms.OpenAICodexAccountIDFromToken(accountToken)
	if err != nil && accountToken != tokens.AccessToken {
		accountID, err = llms.OpenAICodexAccountID(tokens.AccessToken)
	}
	if err != nil {
		accountID = strings.TrimSpace(previousAccountID)
		if accountID == "" {
			return nil, fmt.Errorf("extract OpenAI ChatGPT account id: %w", err)
		}
	}
	credential := &openAICredential{
		AccessToken:  strings.TrimSpace(tokens.AccessToken),
		RefreshToken: refresh,
		AccountID:    accountID,
	}
	if tokens.ExpiresIn > 0 {
		credential.ExpiresAt = time.Now().UTC().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	}
	return credential, nil
}

func saveOpenAILocked(credential *openAICredential) error {
	if credential == nil || credential.AccessToken == "" || credential.AccountID == "" {
		return errors.New("OpenAI OAuth credential requires access token and account id")
	}
	file, err := loadCredentialsLocked()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file.OpenAI = credential
	return writeCredentialsLocked(file)
}

func acquireCredentialLock(ctx context.Context) (func(), error) {
	path, err := CredentialPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create credential directory: %w", err)
	}
	lock := flock.New(path + ".lock")
	locked, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("acquire credential lock: %w", err)
	}
	if !locked {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("wait for credential lock: %w", ctxErr)
		}
		return nil, errors.New("credential lock was not acquired")
	}
	return func() { _ = lock.Unlock() }, nil
}

func loadCredentialsLocked() (credentialFile, error) {
	path, err := CredentialPath()
	if err != nil {
		return credentialFile{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return credentialFile{}, err
	}
	var file credentialFile
	if err := json.Unmarshal(data, &file); err != nil {
		return credentialFile{}, fmt.Errorf("decode credentials %s: %w", path, err)
	}
	if file.Version != 0 && file.Version != credentialVersion {
		return credentialFile{}, fmt.Errorf("unsupported credential version %d", file.Version)
	}
	file.Version = credentialVersion
	return file, nil
}

func writeCredentialsLocked(file credentialFile) error {
	path, err := CredentialPath()
	if err != nil {
		return err
	}
	file.Version = credentialVersion
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary credential file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return fmt.Errorf("protect temporary credential file: %w", err)
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync credentials: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close credentials: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace credentials: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("protect credentials: %w", err)
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open credential directory for sync: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil && runtime.GOOS != "windows" {
		return fmt.Errorf("sync credential directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close credential directory: %w", closeErr)
	}
	return nil
}

func openBrowser(url string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command, args = "open", []string{url}
	case "linux":
		command, args = "xdg-open", []string{url}
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return fmt.Errorf("unsupported browser platform %s", runtime.GOOS)
	}
	return exec.Command(command, args...).Start()
}
