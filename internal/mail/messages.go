package mail

import (
	"strings"
	"text/template"
)

type Data struct {
	AppName string
	Name    string
	URL     string
}

const (
	KindVerify = "verify"
	KindReset  = "reset"

	SubjectVerify = "Confirm your email"
	SubjectReset  = "Reset your password"
)

var (
	verifyBody = template.Must(template.New("verify").Parse(
		`Hello {{.Name}},

Confirm your email address to finish setting up your {{.AppName}} account:

{{.URL}}

The link expires in 24 hours.

If you did not create this account, ignore this email.
`))

	resetBody = template.Must(template.New("reset").Parse(
		`Hello {{.Name}},

Reset the password for your {{.AppName}} account:

{{.URL}}

The link expires in 1 hour.

If you did not ask for this, ignore this email and your password stays unchanged.
`))
)

func Verify(d Data) string {
	return render(verifyBody, d)
}

func Reset(d Data) string {
	return render(resetBody, d)
}

func render(t *template.Template, d Data) string {
	if d.Name == "" {
		d.Name = "there"
	}
	var b strings.Builder
	if err := t.Execute(&b, d); err != nil {
		return d.URL
	}
	return b.String()
}
