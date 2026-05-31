/**
 * Agent Builder Type Definitions
 *
 * This file contains all type definitions for the Agent Builder feature.
 * Agents are workflow automations created through natural language conversation.
 */

// =============================================================================
// Agent Types
// =============================================================================

export type AgentStatus = 'draft' | 'deployed' | 'paused';

// Sync status for backend-first architecture
export type SyncStatus = 'local-only' | 'syncing' | 'synced' | 'error';

export interface Agent {
  id: string;
  userId: string;
  name: string;
  description: string;
  workflow: Workflow;
  status: AgentStatus;
  apiKey?: string; // For webhook authentication
  createdAt: Date;
  updatedAt: Date;
  // Backend-first persistence
  syncStatus: SyncStatus;
  lastSyncError?: string;
}

// =============================================================================
// Workflow Types
// =============================================================================

export interface Workflow {
  id: string;
  blocks: Block[];
  connections: Connection[];
  variables: WorkflowVariable[];
  version: number;
  workflowModelId?: string; // Default LLM model for all blocks in this workflow
}

// Variable types supported in workflows
export type VariableType = 'string' | 'number' | 'boolean' | 'array' | 'object' | 'file';

export interface WorkflowVariable {
  name: string;
  type: VariableType;
  defaultValue?: unknown;
}

// =============================================================================
// File Reference Types
// =============================================================================

/**
 * Represents a file that can be passed between workflow blocks.
 * Files are uploaded to the backend and referenced by fileId.
 */
export interface FileReference {
  fileId: string;
  filename: string;
  mimeType: string;
  size: number;
  type: FileType;
}

/**
 * File type categories for workflow files.
 * - image: JPEG, PNG, GIF, WebP, SVG
 * - document: PDF, DOCX, PPTX
 * - audio: MP3, WAV, M4A, OGG, FLAC, WebM
 * - data: CSV, JSON, Excel, plain text
 */
export type FileType = 'image' | 'document' | 'audio' | 'data';

/**
 * File attachment for LLM blocks with vision support.
 * Currently only images are supported for OpenAI vision models.
 */
export interface FileAttachment {
  fileId: string;
  type: FileType;
}

// =============================================================================
// Block Types
// =============================================================================

// Hybrid Architecture: Supports variable, llm_inference, code_block, and n8n-style block types.
// - variable: Input/output data handling
// - llm_inference: AI reasoning with tool access (for tasks needing decisions/interpretation)
// - code_block: Direct tool execution without LLM (faster, for mechanical tasks)
// - http_request: Universal HTTP request block (GET/POST/PUT/PATCH/DELETE)
// - if_condition: Conditional routing (evaluates field, routes to true/false branch)
// - transform: Data transformation (set/delete/rename/template/extract operations)
// - webhook_trigger: Webhook trigger entry point (MVP: passthrough)
// - schedule_trigger: Schedule/cron trigger entry point (MVP: passthrough)
export type BlockType =
  | 'llm_inference'
  | 'variable'
  | 'code_block'
  | 'http_request'
  | 'if_condition'
  | 'transform'
  | 'webhook_trigger'
  | 'schedule_trigger'
  | 'for_each'
  | 'inline_code'
  | 'sub_agent'
  | 'filter'
  | 'switch'
  | 'merge'
  | 'aggregate'
  | 'sort'
  | 'limit'
  | 'deduplicate'
  | 'wait'
  | 'knowledge_search';

export interface Block {
  id: string;
  normalizedId: string; // Normalized name for variable interpolation (e.g., "search-latest-news")
  type: BlockType;
  name: string;
  description: string;
  config: BlockConfig;
  position: { x: number; y: number };
  timeout: number; // Default 30, max 60 seconds
  retryConfig?: RetryConfig;
}

/** Automatic retry configuration for blocks that make external calls */
export interface RetryConfig {
  maxRetries: number; // 0 = no retry (default)
  retryOn?: string[]; // ["rate_limit", "timeout", "server_error", "network_error", "all_transient"]
  backoffMs?: number; // Initial backoff in ms (default 1000)
  maxBackoffMs?: number; // Max backoff in ms (default 30000)
}

// Union type for block configs (Hybrid: LLM, Variable, Code, and n8n-style blocks)
export type BlockConfig =
  | LLMInferenceConfig
  | VariableConfig
  | CodeBlockConfig
  | HTTPRequestConfig
  | IfConditionConfig
  | TransformConfig
  | WebhookTriggerConfig
  | ScheduleTriggerConfig
  | ForEachConfig
  | InlineCodeConfig
  | SubAgentConfig
  | FilterConfig
  | SwitchConfig
  | MergeConfig
  | AggregateConfig
  | SortConfig
  | LimitConfig
  | DeduplicateConfig
  | WaitConfig;

// Deprecated config types (kept for backwards compatibility with existing workflows)
// eslint-disable-next-line @typescript-eslint/no-unused-vars
type DeprecatedBlockConfig = ToolExecutionConfig | WebhookConfig | PythonToolConfig;

// Structured output metadata for models
export interface StructuredOutputMetadata {
  support: 'excellent' | 'good' | 'fair' | 'poor' | 'unknown';
  compliance?: number; // 0-100
  speed_ms?: number;
  badge?: 'FASTEST' | 'RECOMMENDED' | 'BETA';
  warning?: string;
}

export interface LLMInferenceConfig {
  type: 'llm_inference';
  modelId: string;
  systemPrompt: string;
  userPromptTemplate: string; // Supports {{input.fieldName}} interpolation
  temperature: number;
  maxTokens: number;
  enabledTools: string[]; // Tool IDs to enable (e.g., ["search_web", "send_discord_message"])
  credentials?: string[]; // Credential IDs selected by user for tool authentication
  outputFormat: 'text' | 'json';
  outputSchema?: Record<string, unknown>; // JSON Schema for structured output
  // Vision support: file attachments for OpenAI vision models
  attachments?: FileAttachment[];
}

export interface ToolExecutionConfig {
  type: 'tool_execution';
  toolId: string;
  argumentMapping: Record<string, string>; // Maps input ports to tool args
}

/**
 * Configuration for code_block - direct tool execution without LLM
 * Use when the task is purely mechanical and doesn't need AI reasoning.
 * Example: sending a pre-formatted message, getting current time, making API calls with known params
 */
export interface CodeBlockConfig {
  type: 'code_block';
  toolName: string; // The tool to execute (e.g., "send_discord_message", "get_current_time")
  argumentMapping: Record<string, string>; // Maps {{variables}} to tool arguments
}

export interface WebhookConfig {
  type: 'webhook';
  url: string;
  method: 'GET' | 'POST' | 'PUT' | 'DELETE';
  headers: Record<string, string>;
  bodyTemplate: string; // Supports {{variable}} and {{input.fieldName}}
  authType?: 'none' | 'bearer' | 'basic' | 'api_key';
  authConfig?: Record<string, string>;
}

export interface VariableConfig {
  type: 'variable';
  operation: 'set' | 'read';
  variableName: string;
  valueExpression?: string;
  defaultValue?: string;
  // Input type support for Start blocks
  inputType?: 'text' | 'file' | 'json'; // Default is 'text'
  fileValue?: FileReference | null; // File reference when inputType is 'file'
  acceptedFileTypes?: FileType[]; // Restrict file types (e.g., ['image'] for vision)
  // JSON input support
  jsonValue?: Record<string, unknown> | null; // Parsed JSON when inputType is 'json'
  jsonSchema?: Record<string, unknown> | null; // Optional JSON schema for validation/UI hints
}

// =============================================================================
// n8n-Style Block Configs
// =============================================================================

export interface HTTPRequestConfig {
  type: 'http_request';
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';
  url: string; // Supports {{template}} interpolation, query params embedded in URL
  headers: Record<string, string>;
  body: string; // Supports {{template}} interpolation
  contentType?: string; // Convenience field for Content-Type header
  authType: 'none' | 'bearer' | 'basic' | 'api_key';
  authConfig: Record<string, string>; // e.g. { token: "xxx" } or { username: "x", password: "y" } or { headerName: "X-API-Key", key: "xxx" }
}

export type IfConditionOperator =
  | 'eq'
  | 'neq'
  | 'gt'
  | 'lt'
  | 'gte'
  | 'lte'
  | 'contains'
  | 'not_contains'
  | 'is_empty'
  | 'not_empty'
  | 'is_true'
  | 'is_false'
  | 'starts_with'
  | 'ends_with';

export interface IfConditionConfig {
  type: 'if_condition';
  field: string; // Path to evaluate (e.g. "response.status")
  operator: IfConditionOperator;
  value: string; // Comparison target
}

export interface TransformOperation {
  field: string;
  expression: string;
  operation: 'set' | 'delete' | 'rename' | 'template' | 'extract';
}

export interface TransformConfig {
  type: 'transform';
  operations: TransformOperation[];
}

export interface WebhookTriggerConfig {
  type: 'webhook_trigger';
  path: string; // e.g. "/my-webhook"
  method: 'GET' | 'POST' | 'PUT' | 'DELETE';
  responseMode?: 'trigger_only' | 'respond_with_result'; // default: trigger_only
  responseTemplate?: string; // JSON template with {{block-name.field}} placeholders
  testData?: string; // JSON string for test execution (used when clicking Play)
}

export interface ScheduleTriggerConfig {
  type: 'schedule_trigger';
  cronExpression: string; // e.g. "0 9 * * 1-5"
  timezone?: string; // e.g. "America/New_York", defaults to "UTC"
}

export interface ForEachConfig {
  type: 'for_each';
  arrayField: string; // Path to the array in upstream output (e.g., "response.items")
  itemVariable: string; // Variable name for current item (default: "item")
  maxIterations: number; // Safety limit (default: 100)
}

export interface InlineCodeConfig {
  type: 'inline_code';
  language: 'python' | 'javascript';
  code: string; // User-written code to execute
}

export interface SubAgentConfig {
  type: 'sub_agent';
  agentId: string; // ID of the agent to call
  inputMapping: string; // Template for the input (e.g., "{{upstream-block.response}}")
  waitForCompletion: boolean; // Whether to wait for the sub-agent to finish (default: true)
  timeoutSeconds: number; // Max wait time (default: 120)
}

// =============================================================================
// Data Manipulation Block Configs
// =============================================================================

export interface FilterCondition {
  field: string;
  operator: IfConditionOperator;
  value: string;
}

export interface FilterConfig {
  type: 'filter';
  arrayField: string;
  conditions: FilterCondition[];
  mode: 'include' | 'exclude';
  [key: string]: unknown;
}

export interface SwitchCase {
  label: string;
  operator: IfConditionOperator;
  value: string;
}

export interface SwitchConfig {
  type: 'switch';
  field: string;
  cases: SwitchCase[];
  [key: string]: unknown;
}

export interface MergeConfig {
  type: 'merge';
  mode: 'append' | 'merge_by_key' | 'combine_all';
  keyField?: string;
  [key: string]: unknown;
}

export interface AggregateOp {
  outputField: string;
  operation: 'count' | 'sum' | 'avg' | 'min' | 'max' | 'first' | 'last' | 'concat' | 'collect';
  field?: string;
}

export interface AggregateConfig {
  type: 'aggregate';
  arrayField: string;
  groupBy?: string;
  operations: AggregateOp[];
  [key: string]: unknown;
}

export interface SortField {
  field: string;
  direction: 'asc' | 'desc';
  type?: 'string' | 'number' | 'date';
}

export interface SortConfig {
  type: 'sort';
  arrayField: string;
  sortBy: SortField[];
  [key: string]: unknown;
}

export interface LimitConfig {
  type: 'limit';
  arrayField: string;
  count: number;
  position: 'first' | 'last';
  offset?: number;
  [key: string]: unknown;
}

export interface DeduplicateConfig {
  type: 'deduplicate';
  arrayField: string;
  keyField: string;
  keep: 'first' | 'last';
  [key: string]: unknown;
}

export interface WaitConfig {
  type: 'wait';
  duration: number;
  unit: 'ms' | 'seconds' | 'minutes';
  [key: string]: unknown;
}

export interface PythonToolConfig {
  type: 'python_tool';
  toolId: string; // Reference to custom Python tool
  argumentMapping: Record<string, string>;
}

// =============================================================================
// Connection Types
// =============================================================================

export interface Connection {
  id: string;
  sourceBlockId: string;
  sourceOutput: string; // Output port name
  targetBlockId: string;
  targetInput: string; // Named input: "fromWeatherAPI", "fromNewsAPI"
}

// =============================================================================
// Execution Types
// =============================================================================

export type ExecutionStatus = 'pending' | 'running' | 'completed' | 'failed' | 'partial_failure';

export type BlockExecutionStatus = 'pending' | 'running' | 'completed' | 'failed' | 'skipped';

export interface ExecutionContext {
  executionId: string;
  agentId: string;
  status: ExecutionStatus;
  blockStates: Record<string, BlockExecutionState>;
  variables: Record<string, unknown>;
  startedAt: Date;
  completedAt?: Date;
}

export interface BlockExecutionState {
  blockId: string;
  status: BlockExecutionStatus;
  inputs: Record<string, unknown>;
  outputs: Record<string, unknown>;
  error?: string;
  startedAt?: Date;
  completedAt?: Date;
}

export interface IterationState {
  index: number; // 0-based
  status: 'running' | 'completed' | 'failed';
  item: unknown; // The input item for this iteration
  output?: Record<string, unknown>; // Terminal block output
  error?: string;
}

export interface ForEachIterationState {
  blockId: string;
  currentIteration: number; // 1-based (matches backend _iteration)
  totalItems: number;
  iterations: IterationState[];
}

// =============================================================================
// Standardized API Response Types
// Clean, well-structured output for API consumers
// =============================================================================

/**
 * ExecutionAPIResponse is the standardized response for workflow execution.
 * This provides a clean, predictable structure for API consumers.
 */
export interface ExecutionAPIResponse {
  /** Status of the execution: completed, failed, partial */
  status: string;

  /** The primary output from the workflow - the "answer" */
  result: string;

  /** All generated charts, images, visualizations */
  artifacts?: APIArtifact[];

  /** All generated files with download URLs */
  files?: APIFile[];

  /** Detailed output from each block (for debugging/advanced use) */
  blocks?: Record<string, APIBlockOutput>;

  /** Execution statistics */
  metadata: ExecutionMetadata;

  /** Error message if status is failed */
  error?: string;
}

/** Generated artifact (chart, image, etc.) */
export interface APIArtifact {
  type: string; // "chart", "image", "plot"
  format: string; // "png", "jpeg", "svg"
  data: string; // Base64 encoded data
  title?: string; // Description/title
  source_block?: string; // Which block generated this
}

/** Generated file with download URL */
export interface APIFile {
  file_id?: string;
  filename: string;
  download_url: string;
  mime_type?: string;
  size?: number;
  source_block?: string;
}

/** Clean representation of a block's output */
export interface APIBlockOutput {
  name: string;
  type: string;
  status: string;
  response?: string; // Primary text output
  data?: Record<string, unknown>; // Structured data
  error?: string;
  duration_ms?: number;
}

/** Execution statistics */
export interface ExecutionMetadata {
  execution_id: string;
  agent_id?: string;
  workflow_version?: number;
  duration_ms: number;
  total_tokens?: number;
  blocks_executed: number;
  blocks_failed: number;
}

// =============================================================================
// Builder Chat Types
// =============================================================================

export interface BuilderMessage {
  id: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  timestamp: Date;
  workflowUpdate?: WorkflowUpdate;
  executionResult?: ExecutionResultMessage;
}

export interface ExecutionResultMessage {
  status: 'completed' | 'failed' | 'partial_failure';
  result?: string;
  error?: string;
  blocksExecuted?: number;
  blocksFailed?: number;
}

export interface WorkflowUpdate {
  action: 'create' | 'modify';
  workflow: Workflow;
  explanation: string;
  validationErrors?: ValidationError[];
}

export interface ValidationError {
  type: 'schema' | 'cycle' | 'type_mismatch' | 'missing_input';
  message: string;
  blockId?: string;
  connectionId?: string;
}

// =============================================================================
// Custom Python Tool Types
// =============================================================================

export interface PythonTool {
  id: string;
  userId: string;
  name: string;
  displayName: string;
  description: string;
  icon: string; // Lucide icon name
  parameters: ToolParameter[];
  pythonCode: string;
  createdAt: Date;
  updatedAt: Date;
}

export interface ToolParameter {
  name: string;
  type: 'string' | 'number' | 'boolean' | 'array' | 'object';
  description: string;
  required: boolean;
  default?: unknown;
}

// =============================================================================
// Reconnection Types
// =============================================================================

export interface ReconnectPayload {
  currentState: ExecutionContext;
  recentEvents: ExecutionEvent[];
  missedEventCount: number;
}

export interface ExecutionEvent {
  type: 'block_started' | 'block_completed' | 'block_failed' | 'variable_set';
  timestamp: Date;
  data: unknown;
}

// =============================================================================
// WebSocket Message Types (Agent-specific)
// =============================================================================

// Client → Server
export interface WorkflowGenerateMessage {
  type: 'workflow_generate';
  conversationId: string;
  userMessage: string;
  currentWorkflow?: Workflow;
}

export interface AgentExecuteMessage {
  type: 'agent_execute';
  agentId: string;
  inputs?: Record<string, unknown>;
}

export interface BlockExecuteMessage {
  type: 'block_execute';
  agentId: string;
  blockId: string;
  mockInputs?: Record<string, unknown>;
}

// Server → Client
export interface WorkflowUpdateMessage {
  type: 'workflow_update';
  workflow: Workflow;
  explanation: string;
  validationErrors?: ValidationError[];
}

export interface BlockExecutionMessage {
  type: 'block_execution';
  executionId: string;
  blockId: string;
  status: BlockExecutionStatus;
  inputs?: Record<string, unknown>;
  outputs?: Record<string, unknown>;
  error?: string;
}

export interface ExecutionCompleteMessage {
  type: 'execution_complete';
  executionId: string;
  status: ExecutionStatus;
  blockStates: Record<string, BlockExecutionState>;
}

export interface ReconnectStateMessage {
  type: 'reconnect_state';
  payload: ReconnectPayload;
}

// =============================================================================
// UI State Types
// =============================================================================

export interface AgentBuilderUIState {
  selectedAgentId: string | null;
  selectedBlockId: string | null;
  settingsModalBlockId: string | null;
  isGenerating: boolean;
  isSidebarOpen: boolean;
  canvasZoom: number;
  canvasPosition: { x: number; y: number };
}

// =============================================================================
// Block Display Helpers
// =============================================================================

// Block type display info (all supported block types)
export const BLOCK_TYPE_INFO: Record<BlockType, { label: string; icon: string; color: string }> = {
  llm_inference: {
    label: 'AI Agent',
    icon: 'Brain',
    color: 'bg-purple-500',
  },
  variable: {
    label: 'Input',
    icon: 'Variable',
    color: 'bg-orange-500',
  },
  code_block: {
    label: 'Tool',
    icon: 'Wrench',
    color: 'bg-blue-500',
  },
  http_request: {
    label: 'HTTP Request',
    icon: 'Globe',
    color: 'bg-green-500',
  },
  if_condition: {
    label: 'If / Condition',
    icon: 'GitBranch',
    color: 'bg-yellow-500',
  },
  transform: {
    label: 'Transform',
    icon: 'Shuffle',
    color: 'bg-cyan-500',
  },
  webhook_trigger: {
    label: 'Webhook Trigger',
    icon: 'Webhook',
    color: 'bg-rose-500',
  },
  schedule_trigger: {
    label: 'Schedule Trigger',
    icon: 'Clock',
    color: 'bg-indigo-500',
  },
  for_each: {
    label: 'Loop',
    icon: 'Repeat',
    color: 'bg-amber-500',
  },
  inline_code: {
    label: 'Code',
    icon: 'Terminal',
    color: 'bg-emerald-500',
  },
  sub_agent: {
    label: 'Sub-Agent',
    icon: 'Workflow',
    color: 'bg-pink-500',
  },
  filter: {
    label: 'Filter',
    icon: 'Filter',
    color: 'bg-sky-500',
  },
  switch: {
    label: 'Switch',
    icon: 'Route',
    color: 'bg-violet-500',
  },
  merge: {
    label: 'Merge',
    icon: 'Merge',
    color: 'bg-teal-500',
  },
  aggregate: {
    label: 'Aggregate',
    icon: 'BarChart3',
    color: 'bg-fuchsia-500',
  },
  sort: {
    label: 'Sort',
    icon: 'ArrowUpDown',
    color: 'bg-lime-500',
  },
  limit: {
    label: 'Limit',
    icon: 'ListEnd',
    color: 'bg-stone-500',
  },
  deduplicate: {
    label: 'Deduplicate',
    icon: 'CopyMinus',
    color: 'bg-red-400',
  },
  wait: {
    label: 'Wait',
    icon: 'Timer',
    color: 'bg-slate-500',
  },
  knowledge_search: {
    label: 'Knowledge Search',
    icon: 'BookOpen',
    color: 'bg-pink-500',
  },
};

export const BLOCK_STATUS_INFO: Record<BlockExecutionStatus, { label: string; color: string }> = {
  pending: { label: 'Pending', color: 'text-gray-400' },
  running: { label: 'Running', color: 'text-blue-500' },
  completed: { label: 'Completed', color: 'text-green-500' },
  failed: { label: 'Failed', color: 'text-red-500' },
  skipped: { label: 'Skipped', color: 'text-yellow-500' },
};
