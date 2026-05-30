/**
 * Channel Service
 * Handles API calls for communication channels (Telegram, etc.)
 * Channels allow users to chat with DobbyAI from external platforms
 */

import { api } from './api';

// ============================================================================
// Types
// ============================================================================

export type ChannelPlatform = 'telegram';

export interface Channel {
  id: string;
  platform: ChannelPlatform;
  name: string;
  enabled: boolean;
  webhookUrl: string;
  botUsername?: string;
  botName?: string;
  defaultModelId?: string;
  defaultSystemPrompt?: string;
  maxHistoryMessages: number;
  allowedUsers?: string[]; // Telegram usernames or IDs allowed to use this bot
  messageCount: number;
  lastUsedAt?: string;
  createdAt: string;
}

export interface CreateChannelRequest {
  platform: ChannelPlatform;
  name?: string;
  config: Record<string, unknown>;
}

export interface UpdateChannelRequest {
  name?: string;
  enabled?: boolean;
  defaultModelId?: string;
  defaultSystemPrompt?: string;
  maxHistoryMessages?: number;
  allowedUsers?: string[]; // Telegram usernames or IDs allowed to use this bot
}

export interface TestChannelResponse {
  success: boolean;
  message: string;
  botUsername?: string;
  botName?: string;
}

export interface ListChannelsResponse {
  channels: Channel[];
  total: number;
}

// ============================================================================
// API Functions
// ============================================================================

/**
 * Create a new channel
 */
export async function createChannel(data: CreateChannelRequest): Promise<Channel> {
  return api.post<Channel>('/api/channels', data);
}

/**
 * List all channels for the current user
 */
export async function listChannels(): Promise<Channel[]> {
  const response = await api.get<ListChannelsResponse>('/api/channels');
  return response.channels || [];
}

/**
 * Get a specific channel by ID
 */
export async function getChannel(channelId: string): Promise<Channel> {
  return api.get<Channel>(`/api/channels/${channelId}`);
}

/**
 * Update a channel
 */
export async function updateChannel(
  channelId: string,
  data: UpdateChannelRequest
): Promise<Channel> {
  return api.put<Channel>(`/api/channels/${channelId}`, data);
}

/**
 * Delete a channel
 */
export async function deleteChannel(channelId: string): Promise<void> {
  await api.delete(`/api/channels/${channelId}`);
}

/**
 * Test a channel connection
 */
export async function testChannel(channelId: string): Promise<TestChannelResponse> {
  return api.post<TestChannelResponse>(`/api/channels/${channelId}/test`, {});
}

/**
 * Get channel for a specific platform (if exists)
 */
export async function getChannelByPlatform(platform: ChannelPlatform): Promise<Channel | null> {
  const channels = await listChannels();
  return channels.find(ch => ch.platform === platform) || null;
}
