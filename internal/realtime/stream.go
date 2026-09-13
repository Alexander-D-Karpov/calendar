package realtime

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const (
	pingEvery    = 30 * time.Second
	writeTimeout = 10 * time.Second
	replayLimit  = 1000
)

type Replayer func(ctx context.Context, owner domain.ID, since int64, limit int) ([]domain.Change, error)

type Stream struct {
	Kind   string
	Owner  domain.ID
	Since  int64
	Filter Filter
	Replay Replayer
}

type message struct {
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
	URL   string `json:"url,omitempty"`
	Tag   string `json:"tag,omitempty"`
}

func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, s Stream) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer func() { _ = c.CloseNow() }()
	c.SetReadLimit(1024)

	sub := h.Subscribe(s.Owner)
	defer h.Unsubscribe(sub)
	if sub.Refused() {
		_ = c.Close(websocket.StatusTryAgainLater, "too many open connections")
		return
	}
	if h.gauge != nil {
		g := h.gauge.WithLabelValues(s.Kind)
		g.Inc()
		defer g.Dec()
	}
	ctx := c.CloseRead(r.Context())

	act := Skip
	if s.Since > 0 && s.Replay != nil {
		pending, err := s.Replay(ctx, s.Owner, s.Since, replayLimit)
		if err != nil {
			_ = c.Close(websocket.StatusInternalError, "replay failed")
			return
		}
		for _, ch := range pending {
			act = max(act, s.Filter(ch))
		}
		if len(pending) >= replayLimit {
			act = max(act, Refresh)
		}
	}
	if act != Skip && !send(ctx, c, act) {
		return
	}

	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ch, ok := <-sub.C:
			if !ok {
				_ = c.Close(websocket.StatusGoingAway, "server restarting")
				return
			}
			act := Skip
			if sub.Lost() {
				act = Refresh
			}
			if ch.Seq != 0 {
				act = max(act, s.Filter(ch))
			}
			if act != Skip && !send(ctx, c, act) {
				return
			}
		case n, ok := <-sub.N:
			if !ok {
				return
			}
			if s.Kind != "user" {
				continue
			}
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := wsjson.Write(wctx, c, message{Type: "notice", Title: n.Title, Body: n.Body, URL: n.URL, Tag: n.Tag})
			cancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func send(ctx context.Context, c *websocket.Conn, a Action) bool {
	t := "refresh"
	if a == Reload {
		t = "reload"
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return wsjson.Write(wctx, c, message{Type: t}) == nil
}
