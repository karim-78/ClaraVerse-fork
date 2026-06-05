import type {
  ServerMessage,
  ClientMessage,
  Attachment,
  WebSocketState,
  NewConversationPayload,
  ChatMessagePayload,
  InteractivePromptResponsePayload,
  InteractiveAnswer,
} from '@/types/websocket';
import type { PromptAnswer } from '@/types/interactivePrompt';
import { useSubscriptionStore } from '@/store/useSubscriptionStore';
import { toast } from '@/store/useToastStore';
import { getWsUrl } from '@/lib/config';

type MessageCallback = (message: ServerMessage) => void;
type StateCallback = (state: WebSocketState) => void;

const WS_URL = getWsUrl();
const WS_ENDPOINT = '/ws/chat';

class WebSocketService {
  private ws: WebSocket | null = null;
  private conversationId: string | null = null;
  private messageCallbacks: Set<MessageCallback> = new Set();
  private stateCallbacks: Set<StateCallback> = new Set();
  private reconnectAttempts = 0;
  private maxReconnectAttempts = 100; // Increased from 5 to 100 to handle long backend restarts
  private reconnectDelay = 1000;
  private maxReconnectDelay = 30000; // Cap delay at 30 seconds
  private authToken: string | null = null;
  private state: WebSocketState = 'disconnected';
  private tokenGetter: (() => string | null) | null = null;

  // Stream resume support
  private streamingConversationId: string | null = null;
  private resumeTimeout: ReturnType<typeof setTimeout> | null = null;

  // Outgoing send queue — messages submitted while the socket is down are
  // held here and flushed on reconnect so a network blip never loses a send.
  private pendingSends: string[] = [];
  private readonly MAX_PENDING_SENDS = 20;

  // Heartbeat support - keeps connection alive through proxies
  private heartbeatInterval: ReturnType<typeof setInterval> | null = null;
  private lastPongTime: number = Date.now();
  private missedPongs = 0;
  private readonly HEARTBEAT_INTERVAL = 25000; // 25 seconds (under typical 60s proxy timeout)
  private readonly MAX_MISSED_PONGS = 2; // Reconnect after 2 missed pongs

  /**
   * Connect to WebSocket server
   * @param authToken Optional JWT token for authenticated access
   */
  connect(authToken?: string | null): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      console.log('WebSocket already connected');
      return;
    }

    this.authToken = authToken || null;
    this.setState('connecting');

    const url = authToken
      ? `${WS_URL}${WS_ENDPOINT}?token=${authToken}`
      : `${WS_URL}${WS_ENDPOINT}`;

    console.log('Connecting to WebSocket:', url.replace(/token=.*/, 'token=***'));

    try {
      this.ws = new WebSocket(url);
      this.setupEventHandlers();
    } catch (error) {
      console.error('WebSocket connection error:', error);
      this.setState('error');
      this.handleReconnect();
    }
  }

  /**
   * Disconnect from WebSocket server
   */
  disconnect(): void {
    this.stopHeartbeat();
    if (this.ws) {
      this.ws.close();
      this.ws = null;
      this.conversationId = null;
      this.setState('disconnected');
    }
  }

  /**
   * Start heartbeat to keep connection alive through proxies
   */
  private startHeartbeat(): void {
    this.stopHeartbeat();
    this.lastPongTime = Date.now();
    this.missedPongs = 0;

    this.heartbeatInterval = setInterval(() => {
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
        this.stopHeartbeat();
        return;
      }

      // Check if we've missed too many pongs (connection might be dead)
      const timeSinceLastPong = Date.now() - this.lastPongTime;
      if (timeSinceLastPong > this.HEARTBEAT_INTERVAL * (this.MAX_MISSED_PONGS + 1)) {
        console.warn(`⚠️ No pong received in ${timeSinceLastPong}ms, forcing reconnect`);
        this.missedPongs++;
        if (this.missedPongs >= this.MAX_MISSED_PONGS) {
          console.error('❌ Too many missed pongs, reconnecting...');
          this.ws.close();
          return;
        }
      }

      // Send ping message (as JSON since browser WebSocket doesn't support ping frames)
      try {
        this.ws.send(JSON.stringify({ type: 'ping', timestamp: Date.now() }));
      } catch (error) {
        console.error('❌ Failed to send heartbeat:', error);
      }
    }, this.HEARTBEAT_INTERVAL);

    console.log('💓 Heartbeat started');
  }

  /**
   * Stop heartbeat
   */
  private stopHeartbeat(): void {
    if (this.heartbeatInterval) {
      clearInterval(this.heartbeatInterval);
      this.heartbeatInterval = null;
      console.log('💔 Heartbeat stopped');
    }
  }

  /**
   * Start a new conversation
   * @param conversationId UUID for the conversation
   * @param modelId Model ID to use
   * @param systemInstructions Optional custom system prompt
   */
  startNewConversation(conversationId: string, modelId: string, systemInstructions?: string): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      console.error('WebSocket not connected');
      return;
    }

    this.conversationId = conversationId;

    const message: NewConversationPayload = {
      type: 'new_conversation',
      conversation_id: conversationId,
      model_id: modelId,
    };

    if (systemInstructions) {
      message.system_instructions = systemInstructions;
    }

    console.log('Starting new conversation:', conversationId);
    this.ws.send(JSON.stringify(message));
  }

  /**
   * Send a chat message with conversation history
   * @param content Message content
   * @param modelId Model ID to use
   * @param conversationId Conversation UUID
   * @param history Previous messages for context
   * @param systemInstructions Optional custom system prompt
   * @param attachments Optional file attachments
   * @param customConfig Optional custom provider config (BYOK)
   * @param disableTools Optional flag to disable tools (for agent builder)
   */
  sendMessageWithHistory(
    content: string,
    modelId: string,
    conversationId: string,
    history: Array<{ role: 'user' | 'assistant'; content: string }>,
    systemInstructions?: string,
    attachments?: Attachment[],
    customConfig?: { base_url: string; api_key: string; model: string },
    disableTools?: boolean,
    selectedTools?: string[],
    /**
     * Project IDs whose RAG knowledge bases should be available to
     * the LLM via search_knowledge for this turn. Empty/undefined =
     * no knowledge tool surfaced. The backend builds the tool
     * factory bound to (user, project_ids) at request time.
     */
    knowledgeProjectIds?: string[]
  ): void {
    this.conversationId = conversationId;

    const message: ChatMessagePayload = {
      type: 'chat_message',
      conversation_id: conversationId,
      content,
      model_id: modelId,
      history,
    };

    if (systemInstructions) {
      message.system_instructions = systemInstructions;
    }

    if (attachments && attachments.length > 0) {
      message.attachments = attachments;
    }

    if (customConfig) {
      message.custom_config = customConfig;
    }

    if (disableTools) {
      message.disable_tools = true;
    }

    if (selectedTools && selectedTools.length > 0) {
      message.selected_tools = selectedTools;
    }

    if (knowledgeProjectIds && knowledgeProjectIds.length > 0) {
      message.knowledge_project_ids = knowledgeProjectIds;
    }

    console.log('Sending message with history:', {
      conversationId,
      historyLength: history.length,
      hasCustomConfig: !!customConfig,
      disableTools: !!disableTools,
    });
    this.queueOrSend(JSON.stringify(message));
  }

  /**
   * Send now if the socket is open; otherwise queue the payload and ensure a
   * reconnect is in flight. The queue is flushed in onopen, so a transient
   * disconnect never loses the user's message.
   */
  private queueOrSend(payload: string): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(payload);
      return;
    }
    console.warn('⚠️ WebSocket not open — queuing message for reconnect');
    if (this.pendingSends.length < this.MAX_PENDING_SENDS) {
      this.pendingSends.push(payload);
    }
    this.ensureConnected();
  }

  /** Kick off a reconnect if the socket is missing or fully closed. */
  private ensureConnected(): void {
    const rs = this.ws?.readyState;
    if (rs === WebSocket.OPEN || rs === WebSocket.CONNECTING) return;
    const token = this.tokenGetter ? this.tokenGetter() : this.authToken;
    this.connect(token);
  }

  /**
   * Send a chat message (legacy method - maintained for backward compatibility)
   * @param content Message content
   * @param modelId Model ID to use
   * @param attachments Optional file attachments
   * @deprecated Use sendMessageWithHistory instead
   */
  sendMessage(content: string, modelId: string, attachments?: Attachment[]): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      console.error('WebSocket not connected');
      return;
    }

    const message: ChatMessagePayload = {
      type: 'chat_message',
      conversation_id: this.conversationId,
      content,
      model_id: modelId,
    };

    if (attachments && attachments.length > 0) {
      message.attachments = attachments;
    }

    console.log('Sending message:', message);
    this.ws.send(JSON.stringify(message));
  }

  /**
   * Stop current generation
   */
  stopGeneration(): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN || !this.conversationId) {
      console.error('Cannot stop generation: WebSocket not connected or no active conversation');
      return;
    }

    const message: ClientMessage = {
      type: 'stop_generation',
      conversation_id: this.conversationId,
    };

    console.log('Stopping generation');
    this.ws.send(JSON.stringify(message));
  }

  /**
   * Send interactive prompt response
   * @param promptId The prompt ID from the server
   * @param conversationId The conversation ID
   * @param answers User's answers to questions
   * @param skipped Whether user skipped/cancelled the prompt
   */
  sendPromptResponse(
    promptId: string,
    conversationId: string,
    answers: Record<string, PromptAnswer>,
    skipped: boolean = false
  ): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      console.error('WebSocket not connected');
      return;
    }

    // Convert PromptAnswer to InteractiveAnswer format
    const formattedAnswers: Record<string, InteractiveAnswer> = {};

    for (const [questionId, answer] of Object.entries(answers)) {
      formattedAnswers[questionId] = {
        question_id: questionId,
        value: answer.value,
        is_other: answer.isOther,
      };
    }

    const message: InteractivePromptResponsePayload = {
      type: 'interactive_prompt_response',
      prompt_id: promptId,
      conversation_id: conversationId,
      answers: formattedAnswers,
      skipped,
    };

    console.log('Sending prompt response:', {
      promptId,
      skipped,
      answerCount: Object.keys(answers).length,
    });
    this.ws.send(JSON.stringify(message));
  }

  /**
   * Register callback for incoming messages
   * @param callback Function to call when message received
   * @returns Unsubscribe function
   */
  onMessage(callback: MessageCallback): () => void {
    this.messageCallbacks.add(callback);
    return () => this.messageCallbacks.delete(callback);
  }

  /**
   * Register callback for state changes
   * @param callback Function to call when state changes
   * @returns Unsubscribe function
   */
  onStateChange(callback: StateCallback): () => void {
    this.stateCallbacks.add(callback);
    return () => this.stateCallbacks.delete(callback);
  }

  /**
   * Get current conversation ID
   */
  getConversationId(): string | null {
    return this.conversationId;
  }

  /**
   * Get current connection state
   */
  getState(): WebSocketState {
    return this.state;
  }

  /**
   * Check if WebSocket is connected
   */
  isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
  }

  /**
   * Set a token getter function for fresh token retrieval
   * Used during reconnection to ensure we always use the latest token
   * @param getter Function that returns the current auth token
   */
  setTokenGetter(getter: () => string | null): void {
    this.tokenGetter = getter;
  }

  /**
   * Set the streaming conversation ID (called when streaming starts)
   * This allows the service to request resume on reconnection
   * @param conversationId The conversation ID that is currently streaming
   */
  setStreamingConversation(conversationId: string): void {
    this.streamingConversationId = conversationId;
    console.log('📦 [STREAM] Tracking streaming conversation:', conversationId);
  }

  /**
   * Clear the streaming conversation ID (called when streaming ends or is interrupted)
   */
  clearStreamingConversation(): void {
    if (this.streamingConversationId) {
      console.log('📦 [STREAM] Cleared streaming conversation:', this.streamingConversationId);
    }
    this.streamingConversationId = null;
    if (this.resumeTimeout) {
      clearTimeout(this.resumeTimeout);
      this.resumeTimeout = null;
    }
  }

  /**
   * Get the current streaming conversation ID
   */
  getStreamingConversationId(): string | null {
    return this.streamingConversationId;
  }

  /**
   * Request to resume a stream after reconnection
   * @param conversationId The conversation ID to resume
   */
  private requestStreamResume(conversationId: string): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      console.error('Cannot resume stream: WebSocket not connected');
      return;
    }

    console.log('🔄 [RESUME] Requesting stream resume for:', conversationId);

    const message: ClientMessage = {
      type: 'resume_stream',
      conversation_id: conversationId,
    };

    this.ws.send(JSON.stringify(message));

    // Set a timeout for resume response - if no response in 5s, treat as failed
    this.resumeTimeout = setTimeout(() => {
      console.warn('⚠️ [RESUME] Resume timeout for:', conversationId);
      // Emit a synthetic stream_missed message so the UI can handle it
      this.handleMessage({
        type: 'stream_missed',
        conversation_id: conversationId,
        reason: 'timeout',
      } as ServerMessage);
      this.clearStreamingConversation();
    }, 5000);
  }

  /**
   * Update auth token and reconnect if needed
   * Called when auth state changes (e.g., token refresh)
   * @param newToken The new auth token
   */
  updateAuthToken(newToken: string | null): void {
    // Skip if token hasn't changed
    if (this.authToken === newToken) {
      return;
    }

    console.log('Auth token changed, updating WebSocket...');
    this.authToken = newToken;

    // Reconnect with new token if currently connected
    if (this.ws?.readyState === WebSocket.OPEN) {
      console.log('Reconnecting WebSocket with new token...');
      this.disconnect();
      this.connect(newToken);
    }
  }

  /**
   * Setup WebSocket event handlers
   */
  private setupEventHandlers(): void {
    if (!this.ws) return;

    this.ws.onopen = () => {
      console.log('WebSocket connected');
      this.setState('connected');
      this.reconnectAttempts = 0;

      // Start heartbeat to keep connection alive through proxies
      this.startHeartbeat();

      // Flush any sends that were queued while the socket was down so a
      // transient drop never loses the user's message.
      if (this.pendingSends.length > 0) {
        console.log(`📤 [QUEUE] Flushing ${this.pendingSends.length} queued message(s)`);
        const queued = this.pendingSends;
        this.pendingSends = [];
        for (const payload of queued) {
          try {
            this.ws?.send(payload);
          } catch (err) {
            console.error('❌ [QUEUE] Failed to flush queued message:', err);
          }
        }
      }

      // Check if we were streaming and need to resume
      if (this.streamingConversationId) {
        console.log('🔄 [RESUME] Reconnected while streaming, requesting resume...');
        this.requestStreamResume(this.streamingConversationId);
      }
    };

    this.ws.onmessage = event => {
      // Any message from server = connection is alive
      this.lastPongTime = Date.now();
      this.missedPongs = 0;

      try {
        const message: ServerMessage = JSON.parse(event.data);

        // Handle pong silently (don't pass to message handlers)
        if ((message as { type: string }).type === 'pong') {
          return;
        }

        this.handleMessage(message);
      } catch (error) {
        console.error('Error parsing WebSocket message:', error);
      }
    };

    this.ws.onerror = error => {
      console.error('WebSocket error:', error);
      this.setState('error');
    };

    this.ws.onclose = () => {
      console.log('WebSocket disconnected');
      this.setState('disconnected');
      this.conversationId = null;
      this.handleReconnect();
    };
  }

  /**
   * Handle incoming message from server
   */
  private handleMessage(message: ServerMessage): void {
    // Disabled: Too verbose during streaming - logs every chunk
    // console.log('WebSocket message:', message);

    // Note: Conversation ID comes from client (not from server's "connected" message)
    // The client sets conversationId when calling sendMessageWithHistory() or startNewConversation()

    // Handle limit_exceeded message (authenticated users only — guest limits are client-side)
    if (message.type === 'limit_exceeded') {
      console.log('⚠️ [LIMIT] Usage limit exceeded:', message.code);

      // Extract limit type from code (e.g., "message_limit_exceeded" -> "messages")
      const limitType = message.code.replace('_limit_exceeded', '') as
        | 'messages'
        | 'file_uploads'
        | 'image_generations';

      // Update subscription store with limit exceeded data
      if (message.arguments) {
        useSubscriptionStore.getState().setLimitExceeded({
          type: limitType,
          limit: message.arguments.limit,
          used: message.arguments.used,
          resetAt: message.arguments.reset_at,
          suggestedTier: message.arguments.upgrade_to,
        });
      }

      // Show toast notification for immediate feedback
      toast.error(message.message);
    }

    // Handle stream resume response - clear the timeout
    if (message.type === 'stream_resume') {
      console.log('📦 [RESUME] Received stream_resume response');
      if (this.resumeTimeout) {
        clearTimeout(this.resumeTimeout);
        this.resumeTimeout = null;
      }
      // If stream is complete, clear the streaming state
      if (message.is_complete) {
        this.clearStreamingConversation();
      }
    }

    // Handle stream_missed - clear streaming state
    if (message.type === 'stream_missed') {
      console.log('⚠️ [RESUME] Stream missed:', message.reason);
      this.clearStreamingConversation();
    }

    // Handle stream_end - clear streaming state
    if (message.type === 'stream_end') {
      this.clearStreamingConversation();
    }

    // Notify all registered callbacks
    this.messageCallbacks.forEach(callback => {
      try {
        callback(message);
      } catch (error) {
        console.error('Error in message callback:', error);
      }
    });
  }

  /**
   * Handle reconnection with exponential backoff
   */
  private handleReconnect(): void {
    if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      console.error('Max reconnection attempts reached');
      return;
    }

    this.reconnectAttempts++;
    // Exponential backoff with cap
    const backoffDelay = this.reconnectDelay * Math.pow(2, this.reconnectAttempts - 1);
    const delay = Math.min(backoffDelay, this.maxReconnectDelay);

    console.log(
      `Reconnecting in ${delay}ms (attempt ${this.reconnectAttempts}/${this.maxReconnectAttempts})`
    );

    setTimeout(() => {
      // Use tokenGetter if available to get fresh token, otherwise fall back to cached token
      const token = this.tokenGetter ? this.tokenGetter() : this.authToken;
      this.connect(token);
    }, delay);
  }

  /**
   * Update connection state and notify callbacks
   */
  private setState(state: WebSocketState): void {
    this.state = state;
    this.stateCallbacks.forEach(callback => {
      try {
        callback(state);
      } catch (error) {
        console.error('Error in state callback:', error);
      }
    });
  }
}

// Export singleton instance
export const websocketService = new WebSocketService();
