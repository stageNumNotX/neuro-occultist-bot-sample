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

	if err := m.RemoveChat(ctx, 2); err != nil {
		t.Fatal(err)
	}
	chats, err = m.ListChats(ctx)
	if err != nil || len(chats) != 1 || chats[0].ID != 1 {
		t.Fatalf("after remove chats=%v err=%v", chats, err)
	}
	was, err = m.WasDelivered(ctx, 1, link)
	if err != nil || !was {
		t.Fatalf("remove must not erase delivery records, was=%v err=%v", was, err)
	}
}

func TestMemory_pendingDigest(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()

	got, err := m.GetPendingDigest(ctx)
	if err != nil || got != nil {
		t.Fatalf("empty pending got=%v err=%v", got, err)
	}

	in := PendingDigest{Post: "p", Links: []string{"https://example.com/x"}, Texts: []string{"X — https://example.com/x"}}
	if err := m.SetPendingDigest(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err = m.GetPendingDigest(ctx)
	if err != nil || got == nil || got.Post != "p" || len(got.Links) != 1 {
		t.Fatalf("got=%v err=%v", got, err)
	}
	got.Links[0] = "mutated"
	got2, err := m.GetPendingDigest(ctx)
	if err != nil || got2.Links[0] != "https://example.com/x" {
		t.Fatalf("pending slice must be copied, got=%v err=%v", got2, err)
	}

	if err := m.ClearPendingDigest(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = m.GetPendingDigest(ctx)
	if err != nil || got != nil {
		t.Fatalf("cleared pending got=%v err=%v", got, err)
	}
}
