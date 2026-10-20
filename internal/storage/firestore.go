package storage

import (
	"context"
	"fmt"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Firestore is a Store backed by Cloud Firestore.
type Firestore struct {
	client *firestore.Client
}

// NewFirestore opens a Firestore client. database may be "(default)".
func NewFirestore(ctx context.Context, projectID, database string) (*Firestore, error) {
	if projectID == "" {
		return nil, fmt.Errorf("firestore project id is required")
	}
	var (
		c   *firestore.Client
		err error
	)
	if database == "" || database == "(default)" {
		c, err = firestore.NewClient(ctx, projectID)
	} else {
		c, err = firestore.NewClientWithDatabase(ctx, projectID, database)
	}
	if err != nil {
		return nil, err
	}
	return &Firestore{client: c}, nil
}

func (s *Firestore) SaveChat(ctx context.Context, chat Chat) error {
	_, err := s.client.Collection(collectionChats).Doc(chatIDDoc(chat.ID)).Set(ctx, chat)
	return err
}

func (s *Firestore) ListChats(ctx context.Context) ([]Chat, error) {
	snaps, err := s.client.Collection(collectionChats).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	res := make([]Chat, 0, len(snaps))
	for _, snap := range snaps {
		var c Chat
		if err := snap.DataTo(&c); err == nil {
			res = append(res, c)
		}
	}
	sort.Slice(res, func(i, j int) bool { return res[i].ID < res[j].ID })
	return res, nil
}

func (s *Firestore) MarkDelivered(ctx context.Context, chatID int64, link string) error {
	id := deliveryDoc(chatID, link)
	_, err := s.client.Collection(collectionDeliveries).Doc(id).Set(ctx, Delivery{
		ChatID:    chatID,
		Hash:      id,
		Link:      link,
		CreatedAt: time.Now(),
	})
	return err
}

func (s *Firestore) WasDelivered(ctx context.Context, chatID int64, link string) (bool, error) {
	doc, err := s.client.Collection(collectionDeliveries).Doc(deliveryDoc(chatID, link)).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return false, nil
		}
		return false, err
	}
	return doc.Exists(), nil
}

func (s *Firestore) pendingRef() *firestore.DocumentRef {
	return s.client.Collection(collectionState).Doc(pendingDigestDoc)
}

func (s *Firestore) GetPendingDigest(ctx context.Context) (*PendingDigest, error) {
	doc, err := s.pendingRef().Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, err
	}
	var d PendingDigest
	if err := doc.DataTo(&d); err != nil {
		return nil, err
	}
	if d.Post == "" && len(d.Links) == 0 {
		return nil, nil
	}
	return &d, nil
}

func (s *Firestore) SetPendingDigest(ctx context.Context, d PendingDigest) error {
	_, err := s.pendingRef().Set(ctx, d)
	return err
}

func (s *Firestore) ClearPendingDigest(ctx context.Context) error {
	_, err := s.pendingRef().Delete(ctx)
	if err != nil && status.Code(err) != codes.NotFound {
		return err
	}
	return nil
}

func (s *Firestore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}
