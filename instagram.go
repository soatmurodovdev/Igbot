package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// APIError Instagram API qaytargan xato.
type APIError struct {
	HTTPStatus int
	Code       int
	Subcode    int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("instagram api: http=%d code=%d sub=%d: %s", e.HTTPStatus, e.Code, e.Subcode, e.Message)
}

// IsAuth token yaroqsiz yoki muddati tugagan.
func (e *APIError) IsAuth() bool {
	return e.Code == 190 || e.HTTPStatus == 401
}

// IsRateLimit so'rovlar limiti.
func (e *APIError) IsRateLimit() bool {
	switch e.Code {
	case 4, 17, 32, 613:
		return true
	}
	return e.HTTPStatus == 429
}

// IsTransient vaqtinchalik server xatosi (qayta urinsa bo'ladi).
func (e *APIError) IsTransient() bool {
	return e.HTTPStatus >= 500 || e.Code == 1 || e.Code == 2
}

type IGClient struct {
	cfg   *Config
	db    *DB
	http  *http.Client
	mu    sync.RWMutex
	token string
}

func newIGClient(cfg *Config, db *DB) *IGClient {
	return &IGClient{
		cfg:   cfg,
		db:    db,
		http:  &http.Client{Timeout: 20 * time.Second},
		token: cfg.IGAccessToken,
	}
}

func (c *IGClient) getToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

func (c *IGClient) setToken(t string) {
	c.mu.Lock()
	c.token = t
	c.mu.Unlock()
}

func tokenFingerprint(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:8])
}

func (c *IGClient) base() string {
	return "https://" + c.cfg.GraphHost + "/" + c.cfg.GraphVersion
}

// loadToken: agar token yangilangan bo'lsa, bazadagi yangi tokenni ishlatadi.
// Siz IG_ACCESS_TOKEN'ni o'zgartirsangiz, yangi qiymat ustun turadi.
func (c *IGClient) loadToken(ctx context.Context) {
	if c.cfg.IGAccessToken == "" {
		return
	}
	fp := tokenFingerprint(c.cfg.IGAccessToken)
	storedFP, ok, err := c.db.getSetting(ctx, "ig_token_env_fp")
	if err != nil {
		log.Printf("token sozlamasini o'qishda xato: %v", err)
		return
	}
	if ok && storedFP == fp {
		if t, found, err := c.db.getSetting(ctx, "ig_token"); err == nil && found && t != "" {
			c.setToken(t)
		}
		return
	}
	_ = c.db.setSetting(ctx, "ig_token_env_fp", fp)
	_ = c.db.setSetting(ctx, "ig_token", "")
	_ = c.db.setSetting(ctx, "ig_token_refreshed_at", time.Now().UTC().Format(time.RFC3339))
}

func (c *IGClient) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return cleanErr(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIError(resp.StatusCode, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("javob JSON'i noto'g'ri: %w", err)
		}
	}
	return nil
}

func parseAPIError(status int, data []byte) error {
	var e struct {
		Error struct {
			Message      string `json:"message"`
			Code         int    `json:"code"`
			ErrorSubcode int    `json:"error_subcode"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &e)
	msg := e.Error.Message
	if msg == "" {
		msg = truncate(string(data), 200)
	}
	return &APIError{HTTPStatus: status, Code: e.Error.Code, Subcode: e.Error.ErrorSubcode, Message: msg}
}

// Reply kommentga ochiq javob yozadi: POST /{comment-id}/replies
func (c *IGClient) Reply(ctx context.Context, commentID, message string) (string, error) {
	body, err := json.Marshal(map[string]string{"message": message})
	if err != nil {
		return "", err
	}
	u := fmt.Sprintf("%s/%s/replies", c.base(), url.PathEscape(commentID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.getToken())
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// ReplyExists: shu kommentda aynan shu matnli javob allaqachon bormi (takroriy javobdan saqlanish).
func (c *IGClient) ReplyExists(ctx context.Context, commentID, text string) (bool, error) {
	u := fmt.Sprintf("%s/%s/replies?fields=id,text", c.base(), url.PathEscape(commentID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.getToken())
	var out struct {
		Data []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := c.do(req, &out); err != nil {
		return false, err
	}
	want := strings.TrimSpace(text)
	for _, d := range out.Data {
		if strings.TrimSpace(d.Text) == want {
			return true, nil
		}
	}
	return false, nil
}

// RefreshIfNeeded uzoq muddatli tokenni 30 kundan keyin yangilaydi.
func (c *IGClient) RefreshIfNeeded(ctx context.Context) error {
	if c.getToken() == "" {
		return nil
	}
	ts, ok, err := c.db.getSetting(ctx, "ig_token_refreshed_at")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if !ok {
		return c.db.setSetting(ctx, "ig_token_refreshed_at", now.Format(time.RFC3339))
	}
	t, perr := time.Parse(time.RFC3339, ts)
	if perr != nil {
		return c.db.setSetting(ctx, "ig_token_refreshed_at", now.Format(time.RFC3339))
	}
	if now.Sub(t) < 30*24*time.Hour {
		return nil
	}
	u := fmt.Sprintf("https://%s/refresh_access_token?grant_type=ig_refresh_token&access_token=%s",
		c.cfg.GraphHost, url.QueryEscape(c.getToken()))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := c.do(req, &out); err != nil {
		return err
	}
	if out.AccessToken == "" {
		return fmt.Errorf("token yangilash javobida token yo'q")
	}
	c.setToken(out.AccessToken)
	if err := c.db.setSetting(ctx, "ig_token", out.AccessToken); err != nil {
		return err
	}
	log.Printf("Instagram tokeni yangilandi")
	return c.db.setSetting(ctx, "ig_token_refreshed_at", now.Format(time.RFC3339))
}
