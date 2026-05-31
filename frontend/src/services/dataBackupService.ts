/**
 * Data Backup Service
 * Handles encryption, backup, and restoration of user data for per-user isolation
 */

export interface UserDataBackup {
  version: number;
  userId: string;
  timestamp: number;
  data: {
    chats: string; // Encrypted chat data
    appSettings: string; // Encrypted app settings
    modelSettings: string; // Encrypted model settings
    userName: string; // Encrypted user name
  };
}

const BACKUP_VERSION = 1;
const BACKUP_KEY_PREFIX = 'backup-';

/**
 * Generate a consistent encryption key from user ID
 * This uses the user ID as a seed for the encryption key
 */
async function deriveEncryptionKey(userId: string): Promise<CryptoKey> {
  const encoder = new TextEncoder();
  const keyMaterial = await crypto.subtle.importKey(
    'raw',
    encoder.encode(userId.padEnd(32, '0')), // Ensure 32 bytes
    'PBKDF2',
    false,
    ['deriveBits', 'deriveKey']
  );

  return crypto.subtle.deriveKey(
    {
      name: 'PBKDF2',
      salt: encoder.encode('claraverse-salt'), // Fixed salt for consistency
      iterations: 100000,
      hash: 'SHA-256',
    },
    keyMaterial,
    { name: 'AES-GCM', length: 256 },
    true,
    ['encrypt', 'decrypt']
  );
}

/**
 * Encrypt data using AES-256-GCM
 */
async function encryptData(data: string, userId: string): Promise<string> {
  try {
    const key = await deriveEncryptionKey(userId);
    const encoder = new TextEncoder();
    const dataBuffer = encoder.encode(data);

    // Generate random IV (Initialization Vector)
    const iv = crypto.getRandomValues(new Uint8Array(12));

    const encryptedBuffer = await crypto.subtle.encrypt(
      {
        name: 'AES-GCM',
        iv: iv,
      },
      key,
      dataBuffer
    );

    // Combine IV + encrypted data
    const combined = new Uint8Array(iv.length + encryptedBuffer.byteLength);
    combined.set(iv, 0);
    combined.set(new Uint8Array(encryptedBuffer), iv.length);

    // Convert to base64 for storage
    return btoa(String.fromCharCode(...combined));
  } catch (error) {
    console.error('Encryption failed:', error);
    throw new Error('Failed to encrypt data');
  }
}

/**
 * Decrypt data using AES-256-GCM
 */
async function decryptData(encryptedData: string, userId: string): Promise<string> {
  try {
    const key = await deriveEncryptionKey(userId);

    // Convert from base64
    const combined = Uint8Array.from(atob(encryptedData), c => c.charCodeAt(0));

    // Extract IV and encrypted data
    const iv = combined.slice(0, 12);
    const data = combined.slice(12);

    const decryptedBuffer = await crypto.subtle.decrypt(
      {
        name: 'AES-GCM',
        iv: iv,
      },
      key,
      data
    );

    const decoder = new TextDecoder();
    return decoder.decode(decryptedBuffer);
  } catch (error) {
    console.error('Decryption failed:', error);
    throw new Error('Failed to decrypt data');
  }
}

/**
 * Create encrypted backup of all user data
 */
export async function createBackup(userId: string): Promise<void> {
  try {
    console.log('📦 Creating encrypted backup for user:', userId);

    // Gather all user data from localStorage
    const chatData = localStorage.getItem(`chat-storage-${userId}`) || '{}';
    const appData = localStorage.getItem(`app-storage-${userId}`) || '{}';
    const modelData = localStorage.getItem(`model-storage-${userId}`) || '{}';
    const userName = localStorage.getItem(`claraverse_user_name-${userId}`) || '';

    // Encrypt each piece of data
    const [encryptedChats, encryptedApp, encryptedModel, encryptedName] = await Promise.all([
      encryptData(chatData, userId),
      encryptData(appData, userId),
      encryptData(modelData, userId),
      encryptData(userName, userId),
    ]);

    // Create backup object
    const backup: UserDataBackup = {
      version: BACKUP_VERSION,
      userId,
      timestamp: Date.now(),
      data: {
        chats: encryptedChats,
        appSettings: encryptedApp,
        modelSettings: encryptedModel,
        userName: encryptedName,
      },
    };

    // Store encrypted backup
    const backupKey = `${BACKUP_KEY_PREFIX}${userId}`;
    localStorage.setItem(backupKey, JSON.stringify(backup));

    console.log('✅ Backup created successfully');
  } catch (error) {
    console.error('❌ Backup creation failed:', error);
    throw error;
  }
}

/**
 * Restore user data from encrypted backup
 */
export async function restoreBackup(userId: string): Promise<boolean> {
  try {
    console.log('📂 Restoring backup for user:', userId);

    // Check if backup exists
    const backupKey = `${BACKUP_KEY_PREFIX}${userId}`;
    const backupStr = localStorage.getItem(backupKey);

    if (!backupStr) {
      console.log('ℹ️ No backup found for user');
      return false;
    }

    const backup: UserDataBackup = JSON.parse(backupStr);

    // Verify backup version
    if (backup.version !== BACKUP_VERSION) {
      console.warn('⚠️ Backup version mismatch, skipping restore');
      return false;
    }

    // Decrypt all data
    const [chatData, appData, modelData, userName] = await Promise.all([
      decryptData(backup.data.chats, userId),
      decryptData(backup.data.appSettings, userId),
      decryptData(backup.data.modelSettings, userId),
      decryptData(backup.data.userName, userId),
    ]);

    // Restore to user-specific keys
    localStorage.setItem(`chat-storage-${userId}`, chatData);
    localStorage.setItem(`app-storage-${userId}`, appData);
    localStorage.setItem(`model-storage-${userId}`, modelData);
    localStorage.setItem(`claraverse_user_name-${userId}`, userName);

    // Delete the backup after successful restore to prevent infinite reload loops
    localStorage.removeItem(backupKey);

    console.log('✅ Backup restored successfully');
    return true;
  } catch (error) {
    console.error('❌ Backup restoration failed:', error);
    return false;
  }
}

/**
 * Clear current user's active data (called on logout)
 * If user has cloud sync enabled, also clears IndexedDB since they're likely on a shared/temp device
 */
export function clearUserData(userId: string): void {
  console.log('🗑️ Clearing active data for user:', userId);

  // Check if user has cloud sync enabled
  let isCloudMode = false;
  try {
    const settingsKey = `settings-storage-${userId}`;
    const settingsData = localStorage.getItem(settingsKey);
    if (settingsData) {
      const { state } = JSON.parse(settingsData);
      isCloudMode = state?.chatPrivacyMode === 'cloud';
    }
  } catch {
    // If we can't read settings, default to not clearing IndexedDB
  }

  // Clear localStorage items
  localStorage.removeItem(`chat-storage-${userId}`);
  localStorage.removeItem(`app-storage-${userId}`);
  localStorage.removeItem(`model-storage-${userId}`);
  localStorage.removeItem(`claraverse_user_name-${userId}`);
  localStorage.removeItem(`chat-nav-${userId}`);
  localStorage.removeItem(`chat-deleted-ids-${userId}`);
  localStorage.removeItem(`settings-storage-${userId}`);

  // Reset in-memory chat store state
  import('@/store/useChatStore').then(({ useChatStore }) => {
    useChatStore.getState().resetStore();
  });

  // If cloud sync is enabled, clear IndexedDB completely (they have backup in cloud)
  // This is for privacy on shared/temporary devices
  if (isCloudMode) {
    console.log('☁️ Cloud sync enabled - clearing local IndexedDB for privacy');
    import('./chatDatabase').then(({ clearAll, resetDBConnection }) => {
      clearAll()
        .then(() => {
          resetDBConnection();
          console.log('✅ IndexedDB cleared for cloud user');
        })
        .catch(err => {
          console.error('Failed to clear IndexedDB:', err);
          resetDBConnection();
        });
    });
  } else {
    // Local-only mode: just reset connection, keep data for next login
    import('./chatDatabase').then(({ resetDBConnection }) => {
      resetDBConnection();
    });
  }

  console.log('✅ User data cleared');
}

/**
 * Check if a backup exists for a user
 */
export function hasBackup(userId: string): boolean {
  const backupKey = `${BACKUP_KEY_PREFIX}${userId}`;
  return localStorage.getItem(backupKey) !== null;
}

/**
 * Delete a user's backup (use with caution)
 */
export function deleteBackup(userId: string): void {
  const backupKey = `${BACKUP_KEY_PREFIX}${userId}`;
  localStorage.removeItem(backupKey);
  console.log('🗑️ Backup deleted for user:', userId);
}

/**
 * Migrate legacy non-user-specific data to user-specific keys
 * Called once on first authenticated login
 */
export function migrateLegacyData(userId: string): void {
  console.log('🔄 Migrating legacy data for user:', userId);

  const legacyKeys = ['chat-storage', 'app-storage', 'model-storage', 'claraverse_user_name'];
  let migratedAny = false;

  legacyKeys.forEach(legacyKey => {
    const legacyData = localStorage.getItem(legacyKey);
    if (legacyData) {
      // Migrate to user-specific key
      const newKey =
        legacyKey === 'claraverse_user_name' ? `${legacyKey}-${userId}` : `${legacyKey}-${userId}`;
      localStorage.setItem(newKey, legacyData);

      // Remove legacy key
      localStorage.removeItem(legacyKey);
      migratedAny = true;
      console.log(`✅ Migrated ${legacyKey} → ${newKey}`);
    }
  });

  if (migratedAny) {
    console.log('✅ Legacy data migration complete');
  } else {
    console.log('ℹ️ No legacy data to migrate');
  }
}
