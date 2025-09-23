package inmemory

import (
	"context"
	"sync"

	"github.com/example/accounting/internal/app"
)

// MessageStore is an in-memory implementation of app.MessageStore.
type MessageStore struct {
	mu      sync.RWMutex
	records map[string]app.MessageRecord
}

// NewMessageStore creates the store.
func NewMessageStore() *MessageStore {
	return &MessageStore{records: make(map[string]app.MessageRecord)}
}

func (s *MessageStore) Upsert(_ context.Context, record app.MessageRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.ID] = record
	return nil
}

func (s *MessageStore) FindByID(_ context.Context, id string) (app.MessageRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[id]
	return record, ok, nil
}

var _ app.MessageStore = (*MessageStore)(nil)
