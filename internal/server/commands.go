package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/allones/allones/internal/store"
	"github.com/allones/allones/internal/tools"
)

// runCommand answers a slash command without a model turn.
func (s *Server) runCommand(sessionID, cmd, arg string) string {
	cmd = strings.TrimPrefix(strings.TrimSpace(cmd), "/")
	arg = strings.TrimSpace(arg)
	c := s.cfg.Get()
	switch cmd {
	case "help":
		return "Commands: /status /model /toolset /role /goal /steer /stop /memory /remember /forget /findings /report /engagement /tools /skills /team /rollback /clear"
	case "status":
		st := s.store.Stats(context.Background())
		running := s.engine.IsRunning(sessionID)
		return fmt.Sprintf("model: %s\naux: %s\ntoolset: %s\napproval: %s\nsessions: %d  messages: %d  findings: %d\nrunning: %v",
			c.Model.Default, c.Model.Aux, c.Tools.Toolset, c.Tools.ApprovalMode, st["sessions"], st["messages"], st["findings"], running)
	case "model":
		if arg == "" {
			return "current model: " + c.Model.Default
		}
		if err := s.setSessionField(sessionID, "model", arg); err != nil {
			return err.Error()
		}
		return "model for this session set to " + arg
	case "toolset":
		if arg == "" {
			var names []string
			for k := range tools.Toolsets {
				names = append(names, k)
			}
			return "toolsets: " + strings.Join(names, ", ")
		}
		if _, ok := tools.Toolsets[arg]; !ok {
			return "unknown toolset " + arg
		}
		s.setSessionField(sessionID, "toolset", arg)
		return "toolset set to " + arg
	case "role":
		if arg == "" {
			return strings.Join(s.engine.Roles().Names(), "\n")
		}
		if _, ok := s.engine.Roles().Get(arg); !ok {
			return "unknown role " + arg
		}
		s.setSessionField(sessionID, "role", arg)
		return "role set to " + arg
	case "goal":
		return s.goalCommand(sessionID, arg)
	case "steer":
		if s.engine.Steer(sessionID, arg) {
			return "steering queued"
		}
		return "nothing is running to steer"
	case "stop":
		if s.engine.Stop(sessionID) {
			return "stopping"
		}
		return "nothing running"
	case "memory":
		list, _ := s.store.ListMemories(arg, 30)
		if len(list) == 0 {
			return "no memories"
		}
		var b strings.Builder
		for _, m := range list {
			k := m.ID
			if m.Key != "" {
				k = m.Key
			}
			fmt.Fprintf(&b, "- [%s] %s\n", k, m.Content)
		}
		return b.String()
	case "remember":
		if arg == "" {
			return "usage: /remember [key: ] fact"
		}
		m := &store.Memory{Content: arg}
		if k, v, ok := strings.Cut(arg, ":"); ok && !strings.Contains(k, " ") {
			m.Key, m.Content = strings.TrimSpace(k), strings.TrimSpace(v)
		}
		s.store.SaveMemory(m)
		return "remembered"
	case "forget":
		if ok, _ := s.store.DeleteMemory(arg); ok {
			return "forgotten"
		}
		return "nothing matched"
	case "findings":
		sess, _ := s.store.GetSession(sessionID)
		eng := ""
		if sess != nil {
			eng = sess.EngagementID
		}
		list, _ := s.store.ListFindings(eng)
		if len(list) == 0 {
			return "no findings"
		}
		var b strings.Builder
		for _, f := range list {
			fmt.Fprintf(&b, "- [%s %.1f] %s (%s)\n", f.Severity, f.CVSSScore, f.Title, f.Target)
		}
		return b.String()
	case "report":
		sess, _ := s.store.GetSession(sessionID)
		if sess == nil || sess.EngagementID == "" {
			return "no engagement attached to this session"
		}
		eng, err := s.store.GetEngagement(sess.EngagementID)
		if err != nil {
			return "engagement not found"
		}
		return tools.BuildReport(s.store, eng, "")
	case "engagement":
		sess, _ := s.store.GetSession(sessionID)
		if sess == nil || sess.EngagementID == "" {
			return "no engagement attached; create one in Security → Engagements"
		}
		eng, _ := s.store.GetEngagement(sess.EngagementID)
		return tools.MethodologyStatus(s.store, eng)
	case "tools":
		ts := s.engine.Registry().Select(c.Tools.Toolset, c.Tools.Enabled, c.Tools.Disabled)
		var names []string
		for _, t := range ts {
			names = append(names, t.Name)
		}
		return fmt.Sprintf("%d tools in '%s': %s", len(names), c.Tools.Toolset, strings.Join(names, ", "))
	case "skills":
		var b strings.Builder
		for _, sk := range s.engine.Skills().List() {
			fmt.Fprintf(&b, "- %s\n", sk.Name)
		}
		return b.String()
	case "team":
		stats, _ := s.store.RoleStats()
		if len(stats) == 0 {
			return "no delegation history yet"
		}
		var b strings.Builder
		for _, r := range stats {
			fmt.Fprintf(&b, "%-18s score %d  (%d runs, %d ok)\n", r.Role, r.Score, r.Runs, r.OK)
		}
		return b.String()
	case "rollback":
		snaps, _ := s.store.Snapshots(sessionID)
		if len(snaps) == 0 {
			return "no file changes in this session"
		}
		var b strings.Builder
		b.WriteString("changed files (use the rollback tool to restore):\n")
		for _, sn := range snaps {
			b.WriteString("- " + sn.Path + "\n")
		}
		return b.String()
	case "clear":
		return "(use New chat to start fresh; history is kept)"
	}
	return "unknown command /" + cmd + " — try /help"
}

func (s *Server) goalCommand(sessionID, arg string) string {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return "no session"
	}
	switch {
	case arg == "" || arg == "status":
		if sess.Goal == "" {
			return "no standing goal; set one with /goal <what you want done>"
		}
		return "goal: " + sess.Goal
	case arg == "clear":
		sess.Goal = ""
		s.store.UpdateSession(sess)
		return "goal cleared"
	default:
		sess.Goal = arg
		s.store.UpdateSession(sess)
		return "goal set: " + arg + "\nThe agent will work toward it and a judge will check completion each turn."
	}
}

func (s *Server) setSessionField(sessionID, field, value string) error {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("no session")
	}
	switch field {
	case "model":
		sess.Model = value
	case "toolset":
		sess.Toolset = value
	case "role":
		sess.Role = value
	}
	return s.store.UpdateSession(sess)
}
