package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lcleveland/ninjaone-mcp/internal/config"
	"github.com/lcleveland/ninjaone-mcp/internal/ninjaone"
	"github.com/lcleveland/ninjaone-mcp/internal/version"
)

// Handler serves the MCP endpoint, behind a bearer gate when a token is
// configured, plus an unauthenticated /healthz and, with user context, the
// sign-in callback. The SDK's DNS-rebinding and cross-origin protection stays on.
func Handler(cfg *config.Config, s *mcp.Server, c *ninjaone.Client, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	mux := http.NewServeMux()
	if c.UserState().Configured {
		if u, err := url.Parse(cfg.OAuthRedirectURL); err == nil {
			mux.Handle("GET "+u.Path, callback(c, log))
		}
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"ok","version":"`+version.Version+`"}`)
	})

	var h http.Handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{Logger: log, SessionTimeout: 10 * time.Minute})
	if cfg.HTTPAuthToken != "" {
		want := []byte(cfg.HTTPAuthToken)
		verify := func(_ context.Context, tok string, _ *http.Request) (*auth.TokenInfo, error) {
			if subtle.ConstantTimeCompare([]byte(tok), want) != 1 {
				return nil, auth.ErrInvalidToken
			}
			return &auth.TokenInfo{Expiration: time.Now().Add(time.Hour)}, nil
		}
		h = auth.RequireBearerToken(verify, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(h)
	}
	mux.Handle(cfg.Path, h)
	return mux
}

// callback is where NinjaOne sends the browser after sign-in. It sits outside
// the bearer gate (a browser cannot send one); the single-use state is what
// proves the request answers our own authorize link. Nothing from the query
// is logged or echoed: the code is a credential.
func callback(c *ninjaone.Client, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		q := r.URL.Query()
		err := c.Callback(r.Context(), q.Get("state"), q.Get("code"), q.Get("error"))
		switch {
		case errors.Is(err, ninjaone.ErrBadState):
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, "This sign-in link is stale or was already used. Ask for a new one.\n")
		case errors.Is(err, ninjaone.ErrDenied):
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, err.Error()+". Ask for a new link to try again.\n")
		case err != nil:
			log.Warn("user sign-in failed", "error", err.Error())
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, "NinjaOne refused the sign-in: "+err.Error()+"\n")
		default:
			log.Info("user signed in: script runs and ticket writes now use their token")
			io.WriteString(w, "Authorized. You can close this tab.\n")
		}
	})
}

// ServeHTTP listens until ctx is cancelled, then drains for up to 5s.
func ServeHTTP(ctx context.Context, cfg *config.Config, s *mcp.Server, c *ninjaone.Client, log *slog.Logger) error {
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           Handler(cfg, s, c, log),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "path", cfg.Path, "authenticated", cfg.HTTPAuthToken != "")
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errc <- err
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
		return <-errc
	}
}
