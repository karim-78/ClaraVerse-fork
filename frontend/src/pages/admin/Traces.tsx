import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Activity,
  AlertCircle,
  Check,
  ChevronRight,
  Clock,
  Database,
  RefreshCw,
  X,
} from 'lucide-react';
import { api } from '@/services/api';

// ============================================================================
// Built-in workflow trace viewer.
//
// Two views in one component:
//   1. List of recent traces (window selector + status filter)
//   2. Detail panel: waterfall of all spans in a single trace
//
// Data comes from /api/admin/traces (mongo-backed; backend writes every
// span via MongoSpanExporter). No external Tempo / Jaeger required.

interface TraceListItem {
  trace_id: string;
  name: string;
  service: string;
  start_time: string;
  duration_ms: number;
  status_code: string;
  status_desc?: string;
  execution_id?: string;
  workflow_id?: string;
  user_id?: string;
  block_count?: number;
}

interface SpanItem {
  trace_id: string;
  span_id: string;
  parent_span_id: string;
  name: string;
  service: string;
  kind: string;
  start_time: string;
  end_time: string;
  duration_ms: number;
  status_code: string;
  status_desc?: string;
  attributes?: Record<string, unknown>;
}

interface TraceListResponse {
  traces: TraceListItem[];
  window_sec: number;
  count: number;
}

interface TraceDetailResponse {
  trace_id: string;
  span_count: number;
  trace_start: string;
  trace_end: string;
  trace_duration_ms: number;
  spans: SpanItem[];
  cost_usd?: number;
  cost_partial?: boolean;
  input_tokens?: number;
  output_tokens?: number;
  llm_blocks?: number;
}

interface StatsResponse {
  window_minutes: number;
  total: number;
  errors: number;
  error_rate: number;
  avg_ms: number;
  max_ms: number;
}

const WINDOW_OPTIONS = [
  { label: '15m', value: '15m' },
  { label: '1h', value: '1h' },
  { label: '24h', value: '24h' },
  { label: '7d', value: '168h' },
];

const STATUS_FILTERS = [
  { label: 'All', value: '' },
  { label: 'OK', value: 'ok' },
  { label: 'Errors', value: 'error' },
];

const fmtDuration = (ms: number) => {
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
};

const fmtTimeAgo = (iso: string) => {
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`;
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
  return `${Math.floor(diff / 86_400_000)}d ago`;
};

const fmtAbsoluteTime = (iso: string) => {
  const d = new Date(iso);
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
};

export const Traces = () => {
  const [traces, setTraces] = useState<TraceListItem[]>([]);
  const [stats, setStats] = useState<StatsResponse | null>(null);
  const [windowVal, setWindowVal] = useState('1h');
  const [statusFilter, setStatusFilter] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selectedTraceID, setSelectedTraceID] = useState<string | null>(null);

  const loadList = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const qs = new URLSearchParams();
      qs.set('limit', '100');
      qs.set('since', windowVal);
      if (statusFilter) qs.set('status', statusFilter);
      const [list, st] = await Promise.all([
        api.get<TraceListResponse>(`/api/admin/traces?${qs.toString()}`),
        api.get<StatsResponse>('/api/admin/traces/stats'),
      ]);
      setTraces(list.traces);
      setStats(st);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load traces');
    } finally {
      setLoading(false);
    }
  }, [windowVal, statusFilter]);

  useEffect(() => {
    loadList();
  }, [loadList]);

  // Auto-refresh every 30s when nothing is selected. Keeps the table
  // current without spamming the API when an engineer is reading a trace.
  useEffect(() => {
    if (selectedTraceID) return;
    const t = setInterval(loadList, 30_000);
    return () => clearInterval(t);
  }, [selectedTraceID, loadList]);

  return (
    <div className="p-6 max-w-[1400px] mx-auto">
      {/* Header */}
      <div className="flex items-start justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <Activity className="w-6 h-6" />
            Workflow Traces
          </h1>
          <p className="text-sm text-[var(--color-text-secondary)] mt-1">
            Every workflow execution is captured as a distributed trace. Click any row to see the
            per-block waterfall.
          </p>
        </div>
        <button
          onClick={loadList}
          disabled={loading}
          className="flex items-center gap-2 px-3 py-2 text-sm rounded-md border border-[var(--color-border)] hover:bg-[var(--color-surface-hover)] disabled:opacity-50"
        >
          <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
          Refresh
        </button>
      </div>

      {/* Stats row */}
      {stats && <StatsBar stats={stats} />}

      {/* Filters */}
      <div className="flex items-center gap-2 mb-4 flex-wrap">
        <span className="text-xs text-[var(--color-text-tertiary)]">Window:</span>
        {WINDOW_OPTIONS.map(opt => (
          <FilterChip
            key={opt.value}
            label={opt.label}
            active={windowVal === opt.value}
            onClick={() => setWindowVal(opt.value)}
          />
        ))}
        <span className="text-xs text-[var(--color-text-tertiary)] ml-4">Status:</span>
        {STATUS_FILTERS.map(opt => (
          <FilterChip
            key={opt.value || 'all'}
            label={opt.label}
            active={statusFilter === opt.value}
            onClick={() => setStatusFilter(opt.value)}
          />
        ))}
      </div>

      {error && (
        <div className="p-3 mb-4 rounded-md border border-[var(--color-error-border)] bg-[var(--color-error-light)] text-sm text-[var(--color-error)] flex items-center gap-2">
          <AlertCircle className="w-4 h-4" />
          {error}
        </div>
      )}

      {/* Trace table */}
      <div className="rounded-lg border border-[var(--color-border)] overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-[var(--color-surface-hover)] text-xs uppercase text-[var(--color-text-tertiary)]">
            <tr>
              <th className="text-left px-4 py-2">Time</th>
              <th className="text-left px-4 py-2">Workflow</th>
              <th className="text-left px-4 py-2">Execution</th>
              <th className="text-right px-4 py-2">Blocks</th>
              <th className="text-right px-4 py-2">Duration</th>
              <th className="text-left px-4 py-2">Status</th>
            </tr>
          </thead>
          <tbody>
            {traces.length === 0 && !loading && (
              <tr>
                <td colSpan={6} className="text-center py-12 text-[var(--color-text-secondary)]">
                  No traces in the selected window. Run any workflow to see one appear here.
                </td>
              </tr>
            )}
            {traces.map(t => (
              <tr
                key={t.trace_id}
                onClick={() => setSelectedTraceID(t.trace_id)}
                className="border-t border-[var(--color-border)] hover:bg-[var(--color-surface-hover)] cursor-pointer transition-colors"
              >
                <td className="px-4 py-3 text-[var(--color-text-secondary)]">
                  <div title={t.start_time}>{fmtTimeAgo(t.start_time)}</div>
                </td>
                <td className="px-4 py-3 font-mono text-xs">{t.workflow_id || '—'}</td>
                <td className="px-4 py-3 font-mono text-xs">
                  {t.execution_id ? t.execution_id.slice(0, 18) + (t.execution_id.length > 18 ? '…' : '') : '—'}
                </td>
                <td className="px-4 py-3 text-right">{t.block_count ?? '—'}</td>
                <td className="px-4 py-3 text-right font-variant-numeric tabular-nums">
                  {fmtDuration(t.duration_ms)}
                </td>
                <td className="px-4 py-3">
                  <StatusPill code={t.status_code} desc={t.status_desc} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {selectedTraceID && (
        <TraceDetailModal
          traceID={selectedTraceID}
          onClose={() => setSelectedTraceID(null)}
        />
      )}
    </div>
  );
};

// ─── pieces ──────────────────────────────────────────────────────────────

function StatsBar({ stats }: { stats: StatsResponse }) {
  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-3 mb-4">
      <StatCard
        icon={<Activity className="w-4 h-4" />}
        label="Last hour"
        value={String(stats.total)}
        sub="executions"
      />
      <StatCard
        icon={<AlertCircle className="w-4 h-4" />}
        label="Error rate"
        value={`${(stats.error_rate * 100).toFixed(1)}%`}
        sub={`${stats.errors} failed`}
        accent={stats.error_rate > 0.05 ? 'error' : undefined}
      />
      <StatCard
        icon={<Clock className="w-4 h-4" />}
        label="Avg duration"
        value={fmtDuration(stats.avg_ms)}
      />
      <StatCard
        icon={<Database className="w-4 h-4" />}
        label="Max duration"
        value={fmtDuration(stats.max_ms)}
      />
    </div>
  );
}

function StatCard({
  icon,
  label,
  value,
  sub,
  accent,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  sub?: string;
  accent?: 'error';
}) {
  return (
    <div
      className="p-3 rounded-lg border border-[var(--color-border)]"
      style={{
        background:
          accent === 'error'
            ? 'var(--color-error-light)'
            : 'var(--color-surface)',
      }}
    >
      <div className="flex items-center gap-2 text-xs text-[var(--color-text-tertiary)]">
        {icon}
        {label}
      </div>
      <div
        className="mt-1 text-xl font-semibold"
        style={{ color: accent === 'error' ? 'var(--color-error)' : 'var(--color-text-primary)' }}
      >
        {value}
      </div>
      {sub && <div className="text-xs text-[var(--color-text-tertiary)] mt-1">{sub}</div>}
    </div>
  );
}

function FilterChip({
  label,
  active,
  onClick,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className="px-3 py-1 text-xs rounded-full border transition-colors"
      style={{
        background: active ? 'var(--color-accent)' : 'transparent',
        color: active ? '#fff' : 'var(--color-text-secondary)',
        borderColor: active ? 'var(--color-accent)' : 'var(--color-border)',
      }}
    >
      {label}
    </button>
  );
}

function StatusPill({ code, desc }: { code: string; desc?: string }) {
  const isError = code === 'Error';
  return (
    <span
      title={desc || code}
      className="inline-flex items-center gap-1 px-2 py-0.5 text-xs rounded-full"
      style={{
        background: isError ? 'var(--color-error-light)' : 'var(--color-success-light)',
        color: isError ? 'var(--color-error)' : 'var(--color-success)',
      }}
    >
      {isError ? <X className="w-3 h-3" /> : <Check className="w-3 h-3" />}
      {code}
    </span>
  );
}

// ─── trace detail modal w/ waterfall ─────────────────────────────────────

function TraceDetailModal({ traceID, onClose }: { traceID: string; onClose: () => void }) {
  const [detail, setDetail] = useState<TraceDetailResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedSpanID, setSelectedSpanID] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    api
      .get<TraceDetailResponse>(`/api/admin/traces/${traceID}`)
      .then(d => {
        if (!cancelled) {
          setDetail(d);
          // Auto-select the root span so the attribute panel has something
          const root = d.spans.find(s => !s.parent_span_id);
          setSelectedSpanID(root?.span_id || null);
        }
      })
      .catch(e => {
        if (!cancelled) setError(e instanceof Error ? e.message : 'Failed to load trace');
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [traceID]);

  // Esc closes
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', h);
    return () => window.removeEventListener('keydown', h);
  }, [onClose]);

  const traceStartMs = useMemo(() => {
    if (!detail) return 0;
    return new Date(detail.trace_start).getTime();
  }, [detail]);

  const selectedSpan = detail?.spans.find(s => s.span_id === selectedSpanID) || null;

  return (
    <div
      onClick={onClose}
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0,0,0,0.6)',
        backdropFilter: 'blur(4px)',
        zIndex: 1000,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: '5vh 4vw',
      }}
    >
      <div
        onClick={e => e.stopPropagation()}
        style={{
          width: '100%',
          maxWidth: 1200,
          maxHeight: '90vh',
          background: 'var(--color-surface)',
          border: '1px solid var(--color-border)',
          borderRadius: 'var(--radius-2xl)',
          boxShadow: 'var(--shadow-2xl)',
          display: 'flex',
          flexDirection: 'column',
          overflow: 'hidden',
        }}
      >
        {/* Header */}
        <div className="px-5 py-3 border-b border-[var(--color-border)] flex items-center justify-between">
          <div>
            <div className="text-sm text-[var(--color-text-tertiary)]">trace</div>
            <div className="font-mono text-sm">{traceID}</div>
            {detail && (
              <div className="text-xs text-[var(--color-text-secondary)] mt-1 flex flex-wrap gap-x-3">
                <span>{detail.span_count} spans</span>
                <span>{fmtDuration(detail.trace_duration_ms)}</span>
                {detail.input_tokens != null && detail.input_tokens + (detail.output_tokens || 0) > 0 && (
                  <span>
                    {(detail.input_tokens + (detail.output_tokens || 0)).toLocaleString()} tok
                  </span>
                )}
                {detail.cost_usd != null && detail.cost_usd > 0 && (
                  <span
                    title={detail.cost_partial ? 'Partial — some blocks have no price entry' : ''}
                    style={{ color: 'var(--color-accent)', fontWeight: 600 }}
                  >
                    ${detail.cost_usd.toFixed(detail.cost_usd >= 0.01 ? 3 : 4)}
                    {detail.cost_partial ? ' (partial)' : ''}
                  </span>
                )}
                <span>{fmtAbsoluteTime(detail.trace_start)}</span>
              </div>
            )}
          </div>
          <button onClick={onClose} className="text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)]">
            <X className="w-5 h-5" />
          </button>
        </div>

        {loading && (
          <div className="flex-1 flex items-center justify-center text-[var(--color-text-secondary)]">
            Loading…
          </div>
        )}
        {error && (
          <div className="p-5 text-[var(--color-error)] text-sm">{error}</div>
        )}

        {detail && (
          <div className="flex-1 flex overflow-hidden">
            {/* Waterfall */}
            <div className="flex-1 overflow-y-auto p-4">
              {detail.spans.map(s => {
                const startMs = new Date(s.start_time).getTime() - traceStartMs;
                const pct = detail.trace_duration_ms > 0
                  ? Math.max(0.5, (s.duration_ms / detail.trace_duration_ms) * 100)
                  : 100;
                const offsetPct = detail.trace_duration_ms > 0
                  ? (startMs / detail.trace_duration_ms) * 100
                  : 0;
                const isRoot = !s.parent_span_id;
                const isSelected = s.span_id === selectedSpanID;
                const isError = s.status_code === 'Error';
                const cacheServed =
                  s.attributes && (s.attributes['block.cache_served'] === true ||
                    s.attributes['block.cache_served'] === 'true');
                return (
                  <div
                    key={s.span_id}
                    onClick={() => setSelectedSpanID(s.span_id)}
                    className="mb-1 cursor-pointer"
                    style={{
                      paddingLeft: isRoot ? 0 : 16,
                      opacity: isSelected ? 1 : 0.88,
                    }}
                  >
                    <div className="flex items-center justify-between text-xs mb-1">
                      <span
                        className="truncate font-medium"
                        style={{ color: isSelected ? 'var(--color-accent)' : 'var(--color-text-primary)' }}
                      >
                        {s.name}
                        {cacheServed && (
                          <span className="ml-2 text-[10px] px-1.5 py-0.5 rounded bg-[var(--color-info-light)] text-[var(--color-info)]">
                            cached
                          </span>
                        )}
                      </span>
                      <span className="font-variant-numeric tabular-nums text-[var(--color-text-tertiary)]">
                        {fmtDuration(s.duration_ms)}
                      </span>
                    </div>
                    <div
                      style={{
                        height: 8,
                        background: 'var(--color-surface-hover)',
                        borderRadius: 4,
                        position: 'relative',
                      }}
                    >
                      <div
                        style={{
                          position: 'absolute',
                          left: `${offsetPct}%`,
                          width: `${pct}%`,
                          top: 0,
                          bottom: 0,
                          background: isError
                            ? 'var(--color-error)'
                            : cacheServed
                              ? 'var(--color-info)'
                              : 'var(--color-accent)',
                          borderRadius: 4,
                        }}
                      />
                    </div>
                  </div>
                );
              })}
            </div>

            {/* Attribute panel */}
            <div
              className="w-72 border-l border-[var(--color-border)] overflow-y-auto p-4 text-sm"
              style={{ background: 'var(--color-surface-hover)' }}
            >
              {selectedSpan ? (
                <>
                  <div className="text-xs text-[var(--color-text-tertiary)] mb-1">span</div>
                  <div className="font-mono text-xs mb-3 break-all">{selectedSpan.span_id}</div>
                  <div className="text-xs text-[var(--color-text-tertiary)]">name</div>
                  <div className="mb-3 font-medium">{selectedSpan.name}</div>
                  <div className="text-xs text-[var(--color-text-tertiary)]">duration</div>
                  <div className="mb-3 font-variant-numeric tabular-nums">
                    {fmtDuration(selectedSpan.duration_ms)}
                  </div>
                  <div className="text-xs text-[var(--color-text-tertiary)]">status</div>
                  <div className="mb-3">
                    <StatusPill code={selectedSpan.status_code} desc={selectedSpan.status_desc} />
                  </div>
                  {selectedSpan.attributes && Object.keys(selectedSpan.attributes).length > 0 && (
                    <>
                      <div className="text-xs text-[var(--color-text-tertiary)] mb-2">attributes</div>
                      <div className="space-y-2">
                        {Object.entries(selectedSpan.attributes).map(([k, v]) => (
                          <div key={k} className="text-xs">
                            <div className="text-[var(--color-text-tertiary)] font-mono">{k}</div>
                            <div className="break-all font-mono">{String(v)}</div>
                          </div>
                        ))}
                      </div>
                    </>
                  )}
                </>
              ) : (
                <div className="text-[var(--color-text-secondary)] flex items-center gap-2">
                  <ChevronRight className="w-4 h-4" />
                  Click any bar to inspect
                </div>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

export default Traces;
