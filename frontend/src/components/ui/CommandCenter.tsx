import React, {
  useState,
  useRef,
  useEffect,
  useCallback,
  useImperativeHandle,
  forwardRef,
} from 'react';
import type { KeyboardEvent } from 'react';
import {
  Plus,
  ArrowUp,
  ChevronDown,
  X,
  FileText,
  FileSpreadsheet,
  FileJson,
  ChevronRight,
  Image,
  Shield,
  Presentation,
  Mic,
  MicOff,
  Loader2,
  WifiOff,
  AlertCircle,
  LogIn,
} from 'lucide-react';
import { useModelStore } from '@/store/useModelStore';
import { useSettingsStore } from '@/store/useSettingsStore';
import { useIsMobile } from '@/hooks';
import { ModelSelectorModal } from './ModelSelectorModal';
import { BadgeInfoModal, type BadgeType } from './BadgeInfoModal';
import { Tooltip } from '@/components/design-system/Tooltip/Tooltip';
import type { ActivePrompt, PromptAnswer } from '@/types/interactivePrompt';
import { InteractivePromptMessage } from '@/components/chat/InteractivePromptMessage';
import styles from './CommandCenter.module.css';
import { cn } from '@/utils/cn';

export interface Suggestion {
  label: string;
  prompt: string;
  systemInstruction?: string;
  icon?: React.ReactNode;
}

export interface CommandCenterProps {
  /** Layout mode - centered for new chat, bottom for ongoing conversation */
  mode?: 'centered' | 'bottom';
  /** Greeting text shown at top in centered mode */
  greeting?: string;
  /** Callback when message is sent - returns true if successful, false if failed */
  onSendMessage: (
    message: string,
    isDeepThinking: boolean,
    files?: File[],
    systemInstruction?: string
  ) => Promise<boolean>;
  /** Additional CSS class name */
  className?: string;
  /** Placeholder text for input */
  placeholder?: string;
  /** Suggestion pills to display */
  suggestions?: Suggestion[];
  /** Loading state - when true, shows spinner with stop button */
  isLoading?: boolean;
  /** Callback when stop generation is requested */
  onStopGeneration?: () => void;
  /** Whether models are currently loading */
  isLoadingModels?: boolean;
  /** Whether WebSocket is connected */
  isConnected?: boolean;
  /** Whether we've ever connected - don't show reconnecting on initial load */
  hasEverConnected?: boolean;
  /** Initial value to pre-fill the input (controlled externally) */
  initialValue?: string;
  /** Automatically send the initial value when set (e.g., from URL ?prompt=) */
  autoSendInitialValue?: boolean;
  /** Callback when initial value has been consumed */
  onInitialValueConsumed?: () => void;
  /** Active prompt — replaces the input with the step-by-step wizard */
  activePrompt?: ActivePrompt | null;
  /** Callback when prompt answers are submitted */
  onPromptSubmit?: (answers: Record<string, PromptAnswer>) => void;
  /** Callback when prompt is skipped/closed */
  onPromptSkip?: () => void;
  /** Whether the input is disabled (e.g., guest limit reached) */
  disabled?: boolean;
  /** Message shown when input is disabled */
  disabledMessage?: string;
  /** Callback when the disabled input area is clicked (e.g., to navigate to sign-in) */
  onDisabledClick?: () => void;
  /** Whether file uploads are allowed (default: true) */
  allowFileUpload?: boolean;
  /**
   * Optional slot rendered above the textarea. Used by the chat
   * surface to mount the KnowledgeProjectChips picker so RAG
   * knowledge can be attached per-chat without coupling
   * CommandCenter to the chat-specific store.
   */
  belowAttachmentsSlot?: React.ReactNode;
}

/** Ref handle for CommandCenter - allows parent to focus the input */
export interface CommandCenterHandle {
  focus: () => void;
}

// Helper function to get tier color for indicator dot
const getTierColor = (tier: string): string => {
  const colors: Record<string, string> = {
    tier1: '#8b5cf6', // Purple for Elite
    tier2: '#3b82f6', // Blue for Premium
    tier3: '#10b981', // Green for Standard
    tier4: '#f59e0b', // Orange for Fast
    tier5: '#ec4899', // Pink for New
  };
  return colors[tier] || '#6b7280'; // Gray fallback
};

export const CommandCenter = forwardRef<CommandCenterHandle, CommandCenterProps>(
  (
    {
      mode = 'centered',
      greeting = 'Howdy, How can I help you?',
      onSendMessage,
      className = '',
      placeholder = 'How can I help you today?',
      suggestions = [],
      isLoading = false,
      onStopGeneration,
      isLoadingModels = false,
      isConnected = true,
      hasEverConnected = false,
      initialValue,
      autoSendInitialValue = false,
      onInitialValueConsumed,
      activePrompt = null,
      onPromptSubmit,
      onPromptSkip,
      disabled = false,
      disabledMessage,
      onDisabledClick,
      allowFileUpload = true,
      belowAttachmentsSlot,
    },
    ref
  ) => {
    const [message, setMessage] = useState('');
    const [isDropdownOpen, setIsDropdownOpen] = useState(false);
    const [isModalOpen, setIsModalOpen] = useState(false);
    const [isDeepThinking, setIsDeepThinking] = useState(false);
    const [badgeModalOpen, setBadgeModalOpen] = useState(false);
    const [badgeModalType, setBadgeModalType] = useState<BadgeType>('secure');
    const [badgeModalAnchor, setBadgeModalAnchor] = useState<DOMRect | null>(null);
    const [selectedFiles, setSelectedFiles] = useState<File[]>([]);
    const [filePreviewUrls, setFilePreviewUrls] = useState<Record<string, string>>({});
    const [isDragging, setIsDragging] = useState(false);
    const [isRecording, setIsRecording] = useState(false);
    const [isTranscribing, setIsTranscribing] = useState(false);
    const textareaRef = useRef<HTMLTextAreaElement>(null);
    const dropdownRef = useRef<HTMLDivElement>(null);
    const fileInputRef = useRef<HTMLInputElement>(null);
    const mediaRecorderRef = useRef<MediaRecorder | null>(null);
    const audioChunksRef = useRef<Blob[]>([]);

    // Expose focus method to parent components
    useImperativeHandle(ref, () => ({
      focus: () => {
        textareaRef.current?.focus();
      },
    }));

    // Use model store
    const { models, selectedModelId, setSelectedModel, getSelectedModel } = useModelStore();
    const backendSelectedModel = getSelectedModel();

    // Get custom providers from settings
    const { customProviders } = useSettingsStore();

    // Handle initial value pre-fill (e.g., from URL query params)
    useEffect(() => {
      if (initialValue && initialValue.trim()) {
        setMessage(initialValue);

        if (autoSendInitialValue) {
          // Auto-send after a short delay to let WebSocket connect
          const timer = setTimeout(() => {
            onSendMessage(initialValue, false).then(success => {
              if (success) {
                setMessage('');
              }
            });
            onInitialValueConsumed?.();
          }, 500);
          return () => clearTimeout(timer);
        } else {
          // Just focus the textarea
          setTimeout(() => {
            textareaRef.current?.focus();
          }, 100);
          onInitialValueConsumed?.();
        }
      }
    }, [initialValue, autoSendInitialValue, onInitialValueConsumed, onSendMessage]);

    // Combine backend models with custom provider models
    const allModels = React.useMemo(() => {
      const backendModels = [...models];

      // Add models from all enabled custom providers
      customProviders.forEach(provider => {
        if (
          provider.enabled &&
          provider.baseUrl &&
          provider.apiKey &&
          provider.selectedModels.length > 0
        ) {
          provider.selectedModels.forEach(modelId => {
            const customModelId = `custom:${provider.id}:${modelId}`;
            if (!backendModels.some(m => m.id === customModelId)) {
              backendModels.unshift({
                id: customModelId,
                provider_id: 0,
                name: modelId,
                display_name: modelId,
                provider_name: provider.name || 'Custom Provider',
                provider_favicon: '',
                description: `From ${provider.name}`,
                is_visible: true,
                supports_vision: false,
                supports_tools: true,
                supports_streaming: true,
                provider_secure: false,
              });
            }
          });
        }
      });

      return backendModels;
    }, [models, customProviders]);

    // Get the selected model from allModels (includes custom providers)
    const selectedModel = React.useMemo(() => {
      if (!selectedModelId) return null;
      return allModels.find(m => m.id === selectedModelId) || backendSelectedModel;
    }, [selectedModelId, allModels, backendSelectedModel]);

    // Get display name for the model button
    // If model is not found but ID exists, try to extract a name from the ID
    const modelDisplayName = React.useMemo(() => {
      if (selectedModel?.display_name) {
        return selectedModel.display_name;
      }
      // If we have a selectedModelId but no model found, try to parse custom model name
      if (selectedModelId?.startsWith('custom:')) {
        const parts = selectedModelId.split(':');
        if (parts.length >= 3) {
          return parts.slice(2).join(':'); // Return the model name part
        }
      }
      return null;
    }, [selectedModel, selectedModelId]);

    const isCentered = mode === 'centered';
    const isMobile = useIsMobile();

    // Handle badge click to open info modal
    const handleBadgeClick = (e: React.MouseEvent, badgeType: BadgeType) => {
      e.stopPropagation(); // Prevent model selection
      const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
      setBadgeModalAnchor(rect);
      setBadgeModalType(badgeType);
      setBadgeModalOpen(true);
    };

    // Handle model button click - on mobile, go directly to full modal
    const handleModelButtonClick = () => {
      if (isMobile) {
        setIsModalOpen(true);
      } else {
        setIsDropdownOpen(!isDropdownOpen);
      }
    };

    // Close dropdown when clicking outside
    useEffect(() => {
      const handleClickOutside = (event: MouseEvent) => {
        if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
          setIsDropdownOpen(false);
        }
      };

      if (isDropdownOpen) {
        document.addEventListener('mousedown', handleClickOutside);
      }

      return () => {
        document.removeEventListener('mousedown', handleClickOutside);
      };
    }, [isDropdownOpen]);

    // Cleanup preview URLs on unmount
    useEffect(() => {
      return () => {
        Object.values(filePreviewUrls).forEach(url => URL.revokeObjectURL(url));
      };
    }, [filePreviewUrls]);

    // Generate unique file key for tracking previews (handles duplicate filenames)
    const generateFileKey = (file: File, index: number): string => {
      return `${file.name}-${file.size}-${file.lastModified}-${index}`;
    };

    // Process files - shared logic for file picker, paste, and drag-drop
    const processFiles = (fileList: FileList | File[]) => {
      const files = Array.from(fileList);
      const newFiles: File[] = [];
      const newPreviewUrls: Record<string, string> = {};

      // Get current file count to generate unique keys
      const currentFileCount = selectedFiles.length;

      for (let i = 0; i < files.length; i++) {
        const file = files[i];
        // Validate file type
        const isPDF = file.type === 'application/pdf';
        const isDOCX =
          file.type === 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' ||
          file.name.endsWith('.docx');
        const isPPTX =
          file.type ===
            'application/vnd.openxmlformats-officedocument.presentationml.presentation' ||
          file.name.endsWith('.pptx');
        const isImage = file.type.startsWith('image/');
        const isCSV = file.type === 'text/csv' || file.name.endsWith('.csv');
        const isExcel =
          file.type === 'application/vnd.ms-excel' ||
          file.type === 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' ||
          file.name.endsWith('.xlsx') ||
          file.name.endsWith('.xls');
        const isJSON = file.type === 'application/json' || file.name.endsWith('.json');
        const isText = file.type === 'text/plain' || file.name.endsWith('.txt');
        const isDataFile = isCSV || isExcel || isJSON || isText;
        const isDocument = isPDF || isDOCX || isPPTX;

        if (!isDocument && !isImage && !isDataFile) {
          alert(
            `File "${file.name}" is not supported. Allowed types: Images, PDFs, DOCX, PPTX, CSV, Excel, JSON, Text files.`
          );
          continue;
        }

        // Validate file size
        let maxSize: number;
        let fileTypeLabel: string;

        if (isDocument) {
          maxSize = 10 * 1024 * 1024; // 10MB for documents
          fileTypeLabel = 'documents (PDF/DOCX/PPTX)';
        } else if (isDataFile) {
          maxSize = 100 * 1024 * 1024; // 100MB for data files
          fileTypeLabel = 'data files (CSV/Excel/JSON)';
        } else {
          maxSize = 20 * 1024 * 1024; // 20MB for images
          fileTypeLabel = 'images';
        }

        if (file.size > maxSize) {
          const maxSizeMB = maxSize / (1024 * 1024);
          alert(`File "${file.name}" exceeds ${maxSizeMB}MB limit for ${fileTypeLabel}`);
          continue;
        }

        newFiles.push(file);

        // Create preview URL for images using unique key
        if (isImage) {
          const fileKey = generateFileKey(file, currentFileCount + newFiles.length - 1);
          newPreviewUrls[fileKey] = URL.createObjectURL(file);
        }
      }

      setSelectedFiles(prev => [...prev, ...newFiles]);
      setFilePreviewUrls(prev => ({ ...prev, ...newPreviewUrls }));
    };

    // Handle file selection via file picker
    const handleFileSelect = (event: React.ChangeEvent<HTMLInputElement>) => {
      const files = event.target.files;
      if (!files || files.length === 0) return;

      processFiles(files);

      // Reset file input
      if (fileInputRef.current) {
        fileInputRef.current.value = '';
      }
    };

    // Handle paste event (Ctrl+V)
    const handlePaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
      const items = e.clipboardData?.items;
      if (!items) return;

      const files: File[] = [];

      // Extract files from clipboard
      Array.from(items).forEach(item => {
        if (item.kind === 'file') {
          const file = item.getAsFile();
          if (file) {
            // Accept images, PDFs, DOCX, and PPTX
            const isPDF = file.type === 'application/pdf';
            const isDOCX =
              file.type ===
              'application/vnd.openxmlformats-officedocument.wordprocessingml.document';
            const isPPTX =
              file.type ===
              'application/vnd.openxmlformats-officedocument.presentationml.presentation';
            const isImage = file.type.startsWith('image/');
            if (isPDF || isDOCX || isPPTX || isImage) {
              files.push(file);
            }
          }
        }
      });

      if (files.length > 0) {
        e.preventDefault(); // Prevent default paste of file names
        processFiles(files);
      }
      // If no files detected, allow normal text paste
    };

    // Handle drag over event
    const handleDragOver = (e: React.DragEvent<HTMLDivElement>) => {
      e.preventDefault(); // Required to allow drop
      e.stopPropagation();

      // Check if dragging files
      if (e.dataTransfer.types.includes('Files')) {
        setIsDragging(true);
      }
    };

    // Handle drag leave event
    const handleDragLeave = (e: React.DragEvent<HTMLDivElement>) => {
      e.preventDefault();
      e.stopPropagation();

      // Only set to false if leaving the container
      const rect = e.currentTarget.getBoundingClientRect();
      const x = e.clientX;
      const y = e.clientY;

      if (x <= rect.left || x >= rect.right || y <= rect.top || y >= rect.bottom) {
        setIsDragging(false);
      }
    };

    // Handle drop event
    const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
      e.preventDefault();
      e.stopPropagation();
      setIsDragging(false);

      const files = e.dataTransfer.files;
      if (files && files.length > 0) {
        processFiles(files);
      }
    };

    // Remove file
    const handleRemoveFile = (index: number) => {
      setSelectedFiles(prev => {
        const newFiles = [...prev];
        const removedFile = newFiles[index];

        // Revoke preview URL if exists (use the unique file key)
        const fileKey = generateFileKey(removedFile, index);
        if (filePreviewUrls[fileKey]) {
          URL.revokeObjectURL(filePreviewUrls[fileKey]);
          setFilePreviewUrls(prevUrls => {
            const updated = { ...prevUrls };
            delete updated[fileKey];
            return updated;
          });
        }

        newFiles.splice(index, 1);
        return newFiles;
      });
    };

    const handleSend = async () => {
      if (disabled) return;
      if (message.trim() || selectedFiles.length > 0) {
        const success = await onSendMessage(
          message,
          isDeepThinking,
          selectedFiles.length > 0 ? selectedFiles : undefined
        );

        // Only clear input on successful send
        if (success) {
          setMessage('');
          setSelectedFiles([]);
          // Cleanup preview URLs
          Object.values(filePreviewUrls).forEach(url => URL.revokeObjectURL(url));
          setFilePreviewUrls({});
          // Reset textarea height
          if (textareaRef.current) {
            const minHeight = isCentered ? 80 : 48;
            textareaRef.current.style.height = minHeight + 'px';
          }
        }
        // On failure: message stays in input, user can see error and retry
      }
    };

    const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
      if (e.key === 'Enter' && !e.shiftKey && !isMobile) {
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
          const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:3001';
          const response = await fetch(`${apiBaseUrl}/api/audio/transcribe`, {
            method: 'POST',
            body: formData,
          });

          if (!response.ok) {
            const error = await response.json().catch(() => ({ error: 'Transcription failed' }));
            throw new Error(error.error || 'Transcription failed');
          }

          const result = await response.json();
          if (result.text) {
            // Auto-send the transcribed message immediately
            const success = await onSendMessage(
              result.text,
              isDeepThinking,
              selectedFiles.length > 0 ? selectedFiles : undefined
            );
            // Clear only on successful send
            if (success) {
              setMessage('');
              setSelectedFiles([]);
              // Cleanup preview URLs
              Object.values(filePreviewUrls).forEach(url => URL.revokeObjectURL(url));
              setFilePreviewUrls({});
            }
          }
        } catch (error) {
          console.error('Transcription error:', error);
          alert(error instanceof Error ? error.message : 'Failed to transcribe audio');
        } finally {
          setIsTranscribing(false);
        }
      },
      [onSendMessage, isDeepThinking, selectedFiles, filePreviewUrls]
    );

    const startRecording = useCallback(async () => {
      try {
        const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
        const mediaRecorder = new MediaRecorder(stream, {
          mimeType: MediaRecorder.isTypeSupported('audio/webm') ? 'audio/webm' : 'audio/mp4',
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

    return (
      <div
        className={cn(styles.container, isCentered ? styles.centered : styles.bottom, className)}
      >
        {/* Greeting - Only shown in centered mode */}
        {isCentered && greeting && (
          <div className={styles.greeting}>
            <span style={{ marginRight: 'var(--space-3)' }}>🌸</span>
            {greeting}
          </div>
        )}

        {/* Content Container */}
        <div className={styles.contentWrapper}>
          {/* Interactive Prompt — replaces input when active */}
          {activePrompt ? (
            <InteractivePromptMessage
              prompt={activePrompt}
              onSubmit={answers => onPromptSubmit?.(answers)}
              onSkip={() => onPromptSkip?.()}
            />
          ) : (
            /* Input Box with Controls Inside */
            <div
              onDragOver={handleDragOver}
              onDragLeave={handleDragLeave}
              onDrop={handleDrop}
              className={cn(styles.inputWrapper, isDragging && styles.dragging)}
            >
              {/* Connection Status Banner */}
              {!isConnected && (
                <div className={styles.connectionBanner}>
                  <WifiOff size={14} />
                  <span>
                    {hasEverConnected ? 'Reconnecting to server...' : 'Connecting to server...'}
                  </span>
                </div>
              )}

              {/* Models Loading Banner */}
              {isConnected && isLoadingModels && !selectedModelId && (
                <div className={styles.loadingBanner}>
                  <Loader2 size={14} className={styles.spinIcon} />
                  <span>Loading models...</span>
                </div>
              )}

              {/* File Preview Grid */}
              {selectedFiles.length > 0 && (
                <div className={styles.filePreviewArea}>
                  {selectedFiles.map((file, index) => {
                    const isPDF = file.type === 'application/pdf';
                    const isDOCX =
                      file.type ===
                        'application/vnd.openxmlformats-officedocument.wordprocessingml.document' ||
                      file.name.endsWith('.docx');
                    const isPPTX =
                      file.type ===
                        'application/vnd.openxmlformats-officedocument.presentationml.presentation' ||
                      file.name.endsWith('.pptx');
                    const isImage = file.type.startsWith('image/');
                    const isCSV = file.type === 'text/csv' || file.name.endsWith('.csv');
                    const isExcel =
                      file.type === 'application/vnd.ms-excel' ||
                      file.type ===
                        'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' ||
                      file.name.endsWith('.xlsx') ||
                      file.name.endsWith('.xls');
                    const isJSON = file.type === 'application/json' || file.name.endsWith('.json');
                    const isText = file.type === 'text/plain' || file.name.endsWith('.txt');
                    const isDataFile = isCSV || isExcel || isJSON || isText;
                    const isDocument = isPDF || isDOCX || isPPTX;

                    // Get appropriate icon and label for data files
                    const getDataFileIcon = () => {
                      if (isJSON) return <FileJson size={24} color="var(--color-text-secondary)" />;
                      if (isCSV || isExcel)
                        return <FileSpreadsheet size={24} color="var(--color-text-secondary)" />;
                      return <FileText size={24} color="var(--color-text-secondary)" />;
                    };

                    const getDataFileLabel = () => {
                      if (isJSON) return 'JSON';
                      if (isCSV) return 'CSV';
                      if (isExcel) return 'Excel';
                      if (isText) return 'TXT';
                      return 'Data';
                    };

                    // Get appropriate icon and label for document files
                    const getDocumentIcon = () => {
                      if (isPPTX)
                        return <Presentation size={24} color="var(--color-text-secondary)" />;
                      return <FileText size={24} color="var(--color-text-secondary)" />;
                    };

                    const getDocumentLabel = () => {
                      if (isPDF) return 'PDF';
                      if (isDOCX) return 'DOCX';
                      if (isPPTX) return 'PPTX';
                      return 'Doc';
                    };

                    return (
                      <div key={`${file.name}-${index}`} className={styles.filePreviewItem}>
                        {/* Remove Button */}
                        <button
                          onClick={() => handleRemoveFile(index)}
                          className={styles.removeFileButton}
                        >
                          <X size={10} />
                        </button>

                        {/* Image Preview */}
                        {isImage && filePreviewUrls[generateFileKey(file, index)] && (
                          <img
                            src={filePreviewUrls[generateFileKey(file, index)]}
                            alt={file.name}
                            className={styles.filePreviewImage}
                          />
                        )}

                        {/* Document Preview (PDF, DOCX, PPTX) */}
                        {isDocument && (
                          <div className={styles.filePreviewPdf}>
                            {getDocumentIcon()}
                            <div
                              style={{
                                fontSize: '8px',
                                color: 'var(--color-text-secondary)',
                                marginTop: '2px',
                                overflow: 'hidden',
                                textOverflow: 'ellipsis',
                                whiteSpace: 'nowrap',
                                width: '100%',
                              }}
                            >
                              {getDocumentLabel()}
                            </div>
                          </div>
                        )}

                        {/* Data File Preview (CSV, Excel, JSON, Text) */}
                        {isDataFile && (
                          <div className={styles.filePreviewPdf}>
                            {getDataFileIcon()}
                            <div
                              style={{
                                fontSize: '8px',
                                color: 'var(--color-text-secondary)',
                                marginTop: '2px',
                                overflow: 'hidden',
                                textOverflow: 'ellipsis',
                                whiteSpace: 'nowrap',
                                width: '100%',
                              }}
                            >
                              {getDataFileLabel()}
                            </div>
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}

              {/* Hidden File Input */}
              <input
                ref={fileInputRef}
                type="file"
                accept="image/*,.pdf,.docx,.pptx,.csv,.xlsx,.xls,.json,.txt"
                multiple
                onChange={handleFileSelect}
                style={{ display: 'none' }}
              />

              {/* Optional slot — chat surface mounts the knowledge
                  project picker here. Rendered between attachments
                  and the textarea so chips sit just above the input
                  the user is currently typing into. */}
              {belowAttachmentsSlot}

              {disabled ? (
                <button
                  onClick={onDisabledClick}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    gap: '0.5rem',
                    width: '100%',
                    minHeight: isCentered ? '80px' : '48px',
                    padding: '0.75rem 1rem',
                    background:
                      'linear-gradient(135deg, rgba(99, 102, 241, 0.08) 0%, rgba(168, 85, 247, 0.08) 100%)',
                    border: '1px solid rgba(99, 102, 241, 0.25)',
                    borderRadius: '8px',
                    color: '#6366f1',
                    fontSize: '0.9375rem',
                    fontWeight: 500,
                    cursor: 'pointer',
                    transition: 'all 0.2s ease',
                  }}
                  onMouseEnter={e => {
                    e.currentTarget.style.background =
                      'linear-gradient(135deg, rgba(99, 102, 241, 0.15) 0%, rgba(168, 85, 247, 0.15) 100%)';
                    e.currentTarget.style.borderColor = 'rgba(99, 102, 241, 0.4)';
                  }}
                  onMouseLeave={e => {
                    e.currentTarget.style.background =
                      'linear-gradient(135deg, rgba(99, 102, 241, 0.08) 0%, rgba(168, 85, 247, 0.08) 100%)';
                    e.currentTarget.style.borderColor = 'rgba(99, 102, 241, 0.25)';
                  }}
                >
                  <LogIn size={18} />
                  {disabledMessage ?? 'Sign in to continue'}
                </button>
              ) : (
                <textarea
                  ref={textareaRef}
                  value={message}
                  onChange={e => {
                    setMessage(e.target.value);
                    // Auto-resize textarea
                    const minHeight = isCentered ? 80 : 48;
                    e.target.style.height = minHeight + 'px';
                    e.target.style.height = Math.max(minHeight, e.target.scrollHeight) + 'px';
                  }}
                  onKeyDown={handleKeyDown}
                  onPaste={handlePaste}
                  placeholder={placeholder}
                  rows={1}
                  className={cn(styles.textarea, 'hide-scrollbar')}
                  style={{
                    minHeight: isCentered ? '80px' : '48px',
                    maxHeight: '200px',
                  }}
                />
              )}

              {/* Controls Row - Inside bordered container (hidden when disabled) */}
              {!disabled && (
                <div className={styles.controlsRow}>
                  {/* Left Buttons */}
                  <div className={styles.leftControls}>
                    {/* Add Documents Button */}
                    {allowFileUpload && (
                      <Tooltip content="Add files (images, PDF, DOCX, PPTX, etc.)" position="top">
                        <button
                          onClick={() => fileInputRef.current?.click()}
                          className={styles.iconButton}
                        >
                          <Plus size={18} strokeWidth={2} />
                        </button>
                      </Tooltip>
                    )}

                    {/* Voice Input Button */}
                    <Tooltip
                      content={
                        isTranscribing
                          ? 'Transcribing...'
                          : isRecording
                            ? 'Click to stop recording'
                            : 'Voice input'
                      }
                      position="top"
                    >
                      <button
                        onClick={toggleRecording}
                        disabled={isTranscribing}
                        className={cn(
                          styles.iconButton,
                          isRecording && styles.recording,
                          isTranscribing && styles.transcribing
                        )}
                      >
                        {isTranscribing ? (
                          <Loader2 size={18} strokeWidth={2} className={styles.spin} />
                        ) : isRecording ? (
                          <MicOff size={18} strokeWidth={2} />
                        ) : (
                          <Mic size={18} strokeWidth={2} />
                        )}
                      </button>
                    </Tooltip>
                  </div>

                  {/* Right Controls */}
                  <div className={styles.rightControls}>
                    {/* Model Selector - Custom Dropdown (desktop) or direct Modal (mobile) */}
                    <div ref={dropdownRef} className={styles.modelSelector}>
                      <button onClick={handleModelButtonClick} className={styles.modelButton}>
                        <span>{modelDisplayName || 'Select Model'}</span>
                        <ChevronDown
                          size={14}
                          style={{
                            transition: 'transform var(--transition-fast)',
                            transform: isDropdownOpen ? 'rotate(180deg)' : 'rotate(0deg)',
                          }}
                        />
                      </button>

                      {/* Dropdown Menu - Only shown on desktop */}
                      {isDropdownOpen && !isMobile && (
                        <>
                          <div
                            className={styles.dropdownBackdrop}
                            onClick={() => setIsDropdownOpen(false)}
                          />
                          <div className={styles.dropdownMenu}>
                            {allModels.length === 0 ? (
                              <div
                                style={{
                                  padding: 'var(--space-3)',
                                  color: 'var(--color-text-secondary)',
                                  fontSize: 'var(--text-sm)',
                                  textAlign: 'center',
                                }}
                              >
                                Loading models...
                              </div>
                            ) : (
                              <>
                                {/* Scrollable Model List */}
                                <div className={styles.dropdownModelList}>
                                  {/* Show all visible models - tiered models first, then others */}
                                  {(() => {
                                    // Filter to visible backend models
                                    const eligibleModels = allModels.filter(
                                      model =>
                                        model.is_visible !== false &&
                                        !model.id.startsWith('custom:') // Exclude custom provider models from quick list
                                    );

                                    // Separate tiered and non-tiered models
                                    const tieredModels = eligibleModels.filter(
                                      m =>
                                        m.recommendation_tier &&
                                        typeof m.recommendation_tier === 'object'
                                    );
                                    const nonTieredModels = eligibleModels.filter(
                                      m =>
                                        !m.recommendation_tier ||
                                        typeof m.recommendation_tier !== 'object'
                                    );

                                    // Sort tiered models by tier priority: tier1 > tier2 > tier3 > tier4 > tier5
                                    const tierPriority: Record<string, number> = {
                                      tier1: 0,
                                      tier2: 1,
                                      tier3: 2,
                                      tier4: 3,
                                      tier5: 4,
                                    };

                                    const sortedTiered = [...tieredModels].sort((a, b) => {
                                      if (!a.recommendation_tier || !b.recommendation_tier)
                                        return 0;
                                      const aTier =
                                        typeof a.recommendation_tier === 'object'
                                          ? a.recommendation_tier.tier
                                          : '';
                                      const bTier =
                                        typeof b.recommendation_tier === 'object'
                                          ? b.recommendation_tier.tier
                                          : '';
                                      return (
                                        (tierPriority[aTier] || 999) - (tierPriority[bTier] || 999)
                                      );
                                    });

                                    // Show tiered models in the quick dropdown, or fall back
                                    // to first 5 models when no tiers are configured
                                    const displayModels =
                                      sortedTiered.length > 0
                                        ? sortedTiered
                                        : eligibleModels.slice(0, 5);

                                    return displayModels;
                                  })().map(model => (
                                    <button
                                      key={model.id}
                                      onClick={() => {
                                        setSelectedModel(model.id);
                                        setIsDropdownOpen(false);
                                      }}
                                      className={cn(
                                        styles.dropdownItem,
                                        styles.featuredItem,
                                        selectedModel?.id === model.id && styles.selected
                                      )}
                                    >
                                      {model.provider_favicon && (
                                        <img
                                          src={model.provider_favicon}
                                          alt={model.provider_name}
                                          className={styles.providerIcon}
                                          onError={e => {
                                            (e.target as HTMLImageElement).style.display = 'none';
                                          }}
                                        />
                                      )}
                                      <div className={styles.dropdownItemContent}>
                                        <div className={styles.dropdownItemNameRow}>
                                          <span className={styles.dropdownItemName}>
                                            {model.display_name}
                                          </span>
                                          <div className={styles.dropdownItemBadges}>
                                            {model.supports_vision && (
                                              <span
                                                className={styles.badge}
                                                onClick={e => handleBadgeClick(e, 'vision')}
                                              >
                                                <Image size={10} />
                                              </span>
                                            )}
                                            {model.provider_secure && (
                                              <span
                                                className={`${styles.badge} ${styles.secureBadge}`}
                                                onClick={e => handleBadgeClick(e, 'secure')}
                                              >
                                                <Shield size={10} />
                                              </span>
                                            )}
                                          </div>
                                        </div>
                                        {(model.description ||
                                          (model.recommendation_tier &&
                                            typeof model.recommendation_tier === 'object' &&
                                            model.recommendation_tier.description)) && (
                                          <div className={styles.dropdownItemDescription}>
                                            {model.description ||
                                              (model.recommendation_tier &&
                                              typeof model.recommendation_tier === 'object'
                                                ? model.recommendation_tier.description
                                                : '')}
                                          </div>
                                        )}
                                      </div>
                                    </button>
                                  ))}
                                </div>

                                {/* More Models Option - Sticky at bottom */}
                                <button
                                  onClick={() => {
                                    setIsDropdownOpen(false);
                                    setIsModalOpen(true);
                                  }}
                                  className={cn(styles.dropdownItem, styles.moreModelsItem)}
                                >
                                  <span>More models</span>
                                  <ChevronRight size={16} />
                                </button>
                              </>
                            )}
                          </div>
                        </>
                      )}
                    </div>

                    {/* Send Button / Loading Spinner with Stop Button */}
                    {isLoading ? (
                      <Tooltip content="Stop generation" position="top">
                        <button
                          onClick={onStopGeneration}
                          className={styles.sendButton}
                          style={{
                            background: 'var(--color-surface-elevated)',
                            color: 'var(--color-text-primary)',
                          }}
                        >
                          {/* Spinning circle */}
                          <svg
                            width="20"
                            height="20"
                            viewBox="0 0 36 36"
                            style={{
                              animation: 'spin 1s linear infinite',
                            }}
                          >
                            <circle
                              cx="18"
                              cy="18"
                              r="14"
                              fill="none"
                              stroke="var(--color-accent)"
                              strokeWidth="4"
                              strokeDasharray="87.96"
                              strokeDashoffset="22"
                              strokeLinecap="round"
                            />
                          </svg>
                        </button>
                      </Tooltip>
                    ) : !isConnected ? (
                      // WebSocket not connected - show connecting/reconnecting state
                      <Tooltip
                        content={
                          hasEverConnected ? 'Reconnecting to server...' : 'Connecting to server...'
                        }
                        position="top"
                      >
                        <button
                          disabled
                          className={cn(styles.sendButton, styles.sendButtonDisconnected)}
                        >
                          <WifiOff size={18} strokeWidth={2.5} />
                        </button>
                      </Tooltip>
                    ) : isLoadingModels && !selectedModelId ? (
                      // Models loading - show loading state
                      <Tooltip content="Loading models..." position="top">
                        <button
                          disabled
                          className={cn(styles.sendButton, styles.sendButtonLoading)}
                        >
                          <Loader2 size={18} strokeWidth={2.5} className={styles.spinIcon} />
                        </button>
                      </Tooltip>
                    ) : !selectedModelId ? (
                      // No model selected - show warning
                      <Tooltip content="Select a model to send" position="top">
                        <button
                          onClick={handleSend}
                          disabled={!message.trim()}
                          className={cn(styles.sendButton, styles.sendButtonWarning)}
                        >
                          <AlertCircle size={18} strokeWidth={2.5} />
                        </button>
                      </Tooltip>
                    ) : (
                      // Ready to send
                      <Tooltip content="Send message" position="top">
                        <button
                          onClick={handleSend}
                          disabled={!message.trim() && selectedFiles.length === 0}
                          className={styles.sendButton}
                        >
                          <ArrowUp size={18} strokeWidth={2.5} />
                        </button>
                      </Tooltip>
                    )}
                  </div>
                </div>
              )}
            </div>
          )}

          {/* Suggestion Pills - Only in centered mode */}
          {isCentered && suggestions.length > 0 && (
            <div className={styles.suggestionsContainer}>
              {suggestions.map(suggestion => (
                <button
                  key={suggestion.label}
                  onClick={() =>
                    onSendMessage(
                      suggestion.prompt,
                      isDeepThinking,
                      selectedFiles.length > 0 ? selectedFiles : undefined,
                      suggestion.systemInstruction
                    )
                  }
                  className={styles.suggestionPill}
                >
                  {suggestion.icon && (
                    <span className={styles.suggestionIcon}>{suggestion.icon}</span>
                  )}
                  {suggestion.label}
                </button>
              ))}
            </div>
          )}
        </div>

        {/* Model Selector Modal */}
        <ModelSelectorModal
          isOpen={isModalOpen}
          onClose={() => setIsModalOpen(false)}
          onSelectModel={modelId => {
            setSelectedModel(modelId);
          }}
          currentModelId={selectedModel?.id || null}
        />

        {/* Badge Info Modal */}
        <BadgeInfoModal
          isOpen={badgeModalOpen}
          onClose={() => setBadgeModalOpen(false)}
          badgeType={badgeModalType}
          anchorRect={badgeModalAnchor}
        />
      </div>
    );
  }
);
