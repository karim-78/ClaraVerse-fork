import { useState, useEffect, useCallback } from 'react';
import { Key, Plus, Copy, Check, Trash2, Loader2, AlertCircle, Clock, Shield } from 'lucide-react';
import {
  createAPIKey,
  listAPIKeys,
  revokeAPIKey,
  formatLastUsed,
  maskKeyPrefix,
  copyToClipboard,
  type APIKey,
  type CreateAPIKeyResponse,
} from '@/services/apiKeyService';

interface AgentAPIKeysPanelProps {
  agentId: string;
  className?: string;
}

export function AgentAPIKeysPanel({ agentId, className }: AgentAPIKeysPanelProps) {
  const [keys, setKeys] = useState<APIKey[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [isCreating, setIsCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [newKey, setNewKey] = useState<CreateAPIKeyResponse | null>(null);
  const [copiedKey, setCopiedKey] = useState(false);
  const [copiedCurl, setCopiedCurl] = useState(false);

  // Form state
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [keyName, setKeyName] = useState('');
  const [keyDescription, setKeyDescription] = useState('');
  const [selectedScopes, setSelectedScopes] = useState<string[]>([`execute:${agentId}`]);

  const loadKeys = useCallback(async () => {
    setIsLoading(true);
    try {
      const allKeys = await listAPIKeys();
      // Filter keys that have scope for this agent or execute:*
      const agentKeys = allKeys.filter(
        key =>
          !key.isRevoked &&
          (key.scopes.includes(`execute:${agentId}`) || key.scopes.includes('execute:*'))
      );
      setKeys(agentKeys);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load API keys');
    } finally {
      setIsLoading(false);
    }
  }, [agentId]);

  useEffect(() => {
    loadKeys();
  }, [loadKeys]);

  const handleCreateKey = async () => {
    if (!keyName.trim()) {
      setError('Please enter a key name');
      return;
    }

    setIsCreating(true);
    setError(null);

    try {
      const response = await createAPIKey({
        name: keyName.trim(),
        description: keyDescription.trim() || undefined,
        scopes: selectedScopes,
      });

      setNewKey(response);
      setShowCreateForm(false);
      setKeyName('');
      setKeyDescription('');
      setSelectedScopes([`execute:${agentId}`]);
      await loadKeys();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create API key');
    } finally {
      setIsCreating(false);
    }
  };

  const handleRevokeKey = async (keyId: string) => {
    if (!confirm('Revoke this API key? This cannot be undone.')) return;

    try {
      await revokeAPIKey(keyId);
      await loadKeys();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to revoke key');
    }
  };

  const handleCopyKey = async () => {
    if (newKey) {
      const success = await copyToClipboard(newKey.key);
      if (success) {
        setCopiedKey(true);
        setTimeout(() => setCopiedKey(false), 2000);
      }
    }
  };

  const handleDismissNewKey = () => {
    setNewKey(null);
  };

  const toggleScope = (scope: string) => {
    setSelectedScopes(prev =>
      prev.includes(scope) ? prev.filter(s => s !== scope) : [...prev, scope]
    );
  };

  if (isLoading) {
    return (
      <div className={`flex items-center justify-center py-12 ${className || ''}`}>
        <Loader2 size={24} className="animate-spin text-[var(--color-text-tertiary)]" />
      </div>
    );
  }

  return (
    <div className={className}>
      {/* Header */}
      <div className="flex items-center justify-between mb-4">
        <div>
          <h3 className="text-sm font-medium text-[var(--color-text-primary)]">API Keys</h3>
          <p className="text-xs text-[var(--color-text-tertiary)] mt-0.5">
            Create API keys to trigger this agent programmatically
          </p>
        </div>
        <button
          onClick={() => setShowCreateForm(true)}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-[var(--color-accent)] text-white text-xs font-medium hover:bg-[var(--color-accent-hover)] transition-colors"
        >
          <Plus size={14} />
          Create Key
        </button>
      </div>

      {/* Error */}
      {error && (
        <div className="flex items-center gap-2 p-3 mb-4 rounded-lg bg-red-500/10 text-red-400 text-xs">
          <AlertCircle size={14} />
          {error}
          <button onClick={() => setError(null)} className="ml-auto hover:text-red-300">
            ×
          </button>
        </div>
      )}

      {/* New Key Alert */}
      {newKey && (
        <div className="mb-4 p-4 rounded-lg bg-[var(--color-accent)]/10 border border-[var(--color-accent)]/30">
          <div className="flex items-start gap-3">
            <Key size={16} className="text-[var(--color-accent)] mt-0.5" />
            <div className="flex-1">
              <p className="text-sm font-medium text-[var(--color-text-primary)] mb-1">
                API Key Created
              </p>
              <p className="text-xs text-[var(--color-text-secondary)] mb-3">
                Copy this key now. You won't be able to see it again!
              </p>
              <div className="flex items-center gap-2 p-2 rounded-lg bg-[var(--color-bg-primary)] font-mono text-xs">
                <code className="flex-1 text-[var(--color-text-primary)] break-all">
                  {newKey.key}
                </code>
                <button
                  onClick={handleCopyKey}
                  className="p-1.5 rounded hover:bg-[var(--color-surface)] transition-colors"
                  title="Copy to clipboard"
                >
                  {copiedKey ? (
                    <Check size={14} className="text-green-400" />
                  ) : (
                    <Copy size={14} className="text-[var(--color-text-secondary)]" />
                  )}
                </button>
              </div>
              <button
                onClick={handleDismissNewKey}
                className="mt-3 text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] transition-colors"
              >
                I've saved my key
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Create Form */}
      {showCreateForm && (
        <div className="mb-4 p-4 rounded-lg bg-[var(--color-bg-primary)] border border-[var(--color-border)]">
          <h4 className="text-sm font-medium text-[var(--color-text-primary)] mb-3">
            Create New API Key
          </h4>

          <div className="space-y-3">
            <div>
              <label className="block text-xs text-[var(--color-text-secondary)] mb-1">
                Key Name *
              </label>
              <input
                type="text"
                value={keyName}
                onChange={e => setKeyName(e.target.value)}
                placeholder="e.g., Production API"
                className="w-full px-3 py-2 rounded-lg bg-[var(--color-surface)] border border-[var(--color-border)] text-sm text-[var(--color-text-primary)] placeholder:text-[var(--color-text-tertiary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]"
              />
            </div>

            <div>
              <label className="block text-xs text-[var(--color-text-secondary)] mb-1">
                Description
              </label>
              <input
                type="text"
                value={keyDescription}
                onChange={e => setKeyDescription(e.target.value)}
                placeholder="Optional description"
                className="w-full px-3 py-2 rounded-lg bg-[var(--color-surface)] border border-[var(--color-border)] text-sm text-[var(--color-text-primary)] placeholder:text-[var(--color-text-tertiary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]"
              />
            </div>

            <div>
              <label className="block text-xs text-[var(--color-text-secondary)] mb-2">
                Permissions
              </label>
              <div className="space-y-2">
                {/* Agent-specific execute scope */}
                <label className="flex items-start gap-2 p-2 rounded-lg bg-[var(--color-surface)] cursor-pointer hover:bg-[var(--color-surface-hover)] transition-colors">
                  <input
                    type="checkbox"
                    checked={selectedScopes.includes(`execute:${agentId}`)}
                    onChange={() => toggleScope(`execute:${agentId}`)}
                    className="mt-0.5"
                  />
                  <div>
                    <span className="text-xs font-medium text-[var(--color-text-primary)]">
                      Execute This Agent
                    </span>
                    <p className="text-[10px] text-[var(--color-text-tertiary)]">
                      Can trigger executions for this specific agent
                    </p>
                  </div>
                </label>

                {/* Read executions scope */}
                <label className="flex items-start gap-2 p-2 rounded-lg bg-[var(--color-surface)] cursor-pointer hover:bg-[var(--color-surface-hover)] transition-colors">
                  <input
                    type="checkbox"
                    checked={selectedScopes.includes('read:executions')}
                    onChange={() => toggleScope('read:executions')}
                    className="mt-0.5"
                  />
                  <div>
                    <span className="text-xs font-medium text-[var(--color-text-primary)]">
                      Read Executions
                    </span>
                    <p className="text-[10px] text-[var(--color-text-tertiary)]">
                      View execution history and results
                    </p>
                  </div>
                </label>

                {/* Upload scope - needed for file input workflows */}
                <label className="flex items-start gap-2 p-2 rounded-lg bg-[var(--color-surface)] cursor-pointer hover:bg-[var(--color-surface-hover)] transition-colors">
                  <input
                    type="checkbox"
                    checked={selectedScopes.includes('upload')}
                    onChange={() => toggleScope('upload')}
                    className="mt-0.5"
                  />
                  <div>
                    <span className="text-xs font-medium text-[var(--color-text-primary)]">
                      Upload Files
                    </span>
                    <p className="text-[10px] text-[var(--color-text-tertiary)]">
                      Upload files for workflows with file inputs
                    </p>
                  </div>
                </label>
              </div>
            </div>
          </div>

          <div className="flex items-center gap-2 mt-4 pt-3 border-t border-[var(--color-border)]">
            <button
              onClick={handleCreateKey}
              disabled={isCreating || !keyName.trim()}
              className="flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-[var(--color-accent-hover)] disabled:opacity-50 transition-colors"
            >
              {isCreating && <Loader2 size={14} className="animate-spin" />}
              Create Key
            </button>
            <button
              onClick={() => {
                setShowCreateForm(false);
                setKeyName('');
                setKeyDescription('');
                setSelectedScopes([`execute:${agentId}`]);
              }}
              className="px-4 py-2 rounded-lg text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] transition-colors"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {/* Keys List */}
      {keys.length === 0 && !showCreateForm ? (
        <div className="text-center py-8">
          <Key size={32} className="mx-auto mb-3 text-[var(--color-text-tertiary)] opacity-50" />
          <p className="text-sm text-[var(--color-text-secondary)]">No API keys yet</p>
          <p className="text-xs text-[var(--color-text-tertiary)] mt-1">
            Create an API key to trigger this agent via HTTP
          </p>
        </div>
      ) : (
        <div className="space-y-2">
          {keys.map(key => (
            <div
              key={key.id}
              className="flex items-center gap-3 p-3 rounded-lg bg-[var(--color-bg-primary)] border border-[var(--color-border)]"
            >
              <div className="p-2 rounded-lg bg-[var(--color-surface)]">
                <Key size={14} className="text-[var(--color-text-secondary)]" />
              </div>

              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium text-[var(--color-text-primary)]">
                    {key.name}
                  </span>
                  <code className="px-1.5 py-0.5 rounded bg-[var(--color-surface)] text-[10px] text-[var(--color-text-tertiary)] font-mono">
                    {maskKeyPrefix(key.keyPrefix)}
                  </code>
                </div>
                <div className="flex items-center gap-3 mt-0.5 text-[10px] text-[var(--color-text-tertiary)]">
                  <span className="flex items-center gap-1">
                    <Clock size={10} />
                    {formatLastUsed(key.lastUsedAt)}
                  </span>
                  <span className="flex items-center gap-1">
                    <Shield size={10} />
                    {key.scopes.length} scope{key.scopes.length !== 1 ? 's' : ''}
                  </span>
                </div>
              </div>

              <button
                onClick={() => handleRevokeKey(key.id)}
                className="p-2 rounded-lg hover:bg-red-500/10 text-[var(--color-text-tertiary)] hover:text-red-400 transition-colors"
                title="Revoke key"
              >
                <Trash2 size={14} />
              </button>
            </div>
          ))}
        </div>
      )}

      {/* Usage Example */}
      {keys.length > 0 &&
        (() => {
          const curlCommand = `curl -X POST \\
  ${import.meta.env.VITE_API_BASE_URL || window.location.origin}/api/trigger/${agentId} \\
  -H "X-API-Key: YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"input": {"message": "Hello"}}'`;

          const handleCopyCurl = async () => {
            await copyToClipboard(curlCommand);
            setCopiedCurl(true);
            setTimeout(() => setCopiedCurl(false), 2000);
          };

          return (
            <div className="mt-4 p-3 rounded-lg bg-[var(--color-surface)] border border-[var(--color-border)]">
              <div className="flex items-center justify-between mb-2">
                <p className="text-xs font-medium text-[var(--color-text-secondary)]">
                  Example Usage
                </p>
                <button
                  onClick={handleCopyCurl}
                  className="flex items-center gap-1 px-2 py-1 rounded text-[10px] text-[var(--color-text-tertiary)] hover:text-[var(--color-text-primary)] hover:bg-[var(--color-bg-primary)] transition-colors"
                  title="Copy curl command"
                >
                  {copiedCurl ? (
                    <>
                      <Check size={12} className="text-green-400" />
                      <span className="text-green-400">Copied!</span>
                    </>
                  ) : (
                    <>
                      <Copy size={12} />
                      <span>Copy</span>
                    </>
                  )}
                </button>
              </div>
              <pre className="text-[10px] text-[var(--color-text-tertiary)] font-mono overflow-x-auto">
                {curlCommand}
              </pre>
            </div>
          );
        })()}
    </div>
  );
}
