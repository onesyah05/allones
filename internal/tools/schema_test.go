package tools

import "testing"

// Every object schema's `required` must list only properties it defines. Gemini
// rejects the request outright (HTTP 400) when it does not, and the others
// silently ignore the stray name — so this guards all providers at the source.
func TestToolRequiredMatchesProperties(t *testing.T) {
	for _, tool := range Default().All() {
		checkSchema(t, tool.Name, tool.Schema)
	}
}

func checkSchema(t *testing.T, path string, schema map[string]any) {
	t.Helper()
	props, _ := schema["properties"].(map[string]any)
	for _, name := range requiredNames(schema["required"]) {
		if _, ok := props[name]; !ok {
			t.Errorf("%s: required %q is not a defined property", path, name)
		}
	}
	for key, val := range props {
		if sub, ok := val.(map[string]any); ok {
			checkSchema(t, path+"."+key, sub)
		}
	}
}

func requiredNames(v any) []string {
	switch r := v.(type) {
	case []string:
		return r
	case []any:
		var out []string
		for _, x := range r {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
