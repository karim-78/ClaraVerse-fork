/**
 * Chat Database Service
 *
 * IndexedDB storage for chat data, replacing localStorage to avoid quota issues.
 * Uses the idb library for a clean promise-based API.
 *
 * Schema:
 * - chats: Chat metadata (id, title, dates, starred, etc.)
 * - messages: Separate store for messages with chatId index
 */

import { openDB, type IDBPDatabase } from 'idb';
import type { Chat, Message, ChatStatus } from '@/types/chat';

// Database schema types
interface ChatRecord {
  id: string;
  title: string;
  createdAt: Date;
  updatedAt: Date;
  lastActivityAt?: Date;
  systemInstructions?: string;
  backendStatus?: ChatStatus;
  isStarred?: boolean;
}

interface MessageRecord extends Message {
  chatId: string; // Foreign key to chat
}

interface MetaRecord {
  key: string;
  value: unknown;
}

// Define the DB schema structure
// Using a custom interface instead of extending DBSchema to avoid index signature conflicts
interface ChatDBSchema {
  chats: {
    key: string;
    value: ChatRecord;
    indexes: {
      'by-updatedAt': Date;
      'by-starred': boolean;
    };
  };
  messages: {
    key: string;
    value: MessageRecord;
    indexes: {
      'by-chatId': string;
      'by-timestamp': Date;
      'by-versionGroup': string;
    };
  };
  meta: {
    key: string;
    value: MetaRecord;
  };
}

const DB_NAME_PREFIX = 'claraverse-chats';
const DB_VERSION = 1;

// Current user ID for user-specific database
let currentUserId: string | null = null;

/**
 * Get the current user ID from auth storage
 */
function getCurrentUserId(): string | null {
  try {
    const authStorage = localStorage.getItem('auth-storage');
    if (authStorage) {
      const { state } = JSON.parse(authStorage);
      return state?.user?.id || null;
    }
  } catch {
    // Invalid JSON
  }
  return null;
}

/**
 * Get the database name for the current user
 * Each user gets their own isolated IndexedDB database
 */
function getDBName(): string {
  const userId = getCurrentUserId();
  if (userId) {
    return `${DB_NAME_PREFIX}-${userId}`;
  }
  // Fallback for unauthenticated users (local-only mode)
  return `${DB_NAME_PREFIX}-local`;
}

/**
 * Helper to ensure a value is a proper Date object
 * IndexedDB doesn't preserve Date objects - they get serialized
 */
function ensureDate(value: Date | string | number): Date {
  if (value instanceof Date) return value;
  return new Date(value);
}

// Singleton database instance (per user)
let dbPromise: Promise<IDBPDatabase<ChatDBSchema>> | null = null;

/**
 * Get the database instance (lazy initialization)
 * Creates a user-specific database to isolate data between users
 */
function getDB(): Promise<IDBPDatabase<ChatDBSchema>> {
  const userId = getCurrentUserId();

  // If user changed, close old connection and create new one
  if (currentUserId !== userId) {
    if (dbPromise) {
      dbPromise.then(db => db.close()).catch(() => {});
      dbPromise = null;
    }
    currentUserId = userId;
  }

  if (!dbPromise) {
    const dbName = getDBName();
    console.log(`[ChatDB] Opening database: ${dbName}`);

    dbPromise = openDB<ChatDBSchema>(dbName, DB_VERSION, {
      upgrade(db, oldVersion, newVersion) {
        console.log(`[ChatDB] Upgrading ${dbName} from v${oldVersion} to v${newVersion}`);

        // Create chats store
        if (!db.objectStoreNames.contains('chats')) {
          const chatStore = db.createObjectStore('chats', { keyPath: 'id' });
          chatStore.createIndex('by-updatedAt', 'updatedAt');
          chatStore.createIndex('by-starred', 'isStarred');
        }

        // Create messages store
        if (!db.objectStoreNames.contains('messages')) {
          const messageStore = db.createObjectStore('messages', { keyPath: 'id' });
          messageStore.createIndex('by-chatId', 'chatId');
          messageStore.createIndex('by-timestamp', 'timestamp');
          messageStore.createIndex('by-versionGroup', 'versionGroupId');
        }

        // Create meta store for migration flags, etc.
        if (!db.objectStoreNames.contains('meta')) {
          db.createObjectStore('meta', { keyPath: 'key' });
        }
      },
    });
  }
  return dbPromise;
}

/**
 * Reset the database connection (call on logout/user switch)
 */
export function resetDBConnection(): void {
  if (dbPromise) {
    dbPromise.then(db => db.close()).catch(() => {});
    dbPromise = null;
  }
  currentUserId = null;
  console.log('[ChatDB] Database connection reset');
}

// ===== CHAT OPERATIONS =====

/**
 * Save a chat (upsert - creates or updates)
 */
export async function saveChat(chat: Chat): Promise<void> {
  try {
    const db = await getDB();
    const tx = db.transaction(['chats', 'messages'], 'readwrite');

    // Save chat metadata
    const chatRecord: ChatRecord = {
      id: chat.id,
      title: chat.title,
      createdAt: chat.createdAt,
      updatedAt: chat.updatedAt,
      lastActivityAt: chat.lastActivityAt,
      systemInstructions: chat.systemInstructions,
      backendStatus: chat.backendStatus,
      isStarred: chat.isStarred,
    };
    await tx.objectStore('chats').put(chatRecord);

    // Save messages
    const messageStore = tx.objectStore('messages');
    for (const message of chat.messages) {
      const messageRecord: MessageRecord = {
        ...message,
        chatId: chat.id,
      };
      await messageStore.put(messageRecord);
    }

    await tx.done;
  } catch (error) {
    console.error('[ChatDB] Failed to save chat:', error);
    throw error;
  }
}

/**
 * Save multiple chats in a batch transaction
 */
export async function saveChats(chats: Chat[]): Promise<void> {
  if (chats.length === 0) return;

  try {
    const db = await getDB();
    const tx = db.transaction(['chats', 'messages'], 'readwrite');
    const chatStore = tx.objectStore('chats');
    const messageStore = tx.objectStore('messages');

    for (const chat of chats) {
      // Save chat metadata
      const chatRecord: ChatRecord = {
        id: chat.id,
        title: chat.title,
        createdAt: chat.createdAt,
        updatedAt: chat.updatedAt,
        lastActivityAt: chat.lastActivityAt,
        systemInstructions: chat.systemInstructions,
        backendStatus: chat.backendStatus,
        isStarred: chat.isStarred,
      };
      await chatStore.put(chatRecord);

      // Save messages
      for (const message of chat.messages) {
        const messageRecord: MessageRecord = {
          ...message,
          chatId: chat.id,
        };
        await messageStore.put(messageRecord);
      }
    }

    await tx.done;
    console.log(`[ChatDB] Saved ${chats.length} chats`);
  } catch (error) {
    console.error('[ChatDB] Failed to save chats:', error);
    throw error;
  }
}

/**
 * Get a chat by ID (with messages)
 */
export async function getChat(chatId: string): Promise<Chat | undefined> {
  try {
    const db = await getDB();
    const chatRecord = await db.get('chats', chatId);

    if (!chatRecord) return undefined;

    // Get messages for this chat
    const messages = await db.getAllFromIndex('messages', 'by-chatId', chatId);

    // Sort messages by timestamp
    messages.sort((a, b) => {
      const timeA =
        a.timestamp instanceof Date ? a.timestamp.getTime() : new Date(a.timestamp).getTime();
      const timeB =
        b.timestamp instanceof Date ? b.timestamp.getTime() : new Date(b.timestamp).getTime();
      return timeA - timeB;
    });

    // Reconstruct Chat object with proper Date conversion
    // (IndexedDB doesn't preserve Date objects, they become strings/numbers)
    const chat: Chat = {
      id: chatRecord.id,
      title: chatRecord.title,
      createdAt: ensureDate(chatRecord.createdAt),
      updatedAt: ensureDate(chatRecord.updatedAt),
      lastActivityAt: chatRecord.lastActivityAt ? ensureDate(chatRecord.lastActivityAt) : undefined,
      systemInstructions: chatRecord.systemInstructions,
      backendStatus: chatRecord.backendStatus,
      isStarred: chatRecord.isStarred,
      messages: messages.map(({ chatId: _, ...msg }) => ({
        ...msg,
        timestamp: ensureDate(msg.timestamp),
      })) as Message[],
    };

    return chat;
  } catch (error) {
    console.error('[ChatDB] Failed to get chat:', error);
    return undefined;
  }
}

/**
 * Get all chats (with messages)
 */
export async function getAllChats(): Promise<Chat[]> {
  try {
    const db = await getDB();
    const chatRecords = await db.getAll('chats');
    const allMessages = await db.getAll('messages');

    // Group messages by chatId
    const messagesByChat = new Map<string, MessageRecord[]>();
    for (const msg of allMessages) {
      if (!messagesByChat.has(msg.chatId)) {
        messagesByChat.set(msg.chatId, []);
      }
      messagesByChat.get(msg.chatId)!.push(msg);
    }

    // Reconstruct Chat objects
    const chats: Chat[] = chatRecords.map(chatRecord => {
      const messages = messagesByChat.get(chatRecord.id) || [];

      // Sort messages by timestamp
      messages.sort((a, b) => {
        const timeA =
          a.timestamp instanceof Date ? a.timestamp.getTime() : new Date(a.timestamp).getTime();
        const timeB =
          b.timestamp instanceof Date ? b.timestamp.getTime() : new Date(b.timestamp).getTime();
        return timeA - timeB;
      });

      return {
        id: chatRecord.id,
        title: chatRecord.title,
        createdAt: ensureDate(chatRecord.createdAt),
        updatedAt: ensureDate(chatRecord.updatedAt),
        lastActivityAt: chatRecord.lastActivityAt
          ? ensureDate(chatRecord.lastActivityAt)
          : undefined,
        systemInstructions: chatRecord.systemInstructions,
        backendStatus: chatRecord.backendStatus,
        isStarred: chatRecord.isStarred,
        messages: messages.map(({ chatId: _, ...msg }) => ({
          ...msg,
          timestamp: ensureDate(msg.timestamp),
        })) as Message[],
      };
    });

    // Sort by updatedAt (newest first)
    chats.sort((a, b) => {
      const timeA =
        a.updatedAt instanceof Date ? a.updatedAt.getTime() : new Date(a.updatedAt).getTime();
      const timeB =
        b.updatedAt instanceof Date ? b.updatedAt.getTime() : new Date(b.updatedAt).getTime();
      return timeB - timeA;
    });

    return chats;
  } catch (error) {
    console.error('[ChatDB] Failed to get all chats:', error);
    return [];
  }
}

/**
 * Delete a chat and its messages
 */
export async function deleteChat(chatId: string): Promise<void> {
  try {
    const db = await getDB();
    const tx = db.transaction(['chats', 'messages'], 'readwrite');

    // Delete chat
    await tx.objectStore('chats').delete(chatId);

    // Delete all messages for this chat
    const messageStore = tx.objectStore('messages');
    const messages = await messageStore.index('by-chatId').getAllKeys(chatId);
    for (const msgId of messages) {
      await messageStore.delete(msgId);
    }

    await tx.done;
    console.log(`[ChatDB] Deleted chat: ${chatId}`);
  } catch (error) {
    console.error('[ChatDB] Failed to delete chat:', error);
    throw error;
  }
}

// ===== MESSAGE OPERATIONS =====

/**
 * Save a single message
 */
export async function saveMessage(chatId: string, message: Message): Promise<void> {
  try {
    const db = await getDB();
    const messageRecord: MessageRecord = {
      ...message,
      chatId,
    };
    await db.put('messages', messageRecord);
  } catch (error) {
    console.error('[ChatDB] Failed to save message:', error);
    throw error;
  }
}

/**
 * Update a message
 */
export async function updateMessage(
  chatId: string,
  messageId: string,
  updates: Partial<Message>
): Promise<void> {
  try {
    const db = await getDB();
    const existing = await db.get('messages', messageId);

    if (!existing) {
      console.warn(`[ChatDB] Message not found: ${messageId}`);
      return;
    }

    const updated: MessageRecord = {
      ...existing,
      ...updates,
      chatId, // Ensure chatId is preserved
    };
    await db.put('messages', updated);
  } catch (error) {
    console.error('[ChatDB] Failed to update message:', error);
    throw error;
  }
}

/**
 * Get messages for a chat
 */
export async function getMessages(chatId: string): Promise<Message[]> {
  try {
    const db = await getDB();
    const messages = await db.getAllFromIndex('messages', 'by-chatId', chatId);

    // Sort by timestamp
    messages.sort((a, b) => {
      const timeA =
        a.timestamp instanceof Date ? a.timestamp.getTime() : new Date(a.timestamp).getTime();
      const timeB =
        b.timestamp instanceof Date ? b.timestamp.getTime() : new Date(b.timestamp).getTime();
      return timeA - timeB;
    });

    // Remove chatId from returned messages and ensure timestamps are Date objects
    return messages.map(({ chatId: _, ...msg }) => ({
      ...msg,
      timestamp: ensureDate(msg.timestamp),
    })) as Message[];
  } catch (error) {
    console.error('[ChatDB] Failed to get messages:', error);
    return [];
  }
}

/**
 * Get messages by version group (for version navigation)
 */
export async function getVersionGroup(versionGroupId: string): Promise<Message[]> {
  try {
    const db = await getDB();
    const messages = await db.getAllFromIndex('messages', 'by-versionGroup', versionGroupId);

    // Sort by version number
    messages.sort((a, b) => (a.versionNumber || 1) - (b.versionNumber || 1));

    // Ensure timestamps are Date objects
    return messages.map(({ chatId: _, ...msg }) => ({
      ...msg,
      timestamp: ensureDate(msg.timestamp),
    })) as Message[];
  } catch (error) {
    console.error('[ChatDB] Failed to get version group:', error);
    return [];
  }
}

// ===== UTILITY OPERATIONS =====

/**
 * Clear all chat data
 */
export async function clearAll(): Promise<void> {
  try {
    const db = await getDB();
    const tx = db.transaction(['chats', 'messages'], 'readwrite');
    await tx.objectStore('chats').clear();
    await tx.objectStore('messages').clear();
    await tx.done;
    console.log('[ChatDB] Cleared all data');
  } catch (error) {
    console.error('[ChatDB] Failed to clear all:', error);
    throw error;
  }
}

/**
 * Get database statistics
 */
export async function getStats(): Promise<{
  chatCount: number;
  messageCount: number;
}> {
  try {
    const db = await getDB();
    const chatCount = await db.count('chats');
    const messageCount = await db.count('messages');
    return { chatCount, messageCount };
  } catch (error) {
    console.error('[ChatDB] Failed to get stats:', error);
    return { chatCount: 0, messageCount: 0 };
  }
}

// ===== MIGRATION =====

/**
 * Check if migration from localStorage is needed
 */
export async function needsMigration(): Promise<boolean> {
  try {
    const db = await getDB();
    const migrated = await db.get('meta', 'migrated-from-localstorage');
    return !migrated;
  } catch (error) {
    console.error('[ChatDB] Failed to check migration status:', error);
    return false;
  }
}

/**
 * Mark migration as complete
 */
export async function markMigrationComplete(): Promise<void> {
  try {
    const db = await getDB();
    await db.put('meta', { key: 'migrated-from-localstorage', value: Date.now() });
    console.log('[ChatDB] Migration marked complete');
  } catch (error) {
    console.error('[ChatDB] Failed to mark migration complete:', error);
  }
}

/**
 * Update chat metadata only (without touching messages)
 */
export async function updateChatMeta(
  chatId: string,
  updates: Partial<Omit<ChatRecord, 'id'>>
): Promise<void> {
  try {
    const db = await getDB();
    const existing = await db.get('chats', chatId);

    if (!existing) {
      console.warn(`[ChatDB] Chat not found: ${chatId}`);
      return;
    }

    const updated: ChatRecord = {
      ...existing,
      ...updates,
    };
    await db.put('chats', updated);
  } catch (error) {
    console.error('[ChatDB] Failed to update chat metadata:', error);
    throw error;
  }
}
