// Package mail sends email. The service only ever sends through a Sender; the SMTP sender lives in the deployment
// repository, not here.
package mail

import (
	"context"
	"log/slog"
	"sync"
)

// Message is one email.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender sends email.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Outbox keeps every message instead of sending it: tests read it, and the demo server prints it. It is safe for
// concurrent use.
type Outbox struct {
	mu   sync.Mutex
	msgs []Message
	// Log, when set, also logs each message (the demo server).
	Log *slog.Logger
}

// Send keeps m.
func (o *Outbox) Send(ctx context.Context, m Message) error {
	o.mu.Lock()
	o.msgs = append(o.msgs, m)
	o.mu.Unlock()
	if o.Log != nil {
		o.Log.Info("email", "to", m.To, "subject", m.Subject, "body", m.Body)
	}
	return nil
}

// Messages returns the messages kept so far, oldest first.
func (o *Outbox) Messages() []Message {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]Message(nil), o.msgs...)
}
