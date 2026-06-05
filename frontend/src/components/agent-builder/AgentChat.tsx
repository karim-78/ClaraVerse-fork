import { useState, useRef, useEffect, useCallback, useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Send,
  Bot,
  Loader2,
  AlertCircle,
  Edit2,
  ChevronDown,
  History,
  Plus,
  Trash2,
  Clock,
  MessageSquare,
  Search,
  Rocket,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  Menu,
  Mic,
  MicOff,
  Play,
  Hammer,
  HelpCircle,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { useIsMobile } from '@/hooks';
import { useAgentBuilderStore } from '@/store/useAgentBuilderStore';
import { MarkdownRenderer } from '@/components/design-system';
import { useModelStore } from '@/store/useModelStore';
import { filterAgentModels } from '@/services/modelService';
import { useAuthStore } from '@/store/useAuthStore';
import { useCredentials } from '@/store/useCredentialsStore';
import { normalizeBlockName } from '@/utils/blockUtils';
import {
  deleteBuilderConversation,
  createBuilderConversation,
} from '@/services/conversationService';
import { generateWorkflow, isSuccessfulGeneration } from '@/services/workflowService';
import type { BuilderMessage, Workflow, Block, Connection } from '@/types/agent';
import { ChatExecutionOutput } from './ChatExecutionOutput';

// ============================================================================
// Helper: Detect if workflow needs user input based on request
// ============================================================================

/**
 * Analyzes the user's request to determine if the workflow needs user input.
 * Returns false for automated/scheduled workflows (bots, monitors, daily tasks).
 * Returns true for workflows that process user-provided data.
 */
function detectRequiresInput(userMessage: string): boolean {
  const message = userMessage.toLowerCase();

  // Patterns that suggest NO input needed (automated/scheduled tasks)
  const noInputPatterns = [
    // Scheduling patterns
    /\b(daily|every\s*day|everyday)\b/,
    /\b(hourly|every\s*hour)\b/,
    /\b(weekly|every\s*week)\b/,
    /\b(monthly|every\s*month)\b/,
    /\b(scheduled|schedule|cron)\b/,
    /\b(periodic|periodically)\b/,
    /\b(automatic|automated|automatically)\b/,
    // Bot patterns (posts/sends content without needing input)
    /\bbot\s+that\s+(posts?|sends?|shares?)\b/,
    /\b(posts?|sends?|shares?)\s+(daily|hourly|weekly)\b/,
    // Notification/alert patterns
    /\b(notifier|notification|alert|alerts)\s+(for|when)\b/,
    /\bmonitor(s|ing)?\b/,
    /\b(reminder|reminders)\b/,
    // Content generation without input
    /\b(motivational|inspirational)\s+quotes?\b/,
    /\bquote\s+of\s+the\s+day\b/,
    /\b(weather|news|stock)\s+(update|report|alert)/,
    /\btrending\s+news\b/,
  ];

  // Patterns that suggest input IS needed (processing user data)
  const inputNeededPatterns = [
    /\b(analyze|process|convert|summarize|review|check|read)\s+(this|the|my|a|an)\b/,
    /\b(upload|uploaded|file|document|pdf|csv|image|photo)\b/,
    /\bfrom\s+(user|input|text|file)\b/,
    /\bgiven\s+(a|the|some)\b/,
    /\bbased\s+on\s+(user|input|the)\b/,
    /\bask\s+(me|user|for)\b/,
    /\bwhen\s+(i|user)\s+(provide|give|enter|type)\b/,
  ];

  // Check if it matches "no input" patterns
  const matchesNoInput = noInputPatterns.some(pattern => pattern.test(message));

  // Check if it explicitly needs input
  const matchesNeedsInput = inputNeededPatterns.some(pattern => pattern.test(message));

  // If explicitly needs input, return true
  if (matchesNeedsInput && !matchesNoInput) {
    return true;
  }

  // If matches no-input patterns and doesn't explicitly need input
  if (matchesNoInput && !matchesNeedsInput) {
    return false;
  }

  // Default: require input (safer default)
  return true;
}

// ============================================================================
// Agent Suggestions - Dynamic suggestions based on enabled integrations
// ============================================================================

export interface AgentSuggestion {
  text: string;
  requiredIntegration?: string; // Integration type required for this suggestion
}

export const AGENT_SUGGESTIONS: AgentSuggestion[] = [
  // General suggestions (no integration required) - always available
  { text: 'Build a research assistant that searches the web and summarizes findings' },
  { text: 'Create an agent that analyzes CSV data and generates insights' },
  { text: 'Make a code reviewer that checks scripts for best practices' },
  { text: 'Build a web scraper that extracts product prices from websites' },
  { text: 'Create a math tutor that solves equations step by step' },
  { text: 'Make an agent that transcribes audio files and summarizes them' },
  { text: 'Build a document analyzer that extracts key insights from PDFs' },
  { text: 'Create a sentiment analyzer for customer reviews' },
  { text: 'Make an agent that generates SQL queries from natural language' },
  { text: 'Build a data cleaner that processes and validates spreadsheets' },
  { text: 'Create an image describer that analyzes and captions photos' },
  { text: 'Make a timezone converter that schedules across time zones' },
  { text: 'Build an API tester that validates endpoint responses' },
  { text: 'Create a content writer that researches and drafts articles' },
  { text: 'Make an agent that compares prices across multiple websites' },
  { text: 'Build a recipe finder based on available ingredients' },
  { text: 'Create a language translator that preserves context and tone' },
  { text: 'Make an agent that generates meeting agendas from topics' },
  { text: 'Build a study guide generator from textbook chapters' },
  { text: 'Create a changelog generator from git commits' },

  // Discord suggestions
  {
    text: 'Create a Discord bot that posts daily motivational quotes',
    requiredIntegration: 'discord',
  },
  {
    text: 'Build an agent that sends Discord alerts for website changes',
    requiredIntegration: 'discord',
  },
  { text: 'Make a Discord notifier for GitHub repository updates', requiredIntegration: 'discord' },
  {
    text: 'Create a Discord bot that summarizes long conversations',
    requiredIntegration: 'discord',
  },
  { text: 'Build an agent that posts weather updates to Discord', requiredIntegration: 'discord' },
  {
    text: 'Make a Discord bot that shares trending news headlines',
    requiredIntegration: 'discord',
  },
  { text: 'Create a Discord notifier for stock price alerts', requiredIntegration: 'discord' },

  // Slack suggestions
  { text: 'Build a Slack bot that posts daily standup reminders', requiredIntegration: 'slack' },
  { text: 'Create a Slack notifier for completed CI/CD pipelines', requiredIntegration: 'slack' },
  { text: 'Make a Slack bot that summarizes email threads', requiredIntegration: 'slack' },
  { text: 'Build an agent that posts weekly team metrics to Slack', requiredIntegration: 'slack' },
  { text: 'Create a Slack bot for customer support ticket alerts', requiredIntegration: 'slack' },
  { text: 'Make a Slack notifier for calendar event reminders', requiredIntegration: 'slack' },
  { text: 'Build a Slack bot that shares daily industry news', requiredIntegration: 'slack' },

  // Telegram suggestions
  {
    text: 'Create a Telegram bot that sends expense tracking reminders',
    requiredIntegration: 'telegram',
  },
  { text: 'Build a Telegram notifier for crypto price alerts', requiredIntegration: 'telegram' },
  { text: 'Make a Telegram bot that forwards important emails', requiredIntegration: 'telegram' },
  { text: 'Create a Telegram bot for daily habit tracking', requiredIntegration: 'telegram' },
  { text: 'Build a Telegram notifier for server health status', requiredIntegration: 'telegram' },
  { text: 'Make a Telegram bot that sends recipe suggestions', requiredIntegration: 'telegram' },

  // Notion suggestions
  {
    text: 'Build an agent that creates Notion pages from meeting notes',
    requiredIntegration: 'notion',
  },
  {
    text: 'Create a Notion updater that logs daily tasks automatically',
    requiredIntegration: 'notion',
  },
  {
    text: 'Make an agent that syncs web bookmarks to Notion database',
    requiredIntegration: 'notion',
  },
  { text: 'Build a Notion bot that organizes research findings', requiredIntegration: 'notion' },
  { text: 'Create an agent that updates Notion project statuses', requiredIntegration: 'notion' },
  { text: 'Make a Notion tracker for reading list progress', requiredIntegration: 'notion' },

  // GitHub suggestions
  {
    text: 'Build an agent that creates GitHub issues from bug reports',
    requiredIntegration: 'github',
  },
  { text: 'Create a GitHub bot that labels issues automatically', requiredIntegration: 'github' },
  { text: 'Make an agent that summarizes PR changes', requiredIntegration: 'github' },
  { text: 'Build a GitHub notifier for stale issues', requiredIntegration: 'github' },
  { text: 'Create an agent that generates release notes', requiredIntegration: 'github' },
  { text: 'Make a GitHub bot that assigns reviewers to PRs', requiredIntegration: 'github' },

  // Google Chat suggestions
  { text: 'Build a Google Chat bot for meeting reminders', requiredIntegration: 'google_chat' },
  {
    text: 'Create a Google Chat notifier for form submissions',
    requiredIntegration: 'google_chat',
  },
  { text: 'Make a Google Chat bot that shares daily reports', requiredIntegration: 'google_chat' },
];

/**
 * Get random suggestions based on enabled integrations with weighted selection.
 * Integration-specific suggestions get 3x weight when the integration is enabled.
 */
function getRandomSuggestions(enabledIntegrations: string[], count: number = 3): AgentSuggestion[] {
  // Get all general suggestions (always available)
  const generalSuggestions = AGENT_SUGGESTIONS.filter(s => !s.requiredIntegration);

  // Get integration-specific suggestions for enabled integrations only
  const integrationSuggestions = AGENT_SUGGESTIONS.filter(
    s => s.requiredIntegration && enabledIntegrations.includes(s.requiredIntegration)
  );

  // Build weighted pool - integration suggestions get 3x weight
  const weightedPool: AgentSuggestion[] = [
    ...generalSuggestions,
    ...integrationSuggestions,
    ...integrationSuggestions, // 2x
    ...integrationSuggestions, // 3x weight for integration suggestions
  ];

  // Shuffle using Fisher-Yates algorithm
  const shuffled = [...weightedPool];
  for (let i = shuffled.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [shuffled[i], shuffled[j]] = [shuffled[j], shuffled[i]];
  }

  // Pick unique suggestions (avoid duplicates due to weighting)
  const selected: AgentSuggestion[] = [];
  const seenTexts = new Set<string>();

  for (const suggestion of shuffled) {
    if (!seenTexts.has(suggestion.text) && selected.length < count) {
      selected.push(suggestion);
      seenTexts.add(suggestion.text);
    }
  }

  return selected;
}

interface AgentChatProps {
  className?: string;
  onOpenSidebar?: () => void;
  onCloseSidebar?: () => void;
}

export function AgentChat({ className, onOpenSidebar, onCloseSidebar }: AgentChatProps) {
  const navigate = useNavigate();
  const {
    currentAgent,
    selectedAgentId,
    workflow,
    builderMessages,
    isGenerating,
    isLoadingConversation,
    currentConversationId,
    conversationList,
    pendingChatMessage,
    addBuilderMessage,
    setIsGenerating,
    setWorkflow,
    loadConversation,
    persistMessage,
    fetchConversationList,
    selectConversation,
    clearConversation,
    setPendingChatMessage,
    highlightChatInput,
    setHighlightChatInput,
    saveCurrentAgent,
    setActiveView,
    executionStatus,
  } = useAgentBuilderStore();

  // Disable interaction while loading conversation or generating
  const isInteractionDisabled = isGenerating || isLoadingConversation;

  const {
    models,
    selectedModelId,
    fetchModels,
    getDefaultModelForContext,
    setDefaultModelForContext,
  } = useModelStore();
  const isMobile = useIsMobile();

  const [inputValue, setInputValue] = useState('');
  const [builderModelId, setBuilderModelId] = useState<string | null>(null);
  const [askModelId, setAskModelId] = useState<string | null>(null);
  const [isModelDropdownOpen, setIsModelDropdownOpen] = useState(false);
  const [modelSearchQuery, setModelSearchQuery] = useState('');
  const [isHistoryOpen, setIsHistoryOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [showExecutionBar, setShowExecutionBar] = useState(false);
  const [executionBarProgress, setExecutionBarProgress] = useState(100);
  const [isRecording, setIsRecording] = useState(false);
  const [isTranscribing, setIsTranscribing] = useState(false);
  // Desktop: Show execution output instead of chat during workflow runs
  const [showOutputView, setShowOutputView] = useState(false);
  // Chat mode: 'builder' for modifying workflow, 'ask' for getting help/info
  const [chatMode, setChatMode] = useState<'builder' | 'ask'>('builder');

  // Filter models: first filter by agents_enabled, then by search query
  const filteredModels = useMemo(() => {
    const agentModels = filterAgentModels(models);
    if (!modelSearchQuery.trim()) return agentModels;
    const query = modelSearchQuery.toLowerCase();
    return agentModels.filter(
      model =>
        model.name.toLowerCase().includes(query) ||
        model.display_name?.toLowerCase().includes(query) ||
        model.provider_name?.toLowerCase().includes(query)
    );
  }, [models, modelSearchQuery]);

  const messagesEndRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const audioChunksRef = useRef<Blob[]>([]);

  // Auto-scroll to bottom when messages change
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [builderMessages]);

  // Handle execution completion with decaying progress bar
  useEffect(() => {
    if (
      executionStatus === 'completed' ||
      executionStatus === 'failed' ||
      executionStatus === 'partial_failure'
    ) {
      setShowExecutionBar(true);
      setExecutionBarProgress(100);

      // Start the decay animation
      const decayInterval = setInterval(() => {
        setExecutionBarProgress(prev => {
          if (prev <= 0) {
            clearInterval(decayInterval);
            setShowExecutionBar(false);
            return 0;
          }
          return prev - 2; // Decay by 2% every 100ms = 5 seconds total
        });
      }, 100);

      return () => clearInterval(decayInterval);
    }
  }, [executionStatus]);

  // Desktop: Auto-switch to output view when execution starts
  useEffect(() => {
    if (!isMobile && executionStatus === 'running') {
      setShowOutputView(true);
    }
  }, [executionStatus, isMobile]);

  // Auto-resize textarea - allow growth up to 200px before scrolling
  useEffect(() => {
    if (inputRef.current) {
      inputRef.current.style.height = 'auto';
      inputRef.current.style.height = `${Math.min(inputRef.current.scrollHeight, 200)}px`;
    }
  }, [inputValue]);

  // Fetch models on mount if not already loaded
  useEffect(() => {
    if (models.length === 0) {
      fetchModels();
    }
  }, [models.length, fetchModels]);

  // Initialize builderModelId from selectedModelId or first model
  useEffect(() => {
    if (!builderModelId && models.length > 0) {
      setBuilderModelId(selectedModelId || models[0].id);
    }
  }, [builderModelId, models, selectedModelId]);

  // Load conversation from MongoDB when agent changes
  // Don't wait for builderModelId - load immediately when agent is selected
  // We intentionally omit builderModelId to prevent reload on every model change
  useEffect(() => {
    if (selectedAgentId) {
      loadConversation(selectedAgentId, builderModelId || undefined);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedAgentId, loadConversation]);

  // Reload conversation when model changes (to sync with server if needed)
  // We intentionally omit selectedAgentId since this only tracks model changes
  useEffect(() => {
    if (selectedAgentId && builderModelId) {
      // Model changed - could trigger a reload if we want model-specific conversations
      // For now, conversations are per-agent, not per-model
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [builderModelId]);

  // Fetch conversation list when history panel opens
  useEffect(() => {
    if (isHistoryOpen && selectedAgentId) {
      fetchConversationList(selectedAgentId);
    }
  }, [isHistoryOpen, selectedAgentId, fetchConversationList]);

  // Initialize builder and ask model IDs from defaults
  useEffect(() => {
    if (!builderModelId && models.length > 0) {
      const defaultBuilder = getDefaultModelForContext('builderMode');
      setBuilderModelId(defaultBuilder || selectedModelId || models[0]?.id || null);
    }
    if (!askModelId && models.length > 0) {
      const defaultAsk = getDefaultModelForContext('askMode');
      setAskModelId(defaultAsk || selectedModelId || models[0]?.id || null);
    }
  }, [models, builderModelId, askModelId, selectedModelId, getDefaultModelForContext]);

  // Get the current model for display based on chat mode
  const currentBuilderModel =
    models.find(m => m.id === (chatMode === 'ask' ? askModelId : builderModelId)) || models[0];

  // Format date for conversation history
  const formatDate = (dateString: string) => {
    const date = new Date(dateString);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    const diffHours = Math.floor(diffMs / 3600000);
    const diffDays = Math.floor(diffMs / 86400000);

    if (diffMins < 1) return 'Just now';
    if (diffMins < 60) return `${diffMins}m ago`;
    if (diffHours < 24) return `${diffHours}h ago`;
    if (diffDays < 7) return `${diffDays}d ago`;

    return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
  };

  // Handle creating new conversation
  const handleNewConversation = async () => {
    if (!selectedAgentId) return;
    try {
      await createBuilderConversation(selectedAgentId, builderModelId || '');
      clearConversation();
      await loadConversation(selectedAgentId, builderModelId || undefined);
      await fetchConversationList(selectedAgentId);
      setIsHistoryOpen(false);
    } catch (error) {
      console.error('Failed to create new conversation:', error);
    }
  };

  // Handle selecting a conversation from history
  const handleSelectHistoryConversation = async (conversationId: string) => {
    if (conversationId === currentConversationId) {
      setIsHistoryOpen(false);
      return;
    }
    await selectConversation(conversationId);
    setIsHistoryOpen(false);
  };

  // Handle deleting a conversation
  const handleDeleteConversation = async (conversationId: string, e: React.MouseEvent) => {
    e.stopPropagation();
    if (!selectedAgentId) return;

    if (confirm('Delete this conversation? This cannot be undone.')) {
      try {
        await deleteBuilderConversation(selectedAgentId, conversationId);
        await fetchConversationList(selectedAgentId);
        if (conversationId === currentConversationId) {
          clearConversation();
          await loadConversation(selectedAgentId, builderModelId || undefined);
        }
      } catch (error) {
        console.error('Failed to delete conversation:', error);
      }
    }
  };

  // Process workflow response from the structured output API
  const processWorkflowResponse = useCallback(
    (
      response: import('@/services/workflowService').WorkflowGenerateResponse,
      userMessage: string
    ) => {
      if (isSuccessfulGeneration(response) && response.workflow) {
        const apiWorkflow = response.workflow;
        const isModification = response.action === 'modify';
        const timestamp = Date.now();

        // Detect if the workflow needs user input based on the request
        const requiresInput = detectRequiresInput(userMessage);

        // Create a complete workflow object with normalized IDs
        const newWorkflow: Workflow = {
          id: workflow?.id || `workflow-${timestamp}`,
          blocks: apiWorkflow.blocks.map((block: Partial<Block>, index: number) => {
            const blockName = block.name || `Block ${index + 1}`;
            const isStartBlock = block.type === 'variable';

            // For Start blocks, set requiresInput based on detection
            const blockConfig = isStartBlock
              ? { ...block.config, type: block.type || 'variable', requiresInput }
              : block.config || { type: block.type || 'llm_inference' };

            return {
              id: block.id || `block-${timestamp}-${index}`,
              normalizedId: normalizeBlockName(blockName),
              type: block.type || 'llm_inference',
              name: blockName,
              description: block.description || '',
              config: blockConfig,
              position: block.position || { x: 250, y: 50 + index * 150 },
              timeout: block.timeout || 30,
            };
          }) as Block[],
          connections: (apiWorkflow.connections || []).map(
            (conn: Partial<Connection>, index: number) => ({
              id: conn.id || `conn-${timestamp}-${index}`,
              sourceBlockId: conn.sourceBlockId || '',
              sourceOutput: conn.sourceOutput || 'output',
              targetBlockId: conn.targetBlockId || '',
              targetInput: conn.targetInput || 'input',
            })
          ) as Connection[],
          variables: apiWorkflow.variables || [],
          version: response.version || (isModification ? (workflow?.version || 1) + 1 : 1),
        };

        // Apply the workflow to the store
        setWorkflow(newWorkflow);

        // Create version description for AI-generated changes
        const versionDescription = isModification
          ? `Modified: ${response.explanation?.substring(0, 50) || 'Workflow updated'}...`
          : `Created: ${response.explanation?.substring(0, 50) || 'Initial workflow'}...`;

        // Track version locally for immediate UI feedback
        useAgentBuilderStore.getState().saveWorkflowVersion(versionDescription);

        // Persist workflow to backend with version snapshot (AI-created workflows create versions)
        useAgentBuilderStore
          .getState()
          .saveCurrentAgent(true, versionDescription) // createVersion=true for AI-generated changes
          .catch(err => {
            console.warn('Failed to auto-save workflow:', err);
          });

        // Update agent name and description if AI suggested them
        // Check if we have suggestions AND agent still has default name
        const state = useAgentBuilderStore.getState();
        const hasDefaultName = state.currentAgent?.name === 'New Agent';
        if ((response.suggested_name || response.suggested_description) && hasDefaultName) {
          if (state.currentAgent) {
            const updates: { name?: string; description?: string } = {};
            if (response.suggested_name) {
              updates.name = response.suggested_name;
            }
            if (response.suggested_description) {
              updates.description = response.suggested_description;
            }
            console.log('📝 [AGENT] AI suggested metadata:', updates);
            state.updateAgent(state.currentAgent.id, updates);
          }
        }

        // Add assistant message with workflow update
        const actionExplanation = isModification
          ? `Modified workflow (v${newWorkflow.version}): ${newWorkflow.blocks.length} blocks.`
          : `Created a ${newWorkflow.blocks.length}-block workflow.`;

        const assistantMsg = {
          role: 'assistant' as const,
          content: response.explanation || 'Workflow generated successfully.',
          workflowUpdate: {
            action: response.action,
            workflow: newWorkflow,
            explanation: actionExplanation,
          },
          id: `msg-${Date.now()}`,
          timestamp: new Date(),
        };
        addBuilderMessage(assistantMsg);
        persistMessage(assistantMsg);
      } else {
        // Generation failed - show error message
        const assistantMsg = {
          role: 'assistant' as const,
          content: response.error || response.explanation || 'Failed to generate workflow.',
          id: `msg-${Date.now()}`,
          timestamp: new Date(),
        };
        addBuilderMessage(assistantMsg);
        persistMessage(assistantMsg);
      }
    },
    [setWorkflow, addBuilderMessage, persistMessage, workflow]
  );

  // Send Ask mode request - provides workflow context and helps user understand
  const sendAskModeRequest = useCallback(
    async (userMessage: string) => {
      if (!selectedAgentId) {
        setError('No agent selected');
        return;
      }

      const modelId = askModelId || selectedModelId || models[0]?.id;

      try {
        // Import API and get tool registry
        const { api } = await import('@/services/api');
        const { getToolRegistry } = await import('@/services/workflowService');
        const { generateDeploymentCode } = await import('@/services/deployCodeGenerator');

        // Get available tools
        const toolRegistry = await getToolRegistry();
        const availableTools = toolRegistry.tools.map(t => ({
          name: t.name,
          description: t.description,
          category: t.category,
        }));

        // Check if user is asking for workflow changes
        // Only trigger on clear modification intents, not general questions
        const changePatterns = [
          /\b(add|create|build|update|modify|change|remove|delete)\s+(a\s+)?(new\s+)?(block|tool|step|node|connection)/i,
          /\bcan you (add|create|build|update|modify|change|remove|delete)\b/i,
          /\b(please|could you|would you)\s+(add|create|build|update|modify|change|remove|delete)/i,
          /\bi (want|need)\s+(to\s+)?(add|create|build|update|modify|change|remove|delete)/i,
        ];
        const isAskingForChanges = changePatterns.some(pattern => pattern.test(userMessage));

        if (isAskingForChanges) {
          // Redirect to Builder mode with helpful message
          const assistantMsg = {
            role: 'assistant' as const,
            content:
              "I can see you want to make changes to the workflow! Please switch to **Builder** mode to modify your agent. I'm in **Ask** mode, which is for answering questions about your workflow, available tools, and deployment options.\n\nClick the **Builder** button above to switch modes.",
            id: `msg-${Date.now()}`,
            timestamp: new Date(),
          };
          addBuilderMessage(assistantMsg);
          persistMessage(assistantMsg);
          setIsGenerating(false);
          return;
        }

        // Build context from workflow
        let workflowContext = '';
        if (workflow && workflow.blocks.length > 0) {
          workflowContext = `\nCurrent Workflow:\n${workflow.blocks
            .map(
              (block, i) =>
                `${i + 1}. ${block.name} (${block.type}): ${block.description || 'No description'}`
            )
            .join('\n')}`;
        }

        // Generate deployment code sample for context
        const deploymentExample =
          currentAgent && workflow && workflow.blocks.length > 0
            ? generateDeploymentCode({
                language: 'curl',
                triggerUrl: `${window.location.origin}/api/trigger/${selectedAgentId}`,
                statusUrl: `${window.location.origin}/api/trigger/status/:executionId`,
                apiKey: 'YOUR_API_KEY',
                agentId: selectedAgentId,
                startBlockConfig: workflow.blocks.find(b => b.type === 'variable')?.config || null,
              })
            : '';

        // Call simple chat API with context
        const response = await api.post<{ response: string }>('/api/agents/ask', {
          agent_id: selectedAgentId,
          message: userMessage,
          model_id: modelId,
          context: {
            workflow: workflow || null,
            available_tools: availableTools,
            deployment_example: deploymentExample,
          },
        });

        // Add assistant response
        const assistantMsg = {
          role: 'assistant' as const,
          content: response.response || 'I can help you understand your workflow!',
          id: `msg-${Date.now()}`,
          timestamp: new Date(),
        };
        addBuilderMessage(assistantMsg);
        persistMessage(assistantMsg);
      } catch (err) {
        console.error('Ask mode request failed:', err);
        setError(err instanceof Error ? err.message : 'Failed to get help');
      } finally {
        setIsGenerating(false);
      }
    },
    [
      selectedAgentId,
      askModelId,
      selectedModelId,
      models,
      workflow,
      currentAgent,
      addBuilderMessage,
      persistMessage,
      setIsGenerating,
    ]
  );

  // Helper: Extract recent conversation history for better AI context
  const getRecentHistory = useCallback(
    (pairCount: number = 3): Array<{ role: 'user' | 'assistant'; content: string }> => {
      // Get last N*2 messages (N pairs) from builderMessages
      const historySize = pairCount * 2;
      const recentMessages = builderMessages.slice(-historySize);

      return recentMessages.map(msg => ({
        role: msg.role as 'user' | 'assistant',
        content: msg.content,
      }));
    },
    [builderMessages]
  );

  // Send workflow generation request using dedicated REST endpoint
  const sendWorkflowRequest = useCallback(
    async (userMessage: string) => {
      if (!selectedAgentId) {
        setError('No agent selected');
        return;
      }

      const modelId = builderModelId || selectedModelId || models[0]?.id;

      // Extract recent conversation history for better tool selection
      const conversationHistory = getRecentHistory(3);

      try {
        const response = await generateWorkflow(
          selectedAgentId,
          userMessage,
          workflow,
          modelId,
          conversationHistory
        );

        processWorkflowResponse(response, userMessage);
      } catch (err) {
        console.error('Workflow generation failed:', err);
        setError(err instanceof Error ? err.message : 'Failed to generate workflow');
      } finally {
        setIsGenerating(false);
      }
    },
    [
      selectedAgentId,
      builderModelId,
      selectedModelId,
      models,
      workflow,
      processWorkflowResponse,
      setIsGenerating,
      getRecentHistory,
    ]
  );

  // Handle pending chat message (from "Fix with Agent" button)
  useEffect(() => {
    if (highlightChatInput) {
      // If it's just a highlight request (from PostExecutionGuidance)
      // Focus input after a short delay to ensure visibility
      setTimeout(() => {
        if (inputRef.current) {
          inputRef.current.focus();
        }
      }, 100);

      // Clear highlight after animation
      setTimeout(() => {
        setHighlightChatInput(false);
      }, 3000);
    }

    if (pendingChatMessage && !isInteractionDisabled) {
      // If it's a prefill (from PostExecutionGuidance), set input and focus
      if (highlightChatInput) {
        setInputValue(pendingChatMessage);
        setPendingChatMessage(null);

        // Focus input after a short delay to ensure visibility
        setTimeout(() => {
          if (inputRef.current) {
            inputRef.current.focus();
            // Move cursor to end
            inputRef.current.setSelectionRange(
              inputRef.current.value.length,
              inputRef.current.value.length
            );
          }
        }, 100);
        return;
      }

      // Otherwise, it's an auto-send message (like from Onboarding)
      // Clear the pending message first
      setPendingChatMessage(null);

      // Send the message
      const userMsg = {
        role: 'user' as const,
        content: pendingChatMessage,
        id: `msg-${Date.now()}`,
        timestamp: new Date(),
      };
      addBuilderMessage(userMsg);
      persistMessage(userMsg);
      setIsGenerating(true);
      setError(null);

      // Use the dedicated workflow generation endpoint
      sendWorkflowRequest(pendingChatMessage);
    }
  }, [
    pendingChatMessage,
    isInteractionDisabled,
    setPendingChatMessage,
    addBuilderMessage,
    persistMessage,
    setIsGenerating,
    sendWorkflowRequest,
    highlightChatInput,
    setHighlightChatInput,
  ]);

  const handleSend = async () => {
    if (!inputValue.trim() || isInteractionDisabled) return;

    const userMessage = inputValue.trim();
    setInputValue('');
    setError(null);

    // Add user message and persist to MongoDB
    const userMsg = {
      role: 'user' as const,
      content: userMessage,
      id: `msg-${Date.now()}`,
      timestamp: new Date(),
    };
    addBuilderMessage(userMsg);
    persistMessage(userMsg);

    setIsGenerating(true);

    if (chatMode === 'ask') {
      // Ask mode: Help user understand workflow, tools, deployment docs
      await sendAskModeRequest(userMessage);
    } else {
      // Builder mode: Modify workflow
      await sendWorkflowRequest(userMessage);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  // Voice recording functions
  const transcribeAudio = useCallback(
    async (audioBlob: Blob) => {
      setIsTranscribing(true);
      try {
        // Create form data with audio file
        const formData = new FormData();
        formData.append('file', audioBlob, 'recording.webm');

        // Upload to backend transcription endpoint
        const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || window.location.origin;
        const response = await fetch(`${apiBaseUrl}/api/audio/transcribe`, {
          method: 'POST',
          body: formData,
        });

        if (!response.ok) {
          throw new Error(`Transcription failed: ${response.statusText}`);
        }

        const result = await response.json();
        if (result.text) {
          // Set the transcribed text to input value
          setInputValue(result.text);
          // Focus the input
          if (inputRef.current) {
            inputRef.current.focus();
          }
        } else {
          throw new Error('No transcription text received');
        }
      } catch (error) {
        console.error('Transcription error:', error);
        alert(error instanceof Error ? error.message : 'Failed to transcribe audio');
      } finally {
        setIsTranscribing(false);
      }
    },
    [setInputValue]
  );

  const startRecording = useCallback(async () => {
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const mediaRecorder = new MediaRecorder(stream, {
        mimeType: 'audio/webm',
      });

      audioChunksRef.current = [];

      mediaRecorder.ondataavailable = event => {
        if (event.data.size > 0) {
          audioChunksRef.current.push(event.data);
        }
      };

      mediaRecorder.onstop = async () => {
        // Stop all tracks to release microphone
        stream.getTracks().forEach(track => track.stop());

        if (audioChunksRef.current.length > 0) {
          const audioBlob = new Blob(audioChunksRef.current, {
            type: mediaRecorder.mimeType,
          });
          await transcribeAudio(audioBlob);
        }
      };

      mediaRecorderRef.current = mediaRecorder;
      mediaRecorder.start();
      setIsRecording(true);
    } catch (error) {
      console.error('Failed to start recording:', error);
      if (error instanceof Error && error.name === 'NotAllowedError') {
        alert('Microphone access denied. Please allow microphone access to use voice input.');
      } else {
        alert('Failed to start recording. Please check your microphone.');
      }
    }
  }, [transcribeAudio]);

  const stopRecording = useCallback(() => {
    if (mediaRecorderRef.current && mediaRecorderRef.current.state !== 'inactive') {
      mediaRecorderRef.current.stop();
      setIsRecording(false);
    }
  }, []);

  const toggleRecording = useCallback(() => {
    if (isRecording) {
      stopRecording();
    } else {
      startRecording();
    }
  }, [isRecording, startRecording, stopRecording]);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      if (mediaRecorderRef.current && mediaRecorderRef.current.state !== 'inactive') {
        mediaRecorderRef.current.stop();
      }
    };
  }, []);

  // Handle deploying the agent from execution result message
  const handleDeploy = useCallback(async () => {
    if (!currentAgent) return;

    try {
      // Save the agent first
      await saveCurrentAgent();

      // Update agent status to deployed via API
      const { api } = await import('@/services/api');
      await api.put(`/api/agents/${currentAgent.id}`, { status: 'deployed' });

      // Navigate to deployed view
      setActiveView('deployed');
      navigate(`/agents/deployed/${currentAgent.id}`);
    } catch (error) {
      console.error('Failed to deploy agent:', error);
    }
  }, [currentAgent, saveCurrentAgent, setActiveView, navigate]);

  // Handle suggestion chip selection
  const handleSuggestionSelect = useCallback(
    (text: string) => {
      if (isInteractionDisabled) return;

      setInputValue(text);
      // Trigger send after a brief delay to show the input
      setTimeout(async () => {
        setInputValue('');

        const userMsg = {
          role: 'user' as const,
          content: text,
          id: `msg-${Date.now()}`,
          timestamp: new Date(),
        };
        addBuilderMessage(userMsg);
        persistMessage(userMsg);
        setIsGenerating(true);
        setError(null);

        // Use the dedicated workflow generation endpoint
        await sendWorkflowRequest(text);
      }, 100);
    },
    [isInteractionDisabled, addBuilderMessage, persistMessage, setIsGenerating, sendWorkflowRequest]
  );

  // Welcome state when no agent is selected
  if (!currentAgent) {
    return (
      <div
        className={cn(
          'flex flex-col items-center justify-center h-full p-6 text-center',
          className
        )}
      >
        <div className="w-16 h-16 rounded-2xl bg-[var(--color-accent)] flex items-center justify-center mb-6">
          <Bot size={32} className="text-white" />
        </div>
        <h3 className="text-lg font-semibold text-[var(--color-text-primary)] mb-2">
          No Agent Selected
        </h3>
        <p className="text-sm text-[var(--color-text-secondary)] max-w-[280px]">
          Select an agent from the sidebar or create a new one to start building your workflow.
        </p>
      </div>
    );
  }

  return (
    <div className={cn('flex flex-col h-full overflow-hidden', className)}>
      {/* Execution Status Progress Bar */}
      {showExecutionBar && (
        <div className="absolute top-0 left-0 right-0 z-50">
          <div
            className={cn(
              'h-1 transition-all duration-100 ease-linear',
              executionStatus === 'completed' && 'bg-green-500',
              executionStatus === 'failed' && 'bg-red-500',
              executionStatus === 'partial_failure' && 'bg-yellow-500'
            )}
            style={{ width: `${executionBarProgress}%` }}
          />
        </div>
      )}

      {/* Header with Glass Effect */}
      <div
        className={cn(
          'flex-shrink-0 bg-[var(--color-bg-secondary)] bg-opacity-50 backdrop-blur-xl',
          isMobile ? 'px-3 py-3' : 'px-5 py-4'
        )}
      >
        {isMobile ? (
          /* Mobile: Spacious row with touch-friendly buttons */
          <>
            <div className="flex items-center justify-between gap-3">
              {/* Left: Hamburger */}
              {onOpenSidebar && (
                <button
                  onClick={onOpenSidebar}
                  className="p-2 rounded-xl bg-[var(--color-bg-primary)]/50 text-[var(--color-text-secondary)] active:bg-[var(--color-bg-tertiary)] transition-colors"
                  aria-label="Open menu"
                >
                  <Menu size={20} />
                </button>
              )}

              {/* Center: Name only */}
              <div className="flex-1 min-w-0 flex flex-col items-center">
                <h3 className="text-base font-semibold text-[var(--color-text-primary)] truncate max-w-[180px]">
                  {currentAgent.name}
                </h3>
              </div>

              {/* Right: History + Edit */}
              <div className="flex items-center gap-1">
                <button
                  onClick={() => {
                    onCloseSidebar?.();
                    setIsHistoryOpen(!isHistoryOpen);
                  }}
                  className={cn(
                    'p-2 rounded-xl transition-all',
                    isHistoryOpen
                      ? 'bg-[var(--color-accent)]/20 text-[var(--color-accent)]'
                      : 'bg-[var(--color-bg-primary)]/50 text-[var(--color-text-secondary)]'
                  )}
                >
                  <History size={18} />
                </button>
                <button
                  onClick={() => {
                    onCloseSidebar?.();
                    const agent = useAgentBuilderStore.getState().currentAgent;
                    if (agent) {
                      const newName = prompt('Rename agent:', agent.name);
                      if (newName?.trim()) {
                        useAgentBuilderStore
                          .getState()
                          .updateAgent(agent.id, { name: newName.trim() });
                      }
                    }
                  }}
                  className="p-2 rounded-xl bg-[var(--color-bg-primary)]/50 text-[var(--color-text-secondary)]"
                >
                  <Edit2 size={18} />
                </button>
              </div>
            </div>

            {/* Mobile Model Dropdown - compact centered modal */}
            {isModelDropdownOpen && (
              <>
                <div
                  className="fixed inset-0 z-[9998] bg-black/70"
                  onClick={() => {
                    setIsModelDropdownOpen(false);
                    setModelSearchQuery('');
                  }}
                />
                <div className="fixed left-4 right-4 top-20 z-[9999] max-h-[60vh] rounded-2xl bg-[#1a1a1a] border border-[var(--color-border)] shadow-2xl overflow-hidden flex flex-col">
                  {/* Search input */}
                  <div className="p-3 border-b border-[var(--color-border)]">
                    <div className="relative">
                      <Search
                        size={16}
                        className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-text-tertiary)]"
                      />
                      <input
                        type="text"
                        value={modelSearchQuery}
                        onChange={e => setModelSearchQuery(e.target.value)}
                        placeholder="Search models..."
                        className="w-full pl-10 pr-3 py-2.5 text-sm bg-[var(--color-bg-tertiary)] border border-[var(--color-border)] rounded-xl text-[var(--color-text-primary)] placeholder:text-[var(--color-text-tertiary)] focus:outline-none focus:border-[var(--color-accent)]"
                        autoFocus
                      />
                    </div>
                  </div>
                  {/* Model list */}
                  <div className="flex-1 overflow-y-auto py-2">
                    {filteredModels.length === 0 ? (
                      <div className="px-4 py-6 text-center text-sm text-[var(--color-text-tertiary)]">
                        {models.length === 0
                          ? 'No models available'
                          : 'No models match your search'}
                      </div>
                    ) : (
                      filteredModels.map(model => (
                        <button
                          key={model.id}
                          onClick={() => {
                            if (chatMode === 'ask') {
                              setAskModelId(model.id);
                              setDefaultModelForContext('askMode', model.id);
                            } else {
                              setBuilderModelId(model.id);
                              setDefaultModelForContext('builderMode', model.id);
                            }
                            setIsModelDropdownOpen(false);
                            setModelSearchQuery('');
                          }}
                          className={cn(
                            'w-full px-4 py-3 text-left text-sm active:bg-[var(--color-bg-tertiary)] transition-colors flex items-center gap-3',
                            model.id === (chatMode === 'ask' ? askModelId : builderModelId)
                              ? 'text-[var(--color-accent)] bg-[var(--color-accent)]/10'
                              : 'text-[var(--color-text-primary)]'
                          )}
                        >
                          {model.provider_favicon && (
                            <img
                              src={model.provider_favicon}
                              alt={model.provider_name}
                              className="w-6 h-6 rounded-md flex-shrink-0"
                              onError={e => {
                                (e.target as HTMLImageElement).style.display = 'none';
                              }}
                            />
                          )}
                          <div className="min-w-0 flex-1">
                            <div className="font-medium truncate">
                              {model.display_name || model.name}
                            </div>
                            {model.provider_name && (
                              <div className="text-xs text-[var(--color-text-tertiary)] mt-0.5 truncate">
                                {model.provider_name}
                              </div>
                            )}
                          </div>
                        </button>
                      ))
                    )}
                  </div>
                </div>
              </>
            )}

            {/* Mobile History Dropdown - compact centered modal */}
            {isHistoryOpen && (
              <>
                <div
                  className="fixed inset-0 z-[9998] bg-black/70"
                  onClick={() => setIsHistoryOpen(false)}
                />
                <div className="fixed left-4 right-4 top-20 z-[9999] max-h-[60vh] rounded-2xl bg-[#1a1a1a] border border-[var(--color-border)] shadow-2xl overflow-hidden flex flex-col">
                  {/* Header */}
                  <div className="flex items-center justify-between px-4 py-3 border-b border-[var(--color-border)]">
                    <span className="text-sm font-semibold text-[var(--color-text-primary)]">
                      Conversation History
                    </span>
                    <button
                      onClick={handleNewConversation}
                      className="p-2 rounded-xl bg-[var(--color-bg-tertiary)] text-[var(--color-text-secondary)] active:bg-[var(--color-accent)]/20 transition-colors"
                      title="New conversation"
                    >
                      <Plus size={18} />
                    </button>
                  </div>

                  {/* Conversation list */}
                  <div className="flex-1 overflow-y-auto py-2">
                    {conversationList.length === 0 ? (
                      <div className="px-4 py-8 text-center">
                        <MessageSquare
                          size={32}
                          className="mx-auto mb-3 text-[var(--color-text-tertiary)] opacity-50"
                        />
                        <p className="text-sm text-[var(--color-text-tertiary)]">
                          No conversations yet
                        </p>
                      </div>
                    ) : (
                      conversationList.map(conv => (
                        <div
                          key={conv.id}
                          onClick={() => handleSelectHistoryConversation(conv.id)}
                          role="button"
                          tabIndex={0}
                          className={cn(
                            'w-full px-4 py-3 text-left transition-colors flex items-center justify-between active:bg-[var(--color-bg-tertiary)]',
                            conv.id === currentConversationId && 'bg-[var(--color-accent)]/10'
                          )}
                        >
                          <div className="flex-1 min-w-0">
                            <div className="flex items-center gap-2">
                              <Clock size={14} className="text-[var(--color-text-tertiary)]" />
                              <span className="text-sm text-[var(--color-text-primary)]">
                                {formatDate(conv.updated_at)}
                              </span>
                            </div>
                            <div className="mt-1 flex items-center gap-1.5 ml-5">
                              <MessageSquare
                                size={12}
                                className="text-[var(--color-text-tertiary)]"
                              />
                              <span className="text-xs text-[var(--color-text-tertiary)]">
                                {conv.message_count} message{conv.message_count !== 1 ? 's' : ''}
                              </span>
                            </div>
                          </div>
                          <button
                            onClick={e => handleDeleteConversation(conv.id, e)}
                            className="p-2 rounded-xl text-[var(--color-text-tertiary)] active:bg-red-500/20 active:text-red-400 transition-all"
                            title="Delete"
                          >
                            <Trash2 size={16} />
                          </button>
                        </div>
                      ))
                    )}
                  </div>

                  {/* Footer */}
                  <div className="px-4 py-2 border-t border-[var(--color-border)]">
                    <p className="text-xs text-[var(--color-text-tertiary)] text-center">
                      Encrypted with AES-256
                    </p>
                  </div>
                </div>
              </>
            )}
          </>
        ) : (
          /* Desktop: Original layout */
          <div className="flex items-center justify-between gap-3">
            <div className="flex-1 min-w-0">
              <h3 className="text-base font-semibold text-[var(--color-text-primary)] truncate">
                {currentAgent.name}
              </h3>
            </div>

            {/* History button with dropdown */}
            <div className="relative">
              <button
                onClick={() => setIsHistoryOpen(!isHistoryOpen)}
                className={cn(
                  'flex-shrink-0 p-2 rounded-xl transition-all',
                  isHistoryOpen
                    ? 'bg-[var(--color-accent)]/10 text-[var(--color-accent)]'
                    : 'hover:bg-[var(--color-bg-primary)] hover:bg-opacity-60 text-[var(--color-text-secondary)] hover:text-[var(--color-accent)]'
                )}
                title="Conversation history"
              >
                <History size={16} />
              </button>

              {isHistoryOpen && (
                <>
                  {/* Backdrop */}
                  <div className="fixed inset-0 z-10" onClick={() => setIsHistoryOpen(false)} />
                  {/* History dropdown */}
                  <div className="absolute top-full right-0 mt-1 z-20 w-64 max-h-[320px] overflow-y-auto rounded-xl bg-[#1a1a1a]/95 backdrop-blur-xl border border-[var(--color-border)] shadow-xl">
                    {/* Header */}
                    <div className="flex items-center justify-between px-3 py-2 border-b border-[var(--color-border)]">
                      <span className="text-xs font-medium text-[var(--color-text-primary)]">
                        History
                      </span>
                      <button
                        onClick={handleNewConversation}
                        className="p-1 rounded-md hover:bg-[var(--color-bg-tertiary)] text-[var(--color-text-secondary)] hover:text-[var(--color-accent)] transition-colors"
                        title="New conversation"
                      >
                        <Plus size={14} />
                      </button>
                    </div>

                    {/* Conversation list */}
                    <div className="py-1">
                      {conversationList.length === 0 ? (
                        <div className="px-3 py-4 text-center">
                          <MessageSquare
                            size={24}
                            className="mx-auto mb-2 text-[var(--color-text-tertiary)] opacity-50"
                          />
                          <p className="text-xs text-[var(--color-text-tertiary)]">
                            No conversations yet
                          </p>
                        </div>
                      ) : (
                        conversationList.map(conv => (
                          <div
                            key={conv.id}
                            onClick={() => handleSelectHistoryConversation(conv.id)}
                            role="button"
                            tabIndex={0}
                            onKeyDown={e =>
                              e.key === 'Enter' && handleSelectHistoryConversation(conv.id)
                            }
                            className={cn(
                              'w-full px-3 py-2 text-left transition-colors group cursor-pointer',
                              'hover:bg-[var(--color-bg-tertiary)]',
                              conv.id === currentConversationId && 'bg-[var(--color-accent)]/10'
                            )}
                          >
                            <div className="flex items-start justify-between">
                              <div className="flex-1 min-w-0">
                                <div className="flex items-center gap-1.5">
                                  <Clock size={10} className="text-[var(--color-text-tertiary)]" />
                                  <span className="text-xs text-[var(--color-text-secondary)]">
                                    {formatDate(conv.updated_at)}
                                  </span>
                                </div>
                                <div className="mt-0.5 flex items-center gap-1">
                                  <MessageSquare
                                    size={9}
                                    className="text-[var(--color-text-tertiary)]"
                                  />
                                  <span className="text-[10px] text-[var(--color-text-tertiary)]">
                                    {conv.message_count} message
                                    {conv.message_count !== 1 ? 's' : ''}
                                  </span>
                                </div>
                              </div>
                              <button
                                onClick={e => handleDeleteConversation(conv.id, e)}
                                className="p-1 rounded-md opacity-0 group-hover:opacity-100 hover:bg-red-500/20 text-[var(--color-text-tertiary)] hover:text-red-400 transition-all"
                                title="Delete"
                              >
                                <Trash2 size={11} />
                              </button>
                            </div>
                          </div>
                        ))
                      )}
                    </div>

                    {/* Footer */}
                    <div className="px-3 py-1.5 border-t border-[var(--color-border)]">
                      <p className="text-[9px] text-[var(--color-text-tertiary)] text-center">
                        Encrypted with AES-256
                      </p>
                    </div>
                  </div>
                </>
              )}
            </div>

            <button
              onClick={() => {
                const agent = useAgentBuilderStore.getState().currentAgent;
                if (agent) {
                  const newName = prompt('Rename agent:', agent.name);
                  if (newName && newName.trim()) {
                    useAgentBuilderStore.getState().updateAgent(agent.id, { name: newName.trim() });
                  }
                }
              }}
              className="flex-shrink-0 p-2 rounded-xl hover:bg-[var(--color-bg-primary)] hover:bg-opacity-60 text-[var(--color-text-secondary)] hover:text-[var(--color-accent)] transition-all"
              title="Rename agent"
            >
              <Edit2 size={16} />
            </button>
          </div>
        )}
      </div>

      {/* Desktop: Show execution output or chat based on state */}
      {!isMobile && showOutputView ? (
        <ChatExecutionOutput
          onBackToChat={() => setShowOutputView(false)}
          onDeploy={handleDeploy}
        />
      ) : (
        <>
          {/* Messages */}
          <div className="flex-1 overflow-y-auto p-4 space-y-4">
            {/* Loading conversation indicator */}
            {isLoadingConversation && (
              <div className="flex items-center justify-center py-8">
                <Loader2 size={24} className="animate-spin text-[var(--color-accent)]" />
                <span className="ml-2 text-sm text-[var(--color-text-tertiary)]">
                  Loading conversation...
                </span>
              </div>
            )}

            {!isLoadingConversation && builderMessages.length === 0 ? (
              <WelcomeMessage
                agentName={currentAgent.name}
                onSuggestionSelect={handleSuggestionSelect}
                isDisabled={isInteractionDisabled}
              />
            ) : !isLoadingConversation ? (
              builderMessages.map(message => (
                <MessageBubble key={message.id} message={message} onDeploy={handleDeploy} />
              ))
            ) : null}

            {/* Generating indicator */}
            {isGenerating && (
              <div className="flex items-start gap-3">
                <div className="flex-shrink-0 w-8 h-8 rounded-full bg-gradient-to-br from-[var(--color-accent)] to-[var(--color-accent-hover)] flex items-center justify-center shadow-lg animate-pulse">
                  <Bot size={16} className="text-white" />
                </div>
                <div className="flex items-center gap-2.5 px-4 py-2.5 rounded-2xl bg-[var(--color-bg-tertiary)] bg-opacity-60 backdrop-blur-sm border border-[var(--color-border)] border-opacity-30 text-[var(--color-text-secondary)] shadow-md">
                  <Loader2 size={16} className="animate-spin text-[var(--color-accent)]" />
                  <span className="text-sm font-medium">
                    {chatMode === 'ask' ? 'Thinking...' : 'Generating workflow...'}
                  </span>
                </div>
              </div>
            )}

            {/* Error message */}
            {error && (
              <div className="flex items-start gap-3">
                <div className="flex-shrink-0 w-8 h-8 rounded-full bg-red-500/20 backdrop-blur-sm flex items-center justify-center border border-red-500/30">
                  <AlertCircle size={16} className="text-red-500" />
                </div>
                <div className="max-w-[85%] px-4 py-3 rounded-2xl rounded-tl-sm bg-red-500/10 backdrop-blur-sm border border-red-500/30 text-red-500 text-sm shadow-md">
                  <p>{error}</p>
                </div>
              </div>
            )}

            <div ref={messagesEndRef} />
          </div>

          {/* Input Area - Command Center Style */}
          <div
            className={cn(
              'flex-shrink-0 px-3 pb-3 pt-2 bg-[var(--color-bg-secondary)]',
              highlightChatInput && 'z-10'
            )}
          >
            <div
              className={cn(
                'relative rounded-md bg-[var(--color-bg-primary)] border border-[var(--color-border)] transition-all duration-200',
                'focus-within:border-[var(--color-accent)]',
                highlightChatInput && 'ring-2 ring-[var(--color-accent)]'
              )}
            >
              {/* Textarea */}
              <textarea
                ref={inputRef}
                value={inputValue}
                onChange={e => setInputValue(e.target.value)}
                onKeyDown={handleKeyDown}
                placeholder={
                  highlightChatInput
                    ? 'Describe the issue and let the agent handle the rest...'
                    : isLoadingConversation
                      ? 'Loading conversation...'
                      : chatMode === 'ask'
                        ? 'Ask about your workflow, tools, or deployment...'
                        : 'Describe your agent workflow...'
                }
                disabled={isInteractionDisabled}
                rows={1}
                className="w-full px-3 pt-2.5 pb-12 bg-transparent border-none text-[var(--color-text-primary)] placeholder:text-[var(--color-text-tertiary)] focus:outline-none resize-none text-sm disabled:opacity-50"
                style={{ minHeight: '80px', maxHeight: '200px' }}
              />

              {/* Controls Row - Bottom positioned */}
              <div className="absolute bottom-2 left-2 right-2 flex items-center justify-between">
                {/* Voice Button - Left */}
                <button
                  onClick={toggleRecording}
                  disabled={isTranscribing || isInteractionDisabled}
                  className={cn(
                    'p-1.5 rounded transition-colors text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] hover:bg-[var(--color-bg-tertiary)]',
                    isRecording && 'bg-red-500/10 text-red-500',
                    isTranscribing && 'opacity-50 cursor-not-allowed'
                  )}
                  title={
                    isTranscribing
                      ? 'Transcribing...'
                      : isRecording
                        ? 'Click to stop recording'
                        : 'Voice input'
                  }
                >
                  {isTranscribing ? (
                    <Loader2 size={16} className="animate-spin" />
                  ) : isRecording ? (
                    <MicOff size={16} />
                  ) : (
                    <Mic size={16} />
                  )}
                </button>

                {/* Right side controls */}
                <div className="flex items-center gap-2">
                  {/* Mode Selector */}
                  <div className="flex items-center gap-1 p-0.5 rounded bg-[var(--color-bg-primary)]/50">
                    <button
                      onClick={() => setChatMode('builder')}
                      className={cn(
                        'flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-medium transition-all',
                        chatMode === 'builder'
                          ? 'bg-[var(--color-accent)] text-white'
                          : 'text-[var(--color-text-tertiary)] hover:text-[var(--color-text-secondary)]'
                      )}
                      title="Builder mode - modify workflow"
                    >
                      <Hammer size={10} />
                      Builder
                    </button>
                    <button
                      onClick={() => setChatMode('ask')}
                      className={cn(
                        'flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-medium transition-all',
                        chatMode === 'ask'
                          ? 'bg-[var(--color-accent)] text-white'
                          : 'text-[var(--color-text-tertiary)] hover:text-[var(--color-text-secondary)]'
                      )}
                      title="Ask mode - get help about workflow"
                    >
                      <HelpCircle size={10} />
                      Ask
                    </button>
                  </div>

                  {/* Model Selector Dropdown - Show for both modes */}
                  <div className="relative">
                    <button
                      onClick={() => setIsModelDropdownOpen(!isModelDropdownOpen)}
                      className="flex items-center gap-1.5 px-2 py-1 rounded bg-[var(--color-bg-primary)] bg-opacity-50 hover:bg-opacity-80 text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] transition-all"
                      title={
                        chatMode === 'ask'
                          ? 'Select model for Ask mode'
                          : 'Select model for Builder mode'
                      }
                    >
                      {currentBuilderModel?.provider_favicon && (
                        <img
                          src={currentBuilderModel.provider_favicon}
                          alt={currentBuilderModel.provider_name}
                          className="w-3.5 h-3.5 rounded-sm"
                          onError={e => {
                            (e.target as HTMLImageElement).style.display = 'none';
                          }}
                        />
                      )}
                      <span className="truncate max-w-[120px]">
                        {currentBuilderModel?.display_name ||
                          currentBuilderModel?.name ||
                          'Select Model'}
                      </span>
                      <ChevronDown
                        size={12}
                        className={cn('transition-transform', isModelDropdownOpen && 'rotate-180')}
                      />
                    </button>

                    {isModelDropdownOpen && (
                      <>
                        {/* Backdrop to close dropdown */}
                        <div
                          className="fixed inset-0 z-40"
                          onClick={() => {
                            setIsModelDropdownOpen(false);
                            setModelSearchQuery('');
                          }}
                        />
                        {/* Dropdown container */}
                        <div className="absolute bottom-full right-0 mb-2 z-50 w-[240px] rounded-xl bg-[#1a1a1a]/95 backdrop-blur-xl border border-[var(--color-border)] shadow-xl overflow-hidden">
                          {/* Search input */}
                          <div className="p-2 border-b border-[var(--color-border)]">
                            <div className="relative">
                              <Search
                                size={12}
                                className="absolute left-2 top-1/2 -translate-y-1/2 text-[var(--color-text-tertiary)]"
                              />
                              <input
                                type="text"
                                value={modelSearchQuery}
                                onChange={e => setModelSearchQuery(e.target.value)}
                                placeholder="Search models..."
                                className="w-full pl-6 pr-2 py-1.5 text-xs bg-[var(--color-bg-tertiary)] border border-[var(--color-border)] rounded-md text-[var(--color-text-primary)] placeholder:text-[var(--color-text-tertiary)] focus:outline-none focus:border-[var(--color-accent)]"
                                autoFocus
                              />
                            </div>
                          </div>
                          {/* Model list with styled scrollbar */}
                          <div className="max-h-[200px] overflow-y-auto py-1 model-dropdown-scrollbar">
                            {filteredModels.length === 0 ? (
                              <div className="px-3 py-2 text-xs text-[var(--color-text-tertiary)]">
                                {models.length === 0
                                  ? 'No models available'
                                  : 'No models match your search'}
                              </div>
                            ) : (
                              filteredModels.map(model => (
                                <button
                                  key={model.id}
                                  onClick={() => {
                                    if (chatMode === 'ask') {
                                      setAskModelId(model.id);
                                      setDefaultModelForContext('askMode', model.id);
                                    } else {
                                      setBuilderModelId(model.id);
                                      setDefaultModelForContext('builderMode', model.id);
                                    }
                                    setIsModelDropdownOpen(false);
                                    setModelSearchQuery('');
                                  }}
                                  className={cn(
                                    'w-full px-3 py-2 text-left text-xs hover:bg-[var(--color-bg-tertiary)] transition-colors flex items-center gap-2 overflow-hidden',
                                    model.id === (chatMode === 'ask' ? askModelId : builderModelId)
                                      ? 'text-[var(--color-accent)] bg-[var(--color-accent)]/10'
                                      : 'text-[var(--color-text-primary)]'
                                  )}
                                >
                                  {model.provider_favicon && (
                                    <img
                                      src={model.provider_favicon}
                                      alt={model.provider_name}
                                      className="w-4 h-4 rounded-sm flex-shrink-0"
                                      onError={e => {
                                        (e.target as HTMLImageElement).style.display = 'none';
                                      }}
                                    />
                                  )}
                                  <div className="min-w-0 flex-1 overflow-hidden">
                                    <div className="font-medium truncate">
                                      {model.display_name || model.name}
                                    </div>
                                    {model.provider_name && (
                                      <div className="text-[10px] text-[var(--color-text-tertiary)] mt-0.5 truncate">
                                        {model.provider_name}
                                      </div>
                                    )}
                                  </div>
                                </button>
                              ))
                            )}
                          </div>
                        </div>
                      </>
                    )}
                  </div>

                  {/* Send Button */}
                  {/* View Output Button - shown when there's execution data */}
                  {!isMobile && executionStatus && (
                    <button
                      onClick={() => setShowOutputView(true)}
                      className="p-1.5 rounded transition-all flex items-center justify-center bg-white/5 hover:bg-white/10 text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)]"
                      title="View execution output"
                    >
                      <Play size={16} />
                    </button>
                  )}

                  <button
                    onClick={handleSend}
                    disabled={!inputValue.trim() || isInteractionDisabled}
                    className={cn(
                      'p-1.5 rounded transition-all flex items-center justify-center',
                      inputValue.trim() && !isInteractionDisabled
                        ? 'bg-[var(--color-accent)] text-white hover:bg-[var(--color-accent-hover)]'
                        : 'bg-[var(--color-bg-tertiary)] text-[var(--color-text-tertiary)] cursor-not-allowed opacity-50'
                    )}
                    title="Send message"
                  >
                    <Send size={16} />
                  </button>
                </div>
              </div>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

// Welcome Message Component
interface WelcomeMessageProps {
  agentName: string;
  onSuggestionSelect: (text: string) => void;
  isDisabled: boolean;
}

function WelcomeMessage({ agentName, onSuggestionSelect, isDisabled }: WelcomeMessageProps) {
  // Get credentials from store (stable reference)
  const credentials = useCredentials();

  // Compute enabled integration types from credentials
  const enabledIntegrations = useMemo(() => {
    const types = new Set<string>();
    for (const cred of credentials) {
      types.add(cred.integrationType);
    }
    return Array.from(types);
  }, [credentials]);

  // Get 3 random suggestions - use useState to keep stable across re-renders
  const [suggestions] = useState(() => getRandomSuggestions(enabledIntegrations, 3));

  return (
    <div className="flex flex-col items-center justify-center h-full text-center px-4">
      <div className="w-16 h-16 rounded-2xl bg-[var(--color-accent)] flex items-center justify-center mb-5">
        <Bot size={28} className="text-white" />
      </div>
      <h3 className="text-base font-semibold text-[var(--color-text-primary)] mb-2">
        Build "{agentName}"
      </h3>
      <p className="text-sm text-[var(--color-text-secondary)] max-w-[260px] mb-5 leading-relaxed">
        Describe what you want this agent to do, and I'll create a workflow for you.
      </p>
      <div className="w-full space-y-2.5">
        {suggestions.map((suggestion, index) => (
          <SuggestionChip
            key={index}
            text={suggestion.text}
            onSelect={onSuggestionSelect}
            disabled={isDisabled}
          />
        ))}
      </div>
    </div>
  );
}

// Suggestion Chip Component
interface SuggestionChipProps {
  text: string;
  onSelect: (text: string) => void;
  disabled?: boolean;
}

function SuggestionChip({ text, onSelect, disabled }: SuggestionChipProps) {
  return (
    <button
      onClick={() => onSelect(text)}
      disabled={disabled}
      className={cn(
        'w-full px-4 py-2.5 text-left text-xs text-[var(--color-text-secondary)] rounded-xl bg-[var(--color-bg-secondary)] border border-[var(--color-border)] hover:bg-[var(--color-bg-tertiary)] hover:border-[var(--color-accent)] hover:text-[var(--color-text-primary)] transition-all',
        disabled && 'opacity-50 cursor-not-allowed'
      )}
    >
      {text}
    </button>
  );
}

// Message Bubble Component
interface MessageBubbleProps {
  message: BuilderMessage;
  onDeploy?: () => void;
}

function MessageBubble({ message, onDeploy }: MessageBubbleProps) {
  const isUser = message.role === 'user';
  const isSystem = message.role === 'system';
  const { user } = useAuthStore();
  const { setHighlightChatInput } = useAgentBuilderStore();

  // Get user initials from display name or email
  const getUserInitials = (): string => {
    if (user?.user_metadata?.display_name) {
      const name = user.user_metadata.display_name as string;
      const parts = name.trim().split(' ');
      if (parts.length >= 2) {
        return `${parts[0][0]}${parts[1][0]}`.toUpperCase();
      }
      return name.substring(0, 2).toUpperCase();
    }
    if (user?.email) {
      return user.email.substring(0, 2).toUpperCase();
    }
    return 'U';
  };

  // Render execution result message with special styling
  if (isSystem && message.executionResult) {
    const { status } = message.executionResult;
    const isSuccess = status === 'completed';
    const isFailed = status === 'failed';

    return (
      <div className="flex items-start gap-3">
        <div
          className={cn(
            'flex-shrink-0 w-8 h-8 rounded-full flex items-center justify-center',
            isSuccess && 'bg-green-500/20',
            isFailed && 'bg-red-500/20',
            !isSuccess && !isFailed && 'bg-yellow-500/20'
          )}
        >
          {isSuccess && <CheckCircle2 size={16} className="text-green-500" />}
          {isFailed && <XCircle size={16} className="text-red-500" />}
          {!isSuccess && !isFailed && <AlertTriangle size={16} className="text-yellow-500" />}
        </div>

        <div className="flex-1 max-w-[85%]">
          <div
            className={cn(
              'px-4 py-3 rounded-2xl rounded-tl-sm text-sm overflow-hidden border',
              isSuccess && 'bg-green-500/10 border-green-500/30',
              isFailed && 'bg-red-500/10 border-red-500/30',
              !isSuccess && !isFailed && 'bg-yellow-500/10 border-yellow-500/30'
            )}
          >
            <MarkdownRenderer
              content={message.content}
              className="text-[var(--color-text-primary)] leading-relaxed"
            />
          </div>

          {/* Action buttons for successful execution */}
          {isSuccess && (
            <div className="flex gap-2 mt-2">
              <button
                onClick={onDeploy}
                className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-[var(--color-accent)] hover:bg-[var(--color-accent-hover)] text-white transition-all"
              >
                <Rocket size={12} />
                Deploy Agent
              </button>
              <button
                onClick={() => setHighlightChatInput(true)}
                className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-[var(--color-surface-elevated)] hover:bg-[var(--color-surface)] text-[var(--color-text-primary)] border border-[var(--color-border)] transition-all"
              >
                <MessageSquare size={12} />
                Refine
              </button>
            </div>
          )}
        </div>
      </div>
    );
  }

  return (
    <div className={cn('flex items-start gap-3', isUser && 'flex-row-reverse')}>
      <div
        className={cn(
          'flex-shrink-0 w-8 h-8 rounded-full flex items-center justify-center',
          isUser
            ? 'bg-[var(--color-surface-elevated)] text-[var(--color-text-secondary)]'
            : 'bg-gradient-to-br from-[var(--color-accent)] to-[var(--color-accent-hover)] shadow-lg'
        )}
      >
        {isUser ? (
          <span className="text-xs font-semibold">{getUserInitials()}</span>
        ) : (
          <Bot size={16} className="text-white" />
        )}
      </div>

      <div
        className={cn(
          'max-w-[85%] px-4 py-3 rounded-2xl text-sm overflow-hidden',
          isUser
            ? 'bg-[var(--color-surface-elevated)] text-[var(--color-text-primary)] rounded-tr-sm'
            : 'bg-[var(--color-bg-tertiary)] bg-opacity-60 backdrop-blur-sm text-[var(--color-text-primary)] rounded-tl-sm shadow-md'
        )}
      >
        {isUser ? (
          <p className="whitespace-pre-wrap leading-relaxed break-all">{message.content}</p>
        ) : (
          <MarkdownRenderer
            content={message.content}
            className="leading-relaxed [&_p]:mb-2 [&_p:last-child]:mb-0"
          />
        )}

        {message.workflowUpdate && (
          <div className="mt-2.5 pt-2.5 border-t border-[var(--color-border)]">
            <span className="text-xs opacity-80 font-medium text-[var(--color-text-secondary)]">
              Workflow {message.workflowUpdate.action === 'create' ? 'created' : 'updated'}
            </span>
          </div>
        )}
      </div>
    </div>
  );
}
