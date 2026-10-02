package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func secretFile(t *testing.T, name, v string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(v+"\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSecretLookupOrder(t *testing.T) {
	flagFile := secretFile(t, "flag", "from-flag")
	envFile := secretFile(t, "env", "from-env-file")
	credDir := filepath.Dir(secretFile(t, "client-secret", "from-cred"))
	base := []string{"--region", "us2", "--client-id", "id"}

	cases := []struct {
		name string
		args []string
		env  map[string]string
		want string
		warn bool
	}{
		{"flag wins", append(base, "--client-secret-file", flagFile), map[string]string{"NINJAONE_CLIENT_SECRET_FILE": envFile, "NINJAONE_CLIENT_SECRET": "x", "CREDENTIALS_DIRECTORY": credDir}, "from-flag", false},
		{"env file", base, map[string]string{"NINJAONE_CLIENT_SECRET_FILE": envFile, "NINJAONE_CLIENT_SECRET": "x", "CREDENTIALS_DIRECTORY": credDir}, "from-env-file", false},
		{"env value warns", base, map[string]string{"NINJAONE_CLIENT_SECRET": " from-env ", "CREDENTIALS_DIRECTORY": credDir}, "from-env", true},
		{"systemd credential", base, map[string]string{"CREDENTIALS_DIRECTORY": credDir}, "from-cred", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, warns, err := Parse(tc.args, env(tc.env))
			if err != nil {
				t.Fatal(err)
			}
			if c.ClientSecret != tc.want {
				t.Errorf("secret = %q, want %q", c.ClientSecret, tc.want)
			}
			if (len(warns) > 0) != tc.warn {
				t.Errorf("warnings = %v", warns)
			}
		})
	}
	if _, _, err := Parse(base, env(nil)); err == nil || !strings.Contains(err.Error(), "no client secret") {
		t.Errorf("missing secret: %v", err)
	}
}

func TestRegionAndBaseURL(t *testing.T) {
	sec := secretFile(t, "s", "secret")
	parse := func(extra ...string) (*Config, error) {
		c, _, err := Parse(append([]string{"--client-id", "id", "--client-secret-file", sec}, extra...), env(nil))
		return c, err
	}
	for region, want := range map[string]string{"us2": "https://us2.ninjarmm.com", "app": "https://app.ninjarmm.com", "eu": "https://eu.ninjarmm.com", "fed": "https://fed.ninjarmm.com"} {
		c, err := parse("--region", region)
		if err != nil || c.BaseURL.String() != want {
			t.Errorf("region %s: %v %v", region, c, err)
		}
	}
	c, err := parse("--base-url", "https://example.ninjarmm.com/")
	if err != nil || c.BaseURL.String() != "https://example.ninjarmm.com" || c.Region != "" {
		t.Errorf("base-url: %v %v", c, err)
	}
	if _, err := parse("--base-url", "http://127.0.0.1:9999"); err != nil {
		t.Errorf("loopback http should be allowed: %v", err)
	}
	for _, bad := range [][]string{
		{},
		{"--region", "us2", "--base-url", "https://x"},
		{"--region", "mars"},
		{"--base-url", "http://ninja.example.com"},
		{"--base-url", "ftp://x"},
	} {
		if _, err := parse(bad...); err == nil {
			t.Errorf("%v: want error", bad)
		}
	}
	if _, _, err := Parse([]string{"--region", "us2", "--client-secret-file", sec}, env(nil)); err == nil {
		t.Error("missing client id: want error")
	}
	idDir := filepath.Dir(secretFile(t, "client-id", "from-cred"))
	for _, tc := range []struct {
		args []string
		env  map[string]string
		want string
	}{
		{[]string{"--client-id-file", secretFile(t, "id", "from-file")}, map[string]string{"NINJAONE_CLIENT_ID": "x"}, "from-file"},
		{nil, map[string]string{"NINJAONE_CLIENT_ID_FILE": secretFile(t, "id2", "from-env-file"), "CREDENTIALS_DIRECTORY": idDir}, "from-env-file"},
		{[]string{"--client-id", "flag"}, map[string]string{"CREDENTIALS_DIRECTORY": idDir}, "flag"},
		{nil, map[string]string{"CREDENTIALS_DIRECTORY": idDir}, "from-cred"},
	} {
		c, _, err := Parse(append([]string{"--region", "us2", "--client-secret-file", sec}, tc.args...), env(tc.env))
		if err != nil || c.ClientID != tc.want {
			t.Errorf("%v %v: id = %v, %v; want %q", tc.args, tc.env, c, err, tc.want)
		}
	}
}

func TestCapabilitiesAndGroups(t *testing.T) {
	sec := secretFile(t, "s", "secret")
	c, _, err := Parse([]string{"--region", "us2", "--client-id", "id", "--client-secret-file", sec,
		"--allow-scripts", "--allow-tickets", "--tool-groups", "inventory,alerts"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(c.Enabled()); got != "[tickets scripts]" {
		t.Errorf("enabled = %s", got)
	}
	if !c.GroupOn("core") || !c.GroupOn("alerts") || c.GroupOn("backup") {
		t.Errorf("groups = %v", c.Groups)
	}
	if _, _, err := Parse([]string{"--region", "us2", "--client-id", "id", "--client-secret-file", sec, "--tool-groups", "nope"}, env(nil)); err == nil {
		t.Error("unknown group: want error")
	}
}

func TestHTTPListenerNeedsAuthOffLoopback(t *testing.T) {
	sec := secretFile(t, "s", "secret")
	base := []string{"--region", "us2", "--client-id", "id", "--client-secret-file", sec, "--http"}
	if _, _, err := Parse(append(base, "--addr", "0.0.0.0:8233"), env(nil)); err == nil {
		t.Error("non-loopback without auth: want error")
	}
	tok := secretFile(t, "tok", "bearer")
	c, _, err := Parse(append(base, "--addr", "0.0.0.0:8233", "--http-auth-token-file", tok), env(nil))
	if err != nil || c.HTTPAuthToken != "bearer" {
		t.Errorf("with auth: %v %v", c, err)
	}
	if _, _, err := Parse(append(base, "--stdio"), env(nil)); err == nil {
		t.Error("--stdio with --http: want error")
	}
}

func TestLogValueRedacts(t *testing.T) {
	c := &Config{ClientID: "id", ClientSecret: "super-secret-value", HTTPAuthToken: "bearer-secret-value"}
	var b strings.Builder
	slog.New(slog.NewTextHandler(&b, nil)).Info("cfg", "config", c)
	if s := b.String(); strings.Contains(s, "super-secret-value") || strings.Contains(s, "bearer-secret-value") {
		t.Errorf("secret logged: %s", s)
	}
}
