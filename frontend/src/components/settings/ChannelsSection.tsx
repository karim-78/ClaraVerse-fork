import { useState, useEffect, useCallback } from 'react';
import {
  Send,
  Loader2,
  CheckCircle,
  XCircle,
  AlertCircle,
  Eye,
  EyeOff,
  Copy,
  Check,
  ExternalLink,
  Trash2,
  RefreshCw,
  Users,
  Plus,
  X,
  Shield,
} from 'lucide-react';
import { Button } from '@/components/design-system';
import * as channelService from '@/services/channelService';
import type { Channel, ChannelPlatform } from '@/services/channelService';
import './ChannelsSection.css';

// Telegram icon SVG component
const TelegramIcon = ({ size = 24 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 24 24"
    fill="currentColor"
    xmlns="http://www.w3.org/2000/svg"
  >
    <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm4.64 6.8c-.15 1.58-.8 5.42-1.13 7.19-.14.75-.42 1-.68 1.03-.58.05-1.02-.38-1.58-.75-.88-.58-1.38-.94-2.23-1.5-.99-.65-.35-1.01.22-1.59.15-.15 2.71-2.48 2.76-2.69a.2.2 0 00-.05-.18c-.06-.05-.14-.03-.21-.02-.09.02-1.49.95-4.22 2.79-.4.27-.76.41-1.08.4-.36-.01-1.04-.2-1.55-.37-.63-.2-1.12-.31-1.08-.66.02-.18.27-.36.74-.55 2.92-1.27 4.86-2.11 5.83-2.51 2.78-1.16 3.35-1.36 3.73-1.36.08 0 .27.02.39.12.1.08.13.19.14.27-.01.06.01.24 0 .38z" />
  </svg>
);

interface ChannelConfig {
  id: string;
  name: string;
  icon: React.ReactNode;
  description: string;
  docsUrl: string;
  platform: ChannelPlatform;
  fields: {
    key: string;
    label: string;
    placeholder: string;
    helpText: string;
    sensitive: boolean;
  }[];
}

const CHANNEL_CONFIGS: ChannelConfig[] = [
  {
    id: 'telegram',
    name: 'Telegram',
    icon: <TelegramIcon size={24} />,
    description:
      'Connect your Telegram bot to chat with DobbyAI directly from Telegram. Messages are processed through a secure webhook.',
    docsUrl: 'https://core.telegram.org/bots#how-do-i-create-a-bot',
    platform: 'telegram',
    fields: [
      {
        key: 'bot_token',
        label: 'Bot Token',
        placeholder: '123456789:ABCdefGHIjklMNOpqrsTUVwxyz',
        helpText: 'Get your bot token from @BotFather on Telegram',
        sensitive: true,
      },
    ],
  },
];

export const ChannelsSection = () => {
  // Local state
  const [channels, setChannels] = useState<Channel[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [formData, setFormData] = useState<Record<string, Record<string, string>>>({});
  const [showTokens, setShowTokens] = useState<Record<string, boolean>>({});
  const [isConnecting, setIsConnecting] = useState<string | null>(null);
  const [isTesting, setIsTesting] = useState<string | null>(null);
  const [isDeleting, setIsDeleting] = useState<string | null>(null);
  const [isSavingAllowlist, setIsSavingAllowlist] = useState<string | null>(null);
  const [testResults, setTestResults] = useState<
    Record<string, { success: boolean; message: string; botUsername?: string }>
  >({});
  const [copiedWebhook, setCopiedWebhook] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Allowlist management
  const [allowlistInput, setAllowlistInput] = useState<Record<string, string>>({});
  const [editingAllowlist, setEditingAllowlist] = useState<Record<string, string[]>>({});

  // Fetch channels on mount
  const fetchChannels = useCallback(async () => {
    try {
      setIsLoading(true);
      const data = await channelService.listChannels();
      setChannels(data);
    } catch (err) {
      console.error('Failed to fetch channels:', err);
      setError(err instanceof Error ? err.message : 'Failed to load channels');
    } finally {
      setIsLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchChannels();
  }, [fetchChannels]);

  // Get channel for a platform type
  const getChannelByPlatform = useCallback(
    (platform: string): Channel | undefined => {
      return channels.find(c => c.platform === platform);
    },
    [channels]
  );

  // Handle form input change
  const handleInputChange = (channelId: string, fieldKey: string, value: string) => {
    setFormData(prev => ({
      ...prev,
      [channelId]: {
        ...prev[channelId],
        [fieldKey]: value,
      },
    }));
    // Clear any previous error
    setError(null);
  };

  // Toggle token visibility
  const toggleTokenVisibility = (channelId: string) => {
    setShowTokens(prev => ({
      ...prev,
      [channelId]: !prev[channelId],
    }));
  };

  // Generate webhook URL for a channel
  const getWebhookUrl = (channel?: Channel): string => {
    if (channel?.webhookUrl) {
      return channel.webhookUrl;
    }
    const baseUrl = import.meta.env.VITE_API_BASE_URL || window.location.origin;
    return `${baseUrl}/api/channels/telegram/webhook/{webhook_secret}`;
  };

  // Copy webhook URL to clipboard
  const handleCopyWebhook = async (channelConfigId: string, channel?: Channel) => {
    const url = getWebhookUrl(channel);
    try {
      await navigator.clipboard.writeText(url);
      setCopiedWebhook(channelConfigId);
      setTimeout(() => setCopiedWebhook(null), 2000);
    } catch (err) {
      console.error('Failed to copy:', err);
    }
  };

  // Connect channel (create channel)
  const handleConnect = async (channelConfig: ChannelConfig) => {
    const channelFormData = formData[channelConfig.id];
    if (!channelFormData) {
      setError('Please fill in all required fields');
      return;
    }

    // Validate required fields
    for (const field of channelConfig.fields) {
      if (!channelFormData[field.key]?.trim()) {
        setError(`${field.label} is required`);
        return;
      }
    }

    setIsConnecting(channelConfig.id);
    setError(null);

    try {
      await channelService.createChannel({
        platform: channelConfig.platform,
        config: channelFormData,
      });

      // Clear form data after successful connection
      setFormData(prev => {
        const newData = { ...prev };
        delete newData[channelConfig.id];
        return newData;
      });

      // Refresh channels
      await fetchChannels();
    } catch (err) {
      console.error('Failed to connect:', err);
      setError(err instanceof Error ? err.message : 'Failed to connect channel');
    } finally {
      setIsConnecting(null);
    }
  };

  // Disconnect channel (delete channel)
  const handleDisconnect = async (channel: Channel, platform: string) => {
    setIsDeleting(channel.id);
    setError(null);

    try {
      await channelService.deleteChannel(channel.id);
      setTestResults(prev => {
        const newResults = { ...prev };
        delete newResults[platform];
        return newResults;
      });
      // Refresh channels
      await fetchChannels();
    } catch (err) {
      console.error('Failed to disconnect:', err);
      setError(err instanceof Error ? err.message : 'Failed to disconnect channel');
    } finally {
      setIsDeleting(null);
    }
  };

  // Test channel connection
  const handleTestConnection = async (channel: Channel, platform: string) => {
    setIsTesting(channel.id);
    setError(null);

    try {
      const result = await channelService.testChannel(channel.id);
      setTestResults(prev => ({
        ...prev,
        [platform]: {
          success: result.success,
          message: result.success
            ? `Connection successful! Bot: @${result.botUsername || 'Unknown'}`
            : result.message || 'Connection failed',
          botUsername: result.botUsername,
        },
      }));
    } catch (err) {
      console.error('Test failed:', err);
      setTestResults(prev => ({
        ...prev,
        [platform]: {
          success: false,
          message: err instanceof Error ? err.message : 'Test failed',
        },
      }));
    } finally {
      setIsTesting(null);
    }
  };

  // Initialize allowlist editing state for a channel
  const initializeAllowlistEdit = (channel: Channel) => {
    setEditingAllowlist(prev => ({
      ...prev,
      [channel.id]: channel.allowedUsers || [],
    }));
  };

  // Add user to allowlist
  const handleAddToAllowlist = (channelId: string) => {
    const input = allowlistInput[channelId]?.trim();
    if (!input) return;

    // Remove @ prefix if present
    const normalizedInput = input.startsWith('@') ? input.slice(1) : input;

    setEditingAllowlist(prev => {
      const current = prev[channelId] || [];
      if (current.includes(normalizedInput)) return prev; // Don't add duplicates
      return {
        ...prev,
        [channelId]: [...current, normalizedInput],
      };
    });

    // Clear input
    setAllowlistInput(prev => ({
      ...prev,
      [channelId]: '',
    }));
  };

  // Remove user from allowlist
  const handleRemoveFromAllowlist = (channelId: string, user: string) => {
    setEditingAllowlist(prev => ({
      ...prev,
      [channelId]: (prev[channelId] || []).filter(u => u !== user),
    }));
  };

  // Save allowlist changes
  const handleSaveAllowlist = async (channel: Channel) => {
    setIsSavingAllowlist(channel.id);
    setError(null);

    try {
      const newAllowlist = editingAllowlist[channel.id] || [];
      await channelService.updateChannel(channel.id, {
        allowedUsers: newAllowlist,
      });
      await fetchChannels();
    } catch (err) {
      console.error('Failed to update allowlist:', err);
      setError(err instanceof Error ? err.message : 'Failed to update access list');
    } finally {
      setIsSavingAllowlist(null);
    }
  };

  // Check if allowlist has unsaved changes
  const hasAllowlistChanges = (channel: Channel): boolean => {
    const editing = editingAllowlist[channel.id];
    if (!editing) return false;
    const current = channel.allowedUsers || [];
    if (editing.length !== current.length) return true;
    return !editing.every(u => current.includes(u));
  };

  if (isLoading) {
    return (
      <div className="channels-loading">
        <Loader2 className="animate-spin" size={24} />
        <span>Loading channels...</span>
      </div>
    );
  }

  return (
    <div className="channels-section">
      <div className="channels-header">
        <h2 className="channels-title">Communication Channels</h2>
        <p className="channels-description">
          Connect external messaging platforms to chat with DobbyAI. Each channel creates a
          secure webhook endpoint for real-time communication.
        </p>
      </div>

      {/* Security Warning */}
      <div className="channels-security-warning">
        <Shield size={18} />
        <div className="channels-security-warning-content">
          <strong>⚠️ Important Security Notice</strong>
          <p>
            When you connect a bot,{' '}
            <strong>
              anyone who messages it will use your API credits, tools, and integrations
            </strong>
            . To protect your account, add your Telegram username or user ID to the{' '}
            <strong>Access Control</strong> list after connecting.
          </p>
          <p className="channels-security-warning-tip">
            💡 <strong>Tip:</strong> To find your Telegram user ID, message{' '}
            <a href="https://t.me/userinfobot" target="_blank" rel="noopener noreferrer">
              @userinfobot
            </a>{' '}
            on Telegram.
          </p>
        </div>
      </div>

      {/* Error Banner */}
      {error && (
        <div className="channels-error">
          <AlertCircle size={16} />
          <span>{error}</span>
          <button onClick={() => setError(null)} className="channels-error-close">
            <XCircle size={16} />
          </button>
        </div>
      )}

      {/* Channel Cards */}
      <div className="channels-grid">
        {CHANNEL_CONFIGS.map(channelConfig => {
          const channel = getChannelByPlatform(channelConfig.platform);
          const isConnected = !!channel;
          const channelFormData = formData[channelConfig.id] || {};
          const testResult = testResults[channelConfig.platform];

          return (
            <div
              key={channelConfig.id}
              className={`channel-card ${isConnected ? 'channel-card--connected' : ''}`}
            >
              {/* Card Header */}
              <div className="channel-card-header">
                <div className="channel-card-icon">{channelConfig.icon}</div>
                <div className="channel-card-info">
                  <h3 className="channel-card-name">{channelConfig.name}</h3>
                  <span
                    className={`channel-card-status ${isConnected ? 'channel-card-status--connected' : ''}`}
                  >
                    {isConnected ? (
                      <>
                        <CheckCircle size={12} />
                        Connected{channel.botUsername ? ` (@${channel.botUsername})` : ''}
                      </>
                    ) : (
                      <>
                        <XCircle size={12} />
                        Not connected
                      </>
                    )}
                  </span>
                </div>
                <a
                  href={channelConfig.docsUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="channel-card-docs"
                  title="View documentation"
                >
                  <ExternalLink size={16} />
                </a>
              </div>

              {/* Description */}
              <p className="channel-card-description">{channelConfig.description}</p>

              {/* Connected State */}
              {isConnected && channel && (
                <div className="channel-connected">
                  {/* Polling Mode Notice for localhost */}
                  {channel.webhookUrl?.includes('localhost') && (
                    <div className="channel-polling-notice">
                      <AlertCircle size={14} />
                      <span>
                        <strong>Long polling mode:</strong> Messages are fetched automatically
                        (localhost detected). For production, use a public URL.
                      </span>
                    </div>
                  )}

                  {/* Webhook URL */}
                  <div className="channel-webhook">
                    <label className="channel-webhook-label">
                      {channel.webhookUrl?.includes('localhost')
                        ? 'Webhook URL (not used in polling mode)'
                        : 'Webhook URL'}
                    </label>
                    <div className="channel-webhook-row">
                      <code className="channel-webhook-url">{getWebhookUrl(channel)}</code>
                      <button
                        onClick={() => handleCopyWebhook(channelConfig.id, channel)}
                        className="channel-webhook-copy"
                        title="Copy webhook URL"
                      >
                        {copiedWebhook === channelConfig.id ? (
                          <Check size={14} />
                        ) : (
                          <Copy size={14} />
                        )}
                      </button>
                    </div>
                    {!channel.webhookUrl?.includes('localhost') && (
                      <p className="channel-webhook-help">
                        Set this URL as your bot's webhook in BotFather using /setwebhook
                      </p>
                    )}
                  </div>

                  {/* Test Result */}
                  {testResult && (
                    <div
                      className={`channel-test-result ${testResult.success ? 'channel-test-result--success' : 'channel-test-result--error'}`}
                    >
                      {testResult.success ? <CheckCircle size={14} /> : <AlertCircle size={14} />}
                      <span>{testResult.message}</span>
                    </div>
                  )}

                  {/* Access Control / Allowlist */}
                  <div className="channel-allowlist">
                    <div className="channel-allowlist-header">
                      <Shield size={16} />
                      <label className="channel-allowlist-label">Access Control</label>
                    </div>
                    <p className="channel-allowlist-help">
                      {(channel.allowedUsers?.length || 0) === 0
                        ? 'Anyone can message this bot. Add Telegram usernames or user IDs to restrict access.'
                        : `Only ${channel.allowedUsers?.length} user(s) can access this bot.`}
                    </p>

                    {/* Initialize editing state if needed */}
                    {editingAllowlist[channel.id] === undefined && (
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => initializeAllowlistEdit(channel)}
                        className="channel-allowlist-edit-btn"
                      >
                        <Users size={14} />
                        Manage Access
                      </Button>
                    )}

                    {/* Allowlist Editor */}
                    {editingAllowlist[channel.id] !== undefined && (
                      <div className="channel-allowlist-editor">
                        {/* Current users */}
                        <div className="channel-allowlist-users">
                          {(editingAllowlist[channel.id] || []).map(user => (
                            <div key={user} className="channel-allowlist-user">
                              <span>@{user}</span>
                              <button
                                onClick={() => handleRemoveFromAllowlist(channel.id, user)}
                                className="channel-allowlist-user-remove"
                                title="Remove user"
                              >
                                <X size={12} />
                              </button>
                            </div>
                          ))}
                          {(editingAllowlist[channel.id] || []).length === 0 && (
                            <span className="channel-allowlist-empty">
                              No restrictions - anyone can use this bot
                            </span>
                          )}
                        </div>

                        {/* Add user input */}
                        <div className="channel-allowlist-add">
                          <input
                            type="text"
                            className="channel-allowlist-input"
                            placeholder="Username or user ID"
                            value={allowlistInput[channel.id] || ''}
                            onChange={e =>
                              setAllowlistInput(prev => ({
                                ...prev,
                                [channel.id]: e.target.value,
                              }))
                            }
                            onKeyDown={e => {
                              if (e.key === 'Enter') {
                                handleAddToAllowlist(channel.id);
                              }
                            }}
                          />
                          <Button
                            variant="secondary"
                            size="sm"
                            onClick={() => handleAddToAllowlist(channel.id)}
                            disabled={!allowlistInput[channel.id]?.trim()}
                          >
                            <Plus size={14} />
                            Add
                          </Button>
                        </div>

                        {/* Save button */}
                        <div className="channel-allowlist-actions">
                          <Button
                            variant="primary"
                            size="sm"
                            onClick={() => handleSaveAllowlist(channel)}
                            disabled={
                              isSavingAllowlist === channel.id || !hasAllowlistChanges(channel)
                            }
                          >
                            {isSavingAllowlist === channel.id ? (
                              <>
                                <Loader2 size={14} className="animate-spin" />
                                Saving...
                              </>
                            ) : (
                              <>
                                <Check size={14} />
                                Save Access List
                              </>
                            )}
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => {
                              setEditingAllowlist(prev => {
                                const newState = { ...prev };
                                delete newState[channel.id];
                                return newState;
                              });
                            }}
                          >
                            Cancel
                          </Button>
                        </div>
                      </div>
                    )}
                  </div>

                  {/* Actions */}
                  <div className="channel-actions">
                    <Button
                      variant="secondary"
                      size="sm"
                      onClick={() => handleTestConnection(channel, channelConfig.platform)}
                      disabled={isTesting === channel.id}
                    >
                      {isTesting === channel.id ? (
                        <>
                          <Loader2 size={14} className="animate-spin" />
                          Testing...
                        </>
                      ) : (
                        <>
                          <RefreshCw size={14} />
                          Test Connection
                        </>
                      )}
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => handleDisconnect(channel, channelConfig.platform)}
                      disabled={isDeleting === channel.id}
                      className="channel-disconnect-btn"
                    >
                      {isDeleting === channel.id ? (
                        <>
                          <Loader2 size={14} className="animate-spin" />
                          Disconnecting...
                        </>
                      ) : (
                        <>
                          <Trash2 size={14} />
                          Disconnect
                        </>
                      )}
                    </Button>
                  </div>
                </div>
              )}

              {/* Not Connected State - Show Form */}
              {!isConnected && (
                <div className="channel-form">
                  {channelConfig.fields.map(field => (
                    <div key={field.key} className="channel-field">
                      <label className="channel-field-label">{field.label}</label>
                      <div className="channel-field-input-wrapper">
                        <input
                          type={
                            field.sensitive && !showTokens[channelConfig.id] ? 'password' : 'text'
                          }
                          className="channel-field-input"
                          placeholder={field.placeholder}
                          value={channelFormData[field.key] || ''}
                          onChange={e =>
                            handleInputChange(channelConfig.id, field.key, e.target.value)
                          }
                        />
                        {field.sensitive && (
                          <button
                            type="button"
                            className="channel-field-toggle"
                            onClick={() => toggleTokenVisibility(channelConfig.id)}
                          >
                            {showTokens[channelConfig.id] ? (
                              <EyeOff size={16} />
                            ) : (
                              <Eye size={16} />
                            )}
                          </button>
                        )}
                      </div>
                      <p className="channel-field-help">{field.helpText}</p>
                    </div>
                  ))}

                  <Button
                    variant="primary"
                    onClick={() => handleConnect(channelConfig)}
                    disabled={isConnecting === channelConfig.id}
                    className="channel-connect-btn"
                  >
                    {isConnecting === channelConfig.id ? (
                      <>
                        <Loader2 size={16} className="animate-spin" />
                        Connecting...
                      </>
                    ) : (
                      <>
                        <Send size={16} />
                        Connect {channelConfig.name}
                      </>
                    )}
                  </Button>
                </div>
              )}
            </div>
          );
        })}
      </div>

      {/* Coming Soon Placeholder */}
      <div className="channels-coming-soon">
        <h4>More Channels Coming Soon</h4>
        <p>
          Discord, Slack, WhatsApp, and more messaging platforms will be available in future
          updates.
        </p>
      </div>
    </div>
  );
};
