import { useState, useCallback, useEffect, useMemo, useRef } from 'react';
import { useParams, useNavigate, useLocation, useSearchParams } from 'react-router-dom';
import {
  MessageSquare,
  FolderKanban,
  Box,
  Code2,
  Search,
  Plus,
  X,
  AlertCircle,
  Check,
  PenLine,
  GraduationCap,
  Sparkles,
  Coffee,
  Trash2,
  ArrowDown,
  Menu,
  Cloud,
  Smartphone,
  Home,
  Bot,
} from 'lucide-react';
import { Panel, PanelGroup, PanelResizeHandle } from 'react-resizable-panels';
import type { NavItem, RecentChat, FooterLink } from '@/components/ui';
import {
  Sidebar,
  CommandCenter,
  NameInputModal,
  ApiKeyModal,
  CustomSpinner,
  ConfirmDialog,
  RenameDialog,
  Snowfall,
  PrivacySettingsModal,
} from '@/components/ui';
import type { CommandCenterHandle } from '@/components/ui';
import { Tooltip } from '@/components/design-system/Tooltip/Tooltip';
import {
  UserMessage,
  AssistantMessage,
} from '@/components/chat';
import { useChatStore } from '@/store/useChatStore';
import { useAuthStore } from '@/store/useAuthStore';
import { useModelStore } from '@/store/useModelStore';
import { useArtifactStore } from '@/store/useArtifactStore';
import { useSettingsStore } from '@/store/useSettingsStore';
import { useChatKnowledgeStore } from '@/store/useChatKnowledgeStore';
import { KnowledgeProjectChips } from '@/components/chat/KnowledgeProjectChips';
import { nexusService } from '@/services/nexusService';
import type { NexusProject } from '@/types/nexus';
import type { Message, ToolCall, RetryType } from '@/types/chat';
import type { ActivePrompt } from '@/types/interactivePrompt';
import { generateChatTitle, validateMessage } from '@/services/chatService';
import { websocketService } from '@/services/websocketService';
import type { ServerMessage, Attachment } from '@/types/websocket';
import { uploadFiles, toAttachment } from '@/services/uploadService';
import { checkConversationStatus, isConversationStale } from '@/services/conversationService';
import {
  hasUserName,
  setUserName,
  generateGreeting,
  generateAnonymousGreeting,
} from '@/utils/greetingUtils';
import {
  getCloudChat,
  isAuthenticated as isChatSyncAuthenticated,
} from '@/services/chatSyncService';
import { copyAsFormattedText } from '@/utils/markdownToPlainText';
import { extractArtifacts } from '@/utils/artifactParser';
import { parseSlashCommand } from '@/utils/slashCommands';
import { ConversationSearchModal } from '@/components/chat/ConversationSearchModal';
import { ArtifactPane, ArtifactsGallery } from '@/components/artifacts';
import type { Artifact } from '@/types/artifact';
import { useDocumentTitle } from '@/hooks';
import { ImageGalleryModal } from '@/components/chat/ImageGalleryModal';
import { useImageGalleryStore } from '@/store/useImageGalleryStore';
import styles from './Chat.module.css';

// Import test helper for interactive prompts (dev mode only)
if (import.meta.env.DEV) {
  import('@/utils/interactivePromptTestHelper');
}

/** Footer links for chat page - Home and Agents */
const CHAT_FOOTER_LINKS: FooterLink[] = [
  { href: '/', label: 'Home', icon: Home, ariaLabel: 'Navigate to home' },
  { href: '/agents', label: 'Agents', icon: Bot, ariaLabel: 'Navigate to agents' },
];

/**
 * Stable empty-array sentinel for the chat-knowledge selector.
 * Module-level constant so Zustand's selector returns the SAME
 * reference every render when no projects are attached — otherwise a
 * fresh [] each call triggers an infinite re-render loop.
 */
const KNOWLEDGE_EMPTY: readonly string[] = Object.freeze([]);

export const Chat = () => {
  // URL parameters and navigation
  const { chatId } = useParams<{ chatId?: string }>();
  const navigate = useNavigate();
  const location = useLocation();
  const [searchParams, setSearchParams] = useSearchParams();

  // Initial prompt from URL query parameter (for embedding feature)
  const [initialPrompt, setInitialPrompt] = useState<string | undefined>(undefined);

  // Loading state for fetching chat from URL (when not found locally)
  const [isLoadingUrlChat, setIsLoadingUrlChat] = useState(false);
  // Track if we've already handled this chatId to prevent re-fetching
  const handledChatIdRef = useRef<string | null>(null);

  // Zustand stores
  const {
    activeNav,
    isNewChat,
    searchQuery,
    isLoading,
    error,
    selectedChat,
    filteredChats,
    recentChats: getRecentChats,
    setActiveNav,
    setSearchQuery,
    selectChat,
    startNewChat,
    showHistory,
    createChat,
    updateChatTitle: updateTitle,
    toggleStarChat,
    deleteChat,
    addMessage,
    updateMessage,
    clearError,
    setError,
    setLoading,
    startStreaming,
    activePrompt,
    submitPromptResponse,
    skipPrompt,
  } = useChatStore();

  const { selectedModelId, fetchModels, isLoading: isLoadingModels } = useModelStore();
  const {
    isOpen: isArtifactPaneOpen,
    openArtifacts,
    closePane: closeArtifactPane,
    splitRatio,
    setSplitRatio,
  } = useArtifactStore();

  const { chatPrivacyMode, setChatPrivacyMode } = useSettingsStore();

  // Image gallery store for mobile fullscreen gallery
  const {
    isOpen: isGalleryOpen,
    images: galleryImages,
    initialIndex: galleryInitialIndex,
    closeGallery,
  } = useImageGalleryStore();

  // WebSocket connection state
  const [isWsConnected, setIsWsConnected] = useState(() => websocketService.isConnected());
  // Track if we've ever connected - don't show "reconnecting" on initial page load
  const [hasEverConnected, setHasEverConnected] = useState(() => websocketService.isConnected());

  // Subscribe to WebSocket connection state changes
  useEffect(() => {
    const unsubscribe = websocketService.onStateChange(state => {
      const connected = state === 'connected';
      setIsWsConnected(connected);
      if (connected) {
        setHasEverConnected(true);
      }
    });
    return unsubscribe;
  }, []);

  // Local UI state
  const [isEditingTitle, setIsEditingTitle] = useState(false);
  const [editTitleValue, setEditTitleValue] = useState('');
  const [expandedThinkingPanes, setExpandedThinkingPanes] = useState<Set<string>>(new Set());
  const manuallyCollapsedThinking = useRef<Set<string>>(new Set());
  const autoExpandedThinking = useRef<Set<string>>(new Set());
  const [showNameModal, setShowNameModal] = useState(false);
  const [showPrivacyModal, setShowPrivacyModal] = useState(false);
  const [dynamicGreeting, setDynamicGreeting] = useState('');
  // Context optimization state (for showing compaction progress)
  const [contextOptimizing, setContextOptimizing] = useState<{
    active: boolean;
    progress: number;
    message: string;
  } | null>(null);

  // Auto-animate progress bar while context optimizing is active
  useEffect(() => {
    if (!contextOptimizing || contextOptimizing.progress >= 90) return;

    // Gradually increase progress to make it feel responsive
    const interval = setInterval(() => {
      setContextOptimizing(prev => {
        if (!prev || prev.progress >= 90) return prev;
        // Slow down as we approach 90% (never quite reach it until real completion)
        const increment = Math.max(1, Math.floor((90 - prev.progress) / 10));
        return { ...prev, progress: Math.min(90, prev.progress + increment) };
      });
    }, 300);

    return () => clearInterval(interval);
  }, [contextOptimizing?.active]);

  const [isAnimatingTitle, setIsAnimatingTitle] = useState(false);
  const [displayTitle, setDisplayTitle] = useState('');
  const [copiedMessageId, setCopiedMessageId] = useState<string | null>(null);
  const [isSelectionMode, setIsSelectionMode] = useState(false);
  const [selectedChatIds, setSelectedChatIds] = useState<Set<string>>(new Set());
  const [searchOpen, setSearchOpen] = useState(false);

  // Global cmd/ctrl+K → open the conversation-search modal. Bound at the
  // window level so it works regardless of focus state. Skip when typing
  // in a contenteditable so we don't hijack Notion-style shortcuts in
  // future rich-text fields.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const isCmdK = (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k';
      if (!isCmdK) return;
      const target = e.target as HTMLElement | null;
      if (target?.isContentEditable) return;
      e.preventDefault();
      setSearchOpen(open => !open);
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, []);
  const [showScrollButton, setShowScrollButton] = useState(false);

  // Mobile sidebar state
  const [isMobile, setIsMobile] = useState(() =>
    typeof window !== 'undefined' ? window.innerWidth < 768 : false
  );
  // Sidebar open by default on desktop, closed on mobile
  const [isSidebarOpen, setIsSidebarOpen] = useState(() =>
    typeof window !== 'undefined' ? window.innerWidth >= 768 : true
  );

  // Listen for window resize to detect mobile
  useEffect(() => {
    const handleResize = () => {
      const mobile = window.innerWidth < 768;
      setIsMobile(mobile);
      if (mobile) {
        setIsSidebarOpen(false); // Close sidebar on mobile
      }
    };
    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  // Auto-collapse sidebar when artifact pane opens (desktop only)
  useEffect(() => {
    if (isArtifactPaneOpen && !isMobile) {
      setIsSidebarOpen(false);
    }
  }, [isArtifactPaneOpen, isMobile]);

  // Dialog states
  const [showDeleteDialog, setShowDeleteDialog] = useState(false);
  const [showRenameDialog, setShowRenameDialog] = useState(false);
  const [chatToDelete, setChatToDelete] = useState<string | null>(null);
  const [chatToRename, setChatToRename] = useState<{ id: string; title: string } | null>(null);

  // API Key Modal state
  const [showApiKeyModal, setShowApiKeyModal] = useState(false);
  const [pendingProviderId, setPendingProviderId] = useState<string | null>(null);
  const [pendingMessage, setPendingMessage] = useState<{
    text: string;
    isDeepThinking: boolean;
    files?: File[];
    systemInstruction?: string;
  } | null>(null);

  // Refs for auto-scrolling - SIMPLIFIED approach
  const messagesEndRef = useRef<HTMLDivElement>(null);
  // Ref for the CommandCenter to focus after stream completion
  const commandCenterRef = useRef<CommandCenterHandle>(null);
  // Track if user manually scrolled away from bottom - this is the ONLY source of truth
  const userScrolledAway = useRef(false);
  // Track if we were at bottom before content update (to determine if we should auto-scroll)
  const wasAtBottom = useRef(true);
  // Use state for the container to ensure effects re-run when it's mounted
  const [messagesAreaNode, setMessagesAreaNode] = useState<HTMLDivElement | null>(null);
  const messagesAreaRef = useCallback((node: HTMLDivElement | null) => {
    setMessagesAreaNode(node);
  }, []);

  // Refs for streaming chunk batching (performance optimization)
  const chunkBufferRef = useRef<string>('');
  const flushTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const chat = selectedChat();
  const messages = useMemo(() => chat?.messages || [], [chat?.messages]);

  // Update document title based on current state
  useDocumentTitle(chat?.title ? chat.title : activeNav === 'artifacts' ? 'Artifacts' : 'Chat');

  // ─── RAG: per-chat project knowledge attachments ──────────────────
  //
  // The user picks which project knowledge bases this chat should
  // search via search_knowledge. Selection lives in
  // useChatKnowledgeStore (persisted to localStorage by chat ID), so
  // it survives reloads and tab switches.
  //
  // Projects are loaded once on mount from the Nexus REST API. We
  // tolerate failure silently — RAG is optional, a missing project
  // list just hides the picker rather than breaking chat.
  const [knowledgeProjects, setKnowledgeProjects] = useState<NexusProject[]>([]);
  const knowledgeStoreSet = useChatKnowledgeStore(s => s.set);
  const knowledgeStorePromote = useChatKnowledgeStore(s => s.promoteDraft);
  const chatKnowledgeKey = chat?.id ?? null;
  // Subscribe to the DATA, not the get() method. Subscribing to
  // s.get returned a stable function reference that never changed,
  // so React never re-rendered when the picker toggled a project —
  // the click wrote to the store but the chip row was frozen until
  // a full page refresh. Reading from s.selections triggers a
  // proper subscription. The `?? KNOWLEDGE_EMPTY` keeps the
  // reference stable when no projects are attached (avoids an
  // infinite render loop from a fresh [] every render).
  const knowledgeProjectIds = useChatKnowledgeStore(
    s => s.selections[chatKnowledgeKey ?? ''] ?? KNOWLEDGE_EMPTY
  );

  useEffect(() => {
    let cancelled = false;
    nexusService
      .listProjects()
      .then(list => {
        if (!cancelled) setKnowledgeProjects(list);
      })
      .catch(err => {
        console.warn('[Chat] project list load failed (RAG picker hidden):', err);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const handleKnowledgeProjectsChange = useCallback(
    (ids: string[]) => knowledgeStoreSet(chatKnowledgeKey, ids),
    [chatKnowledgeKey, knowledgeStoreSet]
  );

  // Handle URL-based navigation (like ChatGPT/Claude - fetch from cloud if not local)
  useEffect(() => {
    if (location.pathname === '/artifacts') {
      // On /artifacts route, show artifacts gallery
      setActiveNav('artifacts');
      showHistory();
      return;
    }

    if (!chatId) return;

    // Skip if we've already handled this chatId
    if (handledChatIdRef.current === chatId) {
      // Still select it in case component re-rendered
      const state = useChatStore.getState();
      if (state.chats.find(c => c.id === chatId)) {
        selectChat(chatId);
        setActiveNav('chats');
      }
      return;
    }

    const handleChatNavigation = async () => {
      // Mark this chatId as being handled
      handledChatIdRef.current = chatId;

      // First, check if chat exists locally
      const state = useChatStore.getState();
      const chatExists = state.chats.find(c => c.id === chatId);

      if (chatExists) {
        selectChat(chatId);
        setActiveNav('chats');
        return;
      }

      // Chat not found locally - try to fetch from cloud (like ChatGPT/Claude do)
      if (isChatSyncAuthenticated()) {
        setIsLoadingUrlChat(true);
        try {
          const cloudChat = await getCloudChat(chatId);
          // Add the fetched chat to the store
          useChatStore.setState(state => ({
            chats: [cloudChat, ...state.chats.filter(c => c.id !== chatId)],
          }));
          // Select the chat
          selectChat(chatId);
          setActiveNav('chats');
          console.log(`✅ Loaded chat ${chatId} from cloud via URL`);
        } catch (error) {
          console.warn(`Chat ${chatId} not found in cloud, redirecting to /chat`, error);
          handledChatIdRef.current = null; // Reset so we can try again if needed
          navigate('/chat', { replace: true });
        } finally {
          setIsLoadingUrlChat(false);
        }
      } else {
        // Not authenticated - can't fetch from cloud, redirect
        console.log(`Chat ${chatId} not found locally and not authenticated, redirecting`);
        navigate('/chat', { replace: true });
      }
    };

    handleChatNavigation();
  }, [chatId, location.pathname, selectChat, setActiveNav, navigate, showHistory]);

  // Handle prompt query parameter (for embedding/sharing feature)
  useEffect(() => {
    const promptParam = searchParams.get('prompt');
    if (promptParam) {
      // Decode and set the initial prompt
      setInitialPrompt(decodeURIComponent(promptParam));
      // Clear the URL parameter to clean up the URL
      const newParams = new URLSearchParams(searchParams);
      newParams.delete('prompt');
      setSearchParams(newParams, { replace: true });
      // Ensure we're on a new chat for the prompt
      if (!isNewChat) {
        startNewChat();
      }
    }
  }, [searchParams, setSearchParams, isNewChat, startNewChat]);

  // Callback when initial prompt has been consumed by CommandCenter
  const handleInitialPromptConsumed = useCallback(() => {
    setInitialPrompt(undefined);
  }, []);

  // Toggle thinking pane expansion
  const toggleThinkingPane = useCallback((messageId: string, opts?: { scrollTo?: boolean }) => {
    // Read current state synchronously to decide direction
    setExpandedThinkingPanes(prev => {
      const isCurrentlyExpanded = prev.has(messageId);

      // Set ref FIRST (synchronously within the same tick) so the
      // auto-expand effect sees it before it can re-expand
      if (isCurrentlyExpanded) {
        manuallyCollapsedThinking.current.add(messageId);
      } else {
        manuallyCollapsedThinking.current.delete(messageId);
      }

      const newSet = new Set(prev);
      if (isCurrentlyExpanded) {
        newSet.delete(messageId);
      } else {
        newSet.add(messageId);

        // Scroll to the thinking block if requested
        if (opts?.scrollTo) {
          requestAnimationFrame(() => {
            const el = document.querySelector(`[data-thinking-id="${messageId}"]`);
            el?.scrollIntoView({ behavior: 'smooth', block: 'start' });
          });
        }
      }
      return newSet;
    });
  }, []);

  // Auto-expand thinking pane ONCE when reasoning first appears during streaming.
  // Uses autoExpandedThinking ref to ensure we never re-expand after the initial open.
  useEffect(() => {
    if (!chat?.messages) return;

    const lastMessage = chat.messages[chat.messages.length - 1];
    if (lastMessage?.role === 'assistant' && lastMessage.reasoning && lastMessage.isStreaming) {
      // Only auto-expand once per message — never re-expand on subsequent chunks
      if (!autoExpandedThinking.current.has(lastMessage.id)) {
        autoExpandedThinking.current.add(lastMessage.id);
        setExpandedThinkingPanes(prev => {
          const newSet = new Set(prev);
          newSet.add(lastMessage.id);
          return newSet;
        });
      }
    }

    // Clean up tracking refs when streaming ends
    if (lastMessage?.role === 'assistant' && !lastMessage.isStreaming) {
      manuallyCollapsedThinking.current.delete(lastMessage.id);
      autoExpandedThinking.current.delete(lastMessage.id);
    }
  }, [chat?.messages]);

  // Toggle tool call expansion in a message
  const toggleToolExpansion = useCallback(
    (messageId: string, toolId: string) => {
      if (!chat) return;
      const message = chat.messages.find(msg => msg.id === messageId);
      if (!message || !message.toolCalls) return;

      const updatedToolCalls = message.toolCalls.map(tool =>
        tool.id === toolId ? { ...tool, isExpanded: !tool.isExpanded } : tool
      );

      updateMessage(chat.id, messageId, { toolCalls: updatedToolCalls });
    },
    [chat, updateMessage]
  );

  // Toggle all tools expansion state for a message
  const toggleAllTools = useCallback(
    (messageId: string, toolCalls: ToolCall[], expand: boolean) => {
      if (!chat) return;
      const updatedToolCalls = toolCalls.map(tool => ({
        ...tool,
        isExpanded: expand,
      }));
      updateMessage(chat.id, messageId, { toolCalls: updatedToolCalls });
    },
    [chat, updateMessage]
  );

  const chatTitle = chat?.title || 'New Conversation';
  const hasStartedChat = messages.length > 0;
  const chatList = filteredChats();
  const recentChatsForSidebar = getRecentChats();

  // Check if we're near the bottom of the scroll container
  const isNearBottom = useCallback(
    (threshold = 150) => {
      if (!messagesAreaNode) return true;
      const { scrollTop, scrollHeight, clientHeight } = messagesAreaNode;
      return scrollHeight - scrollTop - clientHeight < threshold;
    },
    [messagesAreaNode]
  );

  // Scroll to bottom - called when user clicks the scroll button or sends a message
  const scrollToBottom = useCallback(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
    userScrolledAway.current = false;
    wasAtBottom.current = true;
    setShowScrollButton(false);
  }, []);

  // Handle wheel events - this is the ONLY way to detect user-initiated scroll
  // Wheel events are fired ONLY when user scrolls with mouse/trackpad
  useEffect(() => {
    if (!messagesAreaNode) return;

    const handleWheel = (e: WheelEvent) => {
      // User is actively scrolling with wheel/trackpad
      // Check direction: deltaY > 0 means scrolling down, deltaY < 0 means scrolling up
      if (e.deltaY < 0) {
        // User scrolled UP - they want to see previous content
        userScrolledAway.current = true;
        setShowScrollButton(true);
      } else if (e.deltaY > 0) {
        // User scrolled DOWN - check if they reached the bottom
        // Use setTimeout to let the scroll complete first
        setTimeout(() => {
          if (isNearBottom(150)) {
            userScrolledAway.current = false;
            setShowScrollButton(false);
          }
        }, 50);
      }
    };

    // Handle touch scrolling for mobile
    let touchStartY = 0;
    const handleTouchStart = (e: TouchEvent) => {
      touchStartY = e.touches[0].clientY;
    };

    const handleTouchMove = (e: TouchEvent) => {
      const touchCurrentY = e.touches[0].clientY;
      const deltaY = touchStartY - touchCurrentY;

      if (deltaY < -10) {
        // Swiping down (scrolling up in content) - user wants to see previous content
        userScrolledAway.current = true;
        setShowScrollButton(true);
      } else if (deltaY > 10) {
        // Swiping up (scrolling down in content)
        setTimeout(() => {
          if (isNearBottom(150)) {
            userScrolledAway.current = false;
            setShowScrollButton(false);
          }
        }, 50);
      }
    };

    messagesAreaNode.addEventListener('wheel', handleWheel, { passive: true });
    messagesAreaNode.addEventListener('touchstart', handleTouchStart, { passive: true });
    messagesAreaNode.addEventListener('touchmove', handleTouchMove, { passive: true });

    return () => {
      messagesAreaNode.removeEventListener('wheel', handleWheel);
      messagesAreaNode.removeEventListener('touchstart', handleTouchStart);
      messagesAreaNode.removeEventListener('touchmove', handleTouchMove);
    };
    // messagesAreaNode as dependency ensures this runs when container mounts
  }, [messagesAreaNode, isNearBottom]);

  // Update scroll button visibility based on position (for initial load, etc.)
  useEffect(() => {
    if (!messagesAreaNode) return;

    const checkScrollButton = () => {
      const hasScroll = messagesAreaNode.scrollHeight > messagesAreaNode.clientHeight + 10;
      const atBottom = isNearBottom(150);

      // Only show button if there's scrollable content and we're not at bottom
      if (hasScroll && !atBottom) {
        setShowScrollButton(true);
      } else if (atBottom) {
        setShowScrollButton(false);
      }
    };

    // Check on mount and when messages change
    checkScrollButton();
    const timer = setTimeout(checkScrollButton, 100);
    return () => clearTimeout(timer);
  }, [messages, messagesAreaNode, isNearBottom]);

  // Auto-scroll when messages change - ONLY if user hasn't scrolled away
  useEffect(() => {
    // CRITICAL: Do NOT auto-scroll if user scrolled away
    if (userScrolledAway.current) {
      return;
    }

    // Scroll to bottom
    messagesEndRef.current?.scrollIntoView({ behavior: 'auto' });
  }, [messages]);

  // Reset scroll state when chat changes
  useEffect(() => {
    userScrolledAway.current = false;
    wasAtBottom.current = true;
    setShowScrollButton(false);

    // Scroll to bottom of new chat
    const timer = setTimeout(() => {
      messagesEndRef.current?.scrollIntoView({ behavior: 'auto' });
    }, 50);

    return () => clearTimeout(timer);
  }, [chat?.id]);

  // Auto-expand thinking pane during streaming, auto-collapse when done
  useEffect(() => {
    messages.forEach(msg => {
      if (msg.reasoning && msg.role === 'assistant') {
        if (msg.isStreaming) {
          // Auto-expand during streaming
          setExpandedThinkingPanes(prev => {
            if (!prev.has(msg.id)) {
              const newSet = new Set(prev);
              newSet.add(msg.id);
              return newSet;
            }
            return prev;
          });
        } else {
          // Auto-collapse when streaming completes
          setExpandedThinkingPanes(prev => {
            if (prev.has(msg.id)) {
              const newSet = new Set(prev);
              newSet.delete(msg.id);
              return newSet;
            }
            return prev;
          });
        }
      }
    });
  }, [messages]);

  // Auto-scroll thinking content to bottom during streaming (only if near bottom)
  useEffect(() => {
    messages.forEach(msg => {
      if (msg.reasoning && msg.role === 'assistant' && msg.isStreaming) {
        const thinkingContent = document.querySelector(
          `[data-thinking-content="${msg.id}"]`
        ) as HTMLElement;
        if (thinkingContent) {
          // Only auto-scroll if user is near the bottom (within 40px)
          const isNearBottom =
            thinkingContent.scrollHeight -
              thinkingContent.scrollTop -
              thinkingContent.clientHeight <
            40;
          if (isNearBottom) {
            thinkingContent.scrollTop = thinkingContent.scrollHeight;
          }
        }
      }
    });
  }, [messages]);

  // Update greeting (regenerate when starting new chat)
  const updateGreeting = useCallback(() => {
    if (hasUserName()) {
      setDynamicGreeting(generateGreeting());
    } else {
      setDynamicGreeting(generateAnonymousGreeting());
    }
  }, []);

  // Sync username from auth to greeting utils
  const { user } = useAuthStore();

  // Compute user initials for chat bubble
  const userInitials = useMemo(() => {
    if (user?.user_metadata?.display_name) {
      const name = (user.user_metadata.display_name as string).trim();
      // Only process if name has actual content after trimming
      if (name.length > 0) {
        const parts = name.split(/\s+/).filter(p => p.length > 0);
        if (parts.length >= 2 && parts[0].length > 0 && parts[parts.length - 1].length > 0) {
          return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
        }
        if (name.length >= 2) {
          return name.substring(0, 2).toUpperCase();
        }
        return name[0].toUpperCase();
      }
    }
    if (user?.email) {
      const emailName = user.email.split('@')[0];
      if (emailName.length >= 2) {
        return emailName.substring(0, 2).toUpperCase();
      }
      if (emailName.length === 1) {
        return emailName[0].toUpperCase();
      }
    }
    return 'ME';
  }, [user]);

  useEffect(() => {
    // If user is authenticated and has a display_name in metadata, use it
    if (user?.user_metadata?.display_name && !hasUserName()) {
      const displayName = user.user_metadata.display_name as string;
      setUserName(displayName);
      updateGreeting();
    }
  }, [user, updateGreeting]);

  // Check if user has set their name, show modal if not
  useEffect(() => {
    if (!user && !hasUserName()) {
      setShowNameModal(true);
    }
    // Generate initial greeting
    updateGreeting();
  }, [user, updateGreeting]);

  // Handle name submission from modal
  const handleNameSubmit = useCallback(
    (name: string) => {
      setUserName(name);
      setShowNameModal(false);
      updateGreeting(); // Regenerate greeting with name
      // Show privacy modal after name is set if not already configured
      if (chatPrivacyMode === null) {
        setShowPrivacyModal(true);
      }
    },
    [updateGreeting, chatPrivacyMode]
  );

  // Show privacy modal for users who haven't set their privacy preference
  useEffect(() => {
    if (chatPrivacyMode === null && !showNameModal && (user || hasUserName())) {
      setShowPrivacyModal(true);
    }
  }, [chatPrivacyMode, showNameModal, user]);

  // Handle privacy mode selection
  const handlePrivacySubmit = useCallback(
    (mode: 'local' | 'cloud' | null) => {
      setChatPrivacyMode(mode);
      setShowPrivacyModal(false);
    },
    [setChatPrivacyMode]
  );

  // Initialize WebSocket and fetch models on mount
  useEffect(() => {
    // Check if user is authenticated and fetch models accordingly
    // If authenticated: fetch all models with JWT token
    // If not authenticated: fetch only free/anonymous models
    const accessToken = useAuthStore.getState().getAccessToken();
    const requireAuth = !!accessToken;

    console.log('Fetching models with auth:', requireAuth ? 'Authenticated' : 'Anonymous');
    if (accessToken) {
      console.log('JWT Token present, will be sent in Authorization header');

      // Initialize settings from backend (async)
      useSettingsStore
        .getState()
        .initializeFromBackend()
        .then(() => {
          // After settings are loaded, check if cloud sync is enabled
          const privacyMode = useSettingsStore.getState().chatPrivacyMode;
          if (privacyMode === 'cloud') {
            useChatStore.getState().initializeCloudSync();
          }
        });
    }

    // Fetch available models (with or without auth)
    fetchModels(requireAuth);

    // Connect to WebSocket (with token if authenticated)
    websocketService.connect(accessToken);

    // Set up token getter for fresh token retrieval during reconnects
    websocketService.setTokenGetter(() => useAuthStore.getState().getAccessToken());

    // Subscribe to auth token changes to keep WebSocket in sync
    let prevToken = accessToken;
    const unsubscribeAuth = useAuthStore.subscribe(state => {
      if (state.accessToken !== prevToken) {
        prevToken = state.accessToken;
        websocketService.updateAuthToken(state.accessToken);
      }
    });

    // Handle incoming WebSocket messages
    const unsubscribe = websocketService.onMessage((message: ServerMessage) => {
      switch (message.type) {
        case 'connected':
          console.log('✅ WebSocket connected and ready');
          // Note: Conversation ID comes from chat state, not from server
          break;

        case 'conversation_reset':
          console.log('Conversation initialized:', message.conversation_id);
          // Conversation is ready for messages
          break;

        case 'stream_chunk':
          // Batched streaming for performance - reduces UI updates
          {
            // Clear context optimization modal and status updates (streaming has started)
            setContextOptimizing(null);

            const state = useChatStore.getState();
            const currentChat = state.selectedChat();
            const streamingMessageId = state.streamingMessageId;

            // Clear status update once content starts streaming
            if (currentChat && streamingMessageId) {
              const streamingMsg = currentChat.messages.find(m => m.id === streamingMessageId);
              if (streamingMsg?.statusUpdate) {
                state.updateMessage(currentChat.id, streamingMessageId, {
                  statusUpdate: undefined,
                });
              }
            }

            if (currentChat && streamingMessageId && message.content !== undefined) {
              // Buffer the chunk
              chunkBufferRef.current += message.content;

              // Cancel existing timeout and reschedule (prevents race condition)
              if (flushTimeoutRef.current) {
                clearTimeout(flushTimeoutRef.current);
              }

              // Schedule batched update with latest accumulated chunks
              flushTimeoutRef.current = setTimeout(() => {
                const bufferedContent = chunkBufferRef.current;
                chunkBufferRef.current = '';
                flushTimeoutRef.current = null;

                if (bufferedContent) {
                  // Get fresh state for the update
                  const freshState = useChatStore.getState();
                  const freshChat = freshState.selectedChat();
                  const streamingId = freshState.streamingMessageId;

                  if (freshChat && streamingId) {
                    // Use stored streamingMessageId for direct lookup (no find needed)
                    freshState.appendStreamChunk(freshChat.id, streamingId, bufferedContent);
                  }
                }
              }, 50); // Batch updates every 50ms (max 20 updates/sec)
            }
          }
          break;

        case 'reasoning_chunk':
          // Handle reasoning chunks from o1/o3 models
          {
            const state = useChatStore.getState();
            const currentChat = state.selectedChat();
            const currentChatId = currentChat?.id;

            if (currentChatId) {
              const streamingMessage = currentChat.messages
                .slice()
                .reverse()
                .find(m => m.role === 'assistant' && m.isStreaming);

              if (streamingMessage) {
                // Append to reasoning field
                const currentReasoning = streamingMessage.reasoning || '';
                const updatedReasoning = currentReasoning + message.content;
                state.updateMessage(currentChatId, streamingMessage.id, {
                  reasoning: updatedReasoning,
                });
              }
            }
          }
          break;

        case 'tool_call':
          // Handle tool call (can be executing or completed)
          {
            const state = useChatStore.getState();
            const currentChat = state.selectedChat();
            const currentChatId = currentChat?.id;

            if (currentChatId) {
              const streamingMessage = currentChat.messages
                .slice()
                .reverse()
                .find(m => m.role === 'assistant' && m.isStreaming);

              if (streamingMessage) {
                let query: string | undefined;

                // Handle arguments - can be string or object
                if (message.arguments) {
                  try {
                    let args: Record<string, unknown>;

                    if (typeof message.arguments === 'string') {
                      args = JSON.parse(message.arguments);
                    } else if (typeof message.arguments === 'object') {
                      args = message.arguments as Record<string, unknown>;
                    } else {
                      args = {};
                    }

                    // Extract query from various possible argument names
                    query = (args.query ||
                      args.search_query ||
                      args.text ||
                      args.expression ||
                      args.input ||
                      args.prompt) as string | undefined;
                  } catch (e) {
                    console.error('Failed to parse tool arguments:', e);
                  }
                }

                const toolId = message.tool_call_id || `tool-${Date.now()}-${Math.random()}`;

                if (message.status === 'executing') {
                  // Add new tool call
                  const newToolCall = {
                    id: toolId,
                    name: message.tool_name || 'Unknown tool',
                    displayName: message.tool_display_name, // Store display name from backend
                    icon: message.tool_icon, // Store icon from backend
                    status: 'executing' as const,
                    query,
                    timestamp: Date.now(),
                  };

                  state.updateMessage(currentChatId, streamingMessage.id, {
                    toolCalls: [...(streamingMessage.toolCalls || []), newToolCall],
                  });
                } else if (message.status === 'completed' || message.status === 'failed') {
                  // Update existing tool call to completed
                  const existingTool = streamingMessage.toolCalls?.find(
                    t => t.id === toolId || t.name === message.tool_name
                  );

                  if (existingTool) {
                    const updatedToolCalls = streamingMessage.toolCalls!.map(tool =>
                      tool.id === toolId || tool.name === message.tool_name
                        ? {
                            ...tool,
                            status: message.status as 'completed' | 'executing',
                            result: message.result || 'No result provided',
                            query: query || tool.query, // Preserve query from executing state
                            displayName: message.tool_display_name || tool.displayName, // Preserve or update display name
                            icon: message.tool_icon || tool.icon, // Preserve or update icon
                          }
                        : tool
                    );
                    state.updateMessage(currentChatId, streamingMessage.id, {
                      toolCalls: updatedToolCalls,
                    });
                  } else {
                    // Tool call completed without seeing executing state - add it directly
                    const newToolCall = {
                      id: toolId,
                      name: message.tool_name || 'Unknown tool',
                      displayName: message.tool_display_name, // Store display name from backend
                      icon: message.tool_icon, // Store icon from backend
                      status: message.status as 'completed' | 'executing',
                      query,
                      result: message.result || 'No result provided',
                      timestamp: Date.now(),
                    };
                    state.updateMessage(currentChatId, streamingMessage.id, {
                      toolCalls: [...(streamingMessage.toolCalls || []), newToolCall],
                    });
                  }
                }
              }
            }
          }
          break;

        case 'tool_result':
          // Show tool result
          {
            const state = useChatStore.getState();
            const currentChat = state.selectedChat();
            const currentChatId = currentChat?.id;

            if (currentChatId) {
              // Find the message with the tool - first try streaming, then last assistant message
              // (tool_result may arrive after stream_end in some cases)
              let targetMessage = currentChat.messages
                .slice()
                .reverse()
                .find(m => m.role === 'assistant' && m.isStreaming);

              // Fallback: find last assistant message with matching tool
              if (!targetMessage) {
                targetMessage = currentChat.messages
                  .slice()
                  .reverse()
                  .find(
                    m =>
                      m.role === 'assistant' &&
                      m.toolCalls?.some(
                        t => t.name === message.tool_name && t.status === 'executing'
                      )
                  );
              }

              if (targetMessage && targetMessage.toolCalls) {
                // Find the tool being updated
                const completedTool = targetMessage.toolCalls.find(
                  tool => tool.name === message.tool_name && tool.status === 'executing'
                );

                // Update tool status to completed
                const updatedToolCalls = targetMessage.toolCalls.map(tool =>
                  tool.name === message.tool_name && tool.status === 'executing'
                    ? {
                        ...tool,
                        status: 'completed' as const,
                        result: message.result,
                        plots: message.plots, // Add visualization plots
                        dataframes: message.dataframes, // Add tabular artifacts (display_df)
                      }
                    : tool
                );

                // Auto-open artifact pane and persist artifact if tool has plots
                if (message.plots && message.plots.length > 0 && completedTool) {
                  const imageArtifact: Artifact = {
                    id: `tool-images-${completedTool.id}`,
                    type: 'image',
                    title: `${completedTool.displayName || completedTool.name} Results`,
                    content: '',
                    images: message.plots.map(
                      (plot: { data: string; format: string }, plotIdx: number) => ({
                        data: plot.data,
                        format: plot.format,
                        caption: `Visualization ${plotIdx + 1}`,
                      })
                    ),
                    metadata: {
                      toolName: completedTool.name,
                    },
                  };

                  // Persist artifact to message immediately (don't wait for stream_end)
                  const existingArtifacts = targetMessage.artifacts || [];
                  // Avoid duplicates - check if artifact with same id already exists
                  const hasExisting = existingArtifacts.some(a => a.id === imageArtifact.id);
                  const updatedArtifacts = hasExisting
                    ? existingArtifacts
                    : [...existingArtifacts, imageArtifact];

                  state.updateMessage(currentChatId, targetMessage.id, {
                    toolCalls: updatedToolCalls,
                    artifacts: updatedArtifacts,
                  });

                  console.log(`📊 Persisted image artifact from tool ${completedTool.name}`);
                  openArtifacts([imageArtifact]);
                } else {
                  state.updateMessage(currentChatId, targetMessage.id, {
                    toolCalls: updatedToolCalls,
                  });
                }
              }
            }
          }
          break;

        case 'stream_end':
          {
            console.log('🔵 stream_end event received');

            // Flush any remaining buffered chunks immediately
            if (flushTimeoutRef.current) {
              clearTimeout(flushTimeoutRef.current);
              flushTimeoutRef.current = null;
            }
            if (chunkBufferRef.current) {
              const bufferedContent = chunkBufferRef.current;
              chunkBufferRef.current = '';
              const flushState = useChatStore.getState();
              const flushChat = flushState.selectedChat();
              if (flushChat) {
                const streamingMsg = flushChat.messages
                  .slice()
                  .reverse()
                  .find(m => m.role === 'assistant' && m.isStreaming);
                if (streamingMsg) {
                  flushState.appendStreamChunk(flushChat.id, streamingMsg.id, bufferedContent);
                }
              }
            }

            const state = useChatStore.getState();
            const currentChat = state.selectedChat();
            const currentChatId = currentChat?.id;

            console.log('🔵 currentChat:', currentChat);
            console.log('🔵 currentChatId:', currentChatId);
            console.log('🔵 messages length:', currentChat?.messages?.length);

            if (currentChatId && currentChat.messages.length > 0) {
              // Get the last assistant message (which just finished streaming)
              const lastMessage = currentChat.messages[currentChat.messages.length - 1];

              console.log('Stream ended, finalizing message');
              console.log('Last message:', lastMessage);
              console.log('Message role:', lastMessage.role);
              console.log('Message content:', lastMessage.content);
              console.log('Message length:', lastMessage.content.length);

              if (lastMessage && lastMessage.role === 'assistant') {
                // Parse artifacts from message content (HTML, SVG, Mermaid)
                const { artifacts: contentArtifacts, cleanedContent } = extractArtifacts(
                  lastMessage.content
                );

                // Start with existing artifacts (may include image artifacts persisted by tool_result)
                const existingArtifacts = lastMessage.artifacts || [];
                const existingIds = new Set(existingArtifacts.map(a => a.id));

                // Merge content artifacts (avoid duplicates)
                const newArtifacts: Artifact[] = [...existingArtifacts];
                for (const artifact of contentArtifacts) {
                  if (!existingIds.has(artifact.id)) {
                    newArtifacts.push(artifact);
                    existingIds.add(artifact.id);
                  }
                }

                // Also create image artifacts from tool plots (in case tool_result didn't persist them)
                console.log('🔍 Checking toolCalls for plots:', lastMessage.toolCalls);
                if (lastMessage.toolCalls && lastMessage.toolCalls.length > 0) {
                  for (const tool of lastMessage.toolCalls) {
                    console.log(`🔍 Tool ${tool.name}: status=${tool.status}, plots=`, tool.plots);
                    if (tool.plots && tool.plots.length > 0 && tool.status === 'completed') {
                      const artifactId = `tool-images-${tool.id}`;
                      // Only add if not already present
                      if (!existingIds.has(artifactId)) {
                        const imageArtifact: Artifact = {
                          id: artifactId,
                          type: 'image',
                          title: `${tool.displayName || tool.name} Results`,
                          content: '',
                          images: tool.plots.map((plot, plotIdx) => ({
                            data: plot.data,
                            format: plot.format,
                            caption: `Visualization ${plotIdx + 1}`,
                          })),
                          metadata: {
                            toolName: tool.name,
                          },
                        };
                        newArtifacts.push(imageArtifact);
                        existingIds.add(artifactId);
                        console.log(
                          `📊 Created image artifact from tool ${tool.name} with ${tool.plots.length} plot(s)`
                        );
                      } else {
                        console.log(`📊 Image artifact ${artifactId} already exists, skipping`);
                      }
                    }
                  }
                }

                console.log('Final artifacts:', newArtifacts);
                console.log('Cleaned content length:', cleanedContent.length);

                // Always update the message (to clean content even if no artifacts)
                const hasContentChange = cleanedContent !== lastMessage.content;
                const hasNewArtifacts = newArtifacts.length > existingArtifacts.length;

                if (hasContentChange || hasNewArtifacts || newArtifacts.length > 0) {
                  console.log(`📦 Finalizing with ${newArtifacts.length} artifact(s)`);

                  state.updateMessage(currentChatId, lastMessage.id, {
                    content: cleanedContent,
                    artifacts: newArtifacts.length > 0 ? newArtifacts : undefined,
                  });

                  // Open artifact pane if there are artifacts
                  if (newArtifacts.length > 0) {
                    openArtifacts(newArtifacts);
                  }
                } else {
                  console.log('❌ No artifacts found in message');
                }

                // Attach per-turn usage (tokens / cache / duration / cost)
                // so AssistantMessage can render the cost chip beneath the
                // finished message. message.tokens may be absent for tool
                // continuations or providers that don't emit usage.
                if (message.tokens) {
                  state.updateMessage(currentChatId, lastMessage.id, {
                    usage: message.tokens,
                  });
                }

                // Finalize streaming if still marked as streaming
                if (lastMessage.isStreaming) {
                  state.finalizeStreamingMessage(currentChatId, lastMessage.id);
                }
              }
            }
          }
          setLoading(false);
          // Focus back to command center input after stream completes
          setTimeout(() => {
            commandCenterRef.current?.focus();
          }, 100);
          break;

        case 'conversation_title':
          // Handle auto-generated title with animation
          {
            const state = useChatStore.getState();
            const currentChat = state.selectedChat();
            const newTitle = message.title || '';

            if (currentChat && currentChat.id === message.conversation_id) {
              console.log('📝 Title generated:', newTitle);

              // Start animation
              setIsAnimatingTitle(true);
              const oldTitle = currentChat.title;

              // Delete animation (backspace effect)
              let currentLength = oldTitle.length;
              const deleteInterval = setInterval(() => {
                if (currentLength > 0) {
                  setDisplayTitle(oldTitle.substring(0, currentLength));
                  currentLength--;
                } else {
                  clearInterval(deleteInterval);

                  // Type new title
                  let typeIndex = 0;
                  const typeInterval = setInterval(() => {
                    if (typeIndex <= newTitle.length) {
                      setDisplayTitle(newTitle.substring(0, typeIndex));
                      typeIndex++;
                    } else {
                      clearInterval(typeInterval);
                      // Update actual title in store
                      state.updateChatTitle(currentChat.id, newTitle);
                      setIsAnimatingTitle(false);
                    }
                  }, 50); // Type speed
                }
              }, 30); // Delete speed
            }
          }
          break;

        case 'interactive_prompt':
          {
            console.log('📋 Interactive prompt received:', message.prompt_id);

            const state = useChatStore.getState();
            const currentChat = state.selectedChat();

            if (!currentChat) {
              console.warn('No active chat for interactive prompt');
              break;
            }

            // Create active prompt from message
            const activePrompt: ActivePrompt = {
              promptId: message.prompt_id,
              conversationId: message.conversation_id,
              title: message.title,
              description: message.description,
              questions: message.questions,
              allowSkip: message.allow_skip ?? true,
              timestamp: Date.now(),
            };

            // Set active prompt (this will show modal)
            state.setActivePrompt(activePrompt);
          }
          break;

        case 'prompt_timeout':
          {
            console.log('⏱️ Prompt timeout:', message.prompt_id);

            const state = useChatStore.getState();
            if (state.activePrompt?.promptId === message.prompt_id) {
              state.clearActivePrompt();
              setError(message.message || 'Prompt timed out');
            }
          }
          break;

        case 'prompt_validation_error':
          {
            console.log('❌ Prompt validation error:', message.prompt_id);

            const state = useChatStore.getState();
            if (state.activePrompt?.promptId === message.prompt_id) {
              // Show validation errors (could be enhanced to pass errors to modal)
              const errorMessages = Object.values(message.errors).join(', ');
              setError(`Validation error: ${errorMessages}`);
            }
          }
          break;

        case 'status_update':
          // Show pre-processing status (skill routing, tool selection, generating)
          {
            const state = useChatStore.getState();
            const currentChat = state.selectedChat();
            const streamingId = state.streamingMessageId;
            if (currentChat && streamingId) {
              const statusMap: Record<string, string> = {
                routing_skill: 'Finding the right skill...',
                skill_matched: message.arguments?.skill_name
                  ? `Using ${message.arguments.skill_name}`
                  : 'Skill matched',
                selecting_tools: 'Selecting tools...',
                predicting_tools: 'Analyzing which tools to use...',
                tools_ready: message.arguments?.count
                  ? `${message.arguments.count} tools ready`
                  : 'Tools ready',
                generating: 'Generating response...',
              };
              const statusText = statusMap[message.status] || message.status;
              state.updateMessage(currentChat.id, streamingId, {
                statusUpdate: statusText,
              });
            }
          }
          break;

        case 'error':
          {
            setError(message.message || 'An error occurred');
            setLoading(false);
            // Focus back to command center input after error
            setTimeout(() => {
              commandCenterRef.current?.focus();
            }, 100);

            // Finalize any streaming message when error occurs
            const state = useChatStore.getState();
            const currentChat = state.selectedChat();
            const currentChatId = currentChat?.id;

            if (currentChatId) {
              const streamingMessage = currentChat.messages
                .slice()
                .reverse()
                .find(m => m.role === 'assistant' && m.isStreaming);

              if (streamingMessage) {
                console.log('Error occurred, finalizing streaming message');
                state.finalizeStreamingMessage(currentChatId, streamingMessage.id);
              }
            }
          }
          break;

        case 'files_expired':
          console.warn('⚠️ [FILES] Some files have expired:', message.content);
          // Show a notification or warning to the user
          // The backend will still process the message, just without the expired files
          setError(
            `⚠ Warning: Some attached files have expired and are no longer available. Continuing without them.`
          );
          break;

        case 'context_optimizing':
          // Handle context optimization status updates
          {
            console.log('📊 [CONTEXT] Optimization status:', message.status, message.progress);
            if (message.status === 'completed') {
              // Hide immediately on completion - streaming is about to start
              setContextOptimizing(null);
            } else {
              setContextOptimizing({
                active: true,
                progress: message.progress,
                message: message.content,
              });
            }
          }
          break;

        case 'stream_resume':
          // Handle resumed stream after reconnection
          {
            console.log(
              '📦 [RESUME] Received buffered chunks:',
              message.content?.length || 0,
              'bytes'
            );
            const state = useChatStore.getState();
            const currentChat = state.selectedChat();

            if (currentChat && message.content) {
              const streamingMsg = currentChat.messages
                .slice()
                .reverse()
                .find(m => m.role === 'assistant' && m.isStreaming);

              if (streamingMsg) {
                // Append all buffered content at once
                state.appendStreamChunk(currentChat.id, streamingMsg.id, message.content);
                console.log('📦 [RESUME] Applied buffered chunks to message:', streamingMsg.id);

                // If stream is complete, finalize the message
                if (message.is_complete) {
                  console.log('📦 [RESUME] Stream was complete, finalizing message');
                  state.finalizeStreamingMessage(currentChat.id, streamingMsg.id);
                  setLoading(false);
                  // Focus back to command center input after stream completes
                  setTimeout(() => {
                    commandCenterRef.current?.focus();
                  }, 100);
                }
              }
            }
          }
          break;

        case 'stream_missed':
          // Handle missed stream (buffer expired)
          {
            console.warn('⚠️ [RESUME] Stream missed:', message.reason);
            const state = useChatStore.getState();
            const currentChat = state.selectedChat();

            if (currentChat) {
              const streamingMsg = currentChat.messages
                .slice()
                .reverse()
                .find(m => m.role === 'assistant' && m.isStreaming);

              if (streamingMsg) {
                // Finalize the message with what we have
                state.finalizeStreamingMessage(currentChat.id, streamingMsg.id);

                // Show error to user
                setError(
                  'Connection was lost during response generation. The message may be incomplete.'
                );
              }
            }
            setLoading(false);
            // Focus back to command center input after stream missed
            setTimeout(() => {
              commandCenterRef.current?.focus();
            }, 100);
          }
          break;

        case 'limit_exceeded':
          setLoading(false);
          break;

        default:
          console.log('Unhandled message type:', (message as ServerMessage).type);
      }
    });

    // Cleanup on unmount
    return () => {
      unsubscribe();
      unsubscribeAuth();
      websocketService.disconnect();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Handle WebSocket messages for current chat
  useEffect(() => {
    // This effect ensures we have the latest chat and messages for WebSocket handlers
    // The actual message handling is in the first useEffect
  }, [chat?.id, messages]);

  // Navigation items configuration
  const navItems: NavItem[] = [
    {
      id: 'chats',
      label: 'Chats',
      icon: MessageSquare,
      // Active when: viewing chat history list OR in an active chat (not new chat)
      isActive: (activeNav === 'chats' && !isNewChat) || chat !== null,
      onClick: () => {
        setActiveNav('chats');
        showHistory();
      },
    },
    {
      id: 'artifacts',
      label: 'Artifacts',
      icon: Box,
      // Only active when explicitly viewing artifacts gallery (not in a chat)
      isActive: activeNav === 'artifacts' && !chat,
      onClick: () => {
        // Navigate to /artifacts
        navigate('/artifacts');
        setActiveNav('artifacts');
        showHistory();
      },
    },
    {
      id: 'projects',
      label: 'Projects',
      icon: FolderKanban,
      // Active when at least one project is attached to the current
      // chat — gives the user a visible cue that knowledge is in play.
      isActive: knowledgeProjectIds.length > 0,
      // Click navigates to Nexus → projects, where the user can
      // create projects + upload knowledge files. The in-chat picker
      // (chip row above the input) handles per-chat attachment.
      onClick: () => navigate('/nexus'),
      tooltip:
        knowledgeProjectIds.length > 0
          ? `${knowledgeProjectIds.length} project knowledge base${knowledgeProjectIds.length > 1 ? 's' : ''} attached`
          : 'Manage your project knowledge bases',
    },
    {
      id: 'code',
      label: 'Code',
      icon: Code2,
      isActive: activeNav === 'code',
      onClick: () => setActiveNav('code'),
      disabled: true,
      tooltip: 'Coming Soon',
    },
  ];

  // Chat action handlers
  const handleStarChat = useCallback(
    (chatId: string) => {
      toggleStarChat(chatId);
    },
    [toggleStarChat]
  );

  const handleRenameChat = useCallback((chatId: string) => {
    const chatToRename = useChatStore.getState().chats.find(c => c.id === chatId);
    if (chatToRename) {
      setChatToRename({ id: chatId, title: chatToRename.title });
      setShowRenameDialog(true);
    }
  }, []);

  const handleDeleteChat = useCallback((chatId: string) => {
    setChatToDelete(chatId);
    setShowDeleteDialog(true);
  }, []);

  const confirmDelete = useCallback(async () => {
    if (chatToDelete) {
      await deleteChat(chatToDelete);
      setChatToDelete(null);
    }
  }, [chatToDelete, deleteChat]);

  const confirmRename = useCallback(
    (newTitle: string) => {
      if (chatToRename) {
        updateTitle(chatToRename.id, newTitle);
        setChatToRename(null);
      }
    },
    [chatToRename, updateTitle]
  );

  // Selection mode handlers
  const toggleSelectionMode = useCallback(() => {
    setIsSelectionMode(prev => !prev);
    // Clear selections when exiting selection mode
    if (isSelectionMode) {
      setSelectedChatIds(new Set());
    }
  }, [isSelectionMode]);

  const toggleChatSelection = useCallback((chatId: string) => {
    setSelectedChatIds(prev => {
      const newSet = new Set(prev);
      if (newSet.has(chatId)) {
        newSet.delete(chatId);
      } else {
        newSet.add(chatId);
      }
      return newSet;
    });
  }, []);

  const deleteSelectedChats = useCallback(async () => {
    if (selectedChatIds.size === 0) return;

    await Promise.all(Array.from(selectedChatIds).map(chatId => deleteChat(chatId)));

    setSelectedChatIds(new Set());
    setIsSelectionMode(false);
    setShowDeleteDialog(false);
  }, [selectedChatIds, deleteChat]);

  const confirmBulkDelete = useCallback(() => {
    if (selectedChatIds.size === 0) return;
    setShowDeleteDialog(true);
  }, [selectedChatIds.size]);

  // Recent chats configuration for sidebar
  const recentChats: RecentChat[] = recentChatsForSidebar.map(chat => ({
    id: chat.id,
    title: chat.title,
    onClick: () => handleChatSelect(chat.id),
    isStarred: chat.isStarred,
    onStar: handleStarChat,
    onRename: handleRenameChat,
    onDelete: handleDeleteChat,
  }));

  const handleNewChat = useCallback(() => {
    // Navigate to /chat for new chat
    navigate('/chat');

    startNewChat();
    setActiveNav('chats'); // Ensure Chats nav is active
    closeArtifactPane(); // Close artifact pane when starting new chat
    updateGreeting(); // Generate new greeting when starting new chat
  }, [startNewChat, updateGreeting, setActiveNav, closeArtifactPane, navigate]);

  const handleChatSelect = useCallback(
    (chatId: string) => {
      // Navigate to the chat URL - effect handles syncing chat selection
      navigate(`/chat/${chatId}`);
      setActiveNav('chats'); // Ensure Chats nav is active until effect runs
      closeArtifactPane(); // Close artifact pane when switching chats
      setIsEditingTitle(false);

      // Reset conversation on server when switching chats
      if (websocketService.isConnected() && selectedModelId) {
        const selectedChat = useChatStore.getState().chats.find(c => c.id === chatId);
        if (selectedChat) {
          websocketService.startNewConversation(
            selectedChat.id,
            selectedModelId,
            selectedChat.systemInstructions
          );
        }
      }
    },
    [selectedModelId, setActiveNav, closeArtifactPane, navigate]
  );

  // Version options for retry
  interface SendMessageOptions {
    versionGroupId?: string;
    versionNumber?: number;
    retryType?: RetryType;
    skipUserMessage?: boolean; // For retries - don't create new user message
  }

  const handleSendMessage = useCallback(
    async (
      text: string,
      isDeepThinkingOrOptions: boolean | SendMessageOptions = false,
      files?: File[],
      systemInstruction?: string,
      tempApiKey?: string
    ): Promise<boolean> => {
      // Handle both old signature (boolean) and new signature (options object)
      const isDeepThinking =
        typeof isDeepThinkingOrOptions === 'boolean' ? isDeepThinkingOrOptions : false;
      const versionOptions =
        typeof isDeepThinkingOrOptions === 'object' ? isDeepThinkingOrOptions : undefined;
      // Start with the original text
      let messageToSend = text;

      // Slash-command parsing: /web /code /sql /research /data. Strips the
      // command prefix from the visible message and prepends the matching
      // system instruction so the model knows to apply that mode this turn.
      // Unknown slashes fall through unchanged — the model may still see
      // them as natural text (e.g. "/path/to/file" in a code snippet).
      const slash = parseSlashCommand(text);
      if (slash) {
        // If the user only typed the command with nothing after, we still
        // proceed — gives a "what would you like to do in <mode>?" feel.
        text = slash.rest || `(${slash.hint})`;
        const userPrefix = systemInstruction ? `\n\n${systemInstruction}` : '';
        systemInstruction = slash.systemPrefix + userPrefix;
      }

      // Validate message (validate original text, not the prefixed version)
      // Allow empty text if files are present
      if (!text.trim() && (!files || files.length === 0)) {
        setError('Please enter a message or attach a file');
        return false;
      }

      if (text.trim()) {
        const validation = validateMessage(text);
        if (!validation.isValid) {
          setError(validation.error || 'Invalid message');
          return false;
        }
      }

      // Check if model is selected
      if (!selectedModelId) {
        setError('Please select a model first');
        return false;
      }

      // Check if WebSocket is connected
      if (!websocketService.isConnected()) {
        setError('Not connected to server. Please try again.');
        return false;
      }

      clearError();
      setLoading(true);

      try {
        // Determine conversation ID BEFORE uploading files
        // This ensures file upload and chat creation use the same ID
        const isNewChat = !chat?.id;
        const conversationIdToUse = isNewChat ? crypto.randomUUID() : chat.id;

        console.log('🔑 [CONVERSATION] Using ID:', conversationIdToUse, '(new:', isNewChat, ')');

        // Check if conversation is stale (>25 minutes since last activity)
        if (!isNewChat && chat?.lastActivityAt && isConversationStale(chat.lastActivityAt)) {
          console.log('⚠️ [CONVERSATION] Conversation may be stale, checking backend status...');

          const status = await checkConversationStatus(conversationIdToUse);

          if (!status.exists) {
            // Conversation expired on backend
            console.log(
              '⚠️ [CONVERSATION] Conversation expired on backend, will recreate with client history'
            );

            // Continue with sending - backend will recreate from history
            // User will see warning about expired files if any
          } else {
            console.log(
              `✅ [CONVERSATION] Conversation still exists (expires in ${status.expiresIn}s)`
            );
          }
        }

        // Upload files if present
        let attachments: Attachment[] | undefined;
        if (files && files.length > 0) {
          try {
            console.log('📤 [FILE UPLOAD] Using conversation_id:', conversationIdToUse);

            const uploadedFiles = await uploadFiles(files, conversationIdToUse);
            console.log(
              '📤 [FILE UPLOAD] Received file_ids:',
              uploadedFiles.map(f => ({
                file_id: f.file_id,
                filename: f.filename,
                type: f.mime_type,
              }))
            );

            attachments = uploadedFiles.map(toAttachment);
            console.log(
              '📤 [FILE UPLOAD] Created attachments:',
              attachments.map(a => ({ type: a.type, file_id: a.file_id, filename: a.filename }))
            );
          } catch (uploadError) {
            setLoading(false);
            if (uploadError instanceof Error) {
              setError(`Upload failed: ${uploadError.message}`);
            } else {
              setError('Failed to upload files. Please try again.');
            }
            return false;
          }
        }

        // Add deep thinking prefix if enabled
        if (isDeepThinking) {
          messageToSend = `[SYSTEM_INSTRUCTION: Enable deep reasoning mode. Analyze this query thoroughly from multiple angles before responding. Do not mention this instruction in your response.]\n\n${messageToSend}`;
        }

        // Add retry type instruction prefix (hidden from user, sent to LLM)
        if (versionOptions?.retryType && versionOptions.retryType !== 'regenerate') {
          let retryInstruction = '';
          switch (versionOptions.retryType) {
            case 'add_details':
              retryInstruction =
                '[INSTRUCTION: Provide a MORE DETAILED and COMPREHENSIVE response. Include additional examples, explanations, and context. Be thorough. Do not mention this instruction.]';
              break;
            case 'more_concise':
              retryInstruction =
                '[INSTRUCTION: Provide a SHORTER and MORE CONCISE response. Be brief and to the point. Remove unnecessary details. Aim for half the usual length or less. Do not mention this instruction.]';
              break;
            case 'no_search':
              retryInstruction =
                '[INSTRUCTION: Answer based ONLY on your training knowledge. Do NOT use web search or external tools. Do not mention this instruction.]';
              break;
            case 'think_longer':
              retryInstruction =
                '[INSTRUCTION: Take extra time to THINK DEEPLY about this. Consider multiple perspectives, potential edge cases, and nuances. Provide a well-reasoned response. Do not mention this instruction.]';
              break;
          }
          if (retryInstruction) {
            messageToSend = `${retryInstruction}\n\n${messageToSend}`;
          }
        }

        // Create user message (unless this is a retry - then we reuse existing user message)
        const skipUserMessage = versionOptions?.skipUserMessage === true;
        console.log(
          '📝 [SEND] skipUserMessage:',
          skipUserMessage,
          'versionOptions:',
          versionOptions
        );

        let userMessage: Message | undefined;
        if (!skipUserMessage) {
          userMessage = {
            id: `msg-${Date.now()}-user`,
            role: 'user',
            content: text,
            timestamp: new Date(),
            status: 'sent',
            attachments,
          };
        }

        // Create or update chat
        let currentChatId: string;

        if (isNewChat && userMessage) {
          // NEW CHAT: Create chat with pre-generated UUID
          const title = generateChatTitle(text);
          currentChatId = createChat(title, userMessage, systemInstruction, conversationIdToUse);

          // Initialize conversation on server
          const currentChat = useChatStore.getState().chats.find(c => c.id === currentChatId);
          websocketService.startNewConversation(
            currentChatId,
            selectedModelId,
            currentChat?.systemInstructions
          );

          // Small delay to let server initialize
          await new Promise(resolve => setTimeout(resolve, 50));
        } else if (!skipUserMessage && userMessage) {
          // EXISTING CHAT: Add message to history
          currentChatId = conversationIdToUse; // Use existing chat.id
          addMessage(currentChatId, userMessage);
        } else {
          // RETRY: Don't add user message, just use existing chat
          currentChatId = conversationIdToUse;
        }

        // Create placeholder assistant message for streaming
        const assistantMessage: Message = {
          id: `msg-${Date.now()}-assistant`,
          role: 'assistant',
          content: '',
          timestamp: new Date(),
          isStreaming: true,
          // Version metadata (if this is a retry)
          versionGroupId: versionOptions?.versionGroupId,
          versionNumber: versionOptions?.versionNumber || 1,
          retryType: versionOptions?.retryType,
        };

        // Reset thinking verb for new response
        resetThinkingVerb();

        addMessage(currentChatId, assistantMessage);
        startStreaming(currentChatId, assistantMessage.id);

        // Prepare conversation history (exclude current user message and placeholder, and hidden versions)
        const currentChat = useChatStore.getState().chats.find(c => c.id === currentChatId);
        const historyMessages =
          currentChat?.messages
            .filter(
              m =>
                m.id !== assistantMessage.id &&
                (!userMessage || m.id !== userMessage.id) &&
                !m.isHidden // Exclude hidden versions from context
            )
            .slice(-20) // Last 20 messages for context
            .map(m => ({
              role: m.role,
              content: m.content,
            })) || [];

        // Get settings for system instructions and custom provider
        const settingsState = useSettingsStore.getState();
        // Note: Formatting guidelines are now appended by the backend to all system prompts
        const effectiveSystemInstructions =
          currentChat?.systemInstructions || settingsState.defaultSystemInstructions || undefined;

        // Note: Retry type instructions are now injected into the message itself (hidden from user)
        // This is more effective than appending to system instructions

        // Determine if we're using a custom provider model
        // Format: custom:providerId:modelId
        const isCustomModel = selectedModelId?.startsWith('custom:');
        let customConfig: { base_url: string; api_key: string; model: string } | undefined;
        let modelIdToUse = selectedModelId;

        if (isCustomModel && selectedModelId) {
          // Parse the custom model ID: custom:providerId:modelId
          const parts = selectedModelId.split(':');
          if (parts.length >= 3) {
            const providerId = parts[1];
            const modelName = parts.slice(2).join(':'); // Handle model names with colons

            // Find the provider
            const provider = settingsState.customProviders.find(p => p.id === providerId);
            if (provider && provider.enabled) {
              const { decryptApiKey } = await import('@/store/useSettingsStore');

              // Check for API key (temp, persistent, or session)
              let apiKey = tempApiKey;

              if (!apiKey) {
                if (provider.apiKey) {
                  apiKey = decryptApiKey(provider.apiKey);
                } else {
                  // Check session keys
                  apiKey = settingsState.sessionApiKeys[provider.id];
                }
              }

              // If still no key, prompt user
              if (!apiKey) {
                console.log('🔒 [CUSTOM PROVIDER] No API key found, prompting user');
                setPendingProviderId(provider.id);
                setPendingMessage({ text, isDeepThinking, files, systemInstruction });
                setShowApiKeyModal(true);
                setLoading(false);
                return false;
              }

              customConfig = {
                base_url: provider.baseUrl,
                api_key: apiKey,
                model: modelName,
              };
              modelIdToUse = modelName;
              console.log('🔧 [CUSTOM PROVIDER] Using custom config:', {
                providerId,
                providerName: provider.name,
                baseUrl: provider.baseUrl,
                model: modelName,
              });
            } else {
              console.error('❌ [CUSTOM PROVIDER] Provider not found or disabled:', providerId);
              setError('Custom provider not found or disabled. Please check your settings.');
              setLoading(false);
              return false;
            }
          } else {
            console.error('❌ [CUSTOM PROVIDER] Invalid model ID format:', selectedModelId);
            setError('Invalid custom model ID format.');
            setLoading(false);
            return false;
          }
        }

        // Send message with history (backend will handle PDF content fetching using file_id)
        console.log('💬 [WEBSOCKET SEND] conversation_id:', currentChatId);
        console.log(
          '💬 [WEBSOCKET SEND] attachments:',
          attachments?.map(a => ({ type: a.type, file_id: a.file_id, filename: a.filename }))
        );
        console.log('💬 [WEBSOCKET SEND] customConfig:', customConfig ? 'enabled' : 'disabled');
        console.log('💬 [WEBSOCKET SEND] modelId:', modelIdToUse);

        // Track streaming conversation for resume capability
        websocketService.setStreamingConversation(currentChatId);

        // Promote the new-chat draft selection to this chat's ID so
        // the picker selection the user made BEFORE the chat was
        // created carries through. Idempotent and safe to call every
        // turn (the store no-ops when there's no draft to promote).
        knowledgeStorePromote(currentChatId);

        // Re-read so we pick up the just-promoted IDs in case this
        // was the chat's first send.
        const knowledgeIdsForTurn = knowledgeStoreGet(currentChatId);

        websocketService.sendMessageWithHistory(
          messageToSend,
          modelIdToUse,
          currentChatId,
          historyMessages,
          effectiveSystemInstructions,
          attachments,
          customConfig,
          undefined,
          undefined,
          knowledgeIdsForTurn
        );

        // Force scroll to bottom when user sends a message
        setTimeout(() => scrollToBottom(), 100);

        // Message sent successfully
        return true;
      } catch (err) {
        setError('An unexpected error occurred. Please try again.');
        console.error('Error sending message:', err);
        setLoading(false);
        return false;
      }
    },
    // Note: chat.lastActivityAt intentionally omitted to prevent unnecessary re-renders
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [
      chat?.id,
      selectedModelId,
      createChat,
      addMessage,
      startStreaming,
      setError,
      clearError,
      setLoading,
      scrollToBottom,
    ]
  );

  const handleUpdateTitle = useCallback(() => {
    if (chat && editTitleValue.trim()) {
      updateTitle(chat.id, editTitleValue.trim());
    }
    setIsEditingTitle(false);
  }, [chat, editTitleValue, updateTitle]);

  const handleCopyMessage = useCallback(
    (content: string, messageId: string) => {
      copyAsFormattedText(content)
        .then(() => {
          setCopiedMessageId(messageId);
          setTimeout(() => setCopiedMessageId(null), 2000);
          console.log('Copied to clipboard (formatted for email)');
        })
        .catch(err => {
          console.error('Failed to copy:', err);
          setError('Failed to copy to clipboard');
        });
    },
    [setError]
  );

  const handleToggleThinking = useCallback(
    (messageId: string, opts?: { scrollTo?: boolean }) => {
      toggleThinkingPane(messageId, opts);
    },
    [toggleThinkingPane]
  );

  const handleRetry = useCallback(
    (messageIdOrIndex: number | string, retryType: RetryType = 'regenerate') => {
      if (!chat) return;

      // Support both message ID (string) and index (number) for backwards compatibility
      let assistantMessage: Message | undefined;
      let userMessage: Message | undefined;

      if (typeof messageIdOrIndex === 'string') {
        // Find by message ID
        const msgIndex = messages.findIndex(m => m.id === messageIdOrIndex);
        if (msgIndex === -1) return;
        assistantMessage = messages[msgIndex];
        // Find the user message before this assistant message (skip hidden messages)
        for (let i = msgIndex - 1; i >= 0; i--) {
          if (messages[i].role === 'user') {
            userMessage = messages[i];
            break;
          }
        }
      } else {
        // Legacy: find by index in filtered list - need to map to actual message
        const visibleMessages = messages.filter(m => !m.isHidden);
        assistantMessage = visibleMessages[messageIdOrIndex];
        if (assistantMessage) {
          // Find user message before this one
          const actualIndex = messages.findIndex(m => m.id === assistantMessage!.id);
          for (let i = actualIndex - 1; i >= 0; i--) {
            if (messages[i].role === 'user') {
              userMessage = messages[i];
              break;
            }
          }
        }
      }

      if (!assistantMessage || !userMessage) return;

      // Generate or reuse version group ID
      const isNewVersionGroup = !assistantMessage.versionGroupId;
      const versionGroupId = assistantMessage.versionGroupId || crypto.randomUUID();

      // Count existing versions in this group
      // If this is a new version group, the current message will become version 1
      // so the new message will be version 2
      let newVersionNumber: number;
      if (isNewVersionGroup) {
        newVersionNumber = 2; // First retry always creates version 2
      } else {
        const existingVersions = messages.filter(
          m => m.versionGroupId === versionGroupId && m.role === 'assistant'
        );
        newVersionNumber = existingVersions.length + 1;
      }

      // Hide the current version
      updateMessage(chat.id, assistantMessage.id, {
        isHidden: true,
        versionGroupId, // Ensure it has the group ID
        versionNumber: assistantMessage.versionNumber || 1,
      });

      // Send new message with version metadata (skip creating new user message)
      // Note: Retry type instructions are injected as hidden prefixes in handleSendMessage
      handleSendMessage(userMessage.content, {
        versionGroupId,
        versionNumber: newVersionNumber,
        retryType,
        skipUserMessage: true, // Don't duplicate user message for retries
      });
    },
    [chat, messages, handleSendMessage, updateMessage]
  );

  // Edit a previous user message + regenerate from there. Forks the
  // conversation by: (a) updating the user message content in place, (b)
  // hiding every message that came after it in the visible thread (kept on
  // disk so version navigation could surface them later — for now they're
  // just out of view), and (c) re-running the assistant from that point.
  const handleEditAndRegenerate = useCallback(
    (messageId: string, newContent: string) => {
      if (!chat) return;
      const idx = messages.findIndex(m => m.id === messageId);
      if (idx === -1) return;
      const target = messages[idx];
      if (target.role !== 'user') return;

      // Rewrite the user message in place.
      updateMessage(chat.id, messageId, { content: newContent });

      // Hide everything that came after it so the chat visually rewinds
      // to the edited turn. We keep the records on the store (isHidden=true)
      // rather than deleting — opens the door to version navigation across
      // edits later, and is safer if the user fat-fingers the edit.
      for (let i = idx + 1; i < messages.length; i++) {
        if (!messages[i].isHidden) {
          updateMessage(chat.id, messages[i].id, { isHidden: true });
        }
      }

      // Fire a fresh send. skipUserMessage keeps handleSendMessage from
      // appending a duplicate — we already have the edited message in the
      // thread. The assistant regenerates against the new content.
      handleSendMessage(newContent, {
        skipUserMessage: true,
      });
    },
    [chat, messages, updateMessage, handleSendMessage]
  );

  // Handle version navigation
  const handleVersionNavigate = useCallback(
    (messageId: string, direction: 'prev' | 'next') => {
      if (!chat) return;

      const message = messages.find(m => m.id === messageId);
      if (!message?.versionGroupId) return;

      // Get all versions in this group (including hidden ones)
      const versions = messages
        .filter(m => m.versionGroupId === message.versionGroupId && m.role === 'assistant')
        .sort((a, b) => (a.versionNumber || 1) - (b.versionNumber || 1));

      const currentIndex = versions.findIndex(v => v.id === messageId);
      const targetIndex = direction === 'prev' ? currentIndex - 1 : currentIndex + 1;

      if (targetIndex < 0 || targetIndex >= versions.length) return;

      const targetMessage = versions[targetIndex];

      // Hide current, show target
      updateMessage(chat.id, message.id, { isHidden: true });
      updateMessage(chat.id, targetMessage.id, { isHidden: false });
    },
    [chat, messages, updateMessage]
  );

  const handleStopGeneration = useCallback(() => {
    websocketService.stopGeneration();
    setLoading(false);

    // Finalize the streaming message to remove the spinner
    if (chat) {
      const streamingMessage = chat.messages
        .slice()
        .reverse()
        .find(m => m.role === 'assistant' && m.isStreaming);

      if (streamingMessage) {
        const state = useChatStore.getState();
        state.finalizeStreamingMessage(chat.id, streamingMessage.id);
      }
    }
  }, [setLoading, chat]);

  const handleApiKeySubmit = useCallback(
    (apiKey: string, rememberSession: boolean) => {
      if (pendingProviderId && pendingMessage) {
        if (rememberSession) {
          useSettingsStore.getState().setSessionApiKey(pendingProviderId, apiKey);
        }

        // Call handleSendMessage with the temp key
        handleSendMessage(
          pendingMessage.text,
          pendingMessage.isDeepThinking,
          pendingMessage.files,
          pendingMessage.systemInstruction,
          apiKey
        );

        setShowApiKeyModal(false);
        setPendingMessage(null);
        setPendingProviderId(null);
      }
    },
    [pendingProviderId, pendingMessage, handleSendMessage]
  );

  // Update edit title value when chat changes
  useEffect(() => {
    if (chat) {
      setEditTitleValue(chat.title);
      if (!isAnimatingTitle) {
        setDisplayTitle(chat.title);
      }
    }
  }, [chat, isAnimatingTitle]);

  return (
    <div className={styles.chatContainer}>
      <Snowfall fadeAfter={5000} />
      {/* Sidebar */}
      <Sidebar
        brandName="Chat"
        navItems={navItems}
        recentChats={recentChats}
        onNewChat={handleNewChat}
        isOpen={isSidebarOpen}
        onOpenChange={setIsSidebarOpen}
        footerLinks={CHAT_FOOTER_LINKS}
      />

      {/* Main Window */}
      <main className={styles.mainContent} role="main">
        {/* Floating Mobile Menu Button - shows on all views when sidebar is closed */}
        {isMobile && !isSidebarOpen && (
          <button
            className={styles.floatingMenuButton}
            onClick={() => setIsSidebarOpen(true)}
            aria-label="Open menu"
          >
            <Menu size={20} />
          </button>
        )}

        {/* API Key Modal */}
        <ApiKeyModal
          isOpen={showApiKeyModal}
          onClose={() => {
            setShowApiKeyModal(false);
            setPendingMessage(null);
            setPendingProviderId(null);
          }}
          onSubmit={handleApiKeySubmit}
          providerName={
            useSettingsStore.getState().customProviders.find(p => p.id === pendingProviderId)
              ?.name || 'Custom Provider'
          }
        />

        {/* Error Banner */}
        {error && (
          <div className={styles.errorMessage} role="alert" aria-live="assertive">
            <AlertCircle size={16} aria-hidden="true" />
            <span className={styles.errorText}>{error}</span>
            <button className={styles.closeButton} onClick={clearError} aria-label="Dismiss error">
              <X size={16} />
            </button>
          </div>
        )}

        {/* Context Optimization Banner */}
        {contextOptimizing && (
          <div className={styles.contextOptimizingBanner} role="status" aria-live="polite">
            <CustomSpinner size={14} />
            <span className={styles.contextOptimizingText}>{contextOptimizing.message}</span>
            <div className={styles.contextOptimizingProgress}>
              <div
                className={styles.contextOptimizingProgressBar}
                style={{ width: `${contextOptimizing.progress}%` }}
              />
            </div>
          </div>
        )}

        {isLoadingUrlChat ? (
          // Loading state when fetching chat from URL (shared link)
          <div className={styles.urlChatLoading}>
            <CustomSpinner size={48} />
            <p className={styles.urlChatLoadingText}>Loading conversation...</p>
          </div>
        ) : activeNav === 'artifacts' && !chat ? (
          // Artifacts Gallery View
          <ArtifactsGallery
            chats={filteredChats()}
            onNewArtifact={handleNewChat}
            onNavigateToChat={chatId => navigate(`/chat/${chatId}`)}
          />
        ) : activeNav === 'chats' && !chat && !isNewChat ? (
          // Chat List View
          <div className={styles.chatListContainer}>
            {/* Header */}
            <div className={styles.chatListHeader}>
              <h1 className={styles.chatListTitle}>Chats</h1>
              <button
                className={styles.newChatButton}
                onClick={handleNewChat}
                aria-label="Start new chat"
              >
                <Plus size={16} strokeWidth={2} aria-hidden="true" />
                New chat
              </button>
            </div>

            {/* Search Bar */}
            <div className={styles.searchContainer}>
              <div className={styles.searchWrapper}>
                <Search size={18} className={styles.searchIcon} aria-hidden="true" />
                <input
                  type="search"
                  placeholder="Search your chats..."
                  value={searchQuery}
                  onChange={e => setSearchQuery(e.target.value)}
                  className={styles.searchInput}
                  aria-label="Search chats"
                />
              </div>
            </div>

            {/* Chat count and Select/Actions */}
            <div className={styles.chatListMeta}>
              {isSelectionMode ? (
                <>
                  <span className={styles.chatCount} aria-live="polite">
                    {selectedChatIds.size} selected
                  </span>
                  <div className={styles.selectionActions}>
                    <button
                      className={styles.deleteSelectedButton}
                      onClick={confirmBulkDelete}
                      disabled={selectedChatIds.size === 0}
                      aria-label={`Delete ${selectedChatIds.size} selected chats`}
                    >
                      <Trash2 size={16} />
                      Delete
                    </button>
                    <button
                      className={styles.cancelButton}
                      onClick={toggleSelectionMode}
                      aria-label="Cancel selection"
                    >
                      Cancel
                    </button>
                  </div>
                </>
              ) : (
                <>
                  <span className={styles.chatCount} aria-live="polite">
                    {chatList.length} {chatList.length === 1 ? 'chat' : 'chats'}
                  </span>
                  <button
                    className={styles.selectButton}
                    onClick={toggleSelectionMode}
                    aria-label="Select chats"
                  >
                    Select
                  </button>
                </>
              )}
            </div>

            {/* Chat List */}
            <div className={styles.chatList} role="list">
              {chatList.map(chat => {
                const isSelected = selectedChatIds.has(chat.id);
                return (
                  <button
                    key={chat.id}
                    onClick={() =>
                      isSelectionMode ? toggleChatSelection(chat.id) : handleChatSelect(chat.id)
                    }
                    className={`${styles.chatItem} ${isSelected ? styles.chatItemSelected : ''}`}
                    role="listitem"
                    aria-label={
                      isSelectionMode
                        ? `${isSelected ? 'Deselect' : 'Select'} chat: ${chat.title}`
                        : `Open chat: ${chat.title}`
                    }
                  >
                    {isSelectionMode && (
                      <div className={styles.chatItemCheckbox}>
                        <div
                          className={`${styles.checkbox} ${isSelected ? styles.checkboxChecked : ''}`}
                        >
                          {isSelected && <Check size={14} strokeWidth={3} />}
                        </div>
                      </div>
                    )}
                    <div className={styles.chatItemContent}>
                      <div className={styles.chatItemTitle}>{chat.title}</div>
                      <div className={styles.chatItemTimestamp}>
                        Last message {formatRelativeTime(chat.updatedAt)}
                      </div>
                    </div>
                  </button>
                );
              })}
            </div>
          </div>
        ) : !chat && !isNewChat ? (
          // Empty state
          <div className={styles.emptyState}>
            <div className={styles.emptyStateContent}>
              <div className={styles.emptyStateTitle}>Start a new conversation</div>
              <p className={styles.emptyStateDescription}>
                Select "New chat" to begin a conversation, or choose a chat from the list to
                continue an existing conversation.
              </p>
            </div>
          </div>
        ) : (
          // Chat conversation view with artifact pane
          <PanelGroup direction="horizontal" className={styles.conversationContainer}>
            {/* Main Chat Panel */}
            <Panel
              defaultSize={splitRatio}
              minSize={30}
              maxSize={80}
              onResize={setSplitRatio}
              className={styles.chatPanelWrapper}
            >
              <div className={styles.chatPanel}>
                {!hasStartedChat ? (
                  // New chat - CommandCenter in centered mode with dynamic greeting
                  <CommandCenter
                    mode="centered"
                    greeting={dynamicGreeting || 'Golden hour thinking'}
                    onSendMessage={handleSendMessage}
                    belowAttachmentsSlot={
                      <KnowledgeProjectChips
                        projects={knowledgeProjects}
                        selectedIds={knowledgeProjectIds}
                        onChange={handleKnowledgeProjectsChange}
                        disabled={isLoading}
                      />
                    }
                    activePrompt={activePrompt}
                    onPromptSubmit={answers => {
                      if (activePrompt) {
                        websocketService.sendPromptResponse(
                          activePrompt.promptId,
                          activePrompt.conversationId,
                          answers,
                          false
                        );
                        submitPromptResponse(activePrompt.promptId, answers);
                      }
                    }}
                    onPromptSkip={() => {
                      if (activePrompt) {
                        websocketService.sendPromptResponse(
                          activePrompt.promptId,
                          activePrompt.conversationId,
                          {},
                          true
                        );
                        skipPrompt(activePrompt.promptId);
                      }
                    }}
                    suggestions={[
                      {
                        label: 'Write',
                        icon: <PenLine size={16} />,
                        prompt:
                          "I'd like to write something. Could you help me get started? Please ask me what kind of content I want to create.",
                        systemInstruction:
                          'You are an expert writing assistant. Help the user draft, edit, and refine content. Start by understanding their goals.',
                      },
                      {
                        label: 'Learn',
                        icon: <GraduationCap size={16} />,
                        prompt:
                          "I want to learn something new. Please act as a tutor and ask me what topic I'm interested in.",
                        systemInstruction:
                          "You are a knowledgeable and patient tutor. Explain concepts clearly and adapt to the user's understanding.",
                      },
                      {
                        label: 'Code',
                        icon: <Code2 size={16} />,
                        prompt:
                          "I have a coding task. Please act as a senior developer and ask me about the programming language and the problem I'm solving.",
                        systemInstruction:
                          'You are an expert software engineer. Provide secure, efficient, and idiomatic code solutions.',
                      },
                      {
                        label: 'Life stuff',
                        icon: <Coffee size={16} />,
                        prompt:
                          "I'd like some advice on life. Please act as a supportive coach and ask me what's on my mind.",
                        systemInstruction:
                          'You are a supportive and empathetic life coach. Listen actively and offer constructive advice.',
                      },
                      {
                        label: "Dobby's choice",
                        icon: <Sparkles size={16} />,
                        prompt:
                          'Surprise me with something interesting! It could be a fun fact, a short story, or a creative idea.',
                        systemInstruction:
                          'You are Dobby, a creative and entertaining AI assistant. Be spontaneous and engaging.',
                      },
                    ]}
                    isLoading={isLoading}
                    onStopGeneration={handleStopGeneration}
                    isLoadingModels={isLoadingModels}
                    isConnected={isWsConnected}
                    hasEverConnected={hasEverConnected}
                    initialValue={initialPrompt}
                    autoSendInitialValue
                    onInitialValueConsumed={handleInitialPromptConsumed}
                    disabled={false}
                    allowFileUpload
                  />
                ) : (
                  <>
                    {/* Chat Header */}
                    <header className={styles.chatHeader}>
                      {/* Editable Title */}
                      {isEditingTitle ? (
                        <input
                          type="text"
                          value={editTitleValue}
                          onChange={e => setEditTitleValue(e.target.value)}
                          onBlur={handleUpdateTitle}
                          onKeyDown={e => {
                            if (e.key === 'Enter') {
                              handleUpdateTitle();
                            } else if (e.key === 'Escape') {
                              setIsEditingTitle(false);
                              setEditTitleValue(chatTitle);
                            }
                          }}
                          autoFocus
                          className={styles.titleInput}
                          aria-label="Edit chat title"
                        />
                      ) : (
                        <button
                          onClick={() => {
                            setEditTitleValue(chatTitle);
                            setIsEditingTitle(true);
                          }}
                          className={styles.titleButton}
                          aria-label="Click to edit chat title"
                        >
                          <span>{isAnimatingTitle ? displayTitle : chatTitle}</span>
                          {chatPrivacyMode === 'cloud' ? (
                            <Tooltip
                              content="Encrypted & synced across your devices"
                              position="bottom"
                            >
                              <Cloud
                                size={14}
                                aria-label="Synced to cloud"
                                className={styles.privacyIcon}
                              />
                            </Tooltip>
                          ) : (
                            <Tooltip content="Stored only on this device" position="bottom">
                              <Smartphone
                                size={14}
                                aria-label="Stored locally"
                                className={styles.privacyIcon}
                              />
                            </Tooltip>
                          )}
                        </button>
                      )}
                    </header>

                    {/* Messages Area */}
                    <div
                      ref={messagesAreaRef}
                      className={styles.messagesArea}
                      role="log"
                      aria-live="polite"
                    >
                      <div className={styles.messagesWrapper}>
                        {messages
                          .filter(m => !m.isHidden) // Filter out hidden versions
                          .map((message, index) => {
                            // Compute version info for assistant messages
                            let currentVersion: number | undefined;
                            let totalVersions: number | undefined;

                            if (message.role === 'assistant' && message.versionGroupId) {
                              const allVersions = messages.filter(
                                m =>
                                  m.versionGroupId === message.versionGroupId &&
                                  m.role === 'assistant'
                              );
                              totalVersions = allVersions.length;
                              currentVersion = message.versionNumber || 1;
                            }

                            return (
                              <div key={message.id} className={styles.messageContainer}>
                                {message.role === 'user' ? (
                                  <UserMessage
                                    message={message}
                                    userInitials={userInitials}
                                    copiedMessageId={copiedMessageId}
                                    onCopy={handleCopyMessage}
                                    onEditSubmit={handleEditAndRegenerate}
                                  />
                                ) : (
                                  <AssistantMessage
                                    message={message}
                                    chatId={chat?.id || ''}
                                    index={index}
                                    isThinkingExpanded={expandedThinkingPanes.has(message.id)}
                                    thinkingVerb={getThinkingVerb()}
                                    copiedMessageId={copiedMessageId}
                                    isLoading={isLoading}
                                    currentVersion={currentVersion}
                                    totalVersions={totalVersions}
                                    onToggleThinking={handleToggleThinking}
                                    onToggleToolExpansion={toggleToolExpansion}
                                    onToggleAllTools={toggleAllTools}
                                    onCopy={handleCopyMessage}
                                    onRetry={(_, type) => handleRetry(message.id, type)}
                                    onVersionNavigate={handleVersionNavigate}
                                    onOpenArtifacts={openArtifacts}
                                  />
                                )}
                              </div>
                            );
                          })}

                        {/* Scroll anchor */}
                        <div ref={messagesEndRef} />
                      </div>
                    </div>

                    {/* Scroll to bottom button - always render, use opacity for show/hide */}
                    <button
                      className={styles.scrollToBottomButton}
                      onClick={scrollToBottom}
                      aria-label="Scroll to bottom"
                      style={{
                        opacity: showScrollButton ? 1 : 0,
                        pointerEvents: showScrollButton ? 'auto' : 'none',
                      }}
                    >
                      <ArrowDown size={18} />
                    </button>

                    {/* CommandCenter at bottom */}
                    <CommandCenter
                      ref={commandCenterRef}
                      mode="bottom"
                      onSendMessage={handleSendMessage}
                      belowAttachmentsSlot={
                        <KnowledgeProjectChips
                          projects={knowledgeProjects}
                          selectedIds={knowledgeProjectIds}
                          onChange={handleKnowledgeProjectsChange}
                          disabled={isLoading}
                        />
                      }
                      placeholder="Type your message here..."
                      isLoading={isLoading}
                      onStopGeneration={handleStopGeneration}
                      isLoadingModels={isLoadingModels}
                      isConnected={isWsConnected}
                      hasEverConnected={hasEverConnected}
                      initialValue={initialPrompt}
                      autoSendInitialValue
                      onInitialValueConsumed={handleInitialPromptConsumed}
                      activePrompt={activePrompt}
                      onPromptSubmit={answers => {
                        if (activePrompt) {
                          websocketService.sendPromptResponse(
                            activePrompt.promptId,
                            activePrompt.conversationId,
                            answers,
                            false
                          );
                          submitPromptResponse(activePrompt.promptId, answers);
                        }
                      }}
                      onPromptSkip={() => {
                        if (activePrompt) {
                          websocketService.sendPromptResponse(
                            activePrompt.promptId,
                            activePrompt.conversationId,
                            {},
                            true
                          );
                          skipPrompt(activePrompt.promptId);
                        }
                      }}
                      disabled={false}
                      allowFileUpload
                    />
                  </>
                )}
              </div>
            </Panel>

            {/* Resize Handle */}
            {isArtifactPaneOpen && (
              <>
                <PanelResizeHandle className={styles.resizeHandle} />

                {/* Artifact Panel */}
                <Panel
                  className={styles.artifactPanel}
                  defaultSize={100 - splitRatio}
                  minSize={20}
                  maxSize={70}
                >
                  <ArtifactPane />
                </Panel>
              </>
            )}
          </PanelGroup>
        )}
      </main>

      {/* Conversation Search Modal (cmd/ctrl + k) */}
      <ConversationSearchModal
        isOpen={searchOpen}
        onClose={() => setSearchOpen(false)}
        onSelect={(targetChatId) => {
          navigate(`/chat/${targetChatId}`);
        }}
      />

      {/* Name Input Modal */}
      <NameInputModal isOpen={showNameModal} onSubmit={handleNameSubmit} />

      {/* Privacy Settings Modal */}
      <PrivacySettingsModal isOpen={showPrivacyModal} onSubmit={handlePrivacySubmit} />

      {/* Delete Confirmation Dialog */}
      <ConfirmDialog
        isOpen={showDeleteDialog}
        onClose={() => {
          setShowDeleteDialog(false);
          setChatToDelete(null);
        }}
        onConfirm={isSelectionMode ? deleteSelectedChats : confirmDelete}
        title={isSelectionMode ? 'Delete Chats' : 'Delete Chat'}
        message={
          isSelectionMode
            ? `Are you sure you want to delete ${selectedChatIds.size} ${selectedChatIds.size === 1 ? 'chat' : 'chats'}? This action cannot be undone.`
            : 'Are you sure you want to delete this chat? This action cannot be undone.'
        }
        confirmText="Delete"
        cancelText="Cancel"
        variant="danger"
      />

      {/* Rename Dialog */}
      <RenameDialog
        isOpen={showRenameDialog}
        onClose={() => {
          setShowRenameDialog(false);
          setChatToRename(null);
        }}
        onRename={confirmRename}
        currentTitle={chatToRename?.title || ''}
        title="Rename Chat"
      />

      {/* Mobile Image Gallery Modal */}
      <ImageGalleryModal
        isOpen={isGalleryOpen}
        images={galleryImages}
        initialIndex={galleryInitialIndex}
        onClose={closeGallery}
      />
    </div>
  );
};

/**
 * Get a random thinking verb for the thinking pane
 */
const thinkingVerbs = [
  'Dobby is thinking',
  'Dobby is wondering',
  'Dobby is figuring out',
  'Dobby is pondering',
  'Dobby is analyzing',
  'Dobby is reasoning',
  'Dobby is considering',
];

let currentThinkingVerb: string | null = null;

function getThinkingVerb(): string {
  // Keep the same verb for the duration of a thinking session
  if (!currentThinkingVerb) {
    currentThinkingVerb = thinkingVerbs[Math.floor(Math.random() * thinkingVerbs.length)];
  }
  return currentThinkingVerb;
}

function resetThinkingVerb(): void {
  currentThinkingVerb = null;
}

/**
 * Format a date as relative time (e.g., "5 hours ago")
 */
function formatRelativeTime(date: Date | undefined | null): string {
  if (!date) return 'Unknown';

  try {
    const now = new Date();
    const dateObj = date instanceof Date ? date : new Date(date);
    if (isNaN(dateObj.getTime())) return 'Unknown';

    const diffMs = now.getTime() - dateObj.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    const diffHours = Math.floor(diffMs / 3600000);
    const diffDays = Math.floor(diffMs / 86400000);

    if (diffMins < 1) return 'just now';
    if (diffMins < 60) return `${diffMins} ${diffMins === 1 ? 'minute' : 'minutes'} ago`;
    if (diffHours < 24) return `${diffHours} ${diffHours === 1 ? 'hour' : 'hours'} ago`;
    if (diffDays < 7) return `${diffDays} ${diffDays === 1 ? 'day' : 'days'} ago`;
    if (diffDays < 30) {
      const weeks = Math.floor(diffDays / 7);
      return `${weeks} ${weeks === 1 ? 'week' : 'weeks'} ago`;
    }

    return dateObj.toLocaleDateString();
  } catch {
    return 'Unknown';
  }
}
