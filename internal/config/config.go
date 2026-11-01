// Package config loads settings from the environment (and an optional .env).
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"

	"github.com/sanjayrj/ai-bot/internal/llm"
)

type Config struct {
	// Env is "local" (default) or "production". Production refuses to start
	// with unsafe settings; see Validate.
	Env string

	Addr     string
	BaseURL  string
	DemoMode bool
	Location *time.Location

	LLM llm.Config

	// AgentAPIToken, when set, is required as a Bearer token on /api/tools.
	AgentAPIToken string

	// RequireApproval makes send_email/create_event wait for the user.
	// Set in config.yaml (require_approval), overridable by REQUIRE_APPROVAL.
	RequireApproval bool
	// UndoSeconds is how long an approved action waits (and can be undone)
	// before it runs. config.yaml undo_seconds; 0 turns the window off.
	UndoSeconds int

	// DatabaseURL, when set, stores users, sessions, conversations and
	// actions in PostgreSQL.
	DatabaseURL string

	// AppSecret encrypts users' Google tokens at rest (min 32 chars).
	AppSecret string
	// AllowedEmails limits sign-in: comma-separated emails or "@domain".
	AllowedEmails string
	// AgentUserEmail is the account /api/tools acts as with AGENT_API_TOKEN.
	AgentUserEmail string
	// SessionTTL is how long a sign-in lasts.
	SessionTTL time.Duration
	// LogToolInputs logs full tool inputs (may include email content).
	LogToolInputs bool
	// MetricsToken protects /metrics; without it, /metrics is local-only.
	MetricsToken string
	// ChatPerMinute caps chat/briefing requests per user per minute.
	ChatPerMinute int

	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string

	// Slack app credentials for "Connect Slack" (optional).
	SlackClientID     string
	SlackClientSecret string
	SlackRedirectURL  string
}

func Load() (Config, error) {
	_ = godotenv.Load() // .env is optional

	port := env("PORT", "8080")
	base := env("BASE_URL", "http://localhost:"+port)
	loc := time.Local
	if tz := os.Getenv("TIMEZONE"); tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return Config{}, err
		}
		loc = l
	}
	demo, _ := strconv.ParseBool(env("DEMO_MODE", "false"))
	logInputs, _ := strconv.ParseBool(env("LOG_TOOL_INPUTS", "false"))
	perMin, err := strconv.Atoi(env("CHAT_PER_MINUTE", "12"))
	if err != nil || perMin < 1 {
		return Config{}, fmt.Errorf("CHAT_PER_MINUTE must be a positive number")
	}
	ttl, err := time.ParseDuration(env("SESSION_TTL", "720h"))
	if err != nil {
		return Config{}, fmt.Errorf("SESSION_TTL: %w", err)
	}
	file, err := loadFile(env("CONFIG_FILE", "config.yaml"))
	if err != nil {
		return Config{}, err
	}
	approval, err := boolSetting("REQUIRE_APPROVAL", file.RequireApproval, true)
	if err != nil {
		return Config{}, err
	}

	undo := 30
	if file.UndoSeconds != nil {
		undo = *file.UndoSeconds
	}
	if undo < 0 || undo > 120 {
		return Config{}, fmt.Errorf("undo_seconds must be between 0 and 120, got %d", undo)
	}

	c := Config{
		Env:                strings.ToLower(env("APP_ENV", "local")),
		Addr:               ":" + port,
		BaseURL:            base,
		DemoMode:           demo,
		Location:           loc,
		LLM:                loadLLM(),
		AgentAPIToken:      os.Getenv("AGENT_API_TOKEN"),
		RequireApproval:    approval,
		UndoSeconds:        undo,
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		AppSecret:          os.Getenv("APP_SECRET"),
		AllowedEmails:      os.Getenv("ALLOWED_EMAILS"),
		AgentUserEmail:     os.Getenv("AGENT_USER_EMAIL"),
		SessionTTL:         ttl,
		LogToolInputs:      logInputs,
		MetricsToken:       os.Getenv("METRICS_TOKEN"),
		ChatPerMinute:      perMin,
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:  env("GOOGLE_REDIRECT_URL", base+"/auth/google/callback"),
		SlackClientID:      os.Getenv("SLACK_CLIENT_ID"),
		SlackClientSecret:  os.Getenv("SLACK_CLIENT_SECRET"),
		SlackRedirectURL:   env("SLACK_REDIRECT_URL", base+"/auth/slack/callback"),
	}
	if c.Env == "production" {
		c.LogToolInputs = false // never log email content in production
	}
	return c, c.Validate()
}

// Validate rejects settings that are unsafe in production.
func (c Config) Validate() error {
	switch c.Env {
	case "local":
		return nil
	case "production":
	default:
		return fmt.Errorf("APP_ENV must be local or production, got %q", c.Env)
	}
	var problems []string
	if c.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL is empty (data would be lost on restart)")
	}
	if c.DemoMode {
		problems = append(problems, "DEMO_MODE is on")
	}
	if !strings.HasPrefix(c.BaseURL, "https://") {
		problems = append(problems, "BASE_URL must start with https:// (secure cookies)")
	}
	if len(c.AppSecret) < 32 {
		problems = append(problems, "APP_SECRET is missing or shorter than 32 characters")
	}
	if c.GoogleClientID == "" || c.GoogleClientSecret == "" {
		problems = append(problems, "GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET are not set")
	}
	if len(problems) > 0 {
		return fmt.Errorf("unsafe production config: %s", strings.Join(problems, "; "))
	}
	return nil
}

// loadLLM reads the generic LLM_* variables, falling back to the common
// vendor variables (OPENAI_API_KEY, ANTHROPIC_API_KEY) so either style works.
func loadLLM() llm.Config {
	c := llm.Config{
		Provider: os.Getenv("LLM_PROVIDER"),
		APIKey:   os.Getenv("LLM_API_KEY"),
		Model:    os.Getenv("LLM_MODEL"),
		BaseURL:  os.Getenv("LLM_BASE_URL"),
	}
	for _, m := range strings.Split(os.Getenv("LLM_FALLBACK_MODELS"), ",") {
		if m = strings.TrimSpace(m); m != "" {
			c.Fallbacks = append(c.Fallbacks, m)
		}
	}
	if c.APIKey == "" {
		switch {
		case os.Getenv("OPENAI_API_KEY") != "" && c.Provider != "anthropic":
			c.APIKey = os.Getenv("OPENAI_API_KEY")
			if c.Provider == "" {
				c.Provider = "openai"
			}
		case os.Getenv("ANTHROPIC_API_KEY") != "":
			c.APIKey = os.Getenv("ANTHROPIC_API_KEY")
			if c.Provider == "" {
				c.Provider = "anthropic"
			}
		}
	}
	return c.Resolve()
}

// LLMEnabled reports whether enough is set to try building a model client.
// Local OpenAI-compatible servers (Ollama etc.) need a base URL but no key.
func (c Config) LLMEnabled() bool { return c.LLM.APIKey != "" || c.LLM.BaseURL != "" }

// fileConfig is the shape of config.yaml. Pointers tell "unset" from false.
type fileConfig struct {
	RequireApproval *bool `yaml:"require_approval"`
	UndoSeconds     *int  `yaml:"undo_seconds"`
}

// loadFile reads config.yaml; a missing file just means defaults.
func loadFile(path string) (fileConfig, error) {
	var fc fileConfig
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fc, nil
	}
	if err != nil {
		return fc, err
	}
	if err := yaml.Unmarshal(b, &fc); err != nil {
		return fc, fmt.Errorf("%s: %w", path, err)
	}
	return fc, nil
}

// boolSetting resolves env var > file value > default.
func boolSetting(envKey string, fromFile *bool, def bool) (bool, error) {
	if v := os.Getenv(envKey); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return false, fmt.Errorf("%s must be true or false, got %q", envKey, v)
		}
		return b, nil
	}
	if fromFile != nil {
		return *fromFile, nil
	}
	return def, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
