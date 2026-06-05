import { memo, useState, useCallback, useEffect, useRef, useMemo } from 'react';
import { createPortal } from 'react-dom';
import {
  ChevronDown,
  ChevronRight,
  ChevronUp,
  Check,
  Search,
  ArrowUp,
  Calendar,
  Bot,
  Code,
  Globe,
  PenTool,
  BarChart3,
  Brain,
  Folder,
  Send,
  Settings2,
  X,
  FileEdit,
  Mic,
  MicOff,
  Loader2,
  Bookmark,
} from 'lucide-react';
import { useModelStore } from '@/store/useModelStore';
import { useClawStore } from '@/store/useClawStore';
import { useNexusStore } from '@/store/useNexusStore';
import { useSkillStore } from '@/store/useSkillStore';
import { nexusService } from '@/services/nexusService';
import type { NexusTask } from '@/types/nexus';
import styles from './Nexus.module.css';

const TEMPLATE_ICON_MAP: Record<string, React.ComponentType<{ size?: number }>> = {
  code: Code,
  search: Search,
  globe: Globe,
  'pen-tool': PenTool,
  'bar-chart-3': BarChart3,
  bot: Bot,
  brain: Brain,
  send: Send,
};

interface CreateTaskOpts {
  content: string;
  initialStatus?: 'draft' | 'pending';
  modelId?: string;
  tools?: string[];
  skillIds?: string[];
  daemonMode?: 'auto' | 'quick' | 'daemon' | 'multi_daemon';
  templateId?: string;
  priority?: number;
  scheduledAt?: string;
  projectId?: string;
  saveIds?: string[];
}

interface FloatingTaskBarProps {
  projectId?: string;
  onCreateTask: (opts: CreateTaskOpts) => void;
  onSendFollowUp?: (content: string, taskId: string) => void;
  followUpTask?: NexusTask | null;
  onClearFollowUp?: () => void;
  detailPanelOpen?: boolean;
}

type DaemonMode = 'auto' | 'quick' | 'daemon' | 'multi_daemon';

const DAEMON_MODES: { value: DaemonMode; label: string }[] = [
  { value: 'auto', label: 'Auto' },
  { value: 'quick', label: 'Quick' },
  { value: 'daemon', label: 'Daemon' },
  { value: 'multi_daemon', label: 'Multi' },
];

const PRIORITIES: { value: number; label: string }[] = [
  { value: 0, label: 'Low' },
  { value: 1, label: 'Normal' },
  { value: 2, label: 'High' },
];

export const FloatingTaskBar = memo(function FloatingTaskBar({
  projectId: externalProjectId,
  onCreateTask,
  onSendFollowUp,
  followUpTask,
  onClearFollowUp,
  detailPanelOpen,
}: FloatingTaskBarProps) {
  // Expanded/collapsed
  const [expanded, setExpanded] = useState(false);

  // Submit mode: Tab cycles between 'queue' and 'draft'
  const [submitMode, setSubmitMode] = useState<'queue' | 'draft'>('queue');

  // Form state
  const [prompt, setPrompt] = useState('');
  const [daemonMode, setDaemonMode] = useState<DaemonMode>('auto');
  const [selectedTemplateId, setSelectedTemplateId] = useState<string | null>(null);
  const [priority, setPriority] = useState(1);
  const [selectedTools, setSelectedTools] = useState<Set<string>>(new Set());
  const [scheduleEnabled, setScheduleEnabled] = useState(false);
  const [scheduledAt, setScheduledAt] = useState('');
  const [projectId, setProjectId] = useState<string | undefined>(externalProjectId);

  // Model dropdown
  const [modelOpen, setModelOpen] = useState(false);
  const modelDropdownRef = useRef<HTMLDivElement>(null);
  const modelDropdownListRef = useRef<HTMLDivElement>(null);
  const modelBtnRef = useRef<HTMLButtonElement>(null);
  const [modelDropdownPos, setModelDropdownPos] = useState<{ bottom: number; left: number } | null>(
    null
  );

  // Tool picker
  const [toolSearch, setToolSearch] = useState('');
  const [expandedCategories, setExpandedCategories] = useState<Set<string>>(new Set());

  // Skill picker
  const [selectedSkillIds, setSelectedSkillIds] = useState<Set<string>>(new Set());
  const [skillsExpanded, setSkillsExpanded] = useState(false);

  // Save attachment picker
  const [selectedSaveIds, setSelectedSaveIds] = useState<Set<string>>(new Set());
  const [savesExpanded, setSavesExpanded] = useState(false);

  // Refs
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const barRef = useRef<HTMLDivElement>(null);

  // Voice input
  const [isRecording, setIsRecording] = useState(false);
  const [isTranscribing, setIsTranscribing] = useState(false);
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const audioChunksRef = useRef<Blob[]>([]);

  // Dynamic centering: center within the kanban area, not the full viewport
  const [barPosition, setBarPosition] = useState<{ left: string; transform: string } | null>(null);

  useEffect(() => {
    if (!barRef.current) return;

    const mainEl = barRef.current.parentElement;
    if (!mainEl) return;
    const layout = mainEl.querySelector('[class*="commandCenterLayout"]');
    if (!layout) return;

    const computePosition = () => {
      // The kanban area is the first child (.main) of commandCenterLayout
      const kanbanArea = layout.firstElementChild as HTMLElement | null;
      if (!kanbanArea) return;

      const rect = kanbanArea.getBoundingClientRect();
      const center = rect.left + rect.width / 2;
      setBarPosition({ left: `${center}px`, transform: 'translateX(-50%)' });
    };

    computePosition();

    // Observe resize to recompute when detail panel opens/closes
    const observer = new ResizeObserver(computePosition);
    observer.observe(layout);
    if (layout.firstElementChild) {
      observer.observe(layout.firstElementChild);
    }

    window.addEventListener('resize', computePosition);
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', computePosition);
    };
  }, [detailPanelOpen]);

  // Sync external projectId
  useEffect(() => {
    setProjectId(externalProjectId);
  }, [externalProjectId]);

  // Stores
  const { models, selectedModelId, setSelectedModel, fetchModels } = useModelStore();
  const { toolCategories, toolsLoading, fetchTools } = useClawStore();
  const daemonTemplates = useNexusStore(s => s.daemonTemplates);
  const setDaemonTemplates = useNexusStore(s => s.setDaemonTemplates);
  const projects = useNexusStore(s => s.projects);
  const tasks = useNexusStore(s => s.tasks);
  const saves = useNexusStore(s => s.saves);
  const {
    skills: allSkills,
    enabledSkillIds,
    fetchSkills: fetchAllSkills,
    fetchMySkills,
  } = useSkillStore();

  // Fetch on mount
  useEffect(() => {
    if (models.length === 0) fetchModels();
  }, [models.length, fetchModels]);

  useEffect(() => {
    if (toolCategories.length === 0) fetchTools();
  }, [toolCategories.length, fetchTools]);

  useEffect(() => {
    if (daemonTemplates.length === 0) {
      nexusService.listDaemonTemplates().then(setDaemonTemplates).catch(console.error);
    }
  }, [daemonTemplates.length, setDaemonTemplates]);

  useEffect(() => {
    fetchAllSkills();
    fetchMySkills();
  }, [fetchAllSkills, fetchMySkills]);

  // Collapse advanced panel when entering follow-up mode
  useEffect(() => {
    if (followUpTask) {
      setExpanded(false);
    }
  }, [followUpTask]);

  // Close model dropdown on outside click
  useEffect(() => {
    if (!modelOpen) return;
    const handler = (e: MouseEvent) => {
      const target = e.target as Node;
      const inBtn = modelDropdownRef.current?.contains(target);
      const inList = modelDropdownListRef.current?.contains(target);
      if (!inBtn && !inList) {
        setModelOpen(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [modelOpen]);

  // "/" keyboard shortcut to focus bar
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (
        e.key === '/' &&
        !e.ctrlKey &&
        !e.metaKey &&
        !e.altKey &&
        !(e.target instanceof HTMLInputElement) &&
        !(e.target instanceof HTMLTextAreaElement) &&
        !(e.target instanceof HTMLSelectElement)
      ) {
        e.preventDefault();
        textareaRef.current?.focus();
      }
    };
    document.addEventListener('keydown', handler);
    return () => document.removeEventListener('keydown', handler);
  }, []);

  // Ref declared early; assigned after handleSubmit is defined below
  const handleSubmitRef = useRef<(modeOverride?: 'queue' | 'draft') => void>(() => {});

  // Shift+Enter submits with current mode, Enter adds newline, Escape collapses/blurs
  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (e.key === 'Tab' && !e.shiftKey && !e.ctrlKey && !e.metaKey) {
        e.preventDefault();
        setSubmitMode(prev => (prev === 'queue' ? 'draft' : 'queue'));
      } else if (e.key === 'Enter' && e.shiftKey) {
        e.preventDefault();
        handleSubmitRef.current();
      } else if (e.key === 'Escape') {
        if (expanded) {
          setExpanded(false);
        } else {
          textareaRef.current?.blur();
        }
      }
    },
    [expanded]
  );

  const activeTemplates = useMemo(
    () => daemonTemplates.filter(t => t.is_active !== false),
    [daemonTemplates]
  );

  const selectedModel = models.find(m => m.id === selectedModelId);
  const visibleModels = models.filter(m => m.is_visible !== false);

  const grouped = useMemo(
    () =>
      visibleModels.reduce<Record<string, typeof visibleModels>>((acc, model) => {
        const key = model.provider_name || 'Other';
        if (!acc[key]) acc[key] = [];
        acc[key].push(model);
        return acc;
      }, {}),
    [visibleModels]
  );

  const filteredCategories = useMemo(() => {
    if (!toolSearch.trim()) return toolCategories;
    const q = toolSearch.toLowerCase();
    return toolCategories
      .map(cat => ({
        ...cat,
        tools: cat.tools.filter(
          t =>
            t.display_name.toLowerCase().includes(q) ||
            t.name.toLowerCase().includes(q) ||
            t.description?.toLowerCase().includes(q)
        ),
      }))
      .filter(cat => cat.tools.length > 0);
  }, [toolCategories, toolSearch]);

  const toggleCategory = useCallback((catName: string) => {
    setExpandedCategories(prev => {
      const next = new Set(prev);
      if (next.has(catName)) next.delete(catName);
      else next.add(catName);
      return next;
    });
  }, []);

  const enabledSkills = useMemo(
    () => allSkills.filter(s => enabledSkillIds.has(s.id)),
    [allSkills, enabledSkillIds]
  );

  const toggleSkill = useCallback((skillId: string) => {
    setSelectedSkillIds(prev => {
      const next = new Set(prev);
      if (next.has(skillId)) next.delete(skillId);
      else next.add(skillId);
      return next;
    });
  }, []);

  const toggleSave = useCallback((saveId: string) => {
    setSelectedSaveIds(prev => {
      const next = new Set(prev);
      if (next.has(saveId)) next.delete(saveId);
      else next.add(saveId);
      return next;
    });
  }, []);

  const toggleTool = useCallback((toolName: string) => {
    setSelectedTools(prev => {
      const next = new Set(prev);
      if (next.has(toolName)) next.delete(toolName);
      else next.add(toolName);
      return next;
    });
  }, []);

  // Determine if we're in follow-up mode
  const isFollowUpMode = !!followUpTask && !!onSendFollowUp;

  const handleSubmit = useCallback(
    (modeOverride?: 'queue' | 'draft') => {
      const trimmed = prompt.trim();
      if (!trimmed) return;

      const effectiveMode = modeOverride ?? submitMode;

      if (isFollowUpMode && followUpTask) {
        // Send as follow-up to the tagged task
        onSendFollowUp!(trimmed, followUpTask.id);
      } else {
        // Create new task
        onCreateTask({
          content: trimmed,
          initialStatus: effectiveMode === 'draft' ? 'draft' : undefined,
          modelId: selectedModelId ?? undefined,
          tools: selectedTools.size > 0 ? Array.from(selectedTools) : undefined,
          skillIds: selectedSkillIds.size > 0 ? Array.from(selectedSkillIds) : undefined,
          daemonMode: selectedTemplateId
            ? undefined
            : daemonMode !== 'auto'
              ? daemonMode
              : undefined,
          templateId: selectedTemplateId ?? undefined,
          priority: priority !== 1 ? priority : undefined,
          scheduledAt: scheduleEnabled && scheduledAt ? scheduledAt : undefined,
          projectId,
          saveIds: selectedSaveIds.size > 0 ? Array.from(selectedSaveIds) : undefined,
        });

        // Reset form (keep model selection)
        setDaemonMode('auto');
        setSelectedTemplateId(null);
        setPriority(1);
        setSelectedTools(new Set());
        setScheduleEnabled(false);
        setScheduledAt('');
        setSelectedSkillIds(new Set());
        setSelectedSaveIds(new Set());
        setExpanded(false);
        setSubmitMode('queue');
      }

      // Always clear prompt text
      setPrompt('');
      if (textareaRef.current) {
        textareaRef.current.style.height = 'auto';
      }
    },
    [
      prompt,
      isFollowUpMode,
      followUpTask,
      onSendFollowUp,
      submitMode,
      selectedModelId,
      selectedTools,
      selectedSkillIds,
      selectedSaveIds,
      daemonMode,
      selectedTemplateId,
      priority,
      scheduleEnabled,
      scheduledAt,
      projectId,
      onCreateTask,
    ]
  );

  // Keep ref in sync so handleKeyDown always calls the latest handleSubmit
  handleSubmitRef.current = handleSubmit;

  // ── Voice input functions ──────────────────────────────────────────
  // Ref to directly submit transcribed text with current mode settings
  const voiceSubmitRef = useRef<(text: string) => void>(() => {});
  voiceSubmitRef.current = (text: string) => {
    if (!text.trim()) return;

    if (isFollowUpMode && followUpTask) {
      onSendFollowUp!(text.trim(), followUpTask.id);
    } else {
      onCreateTask({
        content: text.trim(),
        initialStatus: submitMode === 'draft' ? 'draft' : undefined,
        modelId: selectedModelId ?? undefined,
        tools: selectedTools.size > 0 ? Array.from(selectedTools) : undefined,
        skillIds: selectedSkillIds.size > 0 ? Array.from(selectedSkillIds) : undefined,
        daemonMode: selectedTemplateId ? undefined : daemonMode !== 'auto' ? daemonMode : undefined,
        templateId: selectedTemplateId ?? undefined,
        priority: priority !== 1 ? priority : undefined,
        scheduledAt: scheduleEnabled && scheduledAt ? scheduledAt : undefined,
        projectId,
        saveIds: selectedSaveIds.size > 0 ? Array.from(selectedSaveIds) : undefined,
      });

      setDaemonMode('auto');
      setSelectedTemplateId(null);
      setPriority(1);
      setSelectedTools(new Set());
      setScheduleEnabled(false);
      setScheduledAt('');
      setSelectedSkillIds(new Set());
      setSelectedSaveIds(new Set());
      setExpanded(false);
      setSubmitMode('queue');
    }

    setPrompt('');
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto';
    }
  };

  const transcribeAudio = useCallback(async (audioBlob: Blob) => {
    setIsTranscribing(true);
    try {
      const formData = new FormData();
      formData.append('file', audioBlob, 'recording.webm');

      const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || window.location.origin;
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
        voiceSubmitRef.current(result.text);
      }
    } catch (error) {
      console.error('Transcription error:', error);
      alert(error instanceof Error ? error.message : 'Failed to transcribe audio');
    } finally {
      setIsTranscribing(false);
    }
  }, []);

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

  // Cleanup recorder on unmount
  useEffect(() => {
    return () => {
      if (mediaRecorderRef.current && mediaRecorderRef.current.state !== 'inactive') {
        mediaRecorderRef.current.stop();
      }
    };
  }, []);

  const toggleExpanded = useCallback(() => {
    setExpanded(prev => !prev);
  }, []);

  const collapse = useCallback(() => {
    setExpanded(false);
  }, []);

  // Status chip: show current project name + model + running task count
  const currentProject = projects.find(p => p.id === projectId);
  const runningCount = useMemo(() => {
    if (!projectId) return 0;
    return tasks.filter(
      t => t.project_id === projectId && (t.status === 'executing' || t.status === 'waiting_input')
    ).length;
  }, [tasks, projectId]);

  // Build status label (project + running count — model shown separately in toolbar)
  const statusParts: string[] = [];
  if (currentProject) statusParts.push(currentProject.name);
  if (runningCount > 0) statusParts.push(`${runningCount} running`);
  const statusLabel = statusParts.join(' \u00b7 ');

  // Count active settings (non-default)
  const activeSettingsCount = useMemo(() => {
    let count = 0;
    if (selectedTemplateId) count++;
    if (daemonMode !== 'auto') count++;
    if (priority !== 1) count++;
    if (selectedTools.size > 0) count++;
    if (selectedSkillIds.size > 0) count++;
    if (selectedSaveIds.size > 0) count++;
    if (scheduleEnabled) count++;
    return count;
  }, [
    selectedTemplateId,
    daemonMode,
    priority,
    selectedTools,
    selectedSkillIds,
    selectedSaveIds,
    scheduleEnabled,
  ]);

  return (
    <>
      {expanded && (
        <div className={styles.floatingBarBackdrop} onClick={collapse} aria-hidden="true" />
      )}
      <div
        ref={barRef}
        className={`${styles.floatingBar} ${expanded ? styles.floatingBarExpanded : ''} ${isFollowUpMode && !expanded ? styles.floatingBarWithContext : ''}`}
        style={
          barPosition ? { left: barPosition.left, transform: barPosition.transform } : undefined
        }
      >
        {expanded && (
          <div className={styles.floatingBarSettings}>
            {/* Tools — hidden when template is selected */}
            {!selectedTemplateId && (
              <div className={styles.formGroup}>
                <label className={styles.formLabel}>
                  Tools {selectedTools.size > 0 && `(${selectedTools.size})`}
                </label>
                <div className={styles.toolPicker}>
                  <div className={styles.toolSearchRow}>
                    <Search size={12} />
                    <input
                      className={styles.toolSearchInput}
                      value={toolSearch}
                      onChange={e => setToolSearch(e.target.value)}
                      placeholder="Search tools..."
                    />
                  </div>
                  <div className={styles.toolList}>
                    {toolsLoading ? (
                      <div className={styles.toolEmpty}>Loading tools...</div>
                    ) : filteredCategories.length === 0 ? (
                      <div className={styles.toolEmpty}>No tools found</div>
                    ) : (
                      filteredCategories.map(cat => (
                        <div key={cat.name}>
                          <button
                            className={styles.toolCategoryBtn}
                            onClick={() => toggleCategory(cat.name)}
                          >
                            {expandedCategories.has(cat.name) ? (
                              <ChevronDown size={12} />
                            ) : (
                              <ChevronRight size={12} />
                            )}
                            <span>{cat.name}</span>
                            <span className={styles.toolCategoryCount}>{cat.tools.length}</span>
                          </button>
                          {expandedCategories.has(cat.name) &&
                            cat.tools.map(tool => (
                              <label key={tool.name} className={styles.toolItem}>
                                <input
                                  type="checkbox"
                                  checked={selectedTools.has(tool.name)}
                                  onChange={() => toggleTool(tool.name)}
                                />
                                <span className={styles.toolItemName}>
                                  {tool.display_name || tool.name}
                                </span>
                              </label>
                            ))}
                        </div>
                      ))
                    )}
                  </div>
                </div>
              </div>
            )}

            {/* Skills */}
            {enabledSkills.length > 0 && (
              <div className={styles.formGroup}>
                <label className={styles.formLabel}>
                  Skills {selectedSkillIds.size > 0 && `(${selectedSkillIds.size})`}
                </label>
                <div className={styles.templateChips}>
                  {(skillsExpanded ? enabledSkills : enabledSkills.slice(0, 6)).map(skill => (
                    <button
                      key={skill.id}
                      className={`${styles.templateChip} ${selectedSkillIds.has(skill.id) ? styles.templateChipActive : ''}`}
                      onClick={() => toggleSkill(skill.id)}
                      type="button"
                      title={skill.description}
                    >
                      {skill.name}
                    </button>
                  ))}
                  {enabledSkills.length > 6 && (
                    <button
                      className={styles.templateChip}
                      onClick={() => setSkillsExpanded(!skillsExpanded)}
                      type="button"
                    >
                      {skillsExpanded ? 'Show less' : `+${enabledSkills.length - 6} more`}
                    </button>
                  )}
                </div>
              </div>
            )}

            {/* Saves — attach as context */}
            {saves.length > 0 && (
              <div className={styles.formGroup}>
                <label className={styles.formLabel}>
                  <Bookmark size={12} /> Saves{' '}
                  {selectedSaveIds.size > 0 && `(${selectedSaveIds.size})`}
                </label>
                <div className={styles.templateChips}>
                  {(savesExpanded ? saves : saves.slice(0, 6)).map(save => (
                    <button
                      key={save.id}
                      className={`${styles.templateChip} ${selectedSaveIds.has(save.id) ? styles.templateChipActive : ''}`}
                      onClick={() => toggleSave(save.id)}
                      type="button"
                      title={save.content.slice(0, 120)}
                    >
                      {save.title.length > 24 ? save.title.slice(0, 24) + '...' : save.title}
                    </button>
                  ))}
                  {saves.length > 6 && (
                    <button
                      className={styles.templateChip}
                      onClick={() => setSavesExpanded(!savesExpanded)}
                      type="button"
                    >
                      {savesExpanded ? 'Show less' : `+${saves.length - 6} more`}
                    </button>
                  )}
                </div>
              </div>
            )}

            {/* Daemon Template */}
            <div className={styles.formGroup}>
              <label className={styles.formLabel}>Daemon</label>
              <div className={styles.templateChips}>
                <button
                  className={`${styles.templateChip} ${!selectedTemplateId ? styles.templateChipActive : ''}`}
                  onClick={() => setSelectedTemplateId(null)}
                  type="button"
                >
                  Auto
                </button>
                {activeTemplates.map(tmpl => {
                  const Icon = TEMPLATE_ICON_MAP[tmpl.icon] ?? Bot;
                  return (
                    <button
                      key={tmpl.id}
                      className={`${styles.templateChip} ${selectedTemplateId === tmpl.id ? styles.templateChipActive : ''}`}
                      onClick={() => setSelectedTemplateId(tmpl.id)}
                      type="button"
                      title={tmpl.description}
                    >
                      <Icon size={12} />
                      {tmpl.name}
                    </button>
                  );
                })}
              </div>
            </div>

            {/* Mode — only when no template */}
            {!selectedTemplateId && (
              <div className={styles.formGroup}>
                <label className={styles.formLabel}>Mode</label>
                <div className={styles.segmentedControl}>
                  {DAEMON_MODES.map(mode => (
                    <button
                      key={mode.value}
                      className={`${styles.segmentedOption} ${
                        daemonMode === mode.value ? styles.segmentedOptionActive : ''
                      }`}
                      onClick={() => setDaemonMode(mode.value)}
                      type="button"
                    >
                      {mode.label}
                    </button>
                  ))}
                </div>
              </div>
            )}

            {/* Priority */}
            <div className={styles.formGroup}>
              <label className={styles.formLabel}>Priority</label>
              <div className={styles.segmentedControl}>
                {PRIORITIES.map(p => (
                  <button
                    key={p.value}
                    className={`${styles.segmentedOption} ${
                      priority === p.value ? styles.segmentedOptionActive : ''
                    }`}
                    onClick={() => setPriority(p.value)}
                    type="button"
                  >
                    {p.label}
                  </button>
                ))}
              </div>
            </div>

            {/* Project */}
            {projects.length > 1 && (
              <div className={styles.formGroup}>
                <label className={styles.formLabel}>
                  <Folder size={12} /> Project
                </label>
                <select
                  className={styles.formSelect}
                  value={projectId ?? ''}
                  onChange={e => setProjectId(e.target.value || undefined)}
                >
                  {projects.map(p => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </div>
            )}

            {/* Schedule */}
            <div className={styles.formGroup}>
              <label className={styles.formLabel}>Schedule</label>
              <div className={styles.scheduleToggle}>
                <button
                  className={`${styles.segmentedOption} ${!scheduleEnabled ? styles.segmentedOptionActive : ''}`}
                  onClick={() => setScheduleEnabled(false)}
                  type="button"
                >
                  Run Now
                </button>
                <button
                  className={`${styles.segmentedOption} ${scheduleEnabled ? styles.segmentedOptionActive : ''}`}
                  onClick={() => setScheduleEnabled(true)}
                  type="button"
                >
                  <Calendar size={12} />
                  Schedule
                </button>
              </div>
              {scheduleEnabled && (
                <input
                  type="datetime-local"
                  className={styles.formDateInput}
                  value={scheduledAt}
                  onChange={e => setScheduledAt(e.target.value)}
                />
              )}
            </div>
          </div>
        )}

        {/* Context pill — when following up on a task */}
        {isFollowUpMode && followUpTask && (
          <div className={styles.floatingBarContext}>
            <span className={styles.floatingBarContextPill}>
              <span className={styles.floatingBarContextLabel}>
                {(followUpTask.goal || followUpTask.prompt || '').slice(0, 50)}
                {(followUpTask.goal || followUpTask.prompt || '').length > 50 ? '...' : ''}
              </span>
              <button
                className={styles.floatingBarContextDismiss}
                onClick={e => {
                  e.stopPropagation();
                  onClearFollowUp?.();
                }}
                title="Clear follow-up context"
                type="button"
              >
                <X size={10} />
              </button>
            </span>
          </div>
        )}

        {/* Textarea — always visible */}
        <div className={styles.floatingBarInput}>
          <textarea
            ref={textareaRef}
            className={styles.floatingBarTextarea}
            value={prompt}
            onChange={e => {
              setPrompt(e.target.value);
              e.target.style.height = 'auto';
              e.target.style.height = Math.min(e.target.scrollHeight, 120) + 'px';
            }}
            onKeyDown={handleKeyDown}
            placeholder={
              isFollowUpMode
                ? followUpTask?.status === 'waiting_input'
                  ? 'Respond to daemon... (Shift+Enter to send)'
                  : 'Follow up on this task... (Shift+Enter to send)'
                : 'Describe a task... (Shift+Enter to send)'
            }
            rows={1}
          />
        </div>

        {/* Toolbar row — always visible */}
        <div className={styles.floatingBarToolbar}>
          {!isFollowUpMode && (
            <button
              className={`${styles.floatingBarToolBtn} ${expanded ? styles.floatingBarToolBtnActive : ''}`}
              onClick={toggleExpanded}
              title={expanded ? 'Hide advanced settings' : 'Advanced settings'}
              type="button"
            >
              <Settings2 size={14} />
              <span>Advanced</span>
              {activeSettingsCount > 0 && (
                <span className={styles.floatingBarBadge}>{activeSettingsCount}</span>
              )}
              {expanded ? <ChevronDown size={12} /> : <ChevronUp size={12} />}
            </button>
          )}

          {/* Model selector — inline in toolbar */}
          <div className={styles.floatingBarModelPicker} ref={modelDropdownRef}>
            <button
              ref={modelBtnRef}
              className={styles.floatingBarToolBtn}
              onClick={() => {
                if (!modelOpen && modelBtnRef.current) {
                  const rect = modelBtnRef.current.getBoundingClientRect();
                  setModelDropdownPos({
                    bottom: window.innerHeight - rect.top + 6,
                    left: rect.left,
                  });
                }
                setModelOpen(!modelOpen);
              }}
              type="button"
              title="Select model"
            >
              {selectedModel?.provider_favicon && (
                <img
                  src={selectedModel.provider_favicon}
                  alt=""
                  className={styles.floatingBarModelFavicon}
                />
              )}
              <span>{selectedModel?.display_name || 'Model'}</span>
              <ChevronDown size={10} />
            </button>
          </div>
          {modelOpen &&
            modelDropdownPos &&
            createPortal(
              <div
                ref={modelDropdownListRef}
                className={styles.floatingBarModelDropdown}
                style={{
                  position: 'fixed',
                  bottom: modelDropdownPos.bottom,
                  left: modelDropdownPos.left,
                }}
              >
                {Object.entries(grouped).map(([provider, providerModels]) => (
                  <div key={provider}>
                    <div className={styles.modelProviderLabel}>{provider}</div>
                    {providerModels.map(model => (
                      <button
                        key={model.id}
                        className={`${styles.formDropdownItem} ${
                          model.id === selectedModelId ? styles.formDropdownItemActive : ''
                        }`}
                        onClick={() => {
                          setSelectedModel(model.id);
                          setModelOpen(false);
                        }}
                      >
                        {model.provider_favicon && (
                          <img
                            src={model.provider_favicon}
                            alt=""
                            className={styles.modelFavicon}
                          />
                        )}
                        <span>{model.display_name}</span>
                        {model.id === selectedModelId && <Check size={14} />}
                      </button>
                    ))}
                  </div>
                ))}
                {visibleModels.length === 0 && (
                  <div className={styles.formDropdownEmpty}>No models available</div>
                )}
              </div>,
              document.body
            )}

          {statusLabel && (
            <span className={styles.floatingBarStatus} title={statusLabel}>
              {statusLabel}
            </span>
          )}

          {!isFollowUpMode && (
            <button
              className={`${styles.floatingBarModeToggle} ${submitMode === 'draft' ? styles.floatingBarModeToggleDraft : ''}`}
              onClick={() => setSubmitMode(prev => (prev === 'queue' ? 'draft' : 'queue'))}
              title="Tab to toggle (Queue / Draft)"
              type="button"
            >
              {submitMode === 'queue' ? (
                <>
                  <ArrowUp size={10} /> Queue
                </>
              ) : (
                <>
                  <FileEdit size={10} /> Draft
                </>
              )}
            </button>
          )}

          <div className={styles.floatingBarToolbarRight}>
            {/* Desktop: both buttons visible. Mobile: voice when empty, send when typing */}
            <button
              className={`${styles.floatingBarVoiceBtn} ${isRecording ? styles.floatingBarVoiceBtnRecording : ''} ${isTranscribing ? styles.floatingBarVoiceBtnTranscribing : ''} ${prompt.trim() && !isRecording && !isTranscribing ? styles.floatingBarVoiceBtnHideMobile : ''}`}
              onClick={toggleRecording}
              disabled={isTranscribing}
              title={
                isTranscribing
                  ? 'Transcribing...'
                  : isRecording
                    ? 'Click to stop recording'
                    : 'Voice input'
              }
              type="button"
            >
              {isTranscribing ? (
                <Loader2 size={14} className={styles.floatingBarSpin} />
              ) : isRecording ? (
                <MicOff size={14} />
              ) : (
                <Mic size={14} />
              )}
            </button>
            <button
              className={`${styles.floatingBarSendBtn} ${!isFollowUpMode && submitMode === 'draft' ? styles.floatingBarSendBtnDraft : ''} ${!prompt.trim() || isRecording || isTranscribing ? styles.floatingBarSendBtnHideMobile : ''}`}
              onClick={() => handleSubmit()}
              disabled={!prompt.trim()}
              title={
                isFollowUpMode
                  ? 'Send follow-up (Shift+Enter)'
                  : submitMode === 'queue'
                    ? 'Send to queue (Shift+Enter)'
                    : 'Save as draft (Shift+Enter)'
              }
              type="button"
            >
              {!isFollowUpMode && submitMode === 'draft' ? (
                <FileEdit size={14} />
              ) : (
                <ArrowUp size={14} />
              )}
            </button>
          </div>
        </div>
      </div>
    </>
  );
});
