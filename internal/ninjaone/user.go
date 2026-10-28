package ninjaone

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// User context: script runs and ticket writes need a token tied to a
// signed-in NinjaOne user, which only a Web app's authorization-code grant
// gives. The refresh token rotates on every use, so it lives in a file the
// server rewrites, and survives restarts.

// ErrNotAuthorized means user context is configured but nobody has signed in.
var ErrNotAuthorized = errors.New("no NinjaOne user has authorized this server yet: call ninjaone_status with action authorize and have the user open the link")

const reauthorize = "the stored user authorization was rejected (revoked, user disabled, or the Web app's secret changed): call ninjaone_status with action authorize to sign in again."

type user struct {
	clientID, clientSecret, file string

	mu     sync.Mutex
	token  string
	expiry time.Time
	failed bool // the last refresh was rejected

	// One sign-in at a time: a new AuthorizeURL replaces the pending one.
	pending *pending
	now     func() time.Time
}

type pending struct {
	state, redirect string
	expires         time.Time
}

// ErrBadState is a callback whose state is not the pending one: wrong,
// already used, replaced by a newer link, or expired.
var ErrBadState = errors.New("this sign-in link is stale or was already used: ask for a new one (ninjaone_status action authorize)")

// authorizeScope is what the user token may do; offline_access gets the refresh token.
const authorizeScope = "monitoring management control offline_access"

// EnableUserContext turns on DoUser with a Web app's client ID and secret,
// keeping the rotating refresh token in file.
func (c *Client) EnableUserContext(clientID, clientSecret, file string) {
	c.user = &user{clientID: clientID, clientSecret: clientSecret, file: file, now: time.Now}
}

// AuthorizeURL starts a sign-in: the consent URL, valid for 10 minutes,
// that sends the browser back to redirect with a single-use state.
func (c *Client) AuthorizeURL(redirect string) (string, error) {
	if c.user == nil {
		return "", errors.New("user context is not configured on this server")
	}
	u := c.user
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pending = &pending{state: rand.Text(), redirect: redirect, expires: u.now().Add(10 * time.Minute)}
	q := url.Values{
		"response_type": {"code"},
		"client_id":     {u.clientID},
		"redirect_uri":  {redirect},
		"state":         {u.pending.state},
		"scope":         {authorizeScope},
	}
	return c.url("/ws/oauth/authorize") + "?" + strings.ReplaceAll(q.Encode(), "+", "%20"), nil
}

// Callback finishes a sign-in: checks state, exchanges code, saves the token.
func (c *Client) Callback(ctx context.Context, state, code string) error {
	if c.user == nil {
		return errors.New("user context is not configured on this server")
	}
	u := c.user
	u.mu.Lock()
	defer u.mu.Unlock()
	p := u.pending
	if p == nil || subtle.ConstantTimeCompare([]byte(state), []byte(p.state)) != 1 {
		return ErrBadState
	}
	u.pending = nil // single use, whatever happens next
	if u.now().After(p.expires) {
		return ErrBadState
	}
	t, err := c.tokenRequest(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.redirect},
		"client_id":     {u.clientID},
		"client_secret": {u.clientSecret},
	})
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status/100 == 4 {
			ae.Hint = "NinjaOne rejected the sign-in: check the Web app's redirect URI matches the server's and that it allows the Authorization code grant, then ask for a new link."
		}
		return err
	}
	_, err = u.accept(t, "")
	return err
}

// DoUser is Do with the signed-in user's token instead of the app's.
func (c *Client) DoUser(ctx context.Context, method, path string, q url.Values, body any) (*Response, error) {
	if c.user == nil {
		return nil, errors.New("user context is not configured on this server")
	}
	return c.do(ctx, c.userBearer, method, path, q, body)
}

// userBearer is bearer for the user token: refresh_token grant, the rotated
// refresh token saved before the access token is used.
func (c *Client) userBearer(ctx context.Context, force bool) (string, error) {
	u := c.user
	u.mu.Lock()
	defer u.mu.Unlock()
	if !force && u.token != "" && time.Until(u.expiry) > time.Minute {
		return u.token, nil
	}
	old, err := u.load()
	if err != nil {
		return "", err
	}
	t, err := c.tokenRequest(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {old},
		"client_id":     {u.clientID},
		"client_secret": {u.clientSecret},
	})
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status/100 == 4 {
			u.failed, ae.Hint = true, reauthorize
		}
		return "", err
	}
	return u.accept(t, old)
}

// accept stores a token response, persisting a rotated refresh token first.
func (u *user) accept(t tokenResponse, old string) (string, error) {
	rt := cmp.Or(t.RefreshToken, old) // keep the old one if NinjaOne did not rotate
	if rt == "" {
		return "", errors.New("NinjaOne returned no refresh token: check the Web app allows the Refresh token grant")
	}
	if rt != old {
		if err := u.save(rt); err != nil {
			return "", err
		}
	}
	u.token, u.expiry, u.failed = t.AccessToken, t.Expiry, false
	return u.token, nil
}

type tokenFile struct {
	RefreshToken string `json:"refresh_token"`
}

func (u *user) load() (string, error) {
	b, err := os.ReadFile(u.file)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotAuthorized
	}
	if err != nil {
		return "", fmt.Errorf("reading user token: %w", err)
	}
	var f tokenFile
	if json.Unmarshal(b, &f) != nil || f.RefreshToken == "" {
		return "", ErrNotAuthorized
	}
	return f.RefreshToken, nil
}

// save replaces the token file atomically: losing a rotated refresh token
// would sign the user out for good.
func (u *user) save(rt string) error {
	b, _ := json.Marshal(tokenFile{RefreshToken: rt})
	tmp, err := os.CreateTemp(filepath.Dir(u.file), ".user-token-*") // created 0600
	if err != nil {
		return fmt.Errorf("saving user token: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(b)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), u.file)
	}
	if err != nil {
		return fmt.Errorf("saving user token: %w", err)
	}
	return nil
}

// UserState is what ninjaone_status reports about user context.
type UserState struct {
	Configured bool
	Authorized bool // a refresh token is stored and was not last rejected
}

func (c *Client) UserState() UserState {
	if c.user == nil {
		return UserState{}
	}
	u := c.user
	u.mu.Lock()
	defer u.mu.Unlock()
	_, err := u.load()
	return UserState{Configured: true, Authorized: err == nil && !u.failed}
}
