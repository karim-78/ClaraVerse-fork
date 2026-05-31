import { useEffect, useState, useCallback } from 'react';
import { RefreshCw, AlertCircle, Brain, Info, X } from 'lucide-react';
import { Button, Spinner, Alert } from '@/components/design-system';
import { useSubscriptionStore } from '@/store/useSubscriptionStore';
import memoryService from '@/services/memoryService';
import type { MemoryStats } from '@/services/memoryService';
import { useSettingsStore } from '@/store/useSettingsStore';
import './UsageSection.css';

// Helper function to format reset time with more detail
function formatResetTime(resetAt: string): string {
  const date = new Date(resetAt);
  const now = new Date();
  const diffMs = date.getTime() - now.getTime();

  if (diffMs <= 0) {
    return 'Resetting...';
  }

  const diffMinutes = Math.floor(diffMs / (1000 * 60));
  const diffHours = Math.floor(diffMs / (1000 * 60 * 60));
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24));

  // Less than 1 hour: show hours and minutes
  if (diffMinutes < 60) {
    return `Resets in ${diffMinutes} min`;
  }

  // Less than 24 hours: show hours and remaining minutes
  if (diffHours < 24) {
    const remainingMinutes = diffMinutes % 60;
    if (remainingMinutes > 0) {
      return `Resets in ${diffHours} hr ${remainingMinutes} min`;
    }
    return `Resets in ${diffHours} hr`;
  }

  // 1-6 days: show days
  if (diffDays < 7) {
    return `Resets in ${diffDays} day${diffDays > 1 ? 's' : ''}`;
  }

  // 7+ days: show day of week and time
  return `Resets ${date.toLocaleDateString('en-US', { weekday: 'short' })} ${date.toLocaleTimeString('en-US', { hour: 'numeric', minute: '2-digit' })}`;
}

// Helper to format relative time
function formatTimeAgo(date: Date): string {
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffSeconds = Math.floor(diffMs / 1000);
  const diffMinutes = Math.floor(diffMs / (1000 * 60));

  if (diffSeconds < 60) {
    return 'less than a minute ago';
  }

  if (diffMinutes < 60) {
    return `${diffMinutes} minute${diffMinutes > 1 ? 's' : ''} ago`;
  }

  return date.toLocaleTimeString('en-US', { hour: 'numeric', minute: '2-digit' });
}

// Usage item interface
interface UsageItemData {
  label: string;
  current: number;
  max: number;
  resetAt?: string;
  showCount?: boolean; // If true, show raw count instead of percentage or "Unlimited"
}

// Usage Item Component
interface UsageItemProps {
  item: UsageItemData;
}

const UsageItem: React.FC<UsageItemProps> = ({ item }) => {
  const { label, current, max, resetAt, showCount } = item;
  const isUnlimited = max === -1;
  const percentage = isUnlimited ? 0 : Math.min((current / max) * 100, 100);

  const getProgressStatus = (): 'safe' | 'warning' | 'critical' => {
    if (isUnlimited) return 'safe';
    if (percentage >= 90) return 'critical';
    if (percentage >= 70) return 'warning';
    return 'safe';
  };

  const progressStatus = getProgressStatus();

  // Determine display value
  const displayValue = showCount
    ? current.toLocaleString() // Show raw count with comma formatting
    : isUnlimited
      ? 'Unlimited'
      : `${Math.round(percentage)}% used`;

  return (
    <div className="usage-item">
      <div className="usage-item-header">
        <div className="usage-item-left">
          <span className="usage-item-label">{label}</span>
          {resetAt && <span className="usage-item-reset">{formatResetTime(resetAt)}</span>}
        </div>
        <span className={`usage-item-value ${isUnlimited && !showCount ? 'unlimited' : ''}`}>
          {displayValue}
        </span>
      </div>
      {!isUnlimited && !showCount && (
        <div className="usage-progress-container">
          <div className="usage-progress-bar">
            <div
              className={`usage-progress-fill progress-${progressStatus}`}
              style={{ width: `${percentage}%` }}
            />
          </div>
        </div>
      )}
    </div>
  );
};

// Usage Group Component
interface UsageGroupProps {
  title: string;
  items: UsageItemData[];
}

const UsageGroup: React.FC<UsageGroupProps> = ({ title, items }) => {
  if (items.length === 0) return null;

  return (
    <section className="usage-group">
      <h3 className="usage-group-title">{title}</h3>
      <div className="usage-group-items">
        {items.map((item, index) => (
          <UsageItem key={`${item.label}-${index}`} item={item} />
        ))}
      </div>
    </section>
  );
};

export const UsageSection: React.FC = () => {
  const { usageStats, isLoadingUsage, usageError, fetchUsageStats } = useSubscriptionStore();
  const { memoryEnabled } = useSettingsStore();

  const [lastUpdated, setLastUpdated] = useState<Date>(new Date());
  const [isRefreshing, setIsRefreshing] = useState(false);
  const [memoryStats, setMemoryStats] = useState<MemoryStats | null>(null);
  const [memoryStatsError, setMemoryStatsError] = useState<string | null>(null);
  const [showMemoryInfoModal, setShowMemoryInfoModal] = useState(false);

  // Fetch memory stats
  const fetchMemoryStats = useCallback(async () => {
    if (!memoryEnabled) {
      setMemoryStats(null);
      return;
    }

    try {
      setMemoryStatsError(null);
      const stats = await memoryService.getMemoryStats();
      setMemoryStats(stats);
    } catch (err) {
      console.error('Failed to fetch memory stats:', err);
      setMemoryStatsError('Failed to load memory statistics');
    }
  }, [memoryEnabled]);

  // Fetch usage stats on mount
  useEffect(() => {
    fetchUsageStats();
    fetchMemoryStats();
    setLastUpdated(new Date());
  }, [fetchUsageStats, fetchMemoryStats]);

  // Refresh handler
  const handleRefresh = useCallback(async () => {
    setIsRefreshing(true);
    await Promise.all([fetchUsageStats(), fetchMemoryStats()]);
    setLastUpdated(new Date());
    setIsRefreshing(false);
  }, [fetchUsageStats, fetchMemoryStats]);

  // Group usage data by period
  const sessionItems: UsageItemData[] = [];
  // Session data would go here if backend provides it

  const dailyItems: UsageItemData[] = usageStats
    ? [
        {
          label: 'Executions',
          current: usageStats.executions_today?.current ?? 0,
          max: usageStats.executions_today?.max ?? -1,
        },
        {
          label: 'File uploads',
          current: usageStats.file_uploads?.current ?? 0,
          max: usageStats.file_uploads?.max ?? -1,
          resetAt: usageStats.file_uploads?.reset_at,
        },
        {
          label: 'Image generations',
          current: usageStats.image_generations?.current ?? 0,
          max: usageStats.image_generations?.max ?? -1,
          resetAt: usageStats.image_generations?.reset_at,
        },
        {
          label: 'Memory extractions',
          current: usageStats.memory_extractions?.current ?? 0,
          max: usageStats.memory_extractions?.max ?? -1,
          resetAt: usageStats.memory_extractions?.reset_at,
        },
      ]
    : [];

  const monthlyItems: UsageItemData[] = usageStats
    ? [
        {
          label: 'Messages',
          current: usageStats.messages?.current ?? 0,
          max: usageStats.messages?.max ?? -1,
          resetAt: usageStats.messages?.reset_at,
        },
      ]
    : [];

  const resourceItems: UsageItemData[] = usageStats
    ? [
        {
          label: 'Schedules',
          current: usageStats.schedules?.current ?? 0,
          max: usageStats.schedules?.max ?? -1,
        },
        {
          label: 'API keys',
          current: usageStats.api_keys?.current ?? 0,
          max: usageStats.api_keys?.max ?? -1,
        },
        {
          label: 'Requests per minute',
          current: usageStats.requests_per_min?.current ?? 0,
          max: usageStats.requests_per_min?.max ?? -1,
        },
      ]
    : [];

  const memoryItems: UsageItemData[] =
    memoryEnabled && memoryStats
      ? [
          {
            label: 'Total memories',
            current: memoryStats.total_memories,
            max: -1, // No limit
            showCount: true, // Show raw count
          },
          {
            label: 'Active memories',
            current: memoryStats.active_memories,
            max: -1,
            showCount: true,
          },
          {
            label: 'Archived memories',
            current: memoryStats.archived_memories,
            max: -1,
            showCount: true,
          },
        ]
      : [];

  if (isLoadingUsage && !usageStats) {
    return (
      <div className="usage-loading">
        <Spinner size="lg" />
        <p>Loading usage data...</p>
      </div>
    );
  }

  if (usageError && !usageStats) {
    return (
      <div className="usage-error">
        <Alert variant="error">
          <AlertCircle size={16} />
          <span>{usageError}</span>
        </Alert>
        <Button onClick={handleRefresh}>
          <RefreshCw size={16} />
          Retry
        </Button>
      </div>
    );
  }

  return (
    <div className="usage-section">
      {/* Header */}
      <header className="usage-header">
        <div className="usage-title-section">
          <h1 className="usage-main-title">Plan usage limits</h1>
          <p className="usage-subtitle">
            Monitor your current usage across different resource limits.
          </p>
        </div>
      </header>

      {/* Session Usage */}
      {sessionItems.length > 0 && (
        <>
          <UsageGroup title="Current session" items={sessionItems} />
          <div className="usage-divider" />
        </>
      )}

      {/* Daily Limits */}
      {dailyItems.length > 0 && (
        <>
          <UsageGroup title="Daily limits" items={dailyItems} />
          <div className="usage-divider" />
        </>
      )}

      {/* Monthly Limits */}
      {monthlyItems.length > 0 && (
        <>
          <UsageGroup title="Monthly limits" items={monthlyItems} />
          <div className="usage-divider" />
        </>
      )}

      {/* Resource Limits */}
      {resourceItems.length > 0 && (
        <>
          <UsageGroup title="Resource limits" items={resourceItems} />
          {memoryItems.length > 0 && <div className="usage-divider" />}
        </>
      )}

      {/* Memory System */}
      {memoryItems.length > 0 && (
        <section className="usage-group">
          <h3
            className="usage-group-title"
            style={{ display: 'flex', alignItems: 'center', gap: '8px' }}
          >
            <Brain size={18} />
            Memory System
            <button
              onClick={() => setShowMemoryInfoModal(true)}
              className="text-gray-400 hover:text-gray-200 transition-colors"
              style={{
                background: 'none',
                border: 'none',
                cursor: 'pointer',
                padding: '4px',
                display: 'flex',
                alignItems: 'center',
              }}
              title="Learn how memory works"
            >
              <Info size={16} />
            </button>
          </h3>
          {memoryStatsError && (
            <Alert variant="error" style={{ marginBottom: '16px' }}>
              <AlertCircle size={16} />
              <span>{memoryStatsError}</span>
            </Alert>
          )}
          <div className="usage-group-items">
            {memoryItems.map((item, index) => (
              <UsageItem key={`${item.label}-${index}`} item={item} />
            ))}
          </div>
          <p style={{ fontSize: '12px', color: '#9CA3AF', marginTop: '12px' }}>
            Memory system stores encrypted information from your conversations. Average score:{' '}
            {memoryStats ? memoryStats.avg_score.toFixed(2) : 'N/A'}
          </p>
        </section>
      )}

      {memoryEnabled && !memoryStats && !memoryStatsError && (
        <div style={{ padding: '24px', textAlign: 'center', color: '#9CA3AF' }}>
          <Brain size={24} style={{ margin: '0 auto 8px', opacity: 0.5 }} />
          <p style={{ fontSize: '14px' }}>No memories stored yet</p>
          <p style={{ fontSize: '12px', marginTop: '4px' }}>
            Memories will be extracted as you chat with Dobby
          </p>
        </div>
      )}

      {/* Footer */}
      <footer className="usage-footer">
        <span>Last updated: {formatTimeAgo(lastUpdated)}</span>
        <button
          className={`usage-refresh-btn ${isRefreshing ? 'refreshing' : ''}`}
          onClick={handleRefresh}
          disabled={isRefreshing}
          aria-label="Refresh usage data"
        >
          <RefreshCw size={16} />
        </button>
      </footer>

      {/* Memory System Info Modal */}
      {showMemoryInfoModal && (
        <div
          className="fixed inset-0 flex items-center justify-center z-50 p-4 backdrop-blur-sm bg-black/80"
          onClick={() => setShowMemoryInfoModal(false)}
        >
          <div
            style={{ backgroundColor: '#0d0d0d' }}
            className="rounded-xl shadow-2xl max-w-2xl w-full max-h-[85vh] overflow-hidden"
            onClick={e => e.stopPropagation()}
          >
            <div
              style={{ backgroundColor: '#0d0d0d' }}
              className="sticky top-0 p-5 flex items-center justify-between"
            >
              <div className="flex items-center gap-3">
                <div style={{ backgroundColor: '#0d0d0d' }} className="p-2 rounded-lg">
                  <Brain className="w-4 h-4 text-gray-300" />
                </div>
                <h2 className="text-base font-semibold text-gray-100">Memory System</h2>
              </div>
              <button
                onClick={() => setShowMemoryInfoModal(false)}
                className="p-2 rounded-lg text-gray-400 hover:text-gray-200 hover:bg-gray-800/50 transition-all"
                aria-label="Close"
              >
                <X size={18} />
              </button>
            </div>

            <div className="p-6 space-y-5 text-sm overflow-y-auto max-h-[calc(85vh-80px)]">
              {/* How it works */}
              <section>
                <h3 className="text-sm font-semibold mb-2 text-gray-300">How it works</h3>
                <p className="text-gray-400 leading-relaxed text-xs">
                  Dobby automatically extracts and stores key information from your conversations.
                  Relevant memories are injected into future chats to provide context and
                  continuity.
                </p>
              </section>

              {/* Extraction */}
              <section>
                <h3 className="text-sm font-semibold mb-2 text-gray-300">Extraction</h3>
                <div className="space-y-2 text-gray-400">
                  <p className="text-xs">
                    Memories are extracted after every N messages (configurable: 2-50, default: 20)
                  </p>
                  <p className="text-xs text-gray-500">
                    Categories: personal info, preferences, context, facts, instructions
                  </p>
                </div>
              </section>

              {/* Selection */}
              <section>
                <h3 className="text-sm font-semibold mb-2 text-gray-300">Selection & Injection</h3>
                <div className="space-y-2 text-gray-400">
                  <p className="text-xs">
                    Before each chat, the most relevant memories are automatically selected and
                    injected into the context.
                  </p>
                  <p className="text-xs text-gray-500">Default: Top 5 most relevant memories</p>
                </div>
              </section>

              {/* Decay */}
              <section>
                <h3 className="text-sm font-semibold mb-2 text-gray-300">Memory Decay</h3>
                <div className="space-y-3 text-gray-400">
                  <p className="text-xs">
                    Memories are scored based on recency, frequency of use, and conversation
                    engagement.
                  </p>
                  <div
                    style={{ backgroundColor: '#0d0d0d' }}
                    className="rounded-lg p-3 font-mono text-xs"
                  >
                    <p className="text-gray-300">
                      Score = (0.4 × Recency) + (0.3 × Frequency) + (0.3 × Engagement)
                    </p>
                  </div>
                  <div className="grid grid-cols-3 gap-2 text-xs">
                    <div style={{ backgroundColor: '#0d0d0d' }} className="rounded-lg p-2.5">
                      <p className="text-gray-200 font-medium mb-1.5 text-xs">Recency</p>
                      <p className="text-gray-500 text-xs">Recent: 1.0</p>
                      <p className="text-gray-500 text-xs">1 week: 0.70</p>
                      <p className="text-gray-500 text-xs">1 month: 0.22</p>
                    </div>
                    <div style={{ backgroundColor: '#0d0d0d' }} className="rounded-lg p-2.5">
                      <p className="text-gray-200 font-medium mb-1.5 text-xs">Frequency</p>
                      <p className="text-gray-500 text-xs">0 uses: 0.0</p>
                      <p className="text-gray-500 text-xs">10 uses: 0.5</p>
                      <p className="text-gray-500 text-xs">20+ uses: 1.0</p>
                    </div>
                    <div style={{ backgroundColor: '#0d0d0d' }} className="rounded-lg p-2.5">
                      <p className="text-gray-200 font-medium mb-1.5 text-xs">Engagement</p>
                      <p className="text-gray-500 text-xs">High: 0.8-1.0</p>
                      <p className="text-gray-500 text-xs">Med: 0.4-0.7</p>
                      <p className="text-gray-500 text-xs">Low: 0.0-0.3</p>
                    </div>
                  </div>
                  <p className="text-xs text-gray-500">
                    Memories with score &lt; 0.15 are archived but can be restored
                  </p>
                </div>
              </section>

              {/* Privacy */}
              <section>
                <h3 className="text-sm font-semibold mb-2 text-gray-300">Privacy & Security</h3>
                <div className="space-y-2 text-gray-400">
                  <ul className="space-y-1.5">
                    <li className="flex items-start gap-2 text-xs">
                      <span className="text-gray-300/50 mt-0.5">•</span>
                      <span>
                        End-to-end encrypted with AES-256-GCM using your unique encryption key
                      </span>
                    </li>
                    <li className="flex items-start gap-2 text-xs">
                      <span className="text-gray-300/50 mt-0.5">•</span>
                      <span>ClaraVerse administrators cannot access your encrypted memories</span>
                    </li>
                    <li className="flex items-start gap-2 text-xs">
                      <span className="text-gray-300/50 mt-0.5">•</span>
                      <span>Automatic deduplication prevents storing duplicate information</span>
                    </li>
                  </ul>
                </div>
              </section>

              {/* Credit Info */}
              <section style={{ backgroundColor: '#0d0d0d' }} className="rounded-lg p-3">
                <p className="text-xs text-gray-400">
                  <span className="text-gray-300 font-medium">Credit Usage:</span> Memory extraction
                  uses AI credits. The default threshold (20 messages) is optimized to balance
                  memory quality with credit conservation.
                </p>
              </section>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

export default UsageSection;
