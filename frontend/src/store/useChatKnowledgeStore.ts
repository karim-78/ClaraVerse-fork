// Per-chat selection of project knowledge bases.
//
// Each chat conversation can attach 0..N projects. When the user
// sends a message, the selected project_ids are threaded through the
// websocket payload so the backend injects search_knowledge as a
// per-turn tool scoped to those projects.
//
// Persisted to localStorage (not session) because users expect chat
// associations to survive a tab close — opening the same chat
// tomorrow should still show the same attached projects.
//
// Storage shape is { [chatId]: string[] }. We keep a "new chat" slot
// under the empty-string key so the picker works before the chat has
// been created on the server.

import { create } from 'zustand';
import { persist, createJSONStorage } from 'zustand/middleware';

const NEW_CHAT_KEY = '';

interface ChatKnowledgeState {
  /** chat_id → selected project IDs (empty string key = "new chat" draft) */
  selections: Record<string, string[]>;

  /** Read selection for a chat (or the new-chat draft). Stable empty array reference. */
  get: (chatId: string | null | undefined) => string[];

  /** Replace selection for a chat. */
  set: (chatId: string | null | undefined, ids: string[]) => void;

  /** Add a single project to a chat's selection (idempotent). */
  add: (chatId: string | null | undefined, projectId: string) => void;

  /** Remove a single project. */
  remove: (chatId: string | null | undefined, projectId: string) => void;

  /** Clear all selections for a chat (e.g. when chat is deleted). */
  clear: (chatId: string | null | undefined) => void;

  /**
   * Promote the new-chat draft to a real chat ID once the server
   * assigns one. Called from the chat send path so the user's pre-send
   * selection survives the transition from draft → committed chat.
   */
  promoteDraft: (newChatId: string) => void;
}

// Cached empty array so consumers that compare references don't
// re-render every poll cycle.
const EMPTY: string[] = Object.freeze([]) as unknown as string[];

const keyFor = (chatId: string | null | undefined): string =>
  chatId && chatId.length > 0 ? chatId : NEW_CHAT_KEY;

export const useChatKnowledgeStore = create<ChatKnowledgeState>()(
  persist(
    (set, get) => ({
      selections: {},

      get: chatId => get().selections[keyFor(chatId)] ?? EMPTY,

      set: (chatId, ids) =>
        set(state => {
          const key = keyFor(chatId);
          // Dedupe + drop empties.
          const cleaned = Array.from(new Set(ids.map(s => s.trim()).filter(Boolean)));
          if (cleaned.length === 0) {
            // Empty selection — remove the entry entirely to keep storage tidy.
            const { [key]: _omit, ...rest } = state.selections;
            return { selections: rest };
          }
          return { selections: { ...state.selections, [key]: cleaned } };
        }),

      add: (chatId, projectId) =>
        set(state => {
          const key = keyFor(chatId);
          const existing = state.selections[key] ?? [];
          if (existing.includes(projectId)) return state;
          return { selections: { ...state.selections, [key]: [...existing, projectId] } };
        }),

      remove: (chatId, projectId) =>
        set(state => {
          const key = keyFor(chatId);
          const existing = state.selections[key];
          if (!existing) return state;
          const next = existing.filter(p => p !== projectId);
          if (next.length === 0) {
            const { [key]: _omit, ...rest } = state.selections;
            return { selections: rest };
          }
          return { selections: { ...state.selections, [key]: next } };
        }),

      clear: chatId =>
        set(state => {
          const key = keyFor(chatId);
          if (!state.selections[key]) return state;
          const { [key]: _omit, ...rest } = state.selections;
          return { selections: rest };
        }),

      promoteDraft: newChatId =>
        set(state => {
          const draft = state.selections[NEW_CHAT_KEY];
          if (!draft || draft.length === 0) return state;
          // Don't overwrite an existing real-chat selection (defensive).
          if (state.selections[newChatId]) return state;
          const { [NEW_CHAT_KEY]: _omit, ...rest } = state.selections;
          return { selections: { ...rest, [newChatId]: draft } };
        }),
    }),
    {
      name: 'chat-knowledge-projects',
      storage: createJSONStorage(() => localStorage),
      version: 1,
    }
  )
);
