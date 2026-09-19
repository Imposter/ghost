package direct

import (
	"context"
	"fmt"
)

// Exchanger carries tokens between two peers over a channel of the caller's
// choosing: a chat, a file, a QR code, or an API of their own. Tokens hold
// only public material, but a party that can replace them can make a peer
// link to someone else, so use a channel whose integrity you trust.
type Exchanger interface {
	// Send delivers a token to the other peer.
	Send(ctx context.Context, token string) error
	// Receive waits for the next token from the other peer.
	Receive(ctx context.Context) (string, error)
}

// Invite runs the inviting side of an exchange over x: it sends an invite,
// waits for the answer and applies it.
func Invite(ctx context.Context, s *Signaller, x Exchanger) error {
	invite, err := s.CreateInvite(ctx)
	if err != nil {
		return err
	}
	if err := x.Send(ctx, invite); err != nil {
		return fmt.Errorf("direct: send invite: %w", err)
	}
	answer, err := x.Receive(ctx)
	if err != nil {
		return fmt.Errorf("direct: receive answer: %w", err)
	}
	return s.AcceptAnswer(ctx, answer)
}

// Answer runs the invited side of an exchange over x: it waits for an
// invite, applies it and sends the answer back.
func Answer(ctx context.Context, s *Signaller, x Exchanger) error {
	invite, err := x.Receive(ctx)
	if err != nil {
		return fmt.Errorf("direct: receive invite: %w", err)
	}
	answer, err := s.AcceptInvite(ctx, invite)
	if err != nil {
		return err
	}
	if err := x.Send(ctx, answer); err != nil {
		return fmt.Errorf("direct: send answer: %w", err)
	}
	return nil
}

// Pipe returns two connected in-memory Exchangers, for tests and for two
// members in one process.
func Pipe() (Exchanger, Exchanger) {
	ab, ba := make(chan string, 1), make(chan string, 1)
	return memExchanger{send: ab, recv: ba}, memExchanger{send: ba, recv: ab}
}

type memExchanger struct {
	send chan<- string
	recv <-chan string
}

func (m memExchanger) Send(ctx context.Context, token string) error {
	select {
	case m.send <- token:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m memExchanger) Receive(ctx context.Context) (string, error) {
	select {
	case t := <-m.recv:
		return t, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
