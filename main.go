package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	cfg := loadConfig()
	if err := cfg.validate(); err != nil {
		log.Fatalf("sozlama xatosi: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := openDB(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("bazaga ulanib bo'lmadi: %v", err)
	}
	if err := db.migrate(ctx); err != nil {
		log.Fatalf("baza jadvallarini yaratib bo'lmadi: %v", err)
	}

	bank, err := loadReplyBank(cfg.RepliesFile)
	if err != nil {
		log.Fatalf("javoblar bankini o'qib bo'lmadi: %v", err)
	}

	ig := newIGClient(cfg, db)
	ig.loadToken(ctx)

	app := &App{
		cfg:   cfg,
		db:    db,
		ig:    ig,
		ai:    newAI(cfg, db),
		bank:  bank,
		pacer: &pacer{every: cfg.MinSendInterval},
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           (&Server{app: app}).routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
	}

	if cfg.BackgroundWorker {
		go app.backgroundLoop(ctx)
	}

	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	log.Printf("igbot ishga tushdi: port=%s dry_run=%v test_mode=%v ai=%v", cfg.Port, cfg.DryRun, cfg.TestMode, cfg.AIEnabled)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server xatosi: %v", err)
	}
}
