package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAPIBase      = "https://api.project-ebonhold.com"
	userAgent           = "EbonholdLauncher/1.0"
	clientID            = "EbonholdLauncher"
	origin              = "https://project-ebonhold.com"
	referer             = "https://project-ebonhold.com/download"
	maxAPIResponseBytes = 8 << 20
)

type HTTPAPI struct {
	baseURL string
	client  *http.Client
}

type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

func NewDefaultAPI() *HTTPAPI {
	return newHTTPAPI(defaultAPIBase, &http.Client{})
}

// NewHTTPAPI allows tests to use an HTTPS test server while production uses NewDefaultAPI.
func NewHTTPAPI(baseURL string, client *http.Client) (*HTTPAPI, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || u.Scheme != "https" {
		return nil, errors.New("API base URL must be an absolute HTTPS URL")
	}
	if client == nil {
		client = &http.Client{}
	}
	return newHTTPAPI(strings.TrimRight(baseURL, "/"), client), nil
}

func newHTTPAPI(baseURL string, client *http.Client) *HTTPAPI {
	copy := *client
	copy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return errors.New("refusing an insecure HTTP redirect")
		}
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		return nil
	}
	copy.Timeout = 0
	return &HTTPAPI{baseURL: strings.TrimRight(baseURL, "/"), client: &copy}
}

func (a *HTTPAPI) Login(ctx context.Context, username, password string) (string, error) {
	payload, err := json.Marshal(struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		RememberMe bool   `json:"rememberMe"`
	}{username, password, true})
	if err != nil {
		return "", err
	}
	var response struct {
		Success bool   `json:"success"`
		Token   string `json:"token"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := a.jsonRequest(ctx, http.MethodPost, "/api/auth/login", "", bytes.NewReader(payload), 60*time.Second, false, &response); err != nil {
		return "", err
	}
	if !response.Success {
		return "", errors.New(apiMessage(response.Error, response.Message, "authentication service rejected the login"))
	}
	if response.Token == "" || strings.ContainsAny(response.Token, " \t\r\n\x00") {
		return "", errors.New("login succeeded but no valid token was returned")
	}
	return response.Token, nil
}

func (a *HTTPAPI) Games(ctx context.Context, token string) (Manifest, error) {
	var response Manifest
	if err := a.jsonRequest(ctx, http.MethodGet, "/api/launcher/games", token, nil, 60*time.Second, true, &response); err != nil {
		return Manifest{}, err
	}
	if !response.Success || response.Data.Games == nil {
		return Manifest{}, errors.New("games manifest response is invalid")
	}
	return response, nil
}

func (a *HTTPAPI) Status(ctx context.Context, token string) (ServerStatus, error) {
	var response struct {
		Success bool         `json:"success"`
		Data    ServerStatus `json:"data"`
	}
	if err := a.jsonRequest(ctx, http.MethodGet, "/api/server/status", token, nil, 30*time.Second, false, &response); err != nil {
		return ServerStatus{}, err
	}
	if !response.Success {
		return ServerStatus{}, errors.New("server status response is invalid")
	}
	return response.Data, nil
}

func (a *HTTPAPI) Addons(ctx context.Context, token string) ([]Addon, error) {
	var response struct {
		Success bool    `json:"success"`
		Addons  []Addon `json:"addons"`
	}
	if err := a.jsonRequest(ctx, http.MethodGet, "/api/launcher/addons", token, nil, 60*time.Second, true, &response); err != nil {
		return nil, err
	}
	if !response.Success || response.Addons == nil {
		return nil, errors.New("addon catalog response is invalid")
	}
	return response.Addons, nil
}

func (a *HTTPAPI) DownloadURL(ctx context.Context, token, fileID string) (string, error) {
	values := url.Values{"file_ids": []string{fileID}}
	var response struct {
		URL   string `json:"url"`
		Files []struct {
			URL string `json:"url"`
		} `json:"files"`
	}
	if err := a.jsonRequest(ctx, http.MethodGet, "/api/launcher/download?"+values.Encode(), token, nil, 60*time.Second, true, &response); err != nil {
		return "", err
	}
	downloadURL := response.URL
	if len(response.Files) > 0 && response.Files[0].URL != "" {
		downloadURL = response.Files[0].URL
	}
	if err := requireHTTPS(downloadURL); err != nil {
		return "", errors.New("download URL response is invalid or not HTTPS")
	}
	return downloadURL, nil
}

func (a *HTTPAPI) AddonDownloads(ctx context.Context, token string, ids []int64) ([]AddonDownload, error) {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		if id < 1 {
			return nil, errors.New("addon IDs must be positive")
		}
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	values := url.Values{"addon_ids": []string{strings.Join(parts, ",")}}
	var response struct {
		Success bool            `json:"success"`
		Files   []AddonDownload `json:"files"`
	}
	if err := a.jsonRequest(ctx, http.MethodGet, "/api/launcher/addons/download?"+values.Encode(), token, nil, 60*time.Second, true, &response); err != nil {
		return nil, err
	}
	if !response.Success || response.Files == nil {
		return nil, errors.New("addon download response is invalid")
	}
	return response.Files, nil
}

func (a *HTTPAPI) Download(ctx context.Context, downloadURL string, dst io.Writer) error {
	if err := requireHTTPS(downloadURL); err != nil {
		return errors.New("refusing a non-HTTPS download URL")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return errors.New("invalid download request")
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := a.client.Do(req)
	if err != nil {
		if err := requestCtx.Err(); err != nil {
			return fmt.Errorf("download request: %w", err)
		}
		var network net.Error
		if errors.As(err, &network) && network.Timeout() {
			return errors.New("download request timed out")
		}
		var dns *net.DNSError
		if errors.As(err, &dns) {
			return errors.New("could not resolve the download server")
		}
		return errors.New("could not connect to the download server")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("download returned HTTP %d", resp.StatusCode)}
	}
	if _, err := io.Copy(dst, resp.Body); err != nil {
		if err := requestCtx.Err(); err != nil {
			return fmt.Errorf("download stream: %w", err)
		}
		var file *os.PathError
		if errors.As(err, &file) {
			return fmt.Errorf("write temporary file: %w", file.Err)
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return errors.New("download ended before all bytes arrived")
		}
		return errors.New("download stream failed")
	}
	return nil
}

func (a *HTTPAPI) jsonRequest(ctx context.Context, method, path, token string, body io.Reader, timeout time.Duration, webHeaders bool, dst any) error {
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, method, a.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Client-Id", clientID)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if webHeaders {
		req.Header.Set("Origin", origin)
		req.Header.Set("Referer", referer)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return errors.New("API request timed out")
		}
		var netErr net.Error
		if errors.As(err, &netErr) {
			return errors.New("API request failed")
		}
		return errors.New("API request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseBytes+1))
	if err != nil {
		return errors.New("could not read API response")
	}
	if int64(len(data)) > maxAPIResponseBytes {
		return errors.New("API response exceeded the size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{StatusCode: resp.StatusCode, Message: responseError(data, resp.StatusCode, token)}
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("API returned invalid JSON (HTTP %d, Content-Type %q): %w", resp.StatusCode, resp.Header.Get("Content-Type"), err)
	}
	return nil
}

func responseError(data []byte, status int, token string) string {
	var response struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(data, &response)
	message := apiMessage(response.Error, response.Message, fmt.Sprintf("API returned HTTP %d", status))
	if token != "" {
		message = strings.ReplaceAll(message, token, "[redacted]")
	}
	return message
}

func apiMessage(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "API request failed"
}

func requireHTTPS(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || strings.ContainsAny(raw, "\r\n\x00") {
		return errors.New("URL must be absolute HTTPS without userinfo")
	}
	return nil
}
