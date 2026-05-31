// WebSocket message types for ClaraVerse backend

// Model types
export interface Model {
  id: string;
  provider_id: number;
  provider_name: string;
  provider_favicon?: string;
  name: string;
  display_name: string;
  description?: string;
  context_length?: number;
  supports_tools: boolean;
  supports_streaming: boolean;
  supports_vision: boolean;
  agents_enabled: boolean; // If true, model is available in agent builder
  provider_secure?: boolean; // Indicates if provider doesn't store user data
  is_visible: boolean;
  smart_tool_router?: boolean; // If true, model can be used as tool predictor
  fetched_at?: string;
  recommendation_tier?: {
    tier: 'tier1' | 'tier2' | 'tier3' | 'tier4' | 'tier5';
    label: string;
    description: string;
    icon: string;
  }; // Global recommendation tier with metadata
  structured_output_support?: 'excellent' | 'good' | 'poor' | 'unknown'; // Structured output quality
  structured_output_compliance?: number; // Compliance percentage (0-100)
  structured_output_warning?: string; // Warning message about structured output
  structured_output_speed_ms?: number; // Average response time in milliseconds
  structured_output_badge?: string; // Badge label (e.g., "FASTEST")
}

export interface ModelsResponse {
  models: Model[];
  count: number;
  tier?: 'anonymous' | 'authenticated';
}

// Attachment types
export interface ImageAttachment {
  type: 'image';
  file_id: string;
  url: string;
  mime_type: string;
  size: number;
  filename?: string;
  expired?: boolean; // True if file has expired and is no longer available
}

export interface DocumentAttachment {
  type: 'document';
  file_id: string;
  url: string;
  mime_type: string;
  size: number;
  filename?: string;
  page_count?: number;
  word_count?: number;
  preview?: string;
  expired?: boolean; // True if file has expired and is no longer available
}

export interface DataPreview {
  headers: string[];
  rows: string[][];
  row_count: number; // Total rows in file
  col_count: number; // Total columns
}

export interface DataAttachment {
  type: 'data';
  file_id: string;
  url: string;
  mime_type: string;
  size: number;
  filename?: string;
  expired?: boolean; // True if file has expired and is no longer available
  data_preview?: DataPreview; // CSV table preview
}

export type Attachment = ImageAttachment | DocumentAttachment | DataAttachment;

// Client to Server messages
export interface NewConversationPayload {
  type: 'new_conversation';
  conversation_id: string;
  model_id: string;
  system_instructions?: string;
}

export interface ChatMessagePayload {
  type: 'chat_message';
  conversation_id: string | null;
  content: string;
  model_id?: string;
  history?: Array<{ role: 'user' | 'assistant'; content: string }>; // Conversation history
  system_instructions?: string; // Optional per-message custom prompts
  custom_config?: {
    base_url: string;
    api_key: string;
    model: string;
  };
  attachments?: Attachment[];
  disable_tools?: boolean; // Disable tools for this message (e.g., agent builder needs pure JSON)
  selected_tools?: string[]; // If set, only use these tools (by name) instead of all available
  // RAG: project IDs whose knowledge bases should be searchable via
  // search_knowledge for this turn. Empty/absent = no knowledge tool.
  knowledge_project_ids?: string[];
}

export interface StopGenerationPayload {
  type: 'stop_generation';
  conversation_id: string;
}

export interface ResumeStreamPayload {
  type: 'resume_stream';
  conversation_id: string;
}

// Interactive prompt response (client to server)
export interface InteractiveAnswer {
  question_id: string;
  value: string | number | boolean | string[];
  is_other?: boolean; // True if user selected "Other" option
}

export interface InteractivePromptResponsePayload {
  type: 'interactive_prompt_response';
  prompt_id: string;
  conversation_id: string;
  answers: Record<string, InteractiveAnswer>; // question_id -> answer
  skipped?: boolean; // True if user cancelled/skipped
}

export type ClientMessage =
  | NewConversationPayload
  | ChatMessagePayload
  | StopGenerationPayload
  | ResumeStreamPayload
  | InteractivePromptResponsePayload;

// Server to Client messages
export interface ConnectedMessage {
  type: 'connected';
  conversation_id?: string; // Optional - conversation ID is managed by client, not server
  content: string;
}

export interface ConversationResetMessage {
  type: 'conversation_reset';
  conversation_id: string;
  content: string;
}

export interface StreamChunkMessage {
  type: 'stream_chunk';
  content: string;
}

export interface ReasoningChunkMessage {
  type: 'reasoning_chunk';
  content: string;
}

export interface ToolCallMessage {
  type: 'tool_call';
  tool_name: string;
  tool_call_id?: string;
  tool_icon?: string; // Lucide icon name from backend
  tool_display_name?: string; // User-friendly display name
  status: 'executing' | 'completed' | 'failed';
  arguments?: string | Record<string, unknown>; // Can be JSON string or object
  result?: string; // Present when status is 'completed' or 'failed'
}

export interface PlotData {
  format: string; // "png", "jpg", "svg"
  data: string; // Base64-encoded image data
}

// DataFrameData mirrors models.DataFrameData on the backend — emitted by
// the Python sandbox when the model calls display_df(df). Shaped to be
// drop-in compatible with the DataPreview / DataTablePreview component
// already used for uploaded CSV previews.
export interface DataFrameData {
  name?: string;
  headers: string[];
  rows: string[][];
  row_count: number;
  col_count: number;
}

export interface ToolResultMessage {
  type: 'tool_result';
  tool_name: string;
  tool_icon?: string; // Lucide icon name from backend
  tool_display_name?: string; // User-friendly display name
  status: 'completed' | 'failed';
  result: string;
  plots?: PlotData[]; // Visualization plots from E2B tools
  dataframes?: DataFrameData[]; // Tabular artifacts from display_df()
}

export interface TokenUsage {
  input: number;
  output: number;
  cached?: number;
  duration_ms?: number;
  estimated_cost_usd?: number;
  model?: string;
}

export interface StreamEndMessage {
  type: 'stream_end';
  conversation_id?: string;
  tokens?: TokenUsage;
}

export interface ConversationTitleMessage {
  type: 'conversation_title';
  conversation_id: string;
  title: string;
}

export interface ErrorMessage {
  type: 'error';
  message: string;
  code?: string;
}

export interface FilesExpiredMessage {
  type: 'files_expired';
  message: string;
  code?: string;
  content: string[]; // Array of expired file names
}

// Stream resume messages (for reconnection recovery)
export interface StreamResumeMessage {
  type: 'stream_resume';
  conversation_id: string;
  content: string; // Combined buffered chunks
  is_complete: boolean; // Whether generation finished while disconnected
}

export interface StreamMissedMessage {
  type: 'stream_missed';
  conversation_id: string;
  reason: 'expired' | 'not_found' | 'timeout';
}

// Context optimization message (for long conversations being compacted)
export interface ContextOptimizingMessage {
  type: 'context_optimizing';
  status: 'started' | 'summarizing' | 'completed';
  progress: number; // 0-100
  content: string; // User-friendly status message
}

// Usage limit exceeded message (for tier-based limits)
export interface LimitExceededMessage {
  type: 'limit_exceeded';
  code: string; // "message_limit_exceeded", "file_upload_limit_exceeded", etc.
  message: string;
  arguments?: {
    limit: number;
    used: number;
    reset_at: string;
    upgrade_to: string;
  };
}

// Interactive prompt messages (for AI asking clarifying questions)
export interface InteractiveQuestion {
  id: string; // Unique ID per question
  type: 'text' | 'select' | 'multi-select' | 'number' | 'checkbox';
  label: string; // Question text
  placeholder?: string; // Placeholder for text inputs
  required?: boolean; // Whether answer is required
  options?: string[]; // For select/multi-select types
  allow_other?: boolean; // Enable "Other" option with text input
  default_value?: string | number | boolean | string[]; // Default value
  validation?: {
    min?: number; // For number type
    max?: number; // For number type
    pattern?: string; // Regex pattern for text type
    min_length?: number; // Minimum length for text
    max_length?: number; // Maximum length for text
  };
}

export interface InteractivePromptMessage {
  type: 'interactive_prompt';
  prompt_id: string; // Unique ID for this prompt
  conversation_id: string;
  title: string; // Modal title (e.g., "Need more information")
  description?: string; // Optional description/context
  questions: InteractiveQuestion[]; // Array of questions
  allow_skip?: boolean; // Whether user can skip/cancel
}

// Prompt timeout message (backend timeout waiting for response)
export interface PromptTimeoutMessage {
  type: 'prompt_timeout';
  prompt_id: string;
  message: string;
}

// Prompt validation error message (server-side validation failed)
export interface PromptValidationErrorMessage {
  type: 'prompt_validation_error';
  prompt_id: string;
  errors: Record<string, string>; // question_id -> error message
}

// Status update during pre-processing (skill routing, tool selection, generating)
export interface StatusUpdateMessage {
  type: 'status_update';
  status: string; // "routing_skill" | "skill_matched" | "selecting_tools" | "predicting_tools" | "tools_ready" | "generating"
  arguments?: {
    skill_name?: string;
    skill_icon?: string;
    count?: number;
    method?: string; // "skill" | "predicted" | "fallback"
  };
}

export type ServerMessage =
  | ConnectedMessage
  | ConversationResetMessage
  | StreamChunkMessage
  | ReasoningChunkMessage
  | ToolCallMessage
  | ToolResultMessage
  | StreamEndMessage
  | StreamResumeMessage
  | StreamMissedMessage
  | ContextOptimizingMessage
  | ConversationTitleMessage
  | ErrorMessage
  | FilesExpiredMessage
  | LimitExceededMessage
  | InteractivePromptMessage
  | PromptTimeoutMessage
  | PromptValidationErrorMessage
  | StatusUpdateMessage;

// WebSocket connection state
export type WebSocketState = 'connecting' | 'connected' | 'disconnected' | 'error';

// Upload response
export interface UploadResponse {
  file_id: string;
  url: string;
  mime_type: string;
  size: number;
  filename: string;
}
