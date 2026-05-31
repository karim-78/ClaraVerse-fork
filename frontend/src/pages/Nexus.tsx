import { useCallback, useMemo, useState, useEffect, useRef } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { Menu } from 'lucide-react';
import { CommandCenterView } from '@/components/nexus/CommandCenterView';
import { DaemonsView } from '@/components/nexus/DaemonsView';
import { RoutinesView } from '@/components/nexus/RoutinesView';
import { SettingsView } from '@/components/nexus/SettingsView';
import { SavesView } from '@/components/nexus/SavesView';
import { TaskDetailPanel } from '@/components/nexus/TaskDetailPanel';
import { FloatingTaskBar } from '@/components/nexus/FloatingTaskBar';
import { NexusSidebar } from '@/components/nexus/NexusSidebar';
import { ProjectFormModal } from '@/components/nexus/ProjectFormModal';
import { useNexusWebSocket } from '@/hooks/useNexusWebSocket';
import { useNexusStore } from '@/store/useNexusStore';
import { nexusService } from '@/services/nexusService';
import type { NexusProject, NexusTaskStatus } from '@/types/nexus';
import styles from '@/components/nexus/Nexus.module.css';

const MOBILE_BREAKPOINT = 768;

export function Nexus() {
  const { projectId: urlProjectId } = useParams<{ projectId?: string }>();
  const navigate = useNavigate();

  const {
    createTask,
    cancelDaemon,
    cancelTask,
    requestSession,
    send,
    updateTaskStatus,
    retryTask,
  } = useNexusWebSocket();
  const connected = useNexusStore(s => s.connected);
  const activeView = useNexusStore(s => s.activeView);
  const activeProjectId = useNexusStore(s => s.activeProjectId);
  const selectedTaskId = useNexusStore(s => s.selectedTaskId);
  const setSelectedTaskId = useNexusStore(s => s.setSelectedTaskId);
  const rightPanel = useNexusStore(s => s.rightPanel);
  const setRightPanel = useNexusStore(s => s.setRightPanel);
  const setProjects = useNexusStore(s => s.setProjects);
  const addProject = useNexusStore(s => s.addProject);
  const updateProjectInStore = useNexusStore(s => s.updateProjectInStore);

  const [isSidebarOpen, setIsSidebarOpen] = useState(
    () => typeof window !== 'undefined' && window.innerWidth >= MOBILE_BREAKPOINT
  );
  const [isMobile, setIsMobile] = useState(
    () => typeof window !== 'undefined' && window.innerWidth < MOBILE_BREAKPOINT
  );

  // Project form modal
  const [showProjectForm, setShowProjectForm] = useState(false);
  const [editingProject, setEditingProject] = useState<NexusProject | null>(null);

  useEffect(() => {
    const handleResize = () => {
      setIsMobile(window.innerWidth < MOBILE_BREAKPOINT);
    };
    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  const setActiveProjectId = useNexusStore(s => s.setActiveProjectId);
  const projects = useNexusStore(s => s.projects);

  // Navigate to a project: update store + URL in one shot (no effect loop)
  const navigateToProject = useCallback(
    (id: string) => {
      setActiveProjectId(id);
      setSelectedTaskId(null);
      setRightPanel('none');
      navigate(`/nexus/${id}`, { replace: true });
    },
    [setActiveProjectId, setSelectedTaskId, setRightPanel, navigate]
  );

  // Sync URL → store on mount / URL change (one-way: URL is source of truth)
  const syncingRef = useRef(false);
  useEffect(() => {
    if (projects.length === 0) return;
    if (urlProjectId) {
      const exists = projects.find(p => p.id === urlProjectId);
      if (exists && urlProjectId !== activeProjectId) {
        syncingRef.current = true;
        setActiveProjectId(urlProjectId);
      }
    }
  }, [urlProjectId, projects, activeProjectId, setActiveProjectId]);

  // Keep URL in sync with store: clear projectId when not in project view,
  // navigate when active project changes, and clear stale URLs.
  useEffect(() => {
    if (activeView !== 'project') {
      // Switched to daemons/routines/settings — remove projectId from URL
      if (urlProjectId) {
        navigate('/nexus', { replace: true });
      }
    } else if (!activeProjectId) {
      // All projects deleted — clear stale projectId from URL
      if (urlProjectId) {
        navigate('/nexus', { replace: true });
      }
    } else if (activeProjectId !== urlProjectId) {
      // Skip if this was triggered by the URL→store sync above
      if (syncingRef.current) {
        syncingRef.current = false;
        return;
      }
      navigate(`/nexus/${activeProjectId}`, { replace: true });
    }
  }, [activeView, activeProjectId, urlProjectId, navigate]);

  // Request session state on connect
  useEffect(() => {
    if (connected) {
      requestSession();
    }
  }, [connected, requestSession]);

  // Authoritative task refresh from REST. Fires on three triggers:
  //   1. Connection state flips to true   (WS reconnect after drop)
  //   2. activeProjectId changes          (user switched projects)
  //   3. Page becomes visible             (tab switched back, or app view
  //                                        switched away+back while WS held)
  //
  // The visibility trigger matters because navigating between in-app views
  // (Chat → Workflows → Nexus) unmounts/remounts the Nexus page WITHOUT
  // dropping the WebSocket, so `connected` never flips and the
  // [connected, activeProjectId] effect alone wouldn't re-fire. The
  // document.visibilitychange listener catches the cross-tab case; the
  // separate mount-time fetch (below) catches the cross-view case.
  const setTasksFromServer = useNexusStore(s => s.setTasks);
  const refreshTasks = useCallback(() => {
    if (!connected) return () => {};
    let cancelled = false;
    nexusService
      .listTasks({ limit: 100 })
      .then(tasks => {
        if (!cancelled) setTasksFromServer(tasks);
      })
      .catch(err => {
        console.warn('[Nexus] task list refresh failed, falling back to WS:', err);
      });
    return () => {
      cancelled = true;
    };
  }, [connected, setTasksFromServer]);

  // Trigger 1+2: connect / project change.
  useEffect(() => {
    return refreshTasks();
  }, [connected, activeProjectId, refreshTasks]);

  // Trigger 3 (cross-tab): tab visibility flipping to visible.
  useEffect(() => {
    const onVisibility = () => {
      if (document.visibilityState === 'visible') {
        refreshTasks();
      }
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  }, [refreshTasks]);

  // Trigger 4 (cross-view-in-app): every mount of this page fires once
  // regardless of connected/project state. Even when connected is stale-true
  // from a held WS, leaving Nexus → coming back should re-pull.
  useEffect(() => {
    refreshTasks();
    // intentionally empty deps — fires once on mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Rehydrate the active-daemons map from REST so the live activity panel
  // is repopulated after a hard reload (where sessionStorage-persisted
  // conversation may be empty). We don't pull each daemon's full message
  // log — that's heavy and arrives via WS daemon_thinking/daemon_tool_*
  // updates anyway. We just need the daemons themselves so the panel
  // header + status + current_action render immediately.
  const addDaemon = useNexusStore(s => s.addDaemon);
  useEffect(() => {
    let cancelled = false;
    nexusService
      .listDaemons()
      .then(daemons => {
        if (cancelled) return;
        for (const d of daemons) addDaemon(d);
      })
      .catch(err => {
        console.warn('[Nexus] active daemon rehydrate failed:', err);
      });
    return () => {
      cancelled = true;
    };
  }, [addDaemon]);

  // Fetch projects on connect; auto-create a default if none exist
  const fetchingProjectsRef = useRef(false);
  useEffect(() => {
    if (!connected || fetchingProjectsRef.current) return;
    fetchingProjectsRef.current = true;
    nexusService
      .listProjects()
      .then(async fetched => {
        if (fetched.length === 0) {
          const created = await nexusService.createProject({
            name: 'General',
            icon: 'folder',
            color: '#2196F3',
          });
          fetched = [created];
        }
        setProjects(fetched);
        // Use URL projectId if valid, otherwise auto-select first project
        if (urlProjectId && fetched.find(p => p.id === urlProjectId)) {
          setActiveProjectId(urlProjectId);
        } else {
          const state = useNexusStore.getState();
          if (!state.activeProjectId || !fetched.find(p => p.id === state.activeProjectId)) {
            navigateToProject(fetched[0].id);
          }
        }
      })
      .catch(console.error)
      .finally(() => {
        fetchingProjectsRef.current = false;
      });
  }, [connected, setProjects, setActiveProjectId, navigateToProject, urlProjectId]);

  const handleCancelDaemon = useCallback(
    (id: string) => {
      cancelDaemon(id);
    },
    [cancelDaemon]
  );

  const handleCancelTask = useCallback(
    (taskId: string) => {
      cancelTask(taskId);
    },
    [cancelTask]
  );

  // Auto-collapse sidebar when task detail panel opens (desktop only)
  useEffect(() => {
    if (rightPanel === 'task-detail' && selectedTaskId && !isMobile) {
      setIsSidebarOpen(false);
    }
  }, [rightPanel, selectedTaskId, isMobile]);

  const handleClosePanel = useCallback(() => {
    setRightPanel('none');
    setSelectedTaskId(null);
  }, [setRightPanel, setSelectedTaskId]);

  // Follow-up: auto-tag when a non-terminal task is selected (running, waiting, pending)
  // or a completed task (user explicitly wanted follow-up on completed tasks)
  const tasks = useNexusStore(s => s.tasks);
  const followUpTask = useMemo(() => {
    if (!selectedTaskId) return null;
    const task = tasks.find(t => t.id === selectedTaskId);
    if (!task) return null;
    // Only show follow-up for non-cancelled tasks (completed + active)
    if (task.status === 'cancelled') return null;
    return task;
  }, [selectedTaskId, tasks]);

  const handleClearFollowUp = useCallback(() => {
    setSelectedTaskId(null);
    setRightPanel('none');
  }, [setSelectedTaskId, setRightPanel]);

  const handleCreateTask = useCallback(
    (opts: {
      content: string;
      initialStatus?: 'draft' | 'pending';
      modelId?: string;
      tools?: string[];
      daemonMode?: 'auto' | 'quick' | 'daemon' | 'multi_daemon';
      templateId?: string;
      priority?: number;
      scheduledAt?: string;
      projectId?: string;
      saveIds?: string[];
    }) => {
      if (opts.initialStatus === 'draft') {
        // Create draft via REST API so it persists across refreshes
        const projectId = opts.projectId ?? activeProjectId ?? undefined;
        nexusService
          .createTask({
            prompt: opts.content,
            goal: opts.content,
            priority: opts.priority ?? 1,
            mode: 'quick',
            status: 'draft',
            model_id: opts.modelId,
            project_id: projectId,
          })
          .then(task => {
            useNexusStore.getState().addTask(task);
          })
          .catch(err => {
            console.error('Failed to create draft task:', err);
          });
        return;
      }
      createTask(opts);
    },
    [createTask, activeProjectId]
  );

  const handleFollowUp = useCallback(
    (content: string, taskId: string) => {
      useNexusStore.getState().addConversationItem({
        id: crypto.randomUUID(),
        type: 'user_message',
        content,
        timestamp: new Date(),
        taskId,
      });
      send({ type: 'send_message', content, task_id: taskId });
    },
    [send]
  );

  const handleRetryTask = useCallback(
    (taskId: string) => {
      const task = useNexusStore.getState().tasks.find(t => t.id === taskId);
      if (task && (task.manual_retry_count ?? 0) >= 3) {
        useNexusStore.getState().addConversationItem({
          id: crypto.randomUUID(),
          type: 'error',
          content: 'Maximum retry limit (3) reached for this task.',
          timestamp: new Date(),
        });
        return;
      }
      retryTask(taskId);
    },
    [retryTask]
  );

  const handleStatusChange = useCallback(
    (taskId: string, newStatus: NexusTaskStatus) => {
      if (newStatus === 'pending') {
        const task = useNexusStore.getState().tasks.find(t => t.id === taskId);

        // Draft → Queued: tell backend via WebSocket (it triggers Cortex execution)
        if (task?.status === 'draft') {
          updateTaskStatus(taskId, 'pending');
          return;
        }

        // Terminal → Queued: trigger a retry so the backend re-executes
        const isTerminal =
          task?.status === 'completed' || task?.status === 'failed' || task?.status === 'cancelled';
        if (isTerminal) {
          if ((task.manual_retry_count ?? 0) >= 3) {
            useNexusStore.getState().addConversationItem({
              id: crypto.randomUUID(),
              type: 'error',
              content: 'Maximum retry limit (3) reached for this task.',
              timestamp: new Date(),
            });
            return;
          }
          retryTask(taskId);
          return;
        }
      }

      // Draft → Cancelled: delete from backend
      if (newStatus === 'cancelled') {
        const task = useNexusStore.getState().tasks.find(t => t.id === taskId);
        if (task?.status === 'draft') {
          nexusService
            .deleteTask(taskId)
            .catch(err => console.error('Failed to delete draft:', err));
          useNexusStore.getState().removeTask(taskId);
          return;
        }
      }
      updateTaskStatus(taskId, newStatus);
    },
    [updateTaskStatus, retryTask]
  );

  // Project CRUD
  const handleNewProject = useCallback(() => {
    setEditingProject(null);
    setShowProjectForm(true);
  }, []);

  const handleEditProject = useCallback(() => {
    const project = useNexusStore.getState().projects.find(p => p.id === activeProjectId);
    if (project) {
      setEditingProject(project);
      setShowProjectForm(true);
    }
  }, [activeProjectId]);

  const removeProject = useNexusStore(s => s.removeProject);

  const handleDeleteProject = useCallback(async () => {
    if (!editingProject) return;
    try {
      await nexusService.deleteProject(editingProject.id);
      removeProject(editingProject.id);
      setShowProjectForm(false);
      setEditingProject(null);
    } catch (err) {
      console.error('Failed to delete project:', err);
    }
  }, [editingProject, removeProject]);

  const handleSaveProject = useCallback(
    async (data: Partial<NexusProject>) => {
      try {
        if (editingProject) {
          await nexusService.updateProject(editingProject.id, data);
          updateProjectInStore(editingProject.id, data);
        } else {
          const created = await nexusService.createProject(data);
          addProject(created);
          // Auto-navigate to the newly created project
          navigateToProject(created.id);
        }
        setShowProjectForm(false);
        setEditingProject(null);
      } catch (err) {
        console.error('Failed to save project:', err);
      }
    },
    [editingProject, addProject, updateProjectInStore, navigateToProject]
  );

  const renderRightPanel = () => (
    <>
      {rightPanel === 'task-detail' && selectedTaskId && (
        <TaskDetailPanel
          taskId={selectedTaskId}
          onClose={handleClosePanel}
          onCancelDaemon={handleCancelDaemon}
          onCancelTask={handleCancelTask}
          onRetryTask={handleRetryTask}
          onStatusChange={handleStatusChange}
        />
      )}
    </>
  );

  return (
    <div className={styles.nexusContainer}>
      <NexusSidebar
        isOpen={isSidebarOpen}
        onOpenChange={setIsSidebarOpen}
        onNewProject={handleNewProject}
        onSelectProject={navigateToProject}
      />

      <main className={styles.nexusMain}>
        {isMobile && !isSidebarOpen && (
          <button
            className={styles.floatingMenuButton}
            onClick={() => setIsSidebarOpen(true)}
            aria-label="Open menu"
          >
            <Menu size={20} />
          </button>
        )}

        {activeView === 'project' && activeProjectId && (
          <>
            <div className={styles.commandCenterLayout}>
              <CommandCenterView
                projectId={activeProjectId}
                onCancelDaemon={handleCancelDaemon}
                onCancelTask={handleCancelTask}
                onStatusChange={handleStatusChange}
                onSendFollowUp={handleFollowUp}
                onRetryTask={handleRetryTask}
                onEditProject={handleEditProject}
              />
              {renderRightPanel()}
            </div>
            <FloatingTaskBar
              projectId={activeProjectId}
              onCreateTask={handleCreateTask}
              onSendFollowUp={handleFollowUp}
              followUpTask={followUpTask}
              onClearFollowUp={handleClearFollowUp}
              detailPanelOpen={rightPanel === 'task-detail' && !!selectedTaskId}
            />
          </>
        )}

        {activeView === 'daemons' && <DaemonsView />}

        {activeView === 'routines' && <RoutinesView />}

        {activeView === 'settings' && <SettingsView send={send} />}

        {activeView === 'saves' && <SavesView />}
      </main>

      {showProjectForm && (
        <ProjectFormModal
          project={editingProject}
          onSave={handleSaveProject}
          onDelete={editingProject ? handleDeleteProject : undefined}
          onClose={() => {
            setShowProjectForm(false);
            setEditingProject(null);
          }}
        />
      )}
    </div>
  );
}
