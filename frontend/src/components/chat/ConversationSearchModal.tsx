import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Search, X, Loader2, Star } from 'lucide-react';
import { api } from '@/services/api';

/**
 * Full-text search across the user's conversation history.
 *
 * Encryption-aware: the backend decrypts each chat in memory and scans for
 * the query — so latency is O(n_chats), not O(1). For a typical user
 * (tens to hundreds of chats) this is fast enough; the modal shows a spinner
 * while the request is in flight.
 */

interface ChatSearchHit {
  chat_id: string;
  title: string;
  is_starred: boolean;
  updated_at: string;
  snippet: string;
  match_field: 'title' | 'message';
  message_id?: string;
  message_role?: string;
  message_index?: number;
}

interface ChatSearchResponse {
  query: string;
  hits: ChatSearchHit[];
  total: number;
}

interface ConversationSearchModalProps {
  isOpen: boolean;
  onClose: () => void;
  /** Called when the user picks a hit. Receives the chat id (and msg if relevant). */
  onSelect: (chatId: string, messageId?: string) => void;
}

const DEBOUNCE_MS = 220;

export function ConversationSearchModal({
  isOpen,
  onClose,
  onSelect,
}: ConversationSearchModalProps) {
  const [query, setQuery] = useState('');
  const [hits, setHits] = useState<ChatSearchHit[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selectedIdx, setSelectedIdx] = useState(0);

  const inputRef = useRef<HTMLInputElement>(null);
  // Latest-call tracking: out-of-order responses from a flapping query get
  // dropped so the UI doesn't flash stale results.
  const fetchSeq = useRef(0);

  // Focus the input + reset state when opened.
  useEffect(() => {
    if (isOpen) {
      setQuery('');
      setHits([]);
      setError(null);
      setSelectedIdx(0);
      // Defer focus so the modal is in the DOM
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [isOpen]);

  // Debounced fetch on query change. <2 chars = clear, no request.
  useEffect(() => {
    if (!isOpen) return;
    const q = query.trim();
    if (q.length < 2) {
      setHits([]);
      setError(null);
      setLoading(false);
      return;
    }
    setLoading(true);
    const seq = ++fetchSeq.current;
    const timer = window.setTimeout(async () => {
      try {
        const res = await api.get<ChatSearchResponse>(
          `/api/chats/search?q=${encodeURIComponent(q)}&limit=25`
        );
        if (seq !== fetchSeq.current) return;
        setHits(res.hits);
        setSelectedIdx(0);
        setError(null);
      } catch (e) {
        if (seq !== fetchSeq.current) return;
        setError(e instanceof Error ? e.message : 'Search failed');
        setHits([]);
      } finally {
        if (seq === fetchSeq.current) setLoading(false);
      }
    }, DEBOUNCE_MS);
    return () => window.clearTimeout(timer);
  }, [query, isOpen]);

  // Keyboard nav within the modal.
  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLDivElement>) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
        return;
      }
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setSelectedIdx(i => Math.min(hits.length - 1, i + 1));
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setSelectedIdx(i => Math.max(0, i - 1));
      } else if (e.key === 'Enter' && hits[selectedIdx]) {
        e.preventDefault();
        const hit = hits[selectedIdx];
        onSelect(hit.chat_id, hit.message_id);
        onClose();
      }
    },
    [hits, selectedIdx, onClose, onSelect]
  );

  const emptyState = useMemo(() => {
    if (loading) return null;
    if (error) return error;
    if (query.trim().length < 2) return 'Type at least 2 characters to search…';
    if (hits.length === 0) return `No conversations match “${query}”.`;
    return null;
  }, [loading, error, query, hits.length]);

  if (!isOpen) return null;

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Search conversations"
      onKeyDown={handleKeyDown}
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0, 0, 0, 0.55)',
        backdropFilter: 'blur(4px)',
        zIndex: 1000,
        display: 'flex',
        alignItems: 'flex-start',
        justifyContent: 'center',
        paddingTop: '12vh',
      }}
      onClick={onClose}
    >
      <div
        onClick={e => e.stopPropagation()}
        style={{
          width: 'min(620px, 92vw)',
          maxHeight: '72vh',
          display: 'flex',
          flexDirection: 'column',
          background: 'var(--color-surface)',
          border: '1px solid var(--color-border)',
          borderRadius: 'var(--radius-2xl)',
          boxShadow: 'var(--shadow-2xl)',
          overflow: 'hidden',
        }}
      >
        {/* Input */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 10,
            padding: '14px 16px',
            borderBottom: '1px solid var(--color-border)',
          }}
        >
          <Search size={18} aria-hidden style={{ color: 'var(--color-text-secondary)' }} />
          <input
            ref={inputRef}
            type="text"
            value={query}
            onChange={e => setQuery(e.target.value)}
            placeholder="Search conversations…"
            style={{
              flex: 1,
              background: 'transparent',
              border: 'none',
              outline: 'none',
              fontSize: '1rem',
              color: 'var(--color-text-primary)',
              fontFamily: 'inherit',
            }}
          />
          {loading && <Loader2 size={16} className="animate-spin" />}
          <button
            onClick={onClose}
            aria-label="Close search"
            style={{
              background: 'transparent',
              border: 'none',
              color: 'var(--color-text-secondary)',
              cursor: 'pointer',
              padding: 4,
              display: 'flex',
            }}
          >
            <X size={16} />
          </button>
        </div>

        {/* Results */}
        <div
          style={{
            overflowY: 'auto',
            flex: 1,
          }}
        >
          {emptyState && (
            <div
              style={{
                padding: '40px 20px',
                color: 'var(--color-text-secondary)',
                fontSize: '0.9rem',
                textAlign: 'center',
              }}
            >
              {emptyState}
            </div>
          )}
          {hits.map((hit, i) => {
            const isSelected = i === selectedIdx;
            return (
              <button
                key={`${hit.chat_id}-${hit.message_id ?? 'title'}`}
                onClick={() => {
                  onSelect(hit.chat_id, hit.message_id);
                  onClose();
                }}
                onMouseEnter={() => setSelectedIdx(i)}
                style={{
                  display: 'block',
                  width: '100%',
                  textAlign: 'left',
                  padding: '12px 16px',
                  border: 'none',
                  background: isSelected
                    ? 'var(--color-surface-hover)'
                    : 'transparent',
                  cursor: 'pointer',
                  borderLeft: isSelected
                    ? '3px solid var(--color-accent)'
                    : '3px solid transparent',
                  transition: 'background var(--transition-fast)',
                }}
              >
                <div
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 8,
                    marginBottom: 4,
                  }}
                >
                  {hit.is_starred && (
                    <Star
                      size={12}
                      fill="currentColor"
                      style={{ color: 'var(--color-warning)' }}
                    />
                  )}
                  <span
                    style={{
                      fontWeight: 600,
                      color: 'var(--color-text-primary)',
                      fontSize: '0.9rem',
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {hit.title || 'Untitled'}
                  </span>
                  {hit.match_field === 'message' && hit.message_role && (
                    <span
                      style={{
                        fontSize: '0.7rem',
                        padding: '1px 6px',
                        borderRadius: 'var(--radius-full)',
                        background: 'var(--color-surface-hover)',
                        color: 'var(--color-text-tertiary)',
                        textTransform: 'capitalize',
                      }}
                    >
                      {hit.message_role}
                    </span>
                  )}
                </div>
                <div
                  style={{
                    fontSize: '0.82rem',
                    color: 'var(--color-text-secondary)',
                    lineHeight: 1.4,
                    overflow: 'hidden',
                    display: '-webkit-box',
                    WebkitLineClamp: 2,
                    WebkitBoxOrient: 'vertical',
                  }}
                >
                  {hit.snippet}
                </div>
              </button>
            );
          })}
        </div>

        {/* Footer hint */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '8px 16px',
            borderTop: '1px solid var(--color-border)',
            fontSize: '0.7rem',
            color: 'var(--color-text-tertiary)',
          }}
        >
          <span>↑↓ navigate · ↵ open · Esc close</span>
          {hits.length > 0 && (
            <span>
              {hits.length} match{hits.length === 1 ? '' : 'es'}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}
