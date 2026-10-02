// Package config parses flags and environment into a Config.
//
// Secrets (the OAuth client secret and the HTTP bearer token) are only ever
// read from files, a systemd credential, or, with a warning, the environment.
// There is deliberately no flag that takes a secret on argv, where any local
// user can read it from /proc/<pid>/cmdline.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Regions maps each --region key to its NinjaOne host.
var Regions = map[string]string{
	"app": "https://app.ninjarmm.com",
	"us":  "https://app.ninjarmm.com",
	"us2": "https://us2.ninjarmm.com",
	"eu":  "https://eu.ninjarmm.com",
	"ca":  "https://ca.ninjarmm.com",
	"oc":  "https://oc.ninjarmm.com",
	"fed": "https://fed.ninjarmm.com",
}

// Groups are the tool groups --tool-groups may name. core is always on.
var Groups = []string{"core", "inventory", "alerts", "patching", "docs", "ticketing", "backup"}

// Capabilities are the opt-in write classes, each enabled by --allow-<name>.
// See docs/adr/0001-capability-flags-not-verb-flags.md.
var Capabilities = []string{
	"tickets", "documentation", "custom-fields",
	"device-maintenance", "device-actions", "scripts", "device-admin",
}

type Config struct {
	BaseURL        *url.URL
	Region         string // empty when --base-url was used
	ClientID       string
	ClientSecret   string
	Scopes         string // space-separated; empty asks for every scope on the app
	RequestTimeout time.Duration
	LogLevel       slog.Level

	Allow   map[string]bool // capability -> enabled
	MaxBulk int
	Groups  []string // empty means all

	HTTP          bool
	Addr          string
	Path          string
	HTTPAuthToken string

	ShowVersion bool
}

// LogValue keeps secrets out of logs however the config is printed.
func (c *Config) LogValue() slog.Value {
	u := ""
	if c.BaseURL != nil {
		u = c.BaseURL.String()
	}
	return slog.GroupValue(
		slog.String("base_url", u),
		slog.String("client_id", c.ClientID),
		slog.Bool("client_secret_set", c.ClientSecret != ""),
		slog.String("scopes", c.Scopes),
		slog.Any("capabilities", c.Enabled()),
		slog.Int("max_bulk", c.MaxBulk),
		slog.Any("groups", c.Groups),
		slog.Bool("http", c.HTTP),
		slog.String("addr", c.Addr),
		slog.Bool("http_auth_set", c.HTTPAuthToken != ""),
	)
}

// Parse reads args and the environment. getenv is injected for tests.
// Warnings (non-fatal) are returned for the caller to log once a logger exists.
func Parse(args []string, getenv func(string) string) (*Config, []string, error) {
	var (
		c                                 Config
		region, rawURL, secretFile, hauth string
		idFile                            string
		groups, logLevel                  string
		stdio                             bool
		warnings                          []string
	)
	c.Allow = map[string]bool{}
	allow := map[string]*bool{}

	fs := flag.NewFlagSet("ninjaone-mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&region, "region", getenv("NINJAONE_REGION"), "NinjaOne region: app|us|us2|eu|ca|oc|fed (env NINJAONE_REGION)")
	fs.StringVar(&rawURL, "base-url", getenv("NINJAONE_BASE_URL"), "NinjaOne base URL, instead of --region (env NINJAONE_BASE_URL)")
	fs.StringVar(&c.ClientID, "client-id", getenv("NINJAONE_CLIENT_ID"), "API Services client ID (env NINJAONE_CLIENT_ID)")
	fs.StringVar(&idFile, "client-id-file", getenv("NINJAONE_CLIENT_ID_FILE"), "file holding the client ID, instead of --client-id (env NINJAONE_CLIENT_ID_FILE)")
	fs.StringVar(&secretFile, "client-secret-file", getenv("NINJAONE_CLIENT_SECRET_FILE"), "file holding the client secret (env NINJAONE_CLIENT_SECRET_FILE)")
	fs.StringVar(&c.Scopes, "scopes", "", "space-separated OAuth scopes to request (default: all scopes on the app)")
	fs.DurationVar(&c.RequestTimeout, "request-timeout", 30*time.Second, "per-request timeout to NinjaOne")
	fs.StringVar(&logLevel, "log-level", or(getenv("NINJAONE_MCP_LOG_LEVEL"), "info"), "debug|info|warn|error (env NINJAONE_MCP_LOG_LEVEL)")
	for _, name := range Capabilities {
		allow[name] = fs.Bool("allow-"+name, false, "enable the "+name+" write capability")
	}
	fs.IntVar(&c.MaxBulk, "max-bulk", 50, "maximum records in one batched write")
	fs.StringVar(&groups, "tool-groups", "", "comma-separated tool groups to enable (default all): "+strings.Join(Groups, ","))
	fs.BoolVar(&stdio, "stdio", false, "serve over stdio (default)")
	fs.BoolVar(&c.HTTP, "http", false, "serve over streamable HTTP")
	fs.StringVar(&c.Addr, "addr", "127.0.0.1:8233", "HTTP listen address")
	fs.StringVar(&c.Path, "path", "/mcp", "HTTP MCP endpoint path")
	fs.StringVar(&hauth, "http-auth-token-file", getenv("NINJAONE_MCP_HTTP_AUTH_TOKEN_FILE"), "file holding the bearer token HTTP clients must send (env NINJAONE_MCP_HTTP_AUTH_TOKEN_FILE)")
	fs.BoolVar(&c.ShowVersion, "version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(os.Stderr)
			fmt.Fprintln(os.Stderr, "Usage: ninjaone-mcp --region <region> --client-id <id> --client-secret-file <path> [flags]")
			fs.PrintDefaults()
		}
		return nil, nil, err
	}
	if c.ShowVersion {
		return &c, nil, nil
	}
	if fs.NArg() > 0 {
		return nil, nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if stdio && c.HTTP {
		return nil, nil, errors.New("--stdio and --http are mutually exclusive")
	}
	for name, on := range allow {
		c.Allow[name] = *on
	}

	if err := c.LogLevel.UnmarshalText([]byte(logLevel)); err != nil {
		return nil, nil, fmt.Errorf("--log-level: %w", err)
	}

	u, err := baseURL(region, rawURL)
	if err != nil {
		return nil, nil, err
	}
	c.BaseURL, c.Region = u, region

	credDir := getenv("CREDENTIALS_DIRECTORY")
	switch {
	case idFile != "":
		c.ClientID, err = readSecret(idFile)
	case c.ClientID != "":
	case credDir != "":
		c.ClientID, _ = readSecret(filepath.Join(credDir, "client-id"))
	}
	if err != nil {
		return nil, nil, err
	}
	if c.ClientID == "" {
		return nil, nil, errors.New("no client ID: set --client-id, --client-id-file, NINJAONE_CLIENT_ID, NINJAONE_CLIENT_ID_FILE or the systemd credential client-id")
	}
	switch {
	case secretFile != "":
		c.ClientSecret, err = readSecret(secretFile)
	case getenv("NINJAONE_CLIENT_SECRET") != "":
		c.ClientSecret = strings.TrimSpace(getenv("NINJAONE_CLIENT_SECRET"))
		warnings = append(warnings, "client secret read from NINJAONE_CLIENT_SECRET; prefer --client-secret-file, the environment is readable via /proc")
	case credDir != "":
		c.ClientSecret, err = readSecret(filepath.Join(credDir, "client-secret"))
	default:
		err = errors.New("no client secret: set --client-secret-file, NINJAONE_CLIENT_SECRET_FILE, NINJAONE_CLIENT_SECRET or the systemd credential client-secret")
	}
	if err != nil {
		return nil, nil, err
	}

	if groups != "" {
		for g := range strings.SplitSeq(groups, ",") {
			g = strings.TrimSpace(g)
			if !slices.Contains(Groups, g) {
				return nil, nil, fmt.Errorf("--tool-groups: unknown group %q (want %s)", g, strings.Join(Groups, ","))
			}
			c.Groups = append(c.Groups, g)
		}
	}
	if c.MaxBulk < 1 {
		return nil, nil, errors.New("--max-bulk must be at least 1")
	}

	if c.HTTP {
		if !strings.HasPrefix(c.Path, "/") {
			return nil, nil, errors.New("--path must start with /")
		}
		switch {
		case hauth != "":
			c.HTTPAuthToken, err = readSecret(hauth)
		case credDir != "":
			if s, e := readSecret(filepath.Join(credDir, "http-auth-token")); e == nil {
				c.HTTPAuthToken = s
			}
		}
		if err != nil {
			return nil, nil, err
		}
		if c.HTTPAuthToken == "" && !loopback(c.Addr) {
			return nil, nil, fmt.Errorf("refusing to listen on non-loopback %s without --http-auth-token-file", c.Addr)
		}
	}
	return &c, warnings, nil
}

// baseURL resolves exactly one of --region and --base-url. Plain http is only
// allowed to a loopback host (the VM test's stub tenant).
func baseURL(region, raw string) (*url.URL, error) {
	switch {
	case region != "" && raw != "":
		return nil, errors.New("--region and --base-url are mutually exclusive")
	case region != "":
		host, ok := Regions[region]
		if !ok {
			return nil, fmt.Errorf("--region: unknown region %q (want app, us, us2, eu, ca, oc or fed; or use --base-url)", region)
		}
		return url.Parse(host)
	case raw == "":
		return nil, errors.New("one of --region or --base-url is required")
	}
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("--base-url must be an https URL, got %q", raw)
	}
	if u.Scheme == "http" && !loopback(u.Host) {
		return nil, fmt.Errorf("--base-url must use https unless the host is loopback, got %q", raw)
	}
	return u, nil
}

// Enabled lists the enabled capabilities, in declaration order.
func (c *Config) Enabled() []string {
	var on []string
	for _, n := range Capabilities {
		if c.Allow[n] {
			on = append(on, n)
		}
	}
	return on
}

// GroupOn reports whether a tool group is on.
func (c *Config) GroupOn(group string) bool {
	return group == "core" || len(c.Groups) == 0 || slices.Contains(c.Groups, group)
}

func readSecret(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading secret: %w", err)
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", fmt.Errorf("secret file %s is empty", path)
	}
	return s, nil
}

// loopback reports whether addr ("host:port" or a bare host) is loopback.
func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = strings.Trim(addr, "[]")
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
