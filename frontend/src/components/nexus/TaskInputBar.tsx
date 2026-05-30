import { useState, useRef, useCallback, useEffect } from 'react';
import { ArrowUp, ChevronDown, Check } from 'lucide-react';
import { useModelStore } from '@/store/useModelStore';
import styles from './Nexus.module.css';

interface TaskInputBarProps {
  onSendMessage: (content: string, modelId?: string) => void;
}

export function TaskInputBar({ onSendMessage }: TaskInputBarProps) {
  const [input, setInput] = useState('');
  const [modelOpen, setModelOpen] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);

  const { models, selectedModelId, setSelectedModel, fetchModels } = useModelStore();

  // Fetch models on mount
  useEffect(() => {
    if (models.length === 0) {
      fetchModels();
    }
  }, [models.length, fetchModels]);

  // Close dropdown on outside click
  useEffect(() => {
    if (!modelOpen) return;
    const handler = (e: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setModelOpen(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [modelOpen]);

  const selectedModel = models.find((m) => m.id === selectedModelId);
  const visibleModels = models.filter((m) => m.is_visible !== false);

  const handleSubmit = useCallback(() => {
    const trimmed = input.trim();
    if (!trimmed) return;
    onSendMessage(trimmed, selectedModelId ?? undefined);
    setInput('');
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto';
    }
  }, [input, onSendMessage, selectedModelId]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        handleSubmit();
      }
    },
    [handleSubmit]
  );

  const handleInput = useCallback((e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setInput(e.target.value);
    const el = e.target;
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, 200) + 'px';
  }, []);

  const handleSelectModel = useCallback(
    (modelId: string) => {
      setSelectedModel(modelId);
      setModelOpen(false);
    },
    [setSelectedModel]
  );

  // Group models by provider
  const grouped = visibleModels.reduce<Record<string, typeof visibleModels>>(
    (acc, model) => {
      const key = model.provider_name || 'Other';
      if (!acc[key]) acc[key] = [];
      acc[key].push(model);
      return acc;
    },
    {}
  );

  return (
    <div className={styles.inputSection}>
      <div className={styles.inputContainer}>
        <div className={styles.inputWrapper}>
          {/* Textarea */}
          <textarea
            ref={textareaRef}
            className={styles.textarea}
            value={input}
            onChange={handleInput}
            onKeyDown={handleKeyDown}
            placeholder="Give Dobby a task..."
            rows={1}
          />

          {/* Controls row */}
          <div className={styles.controlsRow}>
            <div className={styles.leftControls}>
              {/* Model selector */}
              <div className={styles.modelSelector} ref={dropdownRef}>
                <button
                  className={styles.modelBtn}
                  onClick={() => setModelOpen(!modelOpen)}
                  title="Select model"
                >
                  {selectedModel?.provider_favicon && (
                    <img
                      src={selectedModel.provider_favicon}
                      alt=""
                      className={styles.modelFavicon}
                    />
                  )}
                  <span className={styles.modelName}>
                    {selectedModel?.display_name || 'Select model'}
                  </span>
                  <ChevronDown
                    size={12}
                    className={modelOpen ? styles.chevronUp : undefined}
                  />
                </button>

                {modelOpen && (
                  <div className={styles.modelDropdown}>
                    {Object.entries(grouped).map(([provider, providerModels]) => (
                      <div key={provider}>
                        <div className={styles.modelProviderLabel}>{provider}</div>
                        {providerModels.map((model) => (
                          <button
                            key={model.id}
                            className={`${styles.modelOption} ${
                              model.id === selectedModelId ? styles.modelOptionSelected : ''
                            }`}
                            onClick={() => handleSelectModel(model.id)}
                          >
                            {model.provider_favicon && (
                              <img
                                src={model.provider_favicon}
                                alt=""
                                className={styles.modelFavicon}
                              />
                            )}
                            <span className={styles.modelOptionName}>
                              {model.display_name}
                            </span>
                            {model.id === selectedModelId && (
                              <Check size={14} className={styles.modelCheck} />
                            )}
                          </button>
                        ))}
                      </div>
                    ))}
                    {visibleModels.length === 0 && (
                      <div className={styles.modelEmpty}>No models available</div>
                    )}
                  </div>
                )}
              </div>
            </div>

            <div className={styles.rightControls}>
              {/* Send button */}
              <button
                className={styles.sendButton}
                onClick={handleSubmit}
                disabled={!input.trim()}
                title="Send task"
              >
                <ArrowUp size={18} />
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
