package bot

import (
	"context"
	"sync"

	"golang.org/x/time/rate"
)

// TelegramRateLimiter applies Telegram Bot API send limits:
// ~30 messages/sec globally and 1 message/sec per chat.
type TelegramRateLimiter struct {
	global       *rate.Limiter
	mu           sync.Mutex
	chatLimiters map[int64]*rate.Limiter
}

// NewTelegramRateLimiter constructs the default limiter.
func NewTelegramRateLimiter() *TelegramRateLimiter {
	return &TelegramRateLimiter{
		global:       rate.NewLimiter(30, 60),
		chatLimiters: make(map[int64]*rate.Limiter),
	}
}

// Wait blocks until sending to chatID is allowed.
func (rl *TelegramRateLimiter) Wait(ctx context.Context, chatID int64) error {
	if err := rl.global.Wait(ctx); err != nil {
		return err
	}
	return rl.chatLimiter(chatID).Wait(ctx)
}

// Allow reports whether a send can proceed without waiting.
func (rl *TelegramRateLimiter) Allow(chatID int64) bool {
	if !rl.global.Allow() {
		return false
	}
	return rl.chatLimiter(chatID).Allow()
}

func (rl *TelegramRateLimiter) chatLimiter(chatID int64) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if lim, ok := rl.chatLimiters[chatID]; ok {
		return lim
	}
	lim := rate.NewLimiter(1, 3)
	rl.chatLimiters[chatID] = lim
	return lim
}

// Stats is a snapshot for the optional admin command.
func (rl *TelegramRateLimiter) Stats() (globalLimit float64, burst, tracked int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return float64(rl.global.Limit()), rl.global.Burst(), len(rl.chatLimiters)
}
