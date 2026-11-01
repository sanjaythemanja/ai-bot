// Command server runs the AI executive assistant.
package main

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	kitprometheus "github.com/go-kit/kit/metrics/prometheus"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	stdprometheus "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/sanjayrj/ai-bot/internal/assistant"
	"github.com/sanjayrj/ai-bot/internal/auth"
	"github.com/sanjayrj/ai-bot/internal/authhttp"
	"github.com/sanjayrj/ai-bot/internal/config"
	"github.com/sanjayrj/ai-bot/internal/httpx"
	"github.com/sanjayrj/ai-bot/internal/llm"
	"github.com/sanjayrj/ai-bot/internal/plugin"
	calendarplugin "github.com/sanjayrj/ai-bot/internal/plugins/calendar"
	gmailplugin "github.com/sanjayrj/ai-bot/internal/plugins/gmail"
	meetplugin "github.com/sanjayrj/ai-bot/internal/plugins/meet"
	slackplugin "github.com/sanjayrj/ai-bot/internal/plugins/slack"
	"github.com/sanjayrj/ai-bot/internal/provider"
	"github.com/sanjayrj/ai-bot/internal/provider/google"
	"github.com/sanjayrj/ai-bot/internal/provider/mock"
	slackclient "github.com/sanjayrj/ai-bot/internal/provider/slack"
	"github.com/sanjayrj/ai-bot/internal/secure"
	"github.com/sanjayrj/ai-bot/internal/store"
	"github.com/sanjayrj/ai-bot/web"
)

func main() {
	logger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	logger = log.With(logger, "ts", log.DefaultTimestampUTC, "caller", log.DefaultCaller)

	cfg, err := config.Load()
	if err != nil {
		_ = level.Error(logger).Log("msg", "config", "err", err)
		os.Exit(1)
	}

	// --- Storage: users, sessions, conversations, actions ---
	var (
		convs     store.Store       = store.NewMemory()
		users     store.Users       = convs.(*store.Memory)
		conns     store.Connections = convs.(*store.Memory)
		prefs     store.Settings    = convs.(*store.Memory)
		jobStore  store.Jobs        = convs.(*store.Memory)
		actions   store.Actions     = store.ActionsView{Memory: convs.(*store.Memory)}
		storeKind                   = "memory"
	)
	if cfg.DatabaseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		pg, err := store.OpenPostgres(ctx, cfg.DatabaseURL)
		cancel()
		if err != nil {
			_ = level.Error(logger).Log("msg", "database", "err", err)
			os.Exit(1)
		}
		convs, users, conns, prefs, jobStore, actions, storeKind = pg, pg, pg, pg, pg, pg.Actions(), "postgres"
	} else {
		_ = level.Warn(logger).Log("msg", "DATABASE_URL not set: users, chats and actions are kept in memory and lost on restart")
	}
	defer convs.Close()

	// --- Sign-in: sessions, encrypted per-user Google tokens ---
	sessions := &auth.Sessions{
		Users: users, TTL: cfg.SessionTTL, Secure: strings.HasPrefix(cfg.BaseURL, "https://"),
		AgentToken: cfg.AgentAPIToken, AgentEmail: cfg.AgentUserEmail,
	}
	var vault google.TokenStore
	connVault := auth.ConnVault{Conns: conns} // Box is added below when APP_SECRET is set
	if cfg.DemoMode {
		// Demo mode signs everyone in as one sample user; no Google needed.
		demo, err := users.UpsertUser(context.Background(), "demo", "demo@example.com", "Demo User")
		if err != nil {
			_ = level.Error(logger).Log("msg", "demo user", "err", err)
			os.Exit(1)
		}
		sessions.DemoUser = &demo
	} else {
		box, err := secure.NewBox(cfg.AppSecret)
		if err != nil {
			_ = level.Error(logger).Log("msg", "APP_SECRET", "err", err)
			os.Exit(1)
		}
		vault = auth.Vault{Users: users, Box: box}
		connVault.Box = box
	}
	gauth := google.NewAuth(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL, vault)
	allow := auth.ParseAllowlist(cfg.AllowedEmails)
	if len(allow) == 0 && !cfg.DemoMode {
		_ = level.Warn(logger).Log("msg", "ALLOWED_EMAILS is empty: anyone Google lets through (your OAuth test users) can sign in")
	}

	// --- Integrations (each call runs as the signed-in user) ---
	var (
		mail provider.Mail     = google.NewGmail(gauth)
		cal  provider.Calendar = google.NewCalendar(gauth)
		meet provider.Meetings = google.NewMeet(gauth)
		chat provider.Chat     // nil unless Slack is configured (or demo)
	)
	slackOAuth := authhttp.SlackOAuth{ClientID: cfg.SlackClientID, ClientSecret: cfg.SlackClientSecret, RedirectURL: cfg.SlackRedirectURL}
	if slackOAuth.Configured() && !cfg.DemoMode {
		chat = slackclient.New(connVault)
	}
	if cfg.DemoMode {
		ws := mock.New(time.Now().In(cfg.Location))
		mail, cal, meet, chat = ws.Mail(), ws.Calendar(), ws.Meetings(), ws.Chat()
		_ = level.Info(logger).Log("msg", "DEMO_MODE on: sample Gmail/Calendar data, signed in as the demo user")
	}

	// --- Model (any provider) ---
	var model llm.Client
	var aiErr string
	if cfg.LLMEnabled() {
		if model, err = llm.New(cfg.LLM); err != nil {
			aiErr = err.Error()
			_ = level.Warn(logger).Log("msg", "AI model not configured", "err", err)
		}
	}

	// --- Service + go-kit middleware stack ---
	// Integrations are plugins: add one here and nothing else in the core changes.
	settings := assistant.UserSettings{Store: prefs}
	plugins := []plugin.Plugin{
		gmailplugin.New(mail, settings),
		calendarplugin.New(cal, cfg.Location, settings),
		meetplugin.New(meet, cal, cfg.Location),
	}
	if chat != nil {
		plugins = append(plugins, slackplugin.New(chat))
	}
	tools := assistant.NewToolbox(actions, cfg.RequireApproval, plugins...)
	agent := assistant.NewAgent(model, tools, cfg.Location, log.With(logger, "component", "agent"))
	agent.LogInputs = cfg.LogToolInputs

	// Chat turns can run in the background; jobs left running by a previous
	// process are marked interrupted.
	jobs := assistant.NewJobs(jobStore, log.With(logger, "component", "jobs"))
	jobs.Recover(context.Background())

	fields := []string{"method"}
	var svc assistant.Service = assistant.NewService(assistant.Config{
		Mail: mail, Calendar: cal, Agent: agent, Tools: tools, Actions: actions,
		Location: cfg.Location, DemoMode: cfg.DemoMode,
		AIConfigured: model != nil, AIProvider: cfg.LLM.Provider, Model: cfg.LLM.Model, AIError: aiErr,
		GoogleConfigured: gauth.Configured(),
		Store:            convs, StoreKind: storeKind, UndoSeconds: cfg.UndoSeconds, ReadAloud: cfg.ReadAloud, Jobs: jobs,
	})
	svc = assistant.LoggingMiddleware(log.With(logger, "component", "service"))(svc)
	svc = assistant.InstrumentingMiddleware(
		kitprometheus.NewCounterFrom(stdprometheus.CounterOpts{Namespace: "exec_assistant", Name: "requests_total", Help: "Service calls."}, fields),
		kitprometheus.NewCounterFrom(stdprometheus.CounterOpts{Namespace: "exec_assistant", Name: "errors_total", Help: "Service calls that failed."}, fields),
		kitprometheus.NewHistogramFrom(stdprometheus.HistogramOpts{Namespace: "exec_assistant", Name: "request_duration_seconds", Help: "Service call latency.",
			Buckets: []float64{.01, .05, .1, .5, 1, 2.5, 5, 10, 30, 60, 120}}, fields),
	)(svc)

	endpoints := assistant.MakeEndpoints(svc, log.With(logger, "component", "endpoint"))

	// --- HTTP ---
	mux := http.NewServeMux()
	assistant.RegisterHTTPRoutes(mux, endpoints, log.With(logger, "component", "http"))
	assistant.RegisterJobStream(mux, jobs)
	demoSlack := ""
	if cfg.DemoMode {
		demoSlack = "Demo workspace"
	}
	authhttp.RegisterConnections(mux, authhttp.ConnDeps{Slack: slackOAuth, Sessions: sessions, Vault: connVault, Logger: log.With(logger, "component", "connections"), DemoLabel: demoSlack})
	authhttp.Register(mux, authhttp.Deps{Google: gauth, Sessions: sessions, Users: users, Allowlist: allow, Logger: log.With(logger, "component", "auth")})
	mux.Handle("GET /metrics", httpx.ProtectMetrics(cfg.MetricsToken, promhttp.Handler()))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	_ = mime.AddExtensionType(".js", "text/javascript")
	mux.Handle("GET /", http.FileServerFS(web.FS()))

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpx.SecurityHeaders(sessions.Secure, jsonOnlyPOST(sessions.Require(
			httpx.NewRateLimiter(cfg.ChatPerMinute, "/api/chat", "/api/chat/start", "/api/briefing").Wrap(mux)))),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      6 * time.Minute, // agent runs can take a while
	}

	go func() {
		_ = level.Info(logger).Log("msg", "listening", "env", cfg.Env, "addr", cfg.BaseURL, "ai_provider", cfg.LLM.Provider,
			"model", cfg.LLM.Model, "fallback_models", strings.Join(cfg.LLM.Fallbacks, ","), "ai", model != nil, "google_oauth", gauth.Configured(), "require_approval", cfg.RequireApproval, "storage", storeKind, "plugins", strings.Join(tools.Plugins(), ","))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_ = level.Error(logger).Log("msg", "server", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	_ = level.Info(logger).Log("msg", "stopped")
}

// jsonOnlyPOST rejects state-changing calls that are not JSON. Browsers
// cannot send application/json cross-origin without a CORS preflight, so this
// stops other sites from approving actions via a hidden form (CSRF).
func jsonOnlyPOST(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guarded := strings.HasPrefix(r.URL.Path, "/api/") || (r.URL.Path == "/auth/logout" || r.URL.Path == "/auth/slack/disconnect")
		if r.Method == http.MethodPost && guarded &&
			!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, `{"error":"Content-Type must be application/json"}`, http.StatusUnsupportedMediaType)
			return
		}
		next.ServeHTTP(w, r)
	})
}
