/**
 * UserMessage Component
 *
 * Renders a user message bubble. Supports inline editing — clicking Edit
 * replaces the bubble with a textarea; saving calls onEditSubmit with the
 * new text. The parent handles the actual conversation fork: snapshots the
 * existing branch, swaps the user message content, drops everything after
 * it, and re-sends so the assistant regenerates from this turn.
 */

import { memo, useEffect, useRef, useState } from 'react';
import { Check, Copy, Pencil, X } from 'lucide-react';
import type { Message } from '@/types/chat';
import { MarkdownRenderer } from '@/components/design-system/content/MarkdownRenderer';
import { MessageAttachment } from './MessageAttachment';
import styles from '@/pages/Chat.module.css';

export interface UserMessageProps {
  message: Message;
  userInitials: string;
  copiedMessageId: string | null;
  onCopy: (content: string, id: string) => void;
  /** Called when the user saves an edit. Parent forks + regenerates. */
  onEditSubmit?: (messageId: string, newContent: string) => void;
}

function UserMessageComponent({
  message,
  userInitials,
  copiedMessageId,
  onCopy,
  onEditSubmit,
}: UserMessageProps) {
  const [isEditing, setIsEditing] = useState(false);
  const [draft, setDraft] = useState(message.content);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // When entering edit mode, focus + grow the textarea to fit current content.
  useEffect(() => {
    if (isEditing && textareaRef.current) {
      const el = textareaRef.current;
      el.focus();
      el.style.height = 'auto';
      el.style.height = el.scrollHeight + 'px';
      // Place cursor at end
      const len = el.value.length;
      el.setSelectionRange(len, len);
    }
  }, [isEditing]);

  const handleStartEdit = () => {
    setDraft(message.content);
    setIsEditing(true);
  };

  const handleCancel = () => {
    setIsEditing(false);
    setDraft(message.content);
  };

  const handleSave = () => {
    const trimmed = draft.trim();
    if (!trimmed || trimmed === message.content || !onEditSubmit) {
      setIsEditing(false);
      return;
    }
    onEditSubmit(message.id, trimmed);
    setIsEditing(false);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      handleCancel();
    } else if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      handleSave();
    }
  };

  return (
    <>
      {/* File Attachments - shown above chat bubble */}
      {message.attachments && message.attachments.length > 0 && !isEditing && (
        <div style={{ marginBottom: 'var(--space-3)' }}>
          <MessageAttachment attachments={message.attachments} />
        </div>
      )}
      <div className={styles.userMessageRow}>
        <div className={styles.userMessage}>
          <div className={styles.userBadge} aria-label="User message">
            {userInitials}
          </div>

          {isEditing ? (
            <div
              style={{
                flex: 1,
                display: 'flex',
                flexDirection: 'column',
                gap: 8,
                minWidth: 0,
              }}
            >
              <textarea
                ref={textareaRef}
                value={draft}
                onChange={e => {
                  setDraft(e.target.value);
                  // auto-grow
                  e.target.style.height = 'auto';
                  e.target.style.height = e.target.scrollHeight + 'px';
                }}
                onKeyDown={handleKeyDown}
                placeholder="Edit your message…"
                style={{
                  width: '100%',
                  minHeight: 60,
                  padding: '8px 10px',
                  background: 'var(--color-surface)',
                  border: '1px solid var(--color-accent-border)',
                  borderRadius: 'var(--radius-md)',
                  color: 'var(--color-text-primary)',
                  font: 'inherit',
                  resize: 'vertical',
                  outline: 'none',
                }}
              />
              <div
                style={{
                  display: 'flex',
                  gap: 8,
                  alignItems: 'center',
                  justifyContent: 'flex-end',
                }}
              >
                <span
                  style={{
                    fontSize: '0.7rem',
                    color: 'var(--color-text-tertiary)',
                    marginRight: 'auto',
                  }}
                >
                  Saving forks the conversation from here. ⌘+↵ to save, Esc to cancel.
                </span>
                <button
                  onClick={handleCancel}
                  style={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: 4,
                    padding: '4px 10px',
                    border: '1px solid var(--color-border)',
                    background: 'transparent',
                    color: 'var(--color-text-secondary)',
                    borderRadius: 'var(--radius-full)',
                    fontSize: '0.75rem',
                    cursor: 'pointer',
                  }}
                >
                  <X size={12} /> Cancel
                </button>
                <button
                  onClick={handleSave}
                  disabled={!draft.trim() || draft.trim() === message.content}
                  style={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: 4,
                    padding: '4px 10px',
                    border: '1px solid var(--color-accent)',
                    background: 'var(--color-accent)',
                    color: '#fff',
                    borderRadius: 'var(--radius-full)',
                    fontSize: '0.75rem',
                    cursor: draft.trim() ? 'pointer' : 'not-allowed',
                    opacity: draft.trim() ? 1 : 0.5,
                  }}
                >
                  Save &amp; regenerate
                </button>
              </div>
            </div>
          ) : (
            <div className={styles.messageText}>
              <MarkdownRenderer content={message.content} />
            </div>
          )}
        </div>

        {/* Action buttons — hidden while editing */}
        {!isEditing && (
          <div
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 4,
            }}
          >
            {onEditSubmit && (
              <button
                onClick={handleStartEdit}
                className={styles.userCopyButton}
                aria-label="Edit message"
                title="Edit message and regenerate"
              >
                <Pencil size={14} aria-hidden="true" />
              </button>
            )}
            <button
              onClick={() => onCopy(message.content, message.id)}
              className={styles.userCopyButton}
              aria-label={copiedMessageId === message.id ? 'Copied' : 'Copy message'}
            >
              {copiedMessageId === message.id ? (
                <Check size={14} aria-hidden="true" />
              ) : (
                <Copy size={14} aria-hidden="true" />
              )}
            </button>
          </div>
        )}
      </div>
    </>
  );
}

export const UserMessage = memo(UserMessageComponent, (prevProps, nextProps) => {
  if (prevProps.message.id !== nextProps.message.id) return false;
  if (prevProps.message.content !== nextProps.message.content) return false;
  const prevIsCopied = prevProps.copiedMessageId === prevProps.message.id;
  const nextIsCopied = nextProps.copiedMessageId === nextProps.message.id;
  if (prevIsCopied !== nextIsCopied) return false;
  if (prevProps.message.attachments?.length !== nextProps.message.attachments?.length) return false;
  if (prevProps.onEditSubmit !== nextProps.onEditSubmit) return false;
  return true;
});

UserMessage.displayName = 'UserMessage';
