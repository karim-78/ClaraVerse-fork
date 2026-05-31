// Client for the per-project RAG knowledge base. Mirrors the
// Go-side rag.Service surface that's reachable over HTTP.
//
// Why a dedicated service file: this isn't a Nexus concept (knowledge
// lives on projects, projects span Chat / Nexus / Workflows) so it
// shouldn't pile onto nexusService. Separate module = easier to find
// + easier to add specialized retry/streaming later.

import { api } from './api';

export type KnowledgeFileStatus = 'queued' | 'ingesting' | 'ready' | 'failed';

export interface KnowledgeFile {
  id: string;
  project_id: string;
  user_id: string;
  filename: string;
  content_type: string;
  size_bytes: number;
  sha256: string;
  source_url?: string;
  status: KnowledgeFileStatus;
  error?: string;
  ingest_progress?: number; // 0..1
  chunk_count?: number;
  created_at: string;
  ingested_at?: string;
}

export interface KnowledgeSearchHit {
  score: number;
  text: string;
  file_id: string;
  file_name: string;
  chunk_idx: number;
  page?: number;
  section?: string;
  project_id: string;
}

export interface KnowledgeHealthInfo {
  ok: boolean;
  dense_loaded: boolean;
  sparse_loaded: boolean;
  rerank_loaded: boolean;
  dense_dim?: number;
  models: Record<string, string>;
  // Only set when the sidecar itself is unreachable.
  available?: boolean;
  error?: string;
}

const base = (projectId: string) => `/api/projects/${projectId}/knowledge`;

export const knowledgeService = {
  /** List all files in a project's knowledge base (newest first). */
  listFiles: (projectId: string) =>
    api.get<{ files: KnowledgeFile[] }>(`${base(projectId)}/files`).then(r => r.files),

  /**
   * Upload a file for ingestion. Returns the created file record
   * immediately with status="queued"; subscribe via list polling or
   * (later) the WS channel to learn when status flips to "ready".
   *
   * The multipart endpoint is authenticated by the standard cookie
   * + bearer token flow; FormData carries the file under the "file"
   * field — matches what the backend handler expects.
   */
  uploadFile: async (projectId: string, file: File): Promise<KnowledgeFile> => {
    // Multipart needs the browser to set its own boundary, so we drop
    // down to fetch directly rather than going through the JSON-shaped
    // api.post helper. Pulls the auth token from localStorage the same
    // way the rest of the codebase does (see api.ts:getAuthToken).
    const form = new FormData();
    form.append('file', file);
    const token =
      typeof window !== 'undefined'
        ? (localStorage.getItem('auth_token') ?? sessionStorage.getItem('auth_token'))
        : null;
    const apiBase =
      (import.meta.env?.VITE_API_BASE_URL as string | undefined)?.replace(/\/$/, '') ?? '';
    const url = `${apiBase}${base(projectId)}/files`;
    const res = await fetch(url, {
      method: 'POST',
      credentials: 'include',
      headers: token ? { Authorization: `Bearer ${token}` } : {},
      body: form,
    });
    if (!res.ok) {
      let detail = `upload failed (${res.status})`;
      try {
        const body = await res.json();
        if (body?.error) detail = body.error;
      } catch {
        /* response not JSON — keep generic message */
      }
      throw new Error(detail);
    }
    return res.json();
  },

  /** Delete a single file (also drops its Qdrant points + disk bytes). */
  deleteFile: (projectId: string, fileId: string) =>
    api.delete<{ deleted: string }>(`${base(projectId)}/files/${fileId}`),

  /**
   * Search across one or more projects.
   * If project_ids is omitted, the call is scoped to the URL's project.
   */
  search: (projectId: string, body: { query: string; top_k?: number; rerank?: boolean; project_ids?: string[] }) =>
    api.post<{ hits: KnowledgeSearchHit[] }>(`${base(projectId)}/search`, body).then(r => r.hits),

  /** Sidecar health (admin UI uses this to warn during cold start). */
  health: () => api.get<KnowledgeHealthInfo>('/api/knowledge/health'),
};
