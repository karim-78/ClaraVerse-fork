/**
 * UpdateBanner — admin-only "update available" widget for the
 * dashboard. Polls the backend's /api/admin/updates/check on mount
 * (and again every 6h), shows a banner when an update is available,
 * and offers a one-click apply that triggers Watchtower.
 *
 * Lifecycle of the apply flow:
 *
 *   1. Click "Update now" → POST /api/admin/updates/apply
 *   2. Backend returns {started: true} + tells Watchtower to update
 *   3. Watchtower pulls latest images + recreates containers
 *      (the backend container is part of this — request may not
 *      return cleanly because the connection drops mid-flight)
 *   4. Frontend enters "updating" state, polls /health every 3s
 *   5. When /health returns 200 again, refresh the page (so the
 *      newly-served frontend bundle takes effect)
 */
import { useCallback, useEffect, useState } from 'react';
import { Download, RefreshCw, AlertCircle, CheckCircle2, ExternalLink } from 'lucide-react';
import { api } from '@/services/api';

interface CheckResponse {
  current_version: string;
  latest_version: string;
  update_available: boolean;
  release_url?: string;
  release_name?: string;
  published_at?: string;
  watchtower_ready: boolean;
  hint_if_manual?: string;
}

interface ApplyResponse {
  started: boolean;
  message: string;
  reconnect_in?: string;
}

type Phase = 'idle' | 'checking' | 'available' | 'applying' | 'reconnecting' | 'done' | 'error';

export function UpdateBanner() {
  const [phase, setPhase] = useState<Phase>('idle');
  const [info, setInfo] = useState<CheckResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);

  // ─ Check for updates ─────────────────────────────────────────
  const check = useCallback(async () => {
    setPhase('checking');
    setError(null);
    try {
      const r = await api.get<CheckResponse>('/api/admin/updates/check');
      setInfo(r);
      setPhase(r.update_available ? 'available' : 'idle');
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'check failed');
      setPhase('error');
    }
  }, []);

  // Poll on mount + every 6h. Six hours is long enough that the
  // admin won't see stale info if they leave the dashboard open
  // for a workday, and short enough that we're not spamming
  // GitHub's API.
  useEffect(() => {
    void check();
    const id = window.setInterval(() => void check(), 6 * 60 * 60 * 1000);
    return () => window.clearInterval(id);
  }, [check]);

  // ─ Apply update ──────────────────────────────────────────────
  const apply = useCallback(async () => {
    setPhase('applying');
    setError(null);
    setConfirming(false);
    try {
      const r = await api.post<ApplyResponse>('/api/admin/updates/apply', {});
      if (!r.started) {
        setError(r.message);
        setPhase('error');
        return;
      }
      // Backend will restart imminently — switch to reconnect mode.
      setPhase('reconnecting');
    } catch (e: unknown) {
      // A connection drop here is EXPECTED — the backend container
      // is recreating. Treat any error during apply as "update
      // started" and switch to reconnect polling.
      setPhase('reconnecting');
      console.warn('[update] apply request errored (expected if container restarted):', e);
    }
  }, []);

  // ─ Reconnect poll ────────────────────────────────────────────
  // Once Watchtower has recreated the backend, /api/admin/updates/check
  // will succeed again. We poll every 3s until that happens, then
  // hard-reload so the new frontend bundle takes effect.
  useEffect(() => {
    if (phase !== 'reconnecting') return;
    let attempts = 0;
    const maxAttempts = 60; // 60 × 3s = 3 min ceiling
    const id = window.setInterval(async () => {
      attempts++;
      try {
        const r = await api.get<CheckResponse>('/api/admin/updates/check');
        // Got a response — backend is up. Hard reload to pick up
        // the new frontend bundle that the new container serves.
        window.clearInterval(id);
        setPhase('done');
        setInfo(r);
        // Brief delay so the user sees the "done" state before reload.
        setTimeout(() => window.location.reload(), 1500);
      } catch {
        // Still restarting — keep polling.
        if (attempts >= maxAttempts) {
          window.clearInterval(id);
          setError(
            'Backend didn\'t come back within 3 minutes. Check `docker logs claraverse-watchtower` and consider running `claraverse update` manually.'
          );
          setPhase('error');
        }
      }
    }, 3000);
    return () => window.clearInterval(id);
  }, [phase]);

  // ─ Render ────────────────────────────────────────────────────
  // Hide the banner entirely when there's nothing to show. Errors
  // and "available" + "applying" states render visibly.
  if (phase === 'idle' || phase === 'checking') return null;
  if (!info && phase !== 'error' && phase !== 'applying' && phase !== 'reconnecting' && phase !== 'done') {
    return null;
  }

  const isError = phase === 'error';
  const isWorking = phase === 'applying' || phase === 'reconnecting';
  const isDone = phase === 'done';

  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'flex-start',
        gap: 12,
        padding: '14px 16px',
        borderRadius: 12,
        border: '1px solid',
        borderColor: isError
          ? 'var(--color-error-border)'
          : isDone
            ? 'var(--color-success-border)'
            : 'var(--color-accent-border)',
        background: isError
          ? 'var(--color-error-light)'
          : isDone
            ? 'var(--color-success-light)'
            : 'var(--color-accent-light)',
        marginBottom: 8,
      }}
    >
      <div style={{ flexShrink: 0, marginTop: 2 }}>
        {isError ? (
          <AlertCircle size={20} style={{ color: 'var(--color-error)' }} />
        ) : isDone ? (
          <CheckCircle2 size={20} style={{ color: 'var(--color-success)' }} />
        ) : isWorking ? (
          <RefreshCw size={20} style={{ color: 'var(--color-accent)' }} className="spin" />
        ) : (
          <Download size={20} style={{ color: 'var(--color-accent)' }} />
        )}
      </div>

      <div style={{ flex: 1, minWidth: 0 }}>
        {phase === 'available' && info && (
          <>
            <div style={{ fontWeight: 600, fontSize: 14 }}>
              ClaraVerse {info.latest_version} is available
            </div>
            <div style={{ fontSize: 13, color: 'var(--color-text-secondary)', marginTop: 2 }}>
              You're running {info.current_version}.{' '}
              {info.release_url && (
                <a
                  href={info.release_url}
                  target="_blank"
                  rel="noopener noreferrer"
                  style={{ color: 'var(--color-accent)', textDecoration: 'none' }}
                >
                  Release notes <ExternalLink size={11} style={{ display: 'inline', verticalAlign: 'middle' }} />
                </a>
              )}
            </div>
            {!info.watchtower_ready && info.hint_if_manual && (
              <div
                style={{
                  marginTop: 8,
                  padding: 8,
                  borderRadius: 6,
                  background: 'var(--color-surface)',
                  fontSize: 12,
                  color: 'var(--color-text-tertiary)',
                  fontFamily: 'monospace',
                }}
              >
                {info.hint_if_manual}
              </div>
            )}
          </>
        )}

        {phase === 'applying' && (
          <div style={{ fontWeight: 500, fontSize: 14 }}>
            Triggering update via Watchtower…
          </div>
        )}

        {phase === 'reconnecting' && (
          <>
            <div style={{ fontWeight: 500, fontSize: 14 }}>
              Update in progress — waiting for backend to come back
            </div>
            <div style={{ fontSize: 12, color: 'var(--color-text-tertiary)', marginTop: 2 }}>
              Page will reload automatically. Don't close this tab.
            </div>
          </>
        )}

        {phase === 'done' && info && (
          <div style={{ fontWeight: 500, fontSize: 14 }}>
            Updated to {info.latest_version}. Reloading…
          </div>
        )}

        {phase === 'error' && (
          <>
            <div style={{ fontWeight: 500, fontSize: 14 }}>Update failed</div>
            <div style={{ fontSize: 12, color: 'var(--color-text-tertiary)', marginTop: 2 }}>
              {error ?? 'Unknown error'}
            </div>
          </>
        )}
      </div>

      {/* Action buttons */}
      {phase === 'available' && info?.watchtower_ready && !confirming && (
        <button
          type="button"
          onClick={() => setConfirming(true)}
          style={{
            padding: '6px 14px',
            borderRadius: 8,
            background: 'var(--color-accent)',
            color: 'var(--color-text-primary)',
            border: 'none',
            fontSize: 13,
            fontWeight: 500,
            cursor: 'pointer',
            flexShrink: 0,
          }}
        >
          Update now
        </button>
      )}

      {phase === 'available' && info?.watchtower_ready && confirming && (
        <div style={{ display: 'flex', gap: 6, flexShrink: 0 }}>
          <button
            type="button"
            onClick={() => setConfirming(false)}
            style={{
              padding: '6px 10px',
              borderRadius: 8,
              background: 'transparent',
              color: 'var(--color-text-secondary)',
              border: '1px solid var(--color-border)',
              fontSize: 13,
              cursor: 'pointer',
            }}
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={() => void apply()}
            style={{
              padding: '6px 14px',
              borderRadius: 8,
              background: 'var(--color-accent)',
              color: 'var(--color-text-primary)',
              border: 'none',
              fontSize: 13,
              fontWeight: 500,
              cursor: 'pointer',
            }}
          >
            Confirm — restart now
          </button>
        </div>
      )}

      {phase === 'error' && (
        <button
          type="button"
          onClick={() => void check()}
          style={{
            padding: '6px 14px',
            borderRadius: 8,
            background: 'transparent',
            color: 'var(--color-text-secondary)',
            border: '1px solid var(--color-border)',
            fontSize: 13,
            cursor: 'pointer',
            flexShrink: 0,
          }}
        >
          Retry check
        </button>
      )}
    </div>
  );
}
