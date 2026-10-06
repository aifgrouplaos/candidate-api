// Package realtime routes live events to users' connected sessions and issues the
// one-use tickets that open those sessions.
//
// ponytail: sessions and tickets live in this process, so it serves one API instance.
// Running replicas needs a shared ticket store and Redis pub/sub fan-out in Hub.Send.
package realtime

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// outboxSize bounds the events queued for one session; a session that falls this far
// behind is dropped and must reconnect and resync.
const outboxSize = 64

var ErrTooManySessions = errors.New("too many live sessions")

// Session is one live connection of a user. Its outbox closes when the session leaves
// or is dropped for falling behind.
type Session struct {
	userID string
	out    chan []byte
	closed bool
	left   bool
}

func (s *Session) Outbox() <-chan []byte { return s.out }

// Hub tracks each user's live sessions.
type Hub struct {
	mu         sync.Mutex
	maxPerUser int
	sessions   map[string]map[*Session]struct{}
}

func NewHub(maxPerUser int) *Hub {
	return &Hub{maxPerUser: maxPerUser, sessions: map[string]map[*Session]struct{}{}}
}

// Join registers a session for userID; first reports that the user just came online.
func (h *Hub) Join(userID string) (s *Session, first bool, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.sessions[userID]) >= h.maxPerUser {
		return nil, false, ErrTooManySessions
	}
	if h.sessions[userID] == nil {
		h.sessions[userID] = map[*Session]struct{}{}
	}
	s = &Session{userID: userID, out: make(chan []byte, outboxSize)}
	h.sessions[userID][s] = struct{}{}
	return s, len(h.sessions[userID]) == 1, nil
}

// Leave unregisters s; last reports, once, that its user has no session left.
func (h *Hub) Leave(s *Session) (last bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s.left {
		return false
	}
	s.left = true
	h.drop(s)
	return len(h.sessions[s.userID]) == 0
}

func (h *Hub) Online(userID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sessions[userID]) > 0
}

// Send queues event for every live session of userID.
func (h *Hub) Send(userID string, event []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.sessions[userID] {
		h.enqueue(s, event)
	}
}

// SendTo queues event for s alone.
func (h *Hub) SendTo(s *Session, event []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.enqueue(s, event)
}

func (h *Hub) enqueue(s *Session, event []byte) {
	if s.closed {
		return
	}
	select {
	case s.out <- event:
	default:
		h.drop(s)
	}
}

func (h *Hub) drop(s *Session) {
	delete(h.sessions[s.userID], s)
	if len(h.sessions[s.userID]) == 0 {
		delete(h.sessions, s.userID)
	}
	if !s.closed {
		s.closed = true
		close(s.out)
	}
}

// Tickets issues random one-use tickets that each carry a value until they expire.
type Tickets[T any] struct {
	mu    sync.Mutex
	ttl   time.Duration
	now   func() time.Time
	items map[string]ticket[T]
}

type ticket[T any] struct {
	value     T
	expiresAt time.Time
}

func NewTickets[T any](ttl time.Duration) *Tickets[T] {
	return &Tickets[T]{ttl: ttl, now: time.Now, items: map[string]ticket[T]{}}
}

func (t *Tickets[T]) Issue(value T) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(raw)
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	// ponytail: O(n) sweep of expired tickets per issue; issuing is rate-limited per user.
	for key, item := range t.items {
		if !now.Before(item.expiresAt) {
			delete(t.items, key)
		}
	}
	t.items[id] = ticket[T]{value: value, expiresAt: now.Add(t.ttl)}
	return id, nil
}

// Redeem consumes ticket and returns its value unless it is unknown, used, or expired.
func (t *Tickets[T]) Redeem(id string) (T, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	item, ok := t.items[id]
	delete(t.items, id)
	if !ok || !t.now().Before(item.expiresAt) {
		var zero T
		return zero, false
	}
	return item.value, true
}
