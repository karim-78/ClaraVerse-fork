import { useState, useEffect } from 'react';
import type { KeyboardEvent } from 'react';
import styles from './NameInputModal.module.css';

interface NameInputModalProps {
  isOpen: boolean;
  onSubmit: (name: string) => void;
}

export const NameInputModal: React.FC<NameInputModalProps> = ({ isOpen, onSubmit }) => {
  const [name, setName] = useState('');
  const [error, setError] = useState('');

  useEffect(() => {
    if (isOpen) {
      // Reset state when modal opens
      setName('');
      setError('');
    }
  }, [isOpen]);

  const handleSubmit = () => {
    const trimmedName = name.trim();

    if (!trimmedName) {
      setError('Please enter your name');
      return;
    }

    if (trimmedName.length < 2) {
      setError('Name should be at least 2 characters');
      return;
    }

    if (trimmedName.length > 50) {
      setError('Name is too long (max 50 characters)');
      return;
    }

    onSubmit(trimmedName);
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      handleSubmit();
    }
  };

  if (!isOpen) return null;

  return (
    <div className={styles.overlay}>
      <div className={styles.modal}>
        <h1 className={styles.title}>Welcome to ClaraVerse Chat</h1>

        <p className={styles.description}>Before we start, what should we call you?</p>

        <div className={styles.inputContainer}>
          <input
            type="text"
            value={name}
            onChange={e => {
              setName(e.target.value);
              setError('');
            }}
            onKeyDown={handleKeyDown}
            placeholder="Enter your name"
            className={styles.input}
            autoFocus
            maxLength={50}
          />
          {error && <span className={styles.error}>{error}</span>}
        </div>

        <button onClick={handleSubmit} className={styles.button}>
          Let's Go!
        </button>

        <p className={styles.note}>We'll use this to personalize your experience</p>
      </div>
    </div>
  );
};
