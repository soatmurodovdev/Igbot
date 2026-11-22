package main

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type App struct {
	cfg   *Config
	db    *DB
	ig    *IGClient
	ai    *AI
	bank  *ReplyBank
	pacer *pacer
}

// pacer Instagram'ga yuboriladigan so'rovlar orasida minimal vaqt oralig'ini ta'minlaydi.
type pacer struct {
	mu    sync.Mutex
	last  time.Time
	every time.Duration
}

func (p *pacer) wait(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	next := p.last.Add(p.every)
	if d := time.Until(next); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.last = time.Now()
	return nil
}

// backoff: 30s, 60s, 2m, 4m ... maksimum 1 soat (exponential backoff).
func backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 8 {
		attempts = 8
	}
	d := (30 * time.Second) << uint(attempts-1)
	if d > time.Hour {
		d = time.Hour
	}
	return d
}

// RunBatch navbatni maxDur vaqt davomida qayta ishlaydi va qayta ishlangan kommentlar sonini qaytaradi.
func (a *App) RunBatch(ctx context.Context, maxDur time.Duration) int64 {
	if !a.cfg.ReplyEnabled {
		return 0
	}
	if n, err := a.db.recoverStale(ctx); err != nil {
		log.Printf("qotib qolgan kommentlarni tiklashda xato: %v", err)
	} else if n > 0 {
		log.Printf("%d ta qotib qolgan komment navbatga qaytarildi", n)
	}

	deadline := time.Now().Add(maxDur)
	var processed atomic.Int64
	var stop atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < a.cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() && time.Now().Before(deadline) && ctx.Err() == nil {
				did, halt := a.processOne(ctx)
				if halt {
					stop.Store(true)
				}
				if !did {
					return
				}
				processed.Add(1)
			}
		}()
	}
	wg.Wait()
	return processed.Load()
}

// backgroundLoop doimiy ishlaydigan rejim (BACKGROUND_WORKER=true).
func (a *App) backgroundLoop(ctx context.Context) {
	for ctx.Err() == nil {
		n := a.RunBatch(ctx, 30*time.Second)
		if n == 0 {
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (a *App) skip(ctx context.Context, c *Comment, reason string) {
	if err := a.db.markSkipped(ctx, c.ID, reason); err != nil {
		log.Printf("SKIPPED yozishda xato (comment=%s): %v", c.CommentID, err)
	}
	_ = a.db.incStat(ctx, "skipped", 1)
}

// makeReply javob yaratadi: avval AI (faqat uzun/tanqid kommentlar uchun), bo'lmasa javoblar banki.
func (a *App) makeReply(ctx context.Context, text, lang, category string) (string, string) {
	if a.cfg.AIEnabled && (category == "long" || category == "criticism") {
		r, err := a.ai.Generate(ctx, text, lang)
		if err == nil {
			return r, "ai"
		}
		if errors.Is(err, errAISkip) {
			return "", "ai_skip"
		}
		log.Printf("AI ishlamadi, javoblar bankiga o'tildi: %v", err)
		_ = a.db.incStat(ctx, "ai_errors", 1)
	}
	recent, err := a.db.recentReplies(ctx, lang, category, 10)
	if err != nil {
		log.Printf("oxirgi javoblarni o'qishda xato: %v", err)
		recent = nil
	}
	return a.bank.Pick(lang, category, recent), "bank"
}

// processOne bitta kommentni qayta ishlaydi.
// Qaytaradi: (komment olindimi, ishni to'xtatish kerakmi).
func (a *App) processOne(ctx context.Context) (bool, bool) {
	c, err := a.db.claimNext(ctx)
	if err != nil {
		log.Printf("navbatdan komment olishda xato: %v", err)
		return false, true
	}
	if c == nil {
		return false, false
	}

	// "test-" bilan boshlangan kommentlarga hech qachon haqiqiy javob yuborilmaydi.
	isTest := strings.HasPrefix(c.CommentID, "test-")
	reply := c.GeneratedReply

	if reply == "" {
		res := Classify(c.Text)
		if err := a.db.setClassification(ctx, c.ID, res.Lang, res.Category); err != nil {
			log.Printf("tasnifni yozishda xato: %v", err)
		}
		_ = a.db.incStat(ctx, "processed", 1)

		if !res.Reply {
			if res.Category == "spam" {
				_ = a.db.incStat(ctx, "spam", 1)
			}
			a.skip(ctx, c, res.Reason)
			return true, false
		}
		if res.Category == "question" && a.cfg.QuestionMode == "skip" {
			a.skip(ctx, c, "savollarga javob berish o'chirilgan")
			return true, false
		}
		if dup, err := a.db.sameTextRecently(ctx, c.AuthorID, c.MediaID, c.Text, c.ID); err != nil {
			log.Printf("takroriy kommentni tekshirishda xato: %v", err)
		} else if dup {
			_ = a.db.incStat(ctx, "repeated", 1)
			a.skip(ctx, c, "takroriy komment")
			return true, false
		}

		text, source := a.makeReply(ctx, c.Text, res.Lang, res.Category)
		if text == "" {
			a.skip(ctx, c, "javob kerak emas ("+source+")")
			return true, false
		}
		if err := a.db.saveGenerated(ctx, c.ID, text, source); err != nil {
			return a.handleSendError(ctx, c, err)
		}
		reply = text
	}

	live := !a.cfg.DryRun && !isTest

	// Oldingi urinishda yuborish boshlangan bo'lsa, takroriy javobdan saqlanish uchun tekshiramiz.
	if c.SendStarted && live {
		exists, err := a.ig.ReplyExists(ctx, c.CommentID, reply)
		if err != nil {
			return a.handleSendError(ctx, c, err)
		}
		if exists {
			_ = a.db.markReplied(ctx, c.ID, reply, "", "avval yuborilgan (tekshiruv orqali topildi)")
			_ = a.db.incStat(ctx, "replied", 1)
			return true, false
		}
	}

	if err := a.db.markSendStarted(ctx, c.ID); err != nil {
		return a.handleSendError(ctx, c, err)
	}

	if !live {
		_ = a.db.markReplied(ctx, c.ID, reply, "", "DRY_RUN: haqiqiy javob yuborilmadi")
		_ = a.db.incStat(ctx, "replied", 1)
		return true, false
	}

	if err := a.pacer.wait(ctx); err != nil {
		_ = a.db.markRetry(ctx, c.ID, err.Error(), 10*time.Second, true)
		return true, true
	}
	_ = a.db.incStat(ctx, "api_requests", 1)
	apiID, err := a.ig.Reply(ctx, c.CommentID, reply)
	if err != nil {
		return a.handleSendError(ctx, c, err)
	}
	if err := a.db.markReplied(ctx, c.ID, reply, apiID, ""); err != nil {
		log.Printf("REPLIED yozishda xato (comment=%s): %v", c.CommentID, err)
	}
	_ = a.db.incStat(ctx, "replied", 1)
	log.Printf("javob yuborildi: comment=%s", c.CommentID)
	return true, false
}

// handleSendError xatoni turiga qarab qayta urinish yoki FAILED holatiga o'tkazadi.
func (a *App) handleSendError(ctx context.Context, c *Comment, err error) (bool, bool) {
	msg := err.Error()
	_ = a.db.incStat(ctx, "api_errors", 1)

	var ae *APIError
	if errors.As(err, &ae) {
		if ae.IsAuth() {
			log.Printf("DIQQAT: Instagram tokeni yaroqsiz yoki muddati tugagan: %v", err)
			_ = a.db.markRetry(ctx, c.ID, msg, 30*time.Minute, true)
			_ = a.db.incStat(ctx, "retried", 1)
			return true, true
		}
		if ae.IsRateLimit() {
			log.Printf("API limiti: %v", err)
			_ = a.db.markRetry(ctx, c.ID, msg, backoff(c.Attempts), true)
			_ = a.db.incStat(ctx, "retried", 1)
			return true, true
		}
		if !ae.IsTransient() {
			log.Printf("doimiy xato, FAILED: comment=%s: %v", c.CommentID, err)
			_ = a.db.markFailed(ctx, c.ID, msg)
			_ = a.db.incStat(ctx, "failed", 1)
			return true, false
		}
	}

	// Vaqtinchalik xato (tarmoq, 5xx, baza)
	if c.Attempts >= a.cfg.MaxAttempts {
		log.Printf("urinishlar tugadi, FAILED: comment=%s: %v", c.CommentID, err)
		_ = a.db.markFailed(ctx, c.ID, msg)
		_ = a.db.incStat(ctx, "failed", 1)
		return true, false
	}
	log.Printf("vaqtinchalik xato, qayta uriniladi: comment=%s: %v", c.CommentID, err)
	_ = a.db.markRetry(ctx, c.ID, msg, backoff(c.Attempts), false)
	_ = a.db.incStat(ctx, "retried", 1)
	return true, false
}
