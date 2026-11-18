package server

import (
	"net/http"
	"os"
	"strings"

	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/providers"
	"github.com/allones/allones/internal/subscription"
)

type providerView struct {
	providers.Info
	Connected bool     `json:"connected"`
	Disabled  bool     `json:"disabled"`
	HasKey    bool     `json:"has_key"`
	FromEnv   bool     `json:"from_env"`
	Custom    bool     `json:"custom"`
	Enabled   []string `json:"enabled_models"`
}

// handleProviderCatalog merges the well-known catalogue with what is configured.
func (s *Server) handleProviderCatalog(w http.ResponseWriter, r *http.Request) {
	c := s.cfg.Get()
	byName := map[string]*config.Provider{}
	for i := range c.Provider {
		byName[c.Provider[i].Name] = &c.Provider[i]
	}
	var out []providerView
	seen := map[string]bool{}
	for _, info := range providers.Catalog() {
		v := providerView{Info: info, Enabled: []string{}}
		if p := byName[info.ID]; p != nil {
			v.Connected, v.Disabled, v.FromEnv = true, p.Disabled, p.FromEnv
			v.HasKey = p.APIKey != ""
			v.Enabled = append(v.Enabled, p.Models...)
			if p.BaseURL != "" {
				v.BaseURL = p.BaseURL
			}
		} else if info.KeyEnv != "" && os.Getenv(info.KeyEnv) != "" {
			v.HasKey = true
		}
		seen[info.ID] = true
		out = append(out, v)
	}
	for _, p := range c.Provider {
		if seen[p.Name] {
			continue
		}
		out = append(out, providerView{
			Info:      providers.Info{ID: p.Name, Label: firstNonEmptyStr(p.Label, p.Name), Kind: p.Kind, BaseURL: p.BaseURL, NeedsKey: true, Group: "custom", Models: []string{}},
			Connected: true, Disabled: p.Disabled, HasKey: p.APIKey != "", FromEnv: p.FromEnv, Custom: true, Enabled: append([]string{}, p.Models...),
		})
	}
	writeJSON(w, 200, map[string]any{"providers": out, "default": c.Model.Default, "aux": c.Model.Aux})
}

type providerPatch struct {
	Label    *string   `json:"label"`
	Kind     *string   `json:"kind"`
	BaseURL  *string   `json:"base_url"`
	APIKey   *string   `json:"api_key"`
	Models   *[]string `json:"models"`
	Disabled *bool     `json:"disabled"`
}

// handleProviderSave connects a catalogue provider, creates a custom one, or
// updates an existing one. A blank api_key keeps the stored key.
func (s *Server) handleProviderSave(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" || strings.ContainsAny(name, "/ \t") {
		writeErr(w, 400, "provider id must be a single word without slashes")
		return
	}
	var patch providerPatch
	if err := readJSON(r, &patch); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	err := s.cfg.Update(func(c *config.Config) error {
		idx := -1
		for i := range c.Provider {
			if c.Provider[i].Name == name {
				idx = i
			}
		}
		if idx < 0 {
			p := config.Provider{Name: name}
			if info, ok := providers.For(name); ok {
				p.Kind, p.BaseURL, p.Label = info.Kind, info.BaseURL, info.Label
				p.Models = append([]string{}, info.Models...)
				if info.KeyEnv != "" {
					p.APIKey = os.Getenv(info.KeyEnv)
				}
			}
			c.Provider = append(c.Provider, p)
			idx = len(c.Provider) - 1
		}
		p := &c.Provider[idx]
		p.FromEnv = false // an edited provider is persisted
		if patch.Label != nil {
			p.Label = *patch.Label
		}
		if patch.Kind != nil && *patch.Kind != "" {
			p.Kind = *patch.Kind
		}
		if patch.BaseURL != nil && *patch.BaseURL != "" {
			p.BaseURL = strings.TrimSpace(*patch.BaseURL)
		}
		if patch.APIKey != nil && *patch.APIKey != "" {
			p.APIKey = strings.TrimSpace(*patch.APIKey)
		}
		if patch.Models != nil {
			p.Models = cleanList(*patch.Models)
		}
		if patch.Disabled != nil {
			p.Disabled = *patch.Disabled
		}
		if c.Model.Default == "" && len(p.Models) > 0 {
			c.Model.Default = p.Name + "/" + p.Models[0]
		}
		return nil
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.store.Audit("operator", "provider.save", name, "", "info")
	s.handleProviderCatalog(w, r)
}

func (s *Server) handleProviderDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.cfg.Update(func(c *config.Config) error {
		var keep []config.Provider
		for _, p := range c.Provider {
			if p.Name != name {
				keep = append(keep, p)
			}
		}
		c.Provider = keep
		if strings.HasPrefix(c.Model.Default, name+"/") {
			c.Model.Default = ""
		}
		if strings.HasPrefix(c.Model.Aux, name+"/") {
			c.Model.Aux = ""
		}
		return nil
	})
	s.store.Audit("operator", "provider.delete", name, "", "info")
	s.handleProviderCatalog(w, r)
}

func (s *Server) findProvider(name string) *config.Provider {
	c := s.cfg.Get()
	for i := range c.Provider {
		if c.Provider[i].Name == name {
			return &c.Provider[i]
		}
	}
	return nil
}

// handleProviderModels fetches the live model list from the provider.
func (s *Server) handleProviderModels(w http.ResponseWriter, r *http.Request) {
	p := s.findProvider(r.PathValue("name"))
	if p == nil {
		writeErr(w, 404, "connect the provider first")
		return
	}
	if p.OAuth {
		fresh, err := subscription.Ready(r.Context(), s.cfg, p.Name)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		p = fresh
	}
	models, err := providers.ListModels(r.Context(), p)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"models": models})
}

func (s *Server) handleProviderTest(w http.ResponseWriter, r *http.Request) {
	p := s.findProvider(r.PathValue("name"))
	if p == nil {
		writeErr(w, 404, "connect the provider first")
		return
	}
	var b struct {
		Model string `json:"model"`
	}
	readJSON(r, &b)
	if b.Model == "" && len(p.Models) > 0 {
		b.Model = p.Models[0]
	}
	if b.Model == "" {
		writeErr(w, 400, "enable at least one model to test")
		return
	}
	if p.OAuth {
		fresh, err := subscription.Ready(r.Context(), s.cfg, p.Name)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		p = fresh
	}
	reply, took, err := providers.Test(r.Context(), p, b.Model)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error(), "model": b.Model})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "reply": reply, "ms": took.Milliseconds(), "model": b.Model})
}

// handleModelDefaults sets the default and aux model.
func (s *Server) handleModelDefaults(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Default *string `json:"default"`
		Aux     *string `json:"aux"`
	}
	readJSON(r, &b)
	s.cfg.Update(func(c *config.Config) error {
		if b.Default != nil {
			c.Model.Default = *b.Default
		}
		if b.Aux != nil {
			c.Model.Aux = *b.Aux
		}
		return nil
	})
	s.handleModels(w, r)
}

func cleanList(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range in {
		x = strings.TrimSpace(x)
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
