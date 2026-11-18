import { useState } from "react";
import { api } from "../lib/api";
import { Wordmark } from "../components/Logo";
import { Field } from "../components/ui";
import { Icon } from "../components/Icon";

const PROVIDERS = [
  { name: "openrouter", kind: "openai-compatible", label: "OpenRouter", model: "anthropic/claude-sonnet-4.5", hint: "One key, every model. Recommended." },
  { name: "anthropic", kind: "anthropic", label: "Anthropic", model: "claude-sonnet-4-5", hint: "Claude, native (thinking + prompt caching)." },
  { name: "openai", kind: "openai", label: "OpenAI", model: "gpt-5", hint: "GPT models." },
  { name: "gemini", kind: "gemini", label: "Google Gemini", model: "gemini-2.5-pro", hint: "Gemini, native." },
  { name: "ollama", kind: "openai-compatible", label: "Ollama (local)", model: "llama3.1", hint: "Local models, no API key." },
];

export function SetupPage({ onDone }: { onDone: () => void }) {
  const [provider, setProvider] = useState(PROVIDERS[0]);
  const [apiKey, setApiKey] = useState("");
  const [model, setModel] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  async function submit() {
    setBusy(true);
    setErr("");
    try {
      const m = model || provider.name + "/" + provider.model;
      await api.post("/setup", {
        provider: {
          name: provider.name,
          kind: provider.kind,
          api_key: apiKey,
          models: [provider.model],
        },
        model: m,
        password: password || undefined,
      });
      onDone();
    } catch (e: any) {
      setErr(e.message || "setup failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="h-full grid place-items-center p-4 overflow-y-auto">
      <div className="w-full max-w-md py-8">
        <div className="flex items-center gap-2.5 mb-1">
          <Wordmark size={22} />
        </div>
        <p className="text-sm text-[var(--color-faint)] mb-6">
          Self-hosted AI agent with a sharp edge for security. Let's connect a model.
        </p>

        <div className="card p-5">
          <Field label="Provider">
            <div className="grid grid-cols-1 gap-2">
              {PROVIDERS.map((p) => (
                <button
                  key={p.name}
                  onClick={() => {
                    setProvider(p);
                    setModel("");
                  }}
                  className={
                    "text-left px-3 py-2.5 rounded-lg border transition-colors " +
                    (provider.name === p.name
                      ? "border-[var(--color-brand)] bg-[var(--color-brand-soft)]"
                      : "border-[var(--color-border)] hover:bg-[var(--color-surface-2)]")
                  }
                >
                  <div className="flex items-center justify-between">
                    <span className="font-medium text-sm">{p.label}</span>
                    {provider.name === p.name && <Icon name="check" className="w-4 h-4 text-[var(--color-brand)]" />}
                  </div>
                  <div className="text-xs text-[var(--color-faint)]">{p.hint}</div>
                </button>
              ))}
            </div>
          </Field>

          {provider.name !== "ollama" && (
            <Field label="API key" hint="Stored locally in ~/.allones/config.yaml. Never sent anywhere but your provider.">
              <input
                className="input"
                type="password"
                placeholder="sk-…"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
              />
            </Field>
          )}

          <Field label="Default model">
            <input
              className="input"
              placeholder={provider.name + "/" + provider.model}
              value={model}
              onChange={(e) => setModel(e.target.value)}
            />
          </Field>

          <Field label="Dashboard password (optional)" hint="Required if you expose AllOnes beyond localhost.">
            <input
              className="input"
              type="password"
              placeholder="leave blank for local-only use"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>

          {err && <div className="text-sm text-[var(--color-danger)] mb-3">{err}</div>}

          <button className="btn btn-brand w-full" disabled={busy} onClick={submit}>
            {busy ? "Saving…" : "Get started"}
          </button>
        </div>
      </div>
    </div>
  );
}
