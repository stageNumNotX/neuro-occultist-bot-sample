package storage

import (
	"context"
	"testing"
	"time"
)

func TestMemory_chatsAndDeliveries(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()

	if err := m.SaveChat(ctx, Chat{ID: 2, Title: "B", Type: "channel", AddedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveChat(ctx, Chat{ID: 1, Title: "A", Type: "channel", AddedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	chats, err := m.ListChats(ctx)
	if err != nil || len(chats) != 2 || chats[0].ID != 1 || chats[1].ID != 2 {
		t.Fatalf("chats=%v err=%v", chats, err)
	}

	link := "https://example.com/1"
	was, err := m.WasDelivered(ctx, 1, link)
	if err != nil || was {
		t.Fatalf("was=%v err=%v", was, err)
	}
	if err := m.MarkDelivered(ctx, 1, link); err != nil {
		t.Fatal(err)
	}
	was, err = m.WasDelivered(ctx, 1, link)
	if err != nil || !was {
		t.Fatalf("expected delivered to chat 1, was=%v err=%v", was, err)
	}
	was, err = m.WasDelivered(ctx, 2, link)
	if err != nil || was {
		t.Fatalf("chat 2 should be independent, was=%v err=%v", was, err)
	}
}
