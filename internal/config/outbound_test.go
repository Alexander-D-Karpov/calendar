package config

import (
	"bytes"
	"strings"
	"testing"
)

func TestOutboundAndSentryDefaults(t *testing.T) {
	c := mustParse(t, validEnv())
	eq(t, "Outbound.Enabled", c.Outbound.Enabled(), false)
	eq(t, "Outbound.Proxied(google)", c.Outbound.Proxied(PurposeGoogle), false)
	eq(t, "Outbound.ProxyFor", strings.Join(c.Outbound.ProxyFor, ","), "google,push,sentry")
	eq(t, "len(Outbound.NoProxy)", len(c.Outbound.NoProxy), 0)
	eq(t, "Sentry.Enabled", c.Sentry.Enabled(), false)
	eq(t, "Sentry.Environment", c.Sentry.Environment, EnvProduction)
	eq(t, "Sentry.TracesSampleRate", c.Sentry.TracesSampleRate, 0.0)
}

func TestOutboundProxy(t *testing.T) {
	env := validEnv()
	env["OUTBOUND_PROXY"] = "socks5h://user:pw@proxy.example.lt:1080"
	env["OUTBOUND_PROXY_FOR"] = "Google, fetch, google"
	env["OUTBOUND_NO_PROXY"] = "localhost, .internal"
	c := mustParse(t, env)
	eq(t, "Proxy.Scheme", c.Outbound.Proxy.Scheme, "socks5h")
	eq(t, "Proxy.Host", c.Outbound.Proxy.Host, "proxy.example.lt:1080")
	eq(t, "ProxyFor", strings.Join(c.Outbound.ProxyFor, ","), "google,fetch")
	eq(t, "Proxied(fetch)", c.Outbound.Proxied(PurposeFetch), true)
	eq(t, "Proxied(push)", c.Outbound.Proxied(PurposePush), false)
	eq(t, "len(NoProxy)", len(c.Outbound.NoProxy), 2)
	eq(t, "fetch warning", hasWarning(c, "DNS pre-flight"), true)
	eq(t, "cleartext warning", hasWarning(c, "clear text"), false)
}

func TestOutboundProxyAll(t *testing.T) {
	env := validEnv()
	env["OUTBOUND_PROXY"] = "http://127.0.0.1:3128"
	env["OUTBOUND_PROXY_FOR"] = "all"
	c := mustParse(t, env)
	eq(t, "ProxyFor", strings.Join(c.Outbound.ProxyFor, ","), "google,push,sentry,fetch")
	eq(t, "fetch warning", hasWarning(c, "DNS pre-flight"), true)

	env["URL_FETCH_ALLOW_PRIVATE"] = "true"
	c = mustParse(t, env)
	eq(t, "fetch warning with private allowed", hasWarning(c, "DNS pre-flight"), false)
}

func TestOutboundProxyInvalid(t *testing.T) {
	for _, v := range []string{"proxy:3128", "ftp://proxy:21", "http://proxy:3128/path", "http://", "http://:3128", "http://proxy:3128?x=1"} {
		env := validEnv()
		env["OUTBOUND_PROXY"] = v
		_, err := Parse(env)
		if !hasProblem(problemsOf(t, err), "OUTBOUND_PROXY") {
			t.Errorf("OUTBOUND_PROXY=%q: expected problem", v)
		}
	}

	env := validEnv()
	env["OUTBOUND_PROXY_FOR"] = "google,mail"
	_, err := Parse(env)
	if !hasProblem(problemsOf(t, err), "OUTBOUND_PROXY_FOR") {
		t.Fatal("expected OUTBOUND_PROXY_FOR problem")
	}
}

func TestOutboundCleartextCredentials(t *testing.T) {
	env := validEnv()
	env["OUTBOUND_PROXY"] = "http://cal:pw@proxy.example.lt:3128"
	c := mustParse(t, env)
	eq(t, "remote http credentials", hasWarning(c, "clear text"), true)

	env["OUTBOUND_PROXY"] = "http://cal:pw@127.0.0.1:3128"
	c = mustParse(t, env)
	eq(t, "loopback http credentials", hasWarning(c, "clear text"), false)

	env["OUTBOUND_PROXY"] = "https://cal:pw@proxy.example.lt:3129"
	c = mustParse(t, env)
	eq(t, "https credentials", hasWarning(c, "clear text"), false)
}

func TestSentryConfig(t *testing.T) {
	env := validEnv()
	env["SENTRY_DSN"] = "https://abc123@o1.ingest.sentry.io/42"
	env["SENTRY_TRACES_SAMPLE_RATE"] = "0.25"
	c := mustParse(t, env)
	eq(t, "Sentry.Enabled", c.Sentry.Enabled(), true)
	eq(t, "Sentry.Environment", c.Sentry.Environment, EnvProduction)
	eq(t, "Sentry.TracesSampleRate", c.Sentry.TracesSampleRate, 0.25)

	env["SENTRY_ENVIRONMENT"] = "staging"
	c = mustParse(t, env)
	eq(t, "Sentry.Environment", c.Sentry.Environment, "staging")

	env["SENTRY_ENVIRONMENT"] = ""
	env["APP_ENV"] = "development"
	env["APP_BASE_URL"] = "http://localhost:8080"
	c = mustParse(t, env)
	eq(t, "Sentry.Environment follows APP_ENV", c.Sentry.Environment, EnvDevelopment)
}

func TestSentryInvalid(t *testing.T) {
	cases := map[string]map[string]string{
		"SENTRY_DSN": {
			"SENTRY_DSN": "https://o1.ingest.sentry.io/42",
		},
		"SENTRY_DSN ": {
			"SENTRY_DSN": "https://abc@o1.ingest.sentry.io/",
		},
		"SENTRY_TRACES_SAMPLE_RATE": {
			"SENTRY_TRACES_SAMPLE_RATE": "2",
		},
		"SENTRY_TRACES_SAMPLE_RATE ": {
			"SENTRY_TRACES_SAMPLE_RATE": "often",
		},
		"SENTRY_ENVIRONMENT": {
			"SENTRY_DSN":         "https://abc@o1.ingest.sentry.io/42",
			"SENTRY_ENVIRONMENT": "prod/eu",
		},
	}
	for name, extra := range cases {
		env := validEnv()
		for k, v := range extra {
			env[k] = v
		}
		_, err := Parse(env)
		if !hasProblem(problemsOf(t, err), strings.TrimSpace(name)) {
			t.Errorf("%s: expected problem for %v", name, extra)
		}
	}
}

func TestPrintMasksOutboundSecrets(t *testing.T) {
	env := validEnv()
	env["OUTBOUND_PROXY"] = "http://cal:hunter3proxy@proxy.example.lt:3128"
	env["SENTRY_DSN"] = "https://abc123key@o1.ingest.sentry.io/42"
	c := mustParse(t, env)
	var buf bytes.Buffer
	if err := c.Print(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, secret := range []string{"hunter3proxy", "abc123key"} {
		if strings.Contains(out, secret) {
			t.Errorf("output leaks %q", secret)
		}
	}
	for _, want := range []string{"proxy.example.lt:3128", "https://****@o1.ingest.sentry.io/42"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}
