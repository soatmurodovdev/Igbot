package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	app *App
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/webhook", s.handleWebhook)
	mux.HandleFunc("/tasks/run", s.handleRun)
	mux.HandleFunc("/admin/stats", s.handleStats)
	mux.HandleFunc("/admin/comments", s.handleComments)
	if s.app.cfg.TestMode {
		mux.HandleFunc("/test/classify", s.handleTestClassify)
		mux.HandleFunc("/test/comment", s.handleTestComment)
		mux.HandleFunc("/test/flood", s.handleTestFlood)
	}
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func secretEqual(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// adminOK: Authorization: Bearer <ADMIN_TOKEN> yoki ?token=<ADMIN_TOKEN> (telefon brauzeri uchun).
func (s *Server) adminOK(r *http.Request) bool {
	tok := bearer(r)
	if tok == "" {
		tok = r.URL.Query().Get("token")
	}
	return secretEqual(tok, s.app.cfg.AdminToken)
}

// ---------- Webhook ----------

type igValue struct {
	ID        string `json:"id"`
	CommentID string `json:"comment_id"`
	ParentID  string `json:"parent_id"`
	Text      string `json:"text"`
	From      struct {
		ID string `json:"id"`
	} `json:"from"`
	Media struct {
		ID string `json:"id"`
	} `json:"media"`
}

type igPayload struct {
	Entry []struct {
		ID      string `json:"id"`
		Changes []struct {
			Field string  `json:"field"`
			Value igValue `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

type incomingComment struct {
	CommentID string
	MediaID   string
	AuthorID  string
	Text      string
}

func validSignature(secret, header string, body []byte) bool {
	if secret == "" || !strings.HasPrefix(header, "sha256=") {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), got)
}

func parseEvents(body []byte) ([]incomingComment, error) {
	trim := bytes.TrimSpace(body)
	var payloads []igPayload
	if len(trim) > 0 && trim[0] == '[' {
		if err := json.Unmarshal(trim, &payloads); err != nil {
			return nil, err
		}
	} else {
		var p igPayload
		if err := json.Unmarshal(trim, &p); err != nil {
			return nil, err
		}
		payloads = []igPayload{p}
	}
	var out []incomingComment
	for _, p := range payloads {
		for _, e := range p.Entry {
			for _, ch := range e.Changes {
				if ch.Field != "comments" {
					continue
				}
				v := ch.Value
				id := v.ID
				if id == "" {
					id = v.CommentID
				}
				if id == "" {
					continue
				}
				// Boshqa komment ostidagi javoblarni va akkaunt egasining o'z kommentlarini o'tkazib yuboramiz.
				if v.ParentID != "" {
					continue
				}
				if v.From.ID != "" && v.From.ID == e.ID {
					continue
				}
				out = append(out, incomingComment{CommentID: id, MediaID: v.Media.ID, AuthorID: v.From.ID, Text: v.Text})
			}
		}
	}
	return out, nil
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		if q.Get("hub.mode") == "subscribe" && secretEqual(q.Get("hub.verify_token"), s.app.cfg.VerifyToken) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(q.Get("hub.challenge")))
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	case http.MethodPost:
		s.receiveEvent(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) receiveEvent(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !validSignature(s.app.cfg.AppSecret, r.Header.Get("X-Hub-Signature-256"), body) {
		log.Printf("webhook imzosi noto'g'ri (APP_SECRET to'g'rimi?)")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	events, err := parseEvents(body)
	if err != nil {
		log.Printf("webhook JSON'ini o'qib bo'lmadi: %v", err)
		_, _ = w.Write([]byte("EVENT_RECEIVED"))
		return
	}
	ctx := r.Context()
	for _, ev := range events {
		inserted, err := s.app.db.insertComment(ctx, ev.CommentID, ev.MediaID, ev.AuthorID, ev.Text, "webhook")
		if err != nil {
			log.Printf("kommentni bazaga yozishda xato: %v", err)
			http.Error(w, "db error", http.StatusInternalServerError) // Meta qayta yuboradi
			return
		}
		if inserted {
			_ = s.app.db.incStat(ctx, "received", 1)
		} else {
			_ = s.app.db.incStat(ctx, "duplicates", 1)
		}
	}
	_, _ = w.Write([]byte("EVENT_RECEIVED"))
}

// ---------- Navbatni ishga tushirish (Cloud Scheduler chaqiradi) ----------

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tok := r.Header.Get("X-Cron-Secret")
	if tok == "" {
		tok = bearer(r)
	}
	if !secretEqual(tok, s.app.cfg.CronSecret) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := s.app.ig.RefreshIfNeeded(r.Context()); err != nil {
		log.Printf("token yangilashda xato: %v", err)
	}
	n := s.app.RunBatch(r.Context(), s.app.cfg.RunMaxDuration)
	writeJSON(w, http.StatusOK, map[string]any{"processed": n})
}

// ---------- Admin ----------

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ctx := r.Context()
	queue, err := s.app.db.queueCounts(ctx)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	today, err := s.app.db.todayStats(ctx)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	avg, _ := s.app.db.avgReplySeconds(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"queue_by_status":   queue,
		"today":             today,
		"avg_reply_seconds": avg,
		"dry_run":           s.app.cfg.DryRun,
		"reply_enabled":     s.app.cfg.ReplyEnabled,
		"ai_enabled":        s.app.cfg.AIEnabled,
	})
}

func (s *Server) handleComments(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	limit := 50
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 && n <= 500 {
		limit = n
	}
	rows, err := s.app.db.searchComments(r.Context(), strings.ToUpper(q.Get("status")), q.Get("q"), limit)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// ---------- Test rejimi (faqat TEST_MODE=true) ----------

func (s *Server) handleTestClassify(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	text := r.URL.Query().Get("text")
	res := Classify(text)
	sample := ""
	if res.Reply {
		sample = s.app.bank.Pick(res.Lang, res.Category, nil)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"category":     res.Category,
		"lang":         res.Lang,
		"will_reply":   res.Reply,
		"reason":       res.Reason,
		"sample_reply": sample,
	})
}

func (s *Server) handleTestComment(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	id := q.Get("id")
	if id == "" {
		id = fmt.Sprintf("test-%d", time.Now().UnixNano())
	}
	if !strings.HasPrefix(id, "test-") {
		id = "test-" + id
	}
	author := q.Get("author")
	if author == "" {
		author = "test-user"
	}
	inserted, err := s.app.db.insertComment(r.Context(), id, "test-media", author, q.Get("text"), "test")
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comment_id": id, "inserted": inserted})
}

var floodSamples = []string{
	"❤️", "🔥🔥", "Mashallah juda chiroyli", "Alloh asrasin", "Assalomu alaykum", "Qayerda olingan?",
	"Zo'r video", "Привет", "Очень красиво", "Wow amazing video", "Hello", "Omin", "Ajoyib!",
	"Bu juda yoqmadi", "Follow me t.me/kanal", "Juda yaxshi, men ham shunday qilib ko'rdim, katta rahmat sizga",
}

// handleTestFlood yuklama testi uchun n ta sun'iy komment qo'shadi.
func (s *Server) handleTestFlood(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	n := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("n")); err == nil && v > 0 && v <= 20000 {
		n = v
	}
	run := time.Now().UnixNano()
	start := time.Now()
	inserted := 0
	for i := 0; i < n; i++ {
		if r.Context().Err() != nil {
			break
		}
		text := fmt.Sprintf("%s %d", floodSamples[i%len(floodSamples)], i)
		ok, err := s.app.db.insertComment(r.Context(), fmt.Sprintf("test-flood-%d-%d", run, i),
			"test-media", fmt.Sprintf("test-u%d", i), text, "test")
		if err != nil {
			log.Printf("flood xatosi: %v", err)
			break
		}
		if ok {
			inserted++
		}
	}
	secs := time.Since(start).Seconds()
	rate := 0.0
	if secs > 0 {
		rate = float64(inserted) / secs
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"requested":       n,
		"inserted":        inserted,
		"seconds":         secs,
		"inserts_per_sec": rate,
	})
}
