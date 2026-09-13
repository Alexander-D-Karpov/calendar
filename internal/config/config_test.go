package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testKey(b byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32))
}

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://calendar:hunter2@localhost:5432/calendar?sslmode=disable",
		"SECRET_KEYS":  "1:" + testKey(1),
	}
}

func smtpEnv() map[string]string {
	env := validEnv()
	env["SMTP_HOST"] = "mail.example.com"
	env["SMTP_FROM"] = "calendar@example.com"
	env["SMTP_USER"] = "calendar"
	env["SMTP_PASSWORD"] = "pw"
	return env
}

func mustParse(t *testing.T, env map[string]string) *Config {
	t.Helper()
	c, err := Parse(env)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return c
}

func problemsOf(t *testing.T, err error) []string {
	t.Helper()
	var cfgErr *Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *Error, got %v", err)
	}
	return cfgErr.Problems
}

func hasProblem(problems []string, key string) bool {
	for _, p := range problems {
		if strings.HasPrefix(p, key+":") {
			return true
		}
	}
	return false
}

func hasWarning(c *Config, substr string) bool {
	for _, w := range c.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func eq[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestParseDefaults(t *testing.T) {
	c := mustParse(t, validEnv())

	eq(t, "App.Env", c.App.Env, EnvProduction)
	eq(t, "App.BaseURL", c.App.BaseURL.String(), "https://calendar.akarpov.ru")
	eq(t, "App.Name", c.App.Name, "Calendar")
	eq(t, "App.DefaultTimezone", c.App.DefaultTimezone.String(), "UTC")
	eq(t, "HTTP.Addr", c.HTTP.Addr, ":8080")
	eq(t, "len(HTTP.TrustedProxies)", len(c.HTTP.TrustedProxies), 2)
	eq(t, "HTTP.ReadTimeout", c.HTTP.ReadTimeout, 15*time.Second)
	eq(t, "HTTP.WriteTimeout", c.HTTP.WriteTimeout, 30*time.Second)
	eq(t, "HTTP.IdleTimeout", c.HTTP.IdleTimeout, 120*time.Second)
	eq(t, "HTTP.MaxBody", c.HTTP.MaxBody, MB)
	eq(t, "Database.MaxConns", c.Database.MaxConns, int32(20))
	eq(t, "Database.AutoMigrate", c.Database.AutoMigrate, true)
	eq(t, "Security.SecretKeyActive", c.Security.SecretKeyActive, uint32(1))
	eq(t, "Security.SessionTTL", c.Security.SessionTTL, 720*time.Hour)
	eq(t, "Security.SessionAbsoluteTTL", c.Security.SessionAbsoluteTTL, 2160*time.Hour)
	eq(t, "Security.CookieSecure", c.Security.CookieSecure, true)
	eq(t, "Security.PasswordMinLength", c.Security.PasswordMinLength, 10)
	eq(t, "Registration.Enabled", c.Registration.Enabled, true)
	eq(t, "Registration.PasswordEnabled", c.Registration.PasswordEnabled, true)
	eq(t, "Registration.GoogleEnabled", c.Registration.GoogleEnabled, true)
	eq(t, "Google.Enabled", c.Google.Enabled(), false)
	eq(t, "Google.PollInterval", c.Google.PollInterval, 5*time.Minute)
	eq(t, "SMTP.Port", c.SMTP.Port, 587)
	eq(t, "SMTP.TLS", c.SMTP.TLS, SMTPStartTLS)
	eq(t, "RateLimit.Login", c.RateLimit.Login, Rate{Count: 10, Per: 15 * time.Minute})
	eq(t, "RateLimit.API", c.RateLimit.API, Rate{Count: 600, Per: time.Minute})
	eq(t, "RateLimit.Share", c.RateLimit.Share, Rate{Count: 120, Per: time.Minute})
	eq(t, "Import.MaxSize", c.Import.MaxSize, 20*MB)
	eq(t, "Import.MaxItems", c.Import.MaxItems, 50000)
	eq(t, "Fetch.Timeout", c.Fetch.Timeout, 15*time.Second)
	eq(t, "Fetch.MaxRedirects", c.Fetch.MaxRedirects, 3)
	eq(t, "Fetch.AllowPrivate", c.Fetch.AllowPrivate, false)
	eq(t, "Subscription.DefaultInterval", c.Subscription.DefaultInterval, time.Hour)
	eq(t, "Subscription.MinInterval", c.Subscription.MinInterval, 15*time.Minute)
	eq(t, "Recurrence.MaxInstances", c.Recurrence.MaxInstances, 5000)
	eq(t, "Search.MaxResults", c.Search.MaxResults, 200)
	eq(t, "OG.CacheDir", c.OG.CacheDir, "./data/og")
	eq(t, "OG.CacheMax", c.OG.CacheMax, 2*GB)
	eq(t, "Retention.Trash", c.Retention.Trash, 720*time.Hour)
	eq(t, "Retention.Changes", c.Retention.Changes, 168*time.Hour)
	eq(t, "Retention.Audit", c.Retention.Audit, 8760*time.Hour)
	eq(t, "Worker.Enabled", c.Worker.Enabled, true)
	eq(t, "Worker.Concurrency", c.Worker.Concurrency, 4)
	eq(t, "Log.Level", c.Log.Level, slog.LevelInfo)
	eq(t, "Log.Format", c.Log.Format, LogFormatAuto)
	eq(t, "Log.Color", c.Log.Color, ColorAuto)
	eq(t, "Metrics.Addr", c.Metrics.Addr, "127.0.0.1:9091")
}

func TestParseDevelopmentCookieDefault(t *testing.T) {
	env := validEnv()
	env["APP_ENV"] = "development"
	env["APP_BASE_URL"] = "http://localhost:8080"
	c := mustParse(t, env)
	eq(t, "Security.CookieSecure", c.Security.CookieSecure, false)
	eq(t, "App.Secure", c.App.Secure(), false)
	eq(t, "App.URL", c.App.URL("/cal"), "http://localhost:8080/cal")
}

func TestParseCollectsAllProblems(t *testing.T) {
	env := map[string]string{
		"HTTP_READ_TIMEOUT":  "soon",
		"DATABASE_MAX_CONNS": "0",
		"LOG_FORMAT":         "xml",
		"RATE_LIMIT_API":     "fast",
		"DEFAULT_TIMEZONE":   "Mars/Olympus",
	}
	_, err := Parse(env)
	problems := problemsOf(t, err)
	for _, key := range []string{"DATABASE_URL", "SECRET_KEYS", "HTTP_READ_TIMEOUT", "DATABASE_MAX_CONNS", "LOG_FORMAT", "RATE_LIMIT_API", "DEFAULT_TIMEZONE"} {
		if !hasProblem(problems, key) {
			t.Errorf("missing problem for %s in %v", key, problems)
		}
	}
}

func TestLogOptions(t *testing.T) {
	env := validEnv()
	env["LOG_FORMAT"] = "Pretty"
	env["LOG_COLOR"] = "never"
	c := mustParse(t, env)
	eq(t, "Log.Format", c.Log.Format, LogFormatPretty)
	eq(t, "Log.Color", c.Log.Color, ColorNever)

	env["LOG_COLOR"] = "rainbow"
	_, err := Parse(env)
	if !hasProblem(problemsOf(t, err), "LOG_COLOR") {
		t.Fatal("expected LOG_COLOR problem")
	}
}

func TestCookieSecureRequiresHTTPS(t *testing.T) {
	env := validEnv()
	env["APP_BASE_URL"] = "http://calendar.example.com"
	_, err := Parse(env)
	if !hasProblem(problemsOf(t, err), "COOKIE_SECURE") {
		t.Fatal("expected COOKIE_SECURE problem")
	}
}

func TestBaseURLRejectsPath(t *testing.T) {
	env := validEnv()
	env["APP_BASE_URL"] = "https://example.com/calendar"
	_, err := Parse(env)
	if !hasProblem(problemsOf(t, err), "APP_BASE_URL") {
		t.Fatal("expected APP_BASE_URL problem")
	}
}

func TestSecretKeyActive(t *testing.T) {
	env := validEnv()
	env["SECRET_KEYS"] = "1:" + testKey(1) + ",2:" + testKey(2)
	c := mustParse(t, env)
	eq(t, "SecretKeyActive", c.Security.SecretKeyActive, uint32(2))

	env["SECRET_KEY_ACTIVE"] = "1"
	c = mustParse(t, env)
	eq(t, "SecretKeyActive", c.Security.SecretKeyActive, uint32(1))

	env["SECRET_KEY_ACTIVE"] = "3"
	_, err := Parse(env)
	if !hasProblem(problemsOf(t, err), "SECRET_KEY_ACTIVE") {
		t.Fatal("expected SECRET_KEY_ACTIVE problem")
	}
}

func TestSecretKeyActiveEntryShowsEffective(t *testing.T) {
	env := validEnv()
	env["SECRET_KEYS"] = "1:" + testKey(1) + ",2:" + testKey(2)
	c := mustParse(t, env)
	for _, e := range c.Entries() {
		if e.Key != "SECRET_KEY_ACTIVE" {
			continue
		}
		if e.Value != "2" || !e.Default {
			t.Fatalf("entry = %+v, want value 2 marked default", e)
		}
		return
	}
	t.Fatal("SECRET_KEY_ACTIVE entry missing")
}

func TestEmptyValues(t *testing.T) {
	env := validEnv()
	env["HTTP_READ_TIMEOUT"] = ""
	env["METRICS_ADDR"] = ""
	env["HTTP_TRUSTED_PROXIES"] = ""
	c := mustParse(t, env)
	eq(t, "HTTP.ReadTimeout", c.HTTP.ReadTimeout, 15*time.Second)
	eq(t, "Metrics.Addr", c.Metrics.Addr, "")
	eq(t, "len(HTTP.TrustedProxies)", len(c.HTTP.TrustedProxies), 0)
}

func TestAddrValidation(t *testing.T) {
	env := validEnv()
	env["HTTP_ADDR"] = "8080"
	env["METRICS_ADDR"] = "localhost:99999"
	problems := problemsOf(t, func() error { _, err := Parse(env); return err }())
	for _, key := range []string{"HTTP_ADDR", "METRICS_ADDR"} {
		if !hasProblem(problems, key) {
			t.Errorf("missing problem for %s in %v", key, problems)
		}
	}

	env["HTTP_ADDR"] = ":9091"
	env["METRICS_ADDR"] = ":9091"
	_, err := Parse(env)
	if !hasProblem(problemsOf(t, err), "METRICS_ADDR") {
		t.Fatal("expected METRICS_ADDR problem for shared address")
	}
}

func TestMetricsPublicWarning(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:9091": false,
		"[::1]:9091":     false,
		"localhost:9091": false,
		":9091":          true,
		"0.0.0.0:9091":   true,
		"10.0.0.5:9091":  true,
	}
	for addr, public := range cases {
		env := validEnv()
		env["METRICS_ADDR"] = addr
		c := mustParse(t, env)
		eq(t, addr, hasWarning(c, "METRICS_ADDR"), public)
	}
}

func TestPartialGoogleConfig(t *testing.T) {
	env := validEnv()
	env["GOOGLE_CLIENT_ID"] = "id"
	_, err := Parse(env)
	if !hasProblem(problemsOf(t, err), "GOOGLE_CLIENT_ID") {
		t.Fatal("expected GOOGLE_CLIENT_ID problem")
	}
}

func TestWebhooksDisabledWithoutHTTPS(t *testing.T) {
	env := validEnv()
	env["APP_ENV"] = "development"
	env["APP_BASE_URL"] = "http://localhost:8080"
	env["GOOGLE_CLIENT_ID"] = "id"
	env["GOOGLE_CLIENT_SECRET"] = "secret"
	c := mustParse(t, env)
	eq(t, "Google.WebhooksEnabled", c.Google.WebhooksEnabled, false)
	if !hasWarning(c, "webhooks") {
		t.Errorf("expected webhook warning in %v", c.Warnings)
	}
}

func TestSMTPValidation(t *testing.T) {
	env := validEnv()
	env["SMTP_HOST"] = "mail.example.com"
	_, err := Parse(env)
	if !hasProblem(problemsOf(t, err), "SMTP_FROM") {
		t.Fatal("expected SMTP_FROM problem")
	}
	env["SMTP_FROM"] = "Calendar <calendar@example.com>"
	env["SMTP_USER"] = "calendar"
	_, err = Parse(env)
	if !hasProblem(problemsOf(t, err), "SMTP_USER") {
		t.Fatal("expected SMTP_USER problem")
	}
	env["SMTP_PASSWORD"] = "pw"
	c := mustParse(t, env)
	eq(t, "SMTP.Enabled", c.SMTP.Enabled(), true)
}

func TestSMTPPortTLSMismatch(t *testing.T) {
	env := smtpEnv()
	env["SMTP_PORT"] = "465"
	_, err := Parse(env)
	if !hasProblem(problemsOf(t, err), "SMTP_TLS") {
		t.Fatal("expected SMTP_TLS problem for 465 with starttls")
	}

	env["SMTP_TLS"] = "tls"
	c := mustParse(t, env)
	eq(t, "465 tls warning", hasWarning(c, "SMTP_TLS"), false)

	env["SMTP_PORT"] = "587"
	c = mustParse(t, env)
	eq(t, "587 tls warning", hasWarning(c, "SMTP_TLS"), true)
}

func TestSMTPPlaintextCredentials(t *testing.T) {
	env := smtpEnv()
	env["SMTP_TLS"] = "none"
	_, err := Parse(env)
	if !hasProblem(problemsOf(t, err), "SMTP_TLS") {
		t.Fatal("expected SMTP_TLS problem for plaintext credentials")
	}

	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		env["SMTP_HOST"] = host
		mustParse(t, env)
	}

	env["SMTP_HOST"] = "mail.example.com"
	delete(env, "SMTP_USER")
	delete(env, "SMTP_PASSWORD")
	mustParse(t, env)
}

func TestWebPushValidation(t *testing.T) {
	env := validEnv()
	env["WEBPUSH_VAPID_PUBLIC"] = "pub"
	_, err := Parse(env)
	if !hasProblem(problemsOf(t, err), "WEBPUSH_VAPID_PUBLIC") {
		t.Fatal("expected WEBPUSH_VAPID_PUBLIC problem")
	}
	env["WEBPUSH_VAPID_PRIVATE"] = "priv"
	env["WEBPUSH_VAPID_SUBJECT"] = "calendar@example.com"
	_, err = Parse(env)
	if !hasProblem(problemsOf(t, err), "WEBPUSH_VAPID_SUBJECT") {
		t.Fatal("expected WEBPUSH_VAPID_SUBJECT problem")
	}
	env["WEBPUSH_VAPID_SUBJECT"] = "mailto:calendar@example.com"
	c := mustParse(t, env)
	eq(t, "WebPush.Enabled", c.WebPush.Enabled(), true)
}

func TestPrintMasksSecrets(t *testing.T) {
	env := smtpEnv()
	env["SMTP_PASSWORD"] = "mailpass"
	env["GOOGLE_CLIENT_ID"] = "client-id"
	env["GOOGLE_CLIENT_SECRET"] = "topsecret"
	c := mustParse(t, env)
	var buf bytes.Buffer
	if err := c.Print(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, secret := range []string{"hunter2", "topsecret", "mailpass", testKey(1)} {
		if strings.Contains(out, secret) {
			t.Errorf("output leaks %q", secret)
		}
	}
	for _, want := range []string{"SECRET_KEYS", "1:****", "client-id", "DATABASE_URL"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestLoadPrecedence(t *testing.T) {
	for _, key := range []string{"APP_NAME", "HTTP_ADDR", "DATABASE_URL", "SECRET_KEYS"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	path := filepath.Join(t.TempDir(), ".env")
	content := "APP_NAME=FromFile\n" +
		"HTTP_ADDR=:1111\n" +
		"DATABASE_URL=postgres://u:p@localhost/db\n" +
		"SECRET_KEYS=1:" + testKey(3) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTTP_ADDR", ":2222")
	c, err := Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "App.Name", c.App.Name, "FromFile")
	eq(t, "HTTP.Addr", c.HTTP.Addr, ":2222")
}

func TestLoadMissingFile(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost/db")
	t.Setenv("SECRET_KEYS", "1:"+testKey(4))
	missing := filepath.Join(t.TempDir(), "absent.env")
	if _, err := Load(missing, false); err != nil {
		t.Fatalf("optional missing file: %v", err)
	}
	if _, err := Load(missing, true); err == nil {
		t.Fatal("expected error for required missing file")
	}
}

func TestEmailAllowed(t *testing.T) {
	open := Registration{}
	eq(t, "open", open.EmailAllowed("a@anything.org"), true)

	restricted := Registration{AllowedDomains: []string{"akarpov.ru", "itmo.ru"}}
	cases := map[string]bool{
		"sasha@akarpov.ru":  true,
		"Sasha@AKARPOV.RU":  true,
		"student@itmo.ru":   true,
		"x@evil.com":        false,
		"x@sub.akarpov.ru":  false,
		"no-at-sign":        false,
		"trailing@":         false,
		"a@b@akarpov.ru":    true,
		"x@akarpov.ru.evil": false,
	}
	for email, want := range cases {
		eq(t, email, restricted.EmailAllowed(email), want)
	}
}

func TestDomainsNormalized(t *testing.T) {
	env := validEnv()
	env["REGISTRATION_ALLOWED_DOMAINS"] = " @Akarpov.RU , itmo.ru "
	c := mustParse(t, env)
	if len(c.Registration.AllowedDomains) != 2 || c.Registration.AllowedDomains[0] != "akarpov.ru" || c.Registration.AllowedDomains[1] != "itmo.ru" {
		t.Fatalf("domains = %v", c.Registration.AllowedDomains)
	}
}
