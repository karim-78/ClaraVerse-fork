/**
 * Thumbnail Generator Utility
 *
 * Generates thumbnail previews for artifacts (HTML, SVG, Mermaid).
 * Uses different strategies for each artifact type.
 * Thumbnails are cached in IndexedDB for persistence across sessions.
 */

import { openDB, type IDBPDatabase } from 'idb';
import mermaid from 'mermaid';

import type { ArtifactImage } from '@/types/artifact';

// ===== IndexedDB Thumbnail Cache =====

interface ThumbnailCacheSchema {
  thumbnails: {
    key: string; // artifactId-type
    value: {
      id: string;
      thumbnail: string; // base64 data URL
      contentHash: string; // hash of content to detect changes
      createdAt: number;
    };
  };
}

const CACHE_DB_NAME = 'claraverse-thumbnails';
const CACHE_DB_VERSION = 1;
const CACHE_MAX_AGE_MS = 7 * 24 * 60 * 60 * 1000; // 7 days

let cacheDbPromise: Promise<IDBPDatabase<ThumbnailCacheSchema>> | null = null;

/**
 * Get the thumbnail cache database
 */
async function getCacheDB(): Promise<IDBPDatabase<ThumbnailCacheSchema>> {
  if (!cacheDbPromise) {
    cacheDbPromise = openDB<ThumbnailCacheSchema>(CACHE_DB_NAME, CACHE_DB_VERSION, {
      upgrade(db) {
        if (!db.objectStoreNames.contains('thumbnails')) {
          db.createObjectStore('thumbnails', { keyPath: 'id' });
        }
      },
    });
  }
  return cacheDbPromise;
}

/**
 * Simple hash function for content comparison
 * Uses a fast, non-cryptographic hash for change detection
 */
function hashContent(content: string): string {
  let hash = 0;
  for (let i = 0; i < content.length; i++) {
    const char = content.charCodeAt(i);
    hash = (hash << 5) - hash + char;
    hash = hash & hash; // Convert to 32bit integer
  }
  return hash.toString(36);
}

// Special marker for cache miss (distinguishes from cached empty string)
const CACHE_MISS = Symbol('CACHE_MISS');

/**
 * Get cached thumbnail from IndexedDB
 * Returns:
 * - string (including empty string) if found in cache
 * - CACHE_MISS symbol if not in cache or expired
 */
async function getFromCache(
  artifactId: string,
  type: string,
  contentHash: string
): Promise<string | typeof CACHE_MISS> {
  try {
    const db = await getCacheDB();
    const cacheKey = `${artifactId}-${type}`;
    const cached = await db.get('thumbnails', cacheKey);

    if (cached) {
      // Check if content has changed
      if (cached.contentHash !== contentHash) {
        // Content changed, invalidate cache
        await db.delete('thumbnails', cacheKey);
        return CACHE_MISS;
      }

      // Check if cache is too old
      if (Date.now() - cached.createdAt > CACHE_MAX_AGE_MS) {
        await db.delete('thumbnails', cacheKey);
        return CACHE_MISS;
      }

      // Return cached value (can be empty string for failed renders)
      return cached.thumbnail;
    }
    return CACHE_MISS;
  } catch (error) {
    console.warn('[ThumbnailCache] Failed to get from cache:', error);
    return CACHE_MISS;
  }
}

/**
 * Save thumbnail to IndexedDB cache
 */
async function saveToCache(
  artifactId: string,
  type: string,
  thumbnail: string,
  contentHash: string
): Promise<void> {
  try {
    const db = await getCacheDB();
    const cacheKey = `${artifactId}-${type}`;
    await db.put('thumbnails', {
      id: cacheKey,
      thumbnail,
      contentHash,
      createdAt: Date.now(),
    });
  } catch (error) {
    console.warn('[ThumbnailCache] Failed to save to cache:', error);
  }
}

/**
 * Clean up old cache entries (call periodically)
 */
export async function cleanupThumbnailCache(): Promise<void> {
  try {
    const db = await getCacheDB();
    const tx = db.transaction('thumbnails', 'readwrite');
    const store = tx.objectStore('thumbnails');
    const allEntries = await store.getAll();

    const now = Date.now();
    for (const entry of allEntries) {
      if (now - entry.createdAt > CACHE_MAX_AGE_MS) {
        await store.delete(entry.id);
      }
    }

    await tx.done;
  } catch (error) {
    console.warn('[ThumbnailCache] Failed to cleanup cache:', error);
  }
}

/**
 * Generate a thumbnail data URL for an artifact
 * Returns a promise that resolves to a base64 data URL
 */
export async function generateThumbnail(
  content: string,
  type: 'html' | 'svg' | 'mermaid' | 'image',
  width: number = 300,
  height: number = 200,
  images?: ArtifactImage[]
): Promise<string> {
  switch (type) {
    case 'svg':
      return generateSVGThumbnail(content, width, height);
    case 'mermaid':
      return generateMermaidThumbnail(content, width, height);
    case 'html':
      return generateHTMLThumbnail(content, width, height);
    case 'image':
      return generateImageThumbnail(images, width, height);
    default:
      return '';
  }
}

/**
 * Generate thumbnail for image artifact
 * For image artifacts, we resize the first image to thumbnail size
 */
async function generateImageThumbnail(
  images: ArtifactImage[] | undefined,
  width: number,
  height: number
): Promise<string> {
  if (!images || images.length === 0) {
    return '';
  }

  const firstImage = images[0];
  const dataUrl = `data:image/${firstImage.format};base64,${firstImage.data}`;

  return new Promise((resolve, reject) => {
    try {
      const canvas = document.createElement('canvas');
      canvas.width = width;
      canvas.height = height;
      const ctx = canvas.getContext('2d');

      if (!ctx) {
        reject(new Error('Failed to get canvas context'));
        return;
      }

      const img = new Image();
      img.onload = () => {
        // Fill background
        ctx.fillStyle = '#0a0a0a';
        ctx.fillRect(0, 0, width, height);

        // Calculate scaling to fit
        const scale = Math.min(width / img.width, height / img.height);
        const x = (width - img.width * scale) / 2;
        const y = (height - img.height * scale) / 2;

        ctx.drawImage(img, x, y, img.width * scale, img.height * scale);

        // Convert to data URL
        const thumbnailDataUrl = canvas.toDataURL('image/png', 0.8);
        resolve(thumbnailDataUrl);
      };

      img.onerror = () => {
        reject(new Error('Failed to load image'));
      };

      img.src = dataUrl;
    } catch (error) {
      reject(error);
    }
  });
}

/**
 * Generate thumbnail for SVG content
 */
async function generateSVGThumbnail(
  content: string,
  width: number,
  height: number
): Promise<string> {
  return new Promise((resolve, reject) => {
    try {
      // Create an off-screen canvas
      const canvas = document.createElement('canvas');
      canvas.width = width;
      canvas.height = height;
      const ctx = canvas.getContext('2d');

      if (!ctx) {
        reject(new Error('Failed to get canvas context'));
        return;
      }

      // Create an image from SVG using data URL to avoid CORS
      const img = new Image();
      const svgBase64 = btoa(unescape(encodeURIComponent(content)));
      const dataUrl = `data:image/svg+xml;base64,${svgBase64}`;

      img.onload = () => {
        // Fill background
        ctx.fillStyle = '#0a0a0a';
        ctx.fillRect(0, 0, width, height);

        // Calculate scaling to fit
        const scale = Math.min(width / img.width, height / img.height);
        const x = (width - img.width * scale) / 2;
        const y = (height - img.height * scale) / 2;

        ctx.drawImage(img, x, y, img.width * scale, img.height * scale);

        // Convert to data URL
        const thumbnailDataUrl = canvas.toDataURL('image/png', 0.8);
        resolve(thumbnailDataUrl);
      };

      img.onerror = () => {
        reject(new Error('Failed to load SVG'));
      };

      img.src = dataUrl;
    } catch (error) {
      reject(error);
    }
  });
}

/**
 * Generate thumbnail for Mermaid diagram
 */
async function generateMermaidThumbnail(
  content: string,
  width: number,
  height: number
): Promise<string> {
  try {
    // Generate a unique ID for this diagram
    const diagramId = `mermaid-thumb-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`;

    // Render the Mermaid diagram to SVG
    const { svg } = await mermaid.render(diagramId, content);

    // Clean up the generated element
    const element = document.getElementById(diagramId);
    if (element) {
      element.remove();
    }

    // Convert the SVG to thumbnail
    return generateSVGThumbnail(svg, width, height);
  } catch (error) {
    console.error('Failed to generate Mermaid thumbnail:', error);

    // Clean up any error elements that Mermaid might have created
    const errorElements = document.querySelectorAll('[id^="mermaid-thumb-"]');
    errorElements.forEach(el => el.remove());

    // Return empty string on error (will fall back to icon)
    return '';
  }
}

/**
 * Generate thumbnail for HTML content
 */
async function generateHTMLThumbnail(
  content: string,
  width: number,
  height: number
): Promise<string> {
  return new Promise((resolve, reject) => {
    let iframe: HTMLIFrameElement | null = null;
    let loadTimeout: ReturnType<typeof setTimeout> | null = null;
    let renderTimeout: ReturnType<typeof setTimeout> | null = null;

    const cleanup = () => {
      if (loadTimeout) clearTimeout(loadTimeout);
      if (renderTimeout) clearTimeout(renderTimeout);
      if (iframe && iframe.parentNode) {
        document.body.removeChild(iframe);
      }
      iframe = null;
    };

    try {
      // Create an off-screen iframe
      iframe = document.createElement('iframe');
      iframe.style.position = 'absolute';
      iframe.style.left = '-9999px';
      iframe.style.width = `${width * 2}px`; // Render at 2x for better quality
      iframe.style.height = `${height * 2}px`;
      iframe.style.border = 'none';
      document.body.appendChild(iframe);

      // Set a timeout for the entire operation (5 seconds)
      loadTimeout = setTimeout(() => {
        cleanup();
        reject(new Error('HTML thumbnail generation timed out'));
      }, 5000);

      // Wait for iframe to load
      iframe.onload = async () => {
        try {
          if (!iframe) return; // Already cleaned up

          const iframeDoc = iframe.contentDocument || iframe.contentWindow?.document;
          if (!iframeDoc) {
            throw new Error('Failed to access iframe document');
          }

          // Wait a bit for rendering (500ms)
          renderTimeout = setTimeout(async () => {
            try {
              if (!iframe) return; // Already cleaned up

              // Use html-to-image to capture the iframe content
              const { toPng } = await import('html-to-image');

              const dataUrl = await toPng(iframeDoc.body, {
                width: width * 2,
                height: height * 2,
                pixelRatio: 1,
                backgroundColor: '#ffffff',
              });

              // Create canvas to resize
              const canvas = document.createElement('canvas');
              canvas.width = width;
              canvas.height = height;
              const ctx = canvas.getContext('2d');

              if (!ctx) {
                throw new Error('Failed to get canvas context');
              }

              const img = new Image();
              img.onload = () => {
                if (!ctx) return;
                ctx.drawImage(img, 0, 0, width, height);
                const thumbnail = canvas.toDataURL('image/png', 0.8);
                cleanup();
                resolve(thumbnail);
              };

              img.onerror = () => {
                cleanup();
                reject(new Error('Failed to load image'));
              };

              img.src = dataUrl;
            } catch (error) {
              cleanup();
              reject(error);
            }
          }, 500);
        } catch (error) {
          cleanup();
          reject(error);
        }
      };

      // Handle iframe load errors
      iframe.onerror = () => {
        cleanup();
        reject(new Error('Failed to load iframe'));
      };

      // Write HTML content to iframe
      const iframeDoc = iframe.contentDocument || iframe.contentWindow?.document;
      if (iframeDoc) {
        iframeDoc.open();
        iframeDoc.write(content);
        iframeDoc.close();
      } else {
        cleanup();
        reject(new Error('Failed to access iframe document'));
      }
    } catch (error) {
      cleanup();
      reject(error);
    }
  });
}

/**
 * In-memory cache for generated thumbnails (fast lookup for current session)
 * Stores both successful thumbnails and failed attempts (empty string)
 */
const memoryCache = new Map<string, string>();

/**
 * Get or generate a thumbnail with multi-level caching:
 * 1. In-memory cache (fastest, current session only)
 * 2. IndexedDB cache (persistent across sessions, includes failed renders)
 * 3. Generate new thumbnail if not cached
 */
export async function getCachedThumbnail(
  artifactId: string,
  content: string,
  type: 'html' | 'svg' | 'mermaid' | 'image',
  width?: number,
  height?: number,
  images?: ArtifactImage[]
): Promise<string> {
  const cacheKey = `${artifactId}-${type}`;

  // Level 1: Check in-memory cache (fastest)
  if (memoryCache.has(cacheKey)) {
    return memoryCache.get(cacheKey)!;
  }

  // Compute content hash for change detection
  const contentToHash = type === 'image' && images?.length ? images[0].data : content;
  const contentHash = hashContent(contentToHash);

  // Level 2: Check IndexedDB cache (persistent)
  // This returns CACHE_MISS symbol if not found, or a string (including empty for failed renders)
  const cachedResult = await getFromCache(artifactId, type, contentHash);
  if (cachedResult !== CACHE_MISS) {
    // Found in IDB cache (could be empty string for failed mermaid/svg)
    // Also store in memory for faster subsequent access
    memoryCache.set(cacheKey, cachedResult);
    return cachedResult;
  }

  // Level 3: Generate new thumbnail
  try {
    const thumbnail = await generateThumbnail(content, type, width, height, images);

    // Cache in both memory and IndexedDB
    memoryCache.set(cacheKey, thumbnail);
    // Persist ALL results to IndexedDB, including empty strings for failed renders
    // This prevents re-attempting to render broken mermaid diagrams on every page load
    await saveToCache(artifactId, type, thumbnail, contentHash);

    return thumbnail;
  } catch (error) {
    console.error('Failed to generate thumbnail:', error);
    // Cache empty string in both memory and IndexedDB
    memoryCache.set(cacheKey, '');
    // Persist failure to IndexedDB so we don't retry on next page load
    await saveToCache(artifactId, type, '', contentHash);
    return '';
  }
}

/**
 * Clear thumbnail cache (both memory and IndexedDB)
 */
export async function clearThumbnailCache(): Promise<void> {
  // Clear in-memory cache
  memoryCache.clear();

  // Clear IndexedDB cache
  try {
    const db = await getCacheDB();
    const tx = db.transaction('thumbnails', 'readwrite');
    await tx.objectStore('thumbnails').clear();
    await tx.done;
  } catch (error) {
    console.warn('[ThumbnailCache] Failed to clear IndexedDB cache:', error);
  }
}
