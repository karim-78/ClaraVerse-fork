import { memo, useCallback, useMemo, useState, useEffect } from 'react';
import { Clock, Loader2, CheckCircle2, AlertCircle, Brain, Search, FileEdit } from 'lucide-react';
import { monitorForElements } from '@atlaskit/pragmatic-drag-and-drop/element/adapter';
import { KanbanColumn } from './KanbanColumn';
import { useNexusStore } from '@/store/useNexusStore';
import { nexusService } from '@/services/nexusService';
import type { NexusTask, NexusTaskStatus } from '@/types/nexus';
import styles from './Nexus.module.css';

/** Allowed status transitions for drag-and-drop */
const ALLOWED_DROPS: Record<string, NexusTaskStatus[]> = {
  draft: ['pending', 'cancelled'],
  pending: ['draft', 'executing', 'cancelled'],
  executing: ['cancelled'],
  waiting_input: ['cancelled'],
  completed: ['pending'],
  failed: ['pending'],
  cancelled: ['pending', 'draft'],
};

interface KanbanBoardProps {
  projectId?: string;
  projectName?: string;
  searchQuery?: string;
  isLoading?: boolean;
  onCancelDaemon?: (id: string) => void;
  onStatusChange?: (taskId: string, newStatus: NexusTaskStatus) => void;
  onViewLive?: (taskId: string) => void;
  onRetryTask?: (taskId: string) => void;
  onCancelTask?: (taskId: string) => void;
}

const MOBILE_BREAKPOINT = 768;

export const KanbanBoard = memo(function KanbanBoard({
  projectId,
  projectName,
  searchQuery = '',
  isLoading,
  onCancelDaemon: _onCancelDaemon,
  onStatusChange,
  onViewLive,
  onRetryTask,
  onCancelTask,
}: KanbanBoardProps) {
  const tasks = useNexusStore(s => s.tasks);
  const daemons = useNexusStore(s => s.daemons);
  const selectedTaskId = useNexusStore(s => s.selectedTaskId);
  const setSelectedTaskId = useNexusStore(s => s.setSelectedTaskId);
  const removeTask = useNexusStore(s => s.removeTask);
  const [isMobile, setIsMobile] = useState(
    () => typeof window !== 'undefined' && window.innerWidth < MOBILE_BREAKPOINT
  );
  // 'done' is expanded by default — completed tasks were getting hidden,
  // making users think the system didn't finish anything. They can collapse
  // manually if the column grows long.
  const [expandedColumns, setExpandedColumns] = useState<Set<string>>(
    () => new Set(['working', 'queued', 'done'])
  );

  // Track mobile breakpoint
  useEffect(() => {
    const handleResize = () => setIsMobile(window.innerWidth < MOBILE_BREAKPOINT);
    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  const toggleColumn = useCallback((key: string) => {
    setExpandedColumns(prev => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }, []);

  // Monitor for cross-column drag-and-drop with transition validation
  useEffect(() => {
    return monitorForElements({
      onDrop: ({ source, location }) => {
        const dest = location.current.dropTargets[0];
        if (!dest) return;
        const taskId = source.data.taskId as string;
        const newStatus = dest.data.columnStatus as NexusTaskStatus;
        const oldStatus = source.data.status as string;
        if (!newStatus || newStatus === oldStatus) return;
        const allowed = ALLOWED_DROPS[oldStatus] ?? [];
        if (!allowed.includes(newStatus)) return;
        onStatusChange?.(taskId, newStatus);
      },
    });
  }, [onStatusChange]);

  const handleDeleteTask = useCallback(
    (taskId: string) => {
      removeTask(taskId);
      nexusService.deleteTask(taskId).catch(() => {
        // Silently ignore — task already removed from UI
      });
    },
    [removeTask]
  );

  // All tasks for this project (unfiltered by search)
  const allProjectTasks = useMemo(() => {
    if (!projectId) return tasks;
    return tasks.filter(t => t.project_id === projectId);
  }, [tasks, projectId]);

  // Tasks filtered by search query
  const boardTasks = useMemo(() => {
    if (!searchQuery.trim()) return allProjectTasks;
    const q = searchQuery.toLowerCase();
    return allProjectTasks.filter(
      t =>
        (t.prompt && t.prompt.toLowerCase().includes(q)) ||
        (t.goal && t.goal.toLowerCase().includes(q))
    );
  }, [allProjectTasks, searchQuery]);

  const isSearchActive = searchQuery.trim() !== '';

  const columns = useMemo(() => {
    const draft: NexusTask[] = [];
    const queued: NexusTask[] = [];
    const working: NexusTask[] = [];
    const done: NexusTask[] = [];
    const failed: NexusTask[] = [];

    for (const task of boardTasks) {
      switch (task.status) {
        case 'draft':
          draft.push(task);
          break;
        case 'pending':
          queued.push(task);
          break;
        case 'executing':
        case 'waiting_input':
          working.push(task);
          break;
        case 'completed':
          done.push(task);
          break;
        case 'failed':
        case 'cancelled':
          failed.push(task);
          break;
      }
    }
    return { draft, queued, working, done, failed };
  }, [boardTasks]);

  const columnDefs: {
    key: string;
    title: string;
    icon: React.ReactNode;
    color: string;
    status: NexusTaskStatus;
    tasks: NexusTask[];
  }[] = [
    {
      key: 'draft',
      title: 'Draft',
      icon: <FileEdit size={14} />,
      color: 'var(--color-text-quaternary, rgba(255,255,255,0.25))',
      status: 'draft' as NexusTaskStatus,
      tasks: columns.draft,
    },
    {
      key: 'queued',
      title: 'Queued',
      icon: <Clock size={14} />,
      color: 'var(--color-text-tertiary)',
      status: 'pending' as NexusTaskStatus,
      tasks: columns.queued,
    },
    {
      key: 'working',
      title: 'Working',
      icon:
        columns.working.length > 0 ? (
          <Loader2 size={14} className={styles.spin} />
        ) : (
          <Loader2 size={14} />
        ),
      color: 'var(--color-accent)',
      status: 'executing' as NexusTaskStatus,
      tasks: columns.working,
    },
    {
      key: 'done',
      title: 'Done',
      icon: <CheckCircle2 size={14} />,
      color: 'var(--color-success)',
      status: 'completed' as NexusTaskStatus,
      tasks: columns.done,
    },
    {
      key: 'failed',
      title: 'Failed',
      icon: <AlertCircle size={14} />,
      color: 'var(--color-error)',
      status: 'failed' as NexusTaskStatus,
      tasks: columns.failed,
    },
  ];

  // Genuinely empty project (no tasks at all, not just filtered)
  if (allProjectTasks.length === 0 && !isLoading) {
    return (
      <div className={styles.kanbanBoard}>
        <div className={styles.kanbanEmptyState}>
          <Brain size={56} strokeWidth={1} className={styles.kanbanEmptyIcon} />
          <h2 className={styles.kanbanEmptyTitle}>
            {projectName ? `No tasks in ${projectName} yet` : 'No tasks yet'}
          </h2>
          <p className={styles.kanbanEmptyDescription}>
            Use the bar below to create your first task.
          </p>
        </div>
      </div>
    );
  }

  // Loading skeleton
  if (isLoading) {
    return (
      <div className={styles.kanbanBoard}>
        <div className={styles.kanbanColumns}>
          {['queued', 'working', 'done', 'failed'].map(key => (
            <div key={key} className={styles.kanbanColumn}>
              <div className={styles.kanbanColumnHeader}>
                <div className={styles.kanbanSkeletonBar} style={{ width: 80 }} />
              </div>
              <div className={styles.kanbanColumnBody}>
                {Array.from({ length: key === 'queued' ? 3 : 2 }).map((_, i) => (
                  <div key={i} className={styles.kanbanSkeletonCard} />
                ))}
              </div>
            </div>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className={styles.kanbanBoard}>
      {/* Search returned no results */}
      {isSearchActive && boardTasks.length === 0 ? (
        <div className={styles.kanbanEmptySearch}>
          <Search size={24} className={styles.kanbanEmptyIcon} />
          <p>No tasks match &ldquo;{searchQuery}&rdquo;</p>
        </div>
      ) : (
        <div className={`${styles.kanbanColumns} ${isMobile ? styles.kanbanColumnsMobile : ''}`}>
          {columnDefs.map(col => (
            <KanbanColumn
              key={col.key}
              title={col.title}
              icon={col.icon}
              accentColor={col.color}
              columnStatus={col.status}
              tasks={col.tasks}
              daemons={daemons}
              selectedTaskId={selectedTaskId}
              onSelectTask={setSelectedTaskId}
              onDeleteTask={handleDeleteTask}
              onViewLive={onViewLive}
              onRetryTask={onRetryTask}
              onCancelTask={onCancelTask}
              isMobile={isMobile}
              isExpanded={expandedColumns.has(col.key)}
              onToggle={() => toggleColumn(col.key)}
            />
          ))}
        </div>
      )}
    </div>
  );
});
