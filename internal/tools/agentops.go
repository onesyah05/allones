package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/allones/allones/internal/store"
)

type TodoItem struct {
	Text   string `json:"text"`
	Status string `json:"status"` // pending | in_progress | done | blocked
}

func registerAgentOps(r *Registry) {
	r.Add(&Tool{Name: "memory", Category: "memory",
		Description: "Durable facts about the user and their work that stay true (preferences, environments, conventions). action=save (content, optional key to overwrite) | search (query) | list | delete (key or id). Do not store transient state.",
		Schema:      Obj(map[string]any{"action": Enum("Operation.", "save", "search", "list", "delete"), "content": Str("Fact to remember."), "key": Str("Stable key, e.g. 'db'."), "query": Str("Search words."), "tags": Str("Comma-separated tags.")}, "action"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Action, Content, Key, Query, Tags string }
			Bind(raw, &a)
			switch a.Action {
			case "save":
				if strings.TrimSpace(a.Content) == "" {
					return Errorf("content is empty")
				}
				m := &store.Memory{Key: a.Key, Content: a.Content, Tags: a.Tags}
				if err := env.Store.SaveMemory(m); err != nil {
					return Errorf("%v", err)
				}
				return Text("remembered (" + m.ID + ")")
			case "delete":
				ok, err := env.Store.DeleteMemory(firstNonEmpty(a.Key, a.Query, a.Content))
				if err != nil || !ok {
					return Errorf("nothing deleted")
				}
				return Text("forgotten")
			default:
				list, err := env.Store.ListMemories(a.Query, 50)
				if err != nil {
					return Errorf("%v", err)
				}
				if len(list) == 0 {
					return Text("no memories")
				}
				var b strings.Builder
				for _, m := range list {
					k := m.ID
					if m.Key != "" {
						k = m.Key
					}
					fmt.Fprintf(&b, "- [%s] %s\n", k, m.Content)
				}
				return Text(b.String())
			}
		}})

	r.Add(&Tool{Name: "session_search", Category: "memory",
		Description: "Full-text search across past conversations. Use to recall what was decided or done before.",
		Schema:      Obj(map[string]any{"query": Str("Words to search."), "limit": Int("Max hits.")}, "query"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Query string
				Limit int
			}
			Bind(raw, &a)
			hits, err := env.Store.SearchMessages(a.Query, a.Limit)
			if err != nil {
				return Errorf("%v", err)
			}
			if len(hits) == 0 {
				return Text("no matches")
			}
			var b strings.Builder
			for _, h := range hits {
				fmt.Fprintf(&b, "- session %s (%s): %s\n", h.SessionID, h.Title, h.Snippet)
			}
			return Text(b.String())
		}})

	r.Add(&Tool{Name: "rag_search", Category: "memory",
		Description: "Semantic + keyword (hybrid) search over indexed documents. Use before answering questions about the user's docs or codebases that were indexed.",
		Schema:      Obj(map[string]any{"query": Str("What to look for."), "collection": Str("Collection name (empty = all)."), "k": Int("Results, default 8.")}, "query"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Query, Collection string
				K                 int
			}
			Bind(raw, &a)
			if env.Services.RAG == nil {
				return Errorf("RAG is disabled")
			}
			chunks, err := env.Services.RAG.Search(ctx, a.Collection, a.Query, a.K)
			if err != nil {
				return Errorf("%v", err)
			}
			if len(chunks) == 0 {
				return Text("no results")
			}
			var b strings.Builder
			for i, c := range chunks {
				fmt.Fprintf(&b, "### %d. %s#%d (%s, score %.3f)\n%s\n\n", i+1, c.Source, c.Index, c.Collection, c.Score, c.Content)
			}
			return External("rag", Truncate(b.String(), env.Cfg.Tools.MaxOutputChars))
		}})

	r.Add(&Tool{Name: "rag_index", Category: "memory", Mutates: true,
		Description: "Index a file or directory (text, code, markdown, PDF text) into a RAG collection for later rag_search, or index raw text with source+text.",
		Schema:      Obj(map[string]any{"path": Str("File or directory in the workspace."), "collection": Str("Collection name, default 'default'."), "source": Str("Source label for raw text."), "text": Str("Raw text to index.")}),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Path, Collection, Source, Text string }
			Bind(raw, &a)
			if env.Services.RAG == nil {
				return Errorf("RAG is disabled")
			}
			if a.Collection == "" {
				a.Collection = "default"
			}
			if a.Text != "" {
				n, err := env.Services.RAG.IndexText(ctx, a.Collection, firstNonEmpty(a.Source, "note"), a.Text)
				if err != nil {
					return Errorf("%v", err)
				}
				return Text(fmt.Sprintf("indexed %d chunks into %s", n, a.Collection))
			}
			p, err := env.Resolve(a.Path)
			if err != nil {
				return Errorf("%v", err)
			}
			files, chunks, err := env.Services.RAG.IndexPath(ctx, a.Collection, p)
			if err != nil {
				return Errorf("%v", err)
			}
			return Text(fmt.Sprintf("indexed %d files (%d chunks) into collection %q", files, chunks, a.Collection))
		}})

	r.Add(&Tool{Name: "todo", Category: "agent",
		Description: "Keep a visible task list for this session. action=set replaces the list (items with text+status), action=get shows it. Update statuses as you work; mark items done only when verified.",
		Schema: Obj(map[string]any{"action": Enum("Operation.", "set", "get"),
			"items": Arr("Task items.", Obj(map[string]any{"text": Str("Task."), "status": Enum("State.", "pending", "in_progress", "done", "blocked")}, "text"))}, "action"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Action string
				Items  []TodoItem
			}
			Bind(raw, &a)
			key := "todo:" + env.SessionID
			if a.Action == "set" {
				for i := range a.Items {
					if a.Items[i].Status == "" {
						a.Items[i].Status = "pending"
					}
				}
				env.Store.SetJSON(key, a.Items)
			} else {
				env.Store.GetJSON(key, &a.Items)
			}
			if len(a.Items) == 0 {
				return Text("(no tasks)")
			}
			var b strings.Builder
			done := 0
			for _, it := range a.Items {
				mark := map[string]string{"done": "[x]", "in_progress": "[~]", "blocked": "[!]"}[it.Status]
				if mark == "" {
					mark = "[ ]"
				}
				if it.Status == "done" {
					done++
				}
				fmt.Fprintf(&b, "%s %s\n", mark, it.Text)
			}
			fmt.Fprintf(&b, "(%d/%d done)", done, len(a.Items))
			return Result{Text: b.String(), Meta: map[string]any{"todo": a.Items}}
		}})

	r.Add(&Tool{Name: "skill", Category: "agent",
		Description: "The skill library: written procedures (SKILL.md). action=list | search (query) | read (name, optional file for bundled references) | save (name, content with YAML front matter). Read a relevant skill BEFORE starting specialised work (security testing, reverse engineering, deployment…).",
		Schema:      Obj(map[string]any{"action": Enum("Operation.", "list", "search", "read", "save"), "name": Str("Skill name."), "query": Str("Search words."), "file": Str("Bundled file path inside the skill, e.g. references/x.md."), "content": Str("Full SKILL.md for save.")}, "action"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Action, Name, Query, File, Content string }
			Bind(raw, &a)
			lib := env.Services.Skills
			if lib == nil {
				return Errorf("skills disabled")
			}
			switch a.Action {
			case "read":
				var s string
				var err error
				if a.File != "" {
					s, err = lib.ReadFile(a.Name, a.File)
				} else {
					s, err = lib.Read(a.Name)
				}
				if err != nil {
					return Errorf("%v", err)
				}
				return Text(Truncate(s, 60000))
			case "save":
				p, err := lib.Save(a.Name, a.Content)
				if err != nil {
					return Errorf("%v", err)
				}
				return Text("saved skill to " + p)
			case "search":
				return skillList(lib.Search(firstNonEmpty(a.Query, a.Name), 25))
			default:
				return skillList(lib.List())
			}
		}})

	r.Add(&Tool{Name: "list_roles", Category: "agent",
		Description: "List specialist roles available to delegate_task.",
		Schema:      Obj(map[string]any{}),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			if env.Services.Delegator == nil {
				return Errorf("delegation unavailable")
			}
			return Text(strings.Join(env.Services.Delegator.RoleNames(), "\n"))
		}})

	r.Add(&Tool{Name: "delegate_task", Category: "agent",
		Description: "Hand a self-contained subtask to an isolated sub-agent, optionally as a specialist role (see list_roles). The sub-agent cannot see this conversation: the prompt must stand alone with all context, paths and the expected output. Returns only its final answer. Several delegate_task calls in one turn run in parallel.",
		Schema:      Obj(map[string]any{"prompt": Str("Complete, standalone instructions."), "role": Str("Specialist role name (optional)."), "isolate": Bool("Run in a separate git worktree (repositories only).")}, "prompt"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Prompt, Role string
				Isolate      bool
			}
			Bind(raw, &a)
			if env.Services.Delegator == nil {
				return Errorf("delegation unavailable")
			}
			if env.Depth >= env.Cfg.Agent.SubagentDepth && env.Cfg.Agent.SubagentDepth > 0 {
				return Errorf("sub-agent depth limit reached (%d); do this work yourself", env.Cfg.Agent.SubagentDepth)
			}
			out, err := env.Services.Delegator.Delegate(ctx, env, a.Role, a.Prompt, a.Isolate)
			if err != nil {
				return Errorf("sub-agent failed: %v\n%s", err, out)
			}
			return Text(out)
		}})

	r.Add(&Tool{Name: "ask_user", Category: "agent",
		Description: "Ask the operator a question and wait for the answer. Use only for decisions only they can make (authorization, ambiguous requirements, missing secrets). Not available to sub-agents or unattended runs.",
		Schema:      Obj(map[string]any{"question": Str("The question."), "options": StrArr("Optional choices.")}, "question"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Question string
				Options  []string
			}
			Bind(raw, &a)
			if !env.Interactive || env.Services.Asker == nil {
				return Errorf("no operator is available in this run; make a reasonable assumption, state it, and continue")
			}
			ans, err := env.Services.Asker.Ask(ctx, env, a.Question, a.Options)
			if err != nil {
				return Errorf("%v", err)
			}
			return Text("operator answered: " + ans)
		}})

	r.Add(&Tool{Name: "schedule", Category: "ops", Mutates: true,
		Description: "Schedule an unattended agent run. action=add (name, schedule as 5-field cron or @every 1h/@daily, prompt, optional role) | list | remove (id) | preview (schedule → next run times).",
		Schema:      Obj(map[string]any{"action": Enum("Operation.", "add", "list", "remove", "preview"), "name": Str("Job name."), "schedule": Str("Cron expression."), "prompt": Str("What the agent should do."), "role": Str("Role to run as."), "id": Str("Job id.")}, "action"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Action, Name, Schedule, Prompt, Role, ID string }
			Bind(raw, &a)
			sch := env.Services.Scheduler
			switch a.Action {
			case "add", "preview":
				if sch == nil {
					return Errorf("scheduler unavailable")
				}
				runs, err := sch.NextRuns(a.Schedule, 3)
				if err != nil {
					return Errorf("bad schedule: %v", err)
				}
				var ts []string
				for _, r := range runs {
					ts = append(ts, time.UnixMilli(r).Format("2006-01-02 15:04 MST"))
				}
				if a.Action == "preview" {
					return Text("next runs: " + strings.Join(ts, ", "))
				}
				j := &store.CronJob{Name: firstNonEmpty(a.Name, clip(a.Prompt, 40)), Schedule: a.Schedule, Prompt: a.Prompt, Role: a.Role, Enabled: true, NextRun: runs[0]}
				if err := env.Store.SaveCron(j); err != nil {
					return Errorf("%v", err)
				}
				return Text(fmt.Sprintf("scheduled %s (%s); next runs: %s", j.ID, j.Name, strings.Join(ts, ", ")))
			case "remove":
				if err := env.Store.DeleteCron(a.ID); err != nil {
					return Errorf("%v", err)
				}
				return Text("removed " + a.ID)
			default:
				jobs, _ := env.Store.ListCron()
				if len(jobs) == 0 {
					return Text("no scheduled jobs")
				}
				var b strings.Builder
				for _, j := range jobs {
					fmt.Fprintf(&b, "%s  %-20s %-15s enabled=%v last=%s\n", j.ID, j.Name, j.Schedule, j.Enabled, j.LastStatus)
				}
				return Text(b.String())
			}
		}})

	r.Add(&Tool{Name: "board", Category: "ops", Mutates: true,
		Description: "The shared Kanban work board. action=list | add (title, description, priority, assignee) | move (id, status: todo|doing|review|done) | update (id, fields) | delete (id).",
		Schema: Obj(map[string]any{"action": Enum("Operation.", "list", "add", "move", "update", "delete"), "id": Str("Task id."), "title": Str("Title."),
			"description": Str("Details."), "status": Enum("Column.", "todo", "doing", "review", "done"), "priority": Enum("Priority.", "low", "normal", "high", "urgent"), "assignee": Str("Role or person.")}, "action"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Action, ID, Title, Description, Status, Priority, Assignee string }
			Bind(raw, &a)
			switch a.Action {
			case "add":
				t := &store.Task{Title: a.Title, Description: a.Description, Status: a.Status, Priority: a.Priority, Assignee: a.Assignee, SessionID: env.SessionID}
				if err := env.Store.SaveTask(t); err != nil {
					return Errorf("%v", err)
				}
				return Text("added " + t.ID)
			case "move", "update":
				t, err := env.Store.GetTask(a.ID)
				if err != nil {
					return Errorf("no task %s", a.ID)
				}
				if a.Status != "" {
					t.Status = a.Status
				}
				if a.Title != "" {
					t.Title = a.Title
				}
				if a.Description != "" {
					t.Description = a.Description
				}
				if a.Priority != "" {
					t.Priority = a.Priority
				}
				if a.Assignee != "" {
					t.Assignee = a.Assignee
				}
				env.Store.SaveTask(t)
				return Text("updated " + t.ID)
			case "delete":
				env.Store.DeleteTask(a.ID)
				return Text("deleted " + a.ID)
			default:
				list, _ := env.Store.ListTasks()
				if len(list) == 0 {
					return Text("board is empty")
				}
				var b strings.Builder
				for _, t := range list {
					fmt.Fprintf(&b, "%s [%s] (%s) %s %s\n", t.ID, t.Status, t.Priority, t.Title, firstNonEmpty("@"+t.Assignee, ""))
				}
				return Text(b.String())
			}
		}})

	r.Add(&Tool{Name: "notify", Category: "ops",
		Description: "Send a notification to the operator (dashboard + configured gateways). Use when an unattended job finishes or needs attention.",
		Schema:      Obj(map[string]any{"title": Str("Short title."), "body": Str("Message.")}, "title"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Title, Body string }
			Bind(raw, &a)
			if env.Services.Notify != nil {
				env.Services.Notify(a.Title, a.Body)
			}
			env.Store.Audit("agent:"+env.SessionID, "notify", a.Title, a.Body, "info")
			return Text("notified")
		}})
}

func skillList(list []SkillInfo) Result {
	if len(list) == 0 {
		return Text("no skills found")
	}
	var b strings.Builder
	for _, s := range list {
		if s.Disabled {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, clip(s.Description, 220))
	}
	return Text(b.String())
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" && s != "@" {
			return s
		}
	}
	return ""
}
