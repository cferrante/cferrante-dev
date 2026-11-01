package agent

import (
	"context"
	"sync"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

// HistoryMessages is how many earlier messages go into the prompt.
const HistoryMessages = 10

// ConversationStore keeps conversation history on the server; the browser
// never supplies it (design §8). Each conversation belongs to the caller who
// started it.
type ConversationStore interface {
	// Load returns the last HistoryMessages messages, and false if the
	// conversation doesn't exist or belongs to someone else.
	Load(ctx context.Context, id, owner string) ([]model.Message, bool, error)
	// Append adds messages, creating the conversation for owner if needed.
	Append(ctx context.Context, id, owner string, msgs ...model.Message) error
}

// MemoryStore is an in-memory ConversationStore for tests and `make dev`.
type MemoryStore struct {
	mu    sync.Mutex
	convs map[string]*memConv
}

type memConv struct {
	owner string
	msgs  []model.Message
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{convs: map[string]*memConv{}} }

func (s *MemoryStore) Load(_ context.Context, id, owner string) ([]model.Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok || c.owner != owner {
		return nil, false, nil
	}
	start := max(0, len(c.msgs)-HistoryMessages)
	return append([]model.Message(nil), c.msgs[start:]...), true, nil
}

func (s *MemoryStore) Append(_ context.Context, id, owner string, msgs ...model.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok {
		c = &memConv{owner: owner}
		s.convs[id] = c
	}
	c.msgs = append(c.msgs, msgs...)
	return nil
}
