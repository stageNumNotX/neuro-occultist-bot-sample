package bot

import (
	"context"
	"testing"
	"time"
)

func TestTelegramRateLimiter_Basic(t *testing.T) {
	rl := NewTelegramRateLimiter()
	chatID := int64(12345)

	for i := 0; i < 3; i++ {
		if !rl.Allow(chatID) {
			t.Errorf("request %d should be allowed due to burst", i+1)
		}
	}

	limit, burst, tracked := rl.Stats()
	if limit != 30 {
		t.Errorf("global limit=%v", limit)
	}
	if burst != 60 {
		t.Errorf("burst=%v", burst)
	}
	if tracked != 1 {
		t.Errorf("tracked=%v", tracked)
	}
}

func TestTelegramRateLimiter_Wait(t *testing.T) {
	rl := NewTelegramRateLimiter()
	ctx := context.Background()

	start := time.Now()
	if err := rl.Wait(ctx, 67890); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatalf("first wait should be immediate")
	}
}

func TestTelegramRateLimiter_ContextCancellation(t *testing.T) {
	rl := NewTelegramRateLimiter()
	chatID := int64(11111)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	for i := 0; i < 10; i++ {
		rl.Allow(chatID)
	}

	if err := rl.Wait(ctx, chatID); err == nil {
		t.Fatal("expected context timeout")
	}
}

func TestTelegramRateLimiter_MultipleChatIsolation(t *testing.T) {
	rl := NewTelegramRateLimiter()

	for i := 0; i < 5; i++ {
		rl.Allow(1111)
	}
	if !rl.Allow(2222) {
		t.Fatal("second chat should have an independent limit")
	}
	_, _, tracked := rl.Stats()
	if tracked != 2 {
		t.Fatalf("tracked=%d", tracked)
	}
}
