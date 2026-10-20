package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	collectionChats      = "chats"
	collectionDeliveries = "deliveries"
)

// Chat is a Telegram chat the bot was added to.
type Chat struct {
	ID      int64     `json:"id" firestore:"id"`
	Type    string    `json:"type" firestore:"type"`
	Title   string    `json:"title" firestore:"title"`
	AddedAt time.Time `json:"added_at" firestore:"added_at"`
}

// Delivery records that one chat received a specific RSS link.
type Delivery struct {
	ChatID    int64     `json:"chat_id" firestore:"chat_id"`
	Hash      string    `json:"hash" firestore:"hash"`
	Link      string    `json:"link" firestore:"link"`
	CreatedAt time.Time `json:"created_at" firestore:"created_at"`
}

// Store persists chats and per-chat delivery of RSS links.
type Store interface {
	SaveChat(ctx context.Context, chat Chat) error
	ListChats(ctx context.Context) ([]Chat, error)
	MarkDelivered(ctx context.Context, chatID int64, link string) error
	WasDelivered(ctx context.Context, chatID int64, link string) (bool, error)
	Close() error
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func chatIDDoc(id int64) string {
	return hash(fmt.Sprintf("%d", id))
}

func deliveryDoc(chatID int64, link string) string {
	return hash(fmt.Sprintf("%d:%s", chatID, link))
}
