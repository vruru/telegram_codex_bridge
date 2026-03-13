package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"telegram-codex-bridge/internal/app"
	"telegram-codex-bridge/internal/buildinfo"
	"telegram-codex-bridge/internal/codex"
	"telegram-codex-bridge/internal/config"
	"telegram-codex-bridge/internal/control"
	"telegram-codex-bridge/internal/logging"
	"telegram-codex-bridge/internal/power"
	"telegram-codex-bridge/internal/store"
	"telegram-codex-bridge/internal/telegram"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "telegram-codex-bridge: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 {
		switch strings.TrimSpace(args[0]) {
		case "serve":
			return runServe()
		default:
			if control.IsCommand(args) {
				return control.Run(args)
			}
			return fmt.Errorf("unknown command %q", strings.TrimSpace(args[0]))
		}
	}

	return runServe()
}

func runServe() error {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger, logCloser, err := logging.NewLogger(logging.Config{
		FilePath:   cfg.Log.FilePath,
		MaxSizeMB:  cfg.Log.MaxSizeMB,
		MaxBackups: cfg.Log.MaxBackups,
	})
	if err != nil {
		return fmt.Errorf("initialize logger: %w", err)
	}
	defer logCloser.Close()
	logger.Printf("starting build version=%s commit=%s", buildinfo.DisplayVersion(), strings.TrimSpace(buildinfo.Commit))

	topicStore, err := store.NewSQLiteTopicStore(cfg.Store.StatePath)
	if err != nil {
		return fmt.Errorf("open topic store: %w", err)
	}

	bot := telegram.NewBot(cfg.Telegram, topicStore)
	codexClient := codex.NewClient(cfg.Codex)
	powerManager := power.New(cfg.Power.PreventSleep, logger)
	application := app.New(logger, bot, codexClient, topicStore, powerManager, cfg.Language, cfg.EnvPath, cfg.Log.Level == "debug")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	codexHealth := codex.CheckHealth(context.Background(), cfg.Codex)
	if codexHealth.Ready {
		logger.Printf("%s ready binary=%s login=%s", codexHealth.Provider, codexHealth.ResolvedBinary, codexHealth.LoginStatus)
	} else {
		logger.Printf("%s not ready: %s", codexHealth.Provider, userVisibleCodexHealth(codexHealth))
	}

	if err := application.Run(ctx); err != nil && err != context.Canceled {
		return fmt.Errorf("run app: %w", err)
	}
	return nil
}

func userVisibleCodexHealth(health codex.Health) string {
	if health.Error != "" {
		return health.Error
	}
	if health.LoginStatus != "" {
		return health.LoginStatus
	}
	return "unknown backend health error"
}
