/**
 * BadgeInfoModal Component
 *
 * A small modal that appears when clicking on model badges (shield, image, tier dots)
 * to explain what each badge means.
 */

import { useEffect, useRef } from 'react';
import { X, Shield, Image, Sparkles, Zap, Star, FlaskConical } from 'lucide-react';
import styles from './BadgeInfoModal.module.css';

export type BadgeType = 'secure' | 'vision' | 'top' | 'medium' | 'fastest' | 'new';

interface BadgeInfoModalProps {
  isOpen: boolean;
  onClose: () => void;
  badgeType: BadgeType;
  anchorRect?: DOMRect | null;
}

const badgeInfo: Record<
  BadgeType,
  { icon: React.ReactNode; title: string; description: string; color: string }
> = {
  secure: {
    icon: <Shield size={20} />,
    title: 'Secure Provider',
    description:
      "This model runs on a provider that either doesn't store your data or operates in Trusted Execution Environments (TEEs). Your conversations are more private and secure.",
    color: 'var(--color-success, #22c55e)',
  },
  vision: {
    icon: <Image size={20} />,
    title: 'Vision Capable',
    description:
      'This model can understand and analyze images. You can upload pictures and ask questions about them, or include images in your conversations.',
    color: 'var(--color-accent)',
  },
  top: {
    icon: <Star size={20} />,
    title: 'Top Tier Model',
    description:
      'The most capable model from this provider. Best for complex reasoning, creative tasks, and when you need the highest quality responses.',
    color: '#f59e0b',
  },
  medium: {
    icon: <Sparkles size={20} />,
    title: 'Balanced Model',
    description:
      'A well-balanced model offering good performance at a reasonable cost. Great for everyday tasks and general conversations.',
    color: '#8b5cf6',
  },
  fastest: {
    icon: <Zap size={20} />,
    title: 'Fastest Model',
    description:
      'Optimized for speed and efficiency. Best for quick responses, simple tasks, and when low latency matters most.',
    color: '#06b6d4',
  },
  new: {
    icon: <FlaskConical size={20} />,
    title: 'Newly Added',
    description:
      'A recently added model to ClaraVerse. Try it out and explore its capabilities - it might become your new favorite!',
    color: '#10b981',
  },
};

export function BadgeInfoModal({ isOpen, onClose, badgeType, anchorRect }: BadgeInfoModalProps) {
  const modalRef = useRef<HTMLDivElement>(null);
  const info = badgeInfo[badgeType];

  // Close on escape key
  useEffect(() => {
    const handleEscape = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    if (isOpen) {
      document.addEventListener('keydown', handleEscape);
      return () => document.removeEventListener('keydown', handleEscape);
    }
  }, [isOpen, onClose]);

  // Close when clicking outside
  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (modalRef.current && !modalRef.current.contains(e.target as Node)) {
        onClose();
      }
    };
    if (isOpen) {
      // Delay to prevent immediate close from the same click that opened it
      setTimeout(() => {
        document.addEventListener('mousedown', handleClickOutside);
      }, 0);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [isOpen, onClose]);

  if (!isOpen) return null;

  // Calculate position based on anchor element
  let modalStyle: React.CSSProperties = {};
  if (anchorRect) {
    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;
    const modalWidth = 280;
    const modalHeight = 180;

    let top = anchorRect.bottom + 8;
    let left = anchorRect.left + anchorRect.width / 2 - modalWidth / 2;

    // Adjust if going off right edge
    if (left + modalWidth > viewportWidth - 16) {
      left = viewportWidth - modalWidth - 16;
    }
    // Adjust if going off left edge
    if (left < 16) {
      left = 16;
    }
    // Show above if going off bottom
    if (top + modalHeight > viewportHeight - 16) {
      top = anchorRect.top - modalHeight - 8;
    }

    modalStyle = {
      position: 'fixed',
      top: `${top}px`,
      left: `${left}px`,
    };
  }

  return (
    <div className={styles.overlay} onClick={onClose}>
      <div
        ref={modalRef}
        className={styles.modal}
        style={modalStyle}
        onClick={e => e.stopPropagation()}
      >
        <button onClick={onClose} className={styles.closeButton} aria-label="Close">
          <X size={14} />
        </button>
        <div className={styles.content}>
          <div className={styles.iconWrapper} style={{ color: info.color }}>
            {info.icon}
          </div>
          <h3 className={styles.title}>{info.title}</h3>
          <p className={styles.description}>{info.description}</p>
        </div>
      </div>
    </div>
  );
}
