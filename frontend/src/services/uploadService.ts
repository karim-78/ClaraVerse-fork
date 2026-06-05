import type {
  Attachment,
  ImageAttachment,
  DocumentAttachment,
  DataAttachment,
  DataPreview,
} from '@/types/websocket';
import { authClient } from '@/lib/auth';
import { storeImage } from './imageCache';
import { getApiBaseUrl } from '@/lib/config';

const API_BASE_URL = getApiBaseUrl();

export interface UploadedFile {
  file_id: string;
  filename: string;
  mime_type: string;
  size: number;
  hash?: string;
  page_count?: number; // For PDFs
  word_count?: number; // For PDFs
  preview?: string; // For PDFs
  url?: string; // For images
  conversation_id?: string;
  data_preview?: DataPreview; // For CSV files
}

export class UploadError extends Error {
  statusCode?: number;
  details?: unknown;

  constructor(message: string, statusCode?: number, details?: unknown) {
    super(message);
    this.name = 'UploadError';
    this.statusCode = statusCode;
    this.details = details;
  }
}

/**
 * Get authentication token (same source as the main API client).
 */
function getAuthToken(): string | null {
  return authClient.getAccessToken();
}

/**
 * fetch() that attaches the current bearer token and — like the main API
 * client — transparently refreshes the access token once on a 401 and retries.
 * Without this, an expired 15-min access token made uploads fail with
 * "Authentication required" until a hard refresh re-initialized auth.
 */
async function authedFetch(url: string, init: RequestInit): Promise<Response> {
  const build = (token: string | null): RequestInit => ({
    ...init,
    headers: {
      ...(init.headers || {}),
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      // NOTE: never set Content-Type here — the browser sets the multipart boundary.
    },
    credentials: 'include', // send the refresh-token cookie
  });

  let response = await fetch(url, build(getAuthToken()));
  if (response.status === 401) {
    try {
      await authClient.refreshToken();
      response = await fetch(url, build(getAuthToken()));
    } catch {
      // fall through and return the original 401
    }
  }
  return response;
}

/**
 * Upload a file (image or PDF) to the backend
 * @param file - The file to upload
 * @param conversationId - The conversation ID to associate with the file
 * @returns Upload response with file metadata
 */
export async function uploadFile(file: File, conversationId: string): Promise<UploadedFile> {
  // Validate file type
  const isPDF = file.type === 'application/pdf';
  const isImage = file.type.startsWith('image/');
  const isDOCX =
    file.type === 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' ||
    file.name.endsWith('.docx');
  const isPPTX =
    file.type === 'application/vnd.openxmlformats-officedocument.presentationml.presentation' ||
    file.name.endsWith('.pptx');
  const isCSV = file.type === 'text/csv' || file.name.endsWith('.csv');
  const isExcel =
    file.type === 'application/vnd.ms-excel' ||
    file.type === 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' ||
    file.name.endsWith('.xlsx') ||
    file.name.endsWith('.xls');
  const isJSON = file.type === 'application/json' || file.name.endsWith('.json');
  const isText = file.type === 'text/plain' || file.name.endsWith('.txt');
  const isAudio =
    file.type.startsWith('audio/') ||
    file.name.endsWith('.mp3') ||
    file.name.endsWith('.wav') ||
    file.name.endsWith('.m4a') ||
    file.name.endsWith('.ogg') ||
    file.name.endsWith('.flac') ||
    file.name.endsWith('.webm');
  const isDataFile = isCSV || isExcel || isJSON || isText;
  const isDocument = isPDF || isDOCX || isPPTX;

  if (!isDocument && !isImage && !isDataFile && !isAudio) {
    throw new UploadError(
      'Only images, PDFs, DOCX, PPTX, CSV, Excel, JSON, text, and audio files are supported'
    );
  }

  // Validate file size
  let maxSize: number;
  let fileTypeLabel: string;

  if (isDocument) {
    maxSize = 10 * 1024 * 1024; // 10MB for documents (PDF, DOCX, PPTX)
    fileTypeLabel = 'documents (PDF/DOCX/PPTX)';
  } else if (isAudio) {
    maxSize = 25 * 1024 * 1024; // 25MB for audio (OpenAI Whisper limit)
    fileTypeLabel = 'audio files';
  } else if (isDataFile) {
    maxSize = 100 * 1024 * 1024; // 100MB for data files
    fileTypeLabel = 'data files (CSV/Excel/JSON)';
  } else {
    maxSize = 20 * 1024 * 1024; // 20MB for images
    fileTypeLabel = 'images';
  }

  if (file.size > maxSize) {
    const maxSizeMB = maxSize / (1024 * 1024);
    throw new UploadError(`File size exceeds ${maxSizeMB}MB limit for ${fileTypeLabel}`);
  }

  // Create form data
  const formData = new FormData();
  formData.append('file', file);
  formData.append('conversation_id', conversationId);

  try {
    const response = await authedFetch(`${API_BASE_URL}/api/upload`, {
      method: 'POST',
      body: formData,
    });

    if (!response.ok) {
      const errorData = await response.json().catch(() => ({
        error: 'Upload failed',
      }));
      throw new UploadError(
        errorData.error || `Upload failed with status ${response.status}`,
        response.status,
        errorData
      );
    }

    const data: UploadedFile = await response.json();

    // Cache image in IndexedDB if it's an image file
    if (isImage) {
      try {
        // Convert File to Blob for caching
        const blob = file.slice(0, file.size, file.type);
        await storeImage(data.file_id, blob, {
          filename: data.filename,
          mime_type: data.mime_type,
          size: data.size,
          url: data.url || '',
        });
        console.log(`[Upload] Image cached in IndexedDB: ${data.file_id}`);
      } catch (cacheError) {
        // Don't fail the upload if caching fails
        console.error('[Upload] Failed to cache image, continuing:', cacheError);
      }
    }

    return data;
  } catch (error) {
    if (error instanceof UploadError) {
      throw error;
    }
    if (error instanceof Error) {
      throw new UploadError(`Network error: ${error.message}`);
    }
    throw new UploadError('Unknown error occurred during upload');
  }
}

/**
 * Upload multiple files in parallel
 * @param files - Array of files to upload
 * @param conversationId - The conversation ID to associate with the files
 * @returns Array of upload responses
 */
export async function uploadFiles(files: File[], conversationId: string): Promise<UploadedFile[]> {
  const uploadPromises = files.map(file => uploadFile(file, conversationId));
  return Promise.all(uploadPromises);
}

/**
 * Check if a MIME type or filename indicates a document file (PDF, DOCX, PPTX)
 */
function isDocumentMimeType(mimeType: string, filename?: string): boolean {
  const documentMimeTypes = [
    'application/pdf',
    'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
    'application/vnd.openxmlformats-officedocument.presentationml.presentation',
  ];

  // Check MIME type (strip charset if present)
  const baseMimeType = mimeType.split(';')[0].trim();
  if (documentMimeTypes.includes(baseMimeType)) {
    return true;
  }

  // Check file extension as fallback
  if (filename) {
    const ext = filename.toLowerCase();
    if (ext.endsWith('.pdf') || ext.endsWith('.docx') || ext.endsWith('.pptx')) {
      return true;
    }
  }

  return false;
}

/**
 * Check if a MIME type or filename indicates a data file (CSV, Excel, JSON, Text)
 */
function isDataFileMimeType(mimeType: string, filename?: string): boolean {
  const dataFileMimeTypes = [
    'text/csv',
    'text/plain',
    'application/json',
    'application/vnd.ms-excel',
    'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
  ];

  // Check MIME type (strip charset if present)
  const baseMimeType = mimeType.split(';')[0].trim();
  if (dataFileMimeTypes.includes(baseMimeType)) {
    return true;
  }

  // Check file extension as fallback
  if (filename) {
    const ext = filename.toLowerCase();
    if (
      ext.endsWith('.csv') ||
      ext.endsWith('.xlsx') ||
      ext.endsWith('.xls') ||
      ext.endsWith('.json') ||
      ext.endsWith('.txt')
    ) {
      return true;
    }
  }

  return false;
}

/**
 * Convert UploadedFile to Attachment type for WebSocket messages
 * @param uploadedFile - The uploaded file metadata
 * @returns Attachment object
 */
export function toAttachment(uploadedFile: UploadedFile): Attachment {
  const isDocument = isDocumentMimeType(uploadedFile.mime_type, uploadedFile.filename);
  const isDataFile = isDataFileMimeType(uploadedFile.mime_type, uploadedFile.filename);

  if (isDocument) {
    // PDF, DOCX, PPTX documents
    const attachment: DocumentAttachment = {
      type: 'document',
      file_id: uploadedFile.file_id,
      url: uploadedFile.url || '',
      mime_type: uploadedFile.mime_type,
      size: uploadedFile.size,
      filename: uploadedFile.filename,
      page_count: uploadedFile.page_count,
      word_count: uploadedFile.word_count,
      preview: uploadedFile.preview,
    };
    return attachment;
  } else if (isDataFile) {
    // Data files (CSV, Excel, JSON, Text)
    const attachment: DataAttachment = {
      type: 'data',
      file_id: uploadedFile.file_id,
      url: uploadedFile.url || '',
      mime_type: uploadedFile.mime_type,
      size: uploadedFile.size,
      filename: uploadedFile.filename,
      data_preview: uploadedFile.data_preview,
    };
    return attachment;
  } else {
    // Default to image for actual images
    const attachment: ImageAttachment = {
      type: 'image',
      file_id: uploadedFile.file_id,
      url: uploadedFile.url || '',
      mime_type: uploadedFile.mime_type,
      size: uploadedFile.size,
      filename: uploadedFile.filename,
    };
    return attachment;
  }
}

/**
 * Format file size for display
 * @param bytes - File size in bytes
 * @returns Formatted size string (e.g., "1.5 MB")
 */
export function formatFileSize(bytes: number): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`;
}

/**
 * File status response from the backend
 */
export interface FileStatusResponse {
  file_id: string;
  available: boolean;
  expired: boolean;
  error?: string;
  filename?: string;
  mime_type?: string;
  size?: number;
}

/**
 * Check if a file is still available (not expired)
 * Files in the backend cache expire after 30 minutes
 * @param fileId - The file ID to check
 * @returns File status information
 */
export async function checkFileStatus(fileId: string): Promise<FileStatusResponse> {
  try {
    const response = await authedFetch(`${API_BASE_URL}/api/upload/${fileId}/status`, {
      method: 'GET',
    });

    if (!response.ok) {
      // If the endpoint returns an error, assume the file is expired
      return {
        file_id: fileId,
        available: false,
        expired: true,
        error: `Failed to check file status (HTTP ${response.status})`,
      };
    }

    return await response.json();
  } catch (error) {
    console.error('[Upload] Failed to check file status:', error);
    // On network error, we can't determine status - assume potentially unavailable
    return {
      file_id: fileId,
      available: false,
      expired: true,
      error: error instanceof Error ? error.message : 'Network error checking file status',
    };
  }
}

/**
 * Check multiple files' status in parallel
 * @param fileIds - Array of file IDs to check
 * @returns Map of file ID to status
 */
export async function checkFilesStatus(
  fileIds: string[]
): Promise<Map<string, FileStatusResponse>> {
  const results = await Promise.all(fileIds.map(id => checkFileStatus(id)));
  const statusMap = new Map<string, FileStatusResponse>();
  results.forEach((status, index) => {
    statusMap.set(fileIds[index], status);
  });
  return statusMap;
}
