/**
 * KnowledgeProjectChips — the chip row + project picker that lives
 * above the chat textarea.
 *
 * Lets the user attach 0..N projects to the current chat. When at
 * least one is attached, the backend injects search_knowledge as a
 * per-turn tool scoped to those projects' Qdrant collections.
 *
 * Two visual states:
 *   1. Nothing selected → just shows a small "+ Add knowledge" button.
 *   2. Some selected   → chips for each project + "+ more" button.
 *
 * The picker is a small popover with a checkbox per project. Plain
 * div + outside-click handler, no Radix — minimal dependency
 * footprint and matches the look of the rest of the chat surface.
 */
import { useCallback, useEffect, useRef, useState } from 'react';
import { BookOpen, Plus, X } from 'lucide-react';
import type { NexusProject } from '@/types/nexus';

interface Props {
  /** All projects the user has access to. Loaded by the parent. */
  projects: NexusProject[];
  /** Currently-selected project IDs (subset of projects). */
  selectedIds: string[];
  /** Called whenever the user adds/removes a project. */
  onChange: (ids: string[]) => void;
  /** Disables the picker (e.g. while messages are being sent). */
  disabled?: boolean;
}

export function KnowledgeProjectChips({ projects, selectedIds, onChange, disabled }: Props) {
  const [pickerOpen, setPickerOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  // Close picker on outside click. Cheap pattern, no library needed.
  useEffect(() => {
    if (!pickerOpen) return;
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setPickerOpen(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [pickerOpen]);

  const toggle = useCallback(
    (projectId: string) => {
      if (selectedIds.includes(projectId)) {
        onChange(selectedIds.filter(id => id !== projectId));
      } else {
        onChange([...selectedIds, projectId]);
      }
    },
    [selectedIds, onChange]
  );

  const remove = useCallback(
    (projectId: string) => onChange(selectedIds.filter(id => id !== projectId)),
    [selectedIds, onChange]
  );

  // Resolve selected IDs → project objects in selection order.
  // Drop dangling IDs (project deleted) so the chip row stays clean.
  const selected = selectedIds
    .map(id => projects.find(p => p.id === id))
    .filter((p): p is NexusProject => Boolean(p));

  return (
    <div ref={containerRef} style={containerStyle}>
      {selected.map(p => (
        <div key={p.id} style={chipStyle} title={`Searching ${p.name}'s knowledge base`}>
          <BookOpen size={12} style={{ flexShrink: 0 }} />
          <span style={chipLabelStyle}>{p.name}</span>
          {!disabled && (
            <button
              type="button"
              onClick={() => remove(p.id)}
              style={removeBtnStyle}
              aria-label={`Remove ${p.name}`}
            >
              <X size={12} />
            </button>
          )}
        </div>
      ))}

      <button
        type="button"
        onClick={() => setPickerOpen(o => !o)}
        disabled={disabled}
        style={{
          ...addBtnStyle,
          opacity: disabled ? 0.4 : 1,
          cursor: disabled ? 'not-allowed' : 'pointer',
        }}
        title={
          projects.length === 0
            ? 'Create a project in Nexus to attach its knowledge base'
            : 'Attach a project knowledge base to this chat'
        }
      >
        <Plus size={12} />
        <span>{selected.length === 0 ? 'Knowledge' : 'Add'}</span>
      </button>

      {pickerOpen && (
        <div style={popoverStyle} onClick={e => e.stopPropagation()}>
          <div style={popoverHeaderStyle}>
            <span style={{ fontSize: 12, fontWeight: 600 }}>Attach project knowledge</span>
            <span style={{ fontSize: 11, color: 'var(--color-text-tertiary)' }}>
              {selected.length} of {projects.length} selected
            </span>
          </div>
          <div style={popoverListStyle}>
            {projects.length === 0 ? (
              <div style={emptyStateStyle}>
                No projects yet. Create one in Nexus and upload files to its Knowledge tab.
              </div>
            ) : (
              projects.map(p => {
                const isOn = selectedIds.includes(p.id);
                return (
                  <button
                    key={p.id}
                    type="button"
                    onClick={() => toggle(p.id)}
                    style={{
                      ...popoverItemStyle,
                      background: isOn ? 'var(--color-accent-light)' : 'transparent',
                      color: isOn ? 'var(--color-accent)' : 'var(--color-text-primary)',
                    }}
                  >
                    <input
                      type="checkbox"
                      checked={isOn}
                      readOnly
                      style={{ marginRight: 8, pointerEvents: 'none' }}
                    />
                    <BookOpen size={13} style={{ marginRight: 8, opacity: 0.7 }} />
                    <span style={{ flex: 1, textAlign: 'left', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {p.name}
                    </span>
                  </button>
                );
              })
            )}
          </div>
          {projects.length > 0 && (
            <div style={popoverFooterStyle}>
              <button type="button" onClick={() => onChange([])} style={footerBtnStyle}>
                Clear
              </button>
              <button type="button" onClick={() => setPickerOpen(false)} style={footerBtnStyle}>
                Done
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// ─ Inline styles (matches the look of the other chat-input controls)

const containerStyle: React.CSSProperties = {
  position: 'relative',
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: 6,
  padding: '6px 8px 0',
  minHeight: 28,
};

const chipStyle: React.CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 6,
  padding: '3px 6px 3px 8px',
  borderRadius: 999,
  background: 'var(--color-accent-light)',
  color: 'var(--color-accent)',
  border: '1px solid var(--color-accent-border)',
  fontSize: 11,
  fontWeight: 500,
  lineHeight: 1.2,
  maxWidth: 220,
};

const chipLabelStyle: React.CSSProperties = {
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
};

const removeBtnStyle: React.CSSProperties = {
  background: 'transparent',
  border: 'none',
  color: 'inherit',
  cursor: 'pointer',
  padding: 0,
  display: 'inline-flex',
  alignItems: 'center',
  opacity: 0.7,
};

const addBtnStyle: React.CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 4,
  padding: '4px 8px',
  borderRadius: 999,
  background: 'transparent',
  color: 'var(--color-text-tertiary)',
  border: '1px dashed var(--color-border)',
  fontSize: 11,
  fontWeight: 500,
};

const popoverStyle: React.CSSProperties = {
  position: 'absolute',
  bottom: 'calc(100% + 6px)',
  left: 8,
  // Stack above the centered-mode greeting overlay (which sits at
  // z-index ~50 from CommandCenter). 9999 is overkill on purpose —
  // any future floating element below this should still lose.
  zIndex: 9999,
  width: 280,
  maxHeight: 320,
  // SOLID background. The original `var(--color-surface-elevated)`
  // is rgba(255,255,255,0.05) in the dark theme — mostly
  // transparent, so the greeting text bled through the popover.
  // The solid color matches the chat input panel's elevated look
  // without relying on translucency.
  background: '#181818',
  border: '1px solid var(--color-border)',
  borderRadius: 10,
  boxShadow: '0 16px 48px rgba(0,0,0,0.7), 0 2px 8px rgba(0,0,0,0.5)',
  display: 'flex',
  flexDirection: 'column',
  overflow: 'hidden',
};

const popoverHeaderStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  padding: '10px 12px',
  borderBottom: '1px solid var(--color-border)',
};

const popoverListStyle: React.CSSProperties = {
  overflowY: 'auto',
  flex: 1,
  padding: 4,
  maxHeight: 220,
};

const popoverItemStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  width: '100%',
  padding: '6px 8px',
  borderRadius: 6,
  border: 'none',
  fontSize: 13,
  cursor: 'pointer',
};

const popoverFooterStyle: React.CSSProperties = {
  display: 'flex',
  justifyContent: 'flex-end',
  gap: 6,
  padding: 6,
  borderTop: '1px solid var(--color-border)',
};

const footerBtnStyle: React.CSSProperties = {
  background: 'transparent',
  border: 'none',
  color: 'var(--color-text-secondary)',
  fontSize: 12,
  fontWeight: 500,
  cursor: 'pointer',
  padding: '4px 8px',
  borderRadius: 4,
};

const emptyStateStyle: React.CSSProperties = {
  padding: 16,
  fontSize: 12,
  color: 'var(--color-text-tertiary)',
  textAlign: 'center',
  lineHeight: 1.4,
};
