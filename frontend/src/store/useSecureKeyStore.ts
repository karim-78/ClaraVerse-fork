/**
 * Secure API Key Storage
 *
 * This store handles API keys separately from other settings with enhanced security:
 *
 * SECURITY MODEL:
 * 1. Keys are stored in sessionStorage by default (cleared on browser/tab close)
 * 2. Optional "Remember" stores in localStorage (user must explicitly opt-in)
 * 3. Keys are encrypted using Web Crypto API with a derived key
 * 4. Each user has isolated storage
 *
 * This is the most secure approach possible in a browser without server-side storage.
 */

import { create } from 'zustand';

// Secure key derivation using Web Crypto API
const SALT = 'claraverse-secure-v1';

/**
 * Derives an encryption key from a password using PBKDF2
 * Uses the user's session identifier as additional entropy
 */
async function deriveKey(password: string): Promise<CryptoKey> {
  const encoder = new TextEncoder();
  const keyMaterial = await crypto.subtle.importKey(
    'raw',
    encoder.encode(password),
    'PBKDF2',
    false,
    ['deriveKey']
  );

  return crypto.subtle.deriveKey(
    {
      name: 'PBKDF2',
      salt: encoder.encode(SALT),
      iterations: 100000,
      hash: 'SHA-256',
    },
    keyMaterial,
    { name: 'AES-GCM', length: 256 },
    false,
    ['encrypt', 'decrypt']
  );
}

/**
 * Encrypts a string using AES-GCM
 */
async function encryptString(plaintext: string, key: CryptoKey): Promise<string> {
  const encoder = new TextEncoder();
  const iv = crypto.getRandomValues(new Uint8Array(12));

  const encrypted = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv },
    key,
    encoder.encode(plaintext)
  );

  // Combine IV + encrypted data and encode as base64
  const combined = new Uint8Array(iv.length + encrypted.byteLength);
  combined.set(iv, 0);
  combined.set(new Uint8Array(encrypted), iv.length);

  return btoa(String.fromCharCode(...combined));
}

/**
 * Decrypts a string using AES-GCM
 */
async function decryptString(ciphertext: string, key: CryptoKey): Promise<string> {
  try {
    const combined = Uint8Array.from(atob(ciphertext), c => c.charCodeAt(0));

    const iv = combined.slice(0, 12);
    const encrypted = combined.slice(12);

    const decrypted = await crypto.subtle.decrypt({ name: 'AES-GCM', iv }, key, encrypted);

    return new TextDecoder().decode(decrypted);
  } catch {
    return '';
  }
}

// Get a unique identifier for key derivation (user-specific)
function getKeyDerivationPassword(): string {
  // Use a combination of factors for key derivation
  const factors: string[] = [];

  // User ID if authenticated
  try {
    const authStorage = localStorage.getItem('auth-storage');
    if (authStorage) {
      const { state } = JSON.parse(authStorage);
      if (state?.user?.id) {
        factors.push(state.user.id);
      }
    }
  } catch {
    // Ignore
  }

  // Browser fingerprint components (not for tracking, just for key uniqueness)
  factors.push(navigator.userAgent);
  factors.push(String(screen.width));
  factors.push(String(screen.height));
  factors.push(Intl.DateTimeFormat().resolvedOptions().timeZone);

  return factors.join('|');
}

// Storage key names
const SESSION_STORAGE_KEY = 'claraverse-secure-keys';
const LOCAL_STORAGE_KEY = 'claraverse-secure-keys-persist';

export interface SecureKeyEntry {
  providerId: string;
  encryptedKey: string;
}

interface SecureKeyState {
  // Runtime cache of decrypted keys (never persisted)
  decryptedKeys: Map<string, string>;

  // Whether keys should persist across sessions
  persistKeys: boolean;

  // Initialization
  isInitialized: boolean;

  // Actions
  initialize: () => Promise<void>;
  storeKey: (providerId: string, apiKey: string) => Promise<void>;
  getKey: (providerId: string) => string | undefined;
  removeKey: (providerId: string) => Promise<void>;
  clearAllKeys: () => void;
  setPersistKeys: (persist: boolean) => Promise<void>;
}

export const useSecureKeyStore = create<SecureKeyState>()((set, get) => ({
  decryptedKeys: new Map(),
  persistKeys: false,
  isInitialized: false,

  initialize: async () => {
    if (get().isInitialized) return;

    try {
      const password = getKeyDerivationPassword();
      const key = await deriveKey(password);

      // Check localStorage first for persistence preference
      const persistFlag = localStorage.getItem('claraverse-persist-keys');
      const shouldPersist = persistFlag === 'true';

      // Load from appropriate storage
      const storage = shouldPersist ? localStorage : sessionStorage;
      const storageKey = shouldPersist ? LOCAL_STORAGE_KEY : SESSION_STORAGE_KEY;
      const stored = storage.getItem(storageKey);

      if (stored) {
        const entries: SecureKeyEntry[] = JSON.parse(stored);
        const decryptedKeys = new Map<string, string>();

        for (const entry of entries) {
          const decrypted = await decryptString(entry.encryptedKey, key);
          if (decrypted) {
            decryptedKeys.set(entry.providerId, decrypted);
          }
        }

        set({ decryptedKeys, persistKeys: shouldPersist, isInitialized: true });
      } else {
        set({ persistKeys: shouldPersist, isInitialized: true });
      }
    } catch (error) {
      console.error('Failed to initialize secure key store:', error);
      set({ isInitialized: true });
    }
  },

  storeKey: async (providerId: string, apiKey: string) => {
    try {
      const password = getKeyDerivationPassword();
      const key = await deriveKey(password);
      const encryptedKey = await encryptString(apiKey, key);

      // Update runtime cache
      const decryptedKeys = new Map(get().decryptedKeys);
      decryptedKeys.set(providerId, apiKey);

      // Persist to storage
      const { persistKeys } = get();
      const storage = persistKeys ? localStorage : sessionStorage;
      const storageKey = persistKeys ? LOCAL_STORAGE_KEY : SESSION_STORAGE_KEY;

      // Load existing entries
      const stored = storage.getItem(storageKey);
      const entries: SecureKeyEntry[] = stored ? JSON.parse(stored) : [];

      // Update or add entry
      const existingIndex = entries.findIndex(e => e.providerId === providerId);
      if (existingIndex >= 0) {
        entries[existingIndex].encryptedKey = encryptedKey;
      } else {
        entries.push({ providerId, encryptedKey });
      }

      storage.setItem(storageKey, JSON.stringify(entries));
      set({ decryptedKeys });
    } catch (error) {
      console.error('Failed to store key:', error);
    }
  },

  getKey: (providerId: string) => {
    return get().decryptedKeys.get(providerId);
  },

  removeKey: async (providerId: string) => {
    const decryptedKeys = new Map(get().decryptedKeys);
    decryptedKeys.delete(providerId);

    // Remove from storage
    const { persistKeys } = get();
    const storage = persistKeys ? localStorage : sessionStorage;
    const storageKey = persistKeys ? LOCAL_STORAGE_KEY : SESSION_STORAGE_KEY;

    const stored = storage.getItem(storageKey);
    if (stored) {
      const entries: SecureKeyEntry[] = JSON.parse(stored);
      const filtered = entries.filter(e => e.providerId !== providerId);
      storage.setItem(storageKey, JSON.stringify(filtered));
    }

    set({ decryptedKeys });
  },

  clearAllKeys: () => {
    sessionStorage.removeItem(SESSION_STORAGE_KEY);
    localStorage.removeItem(LOCAL_STORAGE_KEY);
    set({ decryptedKeys: new Map() });
  },

  setPersistKeys: async (persist: boolean) => {
    const { decryptedKeys } = get();

    // Save preference
    localStorage.setItem('claraverse-persist-keys', String(persist));

    if (persist) {
      // Move from session to local storage
      sessionStorage.removeItem(SESSION_STORAGE_KEY);

      // Re-encrypt and store in localStorage
      const password = getKeyDerivationPassword();
      const key = await deriveKey(password);
      const entries: SecureKeyEntry[] = [];

      for (const [providerId, apiKey] of decryptedKeys) {
        const encryptedKey = await encryptString(apiKey, key);
        entries.push({ providerId, encryptedKey });
      }

      localStorage.setItem(LOCAL_STORAGE_KEY, JSON.stringify(entries));
    } else {
      // Move from local to session storage
      localStorage.removeItem(LOCAL_STORAGE_KEY);

      // Re-encrypt and store in sessionStorage
      const password = getKeyDerivationPassword();
      const key = await deriveKey(password);
      const entries: SecureKeyEntry[] = [];

      for (const [providerId, apiKey] of decryptedKeys) {
        const encryptedKey = await encryptString(apiKey, key);
        entries.push({ providerId, encryptedKey });
      }

      sessionStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify(entries));
    }

    set({ persistKeys: persist });
  },
}));
