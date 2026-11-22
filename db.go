package main

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS comments (
		id BIGSERIAL PRIMARY KEY,
		comment_id TEXT NOT NULL UNIQUE,
		media_id TEXT NOT NULL DEFAULT '',
		author_id TEXT NOT NULL DEFAULT '',
		text TEXT NOT NULL DEFAULT '',
		lang TEXT NOT NULL DEFAULT '',
		category TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'QUEUED',
		generated_reply TEXT NOT NULL DEFAULT '',
		sent_reply TEXT NOT NULL DEFAULT '',
		reply_source TEXT NOT NULL DEFAULT '',
		reply_api_id TEXT NOT NULL DEFAULT '',
		last_error TEXT NOT NULL DEFAULT '',
		attempts INT NOT NULL DEFAULT 0,
		source TEXT NOT NULL DEFAULT 'webhook',
		next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		locked_at TIMESTAMPTZ,
		send_started_at TIMESTAMPTZ,
		replied_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		CONSTRAINT comments_status_chk CHECK (status IN ('NEW','QUEUED','PROCESSING','REPLIED','SKIPPED','FAILED','RETRY'))
	)`,
	`CREATE INDEX IF NOT EXISTS idx_comments_queue ON comments (status, next_attempt_at)`,
	`CREATE INDEX IF NOT EXISTS idx_comments_author_media ON comments (author_id, media_id)`,
	`CREATE TABLE IF NOT EXISTS stats (
		day DATE NOT NULL,
		key TEXT NOT NULL,
		n BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (day, key)
	)`,
	`CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
}

type DB struct {
	pool *pgxpool.Pool
}

// Comment navbatdan olingan komment.
type Comment struct {
	ID             int64
	CommentID      string
	MediaID        string
	AuthorID       string
	Text           string
	Attempts       int
	SendStarted    bool
	GeneratedReply string
	CreatedAt      time.Time
}

type CommentRow struct {
	ID        int64     `json:"id"`
	CommentID string    `json:"comment_id"`
	Text      string    `json:"text"`
	Lang      string    `json:"lang"`
	Category  string    `json:"category"`
	Status    string    `json:"status"`
	Reply     string    `json:"sent_reply"`
	Error     string    `json:"last_error"`
	CreatedAt time.Time `json:"created_at"`
}

func openDB(ctx context.Context, connURL string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(connURL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 6
	// Oddiy protokol: Neon/Supabase pooler bilan muammosiz ishlaydi.
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{pool: pool}, nil
}

func (d *DB) migrate(ctx context.Context) error {
	for _, q := range schemaStatements {
		if _, err := d.pool.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// insertComment yangi kommentni QUEUED holatida yozadi.
// Komment avval kelgan bo'lsa false qaytaradi (idempotency).
func (d *DB) insertComment(ctx context.Context, commentID, mediaID, authorID, text, source string) (bool, error) {
	tag, err := d.pool.Exec(ctx,
		`INSERT INTO comments (comment_id, media_id, author_id, text, source)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (comment_id) DO NOTHING`,
		commentID, mediaID, authorID, cleanText(text), source)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// recoverStale 5 daqiqadan beri PROCESSING'da qotib qolgan kommentlarni qaytaradi.
func (d *DB) recoverStale(ctx context.Context) (int64, error) {
	tag, err := d.pool.Exec(ctx,
		`UPDATE comments SET status = 'RETRY', next_attempt_at = now()
		 WHERE status = 'PROCESSING' AND locked_at < now() - interval '5 minutes'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// claimNext navbatdagi kommentni band qiladi (bir nechta worker bir xil kommentni olmaydi).
func (d *DB) claimNext(ctx context.Context) (*Comment, error) {
	const q = `UPDATE comments
		SET status = 'PROCESSING', locked_at = now(), attempts = attempts + 1
		WHERE id = (
			SELECT id FROM comments
			WHERE status IN ('QUEUED', 'RETRY') AND next_attempt_at <= now()
			ORDER BY created_at, id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, comment_id, media_id, author_id, text, attempts,
			(send_started_at IS NOT NULL), generated_reply, created_at`
	var c Comment
	err := d.pool.QueryRow(ctx, q).Scan(&c.ID, &c.CommentID, &c.MediaID, &c.AuthorID, &c.Text,
		&c.Attempts, &c.SendStarted, &c.GeneratedReply, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (d *DB) setClassification(ctx context.Context, id int64, lang, category string) error {
	_, err := d.pool.Exec(ctx, `UPDATE comments SET lang = $2, category = $3 WHERE id = $1`, id, lang, category)
	return err
}

func (d *DB) saveGenerated(ctx context.Context, id int64, reply, source string) error {
	_, err := d.pool.Exec(ctx, `UPDATE comments SET generated_reply = $2, reply_source = $3 WHERE id = $1`, id, reply, source)
	return err
}

func (d *DB) markSendStarted(ctx context.Context, id int64) error {
	_, err := d.pool.Exec(ctx, `UPDATE comments SET send_started_at = now() WHERE id = $1`, id)
	return err
}

func (d *DB) markReplied(ctx context.Context, id int64, sentReply, apiID, note string) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE comments SET status = 'REPLIED', sent_reply = $2, reply_api_id = $3, last_error = $4, replied_at = now() WHERE id = $1`,
		id, sentReply, apiID, truncate(note, 500))
	return err
}

func (d *DB) markSkipped(ctx context.Context, id int64, reason string) error {
	_, err := d.pool.Exec(ctx, `UPDATE comments SET status = 'SKIPPED', last_error = $2 WHERE id = $1`, id, truncate(reason, 500))
	return err
}

func (d *DB) markFailed(ctx context.Context, id int64, errMsg string) error {
	_, err := d.pool.Exec(ctx, `UPDATE comments SET status = 'FAILED', last_error = $2 WHERE id = $1`, id, truncate(errMsg, 500))
	return err
}

// markRetry kommentni keyinroq qayta urinish uchun navbatga qaytaradi.
// refund=true bo'lsa, urinish hisobi qaytariladi (token/limit xatolarida).
func (d *DB) markRetry(ctx context.Context, id int64, errMsg string, delay time.Duration, refund bool) error {
	dec := 0
	if refund {
		dec = 1
	}
	_, err := d.pool.Exec(ctx,
		`UPDATE comments SET status = 'RETRY', last_error = $2,
		 attempts = GREATEST(attempts - $3, 0),
		 next_attempt_at = now() + ($4 * interval '1 second')
		 WHERE id = $1`,
		id, truncate(errMsg, 500), dec, int64(delay.Seconds()))
	return err
}

func (d *DB) recentReplies(ctx context.Context, lang, category string, n int) ([]string, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT sent_reply FROM comments
		 WHERE status = 'REPLIED' AND lang = $1 AND category = $2 AND sent_reply <> ''
		 ORDER BY replied_at DESC NULLS LAST LIMIT $3`, lang, category, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// sameTextRecently: shu muallif shu postga oxirgi 24 soatda aynan shu matnni yozganmi.
func (d *DB) sameTextRecently(ctx context.Context, authorID, mediaID, text string, exceptID int64) (bool, error) {
	if authorID == "" {
		return false, nil
	}
	var exists bool
	err := d.pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM comments
			WHERE author_id = $1 AND media_id = $2 AND lower(text) = lower($3)
			AND id <> $4 AND created_at > now() - interval '1 day'
		)`, authorID, mediaID, text, exceptID).Scan(&exists)
	return exists, err
}

func (d *DB) incStat(ctx context.Context, key string, n int64) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO stats (day, key, n) VALUES (CURRENT_DATE, $1, $2)
		 ON CONFLICT (day, key) DO UPDATE SET n = stats.n + EXCLUDED.n`, key, n)
	return err
}

func (d *DB) getStatToday(ctx context.Context, key string) (int64, error) {
	var n int64
	err := d.pool.QueryRow(ctx,
		`SELECT COALESCE((SELECT n FROM stats WHERE day = CURRENT_DATE AND key = $1), 0)`, key).Scan(&n)
	return n, err
}

func (d *DB) getSetting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := d.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (d *DB) setSetting(ctx context.Context, key, value string) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, now())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, value)
	return err
}

func (d *DB) queueCounts(ctx context.Context) (map[string]int64, error) {
	rows, err := d.pool.Query(ctx, `SELECT status, count(*) FROM comments GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var s string
		var n int64
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = n
	}
	return out, rows.Err()
}

func (d *DB) todayStats(ctx context.Context) (map[string]int64, error) {
	rows, err := d.pool.Query(ctx, `SELECT key, n FROM stats WHERE day = CURRENT_DATE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

func (d *DB) avgReplySeconds(ctx context.Context) (float64, error) {
	var v float64
	err := d.pool.QueryRow(ctx,
		`SELECT COALESCE(EXTRACT(EPOCH FROM avg(replied_at - created_at)), 0)::float8
		 FROM comments WHERE status = 'REPLIED' AND replied_at IS NOT NULL`).Scan(&v)
	return v, err
}

func (d *DB) searchComments(ctx context.Context, status, q string, limit int) ([]CommentRow, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT id, comment_id, text, lang, category, status, sent_reply, last_error, created_at
		 FROM comments
		 WHERE ($1 = '' OR status = $1)
		   AND ($2 = '' OR text ILIKE '%' || $2 || '%' OR sent_reply ILIKE '%' || $2 || '%')
		 ORDER BY id DESC LIMIT $3`, status, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CommentRow{}
	for rows.Next() {
		var r CommentRow
		if err := rows.Scan(&r.ID, &r.CommentID, &r.Text, &r.Lang, &r.Category, &r.Status, &r.Reply, &r.Error, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
