package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

const (
	ModeWebhook = "webhook"
	ModePolling = "polling"
)

// VertexAI holds optional Vertex image-generation settings.
type VertexAI struct {
	ProjectID   string
	Location    string
	ImageModel  string
	AspectRatio string
}

// AppConfig is runtime configuration loaded from the environment.
type AppConfig struct {
	TelegramToken   string
	WebhookSecret   string
	BroadcastSecret string
	Port            string
	Mode            string
	ProjectID       string
	FirestoreDB     string
	GeminiAPIKey    string
	GeminiModel     string
	DigestLanguage  string
	RSSFeeds        []string
	AdminUserIDs    map[int64]struct{}
	VertexAI        VertexAI
}

// Load reads configuration from environment variables.
// It loads .env then .env.local when present (local values win).
func Load() (AppConfig, error) {
	_ = godotenv.Load(".env")
	_ = godotenv.Overload(".env.local")

	mode := strings.ToLower(strings.TrimSpace(getenvDefault("BOT_MODE", ModeWebhook)))
	if mode != ModeWebhook && mode != ModePolling {
		return AppConfig{}, fmt.Errorf("BOT_MODE must be %q or %q", ModeWebhook, ModePolling)
	}

	cfg := AppConfig{
		TelegramToken: firstNonEmpty(
			os.Getenv("TELEGRAM_TOKEN"),
			os.Getenv("TELEGRAM_BOT_TOKEN"),
		),
		WebhookSecret: firstNonEmpty(
			os.Getenv("WEBHOOK_SECRET"),
			os.Getenv("TELEGRAM_WEBHOOK_SECRET"),
		),
		BroadcastSecret: os.Getenv("BROADCAST_SECRET"),
		Port:            getenvDefault("PORT", "8080"),
		Mode:            mode,
		ProjectID:       firstNonEmpty(os.Getenv("FIRESTORE_PROJECT_ID"), os.Getenv("GOOGLE_CLOUD_PROJECT")),
		FirestoreDB:     getenvDefault("FIRESTORE_DATABASE", "(default)"),
		GeminiAPIKey: firstNonEmpty(
			os.Getenv("GEMINI_API_KEY"),
			os.Getenv("GOOGLE_API_KEY"),
		),
		GeminiModel:    getenvDefault("GEMINI_MODEL", "gemini-3.8-flash"),
		DigestLanguage: normalizeLanguage(os.Getenv("DIGEST_LANGUAGE")),
		AdminUserIDs:   parseIDSet(os.Getenv("ADMIN_USER_IDS")),
		VertexAI: VertexAI{
			ProjectID:   firstNonEmpty(os.Getenv("VERTEX_PROJECT_ID"), os.Getenv("GOOGLE_CLOUD_PROJECT")),
			Location:    getenvDefault("VERTEX_LOCATION", "us-central1"),
			ImageModel:  getenvDefault("VERTEX_IMAGE_MODEL", "gemini-2.5-flash-image"),
			AspectRatio: getenvDefault("VERTEX_ASPECT_RATIO", "1:1"),
		},
	}

	if feeds := firstNonEmpty(os.Getenv("RSS_FEEDS"), os.Getenv("FEEDS")); feeds != "" {
		cfg.RSSFeeds = splitAndTrim(feeds)
	}
	if len(cfg.RSSFeeds) == 0 {
		cfg.RSSFeeds = []string{
			"https://news.ycombinator.com/rss",
			"https://www.theverge.com/rss/index.xml",
		}
	}

	if cfg.TelegramToken == "" {
		return cfg, fmt.Errorf("TELEGRAM_TOKEN is required")
	}
	if cfg.GeminiAPIKey == "" {
		return cfg, fmt.Errorf("GEMINI_API_KEY is required")
	}
	if cfg.Mode == ModeWebhook && cfg.WebhookSecret == "" {
		return cfg, fmt.Errorf("WEBHOOK_SECRET is required in webhook mode")
	}

	return cfg, nil
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	res := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			res = append(res, p)
		}
	}
	return res
}

func parseIDSet(s string) map[int64]struct{} {
	out := make(map[int64]struct{})
	for _, p := range splitAndTrim(s) {
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			continue
		}
		out[id] = struct{}{}
	}
	return out
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// normalizeLanguage maps common codes to a name the model prompt can use.
// Empty / unknown values fall back to Russian, the original product voice.
func normalizeLanguage(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "Russian"
	}
	switch strings.ToLower(s) {
	case "ru", "rus", "russian":
		return "Russian"
	case "en", "eng", "english":
		return "English"
	default:
		return s
	}
}
