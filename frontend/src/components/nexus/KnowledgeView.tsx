/**
 * KnowledgeView — the per-project knowledge base management panel.
 *
 * Lives in the Nexus surface but conceptually project-level: same
 * project, same files, available to Chat / Nexus / Workflows.
 *
 * The view does three things and tries not to do anything else:
 *   1. Upload: drag-and-drop or file picker, kicks off async ingest.
 *   2. List: shows files with their current status (queued / ingesting
 *      / ready / failed) and lets you delete one.
 *   3. Health: a one-line callout when the embeddings sidecar is
 *      warming up, so users know "first ingest may take 60s" rather
 *      than thinking the system is broken.
 *
 * Polls the file list every 3s while any file is non-terminal — much
 * simpler than wiring WebSocket events for v1, and the workload is
 * tiny (a single list query per project).
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Upload,
  FileText,
  CheckCircle2,
  AlertCircle,
  Loader2,
  Trash2,
  Sparkles,
} from 'lucide-react';
import { useNexusStore } from '@/store/useNexusStore';
import {
  knowledgeService,
  type KnowledgeFile,
  type KnowledgeHealthInfo,
} from '@/services/knowledgeService';
import { useToastStore } from '@/store/useToastStore';

export function KnowledgeView() {
  const activeProjectId = useNexusStore(s => s.activeProjectId);
  const projects = useNexusStore(s => s.projects);
  const activeProject = useMemo(
    () => projects.find(p => p.id === activeProjectId),
    [projects, activeProjectId]
  );
  const toast = useToastStore(s => s.addToast);

  const [files, setFiles] = useState<KnowledgeFile[]>([]);
  const [loading, setLoading] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const [health, setHealth] = useState<KnowledgeHealthInfo | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  // ─ Data refresh ────────────────────────────────────────────────
  const refresh = useCallback(async () => {
    if (!activeProjectId) return;
    try {
      const list = await knowledgeService.listFiles(activeProjectId);
      setFiles(list);
    } catch (err) {
      console.warn('[Knowledge] list failed', err);
    }
  }, [activeProjectId]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        await refresh();
        const h = await knowledgeService.health().catch(() => null);
        if (!cancelled) setHealth(h);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [refresh]);

  // Poll while any file is non-terminal. Stops as soon as everything
  // is "ready" or "failed" — no wasted requests on a static page.
  useEffect(() => {
    const hasPending = files.some(
      f => f.status === 'queued' || f.status === 'ingesting'
    );
    if (!hasPending) return;
    const id = window.setInterval(refresh, 3000);
    return () => window.clearInterval(id);
  }, [files, refresh]);

  // ─ Upload handlers ─────────────────────────────────────────────
  const handleFiles = useCallback(
    async (filesToUpload: FileList | File[]) => {
      if (!activeProjectId) return;
      const arr = Array.from(filesToUpload);
      if (arr.length === 0) return;
      setUploading(true);
      // Fire uploads sequentially so the server doesn't get hit with
      // 20 simultaneous multipart writes from a drag of 20 files. The
      // ingest itself is async, this only serializes the upload step.
      for (const file of arr) {
        try {
          await knowledgeService.uploadFile(activeProjectId, file);
          toast({
            type: 'success',
            message: `${file.name} queued for ingestion`,
            duration: 2500,
          });
        } catch (err: unknown) {
          const msg = err instanceof Error ? err.message : 'upload failed';
          toast({ type: 'error', message: `${file.name}: ${msg}`, duration: 5000 });
        }
      }
      setUploading(false);
      await refresh();
    },
    [activeProjectId, refresh, toast]
  );

  const onDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      setDragOver(false);
      if (e.dataTransfer.files.length > 0) {
        void handleFiles(e.dataTransfer.files);
      }
    },
    [handleFiles]
  );

  const onPickFiles = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      if (e.target.files) void handleFiles(e.target.files);
      e.target.value = ''; // allow re-selecting the same file
    },
    [handleFiles]
  );

  const onDelete = useCallback(
    async (fileId: string, filename: string) => {
      if (!activeProjectId) return;
      if (!confirm(`Remove ${filename}? Its chunks will be deleted from this project's knowledge.`)) return;
      try {
        await knowledgeService.deleteFile(activeProjectId, fileId);
        toast({ type: 'success', message: `${filename} removed`, duration: 2500 });
        await refresh();
      } catch (err: unknown) {
        const msg = err instanceof Error ? err.message : 'delete failed';
        toast({ type: 'error', message: msg, duration: 5000 });
      }
    },
    [activeProjectId, refresh, toast]
  );

  // ─ Render ──────────────────────────────────────────────────────
  if (!activeProjectId) {
    return (
      <div style={{ padding: 32, color: 'var(--color-text-tertiary)' }}>
        Select a project from the sidebar to manage its knowledge base.
      </div>
    );
  }

  const totalChunks = files.reduce((sum, f) => sum + (f.chunk_count ?? 0), 0);
  const readyCount = files.filter(f => f.status === 'ready').length;
  // The sidecar warms its dense model in the background at boot now,
  // but on a brand-new deploy it can take ~30-60s to download. Only
  // show the warming banner when SOMETHING is actually waiting on
  // it — files currently queued or ingesting. If the user is just
  // looking at an idle project there's no point announcing the
  // model is "warming" (it's lazy, not late).
  const hasPending = files.some(
    f => f.status === 'queued' || f.status === 'ingesting'
  );
  const sidecarWarming =
    health && health.available !== false && !health.dense_loaded && hasPending;
  const sidecarDown = health?.available === false;

  return (
    <div style={containerStyle}>
      {/* Header */}
      <div style={headerRowStyle}>
        <div>
          <h2 style={{ margin: 0, fontSize: 20, fontWeight: 600 }}>
            {activeProject?.name ?? 'Project'} Knowledge
          </h2>
          <div style={{ marginTop: 4, color: 'var(--color-text-tertiary)', fontSize: 13 }}>
            {readyCount} of {files.length} files ready · {totalChunks} chunks indexed
          </div>
        </div>
      </div>

      {/* Sidecar status banner — only when there's something to say */}
      {(sidecarWarming || sidecarDown) && (
        <div
          style={{
            ...calloutStyle,
            borderColor: sidecarDown
              ? 'var(--color-error-border)'
              : 'var(--color-warning-border)',
            background: sidecarDown
              ? 'var(--color-error-light)'
              : 'var(--color-warning-light)',
          }}
        >
          {sidecarDown ? (
            <>
              <AlertCircle size={16} />
              <span>
                Embeddings service unreachable. Ingestion is paused until the sidecar comes back online.
              </span>
            </>
          ) : (
            <>
              <Loader2 size={16} className="spin" />
              <span>
                Embeddings model is warming up (first start downloads ~133 MB).
                Your first ingest may take up to 60 seconds.
              </span>
            </>
          )}
        </div>
      )}

      {/* Drop zone */}
      <div
        onDragOver={e => {
          e.preventDefault();
          setDragOver(true);
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={onDrop}
        onClick={() => inputRef.current?.click()}
        style={{
          ...dropZoneStyle,
          borderColor: dragOver
            ? 'var(--color-accent)'
            : 'var(--color-border)',
          background: dragOver
            ? 'var(--color-accent-light)'
            : 'var(--color-surface)',
        }}
      >
        <Upload size={28} style={{ marginBottom: 8 }} />
        <div style={{ fontSize: 14, fontWeight: 500 }}>
          {uploading ? 'Uploading…' : 'Drag files here, or click to browse'}
        </div>
        <div style={{ marginTop: 4, fontSize: 12, color: 'var(--color-text-tertiary)' }}>
          PDF · MD · TXT · HTML — up to 50&nbsp;MB each
        </div>
        <input
          ref={inputRef}
          type="file"
          multiple
          onChange={onPickFiles}
          style={{ display: 'none' }}
          accept=".pdf,.md,.markdown,.txt,.html,.htm,application/pdf,text/markdown,text/plain,text/html"
        />
      </div>

      {/* File list */}
      <div style={{ marginTop: 24 }}>
        {loading && files.length === 0 && (
          <div style={{ color: 'var(--color-text-tertiary)', fontSize: 13 }}>Loading…</div>
        )}
        {!loading && files.length === 0 && (
          <div style={{ color: 'var(--color-text-tertiary)', fontSize: 13, textAlign: 'center', padding: 24 }}>
            No files yet. Upload to start building this project's knowledge base.
          </div>
        )}
        {files.map(f => (
          <FileRow key={f.id} file={f} onDelete={onDelete} />
        ))}
      </div>
    </div>
  );
}

function FileRow({
  file,
  onDelete,
}: {
  file: KnowledgeFile;
  onDelete: (id: string, name: string) => void;
}) {
  return (
    <div style={fileRowStyle}>
      <FileText size={18} style={{ color: 'var(--color-text-tertiary)', flexShrink: 0 }} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div
          style={{
            fontSize: 14,
            fontWeight: 500,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
          title={file.filename}
        >
          {file.filename}
        </div>
        <div style={{ marginTop: 2, fontSize: 12, color: 'var(--color-text-tertiary)' }}>
          {prettyBytes(file.size_bytes)}
          {file.chunk_count ? ` · ${file.chunk_count} chunks` : ''}
          {file.status === 'failed' && file.error
            ? ` · ${truncate(file.error, 80)}`
            : ''}
        </div>
      </div>
      <StatusBadge file={file} />
      <button
        type="button"
        onClick={() => onDelete(file.id, file.filename)}
        title="Remove file"
        style={deleteBtnStyle}
      >
        <Trash2 size={14} />
      </button>
    </div>
  );
}

function StatusBadge({ file }: { file: KnowledgeFile }) {
  switch (file.status) {
    case 'ready':
      return (
        <div style={{ ...badgeStyle, color: 'var(--color-success)' }}>
          <CheckCircle2 size={14} />
          <span>ready</span>
        </div>
      );
    case 'failed':
      return (
        <div style={{ ...badgeStyle, color: 'var(--color-error)' }}>
          <AlertCircle size={14} />
          <span>failed</span>
        </div>
      );
    case 'ingesting': {
      const pct = Math.round((file.ingest_progress ?? 0) * 100);
      return (
        <div style={{ ...badgeStyle, color: 'var(--color-accent)' }}>
          <Loader2 size={14} className="spin" />
          <span>ingesting {pct ? `${pct}%` : '…'}</span>
        </div>
      );
    }
    case 'queued':
    default:
      return (
        <div style={{ ...badgeStyle, color: 'var(--color-text-tertiary)' }}>
          <Sparkles size={14} />
          <span>queued</span>
        </div>
      );
  }
}

// ─ Helpers + inline styles ───────────────────────────────────────
// We use inline styles + design tokens here (matching how other Nexus
// views are written in this codebase). Migrating to a stylesheet is a
// later polish pass.

function prettyBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

function truncate(s: string, max: number): string {
  return s.length > max ? `${s.slice(0, max)}…` : s;
}

const containerStyle: React.CSSProperties = {
  padding: 24,
  maxWidth: 880,
  margin: '0 auto',
  height: '100%',
  overflowY: 'auto',
};

const headerRowStyle: React.CSSProperties = {
  display: 'flex',
  justifyContent: 'space-between',
  alignItems: 'flex-start',
  marginBottom: 20,
};

const calloutStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 10,
  padding: '10px 14px',
  borderRadius: 8,
  border: '1px solid',
  fontSize: 13,
  marginBottom: 16,
};

const dropZoneStyle: React.CSSProperties = {
  border: '1.5px dashed',
  borderRadius: 12,
  padding: 36,
  textAlign: 'center',
  cursor: 'pointer',
  transition: 'all 120ms ease',
  color: 'var(--color-text-secondary)',
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  justifyContent: 'center',
};

const fileRowStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 12,
  padding: '12px 14px',
  borderBottom: '1px solid var(--color-border)',
};

const badgeStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 6,
  fontSize: 12,
  fontWeight: 500,
  flexShrink: 0,
};

const deleteBtnStyle: React.CSSProperties = {
  background: 'transparent',
  border: 'none',
  color: 'var(--color-text-tertiary)',
  cursor: 'pointer',
  padding: 6,
  borderRadius: 6,
  display: 'flex',
};
