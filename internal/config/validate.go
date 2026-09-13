package config

import (
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"strconv"
	"strings"
)

type Error struct {
	Problems []string
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("invalid configuration:")
	for _, p := range e.Problems {
		b.WriteString("\n  - ")
		b.WriteString(p)
	}
	return b.String()
}

func (c *Config) validate(r *reader) {
	if c.App.Name == "" {
		r.fail("APP_NAME", "is required")
	}
	if c.HTTP.Addr == "" {
		r.fail("HTTP_ADDR", "is required")
	} else {
		validateAddr(r, "HTTP_ADDR", c.HTTP.Addr)
	}
	if c.Metrics.Addr != "" {
		validateAddr(r, "METRICS_ADDR", c.Metrics.Addr)
		if c.Metrics.Addr == c.HTTP.Addr {
			r.fail("METRICS_ADDR", "must differ from HTTP_ADDR")
		}
		if t := c.Metrics.Token; t != "" && len(t) < 16 {
			r.fail("METRICS_TOKEN", "must be at least 16 characters, generate one with: openssl rand -hex 24")
		}
	}
	if c.OG.CacheDir == "" {
		r.fail("OG_CACHE_DIR", "is required")
	}

	if c.Security.SessionAbsoluteTTL < c.Security.SessionTTL {
		r.fail("SESSION_ABSOLUTE_TTL", "must be greater than or equal to SESSION_TTL")
	}
	if c.Security.SecretKeys != nil {
		if c.Security.SecretKeyActive == 0 {
			c.Security.SecretKeyActive = c.Security.SecretKeys.Highest()
			r.update("SECRET_KEY_ACTIVE", strconv.FormatUint(uint64(c.Security.SecretKeyActive), 10))
		} else if _, ok := c.Security.SecretKeys[c.Security.SecretKeyActive]; !ok {
			r.fail("SECRET_KEY_ACTIVE", "key %d is not present in SECRET_KEYS", c.Security.SecretKeyActive)
		}
	}
	if c.Security.CookieSecure && c.App.BaseURL != nil && c.App.BaseURL.Scheme != "https" {
		r.fail("COOKIE_SECURE", "requires an https APP_BASE_URL, set COOKIE_SECURE=false for plain http")
	}

	if (c.Google.ClientID == "") != (c.Google.ClientSecret == "") {
		r.fail("GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must be set together")
	}

	if c.SMTP.Enabled() {
		c.validateSMTP(r)
	}

	pushSet := 0
	for _, v := range []string{c.WebPush.PublicKey, c.WebPush.PrivateKey, c.WebPush.Subject} {
		if v != "" {
			pushSet++
		}
	}
	if pushSet != 0 && pushSet != 3 {
		r.fail("WEBPUSH_VAPID_PUBLIC", "WEBPUSH_VAPID_PUBLIC, WEBPUSH_VAPID_PRIVATE and WEBPUSH_VAPID_SUBJECT must be set together")
	}
	if s := c.WebPush.Subject; s != "" && !strings.HasPrefix(s, "mailto:") && !strings.HasPrefix(s, "https://") {
		r.fail("WEBPUSH_VAPID_SUBJECT", "must start with mailto: or https://")
	}

	if c.Subscription.MinInterval > c.Subscription.DefaultInterval {
		r.fail("SUBSCRIPTION_MIN_INTERVAL", "must not exceed SUBSCRIPTION_DEFAULT_INTERVAL")
	}

	if c.Sentry.Enabled() {
		if e := c.Sentry.Environment; e == "" || len(e) > 64 || strings.ContainsAny(e, "/ \t\r\n") {
			r.fail("SENTRY_ENVIRONMENT", "must be 1 to 64 characters without slashes or whitespace")
		}
	}

	c.derive()
}

func (c *Config) validateSMTP(r *reader) {
	if c.SMTP.From == "" {
		r.fail("SMTP_FROM", "is required when SMTP_HOST is set")
	} else if _, err := mail.ParseAddress(c.SMTP.From); err != nil {
		r.fail("SMTP_FROM", "invalid address %q", c.SMTP.From)
	}
	if (c.SMTP.User == "") != (c.SMTP.Password == "") {
		r.fail("SMTP_USER", "SMTP_USER and SMTP_PASSWORD must be set together")
	}
	switch {
	case c.SMTP.Port == 465 && c.SMTP.TLS == SMTPStartTLS:
		r.fail("SMTP_TLS", "port 465 uses implicit TLS, set SMTP_TLS=tls")
	case c.SMTP.TLS == SMTPNoTLS && c.SMTP.User != "" && !isLoopbackHost(c.SMTP.Host):
		r.fail("SMTP_TLS", "refusing to send SMTP credentials to %s without TLS", c.SMTP.Host)
	}
}

func (c *Config) derive() {
	if !c.Google.Enabled() {
		c.warn("Google login and sync are disabled because GOOGLE_CLIENT_ID is empty")
	} else if c.Google.WebhooksEnabled && !c.App.Secure() {
		c.Google.WebhooksEnabled = false
		c.warn("Google push webhooks need an https APP_BASE_URL, using polling only")
	}
	if !c.SMTP.Enabled() {
		c.warn("SMTP is not configured, email verification is skipped and password reset is unavailable")
	} else if c.SMTP.TLS == SMTPImplicitTLS && (c.SMTP.Port == 587 || c.SMTP.Port == 25) {
		c.warn(fmt.Sprintf("SMTP_TLS=tls on port %d usually needs SMTP_TLS=starttls", c.SMTP.Port))
	}
	if !c.WebPush.Enabled() {
		c.warn("Web Push is disabled, generate VAPID keys with: calendar vapid generate")
	}
	googleSignup := c.Registration.GoogleEnabled && c.Google.Enabled()
	if c.Registration.Enabled && !c.Registration.PasswordEnabled && !googleSignup {
		c.warn("registration is enabled but no signup method is available")
	}
	if c.Metrics.Addr != "" && c.Metrics.Token == "" {
		if host, _, err := net.SplitHostPort(c.Metrics.Addr); err == nil && !isLoopbackHost(host) {
			c.warn("METRICS_ADDR " + c.Metrics.Addr + " listens beyond localhost without METRICS_TOKEN")
		}
	}
	if p := c.Outbound.Proxy; p != nil {
		if c.Outbound.Proxied(PurposeFetch) && !c.Fetch.AllowPrivate {
			c.warn("URL fetches through OUTBOUND_PROXY rely on a DNS pre-flight check, block private ranges on the proxy too")
		}
		if p.Scheme == "http" && p.User != nil && !isLoopbackHost(p.Hostname()) {
			c.warn("OUTBOUND_PROXY credentials are sent in clear text over http")
		}
	}
}

func (c *Config) warn(msg string) {
	c.Warnings = append(c.Warnings, msg)
}

func validateAddr(r *reader, key, addr string) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		r.fail(key, "invalid listen address %q, expected host:port or :port", addr)
		return
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		r.fail(key, "invalid port %q", port)
	}
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" {
		return true
	}
	a, err := netip.ParseAddr(host)
	return err == nil && a.Unmap().IsLoopback()
}
