// Package ninjaone is a small REST client for the NinjaOne public API v2:
// OAuth2 client credentials, GET-only retry with backoff, and errors that tell
// the model what to do next.
package ninjaone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	base         *url.URL
	clientID     string
	clientSecret string
	scopes       string
	hc           *http.Client
	log          *slog.Logger

	mu     sync.Mutex
	token  string
	expiry time.Time
	scope  string // as granted by the last token response

	// Retry tuning; tests shrink these.
	Attempts  int
	BaseDelay time.Duration
	MaxDelay  time.Duration
}

// New builds a client. hc may be nil; its Timeout is the per-request timeout.
func New(base *url.URL, clientID, clientSecret, scopes string, hc *http.Client, log *slog.Logger) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Client{base: base, clientID: clientID, clientSecret: clientSecret, scopes: scopes, hc: hc, log: log,
		Attempts: 4, BaseDelay: 500 * time.Millisecond, MaxDelay: 8 * time.Second}
}

func (c *Client) BaseURL() string { return c.base.String() }

// Response is a successful (2xx) reply.
type Response struct {
	Status    int
	Header    http.Header
	Body      []byte
	RequestID string // x-nj-request-id
}

// Decode unmarshals the body; an empty body (204) decodes to nil.
func (r *Response) Decode() (any, error) {
	if len(bytes.TrimSpace(r.Body)) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(r.Body, &v); err != nil {
		return nil, fmt.Errorf("decoding NinjaOne response: %w", err)
	}
	return v, nil
}

// Token is the outcome of a token request, for ninjaone_status.
type Token struct {
	Scope   string
	Expires time.Time
}

// Authenticate returns a valid token, fetching one if needed.
func (c *Client) Authenticate(ctx context.Context) (Token, error) {
	if _, err := c.bearer(ctx, false); err != nil {
		return Token{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return Token{Scope: c.scope, Expires: c.expiry}, nil
}

// bearer returns a cached access token, refreshing a minute before expiry or
// when force is set (after a 401). Client credentials has no refresh token,
// so refreshing is simply asking again.
func (c *Client) bearer(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force && c.token != "" && time.Until(c.expiry) > time.Minute {
		return c.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}
	if c.scopes != "" {
		form.Set("scope", c.scopes)
	}
	u := c.url("/ws/oauth/token")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %s", c.scrub(err.Error()))
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", newAPIError(http.MethodPost, "/ws/oauth/token", resp, b, func(s string) string { return c.scrub(s) })
	}
	var t struct {
		AccessToken string  `json:"access_token"`
		ExpiresIn   float64 `json:"expires_in"`
		Scope       string  `json:"scope"`
	}
	if err := json.Unmarshal(b, &t); err != nil || t.AccessToken == "" {
		return "", fmt.Errorf("token request: unexpected response from %s (is the base URL a NinjaOne instance?)", c.base.Host)
	}
	if t.ExpiresIn <= 0 {
		t.ExpiresIn = 3600
	}
	c.token, c.scope = t.AccessToken, t.Scope
	c.expiry = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	return c.token, nil
}

// Do sends one API request. path starts with /v2/. body is JSON-encoded when
// non-nil. Non-2xx replies return *APIError.
//
// Only GETs are retried: NinjaOne actions take no idempotency key, so a
// retried POST can reboot a machine or run a script twice.
func (c *Client) Do(ctx context.Context, method, path string, q url.Values, body any) (*Response, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, fmt.Errorf("encoding request body: %w", err)
		}
	}
	u := c.url(path)
	if len(q) > 0 {
		// NinjaOne's docs encode spaces in df as %20; whether it accepts + is
		// unverified, so never send +. A literal + is already %2B.
		u += "?" + strings.ReplaceAll(q.Encode(), "+", "%20")
	}
	retryable := method == http.MethodGet
	reauthed := false

	for attempt := 1; ; attempt++ {
		tok, err := c.bearer(ctx, false)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.hc.Do(req)
		if err != nil {
			if ctx.Err() == nil && retryable && attempt < c.Attempts {
				if werr := c.wait(ctx, attempt, ""); werr != nil {
					return nil, werr
				}
				continue
			}
			return nil, fmt.Errorf("%s %s: %s", method, path, c.scrub(err.Error(), tok))
		}
		b, rerr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if rerr != nil {
			return nil, fmt.Errorf("%s %s: reading response: %w", method, path, rerr)
		}
		reqID := resp.Header.Get("x-nj-request-id")
		c.log.Debug("ninjaone request", "method", method, "path", path, "status", resp.StatusCode, "request_id", reqID)

		// NinjaOne signals throttling with an HTML page, not always a 429.
		throttled := resp.StatusCode == http.StatusTooManyRequests || (len(b) > 0 && html(resp.Header, b))
		if resp.StatusCode/100 == 2 && !throttled {
			return &Response{Status: resp.StatusCode, Header: resp.Header, Body: b, RequestID: reqID}, nil
		}
		if resp.StatusCode == http.StatusUnauthorized && !reauthed {
			// The token was revoked or expired early; one fresh token, then give up.
			reauthed = true
			if _, err := c.bearer(ctx, true); err != nil {
				return nil, err
			}
			attempt--
			continue
		}
		retry := throttled || (resp.StatusCode >= 500 && resp.StatusCode != 501)
		if retry && retryable && attempt < c.Attempts {
			if werr := c.wait(ctx, attempt, resp.Header.Get("Retry-After")); werr != nil {
				return nil, werr
			}
			continue
		}
		e := newAPIError(method, path, resp, b, func(s string) string { return c.scrub(s, tok) })
		if throttled {
			e.Throttled = true
			e.Hint = hintThrottle(retryable)
		}
		return nil, e
	}
}

func (c *Client) url(path string) string {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	return u.String()
}

// html reports a non-JSON (HTML) body, which on an API path means a throttle
// or gateway page rather than data.
func html(h http.Header, b []byte) bool {
	if mt, _, err := mime.ParseMediaType(h.Get("Content-Type")); err == nil {
		if mt == "text/html" {
			return true
		}
		if mt == "application/json" || strings.HasSuffix(mt, "+json") {
			return false
		}
	}
	return bytes.HasPrefix(bytes.TrimSpace(b), []byte("<"))
}

// wait sleeps with full-jitter exponential backoff, or Retry-After if given.
func (c *Client) wait(ctx context.Context, attempt int, retryAfter string) error {
	d := c.BaseDelay << (attempt - 1)
	if d > c.MaxDelay || d <= 0 {
		d = c.MaxDelay
	}
	d = time.Duration(rand.Int64N(int64(d) + 1))
	if s, err := strconv.Atoi(retryAfter); err == nil && s >= 0 {
		d = min(time.Duration(s)*time.Second, 60*time.Second)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// scrub hides the client secret and any access tokens passed in.
func (c *Client) scrub(s string, tokens ...string) string {
	for _, secret := range append(tokens, c.clientSecret) {
		if len(secret) >= 4 {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	return s
}
