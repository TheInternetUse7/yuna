// Command yuna is a Discord chatbot backed by Bifrost, with persistent memory.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/TheInternetUse7/yuna/internal/ai"
	"github.com/TheInternetUse7/yuna/internal/applog"
	"github.com/TheInternetUse7/yuna/internal/bot"
	"github.com/TheInternetUse7/yuna/internal/config"
	"github.com/TheInternetUse7/yuna/internal/memory"
	"github.com/TheInternetUse7/yuna/internal/store"
)

// version is the release this binary was built from. Release builds set it with
// -ldflags "-X main.version=<git tag>"; every other build reports dev.
var version = "dev"

func versionString() string {
	if v := strings.TrimSpace(version); v != "" {
		return v
	}
	return "dev"
}

func main() {
	checkOnly := flag.Bool("check", false,
		"validate config, open the database and initialise the AI client, then exit without connecting to Discord")
	configPath := flag.String("config", config.DefaultConfigPath,
		"path to the YAML configuration file")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("yuna %s\n", versionString())
		return
	}

	if err := run(*checkOnly, *configPath); err != nil {
		fmt.Fprintf(os.Stderr, "yuna: %v\n", err)
		os.Exit(1)
	}
}

func run(checkOnly bool, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	log, err := applog.New(cfg.LogFile, cfg.Debug)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	log.Infof("starting yuna %s; provider chain: %v (summary: %s/%s)",
		versionString(), cfg.ProviderNames(), cfg.SummaryProvider, cfg.SummaryModel)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Errorf("close database: %v", err)
		}
	}()
	log.Infof("database ready at %s", cfg.DBPath)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	account := ai.NewAccount(cfg.Providers, cfg.MaxRetries, cfg.RequestTimeoutSeconds)
	client, err := ai.NewClient(ctx, account, log.Named("ai"))
	if err != nil {
		return err
	}
	defer client.Close()

	if checkOnly {
		log.Infof("check passed: providers %v (summary: %s/%s), database %s, memory enabled=%t",
			cfg.ProviderNames(), cfg.SummaryProvider, cfg.SummaryModel, cfg.DBPath, cfg.MemoryEnabled)
		return nil
	}

	// config.Load already verified this provider exists in the chain.
	summaryProvider, _ := cfg.Provider(cfg.SummaryProvider)

	mem := memory.NewManager(st, client, memory.Settings{
		SystemPrompt:    cfg.SystemPrompt,
		HistoryWindow:   cfg.HistoryWindow,
		SummaryEvery:    cfg.SummaryEvery,
		SummaryProvider: summaryProvider,
		SummaryModel:    cfg.SummaryModel,
		Chain:           cfg.Providers,
		FactsPerUser:    cfg.FactsPerUserLimit,
		FactsInject:     cfg.FactsInjectLimit,
		Enabled:         cfg.MemoryEnabled,
	}, log.Named("memory"))

	b, err := bot.New(cfg, st, mem, client, log.Named("discord"))
	if err != nil {
		return err
	}

	log.Infof("connecting to discord")
	if err := b.Run(ctx); err != nil {
		return err
	}
	log.Infof("stopped")
	return nil
}
