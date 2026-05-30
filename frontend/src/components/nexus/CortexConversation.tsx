import { memo, useEffect, useRef, useMemo, useState, useCallback } from 'react';
import {
  Brain,
  User,
  AlertCircle,
  CheckCircle2,
  ChevronDown,
  ArrowDown,
} from 'lucide-react';
import { MarkdownRenderer } from '@/components/design-system/content/MarkdownRenderer';
import { useNexusStore } from '@/store/useNexusStore';
import { DaemonActivityBlock } from './DaemonActivityBlock';
import { MissedUpdatesBar } from './MissedUpdatesBar';
import type { NexusConversationItem } from '@/types/nexus';
import styles from './Nexus.module.css';

const SUGGESTIONS = [
  'Search for flights to Tokyo',
  'Build me a portfolio website',
  'Review this pull request',
  'Find the best restaurants nearby',
];

interface CortexConversationProps {
  onExplainTask?: (taskId: string) => void;
}

export const CortexConversation = memo(function CortexConversation({
  onExplainTask,
}: CortexConversationProps) {
  const conversation = useNexusStore((s) => s.conversation);
  const missedUpdates = useNexusStore((s) => s.missedUpdates);
  const isProcessing = useNexusStore((s) => s.isProcessing);
  const hasActiveDaemons = useNexusStore(
    (s) => Object.values(s.daemons).some((d) => d.status === 'executing')
  );
  const showProcessing = isProcessing || hasActiveDaemons;
  const scrollRef = useRef<HTMLDivElement>(null);
  const [showScrollBtn, setShowScrollBtn] = useState(false);

  // Auto-scroll on new items
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const isNearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 150;
    if (isNearBottom) {
      el.scrollTop = el.scrollHeight;
    }
  }, [conversation.length]);

  // Track scroll position for scroll-to-bottom button
  const handleScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    const distFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    setShowScrollBtn(distFromBottom > 200);
  }, []);

  const scrollToBottom = useCallback(() => {
    const el = scrollRef.current;
    if (el) {
      el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' });
    }
  }, []);

  // Group consecutive daemon_activity items by daemonId
  const grouped = useMemo(() => {
    const groups: Array<
      | { type: 'single'; item: NexusConversationItem }
      | { type: 'daemon'; daemonId: string; daemonRole: string; items: NexusConversationItem[] }
    > = [];
    let currentDaemon: {
      daemonId: string;
      daemonRole: string;
      items: NexusConversationItem[];
    } | null = null;

    for (const item of conversation) {
      if (item.type === 'daemon_activity' && item.daemonId) {
        if (currentDaemon && currentDaemon.daemonId === item.daemonId) {
          currentDaemon.items.push(item);
        } else {
          if (currentDaemon) groups.push({ type: 'daemon', ...currentDaemon });
          currentDaemon = {
            daemonId: item.daemonId,
            daemonRole: item.daemonRole ?? 'Daemon',
            items: [item],
          };
        }
      } else {
        if (currentDaemon) {
          groups.push({ type: 'daemon', ...currentDaemon });
          currentDaemon = null;
        }
        groups.push({ type: 'single', item });
      }
    }
    if (currentDaemon) groups.push({ type: 'daemon', ...currentDaemon });
    return groups;
  }, [conversation]);

  const hasMessages = grouped.length > 0 || showProcessing || missedUpdates.length > 0;

  return (
    <div className={styles.conversationArea} ref={scrollRef} onScroll={handleScroll}>
      {!hasMessages ? (
        /* Empty State */
        <div className={styles.emptyState}>
          <div className={styles.emptyStateContent}>
            <div className={styles.emptyStateIcon}>
              <Brain size={56} strokeWidth={1} />
            </div>
            <h3 className={styles.emptyStateTitle}>What can I help with?</h3>
            <p className={styles.emptyStateDescription}>
              Give Dobby a task. Simple questions get instant answers. Complex tasks deploy
              specialized Daemons that work autonomously with tools.
            </p>
            <div className={styles.suggestionPills}>
              {SUGGESTIONS.map((s) => (
                <button key={s} className={styles.suggestionPill} tabIndex={-1}>
                  {s}
                </button>
              ))}
            </div>
          </div>
        </div>
      ) : (
        <div className={styles.conversationWrapper}>
          {missedUpdates.length > 0 && onExplainTask && (
            <MissedUpdatesBar onExplainTask={onExplainTask} />
          )}
          {grouped.map((group, i) => {
            if (group.type === 'daemon') {
              return (
                <DaemonActivityBlock
                  key={`daemon-${group.daemonId}-${i}`}
                  daemonId={group.daemonId}
                  daemonRole={group.daemonRole}
                  items={group.items}
                />
              );
            }

            const item = group.item;
            return (
              <div key={item.id} className={styles.messageItem}>
                {/* User message */}
                {item.type === 'user_message' && (
                  <div className={styles.userMessageRow}>
                    <div className={styles.userBubble}>
                      <div className={styles.userBadge}>
                        <User size={14} />
                      </div>
                      <div className={styles.userText}>{item.content}</div>
                    </div>
                  </div>
                )}

                {/* Cortex response */}
                {item.type === 'cortex_response' && (
                  <div className={styles.assistantMessage}>
                    <div className={styles.assistantLabel}>
                      <Brain size={14} />
                      <span>Dobby</span>
                    </div>
                    <div className={styles.assistantContent}>
                      <MarkdownRenderer content={item.content} />
                    </div>
                  </div>
                )}

                {/* Thinking */}
                {item.type === 'cortex_thinking' && (
                  <ThinkingPane content={item.content} />
                )}

                {/* Task result */}
                {item.type === 'task_result' && (
                  <div className={styles.taskResultMessage}>
                    <div className={styles.taskResultBadge}>
                      <CheckCircle2 size={12} />
                      Task Complete
                    </div>
                    <div className={styles.assistantContent}>
                      <MarkdownRenderer content={item.content} />
                    </div>
                  </div>
                )}

                {/* Error */}
                {item.type === 'error' && (
                  <div className={styles.errorCard}>
                    <AlertCircle size={16} />
                    <span>{item.content}</span>
                  </div>
                )}
              </div>
            );
          })}

          {/* Processing indicator */}
          {showProcessing && (
            <div className={styles.thinkingIndicator}>
              <span className={styles.thinkingDot} />
              <span className={styles.thinkingDot} />
              <span className={styles.thinkingDot} />
            </div>
          )}
        </div>
      )}

      {/* Scroll to bottom button */}
      {showScrollBtn && (
        <button className={styles.scrollToBottom} onClick={scrollToBottom}>
          <ArrowDown size={16} />
        </button>
      )}
    </div>
  );
});

/* ── Thinking Pane Sub-component ──────────────────────────────────── */

const ThinkingPane = memo(function ThinkingPane({ content }: { content: string }) {
  const [expanded, setExpanded] = useState(false);

  return (
    <div className={styles.thinkingPane}>
      <div className={styles.thinkingHeader} onClick={() => setExpanded(!expanded)}>
        <div className={styles.thinkingTitle}>
          <Brain size={14} />
          <span>Thinking</span>
        </div>
        <ChevronDown
          size={14}
          style={{
            transform: expanded ? 'none' : 'rotate(-90deg)',
            transition: 'transform 0.2s',
            color: 'var(--color-text-tertiary)',
          }}
        />
      </div>
      {expanded && <div className={styles.thinkingContent}>{content}</div>}
    </div>
  );
});
