import type { Attachment, PlotData, DataFrameData, TokenUsage } from './websocket';
import type { Artifact } from './artifact';
import type { ActivePrompt } from './interactivePrompt';

export interface ToolCall {
  id: string;
  name: string;
  displayName?: string; // User-friendly display name
  icon?: string; // Lucide icon name
  status: 'executing' | 'completed';
  query?: string;
  result?: string;
  plots?: PlotData[]; // Visualization plots from E2B tools
  dataframes?: DataFrameData[]; // Tabular artifacts from display_df()
  timestamp: number;
  isExpanded?: boolean;
}

// Retry types for response versioning
export type RetryType =
  | 'regenerate'
  | 'add_details'
  | 'more_concise'
  | 'no_search'
  | 'think_longer';

export interface Message {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  timestamp: Date;
  status?: 'sending' | 'sent' | 'error';
  error?: string;
  isStreaming?: boolean;
  toolCalls?: ToolCall[];
  reasoning?: string; // Thinking/reasoning process
  statusUpdate?: string; // Pre-processing status (skill routing, tool selection, generating)
  attachments?: Attachment[]; // File attachments (images, PDFs)
  artifacts?: Artifact[]; // Renderable artifacts (HTML, SVG, Mermaid)
  usage?: TokenUsage; // Per-turn token/cost/latency for the cost chip

  // Interactive prompt (when AI asks clarifying questions)
  interactivePrompt?: {
    promptId: string;
    title: string;
    questions: Array<{
      id: string;
      label: string;
      type: string;
    }>;
    answers: Record<
      string,
      {
        value: string | number | boolean | string[];
        label?: string; // Human-readable representation
      }
    >;
    answeredAt: Date;
  };

  // Response versioning fields
  versionGroupId?: string; // Groups all versions of same response together
  versionNumber?: number; // 1, 2, 3... within the group
  isHidden?: boolean; // Hidden versions (not the currently selected one)
  retryType?: RetryType; // Type of retry that generated this version
}

export type ChatStatus = 'local-only' | 'active' | 'stale' | 'expired';

export interface Chat {
  id: string;
  title: string;
  messages: Message[];
  createdAt: Date;
  updatedAt: Date;
  lastActivityAt?: Date; // Timestamp of last user message sent
  systemInstructions?: string; // Optional per-conversation custom prompts
  backendStatus?: ChatStatus; // Status of conversation on backend
  isStarred?: boolean; // Whether the chat is starred/favorited
  pendingPrompt?: ActivePrompt; // Prompt waiting for user response (persists across navigation)
}
