import { api } from '@/services/api';
import type {
  NexusSession,
  NexusTask,
  NexusProject,
  NexusSave,
  Daemon,
  PersonaFact,
  EngramEntry,
  DaemonTemplate,
} from '@/types/nexus';

const BASE = '/api/nexus';

function buildQuery(params: Record<string, string | number | undefined>): string {
  const entries = Object.entries(params).filter(([, v]) => v !== undefined && v !== '');
  if (entries.length === 0) return '';
  return '?' + entries.map(([k, v]) => `${k}=${encodeURIComponent(String(v))}`).join('&');
}

// Coerce JSON null → empty array. Go encodes a nil slice as `null`,
// not `[]`, so every list endpoint can return null on first-time /
// empty-state. Without this coercion every consumer would need its
// own defensive guard before calling .map/.reduce/.filter.
const asArray = <T,>(v: T[] | null | undefined): T[] => v ?? [];

export const nexusService = {
  getSession: () => api.get<NexusSession>(`${BASE}/session`),

  listTasks: (params?: { status?: string; limit?: number; offset?: number; project_id?: string }) =>
    api
      .get<NexusTask[] | null>(`${BASE}/tasks${buildQuery(params ?? {})}`)
      .then(asArray),

  getTask: (id: string) => api.get<NexusTask>(`${BASE}/tasks/${id}`),

  createTask: (data: {
    prompt: string;
    goal?: string;
    priority?: number;
    mode?: string;
    status: string;
    model_id?: string;
    project_id?: string;
  }) => api.post<NexusTask>(`${BASE}/tasks`, data),

  updateTask: (id: string, data: { prompt?: string; goal?: string }) =>
    api.put<{ status: string }>(`${BASE}/tasks/${id}`, data),

  deleteTask: (id: string) => api.delete<{ status: string }>(`${BASE}/tasks/${id}`),

  listDaemons: () => api.get<Daemon[] | null>(`${BASE}/daemons`).then(asArray),

  getDaemon: (id: string) => api.get<Daemon>(`${BASE}/daemons/${id}`),

  cancelDaemon: (id: string) => api.post<{ status: string }>(`${BASE}/daemons/${id}/cancel`, {}),

  getPersona: () => api.get<PersonaFact[] | null>(`${BASE}/persona`).then(asArray),

  getEngrams: (limit = 20) =>
    api.get<EngramEntry[] | null>(`${BASE}/engrams${buildQuery({ limit })}`).then(asArray),

  // Daemon Templates
  listDaemonTemplates: () =>
    api.get<DaemonTemplate[] | null>(`${BASE}/daemon-templates`).then(asArray),

  createDaemonTemplate: (template: Partial<DaemonTemplate>) =>
    api.post<DaemonTemplate>(`${BASE}/daemon-templates`, template),

  updateDaemonTemplate: (id: string, updates: Partial<DaemonTemplate>) =>
    api.put<{ status: string }>(`${BASE}/daemon-templates/${id}`, updates),

  deleteDaemonTemplate: (id: string) =>
    api.delete<{ status: string }>(`${BASE}/daemon-templates/${id}`),

  // Projects
  listProjects: () =>
    api.get<NexusProject[] | null>(`${BASE}/projects`).then(asArray),

  createProject: (data: Partial<NexusProject>) => api.post<NexusProject>(`${BASE}/projects`, data),

  updateProject: (id: string, data: Partial<NexusProject>) =>
    api.put<{ status: string }>(`${BASE}/projects/${id}`, data),

  deleteProject: (id: string) => api.delete<{ status: string }>(`${BASE}/projects/${id}`),

  moveTaskToProject: (taskId: string, projectId: string | null) =>
    api.post<{ status: string }>(`${BASE}/tasks/${taskId}/move`, { project_id: projectId }),

  // Saves
  listSaves: (params?: { tag?: string; limit?: number; offset?: number }) =>
    api
      .get<NexusSave[] | null>(`${BASE}/saves${buildQuery(params ?? {})}`)
      .then(asArray),

  getSave: (id: string) => api.get<NexusSave>(`${BASE}/saves/${id}`),

  createSave: (data: {
    title: string;
    content: string;
    tags?: string[];
    source_task_id?: string;
    source_project_id?: string;
  }) => api.post<NexusSave>(`${BASE}/saves`, data),

  updateSave: (id: string, data: { title?: string; content?: string; tags?: string[] }) =>
    api.put<{ status: string }>(`${BASE}/saves/${id}`, data),

  deleteSave: (id: string) => api.delete<{ status: string }>(`${BASE}/saves/${id}`),
};
