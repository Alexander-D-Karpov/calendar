package mail

import (
	"strings"
	"testing"
)

func TestBuildHeaders(t *testing.T) {
	msg, err := build("Calendar <no-reply@calendar.test>", "sasha@akarpov.ru", "Confirm your email", "Hello\nthere\n")
	if err != nil {
		t.Fatal(err)
	}
	s := string(msg)
	head, body, ok := strings.Cut(s, "\r\n\r\n")
	if !ok {
		t.Fatal("message must separate headers from the body with a blank line")
	}
	for _, want := range []string{
		"From: Calendar <no-reply@calendar.test>",
		"To: sasha@akarpov.ru",
		"Subject: Confirm your email",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: 8bit",
	} {
		if !strings.Contains(head, want) {
			t.Errorf("headers missing %q", want)
		}
	}
	if !strings.Contains(head, "@calendar.test>") || !strings.Contains(head, "Message-ID: <") {
		t.Errorf("Message-ID must be scoped to the From host:\n%s", head)
	}
	if strings.Contains(body, "\n") && !strings.Contains(body, "\r\n") {
		t.Error("body must use CRLF line endings")
	}
	for _, line := range strings.Split(s, "\r\n") {
		if strings.Contains(line, "\n") {
			t.Errorf("bare newline in %q", line)
		}
	}
}

func TestBuildEncodesNonASCII(t *testing.T) {
	msg, err := build("no-reply@calendar.test", "sasha@akarpov.ru", "Подтвердите почту", "x")
	if err != nil {
		t.Fatal(err)
	}
	s := string(msg)
	if strings.Contains(s, "Подтвердите") {
		t.Error("a non-ASCII subject must be RFC 2047 encoded")
	}
	if !strings.Contains(s, "Subject: =?utf-8?") {
		t.Errorf("subject not encoded:\n%s", s)
	}
}

func TestBuildRejectsInjection(t *testing.T) {
	cases := map[string][3]string{
		"subject CRLF": {"a@b.test", "c@d.test", "Hi\r\nBcc: evil@x.test"},
		"subject LF":   {"a@b.test", "c@d.test", "Hi\nBcc: evil@x.test"},
		"to CRLF":      {"a@b.test", "c@d.test\r\nBcc: evil@x.test", "Hi"},
		"from LF":      {"a@b.test\nBcc: evil@x.test", "c@d.test", "Hi"},
	}
	for name, c := range cases {
		if _, err := build(c[0], c[1], c[2], "body"); err == nil {
			t.Errorf("%s: injection accepted", name)
		}
	}
}

func TestNilSenderSends(t *testing.T) {
	var s *Sender
	if err := s.Send(t.Context(), KindVerify, "a@b.test", "x", "y"); err != nil {
		t.Fatalf("nil sender must be a no-op, got %v", err)
	}
}

func TestMessages(t *testing.T) {
	d := Data{AppName: "Calendar", Name: "Sasha", URL: "https://calendar.test/verify?token=x"}
	v, r := Verify(d), Reset(d)
	if !strings.Contains(v, d.URL) || !strings.Contains(v, "24 hours") {
		t.Errorf("verify body = %q", v)
	}
	if !strings.Contains(r, d.URL) || !strings.Contains(r, "1 hour") || !strings.Contains(r, "stays unchanged") {
		t.Errorf("reset body = %q", r)
	}
	if !strings.Contains(Verify(Data{AppName: "Calendar", URL: "u"}), "Hello there") {
		t.Error("a missing name must fall back")
	}
}
