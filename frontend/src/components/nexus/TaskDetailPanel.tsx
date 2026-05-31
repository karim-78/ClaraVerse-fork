import { memo, useMemo, useState, useCallback, useEffect } from 'react';
import {
  X,
  AlertCircle,
  CheckCircle2,
  Clock,
  Loader2,
  RotateCcw,
  Bot,
  Wrench,
  Folder,
  StopCircle,
  User,
  Cpu,
  FileEdit,
  ArrowRight,
  Bookmark,
  BookmarkCheck,
} from 'lucide-react';
import { Badge } from '@/components/design-system';
import { MarkdownRenderer } from '@/components/design-system/content/MarkdownRenderer';
import { DaemonActivityBlock } from './DaemonActivityBlock';
import { useNexusStore } from '@/store/useNexusStore';
import { nexusService } from '@/services/nexusService';
import type { NexusTaskStatus } from '@/types/nexus';
import styles from './Nexus.module.css';

// extractFileLinks scans a markdown summary for backend file URLs the daemons
// produced (html_to_pdf, create_document, etc.). The daemons emit links like
// `/api/files/<id>` or `(/files/<id>)` in their summaries; we lift them out
// and render them as prominent download buttons so the user doesn't have to
// hunt through markdown to find their PDF.
//
// Deduplicates by URL. Label is derived from the closest preceding text or
// the file extension; never empty.
function extractFileLinks(summary: string): Array<{ url: string; label: string }> {
  if (!summary) return [];
  // Match /api/files/<id> or /files/<id> where id is hex/uuid-ish.
  const re = /\/(api\/)?files\/([A-Za-z0-9_\-.]+)/g;
  const seen = new Set<string>();
  const out: Array<{ url: string; label: string }> = [];
  let m: RegExpExecArray | null;
  while ((m = re.exec(summary)) !== null) {
    const url = m[0];
    if (seen.has(url)) continue;
    seen.add(url);
    // Best-effort filename: trailing segment after the last slash.
    const tail = m[2];
    const ext = tail.includes('.') ? tail.slice(tail.lastIndexOf('.') + 1).toUpperCase() : 'file';
    out.push({ url, label: ext });
  }
  return out;
}

function formatDuration(ms: number): string {
  const totalSeconds = Math.floor(ms / 1000);
  if (totalSeconds < 60) return `${totalSeconds}s`;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes < 60) return `${minutes}m ${seconds}s`;
  const hours = Math.floor(minutes / 60);
  const remainingMinutes = minutes % 60;
  return `${hours}h ${remainingMinutes}m`;
}

interface TaskDetailPanelProps {
  taskId: string;
  onClose: () => void;
  onCancelDaemon?: (id: string) => void;
  onCancelTask?: (taskId: string) => void;
  onRetryTask?: (taskId: string) => void;
  onStatusChange?: (taskId: string, newStatus: NexusTaskStatus) => void;
}

const statusConfig: Record<
  NexusTaskStatus,
  {
    variant: 'default' | 'accent' | 'success' | 'warning' | 'error';
    label: string;
    icon: React.ReactNode;
  }
> = {
  draft: { variant: 'default', label: 'Draft', icon: <FileEdit size={12} /> },
  pending: { variant: 'default', label: 'Queued', icon: <Clock size={12} /> },
  executing: { variant: 'accent', label: 'Running', icon: <Loader2 size={12} /> },
  waiting_input: { variant: 'warning', label: 'Waiting', icon: <Clock size={12} /> },
  completed: { variant: 'success', label: 'Done', icon: <CheckCircle2 size={12} /> },
  failed: { variant: 'error', label: 'Failed', icon: <AlertCircle size={12} /> },
  cancelled: { variant: 'default', label: 'Cancelled', icon: <X size={12} /> },
};

export const TaskDetailPanel = memo(function TaskDetailPanel({
  taskId,
  onClose,
  onCancelDaemon,
  onCancelTask,
  onRetryTask,
  onStatusChange,
}: TaskDetailPanelProps) {
  const tasks = useNexusStore(s => s.tasks);
  const daemons = useNexusStore(s => s.daemons);
  const conversation = useNexusStore(s => s.conversation);
  const projects = useNexusStore(s => s.projects);
  const saves = useNexusStore(s => s.saves);
  const addSave = useNexusStore(s => s.addSave);
  const setTaskProject = useNexusStore(s => s.setTaskProject);
  const updateTask = useNexusStore(s => s.updateTask);
  const [now, setNow] = useState(() => Date.now());
  const [stopping, setStopping] = useState(false);
  const [retrying, setRetrying] = useState(false);
  const [saving, setSaving] = useState(false);
  const [editedGoal, setEditedGoal] = useState('');

  const task = useMemo(() => tasks.find(t => t.id === taskId), [tasks, taskId]);

  // Sync editedGoal when task changes (for draft editing)
  useEffect(() => {
    if (task) setEditedGoal(task.goal || task.prompt || '');
  }, [task?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  // Live tick for running tasks (duration display)
  useEffect(() => {
    if (!task || (task.status !== 'executing' && task.status !== 'waiting_input')) return;
    const interval = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(interval);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [task?.status]);

  // Reset action feedback when task status changes
  useEffect(() => {
    setStopping(false);
    setRetrying(false);
  }, [task?.status]);

  const durationText = useMemo(() => {
    if (!task?.created_at) return null;
    const start = new Date(task.created_at).getTime();
    if (task.status === 'executing' || task.status === 'waiting_input') {
      return `running for ${formatDuration(now - start)}`;
    }
    if (task.completed_at) {
      const end = new Date(task.completed_at).getTime();
      return `took ${formatDuration(end - start)}`;
    }
    return null;
  }, [task, now]);

  const taskDaemons = useMemo(
    () => Object.values(daemons).filter(d => d.task_id === taskId),
    [daemons, taskId]
  );

  // Collect conversation items for this task's daemons
  const daemonIds = useMemo(
    () => new Set(taskDaemons.map(d => d.id).filter(Boolean)),
    [taskDaemons]
  );
  const taskConversation = useMemo(
    () =>
      conversation.filter(
        item => item.taskId === taskId || (item.daemonId && daemonIds.has(item.daemonId))
      ),
    [conversation, taskId, daemonIds]
  );

  // Group conversation items by daemonId for DaemonActivityBlock
  const groupedByDaemon = useMemo(() => {
    const groups: Record<string, typeof taskConversation> = {};
    for (const item of taskConversation) {
      if (item.daemonId) {
        if (!groups[item.daemonId]) groups[item.daemonId] = [];
        groups[item.daemonId].push(item);
      }
    }
    return groups;
  }, [taskConversation]);

  // Task-level conversation items (not grouped by daemon) — user follow-ups + cortex responses
  const taskLevelMessages = useMemo(
    () => taskConversation.filter(item => !item.daemonId && item.type !== 'error'),
    [taskConversation]
  );

  const isSaved = useMemo(() => saves.some(s => s.source_task_id === taskId), [saves, taskId]);

  const handleSaveResult = useCallback(async () => {
    if (!task?.result?.summary || saving || isSaved) return;
    setSaving(true);
    try {
      const saved = await nexusService.createSave({
        title: task.goal || task.prompt,
        content: task.result.summary,
        source_task_id: task.id,
        source_project_id: task.project_id,
      });
      addSave(saved);
    } catch (err) {
      console.error('Failed to save result:', err);
    } finally {
      setSaving(false);
    }
  }, [task, saving, isSaved, addSave]);

  const handleMoveToProject = useCallback(
    async (newProjectId: string) => {
      const pid = newProjectId || null;
      try {
        await nexusService.moveTaskToProject(taskId, pid);
        setTaskProject(taskId, pid);
      } catch (err) {
        console.error('Failed to move task:', err);
      }
    },
    [taskId, setTaskProject]
  );

  if (!task) {
    return (
      <div className={styles.detailPanel}>
        <div className={styles.detailPanelHeader}>
          <h3>Task not found</h3>
          <button className={styles.detailCloseBtn} onClick={onClose}>
            <X size={16} />
          </button>
        </div>
      </div>
    );
  }

  const cfg = statusConfig[task.status] ?? statusConfig.pending;
  const isTerminal =
    task.status === 'completed' || task.status === 'failed' || task.status === 'cancelled';
  const isRunning = task.status === 'executing';

  return (
    <>
      <div className={styles.detailPanelBackdrop} onClick={onClose} aria-hidden="true" />
      <div className={styles.detailPanel}>
        <div className={styles.detailPanelHeader}>
          <h3>Task Detail</h3>
          <div className={styles.detailHeaderActions}>
            {(isRunning || task.status === 'waiting_input') && onCancelTask && (
              <button
                className={`${styles.detailStopBtn} ${stopping ? styles.detailBtnLoading : ''}`}
                onClick={() => {
                  setStopping(true);
                  onCancelTask(taskId);
                }}
                title="Stop task"
                disabled={stopping}
              >
                {stopping ? (
                  <Loader2 size={14} className={styles.spin} />
                ) : (
                  <StopCircle size={14} />
                )}
              </button>
            )}
            {isTerminal && onRetryTask && (
              <button
                className={`${styles.detailRetryBtn} ${retrying ? styles.detailBtnLoading : ''}`}
                onClick={() => {
                  setRetrying(true);
                  onRetryTask(taskId);
                }}
                title={
                  (task.manual_retry_count ?? 0) >= 3 ? 'Maximum retries reached' : 'Retry task'
                }
                disabled={(task.manual_retry_count ?? 0) >= 3 || retrying}
              >
                {retrying ? <Loader2 size={14} className={styles.spin} /> : <RotateCcw size={14} />}
              </button>
            )}
            <button className={styles.detailCloseBtn} onClick={onClose}>
              <X size={16} />
            </button>
          </div>
        </div>

        <div className={styles.detailPanelBody}>
          <div className={styles.detailTaskSummary}>
            <Badge variant={cfg.variant} icon={cfg.icon}>
              {cfg.label}
            </Badge>
            {task.status === 'draft' ? (
              <textarea
                className={styles.detailDraftTextarea}
                value={editedGoal}
                onChange={e => setEditedGoal(e.target.value)}
                onBlur={() => {
                  if (editedGoal !== (task.goal || task.prompt)) {
                    updateTask({ task_id: taskId, goal: editedGoal, prompt: editedGoal });
                    nexusService
                      .updateTask(taskId, { prompt: editedGoal, goal: editedGoal })
                      .catch(err => console.error('Failed to save draft:', err));
                  }
                }}
                placeholder="Describe what you want to do..."
              />
            ) : (
              <p>{task.goal || task.prompt}</p>
            )}
            <div className={styles.detailTaskMeta}>
              <span>{task.mode === 'multi_daemon' ? 'Multi-daemon' : task.mode}</span>
              {task.created_at && <span>{new Date(task.created_at).toLocaleTimeString()}</span>}
              {durationText && <span>{durationText}</span>}
            </div>
            {task.status === 'draft' && onStatusChange && (
              <button
                className={styles.detailQueueBtn}
                onClick={() => onStatusChange(taskId, 'pending')}
              >
                Queue Task <ArrowRight size={14} />
              </button>
            )}
            {projects.length > 0 && (
              <div className={styles.detailProjectRow}>
                <Folder size={12} />
                <select
                  className={styles.formSelect}
                  value={task.project_id ?? ''}
                  onChange={e => handleMoveToProject(e.target.value)}
                >
                  <option value="">Inbox (no project)</option>
                  {projects.map(p => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </div>
            )}
          </div>

          {task.result && (
            <div className={styles.detailTaskResult}>
              <div className={styles.detailResultHeader}>
                <CheckCircle2 size={14} /> Result
                <button
                  className={`${styles.detailSaveBtn} ${isSaved ? styles.detailSaveBtnSaved : ''}`}
                  onClick={handleSaveResult}
                  title={isSaved ? 'Saved' : 'Save to Saves'}
                  disabled={isSaved || saving}
                >
                  {saving ? (
                    <Loader2 size={14} className={styles.spin} />
                  ) : isSaved ? (
                    <BookmarkCheck size={14} />
                  ) : (
                    <Bookmark size={14} />
                  )}
                </button>
              </div>
              {/* File downloads — scrape the result for /api/files/<id> links the
                  daemons produced (html_to_pdf, create_document, etc.). Rendered
                  before the markdown so they're impossible to miss. */}
              {extractFileLinks(task.result.summary).map(link => (
                <a
                  key={link.url}
                  href={link.url}
                  download
                  target="_blank"
                  rel="noopener noreferrer"
                  className={styles.detailDownloadBtn ?? ''}
                  style={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: 8,
                    padding: '8px 14px',
                    margin: '8px 8px 8px 0',
                    background: '#2d8a4f',
                    color: 'white',
                    borderRadius: 6,
                    textDecoration: 'none',
                    fontWeight: 500,
                    fontSize: 13,
                  }}
                >
                  ⬇ Download {link.label}
                </a>
              ))}
              <MarkdownRenderer content={task.result.summary} />
            </div>
          )}

          {task.error && (
            <div className={styles.detailTaskError}>
              <AlertCircle size={14} />
              <span>{task.error}</span>
            </div>
          )}

          {task.retry_of_task_id && (
            <div className={styles.detailRetryInfo}>
              <RotateCcw size={12} />
              <span>Retry attempt {task.manual_retry_count ?? 1} of 3</span>
            </div>
          )}

          {/* Daemon Activity — show DaemonActivityBlock when we have conversation items,
            otherwise show a live status card per daemon */}
          {taskDaemons.length > 0 && (
            <div className={styles.detailDaemons}>
              <h4 className={styles.detailSectionTitle}>Daemon Activity ({taskDaemons.length})</h4>
              {taskDaemons.map(daemon => {
                const items = groupedByDaemon[daemon.id!] ?? [];
                if (items.length > 0) {
                  return (
                    <DaemonActivityBlock
                      key={daemon.id}
                      daemonId={daemon.id!}
                      daemonRole={daemon.role_label || daemon.role || 'Daemon'}
                      items={items}
                      onCancel={onCancelDaemon}
                    />
                  );
                }
                // No conversation items yet — show a live status card
                return (
                  <div key={daemon.id} className={styles.detailDaemonLive}>
                    <div className={styles.detailDaemonLiveHeader}>
                      <Bot size={14} />
                      <span className={styles.detailDaemonLiveName}>
                        {daemon.role_label || daemon.role || 'Daemon'}
                      </span>
                      <Badge
                        variant={
                          daemon.status === 'executing'
                            ? 'accent'
                            : daemon.status === 'completed'
                              ? 'success'
                              : daemon.status === 'failed'
                                ? 'error'
                                : 'default'
                        }
                      >
                        {daemon.status === 'executing' ? 'Running' : (daemon.status ?? 'Idle')}
                      </Badge>
                    </div>
                    {daemon.task_summary && (
                      <div className={styles.detailDaemonLiveTask}>{daemon.task_summary}</div>
                    )}
                    {daemon.current_action && daemon.status === 'executing' && (
                      <div className={styles.detailDaemonLiveAction}>
                        <Wrench size={11} />
                        <span>{daemon.current_action}</span>
                      </div>
                    )}
                    {daemon.status === 'executing' && (
                      <div className={styles.detailDaemonLiveIndicator}>
                        <span className={styles.thinkingDot} />
                        <span className={styles.thinkingDot} />
                        <span className={styles.thinkingDot} />
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}

          {/* Conversation thread — user follow-ups and cortex responses */}
          {taskLevelMessages.length > 0 && (
            <div className={styles.detailConversation}>
              <h4 className={styles.detailSectionTitle}>Conversation</h4>
              {taskLevelMessages.map(item => (
                <div
                  key={item.id}
                  className={`${styles.detailMessage} ${item.type === 'user_message' ? styles.detailMessageUser : styles.detailMessageAssistant}`}
                >
                  <div className={styles.detailMessageIcon}>
                    {item.type === 'user_message' ? <User size={12} /> : <Cpu size={12} />}
                  </div>
                  <div className={styles.detailMessageContent}>
                    {item.type === 'user_message' ? (
                      <p>{item.content}</p>
                    ) : (
                      <MarkdownRenderer content={item.content} />
                    )}
                    <span className={styles.detailMessageTime}>
                      {new Date(item.timestamp).toLocaleTimeString()}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          )}

          {taskDaemons.length === 0 &&
            taskConversation.length === 0 &&
            taskLevelMessages.length === 0 &&
            !isRunning && (
              <div className={styles.detailEmpty}>
                {task.status === 'pending' ? 'Waiting to start...' : 'No activity recorded yet.'}
              </div>
            )}

          {taskDaemons.length === 0 && isRunning && (
            <div className={styles.detailWaiting}>
              <Loader2 size={16} className={styles.spin} />
              <span>Setting up daemons...</span>
            </div>
          )}
        </div>
      </div>
    </>
  );
});
