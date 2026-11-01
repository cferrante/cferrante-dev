// Package fake is a scripted model for tests.
package fake

import (
	"context"
	"sync"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

// Model replays a script. Safe for concurrent use.
type Model struct {
	Name      string
	Events    []model.Event // sent in order
	ErrBefore error         // returned before any event
	ErrAfter  error         // returned after all events
	// Block waits for the context to end before doing anything else. With
	// SendAfterCancel it then still tries to send Events[0], like a provider
	// whose first token was already in flight when it was cancelled.
	Block           bool
	SendAfterCancel bool
	Result          model.Result

	mu       sync.Mutex
	requests []model.Request
}

// Stream plays the script.
func (m *Model) Stream(ctx context.Context, req model.Request, onEvent func(model.Event) error) (model.Result, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	if m.Block {
		<-ctx.Done()
		if m.SendAfterCancel && len(m.Events) > 0 {
			if err := onEvent(m.Events[0]); err != nil {
				return model.Result{}, err
			}
		}
		return model.Result{}, ctx.Err()
	}
	if m.ErrBefore != nil {
		return model.Result{}, m.ErrBefore
	}
	for _, e := range m.Events {
		if err := ctx.Err(); err != nil {
			return model.Result{}, err
		}
		if err := onEvent(e); err != nil {
			return model.Result{}, err
		}
	}
	if m.ErrAfter != nil {
		return model.Result{}, m.ErrAfter
	}
	res := m.Result
	if res.Provider == "" {
		res.Provider = m.Name
	}
	return res, nil
}

// Requests returns every request received, in order.
func (m *Model) Requests() []model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]model.Request(nil), m.requests...)
}
