package llm

import (
	"reflect"
	"testing"
)

// Gemini 400s when a `required` entry is not a defined property. cleanSchema must
// prune such entries — for the []string that builtin tools use and the []any that
// MCP (JSON-decoded) schemas carry — and drop `required` once nothing valid remains.
func TestCleanSchemaPrunesRequired(t *testing.T) {
	t.Run("string slice with a stray name", func(t *testing.T) {
		in := map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
			"required":   []string{"path", "ghost"},
		}
		out := cleanSchema(in).(map[string]any)
		if got := out["required"].([]any); !reflect.DeepEqual(got, []any{"path"}) {
			t.Fatalf("required = %v, want [path]", got)
		}
	})

	t.Run("json any slice, all stray -> dropped", func(t *testing.T) {
		in := map[string]any{
			"type":       "object",
			"properties": map[string]any{"q": map[string]any{"type": "string"}},
			"required":   []any{"missing"},
		}
		out := cleanSchema(in).(map[string]any)
		if _, ok := out["required"]; ok {
			t.Fatalf("required should be dropped, got %v", out["required"])
		}
	})

	t.Run("nested object required is pruned too", func(t *testing.T) {
		in := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filter": map[string]any{
					"type":       "object",
					"properties": map[string]any{"name": map[string]any{"type": "string"}},
					"required":   []any{"name", "nope"},
				},
			},
			"required": []any{"filter"},
		}
		out := cleanSchema(in).(map[string]any)
		nested := out["properties"].(map[string]any)["filter"].(map[string]any)
		if got := nested["required"].([]any); !reflect.DeepEqual(got, []any{"name"}) {
			t.Fatalf("nested required = %v, want [name]", got)
		}
	})
}
