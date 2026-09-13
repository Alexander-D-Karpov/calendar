package realtime

import (
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

// One tab per view is normal; past this an account is leaking sockets.
const maxSubsPerOwner = 8

const subBuffer = 64

type Sub struct {
	owner domain.ID
	C     chan domain.Change
	N     chan domain.Notice
	lost  atomic.Bool

	refused bool
}

// Refused reports that the owner already holds the most sockets allowed, so the
// caller should ask the client to come back rather than serve a dead stream.
func (s *Sub) Refused() bool {
	return s != nil && s.refused
}

func (s *Sub) Lost() bool {
	return s.lost.Swap(false)
}

type Hub struct {
	mu     sync.Mutex
	subs   map[domain.ID]map[*Sub]struct{}
	closed bool
	gauge  *prometheus.GaugeVec
}

func NewHub(gauge *prometheus.GaugeVec) *Hub {
	return &Hub{subs: map[domain.ID]map[*Sub]struct{}{}, gauge: gauge}
}

func (h *Hub) Subscribe(owner domain.ID) *Sub {
	s := &Sub{owner: owner, C: make(chan domain.Change, subBuffer), N: make(chan domain.Notice, subBuffer)}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(s.C)
		close(s.N)
		return s
	}
	set := h.subs[owner]
	if len(set) >= maxSubsPerOwner {
		s.refused = true
		close(s.C)
		close(s.N)
		return s
	}
	if set == nil {
		set = map[*Sub]struct{}{}
		h.subs[owner] = set
	}
	set[s] = struct{}{}
	return s
}

func (h *Hub) Notify(n domain.Notice) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs[n.OwnerID] {
		select {
		case s.N <- n:
		default:
		}
	}
}

func (h *Hub) Unsubscribe(s *Sub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.subs[s.owner]
	if _, ok := set[s]; !ok {
		return
	}
	delete(set, s)
	if len(set) == 0 {
		delete(h.subs, s.owner)
	}
}

func (h *Hub) Publish(c domain.Change) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs[c.OwnerID] {
		deliver(s, c)
	}
}

func (h *Hub) Resync() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, set := range h.subs {
		for s := range set {
			s.lost.Store(true)
			deliver(s, domain.Change{})
		}
	}
}

func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for _, set := range h.subs {
		for s := range set {
			close(s.C)
			close(s.N)
		}
	}
	h.subs = map[domain.ID]map[*Sub]struct{}{}
}

func (h *Hub) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, set := range h.subs {
		n += len(set)
	}
	return n
}

func deliver(s *Sub, c domain.Change) {
	select {
	case s.C <- c:
	default:
		s.lost.Store(true)
	}
}
