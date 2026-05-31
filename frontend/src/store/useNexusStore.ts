import { create } from 'zustand';
import { devtools, persist, createJSONStorage } from 'zustand/middleware';
import type {
  NexusSession,
  NexusTask,
  NexusProject,
  NexusSave,
  Daemon,
  PersonaFact,
  EngramEntry,
  NexusConversationItem,
  ClassificationResult,
  MissedUpdate,
  DaemonTemplate,
} from '@/types/nexus';

export type NexusView = 'project' | 'daemons' | 'routines' | 'settings' | 'saves';
export type RightPanel = 'none' | 'task-detail';

interface NexusState {
  // Connection
  connected: boolean;

  // MCP Bridge (TUI daemon) connection
  bridgeConnected: boolean;

  // Session
  session: NexusSession | null;

  // Active daemons
  daemons: Record<string, Partial<Daemon>>;

  // Recent tasks
  tasks: NexusTask[];

  // Persona facts
  persona: PersonaFact[];

  // Engram entries (recent activity)
  engrams: EngramEntry[];

  // Daemon templates
  daemonTemplates: DaemonTemplate[];

  // Projects
  projects: NexusProject[];
  activeProjectId: string | null;

  // Saves (user-saved outputs)
  saves: NexusSave[];

  // TUI-synced brain data (memories + skills from desktop agent)
  brainMemories: EngramEntry[];

  // Conversation items (for display)
  conversation: NexusConversationItem[];

  // Missed updates (while you were away)
  missedUpdates: MissedUpdate[];

  // Current classification
  classification: ClassificationResult | null;

  // Loading state
  isProcessing: boolean;

  // Navigation
  activeView: NexusView;

  // Kanban UI state
  selectedTaskId: string | null;
  rightPanel: RightPanel;

  // Tracks locally-deleted task IDs to prevent re-addition from session re-fetch
  _deletedTaskIds: Set<string>;

  // Actions
  setConnected: (connected: boolean) => void;
  setBridgeConnected: (connected: boolean) => void;
  setSessionState: (data: Record<string, unknown>) => void;
  setSession: (session: NexusSession) => void;
  // Replace the task list with an authoritative server snapshot (from
  // nexusService.listTasks). Honors locally-deleted IDs.
  setTasks: (tasks: NexusTask[]) => void;

  addDaemon: (daemon: Partial<Daemon>) => void;
  updateDaemon: (update: Record<string, unknown>) => void;
  removeDaemon: (daemonId: string) => void;

  addTask: (task: NexusTask) => void;
  updateTask: (task: Record<string, unknown>) => void;
  removeTask: (taskId: string) => void;

  setDaemonTemplates: (templates: DaemonTemplate[]) => void;
  addDaemonTemplate: (template: DaemonTemplate) => void;
  updateDaemonTemplate: (id: string, updates: Partial<DaemonTemplate>) => void;
  removeDaemonTemplate: (id: string) => void;

  setProjects: (projects: NexusProject[]) => void;
  addProject: (project: NexusProject) => void;
  updateProjectInStore: (id: string, updates: Partial<NexusProject>) => void;
  removeProject: (id: string) => void;
  setActiveProjectId: (id: string | null) => void;

  setSaves: (saves: NexusSave[]) => void;
  addSave: (save: NexusSave) => void;
  updateSaveInStore: (id: string, updates: Partial<NexusSave>) => void;
  removeSave: (id: string) => void;

  setPersona: (facts: unknown[]) => void;
  setEngrams: (entries: unknown[]) => void;
  setBrainMemories: (entries: unknown[]) => void;

  addConversationItem: (item: NexusConversationItem) => void;
  clearConversation: () => void;

  setMissedUpdates: (updates: MissedUpdate[]) => void;
  clearMissedUpdates: () => void;

  setClassification: (result: Record<string, unknown>) => void;
  setIsProcessing: (processing: boolean) => void;

  setTaskProject: (taskId: string, projectId: string | null) => void;

  setActiveView: (view: NexusView) => void;
  setSelectedTaskId: (id: string | null) => void;
  setRightPanel: (panel: RightPanel) => void;

  reset: () => void;
}

const initialState = {
  connected: false,
  bridgeConnected: false,
  session: null,
  daemons: {},
  tasks: [] as NexusTask[],
  daemonTemplates: [] as DaemonTemplate[],
  projects: [] as NexusProject[],
  activeProjectId: null as string | null,
  saves: [] as NexusSave[],
  persona: [] as PersonaFact[],
  engrams: [] as EngramEntry[],
  brainMemories: [] as EngramEntry[],
  conversation: [] as NexusConversationItem[],
  missedUpdates: [] as MissedUpdate[],
  classification: null as ClassificationResult | null,
  isProcessing: false,
  activeView: 'project' as NexusView,
  selectedTaskId: null as string | null,
  rightPanel: 'none' as RightPanel,
  _deletedTaskIds: new Set<string>(),
};

// Conversation/daemon snapshots are persisted to sessionStorage so the
// live activity panel survives in-app navigation (Chat → Workflows → Nexus)
// AND hard page reloads within the same tab. WebSocket reconnect still
// fires a fresh session_state on top, but the cached snapshot fills the
// gap so the UI never goes blank for the user. Scoped to sessionStorage
// (not localStorage) so a different user opening a new tab gets a clean
// slate — multi-user safety on shared devices.
export const useNexusStore = create<NexusState>()(
  devtools(
    persist(
      set => ({
      ...initialState,

      setConnected: connected => set({ connected }),

      setBridgeConnected: connected => set({ bridgeConnected: connected }),

      setSessionState: data =>
        set(state => {
          const incomingTasks = (data.recent_tasks as NexusTask[]) ?? [];
          // Filter out tasks the user has deleted locally (prevents race with session re-fetch)
          const filtered =
            state._deletedTaskIds.size > 0
              ? incomingTasks.filter(t => !state._deletedTaskIds.has(t.id))
              : incomingTasks;
          return {
            bridgeConnected: (data.bridge_connected as boolean) ?? false,
            session: data.session as NexusSession,
            daemons: ((data.active_daemons as Daemon[]) ?? []).reduce(
              (acc, d) => ({ ...acc, [d.id]: d }),
              {} as Record<string, Partial<Daemon>>
            ),
            tasks: filtered,
            persona: (data.persona as PersonaFact[]) ?? [],
            engrams: (data.recent_engrams as EngramEntry[]) ?? [],
            brainMemories: (data.brain_memories as EngramEntry[]) ?? [],
          };
        }),

      setSession: session => set({ session }),

      addDaemon: daemon =>
        set(state => ({
          daemons: { ...state.daemons, [daemon.id!]: daemon },
        })),

      updateDaemon: update =>
        set(state => {
          const id = (update.daemon_id as string) || (update.id as string);
          if (!id) return state;
          const existing = state.daemons[id] || {};
          return {
            daemons: {
              ...state.daemons,
              [id]: { ...existing, ...update, id },
            },
          };
        }),

      removeDaemon: daemonId =>
        set(state => {
          const { [daemonId]: _, ...rest } = state.daemons;
          return { daemons: rest };
        }),

      addTask: task => set(state => ({ tasks: [task, ...state.tasks] })),

      // Authoritative server-side task list (from REST nexusService.listTasks).
      // Used on Nexus page mount / reconnect to refresh from the source of truth
      // instead of relying on the WS session_state message (which can lag if
      // events arrived while the WS was disconnected). Respects locally-deleted
      // task IDs so a delete that hasn't propagated to the server yet doesn't
      // flicker the task back into view.
      setTasks: serverTasks =>
        set(state => {
          const filtered =
            state._deletedTaskIds.size > 0
              ? serverTasks.filter(t => !state._deletedTaskIds.has(t.id))
              : serverTasks;
          return { tasks: filtered };
        }),

      updateTask: task =>
        set(state => {
          const taskId = (task.task_id as string) || (task.id as string);
          if (!taskId) return state;
          const idx = state.tasks.findIndex(t => t.id === taskId);
          if (idx >= 0) {
            const updated = [...state.tasks];
            updated[idx] = { ...updated[idx], ...task } as NexusTask;
            return { tasks: updated };
          }
          return { tasks: [task as unknown as NexusTask, ...state.tasks] };
        }),

      removeTask: taskId =>
        set(state => {
          const deleted = new Set(state._deletedTaskIds);
          deleted.add(taskId);
          return {
            tasks: state.tasks.filter(t => t.id !== taskId),
            selectedTaskId: state.selectedTaskId === taskId ? null : state.selectedTaskId,
            _deletedTaskIds: deleted,
          };
        }),

      setDaemonTemplates: templates => set({ daemonTemplates: templates }),

      addDaemonTemplate: template =>
        set(state => ({
          daemonTemplates: [...state.daemonTemplates, template],
        })),

      updateDaemonTemplate: (id, updates) =>
        set(state => ({
          daemonTemplates: state.daemonTemplates.map(t => (t.id === id ? { ...t, ...updates } : t)),
        })),

      removeDaemonTemplate: id =>
        set(state => ({
          daemonTemplates: state.daemonTemplates.filter(t => t.id !== id),
        })),

      setProjects: projects => set({ projects }),

      addProject: project => set(state => ({ projects: [...state.projects, project] })),

      updateProjectInStore: (id, updates) =>
        set(state => ({
          projects: state.projects.map(p => (p.id === id ? { ...p, ...updates } : p)),
        })),

      removeProject: id =>
        set(state => {
          const remaining = state.projects.filter(p => p.id !== id);
          const wasActive = state.activeProjectId === id;
          return {
            projects: remaining,
            activeProjectId: wasActive ? (remaining[0]?.id ?? null) : state.activeProjectId,
            activeView: wasActive ? 'project' : state.activeView,
          };
        }),

      setActiveProjectId: id => set({ activeProjectId: id, activeView: 'project' }),

      setSaves: saves => set({ saves }),

      addSave: save => set(state => ({ saves: [save, ...state.saves] })),

      updateSaveInStore: (id, updates) =>
        set(state => ({
          saves: state.saves.map(s => (s.id === id ? { ...s, ...updates } : s)),
        })),

      removeSave: id =>
        set(state => ({
          saves: state.saves.filter(s => s.id !== id),
        })),

      setPersona: facts => set({ persona: facts as PersonaFact[] }),

      setEngrams: entries => set({ engrams: entries as EngramEntry[] }),

      setBrainMemories: entries => set({ brainMemories: entries as EngramEntry[] }),

      addConversationItem: item =>
        set(state => ({
          conversation: [...state.conversation, item],
          isProcessing: item.type === 'user_message' ? true : state.isProcessing,
        })),

      clearConversation: () => set({ conversation: [] }),

      setMissedUpdates: updates => set({ missedUpdates: updates }),
      clearMissedUpdates: () => set({ missedUpdates: [] }),

      setClassification: result =>
        set({ classification: result as unknown as ClassificationResult }),

      setIsProcessing: processing => set({ isProcessing: processing }),

      setTaskProject: (taskId, projectId) =>
        set(state => ({
          tasks: state.tasks.map(t =>
            t.id === taskId ? { ...t, project_id: projectId ?? undefined } : t
          ),
        })),

      setActiveView: view => set({ activeView: view }),
      setSelectedTaskId: id => set({ selectedTaskId: id, rightPanel: id ? 'task-detail' : 'none' }),
      setRightPanel: panel => set({ rightPanel: panel }),

      reset: () =>
        set({
          ...initialState,
          daemons: {},
          tasks: [],
          daemonTemplates: [],
          projects: [],
          saves: [],
          persona: [],
          engrams: [],
          brainMemories: [],
          conversation: [],
          missedUpdates: [],
          _deletedTaskIds: new Set<string>(),
        }),
    }),
      {
        name: 'nexus-live-panel',
        storage: createJSONStorage(() => sessionStorage),
        // Persist only what's needed to make the live panel "still there"
        // when you come back. Skip: connection flags (rebuilt by WS),
        // server-authoritative collections (refetched on mount), Sets
        // (don't survive JSON), navigation state (URL is the source).
        // The conversation+daemons pair is the panel; classification
        // keeps the inline routing chip stable across navigation.
        partialize: state => ({
          conversation: state.conversation.slice(-200),
          daemons: state.daemons,
          classification: state.classification,
          missedUpdates: state.missedUpdates,
        }),
        version: 1,
      }
    ),
    { name: 'nexus-store' }
  )
);
