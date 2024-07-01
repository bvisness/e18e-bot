package discord

import (
	"context"
	"fmt"
	"time"
)

var outgoingMessagesReady = make(chan struct{}, 1)

type OutgoingMessage struct {
	ChannelID string               `json:"channel_id"`
	Req       CreateMessageRequest `json:"req"`

	// Optional. If omitted, the message will expire 30 seconds after it
	// is sent.
	ExpiresAt time.Time `json:"expires_at"`

	// Arbitrary unique ID generated by this library. You may serialize and
	// deserialize this, but any ID you provide will be ignored when sending.
	ID string
}

func SendMessages(
	ctx context.Context,
	persistence Persistence,
	msgs ...OutgoingMessage,
) error {
	for i := range msgs {
		msg := &msgs[i]
		msg.ID = fmt.Sprintf("%v", time.Now().UnixNano())
		if msg.ExpiresAt.IsZero() {
			msg.ExpiresAt = time.Now().Add(30 * time.Second)
		}
	}

	err := persistence.StoreOutgoingMessages(ctx, msgs)
	if err != nil {
		return fmt.Errorf("failed to commit outgoing Discord messages: %w", err)
	}

	// Notify senders that messages are ready to go
	select {
	case outgoingMessagesReady <- struct{}{}:
	default:
	}

	return nil
}
