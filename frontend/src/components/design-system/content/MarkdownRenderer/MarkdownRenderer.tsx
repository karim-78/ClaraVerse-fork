import { forwardRef, memo, useState, useEffect, useRef, useMemo, useCallback } from 'react';
import type { HTMLAttributes } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import remarkMath from 'remark-math';
import rehypeKatex from 'rehype-katex';
import { ArrowUpRight, ZoomIn } from 'lucide-react';
import { CodeBlock } from '../CodeBlock';
import { cleanLLMOutput, cleanLLMOutputLight } from '@/utils';
import { ImageGalleryModal, type GalleryImage } from '@/components/chat/ImageGalleryModal';
import './MarkdownRenderer.css';
import 'katex/dist/katex.min.css';
import { getApiBaseUrl } from '@/lib/config';

const API_BASE_URL = getApiBaseUrl();

// Language display aliases (like OpenAI uses short names)
const LANGUAGE_ALIASES: Record<string, string> = {
  javascript: 'js',
  typescript: 'ts',
  python: 'py',
  markdown: 'md',
  dockerfile: 'docker',
  shellscript: 'sh',
  shell: 'sh',
  bash: 'bash',
  powershell: 'ps',
};

const getDisplayLanguage = (lang: string): string => {
  const lower = lang.toLowerCase();
  return LANGUAGE_ALIASES[lower] || lower;
};

/**
 * Check if URL is external (needs proxy) or local (data:, blob:, relative, same origin)
 */
function isExternalUrl(url: string): boolean {
  if (!url) return false;
  // Data URLs, blob URLs don't need proxy
  if (url.startsWith('data:') || url.startsWith('blob:')) {
    return false;
  }
  // Already a proxy URL (relative) - needs API base URL prepended
  if (url.startsWith('/api/proxy/')) {
    return false; // Will be handled separately
  }
  // Relative URLs don't need proxy
  if (url.startsWith('/')) {
    return false;
  }
  // Check if it's an absolute URL to a different origin
  try {
    const urlObj = new URL(url);
    return urlObj.origin !== window.location.origin;
  } catch {
    // Invalid URL, treat as relative
    return false;
  }
}

/**
 * Get the correct image URL - handles external URLs, already-proxied URLs, and local URLs
 */
function getProxiedImageUrl(url: string): string {
  if (!url) return url;

  // If URL is already absolute, use as-is
  if (url.startsWith('http://') || url.startsWith('https://')) {
    return url;
  }

  // If the AI already output a proxy URL (starts with /api/proxy/), prepend the API base URL
  if (url.startsWith('/api/proxy/')) {
    return `${API_BASE_URL}${url}`;
  }

  // External URLs need to go through proxy
  if (isExternalUrl(url)) {
    return `${API_BASE_URL}/api/proxy/image?url=${encodeURIComponent(url)}`;
  }

  // Local URLs (data:, blob:, relative) are used as-is
  return url;
}

// Table component with copy button
interface MarkdownTableProps {
  children: React.ReactNode;
}

function MarkdownTable({ children }: MarkdownTableProps) {
  const [copied, setCopied] = useState(false);
  const tableRef = useRef<HTMLTableElement>(null);

  const handleCopy = async () => {
    if (!tableRef.current) return;

    // Extract table data as tab-separated values
    const rows = tableRef.current.querySelectorAll('tr');
    const text = Array.from(rows)
      .map(row => {
        const cells = row.querySelectorAll('th, td');
        return Array.from(cells)
          .map(cell => cell.textContent?.trim() || '')
          .join('\t');
      })
      .join('\n');

    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (err) {
      console.error('Failed to copy table:', err);
    }
  };

  return (
    <div className="markdown-table-wrapper">
      <button
        type="button"
        onClick={handleCopy}
        className="markdown-table-copy-btn"
        aria-label="Copy table"
      >
        {copied ? (
          <svg
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
          >
            <polyline points="20 6 9 17 4 12" />
          </svg>
        ) : (
          <svg
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
          >
            <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
          </svg>
        )}
      </button>
      <table ref={tableRef}>{children}</table>
    </div>
  );
}

// Image component with error handling for broken/blocked images and click-to-zoom
interface MarkdownImageProps {
  src?: string;
  alt?: string;
  onClick?: () => void;
}

function MarkdownImage({ src, alt, onClick }: MarkdownImageProps) {
  const [hasError, setHasError] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const [isHovered, setIsHovered] = useState(false);
  const imgRef = useRef<HTMLImageElement>(null);

  // Get the actual URL to use (proxied for external, original for local)
  const imageUrl = src ? getProxiedImageUrl(src) : '';

  // Additional check for broken images that don't trigger onError
  // Some browsers don't fire onError for certain failure modes
  useEffect(() => {
    const img = imgRef.current;
    if (!img) return;

    // Check if image is already loaded but broken (naturalWidth === 0)
    const checkBroken = () => {
      if (img.complete && img.naturalWidth === 0) {
        setHasError(true);
      }
    };

    // Check immediately for cached broken images
    checkBroken();

    // Also check after a short delay for slow-loading broken images
    const timer = setTimeout(checkBroken, 3000);
    return () => clearTimeout(timer);
  }, [imageUrl]);

  // Hide broken/missing images entirely instead of showing error UI
  if (hasError || !src) {
    return null;
  }

  return (
    <div
      className={`markdown-image-container ${isHovered ? 'markdown-image-hovered' : ''}`}
      onClick={onClick}
      onMouseEnter={() => setIsHovered(true)}
      onMouseLeave={() => setIsHovered(false)}
      role="button"
      tabIndex={0}
      onKeyDown={e => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onClick?.();
        }
      }}
      aria-label={`View ${alt || 'image'} in full size`}
    >
      <img
        ref={imgRef}
        src={imageUrl}
        alt={alt || ''}
        className={`markdown-image ${isLoading ? 'markdown-image-loading' : ''}`}
        loading="lazy"
        onError={() => setHasError(true)}
        onLoad={e => {
          const img = e.currentTarget;
          // Check if image loaded but is actually broken (0x0 dimensions)
          if (img.naturalWidth === 0 || img.naturalHeight === 0) {
            setHasError(true);
          } else {
            setIsLoading(false);
          }
        }}
      />
      {/* Zoom overlay on hover */}
      <div className="markdown-image-overlay">
        <ZoomIn size={24} />
      </div>
    </div>
  );
}

export interface MarkdownRendererProps extends Omit<HTMLAttributes<HTMLDivElement>, 'children'> {
  content: string;
  isStreaming?: boolean;
}

/**
 * Extract all image URLs from markdown content
 */
function extractImagesFromMarkdown(content: string): GalleryImage[] {
  const images: GalleryImage[] = [];
  // Match markdown images: ![alt](url) or ![alt](url "title")
  const imageRegex = /!\[([^\]]*)\]\(([^)\s]+)(?:\s+"([^"]*)")?\)/g;
  let match;

  while ((match = imageRegex.exec(content)) !== null) {
    const alt = match[1];
    const src = match[2];
    const title = match[3];

    if (src) {
      images.push({
        src: getProxiedImageUrl(src),
        title: title || alt || undefined,
      });
    }
  }

  return images;
}

const MarkdownRendererComponent = forwardRef<HTMLDivElement, MarkdownRendererProps>(
  ({ content, className = '', isStreaming = false, ...props }, ref) => {
    // Gallery modal state
    const [galleryOpen, setGalleryOpen] = useState(false);
    const [galleryIndex, setGalleryIndex] = useState(0);

    // Clean up LLM output quirks - use light cleanup during streaming to avoid visual jumps
    const cleanedContent = useMemo(() => {
      return isStreaming ? cleanLLMOutputLight(content) : cleanLLMOutput(content);
    }, [content, isStreaming]);

    // Extract all images from the markdown for gallery navigation
    const galleryImages = useMemo(() => {
      return extractImagesFromMarkdown(cleanedContent);
    }, [cleanedContent]);

    // Handle image click - find the index and open gallery
    const handleImageClick = useCallback(
      (clickedSrc: string) => {
        const proxiedSrc = getProxiedImageUrl(clickedSrc);
        const index = galleryImages.findIndex(img => img.src === proxiedSrc);
        if (index !== -1) {
          setGalleryIndex(index);
          setGalleryOpen(true);
        } else {
          // Fallback: open gallery with just this image
          setGalleryIndex(0);
          setGalleryOpen(true);
        }
      },
      [galleryImages]
    );

    return (
      <>
        <div ref={ref} className={`markdown-renderer ${className}`} {...props}>
          <ReactMarkdown
            remarkPlugins={[remarkGfm, remarkMath]}
            rehypePlugins={[rehypeKatex]}
            components={{
              code(props) {
                const { className, children, ...codeProps } = props;
                const match = /language-(\w+)/.exec(className || '');
                const codeString = String(children).replace(/\n$/, '');
                const inline = !className && !match;

                // Use lightweight rendering during streaming to avoid expensive syntax highlighting
                if (isStreaming && !inline && match) {
                  const displayLang = getDisplayLanguage(match[1]);
                  return (
                    <div className="streaming-code-block">
                      <div className="streaming-code-header">
                        <span className="streaming-code-language">{displayLang}</span>
                      </div>
                      <pre className="streaming-code-content">
                        <code>{codeString}</code>
                      </pre>
                    </div>
                  );
                }

                return !inline && match ? (
                  <CodeBlock code={codeString} language={match[1]} showLineNumbers={false} />
                ) : (
                  <code className={className} {...codeProps}>
                    {children}
                  </code>
                );
              },
              table({ children }) {
                return <MarkdownTable>{children}</MarkdownTable>;
              },
              img({ src, alt }) {
                return (
                  <MarkdownImage src={src} alt={alt} onClick={() => src && handleImageClick(src)} />
                );
              },
              a({ href, children }) {
                // Check if this is an external link (has http/https)
                const isExternal = href?.startsWith('http://') || href?.startsWith('https://');
                return (
                  <a
                    href={href}
                    target="_blank"
                    rel="noopener noreferrer"
                    className={`markdown-link ${isExternal ? 'markdown-link-external' : ''}`}
                  >
                    <span className="markdown-link-text">{children}</span>
                    {isExternal && <ArrowUpRight size={14} className="markdown-link-arrow" />}
                  </a>
                );
              },
            }}
          >
            {cleanedContent}
          </ReactMarkdown>
        </div>

        {/* Image Gallery Modal */}
        {galleryImages.length > 0 && (
          <ImageGalleryModal
            images={galleryImages}
            initialIndex={galleryIndex}
            isOpen={galleryOpen}
            onClose={() => setGalleryOpen(false)}
          />
        )}
      </>
    );
  }
);

MarkdownRendererComponent.displayName = 'MarkdownRenderer';

// Memoize with custom comparison to prevent unnecessary re-renders
// Only re-render if content or isStreaming actually changes
export const MarkdownRenderer = memo(MarkdownRendererComponent, (prevProps, nextProps) => {
  return (
    prevProps.content === nextProps.content &&
    prevProps.className === nextProps.className &&
    prevProps.isStreaming === nextProps.isStreaming
  );
});
