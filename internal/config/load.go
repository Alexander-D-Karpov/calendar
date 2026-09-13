package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type entry struct {
	Key     string
	Value   string
	Default bool
}

type reader struct {
	env      map[string]string
	entries  []entry
	problems []string
}

func Load(envFile string, mustExist bool) (*Config, error) {
	environ := map[string]string{}
	if envFile != "" {
		vars, err := godotenv.Read(envFile)
		switch {
		case err == nil:
			for k, v := range vars {
				environ[k] = v
			}
		case errors.Is(err, fs.ErrNotExist) && !mustExist:
		default:
			return nil, fmt.Errorf("read env file %s: %w", envFile, err)
		}
	}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			environ[k] = v
		}
	}
	return Parse(environ)
}

func Parse(environ map[string]string) (*Config, error) {
	r := &reader{env: environ}
	c := &Config{}
	c.read(r)
	c.validate(r)
	c.entries = r.entries
	if len(r.problems) > 0 {
		return nil, &Error{Problems: r.problems}
	}
	return c, nil
}

func (c *Config) read(r *reader) {
	c.App.Env = r.enum("APP_ENV", EnvProduction, EnvProduction, EnvDevelopment)
	c.App.BaseURL = r.baseURL("APP_BASE_URL", "https://calendar.akarpov.ru")
	c.App.Name = r.str("APP_NAME", "Calendar")
	c.App.DefaultTimezone = r.location("DEFAULT_TIMEZONE", "UTC")

	c.HTTP.Addr = r.str("HTTP_ADDR", ":8080")
	c.HTTP.TrustedProxies = r.prefixes("HTTP_TRUSTED_PROXIES", "127.0.0.1/32,::1/128")
	c.HTTP.ReadTimeout = r.duration("HTTP_READ_TIMEOUT", "15s", time.Second)
	c.HTTP.WriteTimeout = r.duration("HTTP_WRITE_TIMEOUT", "30s", time.Second)
	c.HTTP.IdleTimeout = r.duration("HTTP_IDLE_TIMEOUT", "120s", time.Second)
	c.HTTP.MaxBody = r.size("HTTP_MAX_BODY", "1MB", KB)

	c.Database.URL = r.databaseURL("DATABASE_URL")
	c.Database.MaxConns = int32(r.integer("DATABASE_MAX_CONNS", "20", 1, 1000))
	c.Database.AutoMigrate = r.boolean("DATABASE_AUTO_MIGRATE", "true")

	c.Security.SecretKeys = r.secretKeys("SECRET_KEYS")
	c.Security.SecretKeyActive = uint32(r.integer("SECRET_KEY_ACTIVE", "0", 0, math.MaxInt32))
	c.Security.SessionTTL = r.duration("SESSION_TTL", "720h", time.Minute)
	c.Security.SessionAbsoluteTTL = r.duration("SESSION_ABSOLUTE_TTL", "2160h", time.Minute)
	c.Security.CookieSecure = r.boolean("COOKIE_SECURE", strconv.FormatBool(!c.App.IsDevelopment()))
	c.Security.PasswordMinLength = r.integer("PASSWORD_MIN_LENGTH", "10", 8, 128)

	c.Registration.Enabled = r.boolean("REGISTRATION_ENABLED", "true")
	c.Registration.AllowedDomains = r.domains("REGISTRATION_ALLOWED_DOMAINS")
	c.Registration.PasswordEnabled = r.boolean("REGISTRATION_PASSWORD_ENABLED", "true")
	c.Registration.GoogleEnabled = r.boolean("REGISTRATION_GOOGLE_ENABLED", "true")

	c.Google.ClientID = r.str("GOOGLE_CLIENT_ID", "")
	c.Google.ClientSecret = r.secret("GOOGLE_CLIENT_SECRET")
	c.Google.SyncEnabled = r.boolean("GOOGLE_SYNC_ENABLED", "true")
	c.Google.WebhooksEnabled = r.boolean("GOOGLE_WEBHOOKS_ENABLED", "true")
	c.Google.PollInterval = r.duration("GOOGLE_POLL_INTERVAL", "5m", time.Minute)

	c.SMTP.Host = r.str("SMTP_HOST", "")
	c.SMTP.Port = r.integer("SMTP_PORT", "587", 1, 65535)
	c.SMTP.User = r.str("SMTP_USER", "")
	c.SMTP.Password = r.secret("SMTP_PASSWORD")
	c.SMTP.From = r.str("SMTP_FROM", "")
	c.SMTP.TLS = r.enum("SMTP_TLS", SMTPStartTLS, SMTPStartTLS, SMTPImplicitTLS, SMTPNoTLS)

	c.WebPush.PublicKey = r.str("WEBPUSH_VAPID_PUBLIC", "")
	c.WebPush.PrivateKey = r.secret("WEBPUSH_VAPID_PRIVATE")
	c.WebPush.Subject = r.str("WEBPUSH_VAPID_SUBJECT", "")

	c.RateLimit.Login = r.rate("RATE_LIMIT_LOGIN", "10/15m")
	c.RateLimit.API = r.rate("RATE_LIMIT_API", "600/1m")
	c.RateLimit.Share = r.rate("RATE_LIMIT_SHARE", "120/1m")

	c.Import.MaxSize = r.size("IMPORT_MAX_SIZE", "20MB", KB)
	c.Import.MaxItems = r.integer("IMPORT_MAX_ITEMS", "50000", 1, 10_000_000)

	c.Fetch.Timeout = r.duration("URL_FETCH_TIMEOUT", "15s", time.Second)
	c.Fetch.MaxRedirects = r.integer("URL_FETCH_MAX_REDIRECTS", "3", 0, 10)
	c.Fetch.AllowPrivate = r.boolean("URL_FETCH_ALLOW_PRIVATE", "false")

	c.Outbound.Proxy = r.proxyURL("OUTBOUND_PROXY")
	c.Outbound.ProxyFor = r.purposes("OUTBOUND_PROXY_FOR", "google,push,sentry")
	c.Outbound.NoProxy = r.list("OUTBOUND_NO_PROXY")

	c.Subscription.DefaultInterval = r.duration("SUBSCRIPTION_DEFAULT_INTERVAL", "1h", time.Minute)
	c.Subscription.MinInterval = r.duration("SUBSCRIPTION_MIN_INTERVAL", "15m", time.Minute)

	c.Recurrence.MaxInstances = r.integer("RECURRENCE_MAX_INSTANCES", "5000", 1, 100_000)
	c.Search.MaxResults = r.integer("SEARCH_MAX_RESULTS", "200", 1, 1000)

	c.OG.CacheDir = r.str("OG_CACHE_DIR", "./data/og")
	c.OG.CacheMax = r.size("OG_CACHE_MAX", "2GB", MB)

	c.Retention.Trash = r.duration("TRASH_RETENTION", "720h", time.Hour)
	c.Retention.Changes = r.duration("CHANGES_RETENTION", "168h", time.Hour)
	c.Retention.Audit = r.duration("AUDIT_RETENTION", "8760h", time.Hour)

	c.Worker.Enabled = r.boolean("WORKER_ENABLED", "true")
	c.Worker.Concurrency = r.integer("WORKER_CONCURRENCY", "4", 1, 64)

	c.Log.Level = r.logLevel("LOG_LEVEL", "info")
	c.Log.Format = r.enum("LOG_FORMAT", LogFormatAuto, LogFormatAuto, LogFormatJSON, LogFormatText, LogFormatPretty)
	c.Log.Color = r.enum("LOG_COLOR", ColorAuto, ColorAuto, ColorAlways, ColorNever)

	c.Metrics.Addr = r.str("METRICS_ADDR", "127.0.0.1:9091")
	c.Metrics.Token = r.secret("METRICS_TOKEN")

	c.Sentry.DSN = r.dsn("SENTRY_DSN")
	c.Sentry.Environment = r.text("SENTRY_ENVIRONMENT", c.App.Env)
	c.Sentry.TracesSampleRate = r.fraction("SENTRY_TRACES_SAMPLE_RATE", "0")
}

func (r *reader) lookup(key string) (string, bool) {
	v, ok := r.env[key]
	return strings.TrimSpace(v), ok
}

func (r *reader) value(key, def string) (string, bool) {
	v, ok := r.lookup(key)
	if !ok || v == "" {
		return def, true
	}
	return v, false
}

func (r *reader) record(key, value string, isDefault bool) {
	r.entries = append(r.entries, entry{Key: key, Value: value, Default: isDefault})
}

func (r *reader) update(key, value string) {
	for i := range r.entries {
		if r.entries[i].Key == key {
			r.entries[i].Value = value
			return
		}
	}
}

func (r *reader) fail(key, format string, args ...any) {
	r.problems = append(r.problems, key+": "+fmt.Sprintf(format, args...))
}

func (r *reader) str(key, def string) string {
	v, ok := r.lookup(key)
	if !ok {
		v = def
	}
	r.record(key, v, !ok)
	return v
}

func (r *reader) text(key, def string) string {
	v, isDef := r.value(key, def)
	r.record(key, v, isDef)
	return v
}

func (r *reader) list(key string) []string {
	v, ok := r.lookup(key)
	r.record(key, v, !ok)
	return ParseList(v)
}

func (r *reader) secret(key string) string {
	v, ok := r.lookup(key)
	masked := ""
	if v != "" {
		masked = "********"
	}
	r.record(key, masked, !ok)
	return v
}

func (r *reader) enum(key, def string, allowed ...string) string {
	v, isDef := r.value(key, def)
	v = strings.ToLower(v)
	r.record(key, v, isDef)
	if !slices.Contains(allowed, v) {
		r.fail(key, "must be one of %s, got %q", strings.Join(allowed, ", "), v)
		return def
	}
	return v
}

func (r *reader) boolean(key, def string) bool {
	v, isDef := r.value(key, def)
	r.record(key, v, isDef)
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	r.fail(key, "invalid boolean %q", v)
	return false
}

func (r *reader) integer(key, def string, lo, hi int) int {
	v, isDef := r.value(key, def)
	r.record(key, v, isDef)
	n, err := strconv.Atoi(v)
	if err != nil {
		r.fail(key, "invalid integer %q", v)
		return 0
	}
	if n < lo || n > hi {
		r.fail(key, "must be between %d and %d, got %d", lo, hi, n)
	}
	return n
}

func (r *reader) fraction(key, def string) float64 {
	v, isDef := r.value(key, def)
	r.record(key, v, isDef)
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) {
		r.fail(key, "invalid number %q", v)
		return 0
	}
	if f < 0 || f > 1 {
		r.fail(key, "must be between 0 and 1, got %s", v)
		return 0
	}
	return f
}

func (r *reader) duration(key, def string, lo time.Duration) time.Duration {
	v, isDef := r.value(key, def)
	r.record(key, v, isDef)
	d, err := time.ParseDuration(v)
	if err != nil {
		r.fail(key, "invalid duration %q", v)
		return 0
	}
	if d < lo {
		r.fail(key, "must be at least %s, got %s", formatDuration(lo), v)
	}
	return d
}

func (r *reader) size(key, def string, lo ByteSize) ByteSize {
	v, isDef := r.value(key, def)
	r.record(key, v, isDef)
	n, err := ParseByteSize(v)
	if err != nil {
		r.fail(key, "%v", err)
		return 0
	}
	if n < lo {
		r.fail(key, "must be at least %s, got %s", lo, v)
	}
	return n
}

func (r *reader) rate(key, def string) Rate {
	v, isDef := r.value(key, def)
	r.record(key, v, isDef)
	rate, err := ParseRate(v)
	if err != nil {
		r.fail(key, "%v", err)
	}
	return rate
}

func (r *reader) location(key, def string) *time.Location {
	v, isDef := r.value(key, def)
	r.record(key, v, isDef)
	loc, err := time.LoadLocation(v)
	if err != nil {
		r.fail(key, "unknown timezone %q", v)
		return time.UTC
	}
	return loc
}

func (r *reader) logLevel(key, def string) slog.Level {
	v, isDef := r.value(key, def)
	r.record(key, v, isDef)
	var level slog.Level
	if err := level.UnmarshalText([]byte(v)); err != nil {
		r.fail(key, "must be debug, info, warn or error, got %q", v)
		return slog.LevelInfo
	}
	return level
}

func (r *reader) prefixes(key, def string) []netip.Prefix {
	v, ok := r.lookup(key)
	if !ok {
		v = def
	}
	r.record(key, v, !ok)
	p, err := ParsePrefixes(v)
	if err != nil {
		r.fail(key, "%v", err)
	}
	return p
}

func (r *reader) domains(key string) []string {
	v, ok := r.lookup(key)
	r.record(key, v, !ok)
	var out []string
	for _, d := range ParseList(v) {
		d = strings.ToLower(strings.TrimPrefix(d, "@"))
		if d == "" || strings.ContainsAny(d, "@/ \t") {
			r.fail(key, "invalid domain %q", d)
			continue
		}
		out = append(out, d)
	}
	return out
}

func (r *reader) purposes(key, def string) []string {
	v, isDef := r.value(key, def)
	v = strings.ToLower(v)
	r.record(key, v, isDef)
	var out []string
	for _, p := range ParseList(v) {
		switch {
		case p == "all":
			out = slices.Clone(OutboundPurposes)
		case !slices.Contains(OutboundPurposes, p):
			r.fail(key, "unknown purpose %q, expected %s or all", p, strings.Join(OutboundPurposes, ", "))
		case !slices.Contains(out, p):
			out = append(out, p)
		}
	}
	return out
}

func (r *reader) proxyURL(key string) *url.URL {
	v, ok := r.lookup(key)
	if v == "" {
		r.record(key, "", !ok)
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || u.Hostname() == "" {
		r.record(key, "<invalid>", false)
		r.fail(key, "must be a URL like http://host:3128 or socks5://host:1080")
		return nil
	}
	r.record(key, u.Redacted(), false)
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		r.fail(key, "scheme must be http, https, socks5 or socks5h, got %q", u.Scheme)
		return nil
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		r.fail(key, "must not contain a path, query or fragment")
		return nil
	}
	u.Path = ""
	return u
}

func (r *reader) dsn(key string) string {
	v, ok := r.lookup(key)
	if v == "" {
		r.record(key, "", !ok)
		return ""
	}
	u, err := url.Parse(v)
	valid := err == nil &&
		(u.Scheme == "https" || u.Scheme == "http") &&
		u.Host != "" &&
		u.User != nil && u.User.Username() != "" &&
		strings.Trim(u.Path, "/") != ""
	if !valid {
		r.record(key, "<invalid>", false)
		r.fail(key, "must look like https://<key>@<host>/<project>")
		return ""
	}
	r.record(key, u.Scheme+"://****@"+u.Host+u.Path, false)
	return v
}

func (r *reader) baseURL(key, def string) *url.URL {
	v, isDef := r.value(key, def)
	r.record(key, v, isDef)
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		r.fail(key, "must be an absolute http or https URL, got %q", v)
		return nil
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		r.fail(key, "must not contain credentials, path, query or fragment")
		return nil
	}
	u.Path = ""
	u.RawPath = ""
	u.Host = strings.ToLower(u.Host)
	if p := u.Port(); (u.Scheme == "https" && p == "443") || (u.Scheme == "http" && p == "80") {
		u.Host = strings.TrimSuffix(u.Host, ":"+p)
	}
	return u
}

func (r *reader) databaseURL(key string) string {
	v, ok := r.lookup(key)
	if v == "" {
		r.record(key, "", !ok)
		r.fail(key, "is required")
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		r.record(key, "<invalid>", false)
		r.fail(key, "must be a postgres:// URL")
		return ""
	}
	r.record(key, u.Redacted(), false)
	return v
}

func (r *reader) secretKeys(key string) SecretKeys {
	v, ok := r.lookup(key)
	if v == "" {
		r.record(key, "", !ok)
		r.fail(key, "is required, generate a key with: openssl rand -base64 32")
		return nil
	}
	keys, err := ParseSecretKeys(v)
	if err != nil {
		r.record(key, "<invalid>", false)
		r.fail(key, "%v", err)
		return nil
	}
	r.record(key, keys.Masked(), false)
	return keys
}
