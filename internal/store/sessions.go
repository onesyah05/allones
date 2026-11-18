package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type Session struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Role         string `json:"role"`
	Model        string `json:"model"`
	Toolset      string `json:"toolset"`
	Source       string `json:"source"`
	EngagementID string `json:"engagement_id"`
	Goal         string `json:"goal"`
	Pinned       bool   `json:"pinned"`
	Summary      string `json:"summary"`
	TokensIn     int64  `json:"tokens_in"`
	TokensOut    int64  `json:"tokens_out"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
	Workspace    string `json:"workspace"`
	Messages     int    `json:"message_count"`
}

type Message struct {
	ID           int64           `json:"id"`
	SessionID    string          `json:"session_id"`
	Role         string          `json:"role"` // user | assistant | tool | system | notice
	Content      string          `json:"content"`
	Reasoning    string          `json:"reasoning,omitempty"`
	ReasoningSig string          `json:"-"`
	ToolCalls    json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID   string          `json:"tool_call_id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Meta         json.RawMessage `json:"meta,omitempty"`
	Compacted    bool            `json:"compacted,omitempty"`
	CreatedAt    int64           `json:"created_at"`
}

const sessionCols = `id,title,role,model,toolset,source,engagement_id,goal,pinned,summary,tokens_in,tokens_out,created_at,updated_at,workspace`

func scanSession(sc interface{ Scan(...any) error }) (*Session, error) {
	var s Session
	var pinned int
	err := sc.Scan(&s.ID, &s.Title, &s.Role, &s.Model, &s.Toolset, &s.Source, &s.EngagementID, &s.Goal, &pinned, &s.Summary, &s.TokensIn, &s.TokensOut, &s.CreatedAt, &s.UpdatedAt, &s.Workspace)
	s.Pinned = pinned == 1
	return &s, err
}

func (s *Store) CreateSession(sess *Session) error {
	if sess.ID == "" {
		sess.ID = NewID("s_")
	}
	now := Now()
	sess.CreatedAt, sess.UpdatedAt = now, now
	if sess.Source == "" {
		sess.Source = "web"
	}
	_, err := s.DB.Exec(`INSERT INTO sessions(`+sessionCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sess.ID, sess.Title, sess.Role, sess.Model, sess.Toolset, sess.Source, sess.EngagementID, sess.Goal, b2i(sess.Pinned), sess.Summary, 0, 0, now, now, sess.Workspace)
	return err
}

func (s *Store) GetSession(id string) (*Session, error) {
	sess, err := scanSession(s.DB.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sess, err
}

func (s *Store) ListSessions(q string, limit int) ([]Session, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.DB.Query(`SELECT `+prefixCols("s.", sessionCols)+`, (SELECT COUNT(*) FROM messages m WHERE m.session_id=s.id AND m.role IN ('user','assistant'))
		FROM sessions s WHERE (?='' OR s.title LIKE '%'||?||'%' ESCAPE '\')
		ORDER BY s.pinned DESC, s.updated_at DESC LIMIT ?`, q, likeEscape(q), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var x Session
		var pinned int
		if err := rows.Scan(&x.ID, &x.Title, &x.Role, &x.Model, &x.Toolset, &x.Source, &x.EngagementID, &x.Goal, &pinned, &x.Summary, &x.TokensIn, &x.TokensOut, &x.CreatedAt, &x.UpdatedAt, &x.Workspace, &x.Messages); err != nil {
			return nil, err
		}
		x.Pinned = pinned == 1
		out = append(out, x)
	}
	return out, rows.Err()
}

func prefixCols(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i := range parts {
		parts[i] = p + parts[i]
	}
	return strings.Join(parts, ",")
}

// UpdateSession writes the mutable fields.
func (s *Store) UpdateSession(sess *Session) error {
	sess.UpdatedAt = Now()
	_, err := s.DB.Exec(`UPDATE sessions SET title=?,role=?,model=?,toolset=?,engagement_id=?,goal=?,pinned=?,summary=?,workspace=?,updated_at=? WHERE id=?`,
		sess.Title, sess.Role, sess.Model, sess.Toolset, sess.EngagementID, sess.Goal, b2i(sess.Pinned), sess.Summary, sess.Workspace, sess.UpdatedAt, sess.ID)
	return err
}

func (s *Store) AddUsage(id string, in, out int64) {
	s.DB.Exec(`UPDATE sessions SET tokens_in=tokens_in+?, tokens_out=tokens_out+?, updated_at=? WHERE id=?`, in, out, Now(), id)
}

func (s *Store) DeleteSession(id string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.Exec(`DELETE FROM messages_fts WHERE session_id=?`, id)
	tx.Exec(`DELETE FROM snapshots WHERE session_id=?`, id)
	tx.Exec(`DELETE FROM kv WHERE key LIKE ?`, "todo:"+id)
	if _, err := tx.Exec(`DELETE FROM sessions WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AppendMessage(m *Message) error {
	m.CreatedAt = Now()
	tc := ""
	if len(m.ToolCalls) > 0 && string(m.ToolCalls) != "null" {
		tc = string(m.ToolCalls)
	}
	meta := ""
	if len(m.Meta) > 0 && string(m.Meta) != "null" {
		meta = string(m.Meta)
	}
	res, err := s.DB.Exec(`INSERT INTO messages(session_id,role,content,reasoning,reasoning_sig,tool_calls,tool_call_id,name,meta,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		m.SessionID, m.Role, m.Content, m.Reasoning, m.ReasoningSig, tc, m.ToolCallID, m.Name, meta, m.CreatedAt)
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	if (m.Role == "user" || m.Role == "assistant") && strings.TrimSpace(m.Content) != "" {
		s.DB.Exec(`INSERT INTO messages_fts(rowid,content,session_id) VALUES(?,?,?)`, m.ID, m.Content, m.SessionID)
	}
	s.DB.Exec(`UPDATE sessions SET updated_at=? WHERE id=?`, m.CreatedAt, m.SessionID)
	return nil
}

func (s *Store) Messages(sessionID string, includeCompacted bool) ([]Message, error) {
	q := `SELECT id,session_id,role,content,reasoning,reasoning_sig,tool_calls,tool_call_id,name,meta,compacted,created_at FROM messages WHERE session_id=?`
	if !includeCompacted {
		q += ` AND compacted=0`
	}
	rows, err := s.DB.Query(q+` ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var tc, meta string
		var comp int
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.Reasoning, &m.ReasoningSig, &tc, &m.ToolCallID, &m.Name, &meta, &comp, &m.CreatedAt); err != nil {
			return nil, err
		}
		if tc != "" {
			m.ToolCalls = json.RawMessage(tc)
		}
		if meta != "" {
			m.Meta = json.RawMessage(meta)
		}
		m.Compacted = comp == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkCompacted hides messages up to and including id from the model context.
func (s *Store) MarkCompacted(sessionID string, uptoID int64) error {
	_, err := s.DB.Exec(`UPDATE messages SET compacted=1 WHERE session_id=? AND id<=?`, sessionID, uptoID)
	return err
}

// TruncateAfter removes messages after id (for edit/regenerate).
func (s *Store) TruncateAfter(sessionID string, id int64) error {
	s.DB.Exec(`DELETE FROM messages_fts WHERE rowid IN (SELECT id FROM messages WHERE session_id=? AND id>=?)`, sessionID, id)
	_, err := s.DB.Exec(`DELETE FROM messages WHERE session_id=? AND id>=?`, sessionID, id)
	return err
}

type SearchHit struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	MessageID int64  `json:"message_id"`
	Snippet   string `json:"snippet"`
}

func (s *Store) SearchMessages(q string, limit int) ([]SearchHit, error) {
	fq := FTSQuery(q)
	if fq == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.DB.Query(`SELECT f.session_id, COALESCE(s.title,''), f.rowid, snippet(messages_fts,0,'[',']','…',16)
		FROM messages_fts f LEFT JOIN sessions s ON s.id=f.session_id WHERE messages_fts MATCH ? ORDER BY rank LIMIT ?`, fq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.SessionID, &h.Title, &h.MessageID, &h.Snippet); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
