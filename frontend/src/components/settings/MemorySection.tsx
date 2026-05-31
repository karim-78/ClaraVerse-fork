import React, { useState, useEffect } from 'react';
import { Brain, Info, AlertCircle, Loader2, Clock, TrendingUp, Trash2 } from 'lucide-react';
import { useSettingsStore } from '@/store/useSettingsStore';
import memoryService from '@/services/memoryService';
import type { MemoryStats } from '@/services/memoryService';
import { MemoryList } from './MemoryList';

export interface MemorySectionProps {
  /** Callback when settings change */
  onSave?: () => void;
}

/**
 * Memory System Configuration section component.
 * Manages memory system settings and displays statistics.
 */
export const MemorySection: React.FC<MemorySectionProps> = ({ onSave }) => {
  const {
    memoryEnabled,
    memoryExtractionThreshold,
    memoryMaxInjection,
    setMemoryEnabled,
    setMemoryExtractionThreshold,
    setMemoryMaxInjection,
    initializeFromBackend,
  } = useSettingsStore();

  const [stats, setStats] = useState<MemoryStats | null>(null);
  const [loadingStats, setLoadingStats] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [showClearConfirm, setShowClearConfirm] = useState(false);
  const [isClearing, setIsClearing] = useState(false);

  // Initialize settings from backend on mount
  useEffect(() => {
    initializeFromBackend();
  }, [initializeFromBackend]);

  // Load memory statistics
  useEffect(() => {
    if (memoryEnabled) {
      loadStats();
    }
  }, [memoryEnabled]);

  const loadStats = async () => {
    setLoadingStats(true);
    setError(null);
    try {
      const data = await memoryService.getMemoryStats();
      setStats(data);
    } catch (err) {
      console.error('Failed to load memory stats:', err);
      setError('Failed to load memory statistics');
    } finally {
      setLoadingStats(false);
    }
  };

  const handleToggleMemory = (enabled: boolean) => {
    setMemoryEnabled(enabled);
    onSave?.();
  };

  const handleThresholdChange = (value: number) => {
    setMemoryExtractionThreshold(value);
    onSave?.();
  };

  const handleMaxInjectionChange = (value: number) => {
    setMemoryMaxInjection(value);
    onSave?.();
  };

  const handleClearAllMemories = async () => {
    if (!stats || stats.total_memories === 0) {
      return;
    }

    setIsClearing(true);
    setError(null);

    try {
      // Get all memories and delete them one by one
      const memoriesResponse = await memoryService.listMemories({
        includeArchived: true,
        pageSize: 1000, // Get all memories
      });

      // Delete each memory
      for (const memory of memoriesResponse.memories) {
        await memoryService.deleteMemory(memory.id);
      }

      // Refresh stats
      await loadStats();
      setShowClearConfirm(false);
    } catch (err) {
      console.error('Failed to clear memories:', err);
      setError('Failed to clear all memories. Please try again.');
    } finally {
      setIsClearing(false);
    }
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h2 className="text-2xl font-bold flex items-center gap-2">
          <Brain className="w-6 h-6" />
          Memory System
        </h2>
        <p className="text-sm text-gray-400 mt-1">
          Configure how Dobby remembers and recalls information from your conversations
        </p>
      </div>

      {/* Enable/Disable Toggle */}
      <div style={{ backgroundColor: '#0d0d0d' }} className="rounded-lg p-6 border border-gray-700">
        <div className="flex items-start justify-between">
          <div className="flex-1">
            <h3 className="text-lg font-semibold flex items-center gap-2">
              <Brain className="w-5 h-5" />
              Enable Memory System
            </h3>
            <p className="text-sm text-gray-400 mt-1">
              Allow Dobby to remember information from your conversations and use it in future chats
            </p>

            {/* Privacy Notice */}
            <div
              style={{ backgroundColor: '#0d0d0d' }}
              className="mt-3 flex items-start gap-2 p-3 border border-gray-700 rounded-lg"
            >
              <Info className="w-4 h-4 text-gray-400 mt-0.5 flex-shrink-0" />
              <p className="text-xs text-gray-300">
                All memories are encrypted with your user-specific key. Not even ClaraVerse
                administrators can access your memories.
              </p>
            </div>
          </div>

          <div className="ml-4 flex-shrink-0">
            <button
              onClick={() => handleToggleMemory(!memoryEnabled)}
              className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                memoryEnabled ? 'bg-gray-600' : 'bg-gray-700'
              }`}
              role="switch"
              aria-checked={memoryEnabled}
            >
              <span
                className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                  memoryEnabled ? 'translate-x-6' : 'translate-x-1'
                }`}
              />
            </button>
          </div>
        </div>
      </div>

      {/* Configuration Options (only shown when enabled) */}
      {memoryEnabled && (
        <>
          {/* Extraction Threshold */}
          <div
            style={{ backgroundColor: '#0d0d0d', borderColor: '#e91e63' }}
            className="rounded-lg p-6 border"
          >
            <div className="flex items-start justify-between mb-4">
              <div>
                <h3 className="text-lg font-semibold flex items-center gap-2">
                  <Clock className="w-5 h-5" />
                  Extraction Frequency
                </h3>
                <p className="text-sm text-gray-400 mt-1">
                  How many messages before extracting new memories
                </p>
              </div>
            </div>

            <div className="space-y-3">
              <div className="flex items-center gap-4">
                <input
                  type="range"
                  min="2"
                  max="50"
                  step="2"
                  value={memoryExtractionThreshold}
                  onChange={e => handleThresholdChange(Number(e.target.value))}
                  className="flex-1 h-2 bg-gray-700 rounded-lg appearance-none cursor-pointer accent-gray-400"
                />
                <span className="text-sm font-semibold min-w-[4rem] text-right">
                  {memoryExtractionThreshold} messages
                </span>
              </div>
              <p className="text-xs text-gray-500">
                Default: 20 messages (conservative to save credits). Lower values extract more
                frequently but use more credits. Range: 2-50 messages.
              </p>
            </div>
          </div>

          {/* Max Injection */}
          <div
            style={{ backgroundColor: '#0d0d0d', borderColor: '#e91e63' }}
            className="rounded-lg p-6 border"
          >
            <div className="flex items-start justify-between mb-4">
              <div>
                <h3 className="text-lg font-semibold flex items-center gap-2">
                  <TrendingUp className="w-5 h-5" />
                  Context Limit
                </h3>
                <p className="text-sm text-gray-400 mt-1">
                  Maximum memories to inject into each conversation
                </p>
              </div>
            </div>

            <div className="space-y-3">
              <div className="flex items-center gap-4">
                <input
                  type="range"
                  min="1"
                  max="10"
                  step="1"
                  value={memoryMaxInjection}
                  onChange={e => handleMaxInjectionChange(Number(e.target.value))}
                  className="flex-1 h-2 bg-gray-700 rounded-lg appearance-none cursor-pointer accent-gray-400"
                />
                <span className="text-sm font-semibold min-w-[3rem] text-right">
                  {memoryMaxInjection} max
                </span>
              </div>
              <p className="text-xs text-gray-500">
                Recommended: 5 memories. Only the most relevant memories based on your current
                conversation are selected.
              </p>
            </div>
          </div>

          {/* Statistics */}
          <div
            style={{ backgroundColor: '#0d0d0d', borderColor: '#e91e63' }}
            className="rounded-lg p-6 border"
          >
            <div className="flex items-start justify-between mb-4">
              <div>
                <h3 className="text-lg font-semibold">Memory Statistics</h3>
                <p className="text-sm text-gray-400 mt-1">Overview of your stored memories</p>
              </div>
              <div className="flex items-center gap-2">
                <button
                  onClick={loadStats}
                  disabled={loadingStats}
                  className="text-gray-400 hover:text-gray-200 transition-colors disabled:opacity-50"
                  title="Refresh statistics"
                >
                  {loadingStats ? (
                    <Loader2 className="w-5 h-5 animate-spin" />
                  ) : (
                    <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path
                        strokeLinecap="round"
                        strokeLinejoin="round"
                        strokeWidth={2}
                        d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
                      />
                    </svg>
                  )}
                </button>
                {stats && stats.total_memories > 0 && (
                  <button
                    onClick={() => setShowClearConfirm(true)}
                    disabled={isClearing}
                    className="text-red-400 hover:text-red-300 transition-colors disabled:opacity-50"
                    title="Clear all memories"
                  >
                    <Trash2 className="w-5 h-5" />
                  </button>
                )}
              </div>
            </div>

            {error && (
              <div className="flex items-center gap-2 p-3 bg-red-500/10 border border-red-500/20 rounded-lg mb-4">
                <AlertCircle className="w-4 h-4 text-red-400 flex-shrink-0" />
                <p className="text-sm text-red-300">{error}</p>
              </div>
            )}

            {stats ? (
              <div className="grid grid-cols-2 gap-4">
                <div
                  style={{ backgroundColor: '#0d0d0d' }}
                  className="rounded-lg p-4 border border-gray-800"
                >
                  <p className="text-sm text-gray-400">Total Memories</p>
                  <p className="text-2xl font-bold mt-1">{stats.total_memories}</p>
                </div>
                <div
                  style={{ backgroundColor: '#0d0d0d' }}
                  className="rounded-lg p-4 border border-gray-800"
                >
                  <p className="text-sm text-gray-400">Active</p>
                  <p className="text-2xl font-bold mt-1 text-green-400">{stats.active_memories}</p>
                </div>
                <div
                  style={{ backgroundColor: '#0d0d0d' }}
                  className="rounded-lg p-4 border border-gray-800"
                >
                  <p className="text-sm text-gray-400">Archived</p>
                  <p className="text-2xl font-bold mt-1 text-gray-500">{stats.archived_memories}</p>
                </div>
                <div
                  style={{ backgroundColor: '#0d0d0d' }}
                  className="rounded-lg p-4 border border-gray-800"
                >
                  <p className="text-sm text-gray-400">Avg Score</p>
                  <p className="text-2xl font-bold mt-1">{stats.avg_score.toFixed(2)}</p>
                </div>
              </div>
            ) : (
              <div className="text-center py-8 text-gray-500">
                <Brain className="w-12 h-12 mx-auto mb-2 opacity-50" />
                <p className="text-sm">Click refresh to load statistics</p>
              </div>
            )}
          </div>

          {/* How it Works */}
          <div
            style={{ backgroundColor: '#0d0d0d', borderColor: '#e91e63' }}
            className="border rounded-lg p-6"
          >
            <h3 className="text-lg font-semibold flex items-center gap-2 mb-3">
              <Info className="w-5 h-5 text-gray-400" />
              How Memory Works
            </h3>
            <ul className="space-y-2 text-sm text-gray-300">
              <li className="flex items-start gap-2">
                <span className="text-gray-400 mt-0.5">•</span>
                <span>
                  <strong>Extraction:</strong> After every {memoryExtractionThreshold} messages,
                  Dobby automatically extracts important information (preferences, facts, context).
                </span>
              </li>
              <li className="flex items-start gap-2">
                <span className="text-gray-400 mt-0.5">•</span>
                <span>
                  <strong>Selection:</strong> Relevant memories are automatically selected and
                  injected into your conversation context.
                </span>
              </li>
              <li className="flex items-start gap-2">
                <span className="text-gray-400 mt-0.5">•</span>
                <span>
                  <strong>Decay:</strong> Memories are scored based on recency, frequency, and
                  importance. Stale memories are automatically archived.
                </span>
              </li>
              <li className="flex items-start gap-2">
                <span className="text-gray-400 mt-0.5">•</span>
                <span>
                  <strong>Privacy:</strong> All memories are encrypted with AES-256-GCM using your
                  unique key. Only you can decrypt them.
                </span>
              </li>
            </ul>
          </div>
        </>
      )}

      {/* Disabled State Info */}
      {!memoryEnabled && (
        <div
          style={{ backgroundColor: 'rgba(13, 13, 13, 0.5)' }}
          className="border border-gray-700/50 rounded-lg p-6 text-center"
        >
          <Brain className="w-12 h-12 mx-auto mb-3 text-gray-600" />
          <p className="text-gray-400 mb-2">Memory system is currently disabled</p>
          <p className="text-sm text-gray-500">
            Enable it above to let Dobby remember and learn from your conversations
          </p>
        </div>
      )}

      {/* Clear All Memories Confirmation Modal */}
      {showClearConfirm && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
          <div
            style={{ backgroundColor: '#0d0d0d' }}
            className="rounded-lg shadow-xl max-w-md w-full border border-gray-700"
          >
            <div className="p-6">
              <div className="flex items-start gap-4">
                <div className="flex-shrink-0 w-12 h-12 rounded-full bg-red-500/10 flex items-center justify-center">
                  <Trash2 className="w-6 h-6 text-red-400" />
                </div>
                <div className="flex-1">
                  <h3 className="text-lg font-semibold mb-2">Clear All Memories?</h3>
                  <p className="text-sm text-gray-400 mb-4">
                    This will permanently delete all {stats?.total_memories || 0} memories
                    (including archived ones). This action cannot be undone.
                  </p>
                  <div className="bg-yellow-500/10 border border-yellow-500/20 rounded-lg p-3 mb-4">
                    <p className="text-xs text-yellow-300">
                      <strong>Warning:</strong> Dobby will lose all learned information about your
                      preferences, projects, and context. You'll start fresh.
                    </p>
                  </div>
                </div>
              </div>

              <div className="flex items-center justify-end gap-3">
                <button
                  onClick={() => setShowClearConfirm(false)}
                  disabled={isClearing}
                  className="px-4 py-2 text-gray-300 hover:text-white transition-colors disabled:opacity-50"
                >
                  Cancel
                </button>
                <button
                  onClick={handleClearAllMemories}
                  disabled={isClearing}
                  className="px-4 py-2 bg-red-600 hover:bg-red-700 disabled:bg-gray-700 disabled:text-gray-500 rounded-lg transition-colors flex items-center gap-2"
                >
                  {isClearing ? (
                    <>
                      <Loader2 className="w-4 h-4 animate-spin" />
                      Clearing...
                    </>
                  ) : (
                    <>
                      <Trash2 className="w-4 h-4" />
                      Clear All Memories
                    </>
                  )}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Browse / delete individual memories */}
      {memoryEnabled && stats && stats.total_memories > 0 && (
        <div
          style={{ backgroundColor: '#0d0d0d' }}
          className="rounded-lg p-6 border border-gray-700"
        >
          <MemoryList />
        </div>
      )}
    </div>
  );
};
