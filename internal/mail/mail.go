package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/metrics"
)

const dialTimeout = 15 * time.Second

type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
	TLS      string
}

type Sender struct {
	cfg     Config
	logger  *slog.Logger
	metrics *metrics.Metrics
}

func New(c Config, logger *slog.Logger, m *metrics.Metrics) *Sender {
	if logger == nil {
		logger = slog.Default()
	}
	return &Sender{cfg: c, logger: logger, metrics: m}
}

// Send is a no-op on a nil Sender so callers do not branch when SMTP is off.
func (s *Sender) Send(ctx context.Context, kind, to, subject, text string) error {
	if s == nil {
		return nil
	}
	msg, err := build(s.cfg.From, to, subject, text)
	if err != nil {
		s.count(kind, err)
		return err
	}
	err = s.deliver(ctx, to, msg)
	s.count(kind, err)
	if err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	return nil
}

func (s *Sender) count(kind string, err error) {
	if s.metrics != nil {
		s.metrics.Mail.WithLabelValues(kind, metrics.Result(err)).Inc()
	}
}

func (s *Sender) deliver(ctx context.Context, to string, msg []byte) error {
	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprint(s.cfg.Port))
	d := &net.Dialer{Timeout: dialTimeout}
	var (
		conn net.Conn
		err  error
	)
	if s.cfg.TLS == config.SMTPImplicitTLS {
		td := &tls.Dialer{
			NetDialer: d,
			Config:    &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12},
		}
		conn, err = td.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = c.Close() }()
	if s.cfg.TLS == config.SMTPStartTLS {
		if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if s.cfg.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.User, s.cfg.Password, s.cfg.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(address(s.cfg.From)); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

var errHeaderInjection = errors.New("mail: header value contains a line break")

func build(from, to, subject, text string) ([]byte, error) {
	if strings.ContainsAny(to, "\r\n") || strings.ContainsAny(subject, "\r\n") || strings.ContainsAny(from, "\r\n") {
		return nil, errHeaderInjection
	}
	var b strings.Builder
	h := func(name, value string) {
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(value)
		b.WriteString("\r\n")
	}
	h("From", from)
	h("To", to)
	h("Subject", mime.QEncoding.Encode("utf-8", subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("Message-ID", "<"+crypto.RandomToken(16)+"@"+hostOf(from)+">")
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=utf-8")
	h("Content-Transfer-Encoding", "8bit")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n"))
	return []byte(b.String()), nil
}

func address(from string) string {
	if i := strings.LastIndex(from, "<"); i >= 0 {
		if j := strings.Index(from[i:], ">"); j >= 0 {
			return from[i+1 : i+j]
		}
	}
	return strings.TrimSpace(from)
}

func hostOf(from string) string {
	addr := address(from)
	if _, host, ok := strings.Cut(addr, "@"); ok && host != "" {
		return host
	}
	return "localhost"
}
