import { useState, useEffect, useRef, useCallback } from 'react';
import {
  X,
  Clock,
  Wrench,
  Cpu,
  KeyRound,
  AlertCircle,
  Type,
  Upload,
  Loader2,
  FileText,
  Image,
  Mic,
  File,
  Braces,
  RefreshCw,
  ChevronDown,
  ChevronRight,
  ChevronLeft,
  Copy,
  Check,
  ExternalLink,
  Database,
  Play,
  Code,
  CheckCircle,
  Sparkles,
  Repeat,
  Plus,
  Trash2,
  Terminal as TerminalIcon,
  Clipboard,
  HelpCircle,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { useAgentBuilderStore } from '@/store/useAgentBuilderStore';
import { useModelStore } from '@/store/useModelStore';
import { filterAgentModels } from '@/services/modelService';
import { uploadFile, formatFileSize, checkFileStatus } from '@/services/uploadService';
import type {
  Block,
  FileReference,
  FileType,
  RetryConfig,
  ForEachIterationState,
} from '@/types/agent';
import { getAgentWebhook, autoFillBlock, testBlock } from '@/services/agentService';
import { toast } from '@/store/useToastStore';
import { ToolSelector } from './ToolSelector';
import { IntegrationPicker } from './IntegrationPicker';
import { TemplatePreview } from './TemplatePreview';
import {
  parseCurl,
  exportCurl,
  parseQueryParamsFromUrl,
  buildUrlWithParams,
} from '@/utils/curlUtils';

// File type icon mapping
const FILE_TYPE_ICONS: Record<FileType, React.ElementType> = {
  image: Image,
  document: FileText,
  audio: Mic,
  data: File,
};

// Get file type from MIME type
function getFileTypeFromMime(mimeType: string): FileType {
  if (mimeType.startsWith('image/')) return 'image';
  if (mimeType.startsWith('audio/')) return 'audio';
  if (
    mimeType === 'application/pdf' ||
    mimeType.includes('wordprocessingml') ||
    mimeType.includes('presentationml')
  )
    return 'document';
  return 'data';
}

// Collapsible help guide for block settings
function BlockHelpGuide({ title, children }: { title: string; children: React.ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="border border-[var(--color-border)] rounded-lg overflow-hidden">
      <button
        onClick={() => setOpen(!open)}
        className="w-full flex items-center gap-1.5 px-3 py-2 text-xs font-medium text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] hover:bg-white/5 transition-colors"
      >
        <HelpCircle size={12} />
        <span>How to use {title}</span>
        <ChevronDown
          size={12}
          className={cn('ml-auto transition-transform', open && 'rotate-180')}
        />
      </button>
      {open && (
        <div className="px-3 pb-3 space-y-2 text-xs text-[var(--color-text-secondary)] border-t border-[var(--color-border)]">
          {children}
        </div>
      )}
    </div>
  );
}

interface BlockSettingsPanelProps {
  className?: string;
}

export function BlockSettingsPanel({ className }: BlockSettingsPanelProps) {
  const { workflow, selectedBlockId, selectBlock, updateBlock } = useAgentBuilderStore();

  const block = workflow?.blocks.find(b => b.id === selectedBlockId);

  const [localBlock, setLocalBlock] = useState<Block | null>(null);
  const [hasChanges, setHasChanges] = useState(false);

  // Sync local state when block changes
  useEffect(() => {
    if (block) {
      setLocalBlock({ ...block });
      setHasChanges(false);
    }
  }, [block]);

  const handleSave = () => {
    if (localBlock) {
      updateBlock(localBlock.id, localBlock);
      setHasChanges(false);
    }
  };

  const handleClose = () => {
    selectBlock(null);
  };

  const updateLocalBlock = (updates: Partial<Block>) => {
    setLocalBlock(prev => (prev ? { ...prev, ...updates } : null));
    setHasChanges(true);
  };

  if (!selectedBlockId || !block || !localBlock) {
    return null;
  }

  return (
    <div className={`h-full flex flex-col bg-[var(--color-bg-secondary)] ${className || ''}`}>
      {/* Header */}
      <header className="flex items-center justify-between px-4 py-3 bg-white/5">
        <h2 className="text-sm font-semibold text-[var(--color-text-primary)]">Block Settings</h2>
        <button
          onClick={handleClose}
          className="p-1.5 rounded-md text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] hover:bg-white/10 transition-colors"
        >
          <X size={18} />
        </button>
      </header>

      {/* Content */}
      <div className="flex-1 overflow-y-auto p-4 space-y-4">
        {/* Name */}
        <div className="space-y-1.5">
          <label className="text-xs font-medium text-[var(--color-text-secondary)]">
            Block Name
          </label>
          <input
            type="text"
            value={localBlock.name}
            onChange={e => updateLocalBlock({ name: e.target.value })}
            className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          />
        </div>

        {/* Description */}
        <div className="space-y-1.5">
          <label className="text-xs font-medium text-[var(--color-text-secondary)]">
            Description
          </label>
          <textarea
            value={localBlock.description}
            onChange={e => updateLocalBlock({ description: e.target.value })}
            rows={2}
            className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] resize-none focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          />
        </div>

        {/* Timeout */}
        <div className="space-y-1.5">
          <label className="text-xs font-medium text-[var(--color-text-secondary)] flex items-center gap-1.5">
            <Clock size={14} />
            Execution Timeout
          </label>
          <select
            value={localBlock.timeout}
            onChange={e => updateLocalBlock({ timeout: Number(e.target.value) })}
            className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          >
            <option value={30}>30 seconds (default)</option>
            <option value={45}>45 seconds</option>
            <option value={60}>60 seconds (max)</option>
          </select>
          <p className="text-xs text-[var(--color-text-tertiary)]">
            Maximum time this block can run before timing out
          </p>
        </div>

        {/* Available Input Data from upstream blocks */}
        <UpstreamDataPreview blockId={localBlock.id} />

        {/* AI Auto-Fill Button */}
        {localBlock.type !== 'variable' && (
          <AutoFillButton
            block={localBlock}
            onConfigUpdate={config => {
              updateLocalBlock({ config: { ...localBlock.config, ...config } });
            }}
          />
        )}

        {/* Block Type Specific Settings */}
        <BlockTypeSettings
          block={localBlock}
          onUpdate={config => updateLocalBlock({ config: { ...localBlock.config, ...config } })}
        />

        {/* Retry Settings (for blocks that make external calls) */}
        {['http_request', 'code_block'].includes(localBlock.type) && (
          <RetrySettings
            retryConfig={localBlock.retryConfig}
            onChange={retryConfig => updateLocalBlock({ retryConfig })}
          />
        )}
      </div>

      {/* Footer with Save + Test */}
      <footer className="px-4 py-3 bg-white/5 space-y-2">
        {hasChanges && (
          <button
            onClick={handleSave}
            className="w-full py-2 rounded-lg bg-[var(--color-accent)] text-white font-medium text-sm hover:bg-[var(--color-accent-hover)] transition-colors"
          >
            Save Changes
          </button>
        )}
        {localBlock.type !== 'variable' && <TestBlockButton block={localBlock} />}
      </footer>
    </div>
  );
}

// =============================================================================
// AI Auto-Fill Button — uses AI to fill block config from upstream data
// =============================================================================

function AutoFillButton({
  block,
  onConfigUpdate,
}: {
  block: Block;
  onConfigUpdate: (config: Record<string, unknown>) => void;
}) {
  const { workflow, blockStates, blockOutputCache } = useAgentBuilderStore();
  const [isLoading, setIsLoading] = useState(false);
  const [isExpanded, setIsExpanded] = useState(false);
  const [userContext, setUserContext] = useState('');

  // Gather upstream block outputs
  const getUpstreamData = useCallback((): Record<string, Record<string, unknown>> => {
    if (!workflow) return {};
    const data: Record<string, Record<string, unknown>> = {};
    const upstreamConns = workflow.connections.filter(c => c.targetBlockId === block.id);
    for (const conn of upstreamConns) {
      const srcBlock = workflow.blocks.find(b => b.id === conn.sourceBlockId);
      if (!srcBlock) continue;
      // Prefer blockOutputCache (richer execution data), fall back to blockStates
      const outputs =
        (blockOutputCache[srcBlock.id] as Record<string, unknown> | undefined) ||
        blockStates[srcBlock.id]?.outputs;
      if (outputs && Object.keys(outputs).length > 0) {
        data[srcBlock.normalizedId] = outputs as Record<string, unknown>;
      }
    }
    return data;
  }, [workflow, block.id, blockStates, blockOutputCache]);

  // Find model ID from workflow
  const getModelId = useCallback((): string => {
    if (!workflow) return '';
    // 1. Workflow-level model (selected in toolbar)
    if (workflow.workflowModelId) return workflow.workflowModelId;
    // 2. First llm_inference block's modelId
    for (const b of workflow.blocks) {
      if (b.type === 'llm_inference') {
        const mid = (b.config as Record<string, unknown>).modelId;
        if (typeof mid === 'string' && mid) return mid;
      }
    }
    // 3. Legacy: variable block with workflowModelId
    for (const b of workflow.blocks) {
      if (b.type === 'variable') {
        const mid = (b.config as Record<string, unknown>).workflowModelId;
        if (typeof mid === 'string' && mid) return mid;
      }
    }
    return '';
  }, [workflow]);

  // Get tool schema for code_block
  const getToolInfo = useCallback(async (): Promise<{
    toolName?: string;
    toolSchema?: Record<string, unknown>;
  }> => {
    if (block.type !== 'code_block') return {};
    const toolName = (block.config as Record<string, unknown>).toolName as string | undefined;
    if (!toolName) return {};
    try {
      const { fetchTools } = await import('@/services/toolsService');
      const resp = await fetchTools();
      for (const cat of resp.categories) {
        for (const tool of cat.tools) {
          if (tool.name === toolName && tool.parameters) {
            return {
              toolName,
              toolSchema: tool.parameters as Record<string, unknown>,
            };
          }
        }
      }
    } catch {
      // ignore
    }
    return { toolName };
  }, [block.type, block.config]);

  const upstreamData = getUpstreamData();
  const hasUpstreamData = Object.keys(upstreamData).length > 0;

  const handleAutoFill = async () => {
    if (!hasUpstreamData) return;
    setIsLoading(true);
    try {
      const modelId = getModelId();
      const toolInfo = await getToolInfo();

      // Strip credentials from config before sending to AI
      const safeConfig = Object.fromEntries(
        Object.entries((block.config as Record<string, unknown>) || {}).filter(
          ([k]) => k !== 'credentials' && k !== 'credentials_id'
        )
      );

      const response = await autoFillBlock({
        model_id: modelId,
        block_type: block.type,
        block_name: block.name,
        tool_name: toolInfo.toolName,
        tool_schema: toolInfo.toolSchema,
        current_config: safeConfig,
        upstream_data: upstreamData,
        user_context: userContext.trim() || undefined,
      });

      if (response.config && Object.keys(response.config).length > 0) {
        // Strip any credentials the LLM may have hallucinated
        const safeResponse = Object.fromEntries(
          Object.entries(response.config).filter(
            ([k]) => k !== 'credentials' && k !== 'credentials_id'
          )
        );
        onConfigUpdate(safeResponse);
        toast.success('Block config auto-filled from upstream data');
      } else {
        toast.warning('AI returned no suggestions for this block');
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Auto-fill failed';
      toast.error(msg);
    } finally {
      setIsLoading(false);
    }
  };

  // Collapsed: just the button
  if (!isExpanded) {
    return (
      <button
        onClick={() => (hasUpstreamData ? setIsExpanded(true) : undefined)}
        disabled={!hasUpstreamData}
        title={
          !hasUpstreamData
            ? 'Run the workflow first to generate upstream data'
            : 'Use AI to auto-fill config from upstream data'
        }
        className={`w-full flex items-center justify-center gap-2 px-3 py-2 rounded-lg text-sm font-medium transition-all ${
          !hasUpstreamData
            ? 'bg-white/5 text-[var(--color-text-tertiary)] cursor-not-allowed opacity-50'
            : 'bg-purple-500/10 text-purple-400 hover:bg-purple-500/20 hover:text-purple-300 cursor-pointer'
        }`}
      >
        <Sparkles size={15} />
        AI Auto-Fill
        {!hasUpstreamData && (
          <span className="text-[10px] font-normal opacity-70 ml-1">(run workflow first)</span>
        )}
      </button>
    );
  }

  // Expanded: context textarea + fill button
  return (
    <div className="rounded-lg border border-purple-500/20 bg-purple-500/5 p-3 space-y-2">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-1.5 text-xs font-medium text-purple-400">
          <Sparkles size={13} />
          AI Auto-Fill
        </div>
        <button
          onClick={() => setIsExpanded(false)}
          className="text-[var(--color-text-tertiary)] hover:text-[var(--color-text-secondary)] transition-colors"
        >
          <X size={14} />
        </button>
      </div>
      <textarea
        value={userContext}
        onChange={e => setUserContext(e.target.value)}
        placeholder="Add context to help AI fill accurately (optional)&#10;e.g. Sheet ID is 1BxiM..., columns are Name, Email, Score&#10;e.g. Post to #alerts channel, include timestamp"
        rows={3}
        className="w-full px-2.5 py-2 rounded-md bg-white/5 text-xs text-[var(--color-text-primary)] placeholder:text-[var(--color-text-tertiary)] resize-none focus:outline-none focus:ring-1 focus:ring-purple-500/30"
      />
      <button
        onClick={handleAutoFill}
        disabled={isLoading}
        className={`w-full flex items-center justify-center gap-2 px-3 py-2 rounded-md text-xs font-medium transition-all ${
          isLoading
            ? 'bg-purple-500/20 text-purple-300 cursor-wait'
            : 'bg-purple-500/20 text-purple-300 hover:bg-purple-500/30 cursor-pointer'
        }`}
      >
        {isLoading ? <Loader2 size={13} className="animate-spin" /> : <Sparkles size={13} />}
        {isLoading ? 'Auto-filling...' : 'Fill Configuration'}
      </button>
    </div>
  );
}

// =============================================================================
// Test Block Button — run a single block in isolation with upstream data
// =============================================================================

function TestBlockButton({ block }: { block: Block }) {
  const { workflow, blockStates, updateBlockExecution } = useAgentBuilderStore();
  const [isTesting, setIsTesting] = useState(false);
  const [testResult, setTestResult] = useState<{
    status: 'completed' | 'failed';
    output?: Record<string, unknown>;
    error?: string;
    duration_ms: number;
    _note?: string;
  } | null>(null);
  const [showOutput, setShowOutput] = useState(false);
  const isTriggerBlock = block.type === 'webhook_trigger' || block.type === 'schedule_trigger';

  // Gather upstream outputs (same logic as AutoFillButton)
  const getUpstreamOutputs = useCallback((): Record<string, Record<string, unknown>> => {
    if (!workflow) return {};
    const data: Record<string, Record<string, unknown>> = {};
    const upstreamConns = workflow.connections.filter(c => c.targetBlockId === block.id);
    for (const conn of upstreamConns) {
      const srcBlock = workflow.blocks.find(b => b.id === conn.sourceBlockId);
      if (!srcBlock) continue;
      const outputs = blockStates[srcBlock.id]?.outputs;
      if (outputs && Object.keys(outputs).length > 0) {
        data[srcBlock.normalizedId] = outputs as Record<string, unknown>;
      }
    }
    return data;
  }, [workflow, block.id, blockStates]);

  const upstreamOutputs = getUpstreamOutputs();
  // Check if the block is a start/first block (no incoming connections) or has upstream data
  const incomingConns = workflow?.connections.filter(c => c.targetBlockId === block.id);
  const isStartBlock = !incomingConns || incomingConns.length === 0;
  const hasUpstreamData = isStartBlock || Object.keys(upstreamOutputs).length > 0;

  const handleTest = async () => {
    // For trigger blocks, use the saved testData from block config as the test payload
    let parsedPayload: Record<string, unknown> | undefined;
    if (isTriggerBlock) {
      const savedTestData = (block.config as Record<string, unknown>).testData as string | undefined;
      const raw = savedTestData?.trim() || '{}';
      try {
        parsedPayload = JSON.parse(raw);
      } catch {
        // fall back to empty body — WebhookTriggerSettings shows its own JSON error
        parsedPayload = {};
      }
    }

    setIsTesting(true);
    setTestResult(null);
    try {
      const response = await testBlock({
        block: {
          id: block.id,
          normalizedId: block.normalizedId,
          type: block.type,
          name: block.name,
          description: block.description,
          config: block.config as Record<string, unknown>,
          timeout: block.timeout,
        },
        upstream_outputs: upstreamOutputs,
        test_payload: parsedPayload,
      });

      setTestResult(response);
      setShowOutput(true);

      // Update block states in the store so downstream blocks can see the output
      if (response.status === 'completed' && response.output) {
        updateBlockExecution(block.id, {
          blockId: block.id,
          status: 'completed',
          inputs: upstreamOutputs,
          outputs: response.output,
          completedAt: new Date(),
        });
        toast.success(`Block executed in ${response.duration_ms}ms`);
      } else {
        updateBlockExecution(block.id, {
          blockId: block.id,
          status: 'failed',
          inputs: upstreamOutputs,
          outputs: {},
          error: response.error,
          completedAt: new Date(),
        });
        toast.error(response.error || 'Block execution failed');
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Test execution failed';
      setTestResult({
        status: 'failed',
        error: msg,
        duration_ms: 0,
      });
      setShowOutput(true);
      toast.error(msg);
    } finally {
      setIsTesting(false);
    }
  };

  return (
    <div className="space-y-2">
      {/* Trigger blocks: test data is configured in the block settings panel above */}
      {isTriggerBlock && (
        <p className="text-[10px] text-[var(--color-text-tertiary)] flex items-center gap-1">
          <Braces size={10} />
          Test data is taken from the <strong>Test Data</strong> field in the settings above.
        </p>
      )}

      <button
        onClick={handleTest}
        disabled={isTesting || !hasUpstreamData}
        title={
          !hasUpstreamData
            ? 'Run the workflow first to generate upstream data'
            : 'Execute this block alone with existing upstream data'
        }
        className={`w-full flex items-center justify-center gap-2 py-2 rounded-lg text-sm font-medium transition-all ${
          isTesting
            ? 'bg-emerald-500/20 text-emerald-300 cursor-wait'
            : !hasUpstreamData
              ? 'bg-white/5 text-[var(--color-text-tertiary)] cursor-not-allowed opacity-50'
              : 'bg-white/5 text-[var(--color-text-secondary)] hover:bg-emerald-500/10 hover:text-emerald-400 cursor-pointer'
        }`}
      >
        {isTesting ? <Loader2 size={15} className="animate-spin" /> : <Play size={15} />}
        {isTesting ? 'Testing...' : 'Test Block'}
        {!hasUpstreamData && (
          <span className="text-[10px] font-normal opacity-70 ml-1">(run workflow first)</span>
        )}
      </button>

      {/* Inline test result */}
      {testResult && showOutput && (
        <div
          className={`rounded-lg border p-3 space-y-2 ${
            testResult.status === 'completed'
              ? 'border-emerald-500/20 bg-emerald-500/5'
              : 'border-red-500/20 bg-red-500/5'
          }`}
        >
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-1.5">
              {testResult.status === 'completed' ? (
                <CheckCircle size={13} className="text-emerald-400" />
              ) : (
                <AlertCircle size={13} className="text-red-400" />
              )}
              <span
                className={`text-xs font-medium ${
                  testResult.status === 'completed' ? 'text-emerald-400' : 'text-red-400'
                }`}
              >
                {testResult.status === 'completed' ? 'Success' : 'Failed'}
              </span>
              <span className="text-[10px] text-[var(--color-text-tertiary)]">
                {testResult.duration_ms}ms
              </span>
            </div>
            <button
              onClick={() => setShowOutput(false)}
              className="text-[var(--color-text-tertiary)] hover:text-[var(--color-text-secondary)]"
            >
              <X size={12} />
            </button>
          </div>

          {testResult.status === 'failed' && testResult.error && (
            <p className="text-xs text-red-400/80 break-all">{testResult.error}</p>
          )}

          {testResult._note && (
            <p className="text-[10px] text-amber-400/80 italic">{testResult._note}</p>
          )}

          {testResult.status === 'completed' && testResult.output && (
            <pre className="text-[10px] text-[var(--color-text-secondary)] bg-black/20 rounded p-2 max-h-32 overflow-auto whitespace-pre-wrap break-all">
              {JSON.stringify(testResult.output, null, 2).slice(0, 2000)}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}

// Tools that require credentials - maps to integration types
// This should match the backend ToolIntegrationMap
const TOOLS_REQUIRING_CREDENTIALS: Record<string, string> = {
  send_discord_message: 'Discord',
  send_slack_message: 'Slack',
  send_telegram_message: 'Telegram',
  send_google_chat_message: 'Google Chat',
  send_webhook: 'Webhook',
};

// Helper to get tools that need credentials from a list of selected tools
function getToolsNeedingCredentials(selectedTools: string[]): string[] {
  return selectedTools
    .filter(tool => tool in TOOLS_REQUIRING_CREDENTIALS)
    .map(tool => TOOLS_REQUIRING_CREDENTIALS[tool]);
}

// Wrapper to connect IterationBrowser to the store
function ForEachIterationBrowser({ blockId }: { blockId: string }) {
  const { forEachStates } = useAgentBuilderStore();
  return <IterationBrowser state={forEachStates[blockId]} />;
}

// Iteration Browser Component for for-each blocks
function IterationBrowser({ state }: { state: ForEachIterationState | undefined }) {
  const [selectedIdx, setSelectedIdx] = useState(0);

  if (!state || state.iterations.length === 0) {
    return (
      <div className="mt-4 pt-4 border-t border-[var(--color-border)]">
        <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide mb-2">
          Iteration Results
        </h3>
        <p className="text-xs text-[var(--color-text-tertiary)] italic py-4 text-center">
          Run the workflow to see iteration results.
        </p>
      </div>
    );
  }

  const { iterations, totalItems } = state;
  const passedCount = iterations.filter(i => i?.status === 'completed').length;
  const failedCount = iterations.filter(i => i?.status === 'failed').length;
  const selected = iterations[selectedIdx];
  const useCompactSelector = totalItems <= 20;

  return (
    <div className="mt-4 pt-4 border-t border-[var(--color-border)]">
      {/* Header */}
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide flex items-center gap-1.5">
          <Repeat size={12} />
          Iteration Results
        </h3>
        <div className="flex items-center gap-1.5 text-[10px]">
          {passedCount > 0 && (
            <span className="px-1.5 py-0.5 rounded bg-green-500/15 text-green-400 font-medium">
              {passedCount} passed
            </span>
          )}
          {failedCount > 0 && (
            <span className="px-1.5 py-0.5 rounded bg-red-500/15 text-red-400 font-medium">
              {failedCount} failed
            </span>
          )}
        </div>
      </div>

      {/* Selector Row */}
      <div className="flex items-center gap-1.5 mb-3">
        <button
          onClick={() => setSelectedIdx(Math.max(0, selectedIdx - 1))}
          disabled={selectedIdx === 0}
          className="p-1 rounded hover:bg-white/5 text-[var(--color-text-tertiary)] disabled:opacity-30 transition-colors"
        >
          <ChevronLeft size={14} />
        </button>

        {useCompactSelector ? (
          <div className="flex flex-wrap gap-1 flex-1">
            {iterations.map((iter, idx) => (
              <button
                key={idx}
                onClick={() => setSelectedIdx(idx)}
                className={cn(
                  'w-6 h-6 rounded text-[10px] font-medium transition-colors',
                  idx === selectedIdx
                    ? 'bg-[var(--color-accent)] text-white'
                    : iter?.status === 'failed'
                      ? 'bg-red-500/15 text-red-400 hover:bg-red-500/25'
                      : iter?.status === 'completed'
                        ? 'bg-white/5 text-[var(--color-text-secondary)] hover:bg-white/10'
                        : 'bg-white/5 text-[var(--color-text-tertiary)] hover:bg-white/10'
                )}
              >
                {idx + 1}
              </button>
            ))}
          </div>
        ) : (
          <div className="flex items-center gap-2 flex-1">
            <input
              type="number"
              min={1}
              max={totalItems}
              value={selectedIdx + 1}
              onChange={e => {
                const val = parseInt(e.target.value);
                if (val >= 1 && val <= totalItems) setSelectedIdx(val - 1);
              }}
              className="w-16 px-2 py-1 rounded text-xs text-center bg-white/5 border border-[var(--color-border)] text-[var(--color-text-primary)] focus:outline-none focus:border-[var(--color-accent)]"
            />
            <span className="text-xs text-[var(--color-text-tertiary)]">of {totalItems}</span>
          </div>
        )}

        <button
          onClick={() => setSelectedIdx(Math.min(totalItems - 1, selectedIdx + 1))}
          disabled={selectedIdx >= totalItems - 1}
          className="p-1 rounded hover:bg-white/5 text-[var(--color-text-tertiary)] disabled:opacity-30 transition-colors"
        >
          <ChevronRight size={14} />
        </button>
      </div>

      {/* Selected Iteration Detail */}
      {selected ? (
        <div className="rounded-lg border border-[var(--color-border)] overflow-hidden">
          {/* Status Bar */}
          <div
            className={cn(
              'flex items-center justify-between px-3 py-2 text-xs font-medium',
              selected.status === 'failed'
                ? 'bg-red-500/10 text-red-400'
                : selected.status === 'completed'
                  ? 'bg-green-500/10 text-green-400'
                  : 'bg-blue-500/10 text-blue-400'
            )}
          >
            <span>Item {selectedIdx + 1}</span>
            <span className="capitalize">{selected.status}</span>
          </div>

          <div className="p-3 space-y-3">
            {/* Input Item */}
            {selected.item !== undefined && (
              <div>
                <h4 className="text-[10px] font-semibold text-[var(--color-text-tertiary)] uppercase tracking-wide mb-1">
                  Input Item
                </h4>
                <pre className="text-xs text-[var(--color-text-secondary)] font-mono bg-white/5 rounded p-2 max-h-[100px] overflow-auto whitespace-pre-wrap break-words">
                  {typeof selected.item === 'string'
                    ? selected.item
                    : JSON.stringify(selected.item, null, 2)}
                </pre>
              </div>
            )}

            {/* Output */}
            {selected.output && (
              <div>
                <h4 className="text-[10px] font-semibold text-[var(--color-text-tertiary)] uppercase tracking-wide mb-1">
                  Output
                </h4>
                <pre className="text-xs text-[var(--color-text-secondary)] font-mono bg-white/5 rounded p-2 max-h-[150px] overflow-auto whitespace-pre-wrap break-words">
                  {JSON.stringify(selected.output, null, 2)}
                </pre>
              </div>
            )}

            {/* Error */}
            {selected.error && (
              <div>
                <h4 className="text-[10px] font-semibold text-red-400 uppercase tracking-wide mb-1">
                  Error
                </h4>
                <p className="text-xs text-red-300 bg-red-500/10 rounded p-2 break-words">
                  {selected.error}
                </p>
              </div>
            )}
          </div>
        </div>
      ) : (
        <p className="text-xs text-[var(--color-text-tertiary)] italic text-center py-2">
          No data for this iteration
        </p>
      )}
    </div>
  );
}

// Block Type Specific Settings Component
interface BlockTypeSettingsProps {
  block: Block;
  onUpdate: (config: Record<string, unknown>) => void;
}

function BlockTypeSettings({ block, onUpdate }: BlockTypeSettingsProps) {
  const config = block.config;

  switch (block.type) {
    case 'llm_inference': {
      // Handle both old format (modelId) and new AI-generated format (systemPrompt/enabledTools)
      // Get tools from either 'tools' or 'enabledTools' key
      const tools = (config.tools as string[]) || (config.enabledTools as string[]) || [];

      return (
        <LLMInferenceSettings config={config} onUpdate={onUpdate} tools={tools} block={block} />
      );
    }

    case 'webhook':
      if ('url' in config) {
        return (
          <div className="space-y-4 pt-4">
            <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
              Webhook Settings
            </h3>

            <div className="space-y-1.5">
              <label className="text-xs font-medium text-[var(--color-text-secondary)]">URL</label>
              <input
                type="url"
                value={config.url}
                onChange={e => onUpdate({ url: e.target.value })}
                placeholder="https://api.example.com/webhook"
                className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
              />
            </div>

            <div className="space-y-1.5">
              <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                Method
              </label>
              <select
                value={config.method}
                onChange={e =>
                  onUpdate({
                    method: e.target.value as 'GET' | 'POST' | 'PUT' | 'DELETE',
                  })
                }
                className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
              >
                <option value="GET">GET</option>
                <option value="POST">POST</option>
                <option value="PUT">PUT</option>
                <option value="DELETE">DELETE</option>
              </select>
            </div>

            <div className="space-y-1.5">
              <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                Body Template
              </label>
              <textarea
                value={config.bodyTemplate}
                onChange={e => onUpdate({ bodyTemplate: e.target.value })}
                rows={4}
                placeholder='{"data": "{{input.result}}"}'
                className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono resize-none focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
              />
              <TemplatePreview value={(config.bodyTemplate as string) || ''} />
              <p className="text-xs text-[var(--color-text-tertiary)]">
                Use {'{{input.fieldName}}'} for variable interpolation
              </p>
            </div>
          </div>
        );
      }
      break;

    case 'variable':
      if ('operation' in config) {
        return <VariableBlockSettings config={config} onUpdate={onUpdate} />;
      }
      break;

    case 'code_block':
      return <CodeBlockSettings config={config} onUpdate={onUpdate} block={block} />;

    case 'http_request':
      return <HTTPRequestSettings config={config} onUpdate={onUpdate} />;

    case 'if_condition': {
      const field = ('field' in config ? (config.field as string) : '') || '';
      const operator =
        ('operator' in config ? (config.operator as string) : 'is_true') || 'is_true';
      const value = ('value' in config ? (config.value as string) : '') || '';

      const needsValue = !['is_empty', 'not_empty', 'is_true', 'is_false'].includes(operator);

      return (
        <div className="space-y-4 pt-4">
          <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
            Condition Settings
          </h3>

          {/* Field Path */}
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Field to Evaluate
            </label>
            <input
              type="text"
              value={field}
              onChange={e => onUpdate({ field: e.target.value })}
              placeholder="response.status"
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            />
            <TemplatePreview value={field} />
            <p className="text-xs text-[var(--color-text-tertiary)]">
              Path in the input data (e.g., &quot;response&quot;, &quot;response.status&quot;)
            </p>
          </div>

          {/* Operator */}
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Operator
            </label>
            <select
              value={operator}
              onChange={e => onUpdate({ operator: e.target.value })}
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            >
              <optgroup label="Comparison">
                <option value="eq">Equals (==)</option>
                <option value="neq">Not Equals (!=)</option>
                <option value="gt">Greater Than (&gt;)</option>
                <option value="lt">Less Than (&lt;)</option>
                <option value="gte">Greater or Equal (&gt;=)</option>
                <option value="lte">Less or Equal (&lt;=)</option>
              </optgroup>
              <optgroup label="String">
                <option value="contains">Contains</option>
                <option value="not_contains">Does Not Contain</option>
                <option value="starts_with">Starts With</option>
                <option value="ends_with">Ends With</option>
              </optgroup>
              <optgroup label="Boolean">
                <option value="is_true">Is True</option>
                <option value="is_false">Is False</option>
                <option value="is_empty">Is Empty</option>
                <option value="not_empty">Not Empty</option>
              </optgroup>
            </select>
          </div>

          {/* Value */}
          {needsValue && (
            <div className="space-y-1.5">
              <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                Compare Value
              </label>
              <input
                type="text"
                value={value}
                onChange={e => onUpdate({ value: e.target.value })}
                placeholder="Expected value..."
                className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
              />
              <TemplatePreview value={value} />
            </div>
          )}

          {/* Info Box */}
          <div className="p-3 rounded-lg bg-yellow-500/10 border border-yellow-500/30">
            <p className="text-xs text-yellow-400">
              <strong>Routing:</strong> This block has two output handles — True and False. Connect
              downstream blocks to the appropriate branch.
            </p>
          </div>
        </div>
      );
    }

    case 'transform': {
      const operations =
        ('operations' in config
          ? (config.operations as { field: string; expression: string; operation: string }[])
          : []) || [];

      const addOperation = () => {
        onUpdate({
          operations: [...operations, { field: '', expression: '', operation: 'set' }],
        });
      };

      const updateOperation = (index: number, updates: Record<string, string>) => {
        const updated = operations.map((op, i) => (i === index ? { ...op, ...updates } : op));
        onUpdate({ operations: updated });
      };

      const removeOperation = (index: number) => {
        onUpdate({ operations: operations.filter((_, i) => i !== index) });
      };

      return (
        <div className="space-y-4 pt-4">
          <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
            Transform Operations
          </h3>

          {operations.map((op, i) => (
            <div key={i} className="p-3 rounded-lg bg-white/5 space-y-2 relative">
              <button
                onClick={() => removeOperation(i)}
                className="absolute top-2 right-2 p-1 rounded text-[var(--color-text-tertiary)] hover:text-red-400 hover:bg-red-500/10"
              >
                <X size={12} />
              </button>

              <select
                value={op.operation}
                onChange={e => updateOperation(i, { operation: e.target.value })}
                className="w-full px-2 py-1.5 rounded bg-white/5 text-xs text-[var(--color-text-primary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
              >
                <option value="set">Set</option>
                <option value="delete">Delete</option>
                <option value="rename">Rename</option>
                <option value="template">Template</option>
                <option value="extract">Extract</option>
              </select>

              <input
                type="text"
                value={op.field}
                onChange={e => updateOperation(i, { field: e.target.value })}
                placeholder="Field path (e.g., result.name)"
                className="w-full px-2 py-1.5 rounded bg-white/5 text-xs text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
              />

              {op.operation !== 'delete' && (
                <input
                  type="text"
                  value={op.expression}
                  onChange={e => updateOperation(i, { expression: e.target.value })}
                  placeholder={
                    op.operation === 'rename'
                      ? 'New field name'
                      : op.operation === 'template'
                        ? '{{block-id.response}} processed'
                        : 'Value or expression'
                  }
                  className="w-full px-2 py-1.5 rounded bg-white/5 text-xs text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
                />
              )}
            </div>
          ))}

          <button
            onClick={addOperation}
            className="w-full py-2 rounded-lg border border-dashed border-[var(--color-border)] text-xs text-[var(--color-text-secondary)] hover:border-[var(--color-accent)]/50 hover:text-[var(--color-accent)] transition-colors"
          >
            + Add Operation
          </button>
        </div>
      );
    }

    case 'webhook_trigger': {
      const triggerMethod = ('method' in config ? (config.method as string) : 'POST') || 'POST';

      return <WebhookTriggerSettings config={config} method={triggerMethod} onUpdate={onUpdate} />;
    }

    case 'schedule_trigger': {
      return <ScheduleTriggerSettings config={config} onUpdate={onUpdate} />;
    }

    case 'for_each': {
      const arrayField =
        ('arrayField' in config ? (config.arrayField as string) : 'response') || 'response';
      const itemVariable =
        ('itemVariable' in config ? (config.itemVariable as string) : 'item') || 'item';
      const maxIterations =
        ('maxIterations' in config ? (config.maxIterations as number) : 100) || 100;

      return (
        <div className="space-y-4 pt-4">
          <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
            Loop Settings
          </h3>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Array Field
            </label>
            <input
              type="text"
              value={arrayField}
              onChange={e => onUpdate({ arrayField: e.target.value })}
              placeholder="response.items"
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            />
            <p className="text-xs text-[var(--color-text-tertiary)]">
              Path to the array in upstream output (e.g., &quot;response&quot; or
              &quot;response.items&quot;)
            </p>
          </div>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Item Variable Name
            </label>
            <input
              type="text"
              value={itemVariable}
              onChange={e => onUpdate({ itemVariable: e.target.value })}
              placeholder="item"
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            />
            <p className="text-xs text-[var(--color-text-tertiary)]">
              Downstream blocks can reference the current item via{' '}
              {'{{loop-block-name.' + itemVariable + '}}'}
            </p>
          </div>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Max Iterations
            </label>
            <input
              type="number"
              value={maxIterations}
              onChange={e => onUpdate({ maxIterations: parseInt(e.target.value) || 100 })}
              min={1}
              max={1000}
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            />
            <p className="text-xs text-[var(--color-text-tertiary)]">
              Safety limit to prevent runaway loops
            </p>
          </div>

          <div className="p-3 rounded-lg bg-amber-500/10 border border-amber-500/30">
            <p className="text-xs text-amber-400">
              <strong>Routing:</strong> Connect the &quot;Each&quot; output to blocks that should
              run per item. Connect the &quot;Done&quot; output to blocks that run after all
              iterations complete.
            </p>
          </div>

          {/* Iteration Results Browser */}
          <ForEachIterationBrowser blockId={block.id} />
        </div>
      );
    }

    case 'inline_code': {
      const language = ('language' in config ? (config.language as string) : 'python') || 'python';
      const code = ('code' in config ? (config.code as string) : '') || '';

      return (
        <div className="space-y-4 pt-4">
          <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
            Code Settings
          </h3>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Language
            </label>
            <select
              value={language}
              onChange={e => onUpdate({ language: e.target.value })}
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            >
              <option value="python">Python</option>
              <option value="javascript">JavaScript</option>
            </select>
          </div>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">Code</label>
            <textarea
              value={code}
              onChange={e => onUpdate({ code: e.target.value })}
              rows={12}
              spellCheck={false}
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono resize-y focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50 leading-relaxed"
              placeholder={
                language === 'python'
                  ? '# Access upstream data via `inputs` dict\n# Set `output` to return results\noutput = inputs.get("response", "")'
                  : '// Access upstream data via `inputs` object\n// Return result with `output`\nconst output = inputs.response || "";'
              }
            />
          </div>

          <div className="p-3 rounded-lg bg-emerald-500/10 border border-emerald-500/30">
            <p className="text-xs text-emerald-400">
              <strong>Variables:</strong> <code className="font-mono">inputs</code> contains all
              upstream block outputs.{' '}
              {language === 'python'
                ? 'Set `output` to return a result.'
                : 'Assign to `output` to return a result.'}
            </p>
          </div>
        </div>
      );
    }

    case 'sub_agent': {
      return <SubAgentSettings config={config} onUpdate={onUpdate} />;
    }

    case 'filter': {
      const arrayField = ('arrayField' in config ? (config.arrayField as string) : '') || '';
      const mode = ('mode' in config ? (config.mode as string) : 'include') || 'include';
      const conditions =
        ('conditions' in config ? (config.conditions as Record<string, string>[]) : []) || [];

      return (
        <div className="space-y-4 pt-4">
          <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
            Filter Settings
          </h3>

          <BlockHelpGuide title="Filter">
            <p className="pt-2">
              <strong>What it does:</strong> Removes items from an array that don&apos;t match your
              conditions — like a SQL WHERE clause.
            </p>
            <p>
              <strong>Configuration:</strong>
            </p>
            <ul className="list-disc pl-4 space-y-1">
              <li>
                <strong>Array Field</strong> — path to the array in upstream output
              </li>
              <li>
                <strong>Mode</strong> — Include keeps matches, Exclude removes them
              </li>
              <li>
                <strong>Conditions</strong> — field/operator/value rules (all must match)
              </li>
            </ul>
            <p>
              <strong>Example:</strong> Filter orders where <code>amount &gt; 100</code> and{' '}
              <code>status = &quot;active&quot;</code>
            </p>
            <p>
              <strong>Output:</strong> <code>{'{ items: [...], count: N, originalCount: N }'}</code>
            </p>
          </BlockHelpGuide>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Array Field
            </label>
            <input
              type="text"
              value={arrayField}
              onChange={e => onUpdate({ arrayField: e.target.value })}
              placeholder="response.items"
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            />
            <p className="text-xs text-[var(--color-text-tertiary)]">
              Path to the array in upstream output (e.g., &quot;response&quot; or
              &quot;response.items&quot;)
            </p>
          </div>
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">Mode</label>
            <select
              value={mode}
              onChange={e => onUpdate({ mode: e.target.value })}
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            >
              <option value="include">Include matching</option>
              <option value="exclude">Exclude matching</option>
            </select>
            <p className="text-xs text-[var(--color-text-tertiary)]">
              Include keeps items that match; Exclude removes items that match
            </p>
          </div>
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                Conditions (AND)
              </label>
              <button
                onClick={() =>
                  onUpdate({
                    conditions: [...conditions, { field: '', operator: 'eq', value: '' }],
                  })
                }
                className="text-xs text-[var(--color-accent)] hover:underline flex items-center gap-1"
              >
                <Plus size={12} /> Add
              </button>
            </div>
            {conditions.map((cond: Record<string, string>, i: number) => (
              <div
                key={i}
                className="flex gap-1.5 items-start p-2 rounded-lg bg-white/5 border border-[var(--color-border)]"
              >
                <input
                  type="text"
                  value={cond.field || ''}
                  onChange={e => {
                    const updated = [...conditions];
                    updated[i] = { ...updated[i], field: e.target.value };
                    onUpdate({ conditions: updated });
                  }}
                  placeholder="field"
                  className="flex-1 px-2 py-1.5 rounded bg-white/5 text-xs font-mono text-[var(--color-text-primary)] focus:outline-none"
                />
                <select
                  value={cond.operator || 'eq'}
                  onChange={e => {
                    const updated = [...conditions];
                    updated[i] = { ...updated[i], operator: e.target.value };
                    onUpdate({ conditions: updated });
                  }}
                  className="px-2 py-1.5 rounded bg-white/5 text-xs text-[var(--color-text-primary)] focus:outline-none"
                >
                  <option value="eq">=</option>
                  <option value="neq">!=</option>
                  <option value="gt">&gt;</option>
                  <option value="lt">&lt;</option>
                  <option value="gte">&gt;=</option>
                  <option value="lte">&lt;=</option>
                  <option value="contains">contains</option>
                  <option value="not_contains">!contains</option>
                  <option value="is_empty">empty</option>
                  <option value="not_empty">!empty</option>
                  <option value="is_true">true</option>
                  <option value="is_false">false</option>
                </select>
                <input
                  type="text"
                  value={cond.value || ''}
                  onChange={e => {
                    const updated = [...conditions];
                    updated[i] = { ...updated[i], value: e.target.value };
                    onUpdate({ conditions: updated });
                  }}
                  placeholder="value"
                  className="flex-1 px-2 py-1.5 rounded bg-white/5 text-xs font-mono text-[var(--color-text-primary)] focus:outline-none"
                />
                <button
                  onClick={() => {
                    const updated = conditions.filter(
                      (_: Record<string, string>, idx: number) => idx !== i
                    );
                    onUpdate({ conditions: updated });
                  }}
                  className="p-1 text-red-400 hover:text-red-300"
                >
                  <Trash2 size={12} />
                </button>
              </div>
            ))}
          </div>

          <div className="p-3 rounded-lg bg-sky-500/10 border border-sky-500/30">
            <p className="text-xs text-sky-400">
              All conditions are AND&apos;d together. Use template syntax{' '}
              <code className="font-mono">{'{{block.field}}'}</code> in values to reference upstream
              data.
            </p>
          </div>
        </div>
      );
    }

    case 'switch': {
      const field = ('field' in config ? (config.field as string) : '') || '';
      const cases = ('cases' in config ? (config.cases as Record<string, string>[]) : []) || [];

      return (
        <div className="space-y-4 pt-4">
          <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
            Switch Settings
          </h3>

          <BlockHelpGuide title="Switch">
            <p className="pt-2">
              <strong>What it does:</strong> Routes data to different branches based on a
              field&apos;s value — like a switch/case statement. First match wins.
            </p>
            <p>
              <strong>Configuration:</strong>
            </p>
            <ul className="list-disc pl-4 space-y-1">
              <li>
                <strong>Field</strong> — path to the value to evaluate
              </li>
              <li>
                <strong>Cases</strong> — each case label becomes an output port name
              </li>
            </ul>
            <p>
              <strong>Example:</strong> Route by <code>order.status</code> — cases
              &quot;pending&quot;, &quot;shipped&quot;, &quot;delivered&quot; each go to different
              downstream blocks.
            </p>
            <p>
              <strong>Output:</strong> Each matching case port receives the full input data.
              Unmatched data goes to &quot;Default&quot;.
            </p>
          </BlockHelpGuide>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Field to Evaluate
            </label>
            <input
              type="text"
              value={field}
              onChange={e => onUpdate({ field: e.target.value })}
              placeholder="response.status"
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            />
            <p className="text-xs text-[var(--color-text-tertiary)]">
              Path to the value to evaluate (e.g., &quot;response.status&quot;,
              &quot;data.type&quot;)
            </p>
          </div>
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                Cases (first match wins)
              </label>
              <button
                onClick={() =>
                  onUpdate({
                    cases: [
                      ...cases,
                      { label: `case_${cases.length + 1}`, operator: 'eq', value: '' },
                    ],
                  })
                }
                className="text-xs text-[var(--color-accent)] hover:underline flex items-center gap-1"
              >
                <Plus size={12} /> Add Case
              </button>
            </div>
            {cases.map((c: Record<string, string>, i: number) => (
              <div
                key={i}
                className="flex gap-1.5 items-start p-2 rounded-lg bg-white/5 border border-[var(--color-border)]"
              >
                <input
                  type="text"
                  value={c.label || ''}
                  onChange={e => {
                    const updated = [...cases];
                    updated[i] = { ...updated[i], label: e.target.value };
                    onUpdate({ cases: updated });
                  }}
                  placeholder="label"
                  className="w-20 px-2 py-1.5 rounded bg-white/5 text-xs font-mono text-[var(--color-text-primary)] focus:outline-none"
                />
                <select
                  value={c.operator || 'eq'}
                  onChange={e => {
                    const updated = [...cases];
                    updated[i] = { ...updated[i], operator: e.target.value };
                    onUpdate({ cases: updated });
                  }}
                  className="px-2 py-1.5 rounded bg-white/5 text-xs text-[var(--color-text-primary)] focus:outline-none"
                >
                  <option value="eq">=</option>
                  <option value="neq">!=</option>
                  <option value="gt">&gt;</option>
                  <option value="lt">&lt;</option>
                  <option value="contains">contains</option>
                  <option value="starts_with">starts</option>
                  <option value="ends_with">ends</option>
                </select>
                <input
                  type="text"
                  value={c.value || ''}
                  onChange={e => {
                    const updated = [...cases];
                    updated[i] = { ...updated[i], value: e.target.value };
                    onUpdate({ cases: updated });
                  }}
                  placeholder="value"
                  className="flex-1 px-2 py-1.5 rounded bg-white/5 text-xs font-mono text-[var(--color-text-primary)] focus:outline-none"
                />
                <button
                  onClick={() => {
                    const updated = cases.filter(
                      (_: Record<string, string>, idx: number) => idx !== i
                    );
                    onUpdate({ cases: updated });
                  }}
                  className="p-1 text-red-400 hover:text-red-300"
                >
                  <Trash2 size={12} />
                </button>
              </div>
            ))}
            <p className="text-[10px] text-[var(--color-text-tertiary)]">
              Unmatched values route to the &quot;Default&quot; output port.
            </p>
          </div>

          <div className="p-3 rounded-lg bg-violet-500/10 border border-violet-500/30">
            <p className="text-xs text-violet-400">
              <strong>Routing:</strong> Each case label creates an output port. Connect downstream
              blocks to the matching port. First matching case wins.
            </p>
          </div>
        </div>
      );
    }

    case 'merge': {
      const mode = ('mode' in config ? (config.mode as string) : 'append') || 'append';
      const keyField = ('keyField' in config ? (config.keyField as string) : '') || '';

      return (
        <div className="space-y-4 pt-4">
          <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
            Merge Settings
          </h3>

          <BlockHelpGuide title="Merge">
            <p className="pt-2">
              <strong>What it does:</strong> Combines data from multiple upstream blocks into one
              output.
            </p>
            <p>
              <strong>Modes:</strong>
            </p>
            <ul className="list-disc pl-4 space-y-1">
              <li>
                <strong>Append</strong> — concatenates arrays from all inputs into one flat array
              </li>
              <li>
                <strong>Merge by Key</strong> — joins objects by a shared field (like SQL JOIN)
              </li>
              <li>
                <strong>Combine All</strong> — wraps each input under its source block name
              </li>
            </ul>
            <p>
              <strong>Example:</strong> Merge API results + database results into a single list.
            </p>
          </BlockHelpGuide>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">Mode</label>
            <select
              value={mode}
              onChange={e => onUpdate({ mode: e.target.value })}
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            >
              <option value="append">Append (concat arrays)</option>
              <option value="merge_by_key">Merge by Key (join objects)</option>
              <option value="combine_all">Combine All (keyed by source)</option>
            </select>
            <p className="text-xs text-[var(--color-text-tertiary)]">
              {mode === 'append'
                ? 'Arrays from all inputs are concatenated into one flat array'
                : mode === 'merge_by_key'
                  ? 'Objects are joined by a shared key field (like SQL JOIN)'
                  : 'Each input is wrapped under its source block name'}
            </p>
          </div>
          {mode === 'merge_by_key' && (
            <div className="space-y-1.5">
              <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                Key Field
              </label>
              <input
                type="text"
                value={keyField}
                onChange={e => onUpdate({ keyField: e.target.value })}
                placeholder="id"
                className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
              />
              <p className="text-xs text-[var(--color-text-tertiary)]">
                Shared field to join objects on (like a SQL JOIN key — e.g., &quot;id&quot;,
                &quot;email&quot;)
              </p>
            </div>
          )}
          <div className="p-3 rounded-lg bg-teal-500/10 border border-teal-500/30">
            <p className="text-xs text-teal-400">
              Connect multiple upstream blocks to this merge node. All inputs are collected and
              combined based on the selected mode.
            </p>
          </div>
        </div>
      );
    }

    case 'aggregate': {
      const arrayField = ('arrayField' in config ? (config.arrayField as string) : '') || '';
      const groupBy = ('groupBy' in config ? (config.groupBy as string) : '') || '';
      const operations =
        ('operations' in config ? (config.operations as Record<string, string>[]) : []) || [];

      return (
        <div className="space-y-4 pt-4">
          <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
            Aggregate Settings
          </h3>

          <BlockHelpGuide title="Aggregate">
            <p className="pt-2">
              <strong>What it does:</strong> Groups and summarizes array data — like SQL GROUP BY
              with aggregate functions (SUM, COUNT, AVG, etc.).
            </p>
            <p>
              <strong>Configuration:</strong>
            </p>
            <ul className="list-disc pl-4 space-y-1">
              <li>
                <strong>Array Field</strong> — path to the array to aggregate
              </li>
              <li>
                <strong>Group By</strong> — field to group items by (leave empty to aggregate all)
              </li>
              <li>
                <strong>Operations</strong> — output name + function + source field
              </li>
            </ul>
            <p>
              <strong>Example:</strong> Sum revenue by category — Group By &quot;category&quot;, Op:
              Sum on &quot;amount&quot; → output &quot;total_revenue&quot;.
            </p>
            <p>
              <strong>Output:</strong> Array of groups, each with the group key + computed fields.
            </p>
          </BlockHelpGuide>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Array Field
            </label>
            <input
              type="text"
              value={arrayField}
              onChange={e => onUpdate({ arrayField: e.target.value })}
              placeholder="response.items"
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            />
            <p className="text-xs text-[var(--color-text-tertiary)]">
              Path to the array in upstream output (e.g., &quot;response&quot; or
              &quot;response.items&quot;)
            </p>
          </div>
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Group By (optional)
            </label>
            <input
              type="text"
              value={groupBy}
              onChange={e => onUpdate({ groupBy: e.target.value })}
              placeholder="category"
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            />
            <p className="text-xs text-[var(--color-text-tertiary)]">
              Leave empty to aggregate all items as one group
            </p>
          </div>
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                Operations
              </label>
              <button
                onClick={() =>
                  onUpdate({
                    operations: [...operations, { outputField: '', operation: 'count', field: '' }],
                  })
                }
                className="text-xs text-[var(--color-accent)] hover:underline flex items-center gap-1"
              >
                <Plus size={12} /> Add
              </button>
            </div>
            {operations.map((op: Record<string, string>, i: number) => (
              <div
                key={i}
                className="flex gap-1.5 items-start p-2 rounded-lg bg-white/5 border border-[var(--color-border)]"
              >
                <input
                  type="text"
                  value={op.outputField || ''}
                  onChange={e => {
                    const updated = [...operations];
                    updated[i] = { ...updated[i], outputField: e.target.value };
                    onUpdate({ operations: updated });
                  }}
                  placeholder="output name"
                  className="flex-1 px-2 py-1.5 rounded bg-white/5 text-xs font-mono text-[var(--color-text-primary)] focus:outline-none"
                />
                <select
                  value={op.operation || 'count'}
                  onChange={e => {
                    const updated = [...operations];
                    updated[i] = { ...updated[i], operation: e.target.value };
                    onUpdate({ operations: updated });
                  }}
                  className="px-2 py-1.5 rounded bg-white/5 text-xs text-[var(--color-text-primary)] focus:outline-none"
                >
                  <option value="count">Count</option>
                  <option value="sum">Sum</option>
                  <option value="avg">Average</option>
                  <option value="min">Min</option>
                  <option value="max">Max</option>
                  <option value="first">First</option>
                  <option value="last">Last</option>
                  <option value="concat">Concat</option>
                  <option value="collect">Collect</option>
                </select>
                <input
                  type="text"
                  value={op.field || ''}
                  onChange={e => {
                    const updated = [...operations];
                    updated[i] = { ...updated[i], field: e.target.value };
                    onUpdate({ operations: updated });
                  }}
                  placeholder="field"
                  className="flex-1 px-2 py-1.5 rounded bg-white/5 text-xs font-mono text-[var(--color-text-primary)] focus:outline-none"
                />
                <button
                  onClick={() => {
                    const updated = operations.filter(
                      (_: Record<string, string>, idx: number) => idx !== i
                    );
                    onUpdate({ operations: updated });
                  }}
                  className="p-1 text-red-400 hover:text-red-300"
                >
                  <Trash2 size={12} />
                </button>
              </div>
            ))}
            <p className="text-[10px] text-[var(--color-text-tertiary)]">
              Output name becomes the key in the result. Count doesn&apos;t need a field.
            </p>
          </div>

          <div className="p-3 rounded-lg bg-fuchsia-500/10 border border-fuchsia-500/30">
            <p className="text-xs text-fuchsia-400">
              Leave Group By empty to aggregate the entire array as one group. Use Count without a
              field to count items.
            </p>
          </div>
        </div>
      );
    }

    case 'sort': {
      const arrayField = ('arrayField' in config ? (config.arrayField as string) : '') || '';
      const sortBy = ('sortBy' in config ? (config.sortBy as Record<string, string>[]) : []) || [];

      return (
        <div className="space-y-4 pt-4">
          <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
            Sort Settings
          </h3>

          <BlockHelpGuide title="Sort">
            <p className="pt-2">
              <strong>What it does:</strong> Sorts array items by one or more fields. Multiple sort
              fields create multi-level ordering.
            </p>
            <p>
              <strong>Configuration:</strong>
            </p>
            <ul className="list-disc pl-4 space-y-1">
              <li>
                <strong>Array Field</strong> — path to the array to sort
              </li>
              <li>
                <strong>Sort By</strong> — field name, direction (asc/desc), and type
              </li>
            </ul>
            <p>
              <strong>Example:</strong> Sort users by role (ascending), then by name (ascending) for
              a grouped alphabetical list.
            </p>
            <p>
              <strong>Output:</strong> <code>{'{ items: [...sorted], count: N }'}</code>
            </p>
          </BlockHelpGuide>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Array Field
            </label>
            <input
              type="text"
              value={arrayField}
              onChange={e => onUpdate({ arrayField: e.target.value })}
              placeholder="response.items"
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            />
            <p className="text-xs text-[var(--color-text-tertiary)]">
              Path to the array in upstream output (e.g., &quot;response&quot; or
              &quot;response.items&quot;)
            </p>
          </div>
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                Sort By
              </label>
              <button
                onClick={() =>
                  onUpdate({
                    sortBy: [...sortBy, { field: '', direction: 'asc', type: 'auto' }],
                  })
                }
                className="text-xs text-[var(--color-accent)] hover:underline flex items-center gap-1"
              >
                <Plus size={12} /> Add
              </button>
            </div>
            {sortBy.map((sf: Record<string, string>, i: number) => (
              <div
                key={i}
                className="flex gap-1.5 items-start p-2 rounded-lg bg-white/5 border border-[var(--color-border)]"
              >
                <input
                  type="text"
                  value={sf.field || ''}
                  onChange={e => {
                    const updated = [...sortBy];
                    updated[i] = { ...updated[i], field: e.target.value };
                    onUpdate({ sortBy: updated });
                  }}
                  placeholder="field"
                  className="flex-1 px-2 py-1.5 rounded bg-white/5 text-xs font-mono text-[var(--color-text-primary)] focus:outline-none"
                />
                <select
                  value={sf.direction || 'asc'}
                  onChange={e => {
                    const updated = [...sortBy];
                    updated[i] = { ...updated[i], direction: e.target.value };
                    onUpdate({ sortBy: updated });
                  }}
                  className="px-2 py-1.5 rounded bg-white/5 text-xs text-[var(--color-text-primary)] focus:outline-none"
                >
                  <option value="asc">Ascending</option>
                  <option value="desc">Descending</option>
                </select>
                <select
                  value={sf.type || 'auto'}
                  onChange={e => {
                    const updated = [...sortBy];
                    updated[i] = { ...updated[i], type: e.target.value };
                    onUpdate({ sortBy: updated });
                  }}
                  className="px-2 py-1.5 rounded bg-white/5 text-xs text-[var(--color-text-primary)] focus:outline-none"
                >
                  <option value="auto">Auto</option>
                  <option value="number">Number</option>
                  <option value="string">String</option>
                  <option value="date">Date</option>
                </select>
                <button
                  onClick={() => {
                    const updated = sortBy.filter(
                      (_: Record<string, string>, idx: number) => idx !== i
                    );
                    onUpdate({ sortBy: updated });
                  }}
                  className="p-1 text-red-400 hover:text-red-300"
                >
                  <Trash2 size={12} />
                </button>
              </div>
            ))}
            <p className="text-[10px] text-[var(--color-text-tertiary)]">
              Add multiple fields for multi-level sorting (first field is primary).
            </p>
          </div>

          <div className="p-3 rounded-lg bg-lime-500/10 border border-lime-500/30">
            <p className="text-xs text-lime-400">
              Auto type detects numbers vs strings. Use Date type for ISO date strings (YYYY-MM-DD).
            </p>
          </div>
        </div>
      );
    }

    case 'limit': {
      const arrayField = ('arrayField' in config ? (config.arrayField as string) : '') || '';
      const count = ('count' in config ? Number(config.count) : 10) || 10;
      const position = ('position' in config ? (config.position as string) : 'first') || 'first';
      const offset = 'offset' in config ? Number(config.offset) : 0;

      return (
        <div className="space-y-4 pt-4">
          <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
            Limit Settings
          </h3>

          <BlockHelpGuide title="Limit">
            <p className="pt-2">
              <strong>What it does:</strong> Takes a slice of array items for pagination — like SQL
              LIMIT/OFFSET.
            </p>
            <p>
              <strong>Configuration:</strong>
            </p>
            <ul className="list-disc pl-4 space-y-1">
              <li>
                <strong>Count</strong> — how many items to take
              </li>
              <li>
                <strong>Position</strong> — take from the start (First N) or end (Last N)
              </li>
              <li>
                <strong>Offset</strong> — skip this many items before taking
              </li>
            </ul>
            <p>
              <strong>Example:</strong> Page 2 of 10 items → Count: 10, Offset: 10, Position: First
              N.
            </p>
            <p>
              <strong>Output:</strong> <code>{'{ items: [...], count: N, totalCount: N }'}</code>
            </p>
          </BlockHelpGuide>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Array Field
            </label>
            <input
              type="text"
              value={arrayField}
              onChange={e => onUpdate({ arrayField: e.target.value })}
              placeholder="response.items"
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            />
            <p className="text-xs text-[var(--color-text-tertiary)]">
              Path to the array in upstream output (e.g., &quot;response&quot; or
              &quot;response.items&quot;)
            </p>
          </div>
          <div className="flex gap-3">
            <div className="flex-1 space-y-1.5">
              <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                Count
              </label>
              <input
                type="number"
                value={count}
                min={1}
                onChange={e => onUpdate({ count: parseInt(e.target.value) || 10 })}
                className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
              />
              <p className="text-xs text-[var(--color-text-tertiary)]">Number of items to take</p>
            </div>
            <div className="flex-1 space-y-1.5">
              <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                Position
              </label>
              <select
                value={position}
                onChange={e => onUpdate({ position: e.target.value })}
                className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
              >
                <option value="first">First N</option>
                <option value="last">Last N</option>
              </select>
              <p className="text-xs text-[var(--color-text-tertiary)]">
                Take from start or end of array
              </p>
            </div>
          </div>
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Offset (skip)
            </label>
            <input
              type="number"
              value={offset}
              min={0}
              onChange={e => onUpdate({ offset: parseInt(e.target.value) || 0 })}
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            />
            <p className="text-xs text-[var(--color-text-tertiary)]">
              Skip this many items before taking (for pagination)
            </p>
          </div>

          <div className="p-3 rounded-lg bg-stone-500/10 border border-stone-500/30">
            <p className="text-xs text-stone-400">
              Combine with a Sort block upstream for ordered pagination.
            </p>
          </div>
        </div>
      );
    }

    case 'deduplicate': {
      const arrayField = ('arrayField' in config ? (config.arrayField as string) : '') || '';
      const keyField = ('keyField' in config ? (config.keyField as string) : '') || '';
      const keep = ('keep' in config ? (config.keep as string) : 'first') || 'first';

      return (
        <div className="space-y-4 pt-4">
          <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
            Deduplicate Settings
          </h3>

          <BlockHelpGuide title="Deduplicate">
            <p className="pt-2">
              <strong>What it does:</strong> Removes duplicate items from an array by comparing a
              key field.
            </p>
            <p>
              <strong>Configuration:</strong>
            </p>
            <ul className="list-disc pl-4 space-y-1">
              <li>
                <strong>Array Field</strong> — path to the array to deduplicate
              </li>
              <li>
                <strong>Key Field</strong> — field to compare for duplicates (e.g.,
                &quot;email&quot;)
              </li>
              <li>
                <strong>Keep</strong> — which occurrence to keep when duplicates are found
              </li>
            </ul>
            <p>
              <strong>Example:</strong> Deduplicate a mailing list by &quot;email&quot; — keeps only
              unique recipients.
            </p>
            <p>
              <strong>Output:</strong> <code>{'{ items: [...], count: N, removedCount: N }'}</code>
            </p>
          </BlockHelpGuide>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Array Field
            </label>
            <input
              type="text"
              value={arrayField}
              onChange={e => onUpdate({ arrayField: e.target.value })}
              placeholder="response.items"
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            />
            <p className="text-xs text-[var(--color-text-tertiary)]">
              Path to the array in upstream output (e.g., &quot;response&quot; or
              &quot;response.items&quot;)
            </p>
          </div>
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Key Field
            </label>
            <input
              type="text"
              value={keyField}
              onChange={e => onUpdate({ keyField: e.target.value })}
              placeholder="id"
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            />
            <p className="text-xs text-[var(--color-text-tertiary)]">
              e.g., &quot;id&quot;, &quot;email&quot; — items with the same value are considered
              duplicates
            </p>
          </div>
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">Keep</label>
            <select
              value={keep}
              onChange={e => onUpdate({ keep: e.target.value })}
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            >
              <option value="first">First occurrence</option>
              <option value="last">Last occurrence</option>
            </select>
            <p className="text-xs text-[var(--color-text-tertiary)]">
              When duplicates are found, keep the first or last occurrence
            </p>
          </div>

          <div className="p-3 rounded-lg bg-red-500/10 border border-red-500/30">
            <p className="text-xs text-red-400">
              Items are compared by their key field value (converted to string). Order of the
              original array is preserved.
            </p>
          </div>
        </div>
      );
    }

    case 'wait': {
      const duration = 'duration' in config ? Number(config.duration) : 1;
      const unit = ('unit' in config ? (config.unit as string) : 'seconds') || 'seconds';

      return (
        <div className="space-y-4 pt-4">
          <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
            Wait Settings
          </h3>

          <BlockHelpGuide title="Wait">
            <p className="pt-2">
              <strong>What it does:</strong> Pauses workflow execution for a specified duration
              before continuing. Useful for rate limiting between API calls.
            </p>
            <p>
              <strong>Configuration:</strong>
            </p>
            <ul className="list-disc pl-4 space-y-1">
              <li>
                <strong>Duration</strong> — how long to wait
              </li>
              <li>
                <strong>Unit</strong> — milliseconds, seconds, or minutes
              </li>
            </ul>
            <p>
              <strong>Example:</strong> Add a 2-second delay between API requests to avoid rate
              limits.
            </p>
            <p>
              <strong>Output:</strong> Passes all input data through unchanged.
            </p>
          </BlockHelpGuide>

          <div className="flex gap-3">
            <div className="flex-1 space-y-1.5">
              <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                Duration
              </label>
              <input
                type="number"
                value={duration}
                min={0}
                step={unit === 'ms' ? 100 : 1}
                onChange={e => onUpdate({ duration: parseFloat(e.target.value) || 1 })}
                className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
              />
              <p className="text-xs text-[var(--color-text-tertiary)]">How long to pause</p>
            </div>
            <div className="flex-1 space-y-1.5">
              <label className="text-xs font-medium text-[var(--color-text-secondary)]">Unit</label>
              <select
                value={unit}
                onChange={e => onUpdate({ unit: e.target.value })}
                className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
              >
                <option value="ms">Milliseconds</option>
                <option value="seconds">Seconds</option>
                <option value="minutes">Minutes</option>
              </select>
              <p className="text-xs text-[var(--color-text-tertiary)]">
                Time unit for the duration
              </p>
            </div>
          </div>
          <div className="p-3 rounded-lg bg-slate-500/10 border border-slate-500/30">
            <p className="text-xs text-slate-400">
              Pauses execution then passes all input data through unchanged. Max 5 minutes.
            </p>
          </div>
        </div>
      );
    }

    case 'knowledge_search': {
      // Project IDs config can arrive as string[] (from default config),
      // a single comma-separated string (legacy / hand-edited), or
      // undefined (fresh block). Normalize to string[].
      let projectIds: string[] = [];
      const rawIds = (config as Record<string, unknown>).project_ids;
      if (Array.isArray(rawIds)) {
        projectIds = rawIds.filter((x): x is string => typeof x === 'string');
      } else if (typeof rawIds === 'string') {
        projectIds = rawIds
          .split(',')
          .map(s => s.trim())
          .filter(Boolean);
      }
      const query = typeof config.query === 'string' ? config.query : '{{input.query}}';
      const topK = typeof config.top_k === 'number' ? config.top_k : 5;
      const rerank = config.rerank === undefined ? true : Boolean(config.rerank);

      return (
        <KnowledgeSearchSettings
          projectIds={projectIds}
          query={query}
          topK={topK}
          rerank={rerank}
          onUpdate={onUpdate}
        />
      );
    }

    default:
      return (
        <div className="pt-4">
          <p className="text-xs text-[var(--color-text-tertiary)]">
            No additional settings for this block type.
          </p>
        </div>
      );
  }

  return null;
}

// ============================================================================
// Schedule Trigger Settings — friendly frequency selector
// ============================================================================

type ScheduleFrequency = 'minutes' | 'hourly' | 'daily' | 'weekly' | 'monthly';

const FREQUENCY_OPTIONS: { key: ScheduleFrequency; label: string; icon: string }[] = [
  { key: 'minutes', label: 'Every X Min', icon: '⏱' },
  { key: 'hourly', label: 'Hourly', icon: '🕐' },
  { key: 'daily', label: 'Daily', icon: '📅' },
  { key: 'weekly', label: 'Weekly', icon: '📆' },
  { key: 'monthly', label: 'Monthly', icon: '🗓' },
];

const COMMON_TIMEZONES = [
  { value: 'UTC', label: 'UTC (Coordinated Universal Time)' },
  { value: 'America/New_York', label: 'US Eastern (New York)' },
  { value: 'America/Chicago', label: 'US Central (Chicago)' },
  { value: 'America/Denver', label: 'US Mountain (Denver)' },
  { value: 'America/Los_Angeles', label: 'US Pacific (Los Angeles)' },
  { value: 'Europe/London', label: 'UK (London)' },
  { value: 'Europe/Paris', label: 'Europe Central (Paris)' },
  { value: 'Europe/Berlin', label: 'Europe Central (Berlin)' },
  { value: 'Asia/Tokyo', label: 'Japan (Tokyo)' },
  { value: 'Asia/Shanghai', label: 'China (Shanghai)' },
  { value: 'Asia/Kolkata', label: 'India (Kolkata)' },
  { value: 'Asia/Dubai', label: 'Gulf (Dubai)' },
  { value: 'Australia/Sydney', label: 'Australia (Sydney)' },
  { value: 'Pacific/Auckland', label: 'New Zealand (Auckland)' },
];

const WEEKDAY_LABELS = [
  'Monday',
  'Tuesday',
  'Wednesday',
  'Thursday',
  'Friday',
  'Saturday',
  'Sunday',
];

/** Parse a cron expression to detect which frequency mode it matches */
function parseCronToFrequency(cron: string): {
  frequency: ScheduleFrequency;
  minute: number;
  hour: number;
  minuteInterval: number;
  hourInterval: number;
  dayOfWeek: number; // 1=Mon
  dayOfMonth: number;
} {
  const parts = cron.trim().split(/\s+/);
  const [minPart, hourPart, domPart, , dowPart] = parts;
  const minute = minPart?.startsWith('*/') ? 0 : parseInt(minPart || '0', 10) || 0;
  const hour = hourPart === '*' ? 9 : parseInt(hourPart || '9', 10) || 0;

  // Every X minutes: */N * * * *
  if (minPart?.startsWith('*/') && hourPart === '*') {
    return {
      frequency: 'minutes',
      minute,
      hour,
      minuteInterval: parseInt(minPart.slice(2), 10) || 5,
      hourInterval: 1,
      dayOfWeek: 1,
      dayOfMonth: 1,
    };
  }
  // Hourly: 0 */N * * * or M * * * *
  if (hourPart === '*' || hourPart?.startsWith('*/')) {
    const hInterval = hourPart?.startsWith('*/') ? parseInt(hourPart.slice(2), 10) || 1 : 1;
    return {
      frequency: 'hourly',
      minute,
      hour,
      minuteInterval: 5,
      hourInterval: hInterval,
      dayOfWeek: 1,
      dayOfMonth: 1,
    };
  }
  // Weekly: has dow != *
  if (dowPart && dowPart !== '*') {
    const dow = parseInt(dowPart, 10) || 1;
    return {
      frequency: 'weekly',
      minute,
      hour,
      minuteInterval: 5,
      hourInterval: 1,
      dayOfWeek: dow,
      dayOfMonth: 1,
    };
  }
  // Monthly: has dom != *
  if (domPart && domPart !== '*') {
    const dom = parseInt(domPart, 10) || 1;
    return {
      frequency: 'monthly',
      minute,
      hour,
      minuteInterval: 5,
      hourInterval: 1,
      dayOfWeek: 1,
      dayOfMonth: dom,
    };
  }
  // Daily
  return {
    frequency: 'daily',
    minute,
    hour,
    minuteInterval: 5,
    hourInterval: 1,
    dayOfWeek: 1,
    dayOfMonth: 1,
  };
}

/** Build a cron expression from the friendly UI state */
function buildCronExpression(
  frequency: ScheduleFrequency,
  minute: number,
  hour: number,
  minuteInterval: number,
  hourInterval: number,
  dayOfWeek: number,
  dayOfMonth: number
): string {
  switch (frequency) {
    case 'minutes':
      return `*/${minuteInterval} * * * *`;
    case 'hourly':
      return hourInterval === 1 ? `${minute} * * * *` : `${minute} */${hourInterval} * * *`;
    case 'daily':
      return `${minute} ${hour} * * *`;
    case 'weekly':
      return `${minute} ${hour} * * ${dayOfWeek}`;
    case 'monthly':
      return `${minute} ${hour} ${dayOfMonth} * *`;
  }
}

/** Format a cron as human-readable summary */
function cronToHumanSummary(
  frequency: ScheduleFrequency,
  minute: number,
  hour: number,
  minuteInterval: number,
  hourInterval: number,
  dayOfWeek: number,
  dayOfMonth: number
): string {
  const timeStr = `${hour.toString().padStart(2, '0')}:${minute.toString().padStart(2, '0')}`;
  const ampm = hour >= 12 ? 'PM' : 'AM';
  const h12 = hour === 0 ? 12 : hour > 12 ? hour - 12 : hour;
  const friendlyTime = `${h12}:${minute.toString().padStart(2, '0')} ${ampm}`;

  switch (frequency) {
    case 'minutes':
      return `Every ${minuteInterval} minutes`;
    case 'hourly':
      if (hourInterval === 1) return `Every hour at :${minute.toString().padStart(2, '0')}`;
      return `Every ${hourInterval} hours at :${minute.toString().padStart(2, '0')}`;
    case 'daily':
      return `Daily at ${friendlyTime}`;
    case 'weekly':
      return `Every ${WEEKDAY_LABELS[(dayOfWeek - 1 + 7) % 7] || 'Monday'} at ${friendlyTime}`;
    case 'monthly': {
      const suffix =
        dayOfMonth === 1 ? 'st' : dayOfMonth === 2 ? 'nd' : dayOfMonth === 3 ? 'rd' : 'th';
      return `Monthly on the ${dayOfMonth}${suffix} at ${friendlyTime}`;
    }
    default:
      return timeStr;
  }
}

function ScheduleTriggerSettings({
  config,
  onUpdate,
}: {
  config: Record<string, unknown>;
  onUpdate: (updates: Record<string, unknown>) => void;
}) {
  const cronExpression =
    ('cronExpression' in config ? (config.cronExpression as string) : '0 9 * * *') || '0 9 * * *';
  const timezone = (config.timezone as string) || 'UTC';

  const parsed = parseCronToFrequency(cronExpression);
  const [frequency, setFrequency] = useState<ScheduleFrequency>(parsed.frequency);
  const [minute, setMinute] = useState(parsed.minute);
  const [hour, setHour] = useState(parsed.hour);
  const [minuteInterval, setMinuteInterval] = useState(parsed.minuteInterval);
  const [hourInterval, setHourInterval] = useState(parsed.hourInterval);
  const [dayOfWeek, setDayOfWeek] = useState(parsed.dayOfWeek);
  const [dayOfMonth, setDayOfMonth] = useState(parsed.dayOfMonth);

  // Whenever any value changes, rebuild and commit the cron expression
  const commitCron = useCallback(
    (
      f: ScheduleFrequency,
      min: number,
      hr: number,
      minInt: number,
      hrInt: number,
      dow: number,
      dom: number
    ) => {
      const cron = buildCronExpression(f, min, hr, minInt, hrInt, dow, dom);
      onUpdate({ cronExpression: cron });
    },
    [onUpdate]
  );

  const updateFrequency = (f: ScheduleFrequency) => {
    setFrequency(f);
    commitCron(f, minute, hour, minuteInterval, hourInterval, dayOfWeek, dayOfMonth);
  };
  const updateMinute = (m: number) => {
    setMinute(m);
    commitCron(frequency, m, hour, minuteInterval, hourInterval, dayOfWeek, dayOfMonth);
  };
  const updateHour = (h: number) => {
    setHour(h);
    commitCron(frequency, minute, h, minuteInterval, hourInterval, dayOfWeek, dayOfMonth);
  };
  const updateMinuteInterval = (mi: number) => {
    setMinuteInterval(mi);
    commitCron(frequency, minute, hour, mi, hourInterval, dayOfWeek, dayOfMonth);
  };
  const updateHourInterval = (hi: number) => {
    setHourInterval(hi);
    commitCron(frequency, minute, hour, minuteInterval, hi, dayOfWeek, dayOfMonth);
  };
  const updateDayOfWeek = (d: number) => {
    setDayOfWeek(d);
    commitCron(frequency, minute, hour, minuteInterval, hourInterval, d, dayOfMonth);
  };
  const updateDayOfMonth = (d: number) => {
    setDayOfMonth(d);
    commitCron(frequency, minute, hour, minuteInterval, hourInterval, dayOfWeek, d);
  };

  const summary = cronToHumanSummary(
    frequency,
    minute,
    hour,
    minuteInterval,
    hourInterval,
    dayOfWeek,
    dayOfMonth
  );

  return (
    <div className="space-y-4 pt-4">
      <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
        Schedule Trigger
      </h3>

      {/* Frequency selector */}
      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)]">
          How often should this run?
        </label>
        <div className="flex gap-1 flex-wrap">
          {FREQUENCY_OPTIONS.map(opt => (
            <button
              key={opt.key}
              type="button"
              onClick={() => updateFrequency(opt.key)}
              className={`flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-[11px] font-medium transition-colors border ${
                frequency === opt.key
                  ? 'bg-[var(--color-accent)]/15 border-[var(--color-accent)] text-[var(--color-accent)]'
                  : 'bg-white/5 border-transparent text-[var(--color-text-secondary)] hover:bg-white/10'
              }`}
            >
              <span className="text-xs">{opt.icon}</span>
              {opt.label}
            </button>
          ))}
        </div>
      </div>

      {/* Every X Minutes config */}
      {frequency === 'minutes' && (
        <div className="space-y-1.5">
          <label className="text-xs font-medium text-[var(--color-text-secondary)]">
            Run every
          </label>
          <div className="flex items-center gap-2">
            <select
              value={minuteInterval}
              onChange={e => updateMinuteInterval(parseInt(e.target.value))}
              className="px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
            >
              {[1, 2, 5, 10, 15, 20, 30, 45].map(m => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </select>
            <span className="text-xs text-[var(--color-text-tertiary)]">minutes</span>
          </div>
        </div>
      )}

      {/* Hourly config */}
      {frequency === 'hourly' && (
        <div className="space-y-3">
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Run every
            </label>
            <div className="flex items-center gap-2">
              <select
                value={hourInterval}
                onChange={e => updateHourInterval(parseInt(e.target.value))}
                className="px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
              >
                {[1, 2, 3, 4, 6, 8, 12].map(h => (
                  <option key={h} value={h}>
                    {h}
                  </option>
                ))}
              </select>
              <span className="text-xs text-[var(--color-text-tertiary)]">
                {hourInterval === 1 ? 'hour' : 'hours'}
              </span>
            </div>
          </div>
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              At minute
            </label>
            <select
              value={minute}
              onChange={e => updateMinute(parseInt(e.target.value))}
              className="px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
            >
              {[0, 5, 10, 15, 20, 30, 45].map(m => (
                <option key={m} value={m}>
                  :{m.toString().padStart(2, '0')}
                </option>
              ))}
            </select>
          </div>
        </div>
      )}

      {/* Daily / Weekly / Monthly — time picker */}
      {(frequency === 'daily' || frequency === 'weekly' || frequency === 'monthly') && (
        <div className="space-y-3">
          {/* Day of week (weekly) */}
          {frequency === 'weekly' && (
            <div className="space-y-1.5">
              <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                On day
              </label>
              <select
                value={dayOfWeek}
                onChange={e => updateDayOfWeek(parseInt(e.target.value))}
                className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
              >
                {WEEKDAY_LABELS.map((label, i) => (
                  <option key={label} value={i + 1}>
                    {label}
                  </option>
                ))}
              </select>
            </div>
          )}

          {/* Day of month (monthly) */}
          {frequency === 'monthly' && (
            <div className="space-y-1.5">
              <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                On day of month
              </label>
              <select
                value={dayOfMonth}
                onChange={e => updateDayOfMonth(parseInt(e.target.value))}
                className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
              >
                {Array.from({ length: 28 }, (_, i) => i + 1).map(d => (
                  <option key={d} value={d}>
                    {d}
                    {d === 1 ? 'st' : d === 2 ? 'nd' : d === 3 ? 'rd' : 'th'}
                  </option>
                ))}
              </select>
            </div>
          )}

          {/* Time picker */}
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              At time
            </label>
            <div className="flex items-center gap-2">
              <select
                value={hour}
                onChange={e => updateHour(parseInt(e.target.value))}
                className="px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
              >
                {Array.from({ length: 24 }, (_, i) => {
                  const ampm = i >= 12 ? 'PM' : 'AM';
                  const h12 = i === 0 ? 12 : i > 12 ? i - 12 : i;
                  return (
                    <option key={i} value={i}>
                      {h12.toString().padStart(2, '0')} {ampm}
                    </option>
                  );
                })}
              </select>
              <span className="text-sm text-[var(--color-text-tertiary)]">:</span>
              <select
                value={minute}
                onChange={e => updateMinute(parseInt(e.target.value))}
                className="px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
              >
                {[0, 15, 30, 45].map(m => (
                  <option key={m} value={m}>
                    {m.toString().padStart(2, '0')}
                  </option>
                ))}
              </select>
            </div>
          </div>
        </div>
      )}

      {/* Human-readable summary */}
      <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-white/5 border border-[var(--color-border)]">
        <Clock size={14} className="text-[var(--color-accent)] flex-shrink-0" />
        <span className="text-xs font-medium text-[var(--color-text-primary)]">{summary}</span>
      </div>

      {/* Timezone */}
      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)]">Timezone</label>
        <select
          value={timezone}
          onChange={e => onUpdate({ timezone: e.target.value })}
          className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
        >
          {COMMON_TIMEZONES.map(tz => (
            <option key={tz.value} value={tz.value}>
              {tz.label}
            </option>
          ))}
        </select>
      </div>

      <div className="p-3 rounded-lg bg-green-500/10 border border-green-500/30">
        <p className="text-xs text-green-400">
          <strong>Auto-registered:</strong> When you deploy this agent, the schedule will be
          registered automatically with distributed locking to prevent duplicate runs.
        </p>
      </div>
    </div>
  );
}

// Webhook Trigger Settings with test data editor and URL display
function WebhookTriggerSettings({
  config,
  method,
  onUpdate,
}: {
  config: Record<string, unknown>;
  method: string;
  onUpdate: (updates: Record<string, unknown>) => void;
}) {
  const [copied, setCopied] = useState(false);
  const [testDataError, setTestDataError] = useState<string | null>(null);
  const { currentAgent } = useAgentBuilderStore();

  const testData = (config.testData as string) || '{\n  "message": "Hello from webhook"\n}';
  const responseMode = (config.responseMode as string) || 'trigger_only';
  const responseTemplate = (config.responseTemplate as string) || '';
  const [templateError, setTemplateError] = useState<string | null>(null);

  // Fetch webhook slug from backend, build URL with frontend's base URL
  const [webhookUrl, setWebhookUrl] = useState<string | null>(null);
  const apiBase = import.meta.env.VITE_API_BASE_URL || window.location.origin;
  useEffect(() => {
    if (!currentAgent?.id) return;
    getAgentWebhook(currentAgent.id).then(info => {
      setWebhookUrl(info?.path ? `${apiBase}/api/wh/${info.path}` : null);
    });
  }, [currentAgent?.id, apiBase]);

  const handleTestDataChange = (value: string) => {
    onUpdate({ testData: value });
    // Validate JSON
    try {
      JSON.parse(value);
      setTestDataError(null);
    } catch {
      setTestDataError('Invalid JSON');
    }
  };

  const handleCopyUrl = () => {
    if (webhookUrl) {
      navigator.clipboard.writeText(webhookUrl);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  const handleCopyCurl = () => {
    if (webhookUrl) {
      const curl = `curl -X ${method} ${webhookUrl} \\\n  -H "Content-Type: application/json" \\\n  -d '${testData.replace(/\n/g, '')}'`;
      navigator.clipboard.writeText(curl);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  return (
    <div className="space-y-4 pt-4">
      <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
        Webhook Trigger
      </h3>

      {/* Webhook URL (shown when deployed) */}
      {webhookUrl ? (
        <div className="space-y-1.5">
          <label className="text-xs font-medium text-green-400 flex items-center gap-1.5">
            <ExternalLink size={14} />
            Live Webhook URL
          </label>
          <div className="flex items-center gap-2">
            <code className="flex-1 px-3 py-2 rounded-lg bg-green-500/10 border border-green-500/30 text-xs text-green-300 font-mono truncate">
              {webhookUrl}
            </code>
            <button
              onClick={handleCopyUrl}
              className="p-2 rounded-lg bg-white/5 hover:bg-white/10 transition-colors flex-shrink-0"
              title="Copy URL"
            >
              {copied ? <Check size={14} className="text-green-400" /> : <Copy size={14} />}
            </button>
          </div>
          <button
            onClick={handleCopyCurl}
            className="text-xs text-[var(--color-accent)] hover:underline"
          >
            Copy as cURL command
          </button>
        </div>
      ) : (
        <div className="p-3 rounded-lg bg-amber-500/10 border border-amber-500/30">
          <p className="text-xs text-amber-400">
            <strong>Deploy to get URL:</strong> Deploy this agent to generate a live webhook URL
            that external services can call.
          </p>
        </div>
      )}

      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)]">Method</label>
        <select
          value={method}
          onChange={e => onUpdate({ method: e.target.value })}
          className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
        >
          <option value="GET">GET</option>
          <option value="POST">POST</option>
          <option value="PUT">PUT</option>
          <option value="DELETE">DELETE</option>
        </select>
      </div>

      {/* Note: Webhook path is auto-generated on deploy */}
      <div className="p-2.5 rounded-lg bg-white/5 border border-white/10">
        <p className="text-[10px] text-[var(--color-text-tertiary)]">
          A unique webhook URL will be auto-generated when this agent is deployed.
        </p>
      </div>

      {/* Response Mode */}
      <div className="space-y-2">
        <label className="text-xs font-medium text-[var(--color-text-secondary)]">
          Response Mode
        </label>
        <div className="flex rounded-lg overflow-hidden border border-[var(--color-border)]">
          <button
            onClick={() => onUpdate({ responseMode: 'trigger_only' })}
            className={`flex-1 px-3 py-2 text-xs font-medium transition-all ${
              responseMode === 'trigger_only'
                ? 'bg-[var(--color-accent)]/15 text-[var(--color-accent)]'
                : 'bg-white/5 text-[var(--color-text-secondary)] hover:bg-white/10'
            }`}
          >
            Trigger Only
          </button>
          <button
            onClick={() => onUpdate({ responseMode: 'respond_with_result' })}
            className={`flex-1 px-3 py-2 text-xs font-medium transition-all border-l border-[var(--color-border)] ${
              responseMode === 'respond_with_result'
                ? 'bg-[var(--color-accent)]/15 text-[var(--color-accent)]'
                : 'bg-white/5 text-[var(--color-text-secondary)] hover:bg-white/10'
            }`}
          >
            Respond with Result
          </button>
        </div>
        <p className="text-[10px] text-[var(--color-text-tertiary)]">
          {responseMode === 'trigger_only'
            ? 'Returns 202 immediately. Workflow runs in background.'
            : 'Waits for workflow to complete and returns the configured response.'}
        </p>
      </div>

      {/* Response Template (only in respond_with_result mode) */}
      {responseMode === 'respond_with_result' && (
        <div className="space-y-1.5">
          <label className="text-xs font-medium text-[var(--color-text-secondary)]">
            Response Template (JSON)
          </label>
          <textarea
            value={responseTemplate}
            onChange={e => {
              onUpdate({ responseTemplate: e.target.value });
              if (e.target.value.trim()) {
                try {
                  JSON.parse(e.target.value);
                  setTemplateError(null);
                } catch {
                  setTemplateError('Invalid JSON');
                }
              } else {
                setTemplateError(null);
              }
            }}
            rows={4}
            placeholder={'{\n  "answer": "{{ai-agent.response}}",\n  "status": "ok"\n}'}
            className={`w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono resize-none focus:outline-none focus:ring-2 ${
              templateError
                ? 'focus:ring-red-500/50 border border-red-500/30'
                : 'focus:ring-[var(--color-accent)]/50'
            }`}
          />
          {templateError && <p className="text-xs text-red-400">{templateError}</p>}
          <p className="text-[10px] text-[var(--color-text-tertiary)]">
            {
              'Use {{block-name.response}} to reference block outputs. Leave empty to return all terminal block outputs.'
            }
          </p>
          <div className="p-2 rounded-lg bg-blue-500/5 border border-blue-500/20">
            <p className="text-[10px] text-blue-400">
              The caller will wait until the workflow completes before receiving a response.
            </p>
          </div>
        </div>
      )}

      {/* Test Data */}
      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)]">
          Test Data (JSON)
        </label>
        <textarea
          value={testData}
          onChange={e => handleTestDataChange(e.target.value)}
          rows={6}
          placeholder='{"key": "value"}'
          className={`w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono resize-none focus:outline-none focus:ring-2 ${
            testDataError
              ? 'focus:ring-red-500/50 border border-red-500/30'
              : 'focus:ring-[var(--color-accent)]/50'
          }`}
        />
        {testDataError && <p className="text-xs text-red-400">{testDataError}</p>}
        <p className="text-xs text-[var(--color-text-tertiary)]">
          This JSON payload is used as the webhook body when you click Play to test the workflow.
        </p>
      </div>

      <div className="p-3 rounded-lg bg-blue-500/10 border border-blue-500/30">
        <p className="text-xs text-blue-400">
          <strong>How to test:</strong> Click Play to run the workflow with the test data above.
          After deploying, you can also send real HTTP requests to the live webhook URL.
        </p>
      </div>
    </div>
  );
}

// =============================================================================
// HTTP Request Block Settings — KV editors, curl import/export, Content-Type helper
// =============================================================================

const CONTENT_TYPE_OPTIONS = [
  { label: 'JSON', value: 'application/json' },
  { label: 'Form URL-Encoded', value: 'application/x-www-form-urlencoded' },
  { label: 'Multipart Form', value: 'multipart/form-data' },
  { label: 'Plain Text', value: 'text/plain' },
  { label: 'Custom', value: '' },
];

function HTTPRequestSettings({
  config,
  onUpdate,
}: {
  config: Record<string, unknown>;
  onUpdate: (config: Record<string, unknown>) => void;
}) {
  const method = ('method' in config ? (config.method as string) : 'GET') || 'GET';
  const url = ('url' in config ? (config.url as string) : '') || '';
  const headers = ('headers' in config ? (config.headers as Record<string, string>) : {}) || {};
  const body = ('body' in config ? (config.body as string) : '') || '';
  const authType = ('authType' in config ? (config.authType as string) : 'none') || 'none';
  const authConfig =
    ('authConfig' in config ? (config.authConfig as Record<string, string>) : {}) || {};

  // Local state for curl import/export
  const [curlInput, setCurlInput] = useState('');
  const [curlError, setCurlError] = useState('');
  const [curlOpen, setCurlOpen] = useState(false);
  const [curlCopied, setCurlCopied] = useState(false);

  // Local state for raw JSON toggle
  const [headersRaw, setHeadersRaw] = useState(false);
  const [headersRawText, setHeadersRawText] = useState('');
  const [paramsRaw, setParamsRaw] = useState(false);
  const [paramsRawText, setParamsRawText] = useState('');

  // Derive header KV array from headers object
  const headerEntries = Object.entries(headers).map(([key, value]) => ({ key, value }));
  if (headerEntries.length === 0) headerEntries.push({ key: '', value: '' });

  // Derive query params from URL
  const queryParamEntries = parseQueryParamsFromUrl(url);
  if (queryParamEntries.length === 0) queryParamEntries.push({ key: '', value: '' });

  // Content-Type from headers
  const contentTypeKey = Object.keys(headers).find(k => k.toLowerCase() === 'content-type');
  const currentContentType = contentTypeKey ? headers[contentTypeKey] : '';
  const showBody = !['GET', 'HEAD'].includes(method);

  // -- Handlers --
  const updateHeaders = (entries: Array<{ key: string; value: string }>) => {
    const newHeaders: Record<string, string> = {};
    for (const { key, value } of entries) {
      if (key) newHeaders[key] = value;
    }
    onUpdate({ headers: newHeaders });
  };

  const updateQueryParams = (entries: Array<{ key: string; value: string }>) => {
    const newUrl = buildUrlWithParams(url, entries);
    onUpdate({ url: newUrl });
  };

  const setContentType = (ct: string) => {
    const newHeaders = { ...headers };
    // Remove any existing content-type key (case-insensitive)
    for (const k of Object.keys(newHeaders)) {
      if (k.toLowerCase() === 'content-type') delete newHeaders[k];
    }
    if (ct) newHeaders['Content-Type'] = ct;
    onUpdate({ headers: newHeaders });
  };

  const handleCurlParse = () => {
    setCurlError('');
    try {
      const parsed = parseCurl(curlInput);
      if (!parsed.url && !parsed.method) {
        setCurlError('Could not parse curl command. Make sure it starts with "curl".');
        return;
      }
      const updates: Record<string, unknown> = {};
      if (parsed.method) updates.method = parsed.method;
      if (parsed.url) updates.url = parsed.url;
      if (parsed.headers && Object.keys(parsed.headers).length > 0) {
        updates.headers = { ...headers, ...parsed.headers };
      }
      if (parsed.body) updates.body = parsed.body;
      if (parsed.authType) {
        updates.authType = parsed.authType;
        updates.authConfig = parsed.authConfig || {};
      }
      onUpdate(updates);
      setCurlInput('');
      setCurlOpen(false);
    } catch {
      setCurlError('Failed to parse curl command.');
    }
  };

  const handleCurlExport = () => {
    const curlStr = exportCurl({
      method,
      url,
      headers,
      body,
      authType,
      authConfig,
    });
    navigator.clipboard.writeText(curlStr).then(() => {
      setCurlCopied(true);
      setTimeout(() => setCurlCopied(false), 2000);
    });
  };

  return (
    <div className="space-y-4 pt-4">
      <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
        HTTP Request Settings
      </h3>

      {/* Method + URL */}
      <div className="flex gap-2">
        <select
          value={method}
          onChange={e => onUpdate({ method: e.target.value })}
          className="w-24 px-2 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
        >
          <option value="GET">GET</option>
          <option value="POST">POST</option>
          <option value="PUT">PUT</option>
          <option value="PATCH">PATCH</option>
          <option value="DELETE">DELETE</option>
          <option value="HEAD">HEAD</option>
          <option value="OPTIONS">OPTIONS</option>
        </select>
        <input
          type="text"
          value={url}
          onChange={e => onUpdate({ url: e.target.value })}
          placeholder="https://api.example.com/data"
          className="flex-1 px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
        />
      </div>
      <TemplatePreview value={url} />
      <p className="text-xs text-[var(--color-text-tertiary)]">
        Use {'{{block-id.response}}'} for template interpolation
      </p>

      {/* Curl Import/Export */}
      <div className="space-y-1.5">
        <button
          type="button"
          onClick={() => setCurlOpen(!curlOpen)}
          className="flex items-center gap-1.5 text-xs font-medium text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] transition-colors"
        >
          {curlOpen ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
          <TerminalIcon size={12} />
          Import / Export curl
        </button>
        {curlOpen && (
          <div className="space-y-2 pl-1">
            <textarea
              value={curlInput}
              onChange={e => setCurlInput(e.target.value)}
              rows={3}
              placeholder="Paste a curl command here..."
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono resize-none focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
            />
            {curlError && (
              <p className="text-xs text-red-400 flex items-center gap-1">
                <AlertCircle size={12} /> {curlError}
              </p>
            )}
            <div className="flex gap-2">
              <button
                type="button"
                onClick={handleCurlParse}
                disabled={!curlInput.trim()}
                className="px-3 py-1.5 rounded-md bg-[var(--color-accent)]/20 text-xs font-medium text-[var(--color-accent)] hover:bg-[var(--color-accent)]/30 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
              >
                Parse
              </button>
              <button
                type="button"
                onClick={handleCurlExport}
                className="px-3 py-1.5 rounded-md bg-white/5 text-xs font-medium text-[var(--color-text-secondary)] hover:bg-white/10 transition-colors flex items-center gap-1.5"
              >
                {curlCopied ? <Check size={12} /> : <Clipboard size={12} />}
                {curlCopied ? 'Copied!' : 'Export as curl'}
              </button>
            </div>
          </div>
        )}
      </div>

      {/* Headers — KV Editor */}
      <div className="space-y-1.5">
        <div className="flex items-center justify-between">
          <label className="text-xs font-medium text-[var(--color-text-secondary)]">Headers</label>
          <button
            type="button"
            onClick={() => {
              if (!headersRaw) setHeadersRawText(JSON.stringify(headers, null, 2));
              setHeadersRaw(!headersRaw);
            }}
            className="text-[10px] text-[var(--color-text-tertiary)] hover:text-[var(--color-text-secondary)] transition-colors"
          >
            {headersRaw ? 'KV Editor' : 'Raw JSON'}
          </button>
        </div>
        {headersRaw ? (
          <textarea
            value={headersRawText}
            onChange={e => {
              setHeadersRawText(e.target.value);
              try {
                onUpdate({ headers: JSON.parse(e.target.value) });
              } catch {
                /* ignore while typing */
              }
            }}
            rows={4}
            placeholder={'{\n  "Content-Type": "application/json"\n}'}
            className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono resize-none focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          />
        ) : (
          <div className="space-y-1.5">
            {headerEntries.map((entry, idx) => (
              <div key={idx} className="flex gap-1.5 items-center">
                <input
                  type="text"
                  value={entry.key}
                  onChange={e => {
                    const updated = [...headerEntries];
                    updated[idx] = { ...updated[idx], key: e.target.value };
                    updateHeaders(updated);
                  }}
                  placeholder="Header name"
                  className="flex-1 px-2 py-1.5 rounded-md bg-white/5 text-xs text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
                />
                <input
                  type="text"
                  value={entry.value}
                  onChange={e => {
                    const updated = [...headerEntries];
                    updated[idx] = { ...updated[idx], value: e.target.value };
                    updateHeaders(updated);
                  }}
                  placeholder="Value"
                  className="flex-1 px-2 py-1.5 rounded-md bg-white/5 text-xs text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
                />
                <button
                  type="button"
                  onClick={() => {
                    const updated = headerEntries.filter((_, i) => i !== idx);
                    updateHeaders(updated.length > 0 ? updated : [{ key: '', value: '' }]);
                  }}
                  className="p-1 rounded text-[var(--color-text-tertiary)] hover:text-red-400 transition-colors"
                >
                  <Trash2 size={12} />
                </button>
              </div>
            ))}
            <button
              type="button"
              onClick={() => updateHeaders([...headerEntries, { key: '', value: '' }])}
              className="flex items-center gap-1 text-xs text-[var(--color-text-tertiary)] hover:text-[var(--color-text-secondary)] transition-colors"
            >
              <Plus size={12} /> Add header
            </button>
          </div>
        )}
      </div>

      {/* Content-Type quick-select (only for methods with body) */}
      {showBody && (
        <div className="space-y-1.5">
          <label className="text-xs font-medium text-[var(--color-text-secondary)]">
            Content-Type
          </label>
          <select
            value={
              CONTENT_TYPE_OPTIONS.find(o => o.value === currentContentType)
                ? currentContentType
                : ''
            }
            onChange={e => setContentType(e.target.value)}
            className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          >
            {CONTENT_TYPE_OPTIONS.map(opt => (
              <option key={opt.value || '__custom'} value={opt.value}>
                {opt.label}
                {opt.value ? ` (${opt.value})` : ''}
              </option>
            ))}
          </select>
          {/* Show custom input if "Custom" selected or current value not in presets */}
          {(!CONTENT_TYPE_OPTIONS.some(o => o.value === currentContentType) ||
            currentContentType === '') &&
            contentTypeKey && (
              <input
                type="text"
                value={currentContentType}
                onChange={e => setContentType(e.target.value)}
                placeholder="application/xml"
                className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
              />
            )}
        </div>
      )}

      {/* Query Parameters — KV Editor (from URL) */}
      <div className="space-y-1.5">
        <div className="flex items-center justify-between">
          <label className="text-xs font-medium text-[var(--color-text-secondary)]">
            Query Parameters
          </label>
          <button
            type="button"
            onClick={() => {
              if (!paramsRaw) {
                const params = parseQueryParamsFromUrl(url);
                const obj: Record<string, string> = {};
                params.forEach(p => {
                  if (p.key) obj[p.key] = p.value;
                });
                setParamsRawText(JSON.stringify(obj, null, 2));
              }
              setParamsRaw(!paramsRaw);
            }}
            className="text-[10px] text-[var(--color-text-tertiary)] hover:text-[var(--color-text-secondary)] transition-colors"
          >
            {paramsRaw ? 'KV Editor' : 'Raw JSON'}
          </button>
        </div>
        {paramsRaw ? (
          <textarea
            value={paramsRawText}
            onChange={e => {
              setParamsRawText(e.target.value);
              try {
                const obj = JSON.parse(e.target.value) as Record<string, string>;
                const entries = Object.entries(obj).map(([key, value]) => ({ key, value }));
                updateQueryParams(entries);
              } catch {
                /* ignore while typing */
              }
            }}
            rows={3}
            placeholder={'{\n  "page": "1"\n}'}
            className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono resize-none focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          />
        ) : (
          <div className="space-y-1.5">
            {queryParamEntries.map((entry, idx) => (
              <div key={idx} className="flex gap-1.5 items-center">
                <input
                  type="text"
                  value={entry.key}
                  onChange={e => {
                    const updated = [...queryParamEntries];
                    updated[idx] = { ...updated[idx], key: e.target.value };
                    updateQueryParams(updated);
                  }}
                  placeholder="Key"
                  className="flex-1 px-2 py-1.5 rounded-md bg-white/5 text-xs text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
                />
                <input
                  type="text"
                  value={entry.value}
                  onChange={e => {
                    const updated = [...queryParamEntries];
                    updated[idx] = { ...updated[idx], value: e.target.value };
                    updateQueryParams(updated);
                  }}
                  placeholder="Value"
                  className="flex-1 px-2 py-1.5 rounded-md bg-white/5 text-xs text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
                />
                <button
                  type="button"
                  onClick={() => {
                    const updated = queryParamEntries.filter((_, i) => i !== idx);
                    updateQueryParams(updated.length > 0 ? updated : [{ key: '', value: '' }]);
                  }}
                  className="p-1 rounded text-[var(--color-text-tertiary)] hover:text-red-400 transition-colors"
                >
                  <Trash2 size={12} />
                </button>
              </div>
            ))}
            <button
              type="button"
              onClick={() => updateQueryParams([...queryParamEntries, { key: '', value: '' }])}
              className="flex items-center gap-1 text-xs text-[var(--color-text-tertiary)] hover:text-[var(--color-text-secondary)] transition-colors"
            >
              <Plus size={12} /> Add parameter
            </button>
          </div>
        )}
        <p className="text-xs text-[var(--color-text-tertiary)]">
          Parameters are embedded in the URL
        </p>
      </div>

      {/* Body */}
      {showBody && (
        <div className="space-y-1.5">
          <label className="text-xs font-medium text-[var(--color-text-secondary)]">
            Request Body
          </label>
          <textarea
            value={body}
            onChange={e => onUpdate({ body: e.target.value })}
            rows={4}
            placeholder={
              currentContentType === 'application/json'
                ? '{"key": "{{input}}"}'
                : currentContentType === 'application/x-www-form-urlencoded'
                  ? 'key=value&other={{input}}'
                  : '{{input}}'
            }
            className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono resize-none focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          />
          <TemplatePreview value={body} />
        </div>
      )}

      {/* Auth Type */}
      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)]">
          Authentication
        </label>
        <select
          value={authType}
          onChange={e => onUpdate({ authType: e.target.value, authConfig: {} })}
          className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
        >
          <option value="none">None</option>
          <option value="bearer">Bearer Token</option>
          <option value="basic">Basic Auth</option>
          <option value="api_key">API Key</option>
        </select>
      </div>

      {/* Auth Config */}
      {authType === 'bearer' && (
        <div className="space-y-1.5">
          <label className="text-xs font-medium text-[var(--color-text-secondary)]">Token</label>
          <input
            type="password"
            value={authConfig.token || ''}
            onChange={e => onUpdate({ authConfig: { token: e.target.value } })}
            placeholder="Bearer token..."
            className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          />
        </div>
      )}
      {authType === 'basic' && (
        <div className="space-y-2">
          <input
            type="text"
            value={authConfig.username || ''}
            onChange={e => onUpdate({ authConfig: { ...authConfig, username: e.target.value } })}
            placeholder="Username"
            className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          />
          <input
            type="password"
            value={authConfig.password || ''}
            onChange={e => onUpdate({ authConfig: { ...authConfig, password: e.target.value } })}
            placeholder="Password"
            className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          />
        </div>
      )}
      {authType === 'api_key' && (
        <div className="space-y-2">
          <input
            type="text"
            value={authConfig.headerName || ''}
            onChange={e => onUpdate({ authConfig: { ...authConfig, headerName: e.target.value } })}
            placeholder="Header name (e.g., X-API-Key)"
            className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          />
          <input
            type="password"
            value={authConfig.key || ''}
            onChange={e => onUpdate({ authConfig: { ...authConfig, key: e.target.value } })}
            placeholder="API key value"
            className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          />
        </div>
      )}
    </div>
  );
}

// LLM Inference (AI Agent) Block Settings with independent model selector
function LLMInferenceSettings({
  config,
  onUpdate,
  tools,
  block,
}: {
  config: Record<string, unknown>;
  onUpdate: (updates: Record<string, unknown>) => void;
  tools: string[];
  block: Block;
}) {
  const { models, fetchModels, isLoading: modelsLoading } = useModelStore();
  const agentModels = filterAgentModels(models);

  // Fetch models on mount
  useEffect(() => {
    if (models.length === 0) {
      fetchModels();
    }
  }, [models.length, fetchModels]);

  const blockModelId = (config.model as string) || (config.modelId as string) || '';

  // Structured output state
  const outputFormat = (config.outputFormat as string) || 'text';
  const [schemaInputMode, setSchemaInputMode] = useState<'schema' | 'example'>('schema');
  const [schemaText, setSchemaText] = useState<string>(() => {
    const schema = config.outputSchema as Record<string, unknown> | undefined;
    return schema && Object.keys(schema).length > 0 ? JSON.stringify(schema, null, 2) : '';
  });
  const [exampleText, setExampleText] = useState<string>('');
  const [exampleError, setExampleError] = useState<string | null>(null);
  const [schemaError, setSchemaError] = useState<string | null>(null);

  /** Infer a JSON Schema from an example JSON value */
  const inferSchemaFromExample = (value: unknown): Record<string, unknown> => {
    if (value === null) return { type: 'string' };
    if (Array.isArray(value)) {
      const items = value.length > 0 ? inferSchemaFromExample(value[0]) : { type: 'string' };
      return { type: 'array', items };
    }
    if (typeof value === 'object') {
      const obj = value as Record<string, unknown>;
      const properties: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(obj)) {
        properties[k] = inferSchemaFromExample(v);
      }
      return {
        type: 'object',
        properties,
        required: Object.keys(obj),
        additionalProperties: false,
      };
    }
    if (typeof value === 'number')
      return Number.isInteger(value) ? { type: 'integer' } : { type: 'number' };
    if (typeof value === 'boolean') return { type: 'boolean' };
    return { type: 'string' };
  };

  const generateSchemaFromExample = useCallback(
    (text: string) => {
      setExampleText(text);
      if (!text.trim()) {
        setExampleError(null);
        return;
      }
      try {
        const parsed = JSON.parse(text);
        setExampleError(null);
        const schema = inferSchemaFromExample(parsed);
        const json = JSON.stringify(schema, null, 2);
        setSchemaText(json);
        setSchemaError(null);
        onUpdate({ outputSchema: schema });
      } catch (e) {
        setExampleError((e as Error).message);
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [onUpdate]
  );

  const commitSchema = useCallback(
    (text: string) => {
      setSchemaText(text);
      if (!text.trim()) {
        setSchemaError(null);
        onUpdate({ outputSchema: undefined });
        return;
      }
      try {
        const parsed = JSON.parse(text);
        setSchemaError(null);
        onUpdate({ outputSchema: parsed });
      } catch (e) {
        setSchemaError((e as Error).message);
      }
    },
    [onUpdate]
  );

  const SCHEMA_TEMPLATES: Record<string, { label: string; schema: Record<string, unknown> }> = {
    extraction: {
      label: 'Data Extraction',
      schema: {
        type: 'object',
        properties: {
          entities: {
            type: 'array',
            items: {
              type: 'object',
              properties: {
                name: { type: 'string', description: 'Entity name' },
                value: { type: 'string', description: 'Extracted value' },
                category: { type: 'string', description: 'Entity category' },
              },
              required: ['name', 'value'],
              additionalProperties: false,
            },
          },
        },
        required: ['entities'],
        additionalProperties: false,
      },
    },
    classification: {
      label: 'Classification',
      schema: {
        type: 'object',
        properties: {
          label: { type: 'string', description: 'Classification label' },
          confidence: { type: 'number', description: 'Confidence score 0-1' },
          reasoning: { type: 'string', description: 'Explanation for the classification' },
        },
        required: ['label', 'confidence'],
        additionalProperties: false,
      },
    },
    summary: {
      label: 'Summary',
      schema: {
        type: 'object',
        properties: {
          summary: { type: 'string', description: 'Concise summary' },
          keyPoints: {
            type: 'array',
            items: { type: 'string' },
            description: 'Key points extracted',
          },
          sentiment: {
            type: 'string',
            enum: ['positive', 'neutral', 'negative'],
            description: 'Overall sentiment',
          },
        },
        required: ['summary', 'keyPoints'],
        additionalProperties: false,
      },
    },
    analysis: {
      label: 'Analysis Report',
      schema: {
        type: 'object',
        properties: {
          title: { type: 'string', description: 'Report title' },
          findings: {
            type: 'array',
            items: {
              type: 'object',
              properties: {
                finding: { type: 'string' },
                severity: { type: 'string', enum: ['low', 'medium', 'high', 'critical'] },
                recommendation: { type: 'string' },
              },
              required: ['finding', 'severity'],
              additionalProperties: false,
            },
          },
          conclusion: { type: 'string', description: 'Overall conclusion' },
        },
        required: ['title', 'findings', 'conclusion'],
        additionalProperties: false,
      },
    },
  };

  return (
    <div className="space-y-4 pt-4">
      <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
        LLM Settings
      </h3>

      {/* Model Selector */}
      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)] flex items-center gap-1.5">
          <Cpu size={14} />
          Model
        </label>
        <select
          value={blockModelId}
          onChange={e => onUpdate({ model: e.target.value, modelId: e.target.value })}
          className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          disabled={modelsLoading}
        >
          <option value="">Use workflow default</option>
          {agentModels.map(model => (
            <option key={model.id} value={model.id}>
              {model.name || model.id}
            </option>
          ))}
        </select>
        <p className="text-xs text-[var(--color-text-tertiary)]">
          Override the workflow model for this block, or leave empty to use the default.
        </p>
      </div>

      {/* System Prompt */}
      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)]">
          System Prompt
        </label>
        <textarea
          value={(config.systemPrompt as string) || ''}
          onChange={e => onUpdate({ systemPrompt: e.target.value })}
          rows={4}
          placeholder="Instructions for the LLM agent..."
          className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono resize-none focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
        />
        <TemplatePreview value={(config.systemPrompt as string) || ''} />
      </div>

      {/* User Prompt */}
      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)]">
          User Prompt
        </label>
        <textarea
          value={(config.userPrompt as string) || ''}
          onChange={e => onUpdate({ userPrompt: e.target.value })}
          rows={2}
          placeholder="{{input}} or {{previous-block.response}}"
          className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono resize-none focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
        />
        <TemplatePreview value={(config.userPrompt as string) || ''} />
        <p className="text-xs text-[var(--color-text-tertiary)]">
          Use {'{{input}}'} for workflow input or {'{{block-id.response}}'} for previous block
          output
        </p>
      </div>

      {/* Temperature */}
      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)]">
          Temperature: {config.temperature ?? 0.7}
        </label>
        <input
          type="range"
          min="0"
          max="1"
          step="0.1"
          value={(config.temperature as number) ?? 0.7}
          onChange={e => onUpdate({ temperature: parseFloat(e.target.value) })}
          className="w-full accent-[var(--color-accent)]"
        />
      </div>

      {/* Output Format */}
      <div className="space-y-2">
        <label className="text-xs font-medium text-[var(--color-text-secondary)] flex items-center gap-1.5">
          <Braces size={14} />
          Output Format
        </label>

        {/* Toggle: Text / Structured */}
        <div className="flex rounded-lg overflow-hidden border border-[var(--color-border)]">
          <button
            type="button"
            onClick={() => {
              onUpdate({ outputFormat: 'text', outputSchema: undefined, strictOutput: undefined });
              setSchemaText('');
              setSchemaError(null);
            }}
            className={`flex-1 flex items-center justify-center gap-1.5 px-3 py-2 text-xs font-medium transition-colors ${
              outputFormat === 'text'
                ? 'bg-[var(--color-accent)] text-white'
                : 'bg-white/5 text-[var(--color-text-secondary)] hover:bg-white/10'
            }`}
          >
            <Type size={13} />
            Text
          </button>
          <button
            type="button"
            onClick={() => onUpdate({ outputFormat: 'json' })}
            className={`flex-1 flex items-center justify-center gap-1.5 px-3 py-2 text-xs font-medium transition-colors ${
              outputFormat === 'json'
                ? 'bg-[var(--color-accent)] text-white'
                : 'bg-white/5 text-[var(--color-text-secondary)] hover:bg-white/10'
            }`}
          >
            <Code size={13} />
            Structured
          </button>
        </div>

        {/* Structured output settings */}
        {outputFormat === 'json' && (
          <div className="space-y-2 pl-0.5">
            {/* Schema input mode: Schema / From Example / Template */}
            <div className="flex rounded-md overflow-hidden border border-[var(--color-border)]">
              <button
                type="button"
                onClick={() => setSchemaInputMode('schema')}
                className={`flex-1 px-2 py-1.5 text-[10px] font-medium transition-colors ${
                  schemaInputMode === 'schema'
                    ? 'bg-white/10 text-[var(--color-text-primary)]'
                    : 'bg-transparent text-[var(--color-text-tertiary)] hover:bg-white/5'
                }`}
              >
                Write Schema
              </button>
              <button
                type="button"
                onClick={() => setSchemaInputMode('example')}
                className={`flex-1 px-2 py-1.5 text-[10px] font-medium transition-colors border-l border-[var(--color-border)] ${
                  schemaInputMode === 'example'
                    ? 'bg-white/10 text-[var(--color-text-primary)]'
                    : 'bg-transparent text-[var(--color-text-tertiary)] hover:bg-white/5'
                }`}
              >
                From Example
              </button>
            </div>

            {/* Template selector (visible in schema mode) */}
            {schemaInputMode === 'schema' && (
              <div className="space-y-1">
                <label className="text-[10px] font-medium text-[var(--color-text-tertiary)] uppercase tracking-wide">
                  Template
                </label>
                <select
                  value=""
                  onChange={e => {
                    const key = e.target.value;
                    if (key && SCHEMA_TEMPLATES[key]) {
                      const json = JSON.stringify(SCHEMA_TEMPLATES[key].schema, null, 2);
                      setSchemaText(json);
                      setSchemaError(null);
                      onUpdate({ outputSchema: SCHEMA_TEMPLATES[key].schema });
                    }
                  }}
                  className="w-full px-2.5 py-1.5 rounded-lg bg-white/5 text-xs text-[var(--color-text-primary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
                >
                  <option value="">Load a template...</option>
                  {Object.entries(SCHEMA_TEMPLATES).map(([key, { label }]) => (
                    <option key={key} value={key}>
                      {label}
                    </option>
                  ))}
                </select>
              </div>
            )}

            {/* From Example mode */}
            {schemaInputMode === 'example' && (
              <div className="space-y-1">
                <label className="text-[10px] font-medium text-[var(--color-text-tertiary)] uppercase tracking-wide">
                  Paste a JSON Example
                </label>
                <textarea
                  value={exampleText}
                  onChange={e => {
                    setExampleText(e.target.value);
                    if (!e.target.value.trim()) {
                      setExampleError(null);
                      return;
                    }
                    try {
                      JSON.parse(e.target.value);
                      setExampleError(null);
                    } catch (err) {
                      setExampleError((err as Error).message);
                    }
                  }}
                  onBlur={() => generateSchemaFromExample(exampleText)}
                  rows={6}
                  placeholder={`{\n  "name": "John Doe",\n  "age": 30,\n  "tags": ["developer", "designer"],\n  "active": true\n}`}
                  className="w-full px-3 py-2 rounded-lg bg-white/5 text-[11px] text-[var(--color-text-primary)] font-mono resize-y focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50 leading-relaxed"
                  spellCheck={false}
                />
                {exampleText.trim() && (
                  <div className="flex items-center gap-1.5">
                    {exampleError ? (
                      <>
                        <AlertCircle size={11} className="text-red-400 flex-shrink-0" />
                        <span className="text-[10px] text-red-400 truncate">{exampleError}</span>
                      </>
                    ) : (
                      <>
                        <CheckCircle size={11} className="text-green-400 flex-shrink-0" />
                        <span className="text-[10px] text-green-400">
                          Valid JSON — schema generated below
                        </span>
                      </>
                    )}
                  </div>
                )}
                <button
                  type="button"
                  disabled={!exampleText.trim() || !!exampleError}
                  onClick={() => generateSchemaFromExample(exampleText)}
                  className="w-full mt-1 px-3 py-1.5 rounded-lg bg-[var(--color-accent)]/20 text-[var(--color-accent)] text-xs font-medium hover:bg-[var(--color-accent)]/30 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
                >
                  Generate Schema
                </button>
              </div>
            )}

            {/* JSON Schema editor (always visible — shows generated or hand-written schema) */}
            <div className="space-y-1">
              <label className="text-[10px] font-medium text-[var(--color-text-tertiary)] uppercase tracking-wide">
                {schemaInputMode === 'example' ? 'Generated Schema' : 'JSON Schema'}
              </label>
              <textarea
                value={schemaText}
                onChange={e => {
                  setSchemaText(e.target.value);
                  if (!e.target.value.trim()) {
                    setSchemaError(null);
                    return;
                  }
                  try {
                    JSON.parse(e.target.value);
                    setSchemaError(null);
                  } catch (err) {
                    setSchemaError((err as Error).message);
                  }
                }}
                onBlur={() => commitSchema(schemaText)}
                rows={schemaInputMode === 'example' ? 8 : 10}
                placeholder={`{\n  "type": "object",\n  "properties": {\n    "result": { "type": "string" }\n  },\n  "required": ["result"],\n  "additionalProperties": false\n}`}
                className="w-full px-3 py-2 rounded-lg bg-white/5 text-[11px] text-[var(--color-text-primary)] font-mono resize-y focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50 leading-relaxed"
                spellCheck={false}
              />

              {/* Validation feedback */}
              {schemaText.trim() && (
                <div className="flex items-center gap-1.5">
                  {schemaError ? (
                    <>
                      <AlertCircle size={11} className="text-red-400 flex-shrink-0" />
                      <span className="text-[10px] text-red-400 truncate">{schemaError}</span>
                    </>
                  ) : (
                    <>
                      <CheckCircle size={11} className="text-green-400 flex-shrink-0" />
                      <span className="text-[10px] text-green-400">Valid JSON Schema</span>
                    </>
                  )}
                </div>
              )}
            </div>

            {/* Strict output checkbox */}
            <label className="flex items-start gap-2 cursor-pointer group">
              <input
                type="checkbox"
                checked={(config.strictOutput as boolean) || false}
                onChange={e => onUpdate({ strictOutput: e.target.checked })}
                className="mt-0.5 rounded border-[var(--color-border)] accent-[var(--color-accent)]"
              />
              <div>
                <span className="text-xs text-[var(--color-text-primary)] group-hover:text-[var(--color-accent)] transition-colors">
                  Strict output
                </span>
                <p className="text-[10px] text-[var(--color-text-tertiary)] mt-0.5">
                  Enforces exact schema compliance. Uses native JSON Schema mode on OpenAI/Gemini,
                  prompt-based enforcement on other providers.
                </p>
              </div>
            </label>
          </div>
        )}
      </div>

      {/* Tool Selector */}
      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)] flex items-center gap-1.5">
          <Wrench size={14} />
          Enabled Tools
        </label>
        <ToolSelector
          selectedTools={tools}
          onSelectionChange={newTools => onUpdate({ tools: newTools, enabledTools: newTools })}
          blockContext={{
            name: block.name,
            description: block.description,
            type: block.type,
          }}
        />
      </div>

      {/* Integration Credentials - show when tools are selected */}
      {tools.length > 0 && (
        <div className="space-y-1.5">
          <label className="text-xs font-medium text-[var(--color-text-secondary)] flex items-center gap-1.5">
            <KeyRound size={14} />
            Available Credentials
          </label>
          <IntegrationPicker
            selectedCredentials={(config.credentials as string[]) || []}
            onSelectionChange={credentials => onUpdate({ credentials })}
            toolFilter={tools}
            compact
          />
          {(() => {
            const toolsNeedingCreds = getToolsNeedingCredentials(tools);
            const selectedCreds = (config.credentials as string[]) || [];
            if (toolsNeedingCreds.length > 0 && selectedCreds.length === 0) {
              return (
                <div className="flex items-center gap-1.5 text-xs text-amber-500 mt-2 p-2 rounded-md bg-amber-500/10">
                  <AlertCircle size={14} className="flex-shrink-0" />
                  <span>
                    Select a credential for {toolsNeedingCreds.join(', ')} tool
                    {toolsNeedingCreds.length > 1 ? 's' : ''}
                  </span>
                </div>
              );
            }
            return null;
          })()}
        </div>
      )}
    </div>
  );
}

// =============================================================================
// Sub-Agent Settings — pick another agent to call
// =============================================================================

function SubAgentSettings({
  config,
  onUpdate,
}: {
  config: Record<string, unknown>;
  onUpdate: (updates: Record<string, unknown>) => void;
}) {
  const { backendAgents, selectedAgentId } = useAgentBuilderStore();
  const agentId = ('agentId' in config ? (config.agentId as string) : '') || '';
  const inputMapping =
    ('inputMapping' in config ? (config.inputMapping as string) : '{{input}}') || '{{input}}';
  const waitForCompletion =
    'waitForCompletion' in config ? (config.waitForCompletion as boolean) : true;
  const timeoutSeconds =
    ('timeoutSeconds' in config ? (config.timeoutSeconds as number) : 120) || 120;

  // Filter out the current agent to prevent self-referencing loops
  const availableAgents = backendAgents.filter(a => a.id !== selectedAgentId);

  return (
    <div className="space-y-4 pt-4">
      <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
        Sub-Agent Settings
      </h3>

      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)]">
          Agent to Call
        </label>
        <select
          value={agentId}
          onChange={e => onUpdate({ agentId: e.target.value })}
          className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
        >
          <option value="">Select an agent...</option>
          {availableAgents.map(agent => (
            <option key={agent.id} value={agent.id}>
              {agent.name}
            </option>
          ))}
        </select>
        {availableAgents.length === 0 && (
          <p className="text-xs text-[var(--color-text-tertiary)]">
            No other agents available. Create another agent first.
          </p>
        )}
      </div>

      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)]">
          Input Template
        </label>
        <input
          type="text"
          value={inputMapping}
          onChange={e => onUpdate({ inputMapping: e.target.value })}
          placeholder="{{upstream-block.response}}"
          className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
        />
        <p className="text-xs text-[var(--color-text-tertiary)]">
          Use {'{{block-name.response}}'} to pass upstream data as the sub-agent&apos;s input
        </p>
      </div>

      <div className="flex items-center justify-between p-3 rounded-lg bg-white/5">
        <div>
          <p className="text-sm font-medium text-[var(--color-text-primary)]">
            Wait for Completion
          </p>
          <p className="text-xs text-[var(--color-text-tertiary)]">
            Block until the sub-agent finishes
          </p>
        </div>
        <button
          onClick={() => onUpdate({ waitForCompletion: !waitForCompletion })}
          className={`relative w-10 h-5 rounded-full transition-colors ${
            waitForCompletion ? 'bg-[var(--color-accent)]' : 'bg-[var(--color-bg-tertiary)]'
          }`}
        >
          <div
            className={`absolute top-0.5 left-0.5 w-4 h-4 bg-white rounded-full transition-transform shadow-sm ${
              waitForCompletion ? 'translate-x-5' : ''
            }`}
          />
        </button>
      </div>

      {waitForCompletion && (
        <div className="space-y-1.5">
          <label className="text-xs font-medium text-[var(--color-text-secondary)]">
            Timeout (seconds)
          </label>
          <input
            type="number"
            value={timeoutSeconds}
            onChange={e => onUpdate({ timeoutSeconds: parseInt(e.target.value) || 120 })}
            min={10}
            max={600}
            className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          />
        </div>
      )}

      <div className="p-3 rounded-lg bg-pink-500/10 border border-pink-500/30">
        <p className="text-xs text-pink-400">
          <strong>How it works:</strong> This block triggers the selected agent via the internal API
          and waits for its result. The sub-agent&apos;s output becomes this block&apos;s output.
        </p>
      </div>
    </div>
  );
}

// =============================================================================
// Code Block (Run Tool) Settings — tool picker with parameter mapping
// =============================================================================

function CodeBlockSettings({
  config,
  onUpdate,
  block,
}: {
  config: Record<string, unknown>;
  onUpdate: (updates: Record<string, unknown>) => void;
  block: Block;
}) {
  const [toolsData, setToolsData] = useState<ToolWithParams[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [showDropdown, setShowDropdown] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);

  const { workflow, blockStates } = useAgentBuilderStore();

  const selectedToolName = (config.toolName as string) || '';
  const argumentMapping =
    (config.argumentMapping as Record<string, string>) || ({} as Record<string, string>);

  // Fetch tools on mount
  useEffect(() => {
    let cancelled = false;
    setIsLoading(true);
    import('@/services/toolsService')
      .then(({ fetchTools }) => fetchTools())
      .then(resp => {
        if (cancelled) return;
        const flat: ToolWithParams[] = [];
        for (const cat of resp.categories) {
          for (const tool of cat.tools) {
            flat.push({
              name: tool.name,
              display_name: tool.display_name,
              description: tool.description,
              icon: tool.icon,
              category: cat.name,
              parameters: tool.parameters as ToolParamsSchema | undefined,
            });
          }
        }
        flat.sort((a, b) => a.display_name.localeCompare(b.display_name));
        setToolsData(flat);
      })
      .catch(err => console.error('Failed to fetch tools:', err))
      .finally(() => !cancelled && setIsLoading(false));
    return () => {
      cancelled = true;
    };
  }, []);

  // Close dropdown on outside click
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setShowDropdown(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  const selectedTool = toolsData.find(t => t.name === selectedToolName);

  // Filter tools for dropdown
  const filteredTools = searchQuery
    ? toolsData.filter(
        t =>
          t.display_name.toLowerCase().includes(searchQuery.toLowerCase()) ||
          t.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
          t.description.toLowerCase().includes(searchQuery.toLowerCase())
      )
    : toolsData;

  // When a tool is selected, auto-populate argument mapping from its parameters
  const handleSelectTool = (tool: ToolWithParams) => {
    const newMapping: Record<string, string> = {};
    if (tool.parameters?.properties) {
      for (const paramName of Object.keys(tool.parameters.properties)) {
        // Keep existing mapping values if any
        newMapping[paramName] = argumentMapping[paramName] || '';
      }
    }
    onUpdate({ toolName: tool.name, argumentMapping: newMapping });
    setShowDropdown(false);
    setSearchQuery('');
  };

  // Find upstream block references for insertion
  const upstreamRefs = getUpstreamRefs(block.id, workflow, blockStates);

  return (
    <div className="space-y-4 pt-4">
      <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
        Run Tool Settings
      </h3>

      {/* Tool Selector Dropdown */}
      <div className="space-y-1.5" ref={dropdownRef}>
        <label className="text-xs font-medium text-[var(--color-text-secondary)] flex items-center gap-1.5">
          <Wrench size={14} />
          Select Tool
        </label>

        {/* Selected tool display / search trigger */}
        <button
          onClick={() => setShowDropdown(!showDropdown)}
          className="w-full flex items-center gap-2 px-3 py-2 rounded-lg bg-white/5 text-sm text-left hover:bg-white/10 transition-colors focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
        >
          {selectedTool ? (
            <>
              <span className="text-[var(--color-text-primary)] font-medium truncate">
                {selectedTool.display_name}
              </span>
              <span className="ml-auto text-[10px] text-[var(--color-text-tertiary)] font-mono">
                {selectedTool.name}
              </span>
            </>
          ) : (
            <span className="text-[var(--color-text-tertiary)]">
              {isLoading ? 'Loading tools...' : 'Choose a tool...'}
            </span>
          )}
          <ChevronDown
            size={14}
            className={`flex-shrink-0 text-[var(--color-text-tertiary)] transition-transform ${showDropdown ? 'rotate-180' : ''}`}
          />
        </button>

        {/* Dropdown */}
        {showDropdown && (
          <div className="relative z-10">
            <div className="absolute top-0 left-0 right-0 max-h-64 overflow-y-auto rounded-lg bg-[#1a1a1f] border border-[var(--color-border)] shadow-xl shadow-black/40">
              {/* Search */}
              <div className="sticky top-0 bg-[#1a1a1f] p-2 border-b border-[var(--color-border)]">
                <input
                  type="text"
                  value={searchQuery}
                  onChange={e => setSearchQuery(e.target.value)}
                  placeholder="Search tools..."
                  autoFocus
                  className="w-full px-2.5 py-1.5 rounded bg-white/10 text-xs text-[var(--color-text-primary)] placeholder-[var(--color-text-tertiary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
                />
              </div>

              {/* Tool list */}
              {filteredTools.length === 0 ? (
                <div className="p-3 text-xs text-[var(--color-text-tertiary)] text-center">
                  No tools found
                </div>
              ) : (
                filteredTools.map(tool => (
                  <button
                    key={tool.name}
                    onClick={() => handleSelectTool(tool)}
                    className={`w-full flex flex-col gap-0.5 px-3 py-2 text-left hover:bg-[var(--color-surface-hover)] transition-colors ${
                      tool.name === selectedToolName ? 'bg-[var(--color-accent)]/10' : ''
                    }`}
                  >
                    <div className="flex items-center gap-2">
                      <span className="text-xs font-medium text-[var(--color-text-primary)]">
                        {tool.display_name}
                      </span>
                      <span className="text-[10px] text-[var(--color-text-tertiary)] font-mono">
                        {tool.category}
                      </span>
                    </div>
                    <span className="text-[10px] text-[var(--color-text-tertiary)] line-clamp-1">
                      {tool.description}
                    </span>
                  </button>
                ))
              )}
            </div>
          </div>
        )}
      </div>

      {/* Selected tool info + parameters */}
      {selectedTool && (
        <>
          {/* Tool description */}
          <div className="p-2.5 rounded-lg bg-white/5">
            <p className="text-xs text-[var(--color-text-secondary)]">{selectedTool.description}</p>
          </div>

          {/* Parameter mapping */}
          {selectedTool.parameters?.properties &&
          Object.keys(selectedTool.parameters.properties).length > 0 ? (
            <div className="space-y-3">
              <label className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
                Parameters
              </label>

              {Object.entries(selectedTool.parameters.properties).map(([paramName, paramDef]) => {
                const isRequired = selectedTool.parameters?.required?.includes(paramName);
                const currentValue = argumentMapping[paramName] || '';
                const paramType = paramDef.type || 'string';

                return (
                  <div key={paramName} className="space-y-1">
                    <div className="flex items-center gap-1.5">
                      <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                        {paramName}
                      </label>
                      {isRequired && (
                        <span className="text-[9px] px-1 py-0.5 rounded bg-red-500/20 text-red-400 font-medium">
                          required
                        </span>
                      )}
                      <span className="text-[9px] px-1 py-0.5 rounded bg-white/10 text-[var(--color-text-tertiary)] font-mono">
                        {paramType}
                      </span>
                    </div>

                    {paramDef.description && (
                      <p className="text-[10px] text-[var(--color-text-tertiary)]">
                        {paramDef.description}
                      </p>
                    )}

                    {/* Input: enum dropdown, textarea for multiline, or text field */}
                    {paramDef.enum ? (
                      <select
                        value={currentValue}
                        onChange={e =>
                          onUpdate({
                            argumentMapping: { ...argumentMapping, [paramName]: e.target.value },
                          })
                        }
                        className="w-full px-2.5 py-1.5 rounded-lg bg-white/5 text-xs text-[var(--color-text-primary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
                      >
                        <option value="">Select...</option>
                        {paramDef.enum.map(opt => (
                          <option key={opt} value={opt}>
                            {opt}
                          </option>
                        ))}
                      </select>
                    ) : paramType === 'object' ? (
                      <textarea
                        value={currentValue}
                        onChange={e =>
                          onUpdate({
                            argumentMapping: { ...argumentMapping, [paramName]: e.target.value },
                          })
                        }
                        rows={3}
                        placeholder={`{{${'{'}upstream-block.response${'}'}}} or JSON value`}
                        className="w-full px-2.5 py-1.5 rounded-lg bg-white/5 text-xs text-[var(--color-text-primary)] font-mono resize-none focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
                      />
                    ) : ['text', 'message', 'body', 'content', 'description', 'html'].includes(
                        paramName
                      ) || paramDef.description?.toLowerCase().includes('message text') ? (
                      <textarea
                        value={currentValue}
                        onChange={e =>
                          onUpdate({
                            argumentMapping: { ...argumentMapping, [paramName]: e.target.value },
                          })
                        }
                        rows={4}
                        placeholder={`{{${'{'}upstream-block.response${'}'}}} or value (supports newlines)`}
                        className="w-full px-2.5 py-1.5 rounded-lg bg-white/5 text-xs text-[var(--color-text-primary)] font-mono resize-y focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
                      />
                    ) : (
                      <input
                        type="text"
                        value={currentValue}
                        onChange={e =>
                          onUpdate({
                            argumentMapping: { ...argumentMapping, [paramName]: e.target.value },
                          })
                        }
                        placeholder={`{{${'{'}upstream-block.response${'}'}}} or value`}
                        className="w-full px-2.5 py-1.5 rounded-lg bg-white/5 text-xs text-[var(--color-text-primary)] font-mono focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]/50"
                      />
                    )}

                    <TemplatePreview value={currentValue} />

                    {/* Quick-insert upstream refs */}
                    {upstreamRefs.length > 0 && (
                      <div className="flex flex-wrap gap-1 mt-1">
                        {upstreamRefs.map(ref => (
                          <button
                            key={ref.path}
                            onClick={() =>
                              onUpdate({
                                argumentMapping: { ...argumentMapping, [paramName]: ref.path },
                              })
                            }
                            className="px-1.5 py-0.5 rounded bg-[var(--color-accent)]/10 text-[9px] font-mono text-[var(--color-accent)] hover:bg-[var(--color-accent)]/20 transition-colors"
                            title={`Insert ${ref.path}`}
                          >
                            {ref.label}
                          </button>
                        ))}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          ) : (
            <div className="p-2.5 rounded-lg bg-amber-500/10 border border-amber-500/20">
              <p className="text-[10px] text-amber-400">
                This tool has no configurable parameters.
              </p>
            </div>
          )}
        </>
      )}

      {/* Integration credentials */}
      {selectedToolName && TOOLS_REQUIRING_CREDENTIALS[selectedToolName] && (
        <div className="space-y-1.5">
          <label className="text-xs font-medium text-[var(--color-text-secondary)] flex items-center gap-1.5">
            <KeyRound size={14} />
            Credentials
          </label>
          <IntegrationPicker
            selectedCredentials={(config.credentials as string[]) || []}
            onSelectionChange={credentials => onUpdate({ credentials })}
            toolFilter={[selectedToolName]}
            compact
          />
        </div>
      )}

      {/* Info Box */}
      <div className="p-3 rounded-lg bg-blue-500/10 border border-blue-500/30">
        <p className="text-xs text-blue-400">
          <strong>Run Tool</strong> executes the selected tool directly without LLM reasoning. Use
          for deterministic tasks like sending messages, making API calls, or data operations.
        </p>
      </div>
    </div>
  );
}

/** Internal type for tools with optional parameter schema */
interface ToolWithParams {
  name: string;
  display_name: string;
  description: string;
  icon: string;
  category: string;
  parameters?: ToolParamsSchema;
}

interface ToolParamsSchema {
  type: string;
  properties: Record<string, { type: string; description?: string; enum?: string[] }>;
  required?: string[];
}

/** Extracts upstream block references for quick-insert buttons */
function getUpstreamRefs(
  blockId: string,
  workflow: {
    connections: { sourceBlockId: string; targetBlockId: string }[];
    blocks: Block[];
  } | null,
  blockStates: Record<string, { outputs?: Record<string, unknown> }>
): { label: string; path: string }[] {
  if (!workflow) return [];
  const refs: { label: string; path: string }[] = [];

  const upstreamConns = workflow.connections.filter(c => c.targetBlockId === blockId);
  for (const conn of upstreamConns) {
    const srcBlock = workflow.blocks.find(b => b.id === conn.sourceBlockId);
    if (!srcBlock) continue;

    const outputs = blockStates[srcBlock.id]?.outputs;
    if (outputs && Object.keys(outputs).length > 0) {
      // Add top-level output fields as references
      for (const key of Object.keys(outputs)) {
        refs.push({
          label: `${srcBlock.name}.${key}`,
          path: `{{${srcBlock.normalizedId}.${key}}}`,
        });
      }
    } else {
      // No execution data yet — add generic references
      refs.push({
        label: `${srcBlock.name}.response`,
        path: `{{${srcBlock.normalizedId}.response}}`,
      });
      refs.push({
        label: `${srcBlock.name}.data`,
        path: `{{${srcBlock.normalizedId}.data}}`,
      });
    }
  }
  return refs;
}

// Variable Block Settings with Model Selector for Start Block
interface VariableBlockSettingsProps {
  config: {
    operation?: 'set' | 'read';
    variableName?: string;
    valueExpression?: string;
    defaultValue?: string;
    workflowModelId?: string;
    inputType?: 'text' | 'file' | 'json';
    fileValue?: FileReference | null;
    jsonValue?: Record<string, unknown> | null;
    requiresInput?: boolean;
  };
  onUpdate: (config: Record<string, unknown>) => void;
}

function VariableBlockSettings({ config, onUpdate }: VariableBlockSettingsProps) {
  const { models, fetchModels, isLoading: modelsLoading } = useModelStore();
  const { currentAgent } = useAgentBuilderStore();
  const isStartBlock = config.operation === 'read' && config.variableName === 'input';

  // Filter models to only show agent-enabled models
  const agentModels = filterAgentModels(models);

  // File upload state
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [isUploading, setIsUploading] = useState(false);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [isFileExpired, setIsFileExpired] = useState(false);
  const [isCheckingFileStatus, setIsCheckingFileStatus] = useState(false);

  // Input type (text or file)
  const inputType = config.inputType || 'text';
  const fileValue = config.fileValue;
  const requiresInput = config.requiresInput !== false;

  // Fetch models on mount if this is the start block
  useEffect(() => {
    if (isStartBlock && models.length === 0) {
      fetchModels();
    }
  }, [isStartBlock, models.length, fetchModels]);

  // Check file expiration status when file is attached
  useEffect(() => {
    if (!isStartBlock || !fileValue?.fileId) {
      setIsFileExpired(false);
      return;
    }

    const checkStatus = async () => {
      setIsCheckingFileStatus(true);
      try {
        const status = await checkFileStatus(fileValue.fileId);
        setIsFileExpired(!status.available || status.expired);
      } catch (err) {
        console.error('Failed to check file status:', err);
      } finally {
        setIsCheckingFileStatus(false);
      }
    };

    checkStatus();
    // Re-check every 5 minutes
    const interval = setInterval(checkStatus, 5 * 60 * 1000);
    return () => clearInterval(interval);
  }, [isStartBlock, fileValue?.fileId]);

  // Handle file selection
  const handleFileSelect = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;

    setIsUploading(true);
    setUploadError(null);

    try {
      const conversationId = currentAgent?.id || 'workflow-test';
      const uploadedFile = await uploadFile(file, conversationId);

      const fileRef: FileReference = {
        fileId: uploadedFile.file_id,
        filename: uploadedFile.filename,
        mimeType: uploadedFile.mime_type,
        size: uploadedFile.size,
        type: getFileTypeFromMime(uploadedFile.mime_type),
      };

      onUpdate({ fileValue: fileRef });
      setIsFileExpired(false);
    } catch (err) {
      console.error('File upload failed:', err);
      setUploadError(err instanceof Error ? err.message : 'Upload failed');
    } finally {
      setIsUploading(false);
      if (fileInputRef.current) {
        fileInputRef.current.value = '';
      }
    }
  };

  // Remove selected file
  const handleRemoveFile = () => {
    onUpdate({ fileValue: null });
    setIsFileExpired(false);
    setUploadError(null);
  };

  // Toggle input type
  const handleInputTypeChange = (newType: 'text' | 'file' | 'json') => {
    onUpdate({ inputType: newType });
  };

  return (
    <div className="space-y-4 pt-4">
      <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
        Variable Settings
      </h3>

      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)]">Operation</label>
        <select
          value={config.operation as string}
          onChange={e => onUpdate({ operation: e.target.value as 'set' | 'read' })}
          className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
        >
          <option value="set">Set Variable</option>
          <option value="read">Read Variable</option>
        </select>
      </div>

      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)]">
          Variable Name
        </label>
        <input
          type="text"
          value={config.variableName as string}
          onChange={e => onUpdate({ variableName: e.target.value })}
          placeholder="myVariable"
          className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
        />
      </div>

      {config.operation === 'set' && (
        <div className="space-y-1.5">
          <label className="text-xs font-medium text-[var(--color-text-secondary)]">
            Value Expression
          </label>
          <input
            type="text"
            value={(config.valueExpression as string) || ''}
            onChange={e => onUpdate({ valueExpression: e.target.value })}
            placeholder="{{input.result}}"
            className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          />
        </div>
      )}

      {isStartBlock && (
        <>
          {/* Model Selector for Workflow */}
          <div className="p-3 rounded-lg bg-[var(--color-accent)]/5 space-y-1.5">
            <label className="text-xs font-medium text-[var(--color-accent)] flex items-center gap-1.5">
              <Cpu size={14} />
              Workflow Model
            </label>
            <select
              value={(config.workflowModelId as string) || ''}
              onChange={e => onUpdate({ workflowModelId: e.target.value })}
              className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
              disabled={modelsLoading}
            >
              <option value="">Use default model</option>
              {agentModels.map(model => (
                <option key={model.id} value={model.id}>
                  {model.name || model.id}
                </option>
              ))}
            </select>
            <p className="text-xs text-[var(--color-text-tertiary)]">
              Select a model to use for all LLM blocks in this workflow.
            </p>
          </div>

          {/* Requires Input Toggle */}
          <div className="flex items-center justify-between p-3 rounded-lg bg-white/5">
            <label className="text-xs font-medium text-[var(--color-text-secondary)]">
              Requires Input
            </label>
            <button
              onClick={() => onUpdate({ requiresInput: !requiresInput })}
              className={`relative w-10 h-5 rounded-full transition-colors ${
                requiresInput ? 'bg-[var(--color-accent)]' : 'bg-white/10'
              }`}
            >
              <div
                className={`absolute top-0.5 w-4 h-4 rounded-full bg-white transition-transform shadow-sm ${
                  requiresInput ? 'left-[22px]' : 'left-0.5'
                }`}
              />
            </button>
          </div>

          {/* Test Input Section - only shown when input is required */}
          {requiresInput && (
            <div className="p-3 rounded-lg bg-[var(--color-accent)]/5 space-y-3">
              {/* Input Type Selector */}
              <div className="space-y-1.5">
                <label className="text-xs font-medium text-[var(--color-accent)]">Input Type</label>
                <div className="flex gap-2">
                  <button
                    onClick={() => handleInputTypeChange('text')}
                    className={`flex-1 flex items-center justify-center gap-1.5 py-2 px-2 rounded-lg text-xs font-medium transition-colors ${
                      inputType === 'text'
                        ? 'bg-[var(--color-accent)] text-white'
                        : 'bg-white/5 text-[var(--color-text-secondary)] hover:bg-white/10'
                    }`}
                  >
                    <Type size={14} />
                    Text
                  </button>
                  <button
                    onClick={() => handleInputTypeChange('json')}
                    className={`flex-1 flex items-center justify-center gap-1.5 py-2 px-2 rounded-lg text-xs font-medium transition-colors ${
                      inputType === 'json'
                        ? 'bg-[var(--color-accent)] text-white'
                        : 'bg-white/5 text-[var(--color-text-secondary)] hover:bg-white/10'
                    }`}
                  >
                    <Braces size={14} />
                    JSON
                  </button>
                  <button
                    onClick={() => handleInputTypeChange('file')}
                    className={`flex-1 flex items-center justify-center gap-1.5 py-2 px-2 rounded-lg text-xs font-medium transition-colors ${
                      inputType === 'file'
                        ? 'bg-[var(--color-accent)] text-white'
                        : 'bg-white/5 text-[var(--color-text-secondary)] hover:bg-white/10'
                    }`}
                  >
                    <Upload size={14} />
                    File
                  </button>
                </div>
              </div>

              {/* Text Input Mode */}
              {inputType === 'text' && (
                <div className="space-y-1.5">
                  <label className="text-xs font-medium text-[var(--color-accent)]">
                    Test Input Value
                  </label>
                  <textarea
                    value={(config.defaultValue as string) || ''}
                    onChange={e => onUpdate({ defaultValue: e.target.value })}
                    placeholder="Enter a test value to run your workflow with..."
                    rows={3}
                    className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] resize-none focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
                  />
                  <p className="text-xs text-[var(--color-text-tertiary)]">
                    This value will be passed to the workflow when you click Run.
                  </p>
                </div>
              )}

              {/* JSON Input Mode */}
              {inputType === 'json' && <JsonInputSection config={config} onUpdate={onUpdate} />}

              {/* File Input Mode */}
              {inputType === 'file' && (
                <div className="space-y-2">
                  {/* Hidden file input */}
                  <input
                    ref={fileInputRef}
                    type="file"
                    onChange={handleFileSelect}
                    className="hidden"
                    accept="image/*,application/pdf,.docx,.pptx,.csv,.xlsx,.xls,.json,.txt,audio/*"
                  />

                  {/* File display or upload button */}
                  {fileValue ? (
                    <div
                      className={`flex items-center gap-3 p-3 rounded-lg ${
                        isFileExpired ? 'bg-red-500/10' : 'bg-white/5'
                      }`}
                    >
                      {/* File type icon or expired warning */}
                      {isFileExpired ? (
                        <AlertCircle size={20} className="text-red-400 flex-shrink-0" />
                      ) : isCheckingFileStatus ? (
                        <Loader2
                          size={20}
                          className="text-[var(--color-accent)] flex-shrink-0 animate-spin"
                        />
                      ) : (
                        (() => {
                          const FileIcon = FILE_TYPE_ICONS[fileValue.type] || File;
                          return (
                            <FileIcon
                              size={20}
                              className="text-[var(--color-accent)] flex-shrink-0"
                            />
                          );
                        })()
                      )}

                      {/* File info */}
                      <div className="flex-1 min-w-0">
                        <p
                          className={`text-sm font-medium truncate ${
                            isFileExpired ? 'text-red-400' : 'text-[var(--color-text-primary)]'
                          }`}
                        >
                          {fileValue.filename}
                        </p>
                        <p
                          className={`text-xs ${
                            isFileExpired ? 'text-red-400/80' : 'text-[var(--color-text-tertiary)]'
                          }`}
                        >
                          {isFileExpired
                            ? 'File expired - please re-upload'
                            : `${formatFileSize(fileValue.size)} • ${fileValue.type}`}
                        </p>
                      </div>

                      {/* Remove button */}
                      <button
                        onClick={handleRemoveFile}
                        className="p-1.5 rounded-md hover:bg-red-500/20 text-[var(--color-text-tertiary)] hover:text-red-400 transition-colors"
                        title="Remove file"
                      >
                        <X size={16} />
                      </button>
                    </div>
                  ) : (
                    <button
                      onClick={() => fileInputRef.current?.click()}
                      disabled={isUploading}
                      className={`w-full flex flex-col items-center justify-center gap-2 p-6 rounded-lg border-2 border-dashed transition-colors cursor-pointer ${
                        isUploading
                          ? 'opacity-50 cursor-wait border-white/10'
                          : 'border-[var(--color-accent)]/30 hover:border-[var(--color-accent)]/50 hover:bg-[var(--color-accent)]/5'
                      }`}
                    >
                      {isUploading ? (
                        <>
                          <Loader2 size={24} className="text-[var(--color-accent)] animate-spin" />
                          <span className="text-xs text-[var(--color-text-tertiary)]">
                            Uploading...
                          </span>
                        </>
                      ) : (
                        <>
                          <Upload size={24} className="text-[var(--color-text-tertiary)]" />
                          <span className="text-sm text-[var(--color-text-secondary)]">
                            Tap to upload file
                          </span>
                          <span className="text-xs text-[var(--color-text-tertiary)]">
                            Images, PDFs, Audio, Data files
                          </span>
                        </>
                      )}
                    </button>
                  )}

                  {/* Upload error */}
                  {uploadError && (
                    <div className="flex items-center gap-2 p-2 rounded-lg bg-red-500/10">
                      <AlertCircle size={14} className="text-red-400 flex-shrink-0" />
                      <p className="text-xs text-red-400">{uploadError}</p>
                    </div>
                  )}

                  <p className="text-xs text-[var(--color-text-tertiary)]">
                    Files are available for 30 minutes after upload.
                  </p>
                </div>
              )}
            </div>
          )}
        </>
      )}
    </div>
  );
}

// JSON Input Section Component
interface JsonInputSectionProps {
  config: {
    jsonValue?: Record<string, unknown> | null;
  };
  onUpdate: (config: Record<string, unknown>) => void;
}

function JsonInputSection({ config, onUpdate }: JsonInputSectionProps) {
  const [jsonText, setJsonText] = useState('');
  const [parseError, setParseError] = useState<string | null>(null);

  // Initialize JSON text from config
  useEffect(() => {
    if (config.jsonValue) {
      try {
        setJsonText(JSON.stringify(config.jsonValue, null, 2));
        setParseError(null);
      } catch {
        setJsonText('');
      }
    } else {
      setJsonText('{\n  \n}');
    }
  }, [config.jsonValue]);

  const handleJsonChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const text = e.target.value;
    setJsonText(text);

    // Try to parse and validate
    try {
      const parsed = JSON.parse(text);
      setParseError(null);
      onUpdate({ jsonValue: parsed });
    } catch (err) {
      setParseError(err instanceof Error ? err.message : 'Invalid JSON');
    }
  };

  const hasValidJson = config.jsonValue !== null && config.jsonValue !== undefined && !parseError;

  return (
    <div className="space-y-1.5">
      <label className="text-xs font-medium text-[var(--color-accent)]">JSON Input Value</label>
      <textarea
        value={jsonText}
        onChange={handleJsonChange}
        placeholder='{\n  "key": "value"\n}'
        rows={5}
        className={`w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] font-mono resize-none focus:outline-none focus:ring-2 ${
          parseError
            ? 'focus:ring-red-500/50'
            : !hasValidJson
              ? 'focus:ring-amber-500/50'
              : 'focus:ring-[var(--color-accent)]/50'
        }`}
      />
      {parseError && (
        <div className="flex items-center gap-1.5 text-xs text-red-400">
          <AlertCircle size={12} />
          {parseError}
        </div>
      )}
      {!parseError && hasValidJson && (
        <div className="flex items-center gap-1.5 text-xs text-green-400">
          <span>✓ Valid JSON</span>
        </div>
      )}
      <p className="text-xs text-[var(--color-text-tertiary)]">
        Enter JSON data to be passed as workflow input.
      </p>
    </div>
  );
}

// =============================================================================
// Upstream Data Preview Component
// =============================================================================

/**
 * Shows outputs from upstream (connected) blocks so users can see what data
 * is available for template interpolation. Requires a workflow run first.
 */
function UpstreamDataPreview({ blockId }: { blockId: string }) {
  const { workflow, blockStates } = useAgentBuilderStore();
  const [expandedBlocks, setExpandedBlocks] = useState<Record<string, boolean>>({});
  const [copiedPath, setCopiedPath] = useState<string | null>(null);

  // Find upstream connections for this block
  const upstreamConnections = (workflow?.connections || []).filter(
    conn => conn.targetBlockId === blockId
  );

  // No upstream connections = entry point block, skip
  if (upstreamConnections.length === 0) return null;

  // Build upstream data map: sourceBlockId → { block, outputs }
  const upstreamData = upstreamConnections.map(conn => {
    const sourceBlock = workflow?.blocks.find(b => b.id === conn.sourceBlockId);
    const state = blockStates[conn.sourceBlockId];
    return {
      connection: conn,
      block: sourceBlock,
      outputs: state?.outputs,
      status: state?.status,
    };
  });

  const hasAnyData = upstreamData.some(d => d.outputs && Object.keys(d.outputs).length > 0);

  const toggleBlock = (id: string) => {
    setExpandedBlocks(prev => ({ ...prev, [id]: !prev[id] }));
  };

  const handleCopyPath = (path: string) => {
    navigator.clipboard.writeText(path);
    setCopiedPath(path);
    setTimeout(() => setCopiedPath(null), 1500);
  };

  return (
    <div className="space-y-2 pt-2">
      <div className="flex items-center gap-1.5">
        <Database size={14} className="text-[var(--color-text-secondary)]" />
        <span className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
          Available Input Data
        </span>
      </div>

      {!hasAnyData ? (
        <div className="p-3 rounded-lg bg-white/5 border border-dashed border-[var(--color-border)]">
          <div className="flex items-center gap-2">
            <Play size={14} className="text-[var(--color-text-tertiary)]" />
            <p className="text-xs text-[var(--color-text-tertiary)]">
              Run the workflow first to see data from upstream blocks.
            </p>
          </div>
        </div>
      ) : (
        <div className="space-y-1.5">
          {upstreamData.map(({ connection, block: sourceBlock, outputs }) => {
            if (!sourceBlock || !outputs || Object.keys(outputs).length === 0) return null;
            const isExpanded = expandedBlocks[sourceBlock.id] ?? true;
            const refPrefix = `{{${sourceBlock.normalizedId}`;

            return (
              <div
                key={connection.id}
                className="rounded-lg bg-white/5 border border-[var(--color-border)] overflow-hidden"
              >
                {/* Source block header */}
                <button
                  onClick={() => toggleBlock(sourceBlock.id)}
                  className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-white/5 transition-colors"
                >
                  {isExpanded ? (
                    <ChevronDown size={12} className="text-[var(--color-text-tertiary)]" />
                  ) : (
                    <ChevronRight size={12} className="text-[var(--color-text-tertiary)]" />
                  )}
                  <span className="text-xs font-medium text-[var(--color-text-primary)] truncate">
                    {sourceBlock.name}
                  </span>
                  <span className="ml-auto text-[10px] text-[var(--color-text-tertiary)] font-mono">
                    {Object.keys(outputs).length} fields
                  </span>
                </button>

                {/* Output fields */}
                {isExpanded && (
                  <div className="px-3 pb-2 space-y-1">
                    <DataTree
                      data={outputs}
                      prefix={refPrefix}
                      copiedPath={copiedPath}
                      onCopy={handleCopyPath}
                      depth={0}
                    />
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

/** Recursively renders a data tree with copyable template references */
function DataTree({
  data,
  prefix,
  copiedPath,
  onCopy,
  depth,
}: {
  data: Record<string, unknown>;
  prefix: string;
  copiedPath: string | null;
  onCopy: (path: string) => void;
  depth: number;
}) {
  const [expandedKeys, setExpandedKeys] = useState<Record<string, boolean>>({});

  const toggleKey = useCallback((key: string) => {
    setExpandedKeys(prev => ({ ...prev, [key]: !prev[key] }));
  }, []);

  if (depth > 4) {
    return <span className="text-[10px] text-[var(--color-text-tertiary)]">...</span>;
  }

  return (
    <>
      {Object.entries(data).map(([key, value]) => {
        const templatePath = `${prefix}.${key}}}`;
        const isObject = value !== null && typeof value === 'object' && !Array.isArray(value);
        const isArray = Array.isArray(value);
        const isExpanded = expandedKeys[key] ?? false;

        // Determine display value
        let displayValue: string;
        if (isObject) {
          displayValue = `{${Object.keys(value as Record<string, unknown>).length} fields}`;
        } else if (isArray) {
          displayValue = `[${(value as unknown[]).length} items]`;
        } else if (typeof value === 'string') {
          displayValue = value.length > 60 ? `"${value.slice(0, 60)}..."` : `"${value}"`;
        } else {
          displayValue = String(value);
        }

        return (
          <div key={key}>
            <div className="flex items-center gap-1 group min-h-[22px]">
              {/* Expand toggle for objects/arrays */}
              {isObject || isArray ? (
                <button
                  onClick={() => toggleKey(key)}
                  className="p-0.5 -ml-0.5 rounded hover:bg-white/10"
                >
                  {isExpanded ? (
                    <ChevronDown size={10} className="text-[var(--color-text-tertiary)]" />
                  ) : (
                    <ChevronRight size={10} className="text-[var(--color-text-tertiary)]" />
                  )}
                </button>
              ) : (
                <span className="w-3.5" />
              )}

              {/* Key name */}
              <span className="text-[11px] font-mono text-[var(--color-accent)]">{key}</span>
              <span className="text-[10px] text-[var(--color-text-tertiary)] mx-0.5">:</span>

              {/* Value preview */}
              <span className="text-[10px] text-[var(--color-text-secondary)] truncate max-w-[140px]">
                {displayValue}
              </span>

              {/* Copy button */}
              <button
                onClick={() => onCopy(templatePath)}
                className="ml-auto p-0.5 rounded opacity-0 group-hover:opacity-100 hover:bg-white/10 transition-opacity flex-shrink-0"
                title={`Copy ${templatePath}`}
              >
                {copiedPath === templatePath ? (
                  <Check size={10} className="text-green-400" />
                ) : (
                  <Copy size={10} className="text-[var(--color-text-tertiary)]" />
                )}
              </button>
            </div>

            {/* Expanded children */}
            {isExpanded && isObject && (
              <div className="ml-3 border-l border-[var(--color-border)] pl-2">
                <DataTree
                  data={value as Record<string, unknown>}
                  prefix={`${prefix}.${key}`}
                  copiedPath={copiedPath}
                  onCopy={onCopy}
                  depth={depth + 1}
                />
              </div>
            )}
            {isExpanded && isArray && (
              <div className="ml-3 border-l border-[var(--color-border)] pl-2">
                {(value as unknown[]).slice(0, 5).map((item, i) => (
                  <div key={i} className="text-[10px] text-[var(--color-text-secondary)] py-0.5">
                    <span className="font-mono text-[var(--color-text-tertiary)]">[{i}]</span>{' '}
                    {typeof item === 'object' && item !== null ? (
                      <DataTree
                        data={item as Record<string, unknown>}
                        prefix={`${prefix}.${key}[${i}]`}
                        copiedPath={copiedPath}
                        onCopy={onCopy}
                        depth={depth + 1}
                      />
                    ) : (
                      <span className="truncate">
                        {typeof item === 'string'
                          ? item.length > 40
                            ? `"${item.slice(0, 40)}..."`
                            : `"${item}"`
                          : String(item)}
                      </span>
                    )}
                  </div>
                ))}
                {(value as unknown[]).length > 5 && (
                  <span className="text-[10px] text-[var(--color-text-tertiary)]">
                    ...and {(value as unknown[]).length - 5} more
                  </span>
                )}
              </div>
            )}
          </div>
        );
      })}
    </>
  );
}

// =============================================================================
// Retry Settings Component
// =============================================================================

const RETRY_ERROR_TYPES = [
  { value: 'all_transient', label: 'All Transient Errors' },
  { value: 'rate_limit', label: 'Rate Limit (429)' },
  { value: 'timeout', label: 'Timeout' },
  { value: 'server_error', label: 'Server Error (5xx)' },
  { value: 'network_error', label: 'Network Error' },
];

interface RetrySettingsProps {
  retryConfig?: RetryConfig;
  onChange: (config: RetryConfig | undefined) => void;
}

function RetrySettings({ retryConfig, onChange }: RetrySettingsProps) {
  const [expanded, setExpanded] = useState(!!retryConfig?.maxRetries);
  const enabled = !!retryConfig && retryConfig.maxRetries > 0;

  const handleToggle = () => {
    if (enabled) {
      onChange(undefined);
    } else {
      onChange({
        maxRetries: 3,
        retryOn: ['all_transient'],
        backoffMs: 1000,
        maxBackoffMs: 30000,
      });
      setExpanded(true);
    }
  };

  const handleUpdate = (updates: Partial<RetryConfig>) => {
    onChange({
      maxRetries: retryConfig?.maxRetries ?? 3,
      retryOn: retryConfig?.retryOn ?? ['all_transient'],
      backoffMs: retryConfig?.backoffMs ?? 1000,
      maxBackoffMs: retryConfig?.maxBackoffMs ?? 30000,
      ...updates,
    });
  };

  const toggleRetryType = (type: string) => {
    const current = retryConfig?.retryOn ?? ['all_transient'];
    const next = current.includes(type) ? current.filter(t => t !== type) : [...current, type];
    handleUpdate({ retryOn: next.length > 0 ? next : ['all_transient'] });
  };

  return (
    <div className="space-y-3 pt-2">
      {/* Header */}
      <button
        onClick={() => setExpanded(!expanded)}
        className="flex items-center gap-2 w-full text-left"
      >
        {expanded ? (
          <ChevronDown size={14} className="text-[var(--color-text-secondary)]" />
        ) : (
          <ChevronRight size={14} className="text-[var(--color-text-secondary)]" />
        )}
        <RefreshCw size={14} className="text-[var(--color-text-secondary)]" />
        <span className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
          Retry Settings
        </span>
        {enabled && (
          <span className="ml-auto text-xs text-green-400 font-medium">
            {retryConfig?.maxRetries}x
          </span>
        )}
      </button>

      {expanded && (
        <div className="space-y-3 pl-1">
          {/* Enable Toggle */}
          <div className="flex items-center justify-between">
            <label className="text-xs text-[var(--color-text-secondary)]">
              Auto-retry on failure
            </label>
            <button
              onClick={handleToggle}
              className={`relative w-9 h-5 rounded-full transition-colors ${
                enabled ? 'bg-[var(--color-accent)]' : 'bg-white/10'
              }`}
            >
              <span
                className={`absolute top-0.5 left-0.5 w-4 h-4 rounded-full bg-white transition-transform ${
                  enabled ? 'translate-x-4' : ''
                }`}
              />
            </button>
          </div>

          {enabled && (
            <>
              {/* Max Retries */}
              <div className="space-y-1.5">
                <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                  Max Retries
                </label>
                <select
                  value={retryConfig?.maxRetries ?? 3}
                  onChange={e => handleUpdate({ maxRetries: Number(e.target.value) })}
                  className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
                >
                  {[1, 2, 3, 4, 5].map(n => (
                    <option key={n} value={n}>
                      {n} {n === 1 ? 'retry' : 'retries'}
                    </option>
                  ))}
                </select>
              </div>

              {/* Retry On */}
              <div className="space-y-1.5">
                <label className="text-xs font-medium text-[var(--color-text-secondary)]">
                  Retry On
                </label>
                <div className="flex flex-wrap gap-1.5">
                  {RETRY_ERROR_TYPES.map(type => {
                    const isSelected = retryConfig?.retryOn?.includes(type.value);
                    return (
                      <button
                        key={type.value}
                        onClick={() => toggleRetryType(type.value)}
                        className={`px-2 py-1 rounded text-xs transition-colors ${
                          isSelected
                            ? 'bg-[var(--color-accent)]/20 text-[var(--color-accent)] border border-[var(--color-accent)]/40'
                            : 'bg-white/5 text-[var(--color-text-secondary)] border border-white/10 hover:bg-white/10'
                        }`}
                      >
                        {type.label}
                      </button>
                    );
                  })}
                </div>
              </div>

              {/* Backoff Info */}
              <div className="p-2 rounded-lg bg-blue-500/10 border border-blue-500/20">
                <p className="text-xs text-blue-400">
                  Uses exponential backoff: {retryConfig?.backoffMs ?? 1000}ms initial delay,
                  doubling each retry up to {retryConfig?.maxBackoffMs ?? 30000}ms max.
                </p>
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}

// ============================================================================
// Knowledge Search Settings — project picker + query + retrieval params
// ============================================================================
//
// Renders the config UI for the `knowledge_search` workflow block.
// Loads the user's Nexus projects from REST, lets them check off one
// or more, plus tune the query template + top_k + rerank toggle.
//
// Why this lives as its own component (not inline in the big
// renderBlockSettings switch): the project list has its own async
// fetch lifecycle and a non-trivial multi-select UI. Keeping it
// scoped out makes the parent switch readable and avoids re-fetching
// projects on every config keystroke.

function KnowledgeSearchSettings({
  projectIds,
  query,
  topK,
  rerank,
  onUpdate,
}: {
  projectIds: string[];
  query: string;
  topK: number;
  rerank: boolean;
  onUpdate: (patch: Record<string, unknown>) => void;
}) {
  const [projects, setProjects] = useState<Array<{ id: string; name: string }>>([]);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    // Inline import — keeping the top-of-file import surface tidy.
    // The Nexus service is already bundled, no extra chunk.
    import('@/services/nexusService')
      .then(({ nexusService }) => nexusService.listProjects())
      .then(list => {
        if (cancelled) return;
        setProjects(list.map(p => ({ id: p.id, name: p.name })));
        setLoadError(null);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setLoadError(err instanceof Error ? err.message : 'failed to load projects');
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const toggleProject = useCallback(
    (id: string) => {
      const next = projectIds.includes(id)
        ? projectIds.filter(p => p !== id)
        : [...projectIds, id];
      onUpdate({ project_ids: next });
    },
    [projectIds, onUpdate]
  );

  const selectedNames = projectIds
    .map(id => projects.find(p => p.id === id)?.name ?? null)
    .filter((n): n is string => Boolean(n));

  return (
    <div className="space-y-4 pt-4">
      <h3 className="text-xs font-semibold text-[var(--color-text-primary)] uppercase tracking-wide">
        Knowledge Search Settings
      </h3>

      <BlockHelpGuide title="Knowledge Search">
        <p className="pt-2">
          <strong>What it does:</strong> Retrieves the most relevant chunks from one or more
          project knowledge bases (Qdrant-backed, dense + sparse hybrid with rerank). Feeds
          downstream LLM blocks with citation-ready evidence.
        </p>
        <p>
          <strong>Configuration:</strong>
        </p>
        <ul className="list-disc pl-4 space-y-1">
          <li>
            <strong>Projects</strong> — check one or more; queries fan across them and
            results merge by reciprocal rank
          </li>
          <li>
            <strong>Query</strong> — what to search for. Supports templates like{' '}
            <code className="px-1 rounded bg-white/10">{'{{input.query}}'}</code>
          </li>
          <li>
            <strong>Top K</strong> — how many chunks to return (5 is a good default)
          </li>
          <li>
            <strong>Rerank</strong> — cross-encoder rerank on top-50 → top-K (worth it 95%
            of the time)
          </li>
        </ul>
        <p>
          <strong>Output:</strong>{' '}
          <code className="px-1 rounded bg-white/10">{'{ chunks, count, elapsed_ms }'}</code>
        </p>
      </BlockHelpGuide>

      {/* ── Project picker ───────────────────────────────────── */}
      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)]">
          Projects ({projectIds.length} selected)
        </label>
        {loadError ? (
          <div className="p-3 rounded-lg bg-red-500/10 border border-red-500/30 text-xs text-red-300">
            Couldn't load projects: {loadError}
          </div>
        ) : loading ? (
          <div className="p-3 rounded-lg bg-white/5 text-xs text-[var(--color-text-tertiary)]">
            Loading projects…
          </div>
        ) : projects.length === 0 ? (
          <div className="p-3 rounded-lg bg-white/5 text-xs text-[var(--color-text-tertiary)]">
            No projects yet. Create one in Nexus and upload files to its Knowledge tab,
            then come back here.
          </div>
        ) : (
          <div className="max-h-56 overflow-y-auto rounded-lg border border-white/10 divide-y divide-white/5">
            {projects.map(p => {
              const checked = projectIds.includes(p.id);
              return (
                <label
                  key={p.id}
                  className={cn(
                    'flex items-center gap-2 px-3 py-2 cursor-pointer transition-colors',
                    checked
                      ? 'bg-[var(--color-accent-light)] text-[var(--color-accent)]'
                      : 'hover:bg-white/5 text-[var(--color-text-primary)]'
                  )}
                >
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={() => toggleProject(p.id)}
                    className="w-4 h-4 rounded accent-[var(--color-accent)]"
                  />
                  <span className="text-sm flex-1 truncate" title={p.name}>
                    {p.name}
                  </span>
                  <span className="text-[10px] font-mono text-[var(--color-text-tertiary)]">
                    {p.id.slice(-6)}
                  </span>
                </label>
              );
            })}
          </div>
        )}
        {selectedNames.length > 0 && (
          <p className="text-xs text-[var(--color-text-tertiary)]">
            Searching: {selectedNames.join(', ')}
          </p>
        )}
      </div>

      {/* ── Query template ────────────────────────────────────── */}
      <div className="space-y-1.5">
        <label className="text-xs font-medium text-[var(--color-text-secondary)]">
          Query
        </label>
        <textarea
          value={query}
          onChange={e => onUpdate({ query: e.target.value })}
          rows={2}
          placeholder="{{input.query}}"
          className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50 font-mono"
        />
        <p className="text-xs text-[var(--color-text-tertiary)]">
          Templates resolve against upstream block outputs at runtime
        </p>
      </div>

      {/* ── Top K + Rerank ────────────────────────────────────── */}
      <div className="flex gap-3">
        <div className="flex-1 space-y-1.5">
          <label className="text-xs font-medium text-[var(--color-text-secondary)]">
            Top K
          </label>
          <input
            type="number"
            value={topK}
            min={1}
            max={30}
            onChange={e => {
              const n = parseInt(e.target.value, 10);
              onUpdate({ top_k: Number.isFinite(n) && n > 0 ? Math.min(n, 30) : 5 });
            }}
            className="w-full px-3 py-2 rounded-lg bg-white/5 text-sm text-[var(--color-text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/50"
          />
          <p className="text-xs text-[var(--color-text-tertiary)]">Max 30 chunks returned</p>
        </div>
        <div className="flex-1 space-y-1.5">
          <label className="text-xs font-medium text-[var(--color-text-secondary)]">
            Rerank
          </label>
          <button
            type="button"
            onClick={() => onUpdate({ rerank: !rerank })}
            className={cn(
              'w-full px-3 py-2 rounded-lg text-sm font-medium transition-colors',
              rerank
                ? 'bg-[var(--color-accent-light)] text-[var(--color-accent)] border border-[var(--color-accent-border)]'
                : 'bg-white/5 text-[var(--color-text-tertiary)] border border-white/10'
            )}
          >
            {rerank ? 'On (cross-encoder)' : 'Off (vector only)'}
          </button>
          <p className="text-xs text-[var(--color-text-tertiary)]">
            {rerank ? '+1-2s, much better' : 'Faster, lower quality'}
          </p>
        </div>
      </div>
    </div>
  );
}
