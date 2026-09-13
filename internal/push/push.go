package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const defaultTTL = 6 * time.Hour

var (
	ErrGone       = errors.New("push: the subscription no longer exists")
	ErrRetryLater = errors.New("push: the push service asked to retry later")
)

type Message struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	URL   string `json:"url,omitempty"`
	Tag   string `json:"tag,omitempty"`
}

type Sender struct {
	client  *http.Client
	clock   clock.Clock
	pub     []byte
	priv    *ecdsaKey
	subject string
}

func New(publicKey, privateKey, subject string, client *http.Client, clk clock.Clock) (*Sender, error) {
	pub, priv, err := parseKeys(publicKey, privateKey)
	if err != nil {
		return nil, err
	}
	if subject == "" {
		return nil, errors.New("push: WEBPUSH_VAPID_SUBJECT is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if clk == nil {
		clk = clock.New()
	}
	return &Sender{client: client, clock: clk, pub: pub, priv: priv, subject: subject}, nil
}

func (s *Sender) PublicKey() string {
	return b64.EncodeToString(s.pub)
}

func (s *Sender) Send(ctx context.Context, sub domain.PushSubscription, m Message) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	uaPublic, err := decodeKey(sub.P256dh)
	if err != nil {
		return err
	}
	authSecret, err := decodeKey(sub.Auth)
	if err != nil {
		return err
	}
	body, err := encrypt(payload, uaPublic, authSecret)
	if err != nil {
		return err
	}
	auth, err := s.authorization(sub.Endpoint, s.clock.Now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	h := req.Header
	h.Set("Authorization", auth)
	h.Set("Content-Encoding", "aes128gcm")
	h.Set("Content-Type", "application/octet-stream")
	h.Set("TTL", strconv.Itoa(int(defaultTTL/time.Second)))
	h.Set("Urgency", "normal")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
	}()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("%w: %s", ErrRetryLater, resp.Status)
	}
	return fmt.Errorf("push: the push service answered %s", resp.Status)
}
