package realtime

import (
	"errors"
	"testing"
	"time"
)

func TestTicketsAreSingleUseAndExpire(t *testing.T) {
	tickets := NewTickets[string](time.Minute)
	now := time.Now()
	tickets.now = func() time.Time { return now }

	ticket, err := tickets.Issue("alice")
	if err != nil || len(ticket) < 40 {
		t.Fatalf("ticket = %q, err %v", ticket, err)
	}
	if got, ok := tickets.Redeem(ticket); !ok || got != "alice" {
		t.Fatalf("redeem = %q, %v", got, ok)
	}
	if _, ok := tickets.Redeem(ticket); ok {
		t.Fatal("ticket redeemed twice")
	}

	expired, _ := tickets.Issue("bob")
	now = now.Add(time.Minute + time.Second)
	if _, ok := tickets.Redeem(expired); ok {
		t.Fatal("expired ticket redeemed")
	}
	if _, ok := tickets.Redeem("unknown"); ok {
		t.Fatal("unknown ticket redeemed")
	}
	tickets.Issue("carol")
	if len(tickets.items) != 1 {
		t.Fatalf("expired tickets kept: %d", len(tickets.items))
	}
}

func TestHubFansOutToEverySessionOfAUser(t *testing.T) {
	hub := NewHub(2)
	first, isFirst, _ := hub.Join("alice")
	second, isFirstAgain, _ := hub.Join("alice")
	bob, _, _ := hub.Join("bob")
	if !isFirst || isFirstAgain || !hub.Online("alice") || hub.Online("carol") {
		t.Fatalf("first = %v, again = %v", isFirst, isFirstAgain)
	}
	if _, _, err := hub.Join("alice"); !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("third session err = %v", err)
	}

	hub.Send("alice", []byte("hi"))
	hub.SendTo(bob, []byte("only bob"))
	for _, s := range []*Session{first, second} {
		if got := string(<-s.Outbox()); got != "hi" {
			t.Fatalf("alice got %q", got)
		}
	}
	if got := string(<-bob.Outbox()); got != "only bob" {
		t.Fatalf("bob got %q", got)
	}

	if hub.Leave(first) || hub.Leave(first) || !hub.Online("alice") {
		t.Fatal("alice went offline with a session left")
	}
	if !hub.Leave(second) || hub.Online("alice") {
		t.Fatal("alice still online after her last session left")
	}
	if _, ok := <-second.Outbox(); ok {
		t.Fatal("outbox open after leave")
	}
}

func TestHubDropsASessionThatFallsBehind(t *testing.T) {
	hub := NewHub(1)
	slow, _, _ := hub.Join("alice")
	for range outboxSize + 1 {
		hub.Send("alice", []byte("x"))
	}
	if hub.Online("alice") {
		t.Fatal("slow session still registered")
	}
	for range outboxSize {
		<-slow.Outbox()
	}
	if _, ok := <-slow.Outbox(); ok {
		t.Fatal("slow outbox not closed")
	}
	if !hub.Leave(slow) || hub.Leave(slow) {
		t.Fatal("leave after drop must report the user offline exactly once")
	}
}
