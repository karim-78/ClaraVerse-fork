import { useState, useEffect, useCallback } from 'react';
import {
  Brain,
  Globe,
  GitBranch,
  Shuffle,
  Webhook,
  Clock,
  Wrench,
  Code,
  Search,
  ChevronRight,
  ChevronDown,
  Loader2,
  PanelLeftClose,
  Plus,
  Plug,
  Repeat,
  Terminal,
  Workflow,
  Filter,
  Route,
  Merge,
  BarChart3,
  ArrowUpDown,
  ListEnd,
  CopyMinus,
  Timer,
  BookOpen,
  icons,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { useAgentBuilderStore } from '@/store/useAgentBuilderStore';
import { toast } from '@/store/useToastStore';
import { normalizeBlockName } from '@/utils/blockUtils';
import { fetchTools, type ToolCategory, type ToolItem } from '@/services/toolService';
import type { Block, BlockType } from '@/types/agent';

// ============================================================================
// Built-in block definitions
// ============================================================================

interface BlockDef {
  id: string;
  label: string;
  description: string;
  icon: React.ElementType;
  color: string;
  bgColor: string;
  blockType: BlockType;
}

const BLOCK_CATEGORIES: { name: string; blocks: BlockDef[] }[] = [
  {
    name: 'Triggers',
    blocks: [
      {
        id: 'webhook_trigger',
        label: 'Webhook',
        description: 'HTTP webhook entry point',
        icon: Webhook,
        color: 'text-rose-500',
        bgColor: 'bg-rose-500/10',
        blockType: 'webhook_trigger',
      },
      {
        id: 'schedule_trigger',
        label: 'Schedule',
        description: 'Cron-based schedule trigger',
        icon: Clock,
        color: 'text-indigo-500',
        bgColor: 'bg-indigo-500/10',
        blockType: 'schedule_trigger',
      },
    ],
  },
  {
    name: 'Logic',
    blocks: [
      {
        id: 'if_condition',
        label: 'If / Condition',
        description: 'Route based on a condition',
        icon: GitBranch,
        color: 'text-yellow-500',
        bgColor: 'bg-yellow-500/10',
        blockType: 'if_condition',
      },
      {
        id: 'transform',
        label: 'Transform',
        description: 'Set, rename, or delete fields',
        icon: Shuffle,
        color: 'text-cyan-500',
        bgColor: 'bg-cyan-500/10',
        blockType: 'transform',
      },
      {
        id: 'for_each',
        label: 'Loop',
        description: 'Iterate over array items',
        icon: Repeat,
        color: 'text-amber-500',
        bgColor: 'bg-amber-500/10',
        blockType: 'for_each',
      },
      {
        id: 'switch',
        label: 'Switch',
        description: 'Multi-way routing by value',
        icon: Route,
        color: 'text-violet-500',
        bgColor: 'bg-violet-500/10',
        blockType: 'switch',
      },
      {
        id: 'wait',
        label: 'Wait',
        description: 'Delay before continuing',
        icon: Timer,
        color: 'text-slate-500',
        bgColor: 'bg-slate-500/10',
        blockType: 'wait',
      },
    ],
  },
  {
    name: 'Data',
    blocks: [
      {
        id: 'filter',
        label: 'Filter',
        description: 'Filter array items by condition',
        icon: Filter,
        color: 'text-sky-500',
        bgColor: 'bg-sky-500/10',
        blockType: 'filter',
      },
      {
        id: 'sort',
        label: 'Sort',
        description: 'Sort array items by fields',
        icon: ArrowUpDown,
        color: 'text-lime-500',
        bgColor: 'bg-lime-500/10',
        blockType: 'sort',
      },
      {
        id: 'aggregate',
        label: 'Aggregate',
        description: 'Group & summarize data',
        icon: BarChart3,
        color: 'text-fuchsia-500',
        bgColor: 'bg-fuchsia-500/10',
        blockType: 'aggregate',
      },
      {
        id: 'merge',
        label: 'Merge',
        description: 'Combine parallel branches',
        icon: Merge,
        color: 'text-teal-500',
        bgColor: 'bg-teal-500/10',
        blockType: 'merge',
      },
      {
        id: 'limit',
        label: 'Limit',
        description: 'Take first/last N items',
        icon: ListEnd,
        color: 'text-stone-500',
        bgColor: 'bg-stone-500/10',
        blockType: 'limit',
      },
      {
        id: 'deduplicate',
        label: 'Deduplicate',
        description: 'Remove duplicate items',
        icon: CopyMinus,
        color: 'text-red-400',
        bgColor: 'bg-red-400/10',
        blockType: 'deduplicate',
      },
    ],
  },
  {
    name: 'AI',
    blocks: [
      {
        id: 'llm_inference',
        label: 'AI Agent',
        description: 'LLM reasoning with tool access',
        icon: Brain,
        color: 'text-purple-500',
        bgColor: 'bg-purple-500/10',
        blockType: 'llm_inference',
      },
    ],
  },
  {
    name: 'Actions',
    blocks: [
      {
        id: 'http_request',
        label: 'HTTP Request',
        description: 'GET, POST, PUT, DELETE',
        icon: Globe,
        color: 'text-green-500',
        bgColor: 'bg-green-500/10',
        blockType: 'http_request',
      },
      {
        id: 'code_block',
        label: 'Run Tool',
        description: 'Execute a tool without LLM',
        icon: Code,
        color: 'text-blue-500',
        bgColor: 'bg-blue-500/10',
        blockType: 'code_block',
      },
      {
        id: 'inline_code',
        label: 'Code',
        description: 'Run Python or JavaScript code',
        icon: Terminal,
        color: 'text-emerald-500',
        bgColor: 'bg-emerald-500/10',
        blockType: 'inline_code',
      },
      {
        id: 'sub_agent',
        label: 'Sub-Agent',
        description: 'Call another agent as a step',
        icon: Workflow,
        color: 'text-pink-500',
        bgColor: 'bg-pink-500/10',
        blockType: 'sub_agent',
      },
      {
        id: 'knowledge_search',
        label: 'Knowledge Search',
        description: 'Search project knowledge base (RAG)',
        icon: BookOpen,
        color: 'text-pink-500',
        bgColor: 'bg-pink-500/10',
        blockType: 'knowledge_search',
      },
    ],
  },
];

// ============================================================================
// Default configs for new blocks
// ============================================================================

function getDefaultConfig(blockType: BlockType): Record<string, unknown> {
  switch (blockType) {
    case 'http_request':
      return {
        type: 'http_request',
        method: 'GET',
        url: '',
        headers: {},
        body: '',
        authType: 'none',
        authConfig: {},
      };
    case 'if_condition':
      return {
        type: 'if_condition',
        field: 'response',
        operator: 'is_true',
        value: '',
      };
    case 'transform':
      return {
        type: 'transform',
        operations: [],
      };
    case 'webhook_trigger':
      return {
        type: 'webhook_trigger',
        path: '/',
        method: 'POST',
        testData: '{\n  "message": "Hello from webhook"\n}',
      };
    case 'schedule_trigger':
      return {
        type: 'schedule_trigger',
        cronExpression: '0 9 * * 1-5',
      };
    case 'llm_inference':
      return {
        type: 'llm_inference',
        modelId: '',
        systemPrompt: '',
        userPromptTemplate: '',
        temperature: 0.7,
        maxTokens: 1024,
        enabledTools: [],
        outputFormat: 'text',
      };
    case 'code_block':
      return {
        type: 'code_block',
        toolName: '',
        argumentMapping: {},
      };
    case 'for_each':
      return {
        type: 'for_each',
        arrayField: 'response',
        itemVariable: 'item',
        maxIterations: 100,
      };
    case 'inline_code':
      return {
        type: 'inline_code',
        language: 'python',
        code: '# Access upstream data via `inputs` dict\n# Return result via `output` variable\noutput = inputs.get("response", "")\n',
      };
    case 'sub_agent':
      return {
        type: 'sub_agent',
        agentId: '',
        inputMapping: '{{input}}',
        waitForCompletion: true,
        timeoutSeconds: 120,
      };
    case 'filter':
      return {
        type: 'filter',
        arrayField: 'response',
        conditions: [],
        mode: 'include',
      };
    case 'switch':
      return {
        type: 'switch',
        field: 'response',
        cases: [],
      };
    case 'merge':
      return {
        type: 'merge',
        mode: 'append',
        keyField: 'id',
      };
    case 'aggregate':
      return {
        type: 'aggregate',
        arrayField: 'response',
        groupBy: '',
        operations: [],
      };
    case 'sort':
      return {
        type: 'sort',
        arrayField: 'response',
        sortBy: [],
      };
    case 'limit':
      return {
        type: 'limit',
        arrayField: 'response',
        count: 10,
        position: 'first',
        offset: 0,
      };
    case 'deduplicate':
      return {
        type: 'deduplicate',
        arrayField: 'response',
        keyField: 'id',
        keep: 'first',
      };
    case 'wait':
      return {
        type: 'wait',
        duration: 1,
        unit: 'seconds',
      };
    case 'knowledge_search':
      // Defaults: empty project_ids array (user picks at design time
      // by editing the block config), templated query that reads from
      // a common variable name, sensible top_k + rerank on.
      return {
        project_ids: [],
        query: '{{input.query}}',
        top_k: 5,
        rerank: true,
      };
    default:
      return { type: blockType };
  }
}

// ============================================================================
// Component
// ============================================================================

interface BlockPaletteProps {
  className?: string;
  onClose?: () => void;
}

export function BlockPalette({ className, onClose }: BlockPaletteProps) {
  const { addBlock, workflow } = useAgentBuilderStore();
  const [searchQuery, setSearchQuery] = useState('');
  const [showIntegrations, setShowIntegrations] = useState(false);
  const [expandedIntegrations, setExpandedIntegrations] = useState<Set<string>>(new Set());
  const [integrationTools, setIntegrationTools] = useState<ToolCategory[]>([]);
  const [isLoadingTools, setIsLoadingTools] = useState(false);

  const hasWorkflow = !!workflow;

  // Fetch integration tools from backend (lazy — only when integrations section is opened)
  useEffect(() => {
    if (!showIntegrations || integrationTools.length > 0) return;

    let mounted = true;
    setIsLoadingTools(true);

    fetchTools()
      .then(response => {
        if (mounted) {
          setIntegrationTools(response.categories);
        }
      })
      .catch(err => {
        console.warn('Failed to load tools for palette:', err);
      })
      .finally(() => {
        if (mounted) setIsLoadingTools(false);
      });

    return () => {
      mounted = false;
    };
  }, [showIntegrations, integrationTools.length]);

  const toggleIntegrationCategory = useCallback((category: string) => {
    setExpandedIntegrations(prev => {
      const next = new Set(prev);
      if (next.has(category)) {
        next.delete(category);
      } else {
        next.add(category);
      }
      return next;
    });
  }, []);

  const handleAddBlock = useCallback(
    (blockDef: BlockDef) => {
      if (!workflow) {
        toast.warning('Create or select an agent first to add blocks.');
        return;
      }

      const lastBlock = workflow.blocks[workflow.blocks.length - 1];
      const position = lastBlock
        ? { x: lastBlock.position.x + 300, y: lastBlock.position.y }
        : { x: 250, y: 150 };

      const newBlock: Block = {
        id: `${blockDef.blockType}-${Date.now()}`,
        normalizedId: normalizeBlockName(blockDef.label),
        type: blockDef.blockType,
        name: blockDef.label,
        description: blockDef.description,
        config: getDefaultConfig(blockDef.blockType) as Block['config'],
        position,
        timeout: 30,
      };

      addBlock(newBlock);
    },
    [workflow, addBlock]
  );

  const handleAddIntegrationTool = useCallback(
    (tool: ToolItem) => {
      if (!workflow) {
        toast.warning('Create or select an agent first to add blocks.');
        return;
      }

      const lastBlock = workflow.blocks[workflow.blocks.length - 1];
      const position = lastBlock
        ? { x: lastBlock.position.x + 300, y: lastBlock.position.y }
        : { x: 250, y: 150 };

      const newBlock: Block = {
        id: `code_block-${Date.now()}`,
        normalizedId: normalizeBlockName(tool.display_name || tool.name),
        type: 'code_block',
        name: tool.display_name || tool.name,
        description: tool.description,
        config: {
          type: 'code_block',
          toolName: tool.name,
          argumentMapping: {},
        },
        position,
        timeout: 30,
      };

      addBlock(newBlock);
    },
    [workflow, addBlock]
  );

  // Filter blocks by search
  const query = searchQuery.toLowerCase().trim();

  const filteredCategories = query
    ? BLOCK_CATEGORIES.map(cat => ({
        ...cat,
        blocks: cat.blocks.filter(
          b =>
            b.label.toLowerCase().includes(query) ||
            b.description.toLowerCase().includes(query) ||
            cat.name.toLowerCase().includes(query)
        ),
      })).filter(cat => cat.blocks.length > 0)
    : BLOCK_CATEGORIES;

  const filteredIntegrations = query
    ? integrationTools
        .map(cat => ({
          ...cat,
          tools: cat.tools.filter(
            t =>
              t.name.toLowerCase().includes(query) ||
              t.display_name.toLowerCase().includes(query) ||
              t.description.toLowerCase().includes(query) ||
              t.keywords?.some(k => k.toLowerCase().includes(query))
          ),
        }))
        .filter(cat => cat.tools.length > 0)
    : integrationTools;

  const totalIntegrationCount = integrationTools.reduce((sum, cat) => sum + cat.tools.length, 0);

  return (
    <div className={cn('flex flex-col h-full bg-[var(--color-bg-secondary)]', className)}>
      {/* Header */}
      <div className="px-3 py-2 border-b border-[var(--color-border)]">
        <div className="flex items-center justify-between mb-2">
          <h3 className="text-sm font-semibold text-[var(--color-text-primary)]">Add Blocks</h3>
          {onClose && (
            <button
              onClick={onClose}
              className="p-1 rounded-md text-[var(--color-text-tertiary)] hover:text-[var(--color-text-primary)] hover:bg-[var(--color-bg-tertiary)] transition-colors"
              title="Hide Block Palette"
            >
              <PanelLeftClose size={16} />
            </button>
          )}
        </div>
        <div className="relative">
          <Search
            size={14}
            className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--color-text-tertiary)]"
          />
          <input
            type="text"
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            placeholder="Search blocks & tools..."
            className="w-full pl-8 pr-3 py-1.5 text-xs rounded-md bg-[var(--color-bg-tertiary)] border border-[var(--color-border)] text-[var(--color-text-primary)] placeholder:text-[var(--color-text-tertiary)] focus:outline-none focus:border-[var(--color-accent)]"
          />
        </div>
      </div>

      {/* No workflow banner */}
      {!hasWorkflow && (
        <div className="mx-3 mt-2 p-2 rounded-md bg-amber-500/10 border border-amber-500/20">
          <p className="text-[11px] text-amber-400">
            Create or select an agent first to add blocks to the canvas.
          </p>
        </div>
      )}

      {/* Scrollable list */}
      <div className="flex-1 overflow-y-auto p-2 space-y-0.5">
        {/* ── Core Blocks ── */}
        {filteredCategories.map(category => (
          <div key={category.name} className="mb-1">
            <div className="px-1 py-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--color-text-tertiary)]">
              {category.name}
            </div>
            <div className="space-y-0.5">
              {category.blocks.map(blockDef => (
                <BlockRow
                  key={blockDef.id}
                  blockDef={blockDef}
                  disabled={!hasWorkflow}
                  onAdd={() => handleAddBlock(blockDef)}
                />
              ))}
            </div>
          </div>
        ))}

        {/* No results for blocks */}
        {query && filteredCategories.length === 0 && filteredIntegrations.length === 0 && (
          <p className="text-xs text-[var(--color-text-tertiary)] text-center py-4">
            No blocks match &quot;{searchQuery}&quot;
          </p>
        )}

        {/* ── Integrations (collapsible section) ── */}
        <div className="mt-3 pt-2 border-t border-[var(--color-border)]">
          <button
            onClick={() => setShowIntegrations(!showIntegrations)}
            className="flex items-center gap-1.5 w-full px-1 py-1.5 text-[11px] font-semibold uppercase tracking-wider text-[var(--color-text-tertiary)] hover:text-[var(--color-text-secondary)] transition-colors"
          >
            <Plug size={12} />
            Integrations
            {totalIntegrationCount > 0 && (
              <span className="text-[10px] font-normal opacity-60">{totalIntegrationCount}</span>
            )}
            <ChevronDown
              size={12}
              className={cn(
                'ml-auto transition-transform',
                showIntegrations ? 'rotate-0' : '-rotate-90'
              )}
            />
          </button>
          <p className="px-1 text-[10px] text-[var(--color-text-tertiary)] mb-1">
            Pre-configured tool blocks (Discord, Slack, GitHub, etc.)
          </p>

          {showIntegrations && (
            <div className="space-y-0.5">
              {isLoadingTools ? (
                <div className="flex items-center gap-2 px-2 py-3 text-xs text-[var(--color-text-tertiary)]">
                  <Loader2 size={14} className="animate-spin" />
                  Loading integrations...
                </div>
              ) : (
                (query ? filteredIntegrations : integrationTools).map(cat => (
                  <div key={cat.name}>
                    <button
                      onClick={() => toggleIntegrationCategory(cat.name)}
                      className="flex items-center gap-1.5 w-full px-1 py-1 text-[11px] font-medium text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] transition-colors"
                    >
                      <ChevronRight
                        size={10}
                        className={cn(
                          'transition-transform flex-shrink-0',
                          expandedIntegrations.has(cat.name) && 'rotate-90'
                        )}
                      />
                      {cat.name}
                      <span className="text-[10px] font-normal ml-auto text-[var(--color-text-tertiary)]">
                        {cat.tools.length}
                      </span>
                    </button>
                    {expandedIntegrations.has(cat.name) && (
                      <div className="space-y-0.5 ml-2 mb-1">
                        {cat.tools.map(tool => (
                          <IntegrationToolRow
                            key={tool.name}
                            tool={tool}
                            disabled={!hasWorkflow}
                            onAdd={() => handleAddIntegrationTool(tool)}
                          />
                        ))}
                      </div>
                    )}
                  </div>
                ))
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// ============================================================================
// Row components
// ============================================================================

function BlockRow({
  blockDef,
  disabled,
  onAdd,
}: {
  blockDef: BlockDef;
  disabled: boolean;
  onAdd: () => void;
}) {
  const Icon = blockDef.icon;
  return (
    <button
      onClick={onAdd}
      disabled={disabled}
      className={cn(
        'flex items-center gap-2.5 w-full px-2 py-2 rounded-md text-left transition-colors group',
        disabled
          ? 'opacity-50 cursor-not-allowed'
          : 'hover:bg-[var(--color-bg-tertiary)] cursor-pointer'
      )}
    >
      <div className={cn('p-1.5 rounded-md flex-shrink-0', blockDef.bgColor, blockDef.color)}>
        <Icon size={15} />
      </div>
      <div className="flex-1 min-w-0">
        <p className="text-xs font-medium text-[var(--color-text-primary)] truncate">
          {blockDef.label}
        </p>
        <p className="text-[10px] text-[var(--color-text-tertiary)] truncate">
          {blockDef.description}
        </p>
      </div>
      <Plus
        size={14}
        className={cn(
          'flex-shrink-0 transition-opacity',
          disabled
            ? 'opacity-0'
            : 'opacity-0 group-hover:opacity-60 text-[var(--color-text-tertiary)]'
        )}
      />
    </button>
  );
}

function IntegrationToolRow({
  tool,
  disabled,
  onAdd,
}: {
  tool: ToolItem;
  disabled: boolean;
  onAdd: () => void;
}) {
  return (
    <button
      onClick={onAdd}
      disabled={disabled}
      className={cn(
        'flex items-center gap-2 w-full px-2 py-1.5 rounded-md text-left transition-colors group',
        disabled
          ? 'opacity-50 cursor-not-allowed'
          : 'hover:bg-[var(--color-bg-tertiary)] cursor-pointer'
      )}
    >
      <div className="p-1 rounded text-blue-400 flex-shrink-0">
        {tool.icon && tool.icon in icons ? (
          (() => {
            const LucideIcon = icons[tool.icon as keyof typeof icons];
            return <LucideIcon size={13} />;
          })()
        ) : (
          <Wrench size={13} />
        )}
      </div>
      <div className="flex-1 min-w-0">
        <p className="text-[11px] font-medium text-[var(--color-text-primary)] truncate">
          {tool.display_name || tool.name}
        </p>
        <p className="text-[10px] text-[var(--color-text-tertiary)] truncate">{tool.description}</p>
      </div>
      <Plus
        size={12}
        className={cn(
          'flex-shrink-0 transition-opacity',
          disabled
            ? 'opacity-0'
            : 'opacity-0 group-hover:opacity-60 text-[var(--color-text-tertiary)]'
        )}
      />
    </button>
  );
}
