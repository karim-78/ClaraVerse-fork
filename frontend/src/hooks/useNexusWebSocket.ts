import { useEffect, useRef, useCallback } from 'react';
import type {
  NexusClientMessage,
  NexusServerMessage,
  NexusServerMessageType,
  NexusTaskStatus,
  MissedUpdate,
} from '@/types/nexus';
import { useNexusStore } from '@/store/useNexusStore';
import { useAuthStore } from '@/store/useAuthStore';
import { toast } from '@/store/useToastStore';
import { getWsUrl } from '@/lib/config';

const WS_URL = getWsUrl();
const WS_ENDPOINT = '/ws/nexus';
const HEARTBEAT_INTERVAL = 25000;
const MAX_RECONNECT_DELAY = 30000;
const MAX_RECONNECT_ATTEMPTS = 15;

// truncateForToast keeps the completion toast readable. Full result lives in
// the task panel; the toast is just the "look over here" nudge.
function truncateForToast(s: string, max = 140): string {
  const trimmed = s.trim().replace(/\s+/g, ' ');
  return trimmed.length > max ? trimmed.slice(0, max - 1) + '…' : trimmed;
}

type MessageHandler = (msg: NexusServerMessage) => void;

export function useNexusWebSocket() {
  const authToken = useAuthStore(s => s.accessToken);
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectAttemptsRef = useRef(0);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);
  const heartbeatRef = useRef<ReturnType<typeof setInterval>>(undefined);
  const handlersRef = useRef<Set<MessageHandler>>(new Set());
  // Ref to always hold the latest token — reconnect closures read this instead of stale closure captures
  const authTokenRef = useRef(authToken);
  authTokenRef.current = authToken;

  const {
    setConnected,
    setBridgeConnected,
    setSessionState,
    addConversationItem,
    updateDaemon,
    addDaemon,
    updateTask,
    setPersona,
    setEngrams,
    setClassification,
    setIsProcessing,
    setMissedUpdates,
  } = useNexusStore();

  const processMessage = useCallback(
    (msg: NexusServerMessage) => {
      const { type, data } = msg;
      const d = data as Record<string, unknown>;

      switch (type as NexusServerMessageType) {
        case 'connected':
          setConnected(true);
          reconnectAttemptsRef.current = 0;
          break;

        case 'pong':
          break;

        case 'session_state':
          if (d) setSessionState(d);
          break;

        case 'cortex_thinking':
          if (d?.content) {
            addConversationItem({
              id: crypto.randomUUID(),
              type: 'cortex_thinking',
              content: d.content as string,
              timestamp: new Date(),
            });
          }
          break;

        case 'cortex_response':
          if (d?.content) {
            addConversationItem({
              id: crypto.randomUUID(),
              type: 'cortex_response',
              content: d.content as string,
              timestamp: new Date(),
              taskId: d.task_id as string | undefined,
            });
            setIsProcessing(false);
          }
          break;

        case 'cortex_classified':
          if (d) setClassification(d);
          break;

        case 'daemon_deployed':
          if (d) {
            addDaemon({
              id: d.daemon_id as string,
              task_id: d.task_id as string,
              role: d.role as string,
              role_label: d.role_label as string,
              task_summary: d.task_summary as string,
              status: 'executing',
              current_action: d.task_summary as string,
              progress: 0,
            });
            addConversationItem({
              id: crypto.randomUUID(),
              type: 'daemon_activity',
              content: `${d.role_label} deployed: ${d.task_summary}`,
              timestamp: new Date(),
              daemonId: d.daemon_id as string,
              daemonRole: d.role_label as string,
            });
          }
          break;

        case 'daemon_status':
          if (d) {
            updateDaemon(d);
            if (d.current_action) {
              addConversationItem({
                id: crypto.randomUUID(),
                type: 'daemon_activity',
                content: d.current_action as string,
                timestamp: new Date(),
                daemonId: (d.daemon_id as string) || (d.id as string),
                daemonRole: d.role as string,
              });
            }
          }
          break;

        case 'daemon_thinking':
          if (d) {
            updateDaemon(d);
            if (d.content) {
              addConversationItem({
                id: crypto.randomUUID(),
                type: 'daemon_activity',
                content: d.content as string,
                timestamp: new Date(),
                daemonId: (d.daemon_id as string) || (d.id as string),
                daemonRole: d.role as string,
              });
            }
          }
          break;

        case 'daemon_tool_call':
          if (d) {
            updateDaemon(d);
            addConversationItem({
              id: crypto.randomUUID(),
              type: 'daemon_activity',
              content: (d.current_action as string) || `Using ${d.tool_name}...`,
              timestamp: new Date(),
              daemonId: (d.daemon_id as string) || (d.id as string),
              daemonRole: d.role as string,
              toolName: d.tool_name as string,
            });
          }
          break;

        case 'daemon_tool_result':
          if (d) {
            updateDaemon(d);
            addConversationItem({
              id: crypto.randomUUID(),
              type: 'daemon_activity',
              content: `${d.tool_name} completed`,
              timestamp: new Date(),
              daemonId: (d.daemon_id as string) || (d.id as string),
              daemonRole: d.role as string,
              toolName: d.tool_name as string,
              toolResult: d.tool_result as string,
            });
          }
          break;

        case 'daemon_completed':
          if (d) {
            updateDaemon({ ...d, status: 'completed', progress: 1 });
            const result = d.result as Record<string, unknown> | undefined;
            addConversationItem({
              id: crypto.randomUUID(),
              type: 'daemon_activity',
              content: (result?.summary as string) || `${d.role} completed`,
              timestamp: new Date(),
              daemonId: (d.daemon_id as string) || (d.id as string),
              daemonRole: d.role as string,
            });
          }
          break;

        case 'daemon_failed':
          if (d) {
            updateDaemon({ ...d, status: 'failed' });
            addConversationItem({
              id: crypto.randomUUID(),
              type: 'error',
              content: `Daemon failed: ${d.error}`,
              timestamp: new Date(),
              daemonId: d.daemon_id as string,
            });
          }
          break;

        case 'daemon_cancelled':
          if (d?.daemon_id) {
            updateDaemon({ daemon_id: d.daemon_id, id: d.daemon_id, status: 'cancelled' });
            addConversationItem({
              id: crypto.randomUUID(),
              type: 'daemon_activity',
              content: 'Daemon cancelled by user',
              timestamp: new Date(),
              daemonId: d.daemon_id as string,
              daemonRole: d.role as string,
            });
          }
          break;

        case 'daemon_detail':
          // Full daemon object — update store
          if (d) updateDaemon(d);
          break;

        case 'task_created':
        case 'task_updated':
          if (d) updateTask(d);
          break;

        case 'task_completed':
          if (d) {
            // Preserve result in conversation history so follow-ups don't lose the previous response
            const taskResult = d.result as Record<string, unknown> | undefined;
            const resultSummary = taskResult?.summary as string | undefined;
            if (resultSummary) {
              addConversationItem({
                id: crypto.randomUUID(),
                type: 'task_result',
                content: resultSummary,
                timestamp: new Date(),
                taskId: (d.task_id as string) || (d.id as string),
              });
            }
            updateTask({ ...d, status: 'completed' });
            setIsProcessing(false);
            // Toast so the user notices completion without manually scanning the
            // kanban — Nexus tasks finish in the background and previously the
            // only signal was the card silently moving to the (often collapsed)
            // Done column.
            toast.success(
              resultSummary
                ? truncateForToast(resultSummary)
                : 'Click the task card to view results.',
              'Task complete'
            );
          }
          break;

        case 'task_failed':
          if (d) {
            updateTask({ ...d, status: 'failed' });
            addConversationItem({
              id: crypto.randomUUID(),
              type: 'error',
              content: `Task failed: ${d.error}`,
              timestamp: new Date(),
              taskId: d.task_id as string,
            });
            setIsProcessing(false);
          }
          break;

        case 'task_status_changed':
          if (d?.task_id && d?.status) {
            updateTask({ task_id: d.task_id, status: d.status });
          }
          break;

        case 'retry_started':
          if (d?.original_task_id) {
            addConversationItem({
              id: crypto.randomUUID(),
              type: 'cortex_thinking',
              content: 'Retrying task...',
              timestamp: new Date(),
              taskId: d.original_task_id as string,
            });
          }
          break;

        case 'persona_updated':
          if (d?.facts) setPersona(d.facts as unknown[]);
          break;

        case 'engram_updated':
          if (d?.entries) setEngrams(d.entries as unknown[]);
          break;

        case 'bridge_state_updated':
          if (d?.bridge_connected !== undefined) {
            setBridgeConnected(d.bridge_connected as boolean);
          }
          // Re-fetch session state (includes fresh engrams) when bridge syncs
          if (wsRef.current?.readyState === WebSocket.OPEN) {
            wsRef.current.send(JSON.stringify({ type: 'get_session' }));
          }
          break;

        case 'missed_updates':
          if (d?.updates) {
            setMissedUpdates(d.updates as MissedUpdate[]);
          }
          break;

        case 'error':
          addConversationItem({
            id: crypto.randomUUID(),
            type: 'error',
            content: (d?.message as string) || 'Unknown error',
            timestamp: new Date(),
          });
          setIsProcessing(false);
          break;

        default:
          break;
      }

      // Notify external handlers
      handlersRef.current.forEach(h => h(msg));
    },
    [
      setConnected,
      setBridgeConnected,
      setSessionState,
      addConversationItem,
      updateDaemon,
      addDaemon,
      updateTask,
      setPersona,
      setEngrams,
      setClassification,
      setIsProcessing,
      setMissedUpdates,
    ]
  );

  const connectRef = useRef<() => void>(() => {});

  const connect = useCallback(() => {
    // Prevent duplicate connections (React StrictMode double-mounts effects)
    if (
      wsRef.current?.readyState === WebSocket.OPEN ||
      wsRef.current?.readyState === WebSocket.CONNECTING
    )
      return;

    // Always read the latest token from the ref (not the stale closure)
    const token = authTokenRef.current;
    if (!token) return;

    const url = `${WS_URL}${WS_ENDPOINT}?token=${token}`;
    const ws = new WebSocket(url);
    wsRef.current = ws;

    ws.onopen = () => {
      reconnectAttemptsRef.current = 0;
      // Start heartbeat
      heartbeatRef.current = setInterval(() => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'ping' }));
        }
      }, HEARTBEAT_INTERVAL);
    };

    ws.onmessage = event => {
      try {
        const msg: NexusServerMessage = JSON.parse(event.data);
        processMessage(msg);
      } catch {
        console.error('[Nexus WS] Failed to parse message');
      }
    };

    ws.onclose = () => {
      setConnected(false);
      if (heartbeatRef.current) clearInterval(heartbeatRef.current);
      // Reconnect with exponential backoff — cap at MAX_RECONNECT_ATTEMPTS
      if (reconnectAttemptsRef.current >= MAX_RECONNECT_ATTEMPTS) {
        console.warn('[Nexus WS] Max reconnect attempts reached, giving up');
        return;
      }
      const delay = Math.min(1000 * Math.pow(2, reconnectAttemptsRef.current), MAX_RECONNECT_DELAY);
      reconnectAttemptsRef.current++;
      reconnectTimerRef.current = setTimeout(() => connectRef.current(), delay);
    };

    ws.onerror = () => {
      ws.close();
    };
  }, [processMessage, setConnected]);

  // Keep connectRef in sync so reconnect closures always call the latest connect
  connectRef.current = connect;

  const disconnect = useCallback(() => {
    if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current);
    if (heartbeatRef.current) clearInterval(heartbeatRef.current);
    if (wsRef.current) {
      wsRef.current.onclose = null;
      wsRef.current.close();
      wsRef.current = null;
    }
    setConnected(false);
  }, [setConnected]);

  const send = useCallback((message: NexusClientMessage) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(message));
    }
  }, []);

  const sendMessage = useCallback(
    (content: string, modelId?: string) => {
      addConversationItem({
        id: crypto.randomUUID(),
        type: 'user_message',
        content,
        timestamp: new Date(),
      });
      send({ type: 'send_message', content, model_id: modelId });
    },
    [send, addConversationItem]
  );

  const createTask = useCallback(
    (opts: {
      content: string;
      modelId?: string;
      tools?: string[];
      skillIds?: string[];
      daemonMode?: 'auto' | 'quick' | 'daemon' | 'multi_daemon';
      templateId?: string;
      priority?: number;
      scheduledAt?: string;
      projectId?: string;
      saveIds?: string[];
    }) => {
      addConversationItem({
        id: crypto.randomUUID(),
        type: 'user_message',
        content: opts.content,
        timestamp: new Date(),
      });
      send({
        type: 'send_message',
        content: opts.content,
        model_id: opts.modelId,
        tools: opts.tools,
        skill_ids: opts.skillIds,
        daemon_mode: opts.daemonMode,
        template_id: opts.templateId,
        priority: opts.priority,
        scheduled_at: opts.scheduledAt,
        project_id: opts.projectId,
        save_ids: opts.saveIds,
      });
    },
    [send, addConversationItem]
  );

  const updateTaskStatus = useCallback(
    (taskId: string, status: NexusTaskStatus) => {
      // Optimistic update — move card instantly in the UI
      useNexusStore.getState().updateTask({ task_id: taskId, status });
      send({ type: 'update_task_status', task_id: taskId, status });
    },
    [send]
  );

  const retryTask = useCallback(
    (taskId: string) => {
      send({ type: 'retry_task', task_id: taskId });
    },
    [send]
  );

  const cancelDaemon = useCallback(
    (daemonId: string) => send({ type: 'cancel_daemon', daemon_id: daemonId }),
    [send]
  );

  const cancelTask = useCallback(
    (taskId: string) => {
      // Optimistic update — move task to cancelled immediately
      useNexusStore.getState().updateTask({ task_id: taskId, status: 'cancelled' });
      send({ type: 'update_task_status', task_id: taskId, status: 'cancelled' });
      // Cancel all executing daemons for this task
      const allDaemons = useNexusStore.getState().daemons;
      for (const daemon of Object.values(allDaemons)) {
        if (daemon.task_id === taskId && daemon.status === 'executing') {
          send({ type: 'cancel_daemon', daemon_id: daemon.id! });
        }
      }
    },
    [send]
  );

  const cancelAll = useCallback(() => send({ type: 'cancel_all' }), [send]);

  const explainTask = useCallback(
    (taskId: string) => send({ type: 'explain_task', task_id: taskId }),
    [send]
  );

  const requestSession = useCallback(() => send({ type: 'get_session' }), [send]);

  const onMessage = useCallback((handler: MessageHandler) => {
    handlersRef.current.add(handler);
    return () => {
      handlersRef.current.delete(handler);
    };
  }, []);

  // Auto-connect/disconnect on auth token change.
  //
  // React 18 StrictMode double-mounts effects in dev: mount → cleanup → mount.
  // The naive `return () => disconnect()` causes the WS to open, immediately
  // close (during the first cleanup), then reopen on the second mount —
  // visible in backend logs as `Connection opened ... Connection closed ...
  // Connection opened` within ~12ms, and visible to users as tasks briefly
  // disappearing on tab switch / page navigation.
  //
  // Fix: defer disconnect by 100 ms with a generation guard. If a remount
  // happens within that window (the StrictMode case), the deferred disconnect
  // detects the new generation and bails. In production (no StrictMode
  // remount), the disconnect fires normally.
  const mountGenRef = useRef(0);
  useEffect(() => {
    if (!authToken) return;
    mountGenRef.current += 1;
    const thisGen = mountGenRef.current;
    connect();
    return () => {
      setTimeout(() => {
        if (mountGenRef.current === thisGen) {
          disconnect();
        }
      }, 100);
    };
  }, [authToken, connect, disconnect]);

  // Re-poll session state when browser tab resumes (e.g. after PC sleep/wake)
  useEffect(() => {
    const handleVisibility = () => {
      if (document.visibilityState === 'visible' && wsRef.current?.readyState === WebSocket.OPEN) {
        wsRef.current.send(JSON.stringify({ type: 'get_session' }));
      }
    };
    document.addEventListener('visibilitychange', handleVisibility);
    return () => document.removeEventListener('visibilitychange', handleVisibility);
  }, []);

  return {
    send,
    sendMessage,
    createTask,
    updateTaskStatus,
    retryTask,
    cancelDaemon,
    cancelTask,
    cancelAll,
    explainTask,
    requestSession,
    onMessage,
    disconnect,
  };
}
