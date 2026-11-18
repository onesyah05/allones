// Package store is the SQLite persistence layer (pure Go, no CGO).
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB *sql.DB
}

var ErrNotFound = errors.New("not found")

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(8000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: one writer; keeps WAL simple and avoids SQLITE_BUSY
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func NewID(prefix string) string {
	b := make([]byte, 8)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func Now() int64 { return time.Now().UnixMilli() }

var migrations = []string{
	// 1: core
	`CREATE TABLE sessions (
		id TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '', role TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '', toolset TEXT NOT NULL DEFAULT '', source TEXT NOT NULL DEFAULT 'web',
		engagement_id TEXT NOT NULL DEFAULT '', goal TEXT NOT NULL DEFAULT '', pinned INTEGER NOT NULL DEFAULT 0,
		summary TEXT NOT NULL DEFAULT '', tokens_in INTEGER NOT NULL DEFAULT 0, tokens_out INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
	CREATE TABLE messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		role TEXT NOT NULL, content TEXT NOT NULL DEFAULT '', reasoning TEXT NOT NULL DEFAULT '',
		reasoning_sig TEXT NOT NULL DEFAULT '', tool_calls TEXT NOT NULL DEFAULT '', tool_call_id TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '', meta TEXT NOT NULL DEFAULT '', compacted INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL);
	CREATE INDEX messages_session ON messages(session_id, id);
	CREATE VIRTUAL TABLE messages_fts USING fts5(content, session_id UNINDEXED, tokenize='porter unicode61');
	CREATE TABLE memories (
		id TEXT PRIMARY KEY, key TEXT, content TEXT NOT NULL, tags TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL, used_at INTEGER NOT NULL);
	CREATE UNIQUE INDEX memories_key ON memories(key) WHERE key IS NOT NULL AND key <> '';
	CREATE TABLE chunks (
		id INTEGER PRIMARY KEY AUTOINCREMENT, collection TEXT NOT NULL, source TEXT NOT NULL,
		idx INTEGER NOT NULL, content TEXT NOT NULL, embedding BLOB, created_at INTEGER NOT NULL);
	CREATE INDEX chunks_coll ON chunks(collection, source);
	CREATE VIRTUAL TABLE chunks_fts USING fts5(content, tokenize='porter unicode61');
	CREATE TABLE kv (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL);
	CREATE TABLE audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, actor TEXT NOT NULL, action TEXT NOT NULL,
		target TEXT NOT NULL DEFAULT '', detail TEXT NOT NULL DEFAULT '', level TEXT NOT NULL DEFAULT 'info');
	CREATE INDEX audit_ts ON audit(ts);`,
	// 2: security engagements
	`CREATE TABLE engagements (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, client TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT '[]',
		exclusions TEXT NOT NULL DEFAULT '[]', authorized INTEGER NOT NULL DEFAULT 0, authorization_note TEXT NOT NULL DEFAULT '',
		rules TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'active', report TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
	CREATE TABLE intel (
		id INTEGER PRIMARY KEY AUTOINCREMENT, engagement_id TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
		kind TEXT NOT NULL, value TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '', source TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL, UNIQUE(engagement_id, kind, value));
	CREATE TABLE findings (
		id TEXT PRIMARY KEY, engagement_id TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL, severity TEXT NOT NULL, cvss_vector TEXT NOT NULL DEFAULT '', cvss_score REAL NOT NULL DEFAULT 0,
		cwe TEXT NOT NULL DEFAULT '', category TEXT NOT NULL DEFAULT '', target TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '', reproduction TEXT NOT NULL DEFAULT '', impact TEXT NOT NULL DEFAULT '',
		remediation TEXT NOT NULL DEFAULT '', evidence TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'open',
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
	CREATE INDEX findings_eng ON findings(engagement_id);`,
	// 3: work board, automation, rollback, servers
	`CREATE TABLE tasks (
		id TEXT PRIMARY KEY, title TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'todo',
		priority TEXT NOT NULL DEFAULT 'normal', assignee TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL DEFAULT '',
		position REAL NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
	CREATE TABLE cron_jobs (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, schedule TEXT NOT NULL, prompt TEXT NOT NULL, role TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, auto_approve INTEGER NOT NULL DEFAULT 0,
		last_run INTEGER NOT NULL DEFAULT 0, next_run INTEGER NOT NULL DEFAULT 0, last_status TEXT NOT NULL DEFAULT '',
		last_session TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL);
	CREATE TABLE snapshots (
		id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, path TEXT NOT NULL, existed INTEGER NOT NULL,
		content BLOB, created_at INTEGER NOT NULL, UNIQUE(session_id, path));
	CREATE TABLE servers (
		id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, host TEXT NOT NULL, port INTEGER NOT NULL DEFAULT 22,
		user TEXT NOT NULL, auth TEXT NOT NULL DEFAULT 'password', secret TEXT NOT NULL DEFAULT '',
		host_key TEXT NOT NULL DEFAULT '', notes TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL);
	CREATE TABLE role_stats (
		role TEXT PRIMARY KEY, runs INTEGER NOT NULL DEFAULT 0, ok INTEGER NOT NULL DEFAULT 0,
		failed INTEGER NOT NULL DEFAULT 0, turns INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL);`,
	// 4: per-session workspace directory
	`ALTER TABLE sessions ADD COLUMN workspace TEXT NOT NULL DEFAULT '';`,
}

func (s *Store) migrate() error {
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	if err := s.DB.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&v); err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("database schema v%d is newer than this build (v%d)", v, len(migrations))
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// ---------- KV ----------

func (s *Store) GetKV(key string) (string, bool) {
	var v string
	err := s.DB.QueryRow(`SELECT value FROM kv WHERE key=?`, key).Scan(&v)
	return v, err == nil
}

func (s *Store) SetKV(key, value string) error {
	_, err := s.DB.Exec(`INSERT INTO kv(key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, Now())
	return err
}

func (s *Store) DeleteKV(key string) error {
	_, err := s.DB.Exec(`DELETE FROM kv WHERE key=?`, key)
	return err
}

func (s *Store) GetJSON(key string, v any) bool {
	raw, ok := s.GetKV(key)
	if !ok {
		return false
	}
	return json.Unmarshal([]byte(raw), v) == nil
}

func (s *Store) SetJSON(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.SetKV(key, string(b))
}

// ---------- Audit ----------

type AuditEntry struct {
	ID     int64  `json:"id"`
	TS     int64  `json:"ts"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Target string `json:"target"`
	Detail string `json:"detail"`
	Level  string `json:"level"`
}

func (s *Store) Audit(actor, action, target, detail, level string) {
	if len(detail) > 4000 {
		detail = detail[:4000] + "…"
	}
	if level == "" {
		level = "info"
	}
	s.DB.Exec(`INSERT INTO audit(ts,actor,action,target,detail,level) VALUES(?,?,?,?,?,?)`, Now(), actor, action, target, detail, level)
}

func (s *Store) AuditLog(limit int, q string) ([]AuditEntry, error) {
	if limit <= 0 || limit > 2000 {
		limit = 300
	}
	rows, err := s.DB.Query(`SELECT id,ts,actor,action,target,detail,level FROM audit
		WHERE (?='' OR action LIKE '%'||?||'%' OR target LIKE '%'||?||'%' OR detail LIKE '%'||?||'%')
		ORDER BY id DESC LIMIT ?`, q, q, q, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var a AuditEntry
		if err := rows.Scan(&a.ID, &a.TS, &a.Actor, &a.Action, &a.Target, &a.Detail, &a.Level); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Stats returns cheap counters for the dashboard.
func (s *Store) Stats(ctx context.Context) map[string]int64 {
	out := map[string]int64{}
	for name, q := range map[string]string{
		"sessions": `SELECT COUNT(*) FROM sessions`, "messages": `SELECT COUNT(*) FROM messages`,
		"memories": `SELECT COUNT(*) FROM memories`, "chunks": `SELECT COUNT(*) FROM chunks`,
		"findings": `SELECT COUNT(*) FROM findings`, "engagements": `SELECT COUNT(*) FROM engagements`,
		"tasks": `SELECT COUNT(*) FROM tasks WHERE status<>'done'`, "cron_jobs": `SELECT COUNT(*) FROM cron_jobs`,
		"tokens_in": `SELECT COALESCE(SUM(tokens_in),0) FROM sessions`, "tokens_out": `SELECT COALESCE(SUM(tokens_out),0) FROM sessions`,
	} {
		var n int64
		s.DB.QueryRowContext(ctx, q).Scan(&n)
		out[name] = n
	}
	return out
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// FTSQuery converts free text into a safe FTS5 OR query.
func FTSQuery(q string) string {
	var terms []string
	for _, f := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !(r == '_' || r == '-' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r > 127)
	}) {
		f = strings.Trim(f, "-.")
		if len(f) < 2 || stopwords[f] {
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(f, `"`, ``)+`"`)
		if len(terms) >= 16 {
			break
		}
	}
	return strings.Join(terms, " OR ")
}

var stopwords = map[string]bool{"the": true, "and": true, "or": true, "of": true, "to": true, "in": true, "is": true, "a": true, "an": true, "for": true, "on": true, "with": true, "what": true, "how": true, "yang": true, "dan": true, "di": true, "ke": true, "ini": true, "itu": true}
