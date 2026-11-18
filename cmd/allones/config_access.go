package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/allones/allones/internal/config"
)

// configGet/Set offer a small dotted-path accessor for the common keys.
func configGet(c *config.Config, key string) string {
	switch key {
	case "model.default":
		return c.Model.Default
	case "model.aux":
		return c.Model.Aux
	case "model.reasoning":
		return c.Model.Reasoning
	case "server.host":
		return c.Server.Host
	case "server.port":
		return strconv.Itoa(c.Server.Port)
	case "tools.toolset":
		return c.Tools.Toolset
	case "tools.approval_mode":
		return c.Tools.ApprovalMode
	case "tools.workspace":
		return c.Tools.Workspace
	case "security.scope_enforcement":
		return c.Security.ScopeEnforcement
	case "security.tools_path":
		return c.Security.ToolsPath
	case "soul.name":
		return c.Soul.Name
	case "agent.verify_replies":
		return strconv.FormatBool(c.Agent.VerifyReplies)
	}
	return "(unknown key)"
}

func configSet(c *config.Config, key, val string) error {
	switch key {
	case "model.default":
		c.Model.Default = val
	case "model.aux":
		c.Model.Aux = val
	case "model.reasoning":
		c.Model.Reasoning = val
	case "server.host":
		c.Server.Host = val
	case "server.port":
		n, err := strconv.Atoi(val)
		if err != nil {
			return err
		}
		c.Server.Port = n
	case "tools.toolset":
		c.Tools.Toolset = val
	case "tools.approval_mode":
		c.Tools.ApprovalMode = val
	case "tools.workspace":
		c.Tools.Workspace = val
	case "security.scope_enforcement":
		c.Security.ScopeEnforcement = val
	case "security.tools_path":
		c.Security.ToolsPath = val
	case "soul.name":
		c.Soul.Name = val
	case "agent.verify_replies":
		c.Agent.VerifyReplies = val == "true"
	default:
		return fmt.Errorf("unknown or read-only key %q (edit %s directly for advanced settings)", key, config.Path())
	}
	return nil
}

func setupCmd() {
	cfg := must(config.Load())
	in := bufio.NewReader(os.Stdin)
	ask := func(prompt, def string) string {
		fmt.Printf("%s", prompt)
		if def != "" {
			fmt.Printf(" [%s]", def)
		}
		fmt.Print(": ")
		line, _ := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return def
		}
		return line
	}
	fmt.Println("AllOnes setup")
	fmt.Println("Pick a provider: 1) OpenRouter  2) Anthropic  3) OpenAI  4) Gemini  5) Ollama (local)")
	choice := ask("Provider", "1")
	var p config.Provider
	switch choice {
	case "2":
		p = config.Provider{Name: "anthropic", Kind: "anthropic", Models: []string{"claude-sonnet-4-5"}}
	case "3":
		p = config.Provider{Name: "openai", Kind: "openai", Models: []string{"gpt-5"}}
	case "4":
		p = config.Provider{Name: "gemini", Kind: "gemini", Models: []string{"gemini-2.5-pro"}}
	case "5":
		p = config.Provider{Name: "ollama", Kind: "openai-compatible", Models: []string{"llama3.1"}}
	default:
		p = config.Provider{Name: "openrouter", Kind: "openai-compatible", Models: []string{"anthropic/claude-sonnet-4.5"}}
	}
	p.BaseURL = config.DefaultBaseURL(p.Kind, p.Name)
	if p.Name != "ollama" {
		p.APIKey = ask("API key", "")
	}
	model := ask("Default model", p.Name+"/"+p.Models[0])
	err := cfg.Update(func(c *config.Config) error {
		c.Provider = append([]config.Provider{p}, c.Provider...)
		c.Model.Default = model
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println("\nSaved to", config.Path())
	fmt.Println("Start with: allones")
}
