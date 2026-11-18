// Package rag provides hybrid retrieval: dense (embeddings) + lexical (FTS5),
// fused by reciprocal rank. Embeddings are optional; without them it is pure BM25.
package rag

import (
	"context"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/llm"
	"github.com/allones/allones/internal/store"
)

type RAG struct {
	store *store.Store
	cfg   *config.Manager
}

func New(st *store.Store, cfg *config.Manager) *RAG { return &RAG{store: st, cfg: cfg} }

func (r *RAG) embedder() (base, key, model string, ok bool) {
	c := r.cfg.Get()
	if !c.RAG.Enabled || c.RAG.EmbedProvider == "" {
		return
	}
	for i := range c.Provider {
		if strings.EqualFold(c.Provider[i].Name, c.RAG.EmbedProvider) {
			p := c.Provider[i]
			m := c.RAG.EmbedModel
			if m == "" {
				m = "text-embedding-3-small"
			}
			return p.BaseURL, p.APIKey, m, p.APIKey != "" || strings.Contains(p.BaseURL, "11434") || strings.Contains(p.BaseURL, "1234")
		}
	}
	return
}

func (r *RAG) Search(ctx context.Context, collection, query string, k int) ([]store.Chunk, error) {
	if k <= 0 {
		k = r.cfg.Get().RAG.TopK
	}
	if k <= 0 {
		k = 8
	}
	recall := k * 5
	lex, err := r.store.LexicalChunks(collection, query, recall)
	if err != nil {
		return nil, err
	}
	ranks := map[int64]float64{}
	meta := map[int64]store.Chunk{}
	for i, c := range lex {
		ranks[c.ID] += 1.0 / float64(60+i+1)
		meta[c.ID] = c
	}
	// Dense side, if embeddings are configured.
	if base, key, model, ok := r.embedder(); ok {
		if vecs, err := llm.Embed(ctx, base, key, model, []string{query}); err == nil && len(vecs) == 1 {
			dense, err := r.store.VectorChunks(collection)
			if err == nil && len(dense) > 0 {
				q := vecs[0]
				type sc struct {
					c store.Chunk
					s float64
				}
				var scored []sc
				for _, c := range dense {
					scored = append(scored, sc{c, cosine(q, c.Embedding)})
				}
				sort.Slice(scored, func(i, j int) bool { return scored[i].s > scored[j].s })
				for i, s := range scored {
					if i >= recall {
						break
					}
					ranks[s.c.ID] += 1.0 / float64(60+i+1)
					if _, seen := meta[s.c.ID]; !seen {
						meta[s.c.ID] = s.c
					}
				}
			}
		}
	}
	var out []store.Chunk
	for id, score := range ranks {
		c := meta[id]
		c.Score = score
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}

func (r *RAG) IndexText(ctx context.Context, collection, source, text string) (int, error) {
	c := r.cfg.Get()
	chunks := chunk(text, c.RAG.ChunkSize, c.RAG.ChunkOverlap)
	recs := make([]store.Chunk, len(chunks))
	for i, ch := range chunks {
		recs[i] = store.Chunk{Collection: collection, Source: source, Index: i, Content: ch}
	}
	r.embedChunks(ctx, recs)
	if err := r.store.ReplaceSource(collection, source, recs); err != nil {
		return 0, err
	}
	return len(recs), nil
}

func (r *RAG) IndexPath(ctx context.Context, collection, path string) (int, int, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	var files []string
	if st.IsDir() {
		filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				n := d.Name()
				if n == ".git" || n == "node_modules" || n == "dist" || n == "build" || n == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if indexable(p) {
				files = append(files, p)
			}
			return nil
		})
	} else {
		files = []string{path}
	}
	total := 0
	for _, f := range files {
		if ctx.Err() != nil {
			break
		}
		b, err := os.ReadFile(f)
		if err != nil || len(b) == 0 {
			continue
		}
		text := string(b)
		if !isText(b) {
			continue
		}
		rel := f
		n, err := r.IndexText(ctx, collection, rel, text)
		if err != nil {
			return len(files), total, err
		}
		total += n
	}
	return len(files), total, nil
}

func (r *RAG) embedChunks(ctx context.Context, recs []store.Chunk) {
	base, key, model, ok := r.embedder()
	if !ok {
		return
	}
	const batch = 64
	for i := 0; i < len(recs); i += batch {
		end := min(i+batch, len(recs))
		inputs := make([]string, end-i)
		for j := i; j < end; j++ {
			inputs[j-i] = recs[j].Content
		}
		vecs, err := llm.Embed(ctx, base, key, model, inputs)
		if err != nil {
			return
		}
		for j := range vecs {
			if i+j < len(recs) {
				recs[i+j].Embedding = vecs[j]
			}
		}
	}
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// chunk splits text on paragraph/line boundaries into ~size-char windows with overlap.
func chunk(text string, size, overlap int) []string {
	if size <= 0 {
		size = 1200
	}
	if overlap < 0 || overlap >= size {
		overlap = size / 8
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	paras := strings.Split(text, "\n\n")
	var out []string
	var cur strings.Builder
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for _, p := range paras {
		if cur.Len()+len(p) > size && cur.Len() > 0 {
			tail := cur.String()
			flush()
			if overlap > 0 && len(tail) > overlap {
				cur.WriteString(tail[len(tail)-overlap:])
				cur.WriteString("\n\n")
			}
		}
		if len(p) > size {
			for len(p) > 0 {
				n := min(size, len(p))
				cur.WriteString(p[:n])
				flush()
				p = p[n:]
			}
			continue
		}
		cur.WriteString(p)
		cur.WriteString("\n\n")
	}
	flush()
	return out
}

var textExts = map[string]bool{".md": true, ".txt": true, ".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true, ".java": true, ".c": true, ".h": true, ".cpp": true, ".cs": true, ".rb": true, ".php": true, ".rs": true, ".sh": true, ".yaml": true, ".yml": true, ".json": true, ".toml": true, ".html": true, ".css": true, ".sql": true, ".xml": true, ".rst": true, ".conf": true, ".ini": true, ".env": true, ".tf": true, ".proto": true}

func indexable(p string) bool {
	st, err := os.Stat(p)
	if err != nil || st.Size() > 2<<20 {
		return false
	}
	return textExts[strings.ToLower(filepath.Ext(p))]
}

func isText(b []byte) bool {
	n := min(len(b), 2000)
	for i := 0; i < n; i++ {
		if b[i] == 0 {
			return false
		}
	}
	return true
}

var _ = fmt.Sprint
