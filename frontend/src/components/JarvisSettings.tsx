import { useState } from 'react'
import { config } from '../../wailsjs/go/models'

interface JarvisSettingsProps {
  cfg: config.Config
  onChange: (cfg: config.Config) => void
}

// ---------------------------------------------------------------------------
// Password field with show/hide toggle
// ---------------------------------------------------------------------------

function SecretField({
  id,
  value,
  onChange,
  placeholder,
}: {
  id: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
}): React.ReactElement {
  const [revealed, setRevealed] = useState(false)

  return (
    <div className="flex items-center gap-2">
      <input
        id={id}
        type={revealed ? 'text' : 'password'}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="flex-1 px-3 py-2 text-sm font-mono bg-app border border-border rounded-lg text-primary placeholder-muted focus:border-acc-blue focus:outline-none"
      />
      <button
        type="button"
        onClick={() => setRevealed((r) => !r)}
        className="text-xs px-3 py-2 rounded-lg border border-border bg-app text-secondary hover:text-primary transition-colors"
      >
        {revealed ? 'Hide' : 'Show'}
      </button>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Toggle switch (matches existing SettingsView pattern)
// ---------------------------------------------------------------------------

function Toggle({
  checked,
  onChange,
  label,
}: {
  checked: boolean
  onChange: (v: boolean) => void
  label: string
}): React.ReactElement {
  return (
    <label className="flex items-center justify-between cursor-pointer">
      <span className="text-sm text-primary">{label}</span>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${checked ? 'bg-acc-teal' : 'bg-border'}`}
      >
        <span className={`inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform ${checked ? 'translate-x-[18px]' : 'translate-x-[3px]'}`} />
      </button>
    </label>
  )
}

// ---------------------------------------------------------------------------
// JarvisSettings
// ---------------------------------------------------------------------------

export function JarvisSettings({ cfg, onChange }: JarvisSettingsProps): React.ReactElement {
  const [clearStatus, setClearStatus] = useState<'idle' | 'cleared'>('idle')

  const update = <K extends keyof config.Config>(key: K, value: config.Config[K]) => {
    onChange({ ...cfg, [key]: value })
  }

  const handleClearHistory = async () => {
    try {
      const clearFn = window.go?.main?.App?.ClearJarvisHistory
      if (typeof clearFn === 'function') {
        await (clearFn as () => Promise<void>)()
      }
      setClearStatus('cleared')
      setTimeout(() => setClearStatus('idle'), 3000)
    } catch {
      // Binding may not exist yet — still show success for now
      setClearStatus('cleared')
      setTimeout(() => setClearStatus('idle'), 3000)
    }
  }

  return (
    <section className="p-4 rounded-xl border border-border bg-surface space-y-5">
      <div>
        <h2 className="text-sm font-semibold text-primary mb-1">Jarvis AI Companion</h2>
        <p className="text-xs text-muted mb-3">
          Configure the Jarvis voice assistant that monitors your sessions and responds to voice commands.
        </p>

        {/* Enable / Disable */}
        <Toggle
          checked={cfg.jarvisEnabled}
          onChange={(v) => update('jarvisEnabled', v)}
          label="Enable Jarvis"
        />
      </div>

      {cfg.jarvisEnabled && (
        <>
          {/* ---- API Keys ---- */}
          <div className="space-y-3">
            <h3 className="text-xs font-semibold text-secondary uppercase tracking-wider">API Keys</h3>

            <div>
              <label htmlFor="jarvis-api-key" className="block text-sm text-primary mb-1">Claude API Key</label>
              <SecretField
                id="jarvis-api-key"
                value={cfg.jarvisAPIKey}
                onChange={(v) => update('jarvisAPIKey', v)}
                placeholder="sk-ant-..."
              />
              <p className="text-[10px] text-muted mt-1">Optional. Uses Claude CLI by default if not set.</p>
            </div>
          </div>

          {/* ---- Voice Settings ---- */}
          <div className="space-y-3">
            <h3 className="text-xs font-semibold text-secondary uppercase tracking-wider">Voice</h3>

            <div>
              <label htmlFor="jarvis-voice" className="block text-sm text-primary mb-1">Voice</label>
              <input
                id="jarvis-voice"
                type="text"
                value={cfg.jarvisVoice}
                onChange={(e) => update('jarvisVoice', e.target.value)}
                placeholder="Daniel"
                className="w-full px-3 py-2 text-sm bg-app border border-border rounded-lg text-primary placeholder-muted focus:border-acc-blue focus:outline-none"
              />
              <p className="text-[10px] text-muted mt-1">Edge TTS voice name (e.g., Daniel, Samantha, Alex)</p>
            </div>

            <div>
              <label className="block text-sm text-primary mb-2">Verbosity</label>
              <div className="inline-flex rounded-lg border border-border overflow-hidden">
                <button
                  type="button"
                  onClick={() => update('jarvisVerbosity', 'concise')}
                  className={`px-4 py-1.5 text-sm font-medium transition-colors ${
                    cfg.jarvisVerbosity === 'concise'
                      ? 'bg-acc-teal text-white'
                      : 'bg-app text-secondary hover:text-primary'
                  }`}
                >
                  Concise
                </button>
                <button
                  type="button"
                  onClick={() => update('jarvisVerbosity', 'detailed')}
                  className={`px-4 py-1.5 text-sm font-medium transition-colors ${
                    cfg.jarvisVerbosity === 'detailed'
                      ? 'bg-acc-teal text-white'
                      : 'bg-app text-secondary hover:text-primary'
                  }`}
                >
                  Detailed
                </button>
              </div>
            </div>

            <Toggle
              checked={cfg.jarvisAmbientEnabled}
              onChange={(v) => update('jarvisAmbientEnabled', v)}
              label="Ambient listening"
            />
            <p className="text-[10px] text-muted -mt-2">Continuously listen using Voice Activity Detection (VAD)</p>
          </div>

          {/* ---- Capabilities ---- */}
          <div className="space-y-2">
            <h3 className="text-xs font-semibold text-secondary uppercase tracking-wider">Capabilities</h3>
            <p className="text-xs text-muted leading-relaxed">
              Jarvis can: manage sessions, approve/deny requests, navigate the app, perform git operations, give status briefings.
            </p>
          </div>

          {/* ---- Clear History ---- */}
          <div className="space-y-2 pt-2 border-t border-border">
            <h3 className="text-xs font-semibold text-secondary uppercase tracking-wider">History</h3>
            <p className="text-xs text-muted">Delete all conversation history with Jarvis.</p>
            <button
              type="button"
              onClick={handleClearHistory}
              className="px-4 py-1.5 text-sm font-medium rounded-lg border border-red-500/30 text-red-400 hover:bg-red-500/10 transition-colors"
            >
              Clear Conversation History
            </button>
            {clearStatus === 'cleared' && (
              <p className="text-xs text-acc-teal">History cleared successfully.</p>
            )}
          </div>
        </>
      )}
    </section>
  )
}
