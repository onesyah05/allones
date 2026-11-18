package store

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"strings"
)

// ---------- Memory ----------

type Memory struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	Content   string `json:"content"`
	Tags      string `json:"tags"`
	CreatedAt int64  `json:"created_at"`
	UsedAt    int64  `json:"used_at"`
}

func (s *Store) SaveMemory(m *Memory) error {
	now := Now()
	if m.Key != "" {
		var id string
		if err := s.DB.QueryRow(`SELECT id FROM memories WHERE key=?`, m.Key).Scan(&id); err == nil {
			m.ID = id
			_, err := s.DB.Exec(`UPDATE memories SET content=?, tags=?, used_at=? WHERE id=?`, m.Content, m.Tags, now, id)
			return err
		}
	}
	if m.ID == "" {
		m.ID = NewID("m_")
	}
	m.CreatedAt, m.UsedAt = now, now
	var key any
	if m.Key != "" {
		key = m.Key
	}
	_, err := s.DB.Exec(`INSERT INTO memories(id,key,content,tags,created_at,used_at) VALUES(?,?,?,?,?,?)`, m.ID, key, m.Content, m.Tags, now, now)
	return err
}

func (s *Store) ListMemories(q string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 500
	}
	words := strings.Fields(strings.ToLower(q))
	rows, err := s.DB.Query(`SELECT id,COALESCE(key,''),content,tags,created_at,used_at FROM memories ORDER BY used_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Key, &m.Content, &m.Tags, &m.CreatedAt, &m.UsedAt); err != nil {
			return nil, err
		}
		if len(words) > 0 {
			hay := strings.ToLower(m.Key + " " + m.Content + " " + m.Tags)
			hit := false
			for _, w := range words {
				if strings.Contains(hay, w) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		out = append(out, m)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

func (s *Store) DeleteMemory(idOrKey string) (bool, error) {
	res, err := s.DB.Exec(`DELETE FROM memories WHERE id=? OR key=?`, idOrKey, idOrKey)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) TouchMemories(ids []string) {
	for _, id := range ids {
		s.DB.Exec(`UPDATE memories SET used_at=? WHERE id=?`, Now(), id)
	}
}

// ---------- RAG chunks ----------

type Chunk struct {
	ID         int64     `json:"id"`
	Collection string    `json:"collection"`
	Source     string    `json:"source"`
	Index      int       `json:"index"`
	Content    string    `json:"content"`
	Embedding  []float32 `json:"-"`
	Score      float64   `json:"score"`
}

func EncodeVec(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

func DecodeVec(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}

func (s *Store) ReplaceSource(collection, source string, chunks []Chunk) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.Exec(`DELETE FROM chunks_fts WHERE rowid IN (SELECT id FROM chunks WHERE collection=? AND source=?)`, collection, source)
	if _, err := tx.Exec(`DELETE FROM chunks WHERE collection=? AND source=?`, collection, source); err != nil {
		return err
	}
	now := Now()
	for _, c := range chunks {
		res, err := tx.Exec(`INSERT INTO chunks(collection,source,idx,content,embedding,created_at) VALUES(?,?,?,?,?,?)`, collection, source, c.Index, c.Content, EncodeVec(c.Embedding), now)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		if _, err := tx.Exec(`INSERT INTO chunks_fts(rowid,content) VALUES(?,?)`, id, c.Content); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LexicalChunks(collection, q string, limit int) ([]Chunk, error) {
	fq := FTSQuery(q)
	if fq == "" {
		return nil, nil
	}
	rows, err := s.DB.Query(`SELECT c.id,c.collection,c.source,c.idx,c.content,bm25(chunks_fts) FROM chunks_fts f JOIN chunks c ON c.id=f.rowid
		WHERE chunks_fts MATCH ? AND (?='' OR c.collection=?) ORDER BY bm25(chunks_fts) LIMIT ?`, fq, collection, collection, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chunk
	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.ID, &c.Collection, &c.Source, &c.Index, &c.Content, &c.Score); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// VectorChunks loads all embedded chunks of a collection (brute force cosine; fine to ~100k chunks).
func (s *Store) VectorChunks(collection string) ([]Chunk, error) {
	rows, err := s.DB.Query(`SELECT id,collection,source,idx,content,embedding FROM chunks WHERE embedding IS NOT NULL AND (?='' OR collection=?)`, collection, collection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chunk
	for rows.Next() {
		var c Chunk
		var emb []byte
		if err := rows.Scan(&c.ID, &c.Collection, &c.Source, &c.Index, &c.Content, &emb); err != nil {
			return nil, err
		}
		c.Embedding = DecodeVec(emb)
		out = append(out, c)
	}
	return out, rows.Err()
}

type CollectionInfo struct {
	Name    string `json:"name"`
	Sources int    `json:"sources"`
	Chunks  int    `json:"chunks"`
}

func (s *Store) Collections() ([]CollectionInfo, error) {
	rows, err := s.DB.Query(`SELECT collection, COUNT(DISTINCT source), COUNT(*) FROM chunks GROUP BY collection ORDER BY collection`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CollectionInfo
	for rows.Next() {
		var c CollectionInfo
		rows.Scan(&c.Name, &c.Sources, &c.Chunks)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Sources(collection string) ([]string, error) {
	rows, err := s.DB.Query(`SELECT DISTINCT source FROM chunks WHERE collection=? ORDER BY source`, collection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var x string
		rows.Scan(&x)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) DeleteCollection(collection, source string) error {
	s.DB.Exec(`DELETE FROM chunks_fts WHERE rowid IN (SELECT id FROM chunks WHERE collection=? AND (?='' OR source=?))`, collection, source, source)
	_, err := s.DB.Exec(`DELETE FROM chunks WHERE collection=? AND (?='' OR source=?)`, collection, source, source)
	return err
}

// ---------- Engagements, intel, findings ----------

type Engagement struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Client            string   `json:"client"`
	Scope             []string `json:"scope"`
	Exclusions        []string `json:"exclusions"`
	Authorized        bool     `json:"authorized"`
	AuthorizationNote string   `json:"authorization_note"`
	Rules             string   `json:"rules"`
	Status            string   `json:"status"`
	Report            string   `json:"report"`
	CreatedAt         int64    `json:"created_at"`
	UpdatedAt         int64    `json:"updated_at"`
}

func (s *Store) SaveEngagement(e *Engagement) error {
	now := Now()
	if e.Scope == nil {
		e.Scope = []string{}
	}
	if e.Exclusions == nil {
		e.Exclusions = []string{}
	}
	if e.Status == "" {
		e.Status = "active"
	}
	sc, _ := json.Marshal(e.Scope)
	ex, _ := json.Marshal(e.Exclusions)
	if e.ID == "" {
		e.ID = NewID("e_")
		e.CreatedAt = now
	}
	e.UpdatedAt = now
	_, err := s.DB.Exec(`INSERT INTO engagements(id,name,client,scope,exclusions,authorized,authorization_note,rules,status,report,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, client=excluded.client, scope=excluded.scope,
		exclusions=excluded.exclusions, authorized=excluded.authorized, authorization_note=excluded.authorization_note, rules=excluded.rules,
		status=excluded.status, report=excluded.report, updated_at=excluded.updated_at`,
		e.ID, e.Name, e.Client, string(sc), string(ex), b2i(e.Authorized), e.AuthorizationNote, e.Rules, e.Status, e.Report, e.CreatedAt, e.UpdatedAt)
	return err
}

func scanEngagement(sc interface{ Scan(...any) error }) (*Engagement, error) {
	var e Engagement
	var sc1, ex string
	var auth int
	if err := sc.Scan(&e.ID, &e.Name, &e.Client, &sc1, &ex, &auth, &e.AuthorizationNote, &e.Rules, &e.Status, &e.Report, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(sc1), &e.Scope)
	json.Unmarshal([]byte(ex), &e.Exclusions)
	e.Authorized = auth == 1
	return &e, nil
}

const engCols = `id,name,client,scope,exclusions,authorized,authorization_note,rules,status,report,created_at,updated_at`

func (s *Store) GetEngagement(id string) (*Engagement, error) {
	e, err := scanEngagement(s.DB.QueryRow(`SELECT `+engCols+` FROM engagements WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

func (s *Store) ListEngagements() ([]Engagement, error) {
	rows, err := s.DB.Query(`SELECT ` + engCols + ` FROM engagements ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Engagement
	for rows.Next() {
		e, err := scanEngagement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

func (s *Store) DeleteEngagement(id string) error {
	s.DB.Exec(`DELETE FROM findings WHERE engagement_id=?`, id)
	_, err := s.DB.Exec(`DELETE FROM engagements WHERE id=?`, id)
	return err
}

type Intel struct {
	ID           int64  `json:"id"`
	EngagementID string `json:"engagement_id"`
	Kind         string `json:"kind"`
	Value        string `json:"value"`
	Detail       string `json:"detail"`
	Source       string `json:"source"`
	CreatedAt    int64  `json:"created_at"`
}

func (s *Store) AddIntel(i *Intel) (bool, error) {
	res, err := s.DB.Exec(`INSERT INTO intel(engagement_id,kind,value,detail,source,created_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(engagement_id,kind,value) DO UPDATE SET detail=CASE WHEN excluded.detail<>'' THEN excluded.detail ELSE intel.detail END`,
		i.EngagementID, i.Kind, i.Value, i.Detail, i.Source, Now())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) ListIntel(engagementID, kind string) ([]Intel, error) {
	rows, err := s.DB.Query(`SELECT id,engagement_id,kind,value,detail,source,created_at FROM intel WHERE engagement_id=? AND (?='' OR kind=?) ORDER BY kind, value`, engagementID, kind, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Intel
	for rows.Next() {
		var i Intel
		rows.Scan(&i.ID, &i.EngagementID, &i.Kind, &i.Value, &i.Detail, &i.Source, &i.CreatedAt)
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) DeleteIntel(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM intel WHERE id=?`, id)
	return err
}

type Finding struct {
	ID           string  `json:"id"`
	EngagementID string  `json:"engagement_id"`
	SessionID    string  `json:"session_id"`
	Title        string  `json:"title"`
	Severity     string  `json:"severity"`
	CVSSVector   string  `json:"cvss_vector"`
	CVSSScore    float64 `json:"cvss_score"`
	CWE          string  `json:"cwe"`
	Category     string  `json:"category"`
	Target       string  `json:"target"`
	Description  string  `json:"description"`
	Reproduction string  `json:"reproduction"`
	Impact       string  `json:"impact"`
	Remediation  string  `json:"remediation"`
	Evidence     string  `json:"evidence"`
	Status       string  `json:"status"`
	CreatedAt    int64   `json:"created_at"`
	UpdatedAt    int64   `json:"updated_at"`
}

const findingCols = `id,engagement_id,session_id,title,severity,cvss_vector,cvss_score,cwe,category,target,description,reproduction,impact,remediation,evidence,status,created_at,updated_at`

func (s *Store) SaveFinding(f *Finding) error {
	now := Now()
	if f.ID == "" {
		f.ID = NewID("f_")
		f.CreatedAt = now
	}
	if f.Status == "" {
		f.Status = "open"
	}
	f.UpdatedAt = now
	_, err := s.DB.Exec(`INSERT INTO findings(`+findingCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET title=excluded.title, severity=excluded.severity, cvss_vector=excluded.cvss_vector, cvss_score=excluded.cvss_score,
		cwe=excluded.cwe, category=excluded.category, target=excluded.target, description=excluded.description, reproduction=excluded.reproduction,
		impact=excluded.impact, remediation=excluded.remediation, evidence=excluded.evidence, status=excluded.status, updated_at=excluded.updated_at`,
		f.ID, f.EngagementID, f.SessionID, f.Title, f.Severity, f.CVSSVector, f.CVSSScore, f.CWE, f.Category, f.Target, f.Description, f.Reproduction, f.Impact, f.Remediation, f.Evidence, f.Status, f.CreatedAt, f.UpdatedAt)
	return err
}

func (s *Store) ListFindings(engagementID string) ([]Finding, error) {
	rows, err := s.DB.Query(`SELECT `+findingCols+` FROM findings WHERE (?='' OR engagement_id=?) ORDER BY cvss_score DESC, created_at`, engagementID, engagementID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Finding
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.ID, &f.EngagementID, &f.SessionID, &f.Title, &f.Severity, &f.CVSSVector, &f.CVSSScore, &f.CWE, &f.Category, &f.Target, &f.Description, &f.Reproduction, &f.Impact, &f.Remediation, &f.Evidence, &f.Status, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) DeleteFinding(id string) error {
	_, err := s.DB.Exec(`DELETE FROM findings WHERE id=?`, id)
	return err
}

// ---------- Board ----------

type Task struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Status      string  `json:"status"`
	Priority    string  `json:"priority"`
	Assignee    string  `json:"assignee"`
	SessionID   string  `json:"session_id"`
	Position    float64 `json:"position"`
	CreatedAt   int64   `json:"created_at"`
	UpdatedAt   int64   `json:"updated_at"`
}

func (s *Store) SaveTask(t *Task) error {
	now := Now()
	if t.ID == "" {
		t.ID = NewID("t_")
		t.CreatedAt = now
		if t.Position == 0 {
			t.Position = float64(now)
		}
	}
	if t.Status == "" {
		t.Status = "todo"
	}
	if t.Priority == "" {
		t.Priority = "normal"
	}
	t.UpdatedAt = now
	_, err := s.DB.Exec(`INSERT INTO tasks(id,title,description,status,priority,assignee,session_id,position,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET title=excluded.title, description=excluded.description, status=excluded.status, priority=excluded.priority,
		assignee=excluded.assignee, session_id=excluded.session_id, position=excluded.position, updated_at=excluded.updated_at`,
		t.ID, t.Title, t.Description, t.Status, t.Priority, t.Assignee, t.SessionID, t.Position, t.CreatedAt, t.UpdatedAt)
	return err
}

func (s *Store) GetTask(id string) (*Task, error) {
	var t Task
	err := s.DB.QueryRow(`SELECT id,title,description,status,priority,assignee,session_id,position,created_at,updated_at FROM tasks WHERE id=?`, id).
		Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee, &t.SessionID, &t.Position, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

func (s *Store) ListTasks() ([]Task, error) {
	rows, err := s.DB.Query(`SELECT id,title,description,status,priority,assignee,session_id,position,created_at,updated_at FROM tasks ORDER BY position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		rows.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee, &t.SessionID, &t.Position, &t.CreatedAt, &t.UpdatedAt)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteTask(id string) error {
	_, err := s.DB.Exec(`DELETE FROM tasks WHERE id=?`, id)
	return err
}

// ---------- Cron ----------

type CronJob struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Schedule    string `json:"schedule"`
	Prompt      string `json:"prompt"`
	Role        string `json:"role"`
	Model       string `json:"model"`
	Enabled     bool   `json:"enabled"`
	AutoApprove bool   `json:"auto_approve"`
	LastRun     int64  `json:"last_run"`
	NextRun     int64  `json:"next_run"`
	LastStatus  string `json:"last_status"`
	LastSession string `json:"last_session"`
	CreatedAt   int64  `json:"created_at"`
}

func (s *Store) SaveCron(j *CronJob) error {
	if j.ID == "" {
		j.ID = NewID("c_")
		j.CreatedAt = Now()
	}
	_, err := s.DB.Exec(`INSERT INTO cron_jobs(id,name,schedule,prompt,role,model,enabled,auto_approve,last_run,next_run,last_status,last_session,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, schedule=excluded.schedule, prompt=excluded.prompt,
		role=excluded.role, model=excluded.model, enabled=excluded.enabled, auto_approve=excluded.auto_approve, last_run=excluded.last_run,
		next_run=excluded.next_run, last_status=excluded.last_status, last_session=excluded.last_session`,
		j.ID, j.Name, j.Schedule, j.Prompt, j.Role, j.Model, b2i(j.Enabled), b2i(j.AutoApprove), j.LastRun, j.NextRun, j.LastStatus, j.LastSession, j.CreatedAt)
	return err
}

func (s *Store) ListCron() ([]CronJob, error) {
	rows, err := s.DB.Query(`SELECT id,name,schedule,prompt,role,model,enabled,auto_approve,last_run,next_run,last_status,last_session,created_at FROM cron_jobs ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CronJob
	for rows.Next() {
		var j CronJob
		var en, aa int
		rows.Scan(&j.ID, &j.Name, &j.Schedule, &j.Prompt, &j.Role, &j.Model, &en, &aa, &j.LastRun, &j.NextRun, &j.LastStatus, &j.LastSession, &j.CreatedAt)
		j.Enabled, j.AutoApprove = en == 1, aa == 1
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) DeleteCron(id string) error {
	_, err := s.DB.Exec(`DELETE FROM cron_jobs WHERE id=?`, id)
	return err
}

// ---------- Snapshots (rollback) ----------

type Snapshot struct {
	Path      string `json:"path"`
	Existed   bool   `json:"existed"`
	Content   []byte `json:"-"`
	CreatedAt int64  `json:"created_at"`
}

// SnapshotOnce records the first pre-change state of path within a session.
func (s *Store) SnapshotOnce(sessionID, path string, existed bool, content []byte) {
	s.DB.Exec(`INSERT OR IGNORE INTO snapshots(session_id,path,existed,content,created_at) VALUES(?,?,?,?,?)`, sessionID, path, b2i(existed), content, Now())
}

func (s *Store) Snapshots(sessionID string) ([]Snapshot, error) {
	rows, err := s.DB.Query(`SELECT path,existed,content,created_at FROM snapshots WHERE session_id=? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		var sn Snapshot
		var ex int
		rows.Scan(&sn.Path, &ex, &sn.Content, &sn.CreatedAt)
		sn.Existed = ex == 1
		out = append(out, sn)
	}
	return out, rows.Err()
}

func (s *Store) DropSnapshot(sessionID, path string) {
	s.DB.Exec(`DELETE FROM snapshots WHERE session_id=? AND path=?`, sessionID, path)
}

// ---------- Servers (VPS) ----------

type Server struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	Auth      string `json:"auth"` // password | key
	Secret    string `json:"-"`    // encrypted
	HostKey   string `json:"host_key"`
	Notes     string `json:"notes"`
	CreatedAt int64  `json:"created_at"`
}

func (s *Store) SaveServer(v *Server) error {
	if v.ID == "" {
		v.ID = NewID("v_")
		v.CreatedAt = Now()
	}
	if v.Port == 0 {
		v.Port = 22
	}
	_, err := s.DB.Exec(`INSERT INTO servers(id,name,host,port,user,auth,secret,host_key,notes,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, host=excluded.host, port=excluded.port, user=excluded.user, auth=excluded.auth,
		secret=CASE WHEN excluded.secret<>'' THEN excluded.secret ELSE servers.secret END, host_key=excluded.host_key, notes=excluded.notes`,
		v.ID, v.Name, v.Host, v.Port, v.User, v.Auth, v.Secret, v.HostKey, v.Notes, v.CreatedAt)
	return err
}

func (s *Store) ListServers() ([]Server, error) {
	rows, err := s.DB.Query(`SELECT id,name,host,port,user,auth,secret,host_key,notes,created_at FROM servers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		var v Server
		rows.Scan(&v.ID, &v.Name, &v.Host, &v.Port, &v.User, &v.Auth, &v.Secret, &v.HostKey, &v.Notes, &v.CreatedAt)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) FindServer(nameOrID string) (*Server, error) {
	list, err := s.ListServers()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == nameOrID || strings.EqualFold(list[i].Name, nameOrID) {
			return &list[i], nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) DeleteServer(id string) error {
	_, err := s.DB.Exec(`DELETE FROM servers WHERE id=?`, id)
	return err
}

// ---------- Role performance ----------

type RoleStat struct {
	Role  string `json:"role"`
	Runs  int    `json:"runs"`
	OK    int    `json:"ok"`
	Fail  int    `json:"failed"`
	Turns int    `json:"turns"`
	Score int    `json:"score"`
}

func (s *Store) RecordRole(role string, ok bool, turns int) {
	okN, fail := 0, 1
	if ok {
		okN, fail = 1, 0
	}
	s.DB.Exec(`INSERT INTO role_stats(role,runs,ok,failed,turns,updated_at) VALUES(?,1,?,?,?,?)
		ON CONFLICT(role) DO UPDATE SET runs=runs+1, ok=ok+excluded.ok, failed=failed+excluded.failed, turns=turns+excluded.turns, updated_at=excluded.updated_at`,
		role, okN, fail, turns, Now())
}

func (s *Store) RoleStats() ([]RoleStat, error) {
	rows, err := s.DB.Query(`SELECT role,runs,ok,failed,turns FROM role_stats ORDER BY runs DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoleStat
	for rows.Next() {
		var r RoleStat
		rows.Scan(&r.Role, &r.Runs, &r.OK, &r.Fail, &r.Turns)
		// Laplace-smoothed success rate: an untried role starts neutral (50).
		r.Score = int(math.Round(100 * float64(r.OK+1) / float64(r.Runs+2)))
		out = append(out, r)
	}
	return out, rows.Err()
}
