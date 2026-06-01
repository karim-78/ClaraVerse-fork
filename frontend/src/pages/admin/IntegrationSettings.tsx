/**
 * IntegrationSettings — admin page for managing integration
 * credentials (Composio + E2B + future additions) from the UI
 * instead of requiring shell access + docker restart.
 *
 * Pulls the list of supported integrations from the backend
 * (/api/admin/integration-settings), grouped by provider, and
 * renders one masked input per setting with a single Save button
 * that batches all the changes.
 *
 * Per-row state:
 *   - source="db"   → set via this UI, can be cleared
 *   - source="env"  → set via env var; can be overridden in the
 *                     UI (which then takes precedence)
 *   - source=""     → not set anywhere; integration won't work
 *
 * On save the backend invalidates its in-memory cache so the new
 * values take effect on the next tool call (no restart).
 */
import { useCallback, useEffect, useMemo, useState } from 'react';
import { Save, Eye, EyeOff, Loader2, CheckCircle2, AlertCircle, ExternalLink, BookOpen, Box } from 'lucide-react';
import { api } from '@/services/api';
import { toast } from '@/store/useToastStore';

interface IntegrationSetting {
  key: string;
  env_key: string;
  label: string;
  description: string;
  group: 'composio' | 'e2b' | string;
  is_primary: boolean;
  is_set: boolean;
  source: 'db' | 'env' | '';
  masked_value: string;
}

interface ListResponse {
  settings: IntegrationSetting[];
}

const GROUP_META: Record<string, { title: string; icon: typeof Box; helpURL: string; help: string }> = {
  composio: {
    title: 'Composio',
    icon: BookOpen,
    helpURL: 'https://app.composio.dev',
    help: 'Set the master API key first, then add Auth Config IDs for each integration you want enabled. Each integration needs its own OAuth app created in the Composio dashboard.',
  },
  e2b: {
    title: 'E2B Code Interpreter',
    icon: Box,
    helpURL: 'https://e2b.dev',
    help: 'Sandbox API key. Required for the Python runner tool and any agent that executes code. Free tier available.',
  },
};

export const IntegrationSettings = () => {
  const [settings, setSettings] = useState<IntegrationSetting[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  // pending: key → new value the admin is typing. Only keys with
  // a string here get sent on save; untouched rows are skipped.
  const [pending, setPending] = useState<Record<string, string>>({});
  // shown: key → whether the user has clicked the eye to reveal
  // the field while typing. The MASKED value from the backend is
  // never revealed (the raw secret isn't sent to the UI on GET).
  const [shown, setShown] = useState<Record<string, boolean>>({});

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const r = await api.get<ListResponse>('/api/admin/integration-settings');
      setSettings(r.settings ?? []);
      // Reset pending state on reload so saved values don't keep
      // showing as "unsaved" in the input field.
      setPending({});
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : 'Failed to load integrations', 'Error');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const groups = useMemo(() => {
    const m: Record<string, IntegrationSetting[]> = {};
    for (const s of settings) {
      (m[s.group] ?? (m[s.group] = [])).push(s);
    }
    return m;
  }, [settings]);

  const handleSave = useCallback(async () => {
    if (Object.keys(pending).length === 0) {
      toast.warning('Nothing to save', 'No changes');
      return;
    }
    setSaving(true);
    try {
      await api.put('/api/admin/integration-settings', { updates: pending });
      toast.success(
        `${Object.keys(pending).length} integration setting(s) saved`,
        'Saved'
      );
      await load();
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : 'Save failed', 'Error');
    } finally {
      setSaving(false);
    }
  }, [pending, load]);

  if (loading) {
    return (
      <div className="space-y-6">
        <h1 className="text-3xl font-bold text-[var(--color-text-primary)]">Integrations</h1>
        <div className="flex items-center gap-2 text-[var(--color-text-secondary)]">
          <Loader2 size={16} className="animate-spin" />
          Loading…
        </div>
      </div>
    );
  }

  const dirtyCount = Object.keys(pending).length;

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-3xl font-bold text-[var(--color-text-primary)]">Integrations</h1>
          <p className="text-[var(--color-text-secondary)] mt-2">
            Configure third-party services. Changes take effect immediately — no restart.
          </p>
        </div>
        <button
          type="button"
          onClick={() => void handleSave()}
          disabled={saving || dirtyCount === 0}
          className="flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {saving ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />}
          {saving ? 'Saving…' : dirtyCount > 0 ? `Save (${dirtyCount})` : 'Save'}
        </button>
      </div>

      {Object.entries(groups).map(([group, rows]) => {
        const meta = GROUP_META[group] ?? { title: group, icon: Box, helpURL: '', help: '' };
        const Icon = meta.icon;
        const primary = rows.find(r => r.is_primary);
        const others = rows.filter(r => !r.is_primary);
        const primaryConfigured = primary?.is_set ?? false;

        return (
          <div
            key={group}
            className="rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] overflow-hidden"
          >
            {/* Group header */}
            <div className="flex items-start justify-between p-5 border-b border-[var(--color-border)]">
              <div className="flex items-start gap-3">
                <div className="p-2 rounded-lg bg-[var(--color-accent-light)] text-[var(--color-accent)]">
                  <Icon size={20} />
                </div>
                <div>
                  <h2 className="text-lg font-semibold text-[var(--color-text-primary)]">
                    {meta.title}
                  </h2>
                  <p className="text-sm text-[var(--color-text-tertiary)] mt-1 max-w-2xl">
                    {meta.help}
                  </p>
                </div>
              </div>
              {meta.helpURL && (
                <a
                  href={meta.helpURL}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="flex items-center gap-1 text-sm text-[var(--color-accent)] hover:underline shrink-0"
                >
                  Dashboard <ExternalLink size={12} />
                </a>
              )}
            </div>

            <div className="divide-y divide-[var(--color-border)]">
              {/* Primary key row (rendered first, always) */}
              {primary && (
                <SettingRow
                  setting={primary}
                  value={pending[primary.key]}
                  show={shown[primary.key] ?? false}
                  onChange={v => setPending(p => ({ ...p, [primary.key]: v }))}
                  onToggleShow={() => setShown(s => ({ ...s, [primary.key]: !s[primary.key] }))}
                />
              )}

              {/* Other keys — greyed when primary isn't set yet */}
              {others.map(r => (
                <div key={r.key} className={primaryConfigured ? '' : 'opacity-40 pointer-events-none'}>
                  <SettingRow
                    setting={r}
                    value={pending[r.key]}
                    show={shown[r.key] ?? false}
                    onChange={v => setPending(p => ({ ...p, [r.key]: v }))}
                    onToggleShow={() => setShown(s => ({ ...s, [r.key]: !s[r.key] }))}
                  />
                </div>
              ))}

              {!primaryConfigured && others.length > 0 && (
                <div className="p-3 text-xs text-[var(--color-text-tertiary)] bg-[var(--color-surface)] flex items-center gap-2">
                  <AlertCircle size={12} />
                  Set the {meta.title} API key first to enable individual integrations.
                </div>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
};

function SettingRow({
  setting,
  value,
  show,
  onChange,
  onToggleShow,
}: {
  setting: IntegrationSetting;
  value: string | undefined;
  show: boolean;
  onChange: (v: string) => void;
  onToggleShow: () => void;
}) {
  const hasPending = value !== undefined;
  const displayValue =
    hasPending && (show || value === '')
      ? value
      : hasPending
        ? value!.replace(/./g, '•')
        : setting.masked_value;

  return (
    <div className="p-4 flex items-start gap-4">
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 flex-wrap">
          <label className="text-sm font-medium text-[var(--color-text-primary)]">
            {setting.label}
          </label>
          {setting.is_set && setting.source === 'db' && (
            <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium bg-[var(--color-success-light)] text-[var(--color-success)]">
              <CheckCircle2 size={10} />
              Set
            </span>
          )}
          {setting.is_set && setting.source === 'env' && (
            <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium bg-[var(--color-warning-light)] text-[var(--color-warning)]">
              From env: {setting.env_key}
            </span>
          )}
          {!setting.is_set && (
            <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-[var(--color-surface)] text-[var(--color-text-tertiary)]">
              Not set
            </span>
          )}
        </div>
        <p className="text-xs text-[var(--color-text-tertiary)] mt-1">{setting.description}</p>
      </div>
      <div className="flex items-center gap-2 w-72 shrink-0">
        <div className="relative flex-1">
          <input
            type={show ? 'text' : 'password'}
            value={hasPending ? value : ''}
            placeholder={setting.is_set ? setting.masked_value : 'paste key…'}
            onChange={e => onChange(e.target.value)}
            className="w-full px-3 py-1.5 pr-9 rounded-md bg-[var(--color-surface-elevated)] text-sm text-[var(--color-text-primary)] border border-[var(--color-border)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50 font-mono"
          />
          {hasPending && (
            <button
              type="button"
              onClick={onToggleShow}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-[var(--color-text-tertiary)] hover:text-[var(--color-text-primary)]"
              title={show ? 'Hide' : 'Show'}
            >
              {show ? <EyeOff size={14} /> : <Eye size={14} />}
            </button>
          )}
        </div>
      </div>
      <div className="text-[10px] text-[var(--color-text-tertiary)] w-32 truncate" title={displayValue}>
        {/* visual placeholder so layout stays stable; the actual editable input is above */}
      </div>
    </div>
  );
}

export default IntegrationSettings;
