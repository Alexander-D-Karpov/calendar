package config

import (
	"strings"
	"testing"
)

func TestMetricsToken(t *testing.T) {
	env := validEnv()
	env["METRICS_ADDR"] = "10.0.0.5:9091"
	env["METRICS_TOKEN"] = "short"
	_, err := Parse(env)
	if !hasProblem(problemsOf(t, err), "METRICS_TOKEN") {
		t.Fatal("expected METRICS_TOKEN problem")
	}

	env["METRICS_TOKEN"] = strings.Repeat("a", 24)
	c := mustParse(t, env)
	eq(t, "Metrics.Token", c.Metrics.Token, env["METRICS_TOKEN"])
	eq(t, "public warning with token", hasWarning(c, "METRICS_ADDR"), false)
	for _, e := range c.Entries() {
		if e.Key == "METRICS_TOKEN" && e.Value != "********" {
			t.Fatalf("METRICS_TOKEN printed as %q", e.Value)
		}
	}
}
