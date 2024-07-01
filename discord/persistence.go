package discord

import "context"

type Session struct {
	ID             string `json:"session_id"`
	SequenceNumber int    `json:"sequence_number"`
}

type Persistence interface {
	LoadSession(ctx context.Context) (Session, bool, error)
	StoreSession(ctx context.Context, session Session) error
	DeleteSession(ctx context.Context) error

	LoadOutgoingMessages(ctx context.Context) ([]OutgoingMessage, error)
	StoreOutgoingMessages(ctx context.Context, msgs []OutgoingMessage) error
	DeleteOutgoingMessages(ctx context.Context, msgIDs []string) error
}

// A dummy implementation of Persistence. Does not persist anything. Do not use except for testing!
type DummyPersistence struct {
	session    Session
	hasSession bool

	outgoingMessages map[string]OutgoingMessage
}

var _ Persistence = &DummyPersistence{}

// LoadSession implements Persistence.
func (d *DummyPersistence) LoadSession(ctx context.Context) (Session, bool, error) {
	return d.session, d.hasSession, nil
}

// StoreSession implements Persistence.
func (d *DummyPersistence) StoreSession(ctx context.Context, session Session) error {
	d.session = session
	d.hasSession = true
	return nil
}

// DeleteSession implements Persistence.
func (d *DummyPersistence) DeleteSession(ctx context.Context) error {
	d.session = Session{}
	d.hasSession = false
	return nil
}

// LoadOutgoingMessages implements Persistence.
func (d *DummyPersistence) LoadOutgoingMessages(ctx context.Context) ([]OutgoingMessage, error) {
	var res []OutgoingMessage
	for _, msg := range d.outgoingMessages {
		res = append(res, msg)
	}
	return res, nil
}

// StoreOutgoingMessages implements Persistence.
func (d *DummyPersistence) StoreOutgoingMessages(ctx context.Context, msgs []OutgoingMessage) error {
	if d.outgoingMessages == nil {
		d.outgoingMessages = make(map[string]OutgoingMessage)
	}
	for _, msg := range msgs {
		d.outgoingMessages[msg.ID] = msg
	}
	return nil
}

// DeleteOutgoingMessages implements Persistence.
func (d *DummyPersistence) DeleteOutgoingMessages(ctx context.Context, msgIDs []string) error {
	for _, id := range msgIDs {
		delete(d.outgoingMessages, id)
	}
	return nil
}
