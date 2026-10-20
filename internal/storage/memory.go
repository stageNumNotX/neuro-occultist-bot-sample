package storage

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Memory is an in-process Store used for local runs and tests.
type Memory struct {
	mu         sync.Mutex
	chats      map[int64]Chat
	deliveries map[string]Delivery
	pending    *PendingDigest
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		chats:      make(map[int64]Chat),
		deliveries: make(map[string]Delivery),
	}
}

func (m *Memory) SaveChat(_ context.Context, chat Chat) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chats[chat.ID] = chat
	return nil
}

func (m *Memory) ListChats(_ context.Context) ([]Chat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Chat, 0, len(m.chats))
	for _, c := range m.chats {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Memory) MarkDelivered(_ context.Context, chatID int64, link string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := deliveryDoc(chatID, link)
	m.deliveries[id] = Delivery{
		ChatID:    chatID,
		Hash:      id,
		Link:      link,
		CreatedAt: time.Now(),
	}
	return nil
}

func (m *Memory) WasDelivered(_ context.Context, chatID int64, link string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.deliveries[deliveryDoc(chatID, link)]
	return ok, nil
}

func (m *Memory) GetPendingDigest(_ context.Context) (*PendingDigest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		return nil, nil
	}
	return clonePending(m.pending), nil
}

func (m *Memory) SetPendingDigest(_ context.Context, d PendingDigest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending = clonePending(&d)
	return nil
}

func (m *Memory) ClearPendingDigest(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending = nil
	return nil
}

func (m *Memory) Close() error { return nil }

func clonePending(d *PendingDigest) *PendingDigest {
	if d == nil {
		return nil
	}
	out := *d
	out.Links = append([]string(nil), d.Links...)
	out.Texts = append([]string(nil), d.Texts...)
	return &out
}
