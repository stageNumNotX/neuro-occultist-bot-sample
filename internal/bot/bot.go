package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	tb "gopkg.in/telebot.v3"

	"neuro-occultist-bot/internal/image"
	"neuro-occultist-bot/internal/service"
	"neuro-occultist-bot/internal/storage"
	"neuro-occultist-bot/internal/textutil"
)

const (
	userPromptMaxRunes = 400
	captionMaxRunes    = 1000
	genericError       = "Something went wrong. Please try again in a moment."
)

var (
	// ErrNoChats means there are no saved chats to deliver to.
	ErrNoChats = errors.New("no chats to broadcast to")
	// ErrAllFailed means every attempted send was rejected by Telegram.
	ErrAllFailed = errors.New("broadcast failed for all chats")
	// ErrPartial means Telegram accepted the post for some chats and rejected it for others.
	ErrPartial = errors.New("broadcast delivered to some chats only")
	// ErrStateSave means Telegram accepted one or more messages but delivery
	// state was not fully persisted. The next run may send those chats again.
	ErrStateSave = errors.New("broadcast sent but delivery state was not fully saved")
	// ErrRecipientDrop means a chat is permanently unavailable but could not
	// be removed from the active recipient list.
	ErrRecipientDrop = errors.New("broadcast could not remove an unavailable recipient")
)

// Options wires Telegram, storage, the RSS/LLM pipeline, and optional images.
type Options struct {
	Token       string
	Polling     bool
	Offline     bool
	Synchronous bool
	Store       storage.Store
	Agent       *service.Agent
	Images      image.Generator
	AdminIDs    map[int64]struct{}
	HTTP        *http.Client
}

// Bot is the Telegram-facing adapter.
type Bot struct {
	api     *tb.Bot
	store   storage.Store
	agent   *service.Agent
	images  image.Generator
	admins  map[int64]struct{}
	limiter *TelegramRateLimiter
	polling bool
}

// New constructs a telebot instance and registers commands.
func New(opts Options) (*Bot, error) {
	pref := tb.Settings{
		Token:       opts.Token,
		Synchronous: opts.Synchronous,
		Offline:     opts.Offline,
		Client:      opts.HTTP,
	}
	if pref.Client == nil {
		pref.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if opts.Polling {
		pref.Poller = &tb.LongPoller{Timeout: 10 * time.Second}
	}

	api, err := tb.NewBot(pref)
	if err != nil {
		return nil, err
	}

	b := &Bot{
		api:     api,
		store:   opts.Store,
		agent:   opts.Agent,
		images:  opts.Images,
		admins:  opts.AdminIDs,
		limiter: NewTelegramRateLimiter(),
		polling: opts.Polling,
	}
	b.register()
	return b, nil
}

func (b *Bot) register() {
	b.api.Handle("/start", b.cmdStart)
	b.api.Handle("/ping", func(c tb.Context) error {
		return c.Send("Bot is up.")
	})
	b.api.Handle("/rss_summary", b.cmdRSSSummary)
	b.api.Handle("/vision", b.cmdVision)
	b.api.Handle("/news_vision", b.cmdNewsVision)
	b.api.Handle("/status", b.cmdStatus)
	b.api.Handle("/chats", b.cmdChats)
	b.api.Handle(tb.OnMyChatMember, b.onMyChatMember)
}

func (b *Bot) cmdStart(c tb.Context) error {
	return c.Send("I am Neuro-Occultist. Add me as an admin to a channel and I will bring news from the digital aether.\n\n" +
		"/ping — health check\n" +
		"/rss_summary — mystical digest of current RSS items (does not affect channel broadcast)\n" +
		"/vision <description> — generate an illustration\n" +
		"/news_vision — illustrate current headlines\n" +
		"/status — connectivity (admins)")
}

func (b *Bot) cmdRSSSummary(c tb.Context) error {
	if b.agent == nil {
		return c.Send("The writing pipeline is not configured.")
	}
	if err := c.Send("Gathering headlines…"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	digest, err := b.agent.BuildLatestDigest(ctx, 6)
	if err != nil {
		log.Printf("rss_summary: %v", err)
		return c.Send(genericError)
	}
	if len(digest.Texts) == 0 {
		return c.Send("No headlines right now.")
	}
	return c.Send(digest.Post)
}

func (b *Bot) cmdVision(c tb.Context) error {
	if b.images == nil {
		return c.Send("Image generation is not configured.")
	}
	var raw string
	if msg := c.Message(); msg != nil {
		raw = msg.Payload
	}
	if raw == "" {
		raw = strings.Join(c.Args(), " ")
	}
	prompt := textutil.SanitizeUntrusted(raw, userPromptMaxRunes)
	if prompt == "" {
		return c.Send("Describe the vision after the command.\nExample: /vision ancient symbol of force")
	}
	if err := c.Send("Rendering…"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	img, err := b.images.Generate(ctx, prompt)
	if err != nil {
		log.Printf("vision: %v", err)
		return c.Send(genericError)
	}
	photo := &tb.Photo{File: tb.FromReader(bytes.NewReader(img))}
	photo.Caption = textutil.Truncate("Vision: "+prompt, captionMaxRunes)
	return c.Send(photo)
}

func (b *Bot) cmdNewsVision(c tb.Context) error {
	if b.images == nil || b.agent == nil {
		return c.Send("Image generation is not configured.")
	}
	if err := c.Send("Scanning feeds…"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	texts, _, err := b.agent.FetchLatest(ctx, 5)
	if err != nil {
		log.Printf("news_vision fetch: %v", err)
		return c.Send(genericError)
	}
	if len(texts) == 0 {
		return c.Send("No headlines to illustrate.")
	}
	img, err := b.images.Generate(ctx, image.BuildNewsPrompt(service.ExtractTopics(texts, 3)))
	if err != nil {
		log.Printf("news_vision image: %v", err)
		return c.Send(genericError)
	}

	caption := "Current headlines:\n"
	for _, t := range service.ExtractTopics(texts, 5) {
		caption += "• " + t + "\n"
	}
	photo := &tb.Photo{File: tb.FromReader(bytes.NewReader(img))}
	photo.Caption = textutil.Truncate(caption, captionMaxRunes)
	return c.Send(photo)
}

func (b *Bot) cmdStatus(c tb.Context) error {
	if !b.isAdmin(c) {
		return c.Send("This command is limited to admins.")
	}
	store := "memory/local"
	if _, ok := b.store.(*storage.Firestore); ok {
		store = "firestore"
	}
	images := "disabled"
	if b.images != nil {
		images = "enabled"
	}
	return c.Send(fmt.Sprintf("store=%s images=%s", store, images))
}

func (b *Bot) cmdChats(c tb.Context) error {
	if !b.isAdmin(c) {
		return c.Send("This command is limited to admins.")
	}
	if b.store == nil {
		return c.Send("No store configured.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	chats, err := b.store.ListChats(ctx)
	if err != nil {
		log.Printf("list chats: %v", err)
		return c.Send(genericError)
	}
	if len(chats) == 0 {
		return c.Send("No saved chats.")
	}
	msg := fmt.Sprintf("Saved chats: %d\n", len(chats))
	for i, chat := range chats {
		if i >= 10 {
			msg += "…\n"
			break
		}
		msg += fmt.Sprintf("• %s (%s)\n", chat.Title, chat.Type)
	}
	return c.Send(msg)
}

func (b *Bot) onMyChatMember(c tb.Context) error {
	if b.store == nil {
		return nil
	}
	upd := c.Update()
	if upd.MyChatMember == nil || upd.MyChatMember.Chat == nil || upd.MyChatMember.NewChatMember == nil {
		return nil
	}
	chat := upd.MyChatMember.Chat
	role := upd.MyChatMember.NewChatMember.Role
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch role {
	case tb.Left, tb.Kicked:
		if err := b.store.RemoveChat(ctx, chat.ID); err != nil {
			log.Printf("remove chat %d: %v", chat.ID, err)
		}
		return nil
	case tb.Member, tb.Administrator, tb.Creator:
		if err := b.store.SaveChat(ctx, storage.Chat{
			ID:      chat.ID,
			Title:   chat.Title,
			Type:    string(chat.Type),
			AddedAt: time.Now(),
		}); err != nil {
			log.Printf("save chat: %v", err)
			return nil
		}
		return c.Send(fmt.Sprintf("Saved chat %q. Ready.", chat.Title))
	default:
		return nil
	}
}

func (b *Bot) isAdmin(c tb.Context) bool {
	if len(b.admins) == 0 {
		return false
	}
	u := c.Sender()
	if u == nil {
		return false
	}
	_, ok := b.admins[u.ID]
	return ok
}

// Start begins long-polling. Blocks until Stop.
func (b *Bot) Start() {
	if b != nil && b.api != nil {
		b.api.Start()
	}
}

// Stop ends polling. It is a no-op when the bot was constructed without a poller
// (webhook mode). Calling telebot Stop without Start blocks forever.
func (b *Bot) Stop() {
	if b == nil || b.api == nil || !b.polling {
		return
	}
	b.api.Stop()
}

// ProcessUpdate delivers a Telegram update to registered handlers.
func (b *Bot) ProcessUpdate(u tb.Update) {
	if b != nil && b.api != nil {
		b.api.ProcessUpdate(u)
	}
}

// HandleWebhook decodes a Telegram update and hands it to telebot.
func (b *Bot) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer func() { _ = r.Body.Close() }()

	var update tb.Update
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	b.api.ProcessUpdate(update)
	w.WriteHeader(http.StatusOK)
}

// Broadcast sends one post (and optional image) to active chats that have not
// yet received every link in this digest. Successful chats are recorded so a
// retry skips them. Temporary send failures leave the chat unmarked.
// Permanent unavailability (kicked/blocked/not a member) removes the chat
// without writing a fake delivery record.
func (b *Bot) Broadcast(ctx context.Context, post string, img []byte, links []string) error {
	if b.store == nil {
		return fmt.Errorf("store is not configured")
	}
	chats, err := b.store.ListChats(ctx)
	if err != nil {
		return err
	}
	if len(chats) == 0 {
		return ErrNoChats
	}

	var sent, saved, unsaved, failed, skipped, dropFailed int
	var firstErr error
	for _, ch := range chats {
		if err := ctx.Err(); err != nil {
			return err
		}
		if b.chatHasAll(ctx, ch.ID, links) {
			skipped++
			continue
		}
		if err := b.limiter.Wait(ctx, ch.ID); err != nil {
			return err
		}
		chat := &tb.Chat{ID: ch.ID}
		if err := b.sendOne(chat, post, img); err != nil {
			log.Printf("send to chat %d failed: %v", ch.ID, err)
			if isPermanentUnavailable(err) {
				if dropErr := b.dropRecipient(ctx, ch.ID, err); dropErr != nil {
					dropFailed++
					if firstErr == nil {
						firstErr = dropErr
					}
				}
				continue
			}
			failed++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		sent++
		if err := b.markChat(ctx, ch.ID, links); err != nil {
			log.Printf("delivery state not fully saved chat=%d: %v", ch.ID, err)
			unsaved++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		saved++
	}

	switch {
	case sent == 0 && failed > 0 && skipped == 0 && dropFailed == 0:
		return fmt.Errorf("%w: %v", ErrAllFailed, firstErr)
	case failed > 0 && unsaved > 0:
		return fmt.Errorf("%w: %w: sent=%d saved=%d unsaved=%d failed=%d skipped=%d dropFailed=%d: %v", ErrPartial, ErrStateSave, sent, saved, unsaved, failed, skipped, dropFailed, firstErr)
	case failed > 0:
		return fmt.Errorf("%w: sent=%d saved=%d unsaved=%d failed=%d skipped=%d dropFailed=%d", ErrPartial, sent, saved, unsaved, failed, skipped, dropFailed)
	case unsaved > 0:
		return fmt.Errorf("%w: sent=%d saved=%d unsaved=%d skipped=%d dropFailed=%d: %v", ErrStateSave, sent, saved, unsaved, skipped, dropFailed, firstErr)
	case dropFailed > 0:
		return fmt.Errorf("%w: %v", ErrRecipientDrop, firstErr)
	default:
		return nil
	}
}

func (b *Bot) dropRecipient(ctx context.Context, chatID int64, reason error) error {
	log.Printf("dropping inactive chat %d: %v", chatID, reason)
	if err := b.store.RemoveChat(ctx, chatID); err != nil {
		log.Printf("remove chat %d: %v", chatID, err)
		return err
	}
	return nil
}

func isPermanentUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, tb.ErrKickedFromGroup) ||
		errors.Is(err, tb.ErrKickedFromSuperGroup) ||
		errors.Is(err, tb.ErrKickedFromChannel) ||
		errors.Is(err, tb.ErrBlockedByUser) ||
		errors.Is(err, tb.ErrNotChannelMember) ||
		errors.Is(err, tb.ErrChatNotFound) ||
		errors.Is(err, tb.ErrUserIsDeactivated) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "bot was kicked") ||
		strings.Contains(s, "bot was blocked") ||
		strings.Contains(s, "bot is not a member") ||
		strings.Contains(s, "chat not found") ||
		strings.Contains(s, "user is deactivated")
}

func (b *Bot) chatHasAll(ctx context.Context, chatID int64, links []string) bool {
	if len(links) == 0 {
		return false
	}
	for _, link := range links {
		ok, err := b.store.WasDelivered(ctx, chatID, link)
		if err != nil || !ok {
			return false
		}
	}
	return true
}

// markChat writes delivery records for every link. It keeps going after a
// failure so a multi-link batch can be partially saved, and returns the first
// MarkDelivered error. Telegram acceptance is tracked separately by Broadcast.
func (b *Bot) markChat(ctx context.Context, chatID int64, links []string) error {
	var firstErr error
	for _, link := range links {
		if err := b.store.MarkDelivered(ctx, chatID, link); err != nil {
			log.Printf("mark delivered chat=%d link=%s: %v", chatID, link, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (b *Bot) sendOne(chat *tb.Chat, post string, img []byte) error {
	if len(img) > 0 {
		photo := &tb.Photo{File: tb.FromReader(bytes.NewReader(img))}
		photo.Caption = textutil.Truncate(post, captionMaxRunes)
		_, err := b.api.Send(chat, photo)
		return err
	}
	_, err := b.api.Send(chat, post, &tb.SendOptions{DisableWebPagePreview: true})
	return err
}
