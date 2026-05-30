import { useCallback, useEffect, useState } from 'react';
import {
  Archive,
  ArchiveRestore,
  Brain,
  Calendar,
  Filter,
  Loader2,
  RefreshCw,
  Tag,
  Trash2,
} from 'lucide-react';
import memoryService, { type Memory } from '@/services/memoryService';

// ============================================================================
// MemoryList — user-facing view of what's been remembered.
//
// Lists every active memory the system has stored for the current user,
// grouped by category, with delete + archive controls. Trust signal: the
// user can audit and correct what the model "knows" about them at any
// time. Backed by the existing /api/memories CRUD that the backend
// already exposed; previously the only consumer was the
// SearchByEmbedding code path.
//
// Design notes:
//  • Categories are colour-coded so users can scan at a glance.
//  • Delete is hard-delete (matches the backend behaviour) — we ask for
//    confirm inline rather than via a modal to keep the flow light.
//  • Archive is a softer alternative: hidden from selection but kept on
//    disk in case the user wants to restore later. We surface both.
//  • Refresh and includeArchived are simple state, no react-query.

const CATEGORY_META: Record<
  Memory['category'],
  { label: string; color: string }
> = {
  personal_info: { label: 'Personal Info', color: '#8a5cff' },
  preferences:   { label: 'Preferences',   color: '#10b981' },
  context:       { label: 'Context',       color: '#3b82f6' },
  fact:          { label: 'Fact',          color: '#f59e0b' },
  instruction:   { label: 'Instruction',   color: '#ef4444' },
};

const fmtDate = (iso: string | null) => {
  if (!iso) return 'never';
  const d = new Date(iso);
  const days = Math.floor((Date.now() - d.getTime()) / 86_400_000);
  if (days === 0) return 'today';
  if (days === 1) return 'yesterday';
  if (days < 30) return `${days}d ago`;
  return d.toLocaleDateString();
};

export const MemoryList: React.FC = () => {
  const [memories, setMemories] = useState<Memory[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [categoryFilter, setCategoryFilter] = useState<string>('');
  const [includeArchived, setIncludeArchived] = useState(false);
  const [confirmingDeleteId, setConfirmingDeleteId] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await memoryService.listMemories({
        category: categoryFilter || undefined,
        includeArchived,
        pageSize: 200,
      });
      setMemories(res.memories);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load memories');
    } finally {
      setLoading(false);
    }
  }, [categoryFilter, includeArchived]);

  useEffect(() => {
    load();
  }, [load]);

  const handleDelete = async (id: string) => {
    setBusyId(id);
    try {
      await memoryService.deleteMemory(id);
      setMemories(prev => prev.filter(m => m.id !== id));
      setConfirmingDeleteId(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Delete failed');
    } finally {
      setBusyId(null);
    }
  };

  const handleArchive = async (m: Memory) => {
    setBusyId(m.id);
    try {
      if (m.is_archived) {
        await memoryService.unarchiveMemory(m.id);
      } else {
        await memoryService.archiveMemory(m.id);
      }
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Archive failed');
    } finally {
      setBusyId(null);
    }
  };

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-start justify-between gap-3 flex-wrap">
        <div>
          <h3 className="text-lg font-semibold flex items-center gap-2">
            <Brain className="w-5 h-5" />
            Saved memories
          </h3>
          <p className="text-sm text-[var(--color-text-secondary)] mt-1">
            Facts the AI has stored about you. You can delete any entry — it'll be removed
            immediately and won't surface in future chats.
          </p>
        </div>
        <button
          onClick={load}
          disabled={loading}
          className="flex items-center gap-2 px-3 py-2 text-xs rounded-md border border-[var(--color-border)] hover:bg-[var(--color-surface-hover)] disabled:opacity-50"
        >
          <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
          Refresh
        </button>
      </div>

      {/* Filters */}
      <div
        className="p-3 rounded-lg border border-[var(--color-border)] flex items-center gap-3 flex-wrap"
        style={{ background: 'var(--color-surface)' }}
      >
        <Filter className="w-4 h-4 text-[var(--color-text-tertiary)]" />
        <select
          value={categoryFilter}
          onChange={e => setCategoryFilter(e.target.value)}
          className="text-xs px-2 py-1 rounded border border-[var(--color-border)] bg-transparent"
        >
          <option value="">All categories</option>
          {Object.entries(CATEGORY_META).map(([key, meta]) => (
            <option key={key} value={key}>
              {meta.label}
            </option>
          ))}
        </select>
        <label className="text-xs flex items-center gap-1.5 text-[var(--color-text-secondary)]">
          <input
            type="checkbox"
            checked={includeArchived}
            onChange={e => setIncludeArchived(e.target.checked)}
          />
          Include archived
        </label>
        <span className="ml-auto text-xs text-[var(--color-text-tertiary)]">
          {memories.length} entr{memories.length === 1 ? 'y' : 'ies'}
        </span>
      </div>

      {error && (
        <div className="p-3 rounded-md border border-[var(--color-error-border)] bg-[var(--color-error-light)] text-sm text-[var(--color-error)]">
          {error}
        </div>
      )}

      {/* List */}
      <div className="space-y-2">
        {loading && memories.length === 0 && (
          <div className="flex items-center justify-center py-12 text-[var(--color-text-secondary)] gap-2">
            <Loader2 className="w-4 h-4 animate-spin" />
            Loading…
          </div>
        )}
        {!loading && memories.length === 0 && !error && (
          <div className="text-center py-12 text-[var(--color-text-secondary)] text-sm">
            No memories yet. Mention preferences, instructions, or facts in chat and the AI will
            save them automatically (or call <code>add_memory</code> explicitly).
          </div>
        )}
        {memories.map(m => {
          const meta = CATEGORY_META[m.category] || CATEGORY_META.fact;
          const confirming = confirmingDeleteId === m.id;
          const busy = busyId === m.id;
          return (
            <div
              key={m.id}
              className="p-3 rounded-lg border"
              style={{
                borderColor: m.is_archived ? 'var(--color-border)' : 'var(--color-border)',
                background: 'var(--color-surface)',
                opacity: m.is_archived ? 0.6 : 1,
              }}
            >
              <div className="flex items-start gap-3">
                {/* Category pill */}
                <span
                  className="text-[10px] uppercase tracking-wide px-2 py-0.5 rounded-full flex-shrink-0"
                  style={{
                    background: meta.color + '22',
                    color: meta.color,
                    border: '1px solid ' + meta.color + '55',
                  }}
                >
                  {meta.label}
                </span>

                {/* Content + meta */}
                <div className="flex-1 min-w-0">
                  <div className="text-sm break-words">{m.content}</div>
                  <div className="mt-2 flex items-center gap-3 text-xs text-[var(--color-text-tertiary)] flex-wrap">
                    <span className="flex items-center gap-1">
                      <Calendar className="w-3 h-3" />
                      saved {fmtDate(m.created_at)}
                    </span>
                    {m.last_accessed_at && (
                      <span>used {fmtDate(m.last_accessed_at)}</span>
                    )}
                    {m.access_count > 0 && (
                      <span>{m.access_count} use{m.access_count === 1 ? '' : 's'}</span>
                    )}
                    {m.tags && m.tags.length > 0 && (
                      <span className="flex items-center gap-1">
                        <Tag className="w-3 h-3" />
                        {m.tags.join(', ')}
                      </span>
                    )}
                    {m.is_archived && (
                      <span className="text-[var(--color-warning)]">archived</span>
                    )}
                  </div>
                </div>

                {/* Actions */}
                <div className="flex items-center gap-1 flex-shrink-0">
                  <button
                    onClick={() => handleArchive(m)}
                    disabled={busy}
                    title={m.is_archived ? 'Unarchive' : 'Archive (hide from selection but keep)'}
                    className="p-1.5 rounded hover:bg-[var(--color-surface-hover)] text-[var(--color-text-secondary)] disabled:opacity-50"
                  >
                    {m.is_archived ? (
                      <ArchiveRestore className="w-3.5 h-3.5" />
                    ) : (
                      <Archive className="w-3.5 h-3.5" />
                    )}
                  </button>
                  {confirming ? (
                    <>
                      <button
                        onClick={() => handleDelete(m.id)}
                        disabled={busy}
                        className="px-2 py-1 text-xs rounded bg-[var(--color-error)] text-white hover:opacity-90 disabled:opacity-50"
                      >
                        Confirm
                      </button>
                      <button
                        onClick={() => setConfirmingDeleteId(null)}
                        disabled={busy}
                        className="px-2 py-1 text-xs rounded border border-[var(--color-border)] hover:bg-[var(--color-surface-hover)]"
                      >
                        Cancel
                      </button>
                    </>
                  ) : (
                    <button
                      onClick={() => setConfirmingDeleteId(m.id)}
                      disabled={busy}
                      title="Delete this memory"
                      className="p-1.5 rounded hover:bg-[var(--color-error-light)] text-[var(--color-text-secondary)] hover:text-[var(--color-error)] disabled:opacity-50"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </button>
                  )}
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
};

export default MemoryList;
