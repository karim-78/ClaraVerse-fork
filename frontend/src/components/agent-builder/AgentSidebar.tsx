import { useState, useEffect, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Plus,
  Bot,
  Rocket,
  Home,
  PanelLeftClose,
  PanelLeft,
  Loader2,
  MoreHorizontal,
  Trash2,
  MessageSquare,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { useAgentBuilderStore } from '@/store/useAgentBuilderStore';
import type { AgentView } from '@/store/useAgentBuilderStore';
import { deleteAgent as deleteAgentAPI } from '@/services/agentService';
import faviconIcon from '@/assets/favicon-32x32.png';

interface AgentSidebarProps {
  className?: string;
  isCollapsed?: boolean;
  onToggle?: () => void;
  /** When true, sidebar renders in mobile mode (always expanded, calls onNavigate after nav) */
  isMobile?: boolean;
  /** Called after navigation actions - used to close mobile sidebar */
  onNavigate?: () => void;
}

export function AgentSidebar({
  className,
  isCollapsed = false,
  onToggle,
  isMobile = false,
  onNavigate,
}: AgentSidebarProps) {
  const navigate = useNavigate();
  const {
    backendAgents,
    selectedAgentId,
    activeView,
    isLoadingAgents,
    createAgent,
    setActiveView,
    trackAgentAccess,
    loadAgentFromBackend,
    fetchRecentAgents,
    deleteAgent,
    requestAgentSwitch,
  } = useAgentBuilderStore();

  const [isCreating, setIsCreating] = useState(false);
  const [menuOpenId, setMenuOpenId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  // On mobile, always show expanded view
  const isExpanded = isMobile || !isCollapsed;

  // Close menu when clicking outside
  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenuOpenId(null);
      }
    };
    if (menuOpenId) {
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [menuOpenId]);

  // Fetch recent agents from backend on mount
  useEffect(() => {
    fetchRecentAgents();
  }, [fetchRecentAgents]);

  // Display backend agents as recents (already sorted by updated_at desc)
  const recentAgents = backendAgents.slice(0, 10);

  const handleNewAgent = async () => {
    if (isCreating) return;
    setIsCreating(true);
    try {
      const agent = await createAgent('New Agent', 'Describe what this agent does...');
      if (agent) {
        trackAgentAccess(agent.id);
        setActiveView('onboarding');
        navigate(`/agents/builder/${agent.id}`);
        // Close mobile sidebar after navigation
        onNavigate?.();
      }
    } finally {
      setIsCreating(false);
    }
  };

  const handleNavClick = (view: AgentView) => {
    setActiveView(view);
    // Navigate to base agents page for list views
    if (view === 'my-agents' || view === 'deployed') {
      navigate('/agents');
    }
    // Close mobile sidebar after navigation
    onNavigate?.();
  };

  const handleRecentClick = async (agentId: string) => {
    const doSwitch = async () => {
      trackAgentAccess(agentId);
      await loadAgentFromBackend(agentId);
      setActiveView('canvas');
      navigate(`/agents/builder/${agentId}`);
      onNavigate?.();
    };

    // Guard: check for unsaved changes before switching
    const canProceed = requestAgentSwitch(agentId, () => {
      doSwitch();
    });
    if (!canProceed) return;
    await doSwitch();
  };

  const handleDeleteAgent = async (agentId: string, e: React.MouseEvent) => {
    e.stopPropagation();
    setDeletingId(agentId);
    setMenuOpenId(null);
    try {
      await deleteAgentAPI(agentId);
      deleteAgent(agentId);
    } catch (error) {
      console.error('Failed to delete agent:', error);
    } finally {
      setDeletingId(null);
    }
  };

  const handleMenuClick = (agentId: string, e: React.MouseEvent) => {
    e.stopPropagation();
    setMenuOpenId(menuOpenId === agentId ? null : agentId);
  };

  return (
    <div
      className={cn('h-full flex flex-col bg-[#080808] overflow-hidden', className)}
      style={{ borderRight: '1px solid var(--color-border)' }}
    >
      {/* Header - Logo + Title + Toggle */}
      <header
        className={cn(
          'p-4 flex items-center gap-2 flex-shrink-0',
          isExpanded ? 'justify-between' : 'justify-center'
        )}
      >
        {isExpanded && (
          <div className="flex items-center gap-2 flex-1 overflow-hidden">
            <img src={faviconIcon} alt="Dobby" className="w-6 h-6 flex-shrink-0 rounded-full" />
            <span className="text-[1.375rem] font-semibold text-[var(--color-text-primary)] whitespace-nowrap overflow-hidden">
              Agents
            </span>
          </div>
        )}
        {/* On mobile, clicking toggle closes sidebar via onNavigate. On desktop, use onToggle */}
        <button
          onClick={isMobile ? onNavigate : onToggle}
          className="flex items-center justify-center w-8 h-8 rounded-md bg-transparent text-[var(--color-text-secondary)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text-primary)] transition-all flex-shrink-0"
          aria-label={isExpanded ? 'Collapse sidebar' : 'Expand sidebar'}
        >
          {isExpanded ? <PanelLeftClose size={20} /> : <PanelLeft size={20} />}
        </button>
      </header>

      {/* New Agent Button */}
      <div className={cn('pb-4 flex-shrink-0', isExpanded ? 'px-3' : 'flex justify-center')}>
        <button
          onClick={handleNewAgent}
          disabled={isCreating}
          className={cn(
            'flex items-center text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] hover:bg-[var(--color-surface-hover)] transition-all rounded-md disabled:opacity-50',
            isExpanded ? 'w-full gap-3 p-3 justify-start' : 'w-8 h-8 justify-center'
          )}
          title="New Agent"
        >
          {isCreating ? (
            <Loader2 size={20} className="animate-spin" />
          ) : (
            <Plus size={20} strokeWidth={2} />
          )}
          {isExpanded && (
            <span className="text-[0.9375rem] font-medium whitespace-nowrap overflow-hidden">
              {isCreating ? 'Creating...' : 'New agent'}
            </span>
          )}
        </button>
      </div>

      {/* Navigation Items */}
      <nav className={cn('flex-shrink-0 flex flex-col gap-1', isExpanded ? 'px-3' : 'px-2')}>
        {/* My Agents */}
        <button
          onClick={() => handleNavClick('my-agents')}
          className={cn(
            'flex items-center rounded-md transition-all',
            isExpanded ? 'w-full gap-3 p-3 justify-start' : 'w-full p-3 justify-center',
            activeView === 'my-agents'
              ? 'bg-[var(--color-surface-hover)] text-[var(--color-text-primary)]'
              : 'text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] hover:bg-[var(--color-surface-hover)]'
          )}
          title="My Agents"
        >
          <Bot size={18} strokeWidth={2} />
          {isExpanded && (
            <span className="text-[0.9375rem] font-medium whitespace-nowrap overflow-hidden">
              My Agents
            </span>
          )}
        </button>

        {/* Deployed */}
        <button
          onClick={() => handleNavClick('deployed')}
          className={cn(
            'flex items-center rounded-md transition-all',
            isExpanded ? 'w-full gap-3 p-3 justify-start' : 'w-full p-3 justify-center',
            activeView === 'deployed'
              ? 'bg-[var(--color-surface-hover)] text-[var(--color-text-primary)]'
              : 'text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] hover:bg-[var(--color-surface-hover)]'
          )}
          title="Deployed"
        >
          <Rocket size={18} strokeWidth={2} />
          {isExpanded && (
            <span className="text-[0.9375rem] font-medium whitespace-nowrap overflow-hidden">
              Deployed
            </span>
          )}
        </button>
      </nav>

      {/* Recents Section - only when expanded */}
      {isExpanded && (
        <div className="flex-1 overflow-y-auto px-3 mt-4">
          <h3 className="px-3 pb-2 text-xs font-medium text-[var(--color-text-tertiary)] uppercase tracking-wide">
            Recents
          </h3>
          {isLoadingAgents ? (
            <div className="flex items-center justify-center py-4">
              <Loader2 size={16} className="animate-spin text-[var(--color-text-tertiary)]" />
            </div>
          ) : recentAgents.length === 0 ? (
            <div className="px-3 py-2 text-xs text-[var(--color-text-tertiary)]">No agents yet</div>
          ) : (
            <div className="space-y-0.5">
              {recentAgents.map(
                agent =>
                  agent && (
                    <div
                      key={agent.id}
                      className="relative group"
                      ref={menuOpenId === agent.id ? menuRef : null}
                    >
                      <button
                        onClick={() => handleRecentClick(agent.id)}
                        disabled={deletingId === agent.id}
                        className={cn(
                          'w-full flex items-center gap-2 px-3 py-2.5 rounded-md text-left transition-all text-sm',
                          selectedAgentId === agent.id && activeView === 'canvas'
                            ? 'bg-[var(--color-surface-hover)] text-[var(--color-text-primary)]'
                            : 'text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] hover:bg-[var(--color-surface-hover)]',
                          deletingId === agent.id && 'opacity-50'
                        )}
                      >
                        <span className="truncate flex-1">{agent.name}</span>
                        {deletingId === agent.id && (
                          <Loader2 size={14} className="animate-spin flex-shrink-0" />
                        )}
                      </button>
                      {/* Three-dot menu button - visible on hover */}
                      <button
                        onClick={e => handleMenuClick(agent.id, e)}
                        className={cn(
                          'absolute right-1 top-1/2 -translate-y-1/2 p-1.5 rounded-md transition-all',
                          'text-[var(--color-text-tertiary)] hover:text-[var(--color-text-primary)] hover:bg-[var(--color-surface)]',
                          menuOpenId === agent.id
                            ? 'opacity-100'
                            : 'opacity-0 group-hover:opacity-100'
                        )}
                      >
                        <MoreHorizontal size={14} />
                      </button>
                      {/* Dropdown menu */}
                      {menuOpenId === agent.id && (
                        <div className="absolute right-0 top-full mt-1 z-50 bg-[var(--color-surface-elevated)] border border-[var(--color-border)] rounded-lg shadow-xl py-1 min-w-[120px]">
                          <button
                            onClick={e => handleDeleteAgent(agent.id, e)}
                            className="w-full flex items-center gap-2 px-3 py-2 text-sm text-red-400 hover:bg-red-500/10 transition-colors"
                          >
                            <Trash2 size={14} />
                            <span>Delete</span>
                          </button>
                        </div>
                      )}
                    </div>
                  )
              )}
            </div>
          )}
        </div>
      )}

      {/* Spacer when collapsed */}
      {!isExpanded && <div className="flex-1" />}

      {/* Navigation Footer */}
      <footer className={cn('p-3 flex-shrink-0 mt-auto')}>
        <div className={cn('flex gap-3', !isExpanded && 'flex-col gap-2')}>
          <a
            href="/"
            className={cn(
              'flex items-center text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] hover:bg-[var(--color-surface-hover)] transition-all rounded-md font-medium',
              isExpanded
                ? 'flex-1 justify-start gap-3 p-3 text-[0.9375rem]'
                : 'w-full justify-center p-3'
            )}
            title="Home"
          >
            <Home size={18} strokeWidth={2} />
            {isExpanded && <span className="whitespace-nowrap">Home</span>}
          </a>
          <a
            href="/chat"
            className={cn(
              'flex items-center text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] hover:bg-[var(--color-surface-hover)] transition-all rounded-md font-medium',
              isExpanded
                ? 'flex-1 justify-start gap-3 p-3 text-[0.9375rem]'
                : 'w-full justify-center p-3'
            )}
            title="Chat"
          >
            <MessageSquare size={18} strokeWidth={2} />
            {isExpanded && <span className="whitespace-nowrap">Chat</span>}
          </a>
        </div>
      </footer>
    </div>
  );
}
