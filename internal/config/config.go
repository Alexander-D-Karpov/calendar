package config

import (
	"log/slog"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	EnvProduction  = "production"
	EnvDevelopment = "development"
)

const (
	SMTPStartTLS    = "starttls"
	SMTPImplicitTLS = "tls"
	SMTPNoTLS       = "none"
)

const (
	LogFormatAuto   = "auto"
	LogFormatJSON   = "json"
	LogFormatText   = "text"
	LogFormatPretty = "pretty"
)

const (
	ColorAuto   = "auto"
	ColorAlways = "always"
	ColorNever  = "never"
)

const (
	PurposeGoogle = "google"
	PurposePush   = "push"
	PurposeSentry = "sentry"
	PurposeFetch  = "fetch"
)

var OutboundPurposes = []string{PurposeGoogle, PurposePush, PurposeSentry, PurposeFetch}

type Config struct {
	App          App
	HTTP         HTTP
	Database     Database
	Security     Security
	Registration Registration
	Google       Google
	SMTP         SMTP
	WebPush      WebPush
	RateLimit    RateLimits
	Import       Import
	Fetch        Fetch
	Outbound     Outbound
	Subscription Subscription
	Recurrence   Recurrence
	Search       Search
	OG           OG
	Retention    Retention
	Worker       Worker
	Log          Log
	Metrics      Metrics
	Sentry       Sentry
	Warnings     []string

	entries []entry
}

type App struct {
	Env             string
	BaseURL         *url.URL
	Name            string
	DefaultTimezone *time.Location
}

func (a App) IsDevelopment() bool {
	return a.Env == EnvDevelopment
}

func (a App) Secure() bool {
	return a.BaseURL != nil && a.BaseURL.Scheme == "https"
}

func (a App) Origin() string {
	if a.BaseURL == nil {
		return ""
	}
	return a.BaseURL.Scheme + "://" + a.BaseURL.Host
}

func (a App) URL(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return a.Origin() + path
}

type HTTP struct {
	Addr           string
	TrustedProxies []netip.Prefix
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	IdleTimeout    time.Duration
	MaxBody        ByteSize
}

type Database struct {
	URL         string
	MaxConns    int32
	AutoMigrate bool
}

type Security struct {
	SecretKeys         SecretKeys
	SecretKeyActive    uint32
	SessionTTL         time.Duration
	SessionAbsoluteTTL time.Duration
	CookieSecure       bool
	PasswordMinLength  int
}

type Registration struct {
	Enabled         bool
	AllowedDomains  []string
	PasswordEnabled bool
	GoogleEnabled   bool
}

func (r Registration) EmailAllowed(email string) bool {
	if len(r.AllowedDomains) == 0 {
		return true
	}
	at := strings.LastIndexByte(email, '@')
	if at < 0 || at == len(email)-1 {
		return false
	}
	domain := strings.ToLower(strings.TrimSpace(email[at+1:]))
	return slices.Contains(r.AllowedDomains, domain)
}

type Google struct {
	ClientID        string
	ClientSecret    string
	SyncEnabled     bool
	WebhooksEnabled bool
	PollInterval    time.Duration
}

func (g Google) Enabled() bool {
	return g.ClientID != "" && g.ClientSecret != ""
}

func (g Google) SyncActive() bool {
	return g.Enabled() && g.SyncEnabled
}

type SMTP struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
	TLS      string
}

func (s SMTP) Enabled() bool {
	return s.Host != ""
}

type WebPush struct {
	PublicKey  string
	PrivateKey string
	Subject    string
}

func (w WebPush) Enabled() bool {
	return w.PublicKey != "" && w.PrivateKey != "" && w.Subject != ""
}

type RateLimits struct {
	Login Rate
	API   Rate
	Share Rate
}

type Import struct {
	MaxSize  ByteSize
	MaxItems int
}

type Fetch struct {
	Timeout      time.Duration
	MaxRedirects int
	AllowPrivate bool
}

type Outbound struct {
	Proxy    *url.URL
	ProxyFor []string
	NoProxy  []string
}

func (o Outbound) Enabled() bool {
	return o.Proxy != nil
}

func (o Outbound) Proxied(purpose string) bool {
	return o.Proxy != nil && slices.Contains(o.ProxyFor, purpose)
}

type Subscription struct {
	DefaultInterval time.Duration
	MinInterval     time.Duration
}

type Recurrence struct {
	MaxInstances int
}

type Search struct {
	MaxResults int
}

type OG struct {
	CacheDir string
	CacheMax ByteSize
}

type Retention struct {
	Trash   time.Duration
	Changes time.Duration
	Audit   time.Duration
}

type Worker struct {
	Enabled     bool
	Concurrency int
}

type Log struct {
	Level  slog.Level
	Format string
	Color  string
}

type Metrics struct {
	Addr  string
	Token string
}

type Sentry struct {
	DSN              string
	Environment      string
	TracesSampleRate float64
}

func (s Sentry) Enabled() bool {
	return s.DSN != ""
}
