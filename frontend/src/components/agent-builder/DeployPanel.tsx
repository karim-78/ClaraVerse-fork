/**
 * DeployPanel Component
 *
 * Slide-in panel for deploying workflows and managing triggers.
 * Shows API endpoints, scheduling options, and deployment status.
 * Uses frosted glass design matching the app's aesthetic.
 */

import { useState, useEffect, useCallback } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import {
  X,
  Copy,
  Eye,
  EyeOff,
  Rocket,
  Clock,
  Webhook,
  CheckCircle2,
  Globe,
  Key,
  Code,
  Zap,
  Settings,
  ChevronRight,
  Trash2,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { useAgentBuilderStore } from '@/store/useAgentBuilderStore';
import {
  getSchedule,
  createSchedule,
  updateSchedule,
  deleteSchedule,
  COMMON_TIMEZONES,
  parseCronToHuman,
  formatNextRun,
  type Schedule,
} from '@/services/scheduleService';
import {
  createAPIKey,
  listAPIKeys,
  type CreateAPIKeyResponse,
  type APIKey as APIKeyType,
} from '@/services/apiKeyService';
import {
  generateDeploymentCode,
  generateWebhookCode,
  type Language,
  type WebhookResponseMode,
} from '@/services/deployCodeGenerator';
import { getAgentWebhook, type AgentWebhookInfo } from '@/services/agentService';
import type { WebhookTriggerConfig, Block } from '@/types/agent';

interface DeployPanelProps {
  isOpen: boolean;
  onClose: () => void;
}

export function DeployPanel({ isOpen, onClose }: DeployPanelProps) {
  const { currentAgent } = useAgentBuilderStore();
  const [showApiKey, setShowApiKey] = useState(false);
  const [copiedEndpoint, setCopiedEndpoint] = useState(false);
  const [copiedKey, setCopiedKey] = useState(false);
  const [copiedCurl, setCopiedCurl] = useState(false);
  const [isDeploying, setIsDeploying] = useState(false);
  const [deploymentStatus, setDeploymentStatus] = useState<'draft' | 'active' | 'deploying'>(
    'draft'
  );
  const [expandedSection, setExpandedSection] = useState<'api' | 'schedule' | 'webhook' | null>(
    'api'
  );

  // Schedule state
  const [schedule, setSchedule] = useState<Schedule | null>(null);
  const [loadingSchedule, setLoadingSchedule] = useState(false);
  const [scheduleType, setScheduleType] = useState<
    'hourly' | 'daily' | 'weekly' | 'monthly' | 'custom'
  >('daily');
  const [hourInterval, setHourInterval] = useState(6); // For hourly: every N hours
  const [selectedHour, setSelectedHour] = useState(9); // For daily/weekly/monthly
  const [selectedMinute, setSelectedMinute] = useState(0);
  const [selectedDayOfWeek, setSelectedDayOfWeek] = useState(1); // Monday
  const [selectedDayOfMonth, setSelectedDayOfMonth] = useState(1);
  const [customCron, setCustomCron] = useState('');
  const [selectedTimezone, setSelectedTimezone] = useState('UTC');
  const [scheduleEnabled, setScheduleEnabled] = useState(true);
  const [savingSchedule, setSavingSchedule] = useState(false);

  // API Key state
  const [apiKeyData, setApiKeyData] = useState<CreateAPIKeyResponse | null>(null);
  const [existingApiKey, setExistingApiKey] = useState<APIKeyType | null>(null);
  const [loadingApiKey, setLoadingApiKey] = useState(false);

  // Code generation state
  const [selectedLanguage, setSelectedLanguage] = useState<Language>('curl');

  // API Key generation modal state
  const [showApiKeyModal, setShowApiKeyModal] = useState(false);

  // Webhook info from backend (fetched separately from agent)
  const [webhookInfo, setWebhookInfo] = useState<AgentWebhookInfo | null>(null);

  // Detect trigger type from workflow blocks
  const webhookTriggerBlock: Block | undefined = currentAgent?.workflow?.blocks?.find(
    b => b.type === 'webhook_trigger'
  );
  const scheduleTriggerBlock: Block | undefined = currentAgent?.workflow?.blocks?.find(
    b => b.type === 'schedule_trigger'
  );

  const triggerType: 'webhook' | 'schedule' | 'none' = webhookTriggerBlock
    ? 'webhook'
    : scheduleTriggerBlock
      ? 'schedule'
      : 'none';

  // Webhook config
  const webhookConfig = webhookTriggerBlock?.config as WebhookTriggerConfig | undefined;
  const webhookResponseMode: WebhookResponseMode = webhookConfig?.responseMode || 'trigger_only';
  const webhookMethod = webhookConfig?.method || 'POST';

  // Computed values
  const baseApiUrl = import.meta.env.VITE_API_BASE_URL || window.location.origin;
  // Build webhook URL from the slug returned by backend + frontend's base URL
  const webhookEndpoint = webhookInfo?.path ? `${baseApiUrl}/api/wh/${webhookInfo.path}` : '';
  const endpoint = `${baseApiUrl}/api/trigger/${currentAgent?.id || 'xxx'}`;
  const statusUrl = `${baseApiUrl}/api/trigger/status/:executionId`;
  const webhookStatusUrl = `${baseApiUrl}/api/wh/status/:executionId`;
  const apiKey = apiKeyData?.key || existingApiKey?.key || '';
  const maskedKey = '••••••••••••••••••';

  // Get Start block configuration for input detection
  const startBlock = currentAgent?.workflow?.blocks?.find(block => {
    if (block.type === 'variable') {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const config = block.config as any;
      return config.operation === 'read' && config.variableName === 'input';
    }
    return false;
  });
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const startBlockConfig = startBlock ? (startBlock.config as any) : null;

  // Generate code based on trigger type and selected language
  const generatedCode =
    triggerType === 'webhook'
      ? generateWebhookCode({
          language: selectedLanguage,
          webhookUrl: webhookEndpoint,
          statusUrl: webhookStatusUrl,
          responseMode: webhookResponseMode,
          samplePayload: webhookConfig?.testData || undefined,
        })
      : generateDeploymentCode({
          language: selectedLanguage,
          triggerUrl: endpoint,
          statusUrl: statusUrl,
          apiKey: apiKey,
          agentId: currentAgent?.id || '',
          startBlockConfig: startBlockConfig,
        });

  const copyToClipboard = async (text: string, setCopied: (val: boolean) => void) => {
    await navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  // Fetch webhook info from backend (for webhook-trigger agents)
  const loadWebhookInfo = useCallback(async () => {
    if (!currentAgent?.id || triggerType !== 'webhook') {
      setWebhookInfo(null);
      return;
    }
    try {
      const info = await getAgentWebhook(currentAgent.id);
      setWebhookInfo(info);
    } catch {
      setWebhookInfo(null);
    }
  }, [currentAgent?.id, triggerType]);

  const handleDeploy = async () => {
    if (!currentAgent) return;

    setIsDeploying(true);
    setDeploymentStatus('deploying');

    try {
      // Check if there's already a universal API key with execute:* scope
      const keys = await listAPIKeys();
      const existingKey = keys.find(
        k =>
          k.scopes.includes('execute:*') &&
          !k.isRevoked &&
          (!k.expiresAt || new Date(k.expiresAt) > new Date())
      );

      if (existingKey) {
        // Reuse existing universal key - no need to create a new one
        setExistingApiKey(existingKey);
        setApiKeyData(null);
      } else {
        // First deployment ever - create ONE universal API key for all agents
        const newKey = await createAPIKey({
          name: 'Universal API Key',
          description:
            'Universal API key for all agents with full permissions (execute all, read executions, upload)',
          scopes: ['execute:*', 'read:executions', 'upload'],
        });
        setApiKeyData(newKey);
        setExistingApiKey(null);
      }

      setDeploymentStatus('active');
      setExpandedSection('api');

      // Refresh webhook info after deploy (backend creates webhook on deploy)
      await loadWebhookInfo();
    } catch (error) {
      console.error('Deployment failed:', error);
      setDeploymentStatus('draft');
    } finally {
      setIsDeploying(false);
    }
  };

  // Load schedule and API key when panel opens or agent changes
  useEffect(() => {
    if (!currentAgent?.id || !isOpen) return;

    // Check if already deployed
    if (currentAgent?.status === 'deployed') {
      setDeploymentStatus('active');
    }

    // Load schedule
    loadScheduleData();

    // Load existing API key
    loadExistingApiKey();

    // Load webhook info (for webhook-trigger agents)
    loadWebhookInfo();
  }, [currentAgent, isOpen, loadWebhookInfo]);

  const loadExistingApiKey = async () => {
    if (!currentAgent?.id) return;

    setLoadingApiKey(true);
    try {
      const keys = await listAPIKeys();
      // Look for universal API key with execute:* scope
      const existingKey = keys.find(
        k =>
          k.scopes.includes('execute:*') &&
          !k.isRevoked &&
          (!k.expiresAt || new Date(k.expiresAt) > new Date())
      );

      if (existingKey) {
        setExistingApiKey(existingKey);
        setDeploymentStatus('active');
      }
    } catch (error) {
      console.error('Failed to load API key:', error);
    } finally {
      setLoadingApiKey(false);
    }
  };

  // Build cron expression from UI state
  const buildCronExpression = (): string => {
    if (scheduleType === 'custom') {
      return customCron;
    }

    switch (scheduleType) {
      case 'hourly':
        return `0 */${hourInterval} * * *`;
      case 'daily':
        return `${selectedMinute} ${selectedHour} * * *`;
      case 'weekly':
        return `${selectedMinute} ${selectedHour} * * ${selectedDayOfWeek}`;
      case 'monthly':
        return `${selectedMinute} ${selectedHour} ${selectedDayOfMonth} * *`;
      default:
        return '0 9 * * *';
    }
  };

  // Parse cron expression to populate UI
  const parseCronToUI = (cron: string) => {
    const parts = cron.split(' ');
    if (parts.length !== 5) {
      setScheduleType('custom');
      setCustomCron(cron);
      return;
    }

    const [minute, hour, dayOfMonth, month, dayOfWeek] = parts;

    // Hourly pattern: 0 */N * * *
    if (
      minute === '0' &&
      hour.startsWith('*/') &&
      dayOfMonth === '*' &&
      month === '*' &&
      dayOfWeek === '*'
    ) {
      setScheduleType('hourly');
      setHourInterval(parseInt(hour.slice(2)) || 6);
      return;
    }

    // Daily pattern: M H * * *
    if (dayOfMonth === '*' && month === '*' && dayOfWeek === '*') {
      setScheduleType('daily');
      setSelectedMinute(parseInt(minute) || 0);
      setSelectedHour(parseInt(hour) || 9);
      return;
    }

    // Weekly pattern: M H * * D
    if (dayOfMonth === '*' && month === '*' && dayOfWeek !== '*') {
      setScheduleType('weekly');
      setSelectedMinute(parseInt(minute) || 0);
      setSelectedHour(parseInt(hour) || 9);
      setSelectedDayOfWeek(parseInt(dayOfWeek) || 1);
      return;
    }

    // Monthly pattern: M H D * *
    if (month === '*' && dayOfWeek === '*' && dayOfMonth !== '*') {
      setScheduleType('monthly');
      setSelectedMinute(parseInt(minute) || 0);
      setSelectedHour(parseInt(hour) || 9);
      setSelectedDayOfMonth(parseInt(dayOfMonth) || 1);
      return;
    }

    // Default to custom
    setScheduleType('custom');
    setCustomCron(cron);
  };

  const loadScheduleData = async () => {
    if (!currentAgent?.id) return;

    setLoadingSchedule(true);
    try {
      const scheduleData = await getSchedule(currentAgent.id);
      setSchedule(scheduleData);

      if (scheduleData) {
        // Populate form with existing schedule
        parseCronToUI(scheduleData.cronExpression);
        setSelectedTimezone(scheduleData.timezone);
        setScheduleEnabled(scheduleData.enabled);
      }
    } catch (error) {
      console.error('Failed to load schedule:', error);
    } finally {
      setLoadingSchedule(false);
    }
  };

  const handleSaveSchedule = async () => {
    if (!currentAgent?.id) return;

    const cronExpression = buildCronExpression();
    if (!cronExpression) return;

    setSavingSchedule(true);
    try {
      if (schedule) {
        // Update existing schedule
        const updated = await updateSchedule(currentAgent.id, {
          cron_expression: cronExpression,
          timezone: selectedTimezone,
          enabled: scheduleEnabled,
        });
        setSchedule(updated);
      } else {
        // Create new schedule
        const created = await createSchedule(currentAgent.id, {
          cron_expression: cronExpression,
          timezone: selectedTimezone,
          enabled: scheduleEnabled,
        });
        setSchedule(created);
      }
    } catch (error) {
      console.error('Failed to save schedule:', error);
    } finally {
      setSavingSchedule(false);
    }
  };

  const handleDeleteSchedule = async () => {
    if (!currentAgent?.id || !schedule) return;

    try {
      await deleteSchedule(currentAgent.id);
      setSchedule(null);
      setSelectedPreset('0 9 * * *');
      setSelectedTimezone('UTC');
      setScheduleEnabled(true);
    } catch (error) {
      console.error('Failed to delete schedule:', error);
    }
  };

  return (
    <AnimatePresence>
      {isOpen && (
        <>
          {/* Backdrop - frosted blur */}
          <motion.div
            className="fixed inset-0 bg-black/60 z-[200] backdrop-blur-md"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            onClick={onClose}
          />

          {/* Panel - frosted glass */}
          <motion.div
            className="fixed right-0 top-0 h-full w-full max-w-md bg-black/40 backdrop-blur-xl z-[201] shadow-2xl overflow-hidden flex flex-col"
            initial={{ x: '100%' }}
            animate={{ x: 0 }}
            exit={{ x: '100%' }}
            transition={{ type: 'spring', damping: 25, stiffness: 300 }}
          >
            {/* Header */}
            <div className="flex items-center justify-between px-6 py-5 bg-white/5">
              <div className="flex items-center gap-3">
                <div
                  className={cn(
                    'p-2.5 rounded-xl',
                    deploymentStatus === 'active'
                      ? 'bg-green-500/20'
                      : deploymentStatus === 'deploying'
                        ? 'bg-blue-500/20'
                        : 'bg-purple-500/20'
                  )}
                >
                  {deploymentStatus === 'active' ? (
                    <CheckCircle2 size={20} className="text-green-400" />
                  ) : deploymentStatus === 'deploying' ? (
                    <div className="w-5 h-5 border-2 border-blue-400/30 border-t-blue-400 rounded-full animate-spin" />
                  ) : (
                    <Rocket size={20} className="text-purple-400" />
                  )}
                </div>
                <div>
                  <h2 className="text-lg font-semibold text-white">Deploy & Trigger</h2>
                  <p className="text-sm text-white/50">
                    {deploymentStatus === 'active'
                      ? 'Active'
                      : deploymentStatus === 'deploying'
                        ? 'Deploying...'
                        : 'Ready to deploy'}
                  </p>
                </div>
              </div>
              <button
                onClick={onClose}
                className="p-2 rounded-xl hover:bg-white/10 text-white/40 hover:text-white transition-colors"
              >
                <X size={20} />
              </button>
            </div>

            {/* Content */}
            <div className="flex-1 overflow-y-auto p-6 space-y-5">
              {deploymentStatus === 'draft' ? (
                /* Initial Deploy State */
                <>
                  <div className="p-4 rounded-2xl bg-purple-500/10 backdrop-blur-sm">
                    <div className="flex items-start gap-3">
                      <Rocket size={18} className="text-purple-400 mt-0.5 flex-shrink-0" />
                      <div>
                        <p className="text-sm font-medium text-purple-300">Ready to Deploy?</p>
                        <p className="text-xs text-purple-300/60 mt-1">
                          Deploy your workflow to get an API endpoint and start triggering it from
                          anywhere.
                        </p>
                      </div>
                    </div>
                  </div>

                  <div className="space-y-3">
                    <div className="p-4 rounded-2xl bg-white/5">
                      <div className="flex items-center gap-3 mb-2">
                        <CheckCircle2 size={16} className="text-green-400" />
                        <p className="text-sm font-medium text-white">
                          Secure API endpoint is created
                        </p>
                      </div>
                    </div>
                    <div className="p-4 rounded-2xl bg-white/5">
                      <div className="flex items-center gap-3 mb-2">
                        <CheckCircle2 size={16} className="text-green-400" />
                        <p className="text-sm font-medium text-white">
                          API key is auto-generated for authentication
                        </p>
                      </div>
                    </div>
                    <div className="p-4 rounded-2xl bg-white/5">
                      <div className="flex items-center gap-3 mb-2">
                        <CheckCircle2 size={16} className="text-green-400" />
                        <p className="text-sm font-medium text-white">
                          Workflow becomes accessible via REST API
                        </p>
                      </div>
                    </div>
                  </div>

                  <button
                    onClick={handleDeploy}
                    disabled={isDeploying}
                    className="w-full flex items-center justify-center gap-2 p-4 rounded-lg bg-[var(--color-accent)] hover:bg-[var(--color-accent)]/80 text-white font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    {isDeploying ? (
                      <>
                        <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                        Deploying...
                      </>
                    ) : (
                      <>
                        <Rocket size={18} />
                        Deploy Workflow
                      </>
                    )}
                  </button>
                </>
              ) : (
                /* Active/Deployed State */
                <>
                  {/* Success Banner */}
                  {deploymentStatus === 'active' && (
                    <motion.div
                      initial={{ opacity: 0, y: -10 }}
                      animate={{ opacity: 1, y: 0 }}
                      className="p-4 rounded-2xl bg-green-500/10 backdrop-blur-sm"
                    >
                      <div className="flex items-start gap-3">
                        <CheckCircle2 size={18} className="text-green-400 mt-0.5 flex-shrink-0" />
                        <div>
                          <p className="text-sm font-medium text-green-300">
                            Your workflow is live!
                          </p>
                          <p className="text-xs text-green-300/60 mt-1">
                            Everything you need to trigger it is below
                          </p>
                        </div>
                      </div>
                    </motion.div>
                  )}

                  {/* ====== WEBHOOK TRIGGER ====== */}
                  {triggerType === 'webhook' && (
                    <div className="space-y-4">
                      {/* Webhook Endpoint */}
                      <div>
                        <label className="block text-xs font-medium text-white/60 mb-2">
                          Webhook URL
                        </label>
                        {webhookEndpoint ? (
                          <div className="flex gap-2">
                            <input
                              type="text"
                              value={webhookEndpoint}
                              readOnly
                              className="flex-1 px-3 py-2 bg-white/5 border border-white/10 rounded-xl text-sm font-mono text-white/90 focus:outline-none focus:border-[var(--color-accent)]/50"
                            />
                            <button
                              onClick={() => copyToClipboard(webhookEndpoint, setCopiedEndpoint)}
                              className="px-3 py-2 bg-white/5 hover:bg-white/10 rounded-xl transition-colors"
                              title="Copy endpoint"
                            >
                              {copiedEndpoint ? (
                                <CheckCircle2 size={16} className="text-green-400" />
                              ) : (
                                <Copy size={16} className="text-white/60" />
                              )}
                            </button>
                          </div>
                        ) : (
                          <div className="p-3 rounded-xl bg-amber-500/10 border border-amber-500/20">
                            <p className="text-xs text-amber-300/80">
                              Deploy this agent to generate a unique webhook URL. Each agent gets
                              its own unique endpoint.
                            </p>
                          </div>
                        )}
                      </div>

                      {/* Webhook Info */}
                      <div className="p-3 rounded-xl bg-white/5 space-y-2">
                        <div className="flex items-center justify-between text-xs">
                          <span className="text-white/40">Method</span>
                          <span className="text-white/80 font-mono">{webhookMethod}</span>
                        </div>
                        <div className="flex items-center justify-between text-xs">
                          <span className="text-white/40">Response Mode</span>
                          <span
                            className={cn(
                              'px-2 py-0.5 rounded text-xs',
                              webhookResponseMode === 'respond_with_result'
                                ? 'bg-green-500/20 text-green-300'
                                : 'bg-amber-500/20 text-amber-300'
                            )}
                          >
                            {webhookResponseMode === 'respond_with_result'
                              ? 'Synchronous'
                              : 'Fire & Forget'}
                          </span>
                        </div>
                      </div>

                      {/* Mode Explanation */}
                      <div className="p-3 rounded-xl bg-blue-500/10 border border-blue-500/20">
                        <p className="text-xs text-blue-300/80">
                          {webhookResponseMode === 'respond_with_result'
                            ? 'This webhook waits for the workflow to complete and returns the result directly in the HTTP response.'
                            : 'This webhook returns immediately with an execution ID. Poll the status URL to get results.'}
                        </p>
                      </div>

                      {/* Integration Code — only shown when webhook URL is available */}
                      {webhookEndpoint && (
                        <div>
                          <div className="flex items-center justify-between mb-2">
                            <label className="block text-xs font-medium text-white/60">
                              Integration Code
                            </label>
                            <select
                              value={selectedLanguage}
                              onChange={e => setSelectedLanguage(e.target.value as Language)}
                              className="px-2 py-1 text-xs bg-white/5 border border-white/10 rounded-lg text-white/80 focus:outline-none focus:border-[var(--color-accent)]"
                            >
                              <option value="curl">cURL</option>
                              <option value="python">Python</option>
                              <option value="javascript-axios">JavaScript (Axios)</option>
                              <option value="javascript-fetch">JavaScript (Fetch)</option>
                              <option value="javascript-ajax">JavaScript (Ajax)</option>
                              <option value="go">Go</option>
                            </select>
                          </div>
                          <div className="relative">
                            <pre className="bg-black/40 text-gray-300 text-xs p-4 rounded-xl overflow-x-auto border border-white/10 max-h-[400px]">
                              <code>{generatedCode}</code>
                            </pre>
                            <button
                              onClick={() => copyToClipboard(generatedCode, setCopiedCurl)}
                              className="absolute top-2 right-2 px-2 py-1 bg-white/5 hover:bg-white/10 rounded-lg text-xs flex items-center gap-1.5 transition-colors"
                            >
                              {copiedCurl ? (
                                <>
                                  <CheckCircle2 size={12} className="text-green-400" />
                                  <span className="text-green-400">Copied!</span>
                                </>
                              ) : (
                                <>
                                  <Copy size={12} className="text-white/60" />
                                  <span className="text-white/60">Copy</span>
                                </>
                              )}
                            </button>
                          </div>
                        </div>
                      )}
                    </div>
                  )}

                  {/* ====== SCHEDULE TRIGGER ====== */}
                  {triggerType === 'schedule' && (
                    <div className="space-y-3">
                      <div className="flex items-center gap-3 p-4 rounded-lg bg-white/5">
                        <Clock size={18} className="text-amber-400" />
                        <div className="text-left flex-1">
                          <div className="flex items-center gap-2">
                            <p className="text-sm font-medium text-white">Schedule</p>
                            {schedule && schedule.enabled && (
                              <span className="px-2 py-0.5 bg-green-500/20 text-green-300 text-xs rounded">
                                Active
                              </span>
                            )}
                            {schedule && !schedule.enabled && (
                              <span className="px-2 py-0.5 bg-amber-500/20 text-amber-300 text-xs rounded">
                                Paused
                              </span>
                            )}
                          </div>
                          <p className="text-xs text-white/40">
                            {schedule
                              ? parseCronToHuman(schedule.cronExpression)
                              : 'Run automatically on a schedule'}
                          </p>
                        </div>
                      </div>
                    </div>
                  )}

                  {/* ====== NO TRIGGER — GENERIC API + GUIDANCE ====== */}
                  {triggerType === 'none' && (
                    <>
                      {/* Hint to add trigger blocks */}
                      <div className="p-4 rounded-xl bg-purple-500/10 border border-purple-500/20">
                        <div className="flex items-start gap-3">
                          <Webhook size={16} className="text-purple-400 mt-0.5 flex-shrink-0" />
                          <div>
                            <p className="text-sm font-medium text-purple-300">
                              Add a trigger block
                            </p>
                            <p className="text-xs text-purple-300/60 mt-1">
                              Add a Webhook Trigger or Schedule Trigger block to your workflow for
                              dedicated endpoints and automatic scheduling.
                            </p>
                          </div>
                        </div>
                      </div>
                    </>
                  )}

                  {/* API Section — shown for schedule + no-trigger modes */}
                  {triggerType !== 'webhook' && (
                    <div className="space-y-3">
                      <button
                        onClick={() => setExpandedSection(expandedSection === 'api' ? null : 'api')}
                        className="w-full flex items-center justify-between p-4 rounded-lg bg-white/5 hover:bg-white/10 transition-colors"
                      >
                        <div className="flex items-center gap-3">
                          <Globe size={18} className="text-blue-400" />
                          <div className="text-left">
                            <p className="text-sm font-medium text-white">API Endpoint</p>
                            <p className="text-xs text-white/40">Trigger via HTTP POST request</p>
                          </div>
                        </div>
                        <ChevronRight
                          size={18}
                          className={cn(
                            'text-white/30 transition-transform',
                            expandedSection === 'api' && 'rotate-90'
                          )}
                        />
                      </button>

                      <AnimatePresence>
                        {expandedSection === 'api' && (
                          <motion.div
                            initial={{ height: 0, opacity: 0 }}
                            animate={{ height: 'auto', opacity: 1 }}
                            exit={{ height: 0, opacity: 0 }}
                            className="overflow-hidden"
                          >
                            <div className="space-y-4 pl-3">
                              {/* Endpoint */}
                              <div>
                                <label className="block text-xs font-medium text-white/60 mb-2">
                                  Endpoint URL
                                </label>
                                <div className="flex gap-2">
                                  <input
                                    type="text"
                                    value={endpoint}
                                    readOnly
                                    className="flex-1 px-3 py-2 bg-white/5 border border-white/10 rounded-xl text-sm font-mono text-white/90 focus:outline-none focus:border-[var(--color-accent)]/50"
                                  />
                                  <button
                                    onClick={() => copyToClipboard(endpoint, setCopiedEndpoint)}
                                    className="px-3 py-2 bg-white/5 hover:bg-white/10 rounded-xl transition-colors"
                                    title="Copy endpoint"
                                  >
                                    {copiedEndpoint ? (
                                      <CheckCircle2 size={16} className="text-green-400" />
                                    ) : (
                                      <Copy size={16} className="text-white/60" />
                                    )}
                                  </button>
                                </div>
                              </div>

                              {/* API Key */}
                              <div>
                                <label className="block text-xs font-medium text-white/60 mb-2">
                                  API Key
                                </label>

                                {/* Success message for newly created key */}
                                {apiKeyData && (
                                  <div className="p-3 rounded-xl bg-green-500/10 border border-green-500/20 mb-2">
                                    <p className="text-xs text-green-300/80 font-medium">
                                      ✅ New API key created! Copy it now - it won't be shown again.
                                    </p>
                                  </div>
                                )}

                                {/* Show input if API key exists, otherwise show generate button */}
                                {apiKeyData || existingApiKey ? (
                                  <>
                                    <div className="flex gap-2">
                                      <input
                                        type={showApiKey ? 'text' : 'password'}
                                        value={showApiKey ? apiKey : maskedKey}
                                        readOnly
                                        className="flex-1 px-3 py-2 bg-white/5 border border-white/10 rounded-xl text-sm font-mono text-white/90 focus:outline-none focus:border-[var(--color-accent)]/50"
                                      />
                                      <button
                                        onClick={() => setShowApiKey(!showApiKey)}
                                        className="px-3 py-2 bg-white/5 hover:bg-white/10 rounded-xl transition-colors"
                                        title={showApiKey ? 'Hide' : 'Show'}
                                      >
                                        {showApiKey ? (
                                          <EyeOff size={16} className="text-white/60" />
                                        ) : (
                                          <Eye size={16} className="text-white/60" />
                                        )}
                                      </button>
                                      {apiKeyData && (
                                        <button
                                          onClick={() => copyToClipboard(apiKey, setCopiedKey)}
                                          className="px-3 py-2 bg-white/5 hover:bg-white/10 rounded-xl transition-colors"
                                          title="Copy API key"
                                        >
                                          {copiedKey ? (
                                            <CheckCircle2 size={16} className="text-green-400" />
                                          ) : (
                                            <Copy size={16} className="text-white/60" />
                                          )}
                                        </button>
                                      )}
                                    </div>

                                    {/* Info text with rotate option */}
                                    <div className="flex items-center justify-between mt-2">
                                      <p className="text-xs text-white/40">
                                        {apiKeyData
                                          ? 'New key created! Copy it now.'
                                          : existingApiKey
                                            ? existingApiKey.key
                                              ? 'Your universal API key for all agents'
                                              : 'Old key detected - click "Rotate Key" to get a visible key'
                                            : 'Deploy to generate your API key'}
                                      </p>
                                      {existingApiKey && !apiKeyData && (
                                        <button
                                          onClick={() => setShowApiKeyModal(true)}
                                          className="text-xs text-blue-400 hover:text-blue-300 transition-colors"
                                        >
                                          {existingApiKey.key ? 'Rotate Key' : 'Generate New Key'}
                                        </button>
                                      )}
                                    </div>
                                  </>
                                ) : (
                                  /* Generate API Key Button */
                                  <button
                                    onClick={() => setShowApiKeyModal(true)}
                                    disabled={loadingApiKey}
                                    className="w-full flex items-center justify-center gap-2 px-4 py-3 rounded-lg bg-[var(--color-accent)] hover:bg-[var(--color-accent)]/80 text-white font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                                  >
                                    {loadingApiKey ? (
                                      <>
                                        <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                                        Generating...
                                      </>
                                    ) : (
                                      <>
                                        <Key size={16} />
                                        Generate API Key
                                      </>
                                    )}
                                  </button>
                                )}
                              </div>

                              {/* Code Example with Language Selector */}
                              <div>
                                <div className="flex items-center justify-between mb-2">
                                  <label className="block text-xs font-medium text-white/60">
                                    Integration Code
                                  </label>
                                  <select
                                    value={selectedLanguage}
                                    onChange={e => setSelectedLanguage(e.target.value as Language)}
                                    className="px-2 py-1 text-xs bg-white/5 border border-white/10 rounded-lg text-white/80 focus:outline-none focus:border-[var(--color-accent)]"
                                  >
                                    <option value="curl">cURL</option>
                                    <option value="python">Python</option>
                                    <option value="javascript-axios">JavaScript (Axios)</option>
                                    <option value="javascript-fetch">JavaScript (Fetch)</option>
                                    <option value="javascript-ajax">JavaScript (Ajax)</option>
                                    <option value="go">Go</option>
                                  </select>
                                </div>
                                <div className="relative">
                                  <pre className="bg-black/40 text-gray-300 text-xs p-4 rounded-xl overflow-x-auto border border-white/10 max-h-[400px]">
                                    <code>{generatedCode}</code>
                                  </pre>
                                  <button
                                    onClick={() => copyToClipboard(generatedCode, setCopiedCurl)}
                                    className="absolute top-2 right-2 px-2 py-1 bg-white/5 hover:bg-white/10 rounded-lg text-xs flex items-center gap-1.5 transition-colors"
                                  >
                                    {copiedCurl ? (
                                      <>
                                        <CheckCircle2 size={12} className="text-green-400" />
                                        <span className="text-green-400">Copied!</span>
                                      </>
                                    ) : (
                                      <>
                                        <Copy size={12} className="text-white/60" />
                                        <span className="text-white/60">Copy</span>
                                      </>
                                    )}
                                  </button>
                                </div>

                                {/* Input Type Info */}
                                {startBlockConfig && (
                                  <div className="mt-2 px-3 py-2 bg-blue-500/10 border border-blue-500/20 rounded-lg">
                                    <p className="text-xs text-blue-200/90">
                                      {startBlockConfig.inputType === 'file' &&
                                        '📎 This workflow accepts file uploads. Upload the file first, then reference it in the trigger.'}
                                      {startBlockConfig.inputType === 'json' &&
                                        '📋 This workflow accepts JSON input. Customize the input object as needed.'}
                                      {(!startBlockConfig.inputType ||
                                        startBlockConfig.inputType === 'text') &&
                                        '💬 This workflow accepts text input. Customize the message as needed.'}
                                    </p>
                                  </div>
                                )}
                              </div>
                            </div>
                          </motion.div>
                        )}
                      </AnimatePresence>
                    </div>
                  )}

                  {/* Schedule Section — shown for schedule + no-trigger modes */}
                  {triggerType !== 'webhook' && (
                    <div className="space-y-3">
                      <button
                        onClick={() =>
                          setExpandedSection(expandedSection === 'schedule' ? null : 'schedule')
                        }
                        className="w-full flex items-center justify-between p-4 rounded-lg bg-white/5 hover:bg-white/10 transition-colors"
                      >
                        <div className="flex items-center gap-3">
                          <Clock size={18} className="text-amber-400" />
                          <div className="text-left flex-1">
                            <div className="flex items-center gap-2">
                              <p className="text-sm font-medium text-white">Schedule</p>
                              {schedule && schedule.enabled && (
                                <span className="px-2 py-0.5 bg-green-500/20 text-green-300 text-xs rounded">
                                  Active
                                </span>
                              )}
                              {schedule && !schedule.enabled && (
                                <span className="px-2 py-0.5 bg-amber-500/20 text-amber-300 text-xs rounded">
                                  Paused
                                </span>
                              )}
                            </div>
                            <p className="text-xs text-white/40">
                              {schedule
                                ? parseCronToHuman(schedule.cronExpression)
                                : 'Run automatically on a schedule'}
                            </p>
                          </div>
                        </div>
                        <ChevronRight
                          size={18}
                          className={cn(
                            'text-white/30 transition-transform',
                            expandedSection === 'schedule' && 'rotate-90'
                          )}
                        />
                      </button>

                      <AnimatePresence>
                        {expandedSection === 'schedule' && (
                          <motion.div
                            initial={{ height: 0, opacity: 0 }}
                            animate={{ height: 'auto', opacity: 1 }}
                            exit={{ height: 0, opacity: 0 }}
                            className="overflow-hidden"
                          >
                            <div className="space-y-4 pl-3">
                              {loadingSchedule ? (
                                <div className="p-4 rounded-xl bg-white/5 text-center">
                                  <div className="w-8 h-8 border-2 border-white/20 border-t-white/60 rounded-full animate-spin mx-auto mb-2" />
                                  <p className="text-xs text-white/40">Loading schedule...</p>
                                </div>
                              ) : (
                                <>
                                  {/* Existing Schedule Info */}
                                  {schedule && (
                                    <div className="p-4 rounded-xl bg-green-500/10 border border-green-500/20">
                                      <div className="flex items-start gap-3 mb-3">
                                        <CheckCircle2 size={16} className="text-green-400 mt-0.5" />
                                        <div className="flex-1">
                                          <p className="text-sm font-medium text-green-300">
                                            Schedule Active
                                          </p>
                                          <p className="text-xs text-green-300/60 mt-1">
                                            {parseCronToHuman(schedule.cronExpression)} •{' '}
                                            {schedule.timezone}
                                          </p>
                                        </div>
                                        {!schedule.enabled && (
                                          <span className="px-2 py-1 bg-amber-500/20 text-amber-300 text-xs rounded-lg">
                                            Paused
                                          </span>
                                        )}
                                      </div>
                                      <div className="flex items-center justify-between text-xs">
                                        <span className="text-white/40">Next run:</span>
                                        <span className="text-white/80">
                                          {formatNextRun(schedule.nextRunAt)}
                                        </span>
                                      </div>
                                      <div className="flex items-center justify-between text-xs mt-1">
                                        <span className="text-white/40">Total runs:</span>
                                        <span className="text-white/80">{schedule.totalRuns}</span>
                                      </div>
                                    </div>
                                  )}

                                  {/* Schedule Type */}
                                  <div>
                                    <label className="block text-xs font-medium text-white/60 mb-2">
                                      Frequency
                                    </label>
                                    <select
                                      value={scheduleType}
                                      onChange={e =>
                                        setScheduleType(
                                          e.target.value as
                                            | 'hourly'
                                            | 'daily'
                                            | 'weekly'
                                            | 'monthly'
                                            | 'custom'
                                        )
                                      }
                                      className="w-full px-3 py-2 bg-white/5 border border-white/10 rounded-xl text-sm text-white/90 focus:outline-none focus:border-[var(--color-accent)]/50"
                                    >
                                      <option value="hourly">Hourly</option>
                                      <option value="daily">Daily</option>
                                      <option value="weekly">Weekly</option>
                                      <option value="monthly">Monthly</option>
                                      <option value="custom">Custom (Cron)</option>
                                    </select>
                                  </div>

                                  {/* Hourly Configuration */}
                                  {scheduleType === 'hourly' && (
                                    <div>
                                      <label className="block text-xs font-medium text-white/60 mb-2">
                                        Run every
                                      </label>
                                      <div className="flex items-center gap-2">
                                        <select
                                          value={hourInterval}
                                          onChange={e => setHourInterval(parseInt(e.target.value))}
                                          className="flex-1 px-3 py-2 bg-white/5 border border-white/10 rounded-xl text-sm text-white/90 focus:outline-none focus:border-[var(--color-accent)]/50"
                                        >
                                          {[1, 2, 3, 4, 6, 8, 12].map(h => (
                                            <option key={h} value={h}>
                                              {h} {h === 1 ? 'hour' : 'hours'}
                                            </option>
                                          ))}
                                        </select>
                                      </div>
                                    </div>
                                  )}

                                  {/* Daily/Weekly/Monthly Time Configuration */}
                                  {(scheduleType === 'daily' ||
                                    scheduleType === 'weekly' ||
                                    scheduleType === 'monthly') && (
                                    <div>
                                      <label className="block text-xs font-medium text-white/60 mb-2">
                                        Time
                                      </label>
                                      <div className="grid grid-cols-2 gap-2">
                                        <select
                                          value={selectedHour}
                                          onChange={e => setSelectedHour(parseInt(e.target.value))}
                                          className="px-3 py-2 bg-white/5 border border-white/10 rounded-xl text-sm text-white/90 focus:outline-none focus:border-[var(--color-accent)]/50"
                                        >
                                          {Array.from({ length: 24 }, (_, i) => (
                                            <option key={i} value={i}>
                                              {i.toString().padStart(2, '0')}:00
                                            </option>
                                          ))}
                                        </select>
                                        <select
                                          value={selectedMinute}
                                          onChange={e =>
                                            setSelectedMinute(parseInt(e.target.value))
                                          }
                                          className="px-3 py-2 bg-white/5 border border-white/10 rounded-xl text-sm text-white/90 focus:outline-none focus:border-[var(--color-accent)]/50"
                                        >
                                          {[0, 15, 30, 45].map(m => (
                                            <option key={m} value={m}>
                                              :{m.toString().padStart(2, '0')}
                                            </option>
                                          ))}
                                        </select>
                                      </div>
                                    </div>
                                  )}

                                  {/* Weekly Day Selection */}
                                  {scheduleType === 'weekly' && (
                                    <div>
                                      <label className="block text-xs font-medium text-white/60 mb-2">
                                        Day of week
                                      </label>
                                      <select
                                        value={selectedDayOfWeek}
                                        onChange={e =>
                                          setSelectedDayOfWeek(parseInt(e.target.value))
                                        }
                                        className="w-full px-3 py-2 bg-white/5 border border-white/10 rounded-xl text-sm text-white/90 focus:outline-none focus:border-[var(--color-accent)]/50"
                                      >
                                        <option value={0}>Sunday</option>
                                        <option value={1}>Monday</option>
                                        <option value={2}>Tuesday</option>
                                        <option value={3}>Wednesday</option>
                                        <option value={4}>Thursday</option>
                                        <option value={5}>Friday</option>
                                        <option value={6}>Saturday</option>
                                      </select>
                                    </div>
                                  )}

                                  {/* Monthly Day Selection */}
                                  {scheduleType === 'monthly' && (
                                    <div>
                                      <label className="block text-xs font-medium text-white/60 mb-2">
                                        Day of month
                                      </label>
                                      <select
                                        value={selectedDayOfMonth}
                                        onChange={e =>
                                          setSelectedDayOfMonth(parseInt(e.target.value))
                                        }
                                        className="w-full px-3 py-2 bg-white/5 border border-white/10 rounded-xl text-sm text-white/90 focus:outline-none focus:border-[var(--color-accent)]/50"
                                      >
                                        {Array.from({ length: 31 }, (_, i) => i + 1).map(d => (
                                          <option key={d} value={d}>
                                            {d}
                                            {d === 1
                                              ? 'st'
                                              : d === 2
                                                ? 'nd'
                                                : d === 3
                                                  ? 'rd'
                                                  : 'th'}{' '}
                                            day
                                          </option>
                                        ))}
                                      </select>
                                    </div>
                                  )}

                                  {/* Custom Cron Input */}
                                  {scheduleType === 'custom' && (
                                    <div>
                                      <label className="block text-xs font-medium text-white/60 mb-2">
                                        Custom Cron Expression
                                      </label>
                                      <input
                                        type="text"
                                        value={customCron}
                                        onChange={e => setCustomCron(e.target.value)}
                                        placeholder="0 9 * * *"
                                        className="w-full px-3 py-2 bg-white/5 border border-white/10 rounded-xl text-sm font-mono text-white/90 focus:outline-none focus:border-[var(--color-accent)]/50"
                                      />
                                      <p className="text-xs text-white/40 mt-1">
                                        Format: minute hour day month weekday
                                      </p>
                                    </div>
                                  )}

                                  {/* Timezone */}
                                  <div>
                                    <label className="block text-xs font-medium text-white/60 mb-2">
                                      Timezone
                                    </label>
                                    <select
                                      value={selectedTimezone}
                                      onChange={e => setSelectedTimezone(e.target.value)}
                                      className="w-full px-3 py-2 bg-white/5 border border-white/10 rounded-xl text-sm text-white/90 focus:outline-none focus:border-[var(--color-accent)]/50 max-h-[200px]"
                                      size={1}
                                    >
                                      {COMMON_TIMEZONES.map(tz => (
                                        <option key={tz.value} value={tz.value}>
                                          {tz.label}
                                        </option>
                                      ))}
                                    </select>
                                  </div>

                                  {/* Enable/Disable Toggle */}
                                  <div className="flex items-center justify-between p-3 rounded-xl bg-white/5">
                                    <div>
                                      <p className="text-sm font-medium text-white">
                                        Enable Schedule
                                      </p>
                                      <p className="text-xs text-white/40">
                                        {scheduleEnabled
                                          ? 'Workflow will run automatically'
                                          : 'Workflow is paused'}
                                      </p>
                                    </div>
                                    <button
                                      onClick={() => setScheduleEnabled(!scheduleEnabled)}
                                      className={cn(
                                        'relative w-11 h-6 rounded-full transition-colors border-l-2',
                                        scheduleEnabled
                                          ? 'bg-green-500 border-l-green-400/40'
                                          : 'bg-white/20 border-l-white/20'
                                      )}
                                    >
                                      <div
                                        className={cn(
                                          'absolute top-0.5 left-0.5 w-5 h-5 bg-white rounded-full transition-transform shadow-sm',
                                          scheduleEnabled && 'translate-x-5'
                                        )}
                                      />
                                    </button>
                                  </div>

                                  {/* Action Buttons */}
                                  <div className="flex gap-2">
                                    <button
                                      onClick={handleSaveSchedule}
                                      disabled={savingSchedule}
                                      className="flex-1 flex items-center justify-center gap-2 px-4 py-3 bg-[var(--color-accent)] hover:bg-[var(--color-accent)]/80 text-white rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
                                    >
                                      {savingSchedule ? (
                                        <>
                                          <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                                          Saving...
                                        </>
                                      ) : schedule ? (
                                        <>
                                          <CheckCircle2 size={16} />
                                          Update Schedule
                                        </>
                                      ) : (
                                        <>
                                          <Clock size={16} />
                                          Create Schedule
                                        </>
                                      )}
                                    </button>

                                    {schedule && (
                                      <button
                                        onClick={handleDeleteSchedule}
                                        className="px-4 py-3 bg-red-500/20 hover:bg-red-500/30 text-red-300 rounded-lg transition-colors"
                                        title="Delete schedule"
                                      >
                                        <Trash2 size={16} />
                                      </button>
                                    )}
                                  </div>

                                  {/* Helper Text */}
                                  <div className="p-3 rounded-xl bg-blue-500/10 border border-blue-500/20">
                                    <p className="text-xs text-blue-300/80">
                                      The workflow will run automatically according to this schedule
                                      using the input defined in the Start block.
                                    </p>
                                  </div>
                                </>
                              )}
                            </div>
                          </motion.div>
                        )}
                      </AnimatePresence>
                    </div>
                  )}
                </>
              )}
            </div>

            {/* Footer */}
            <div className="px-6 py-4 bg-white/5">
              <button
                onClick={onClose}
                className="w-full px-4 py-3 rounded-lg text-sm text-white/60 hover:text-white hover:bg-white/5 transition-colors"
              >
                Close
              </button>
            </div>
          </motion.div>

          {/* API Key Generation Modal */}
          <AnimatePresence>
            {showApiKeyModal && (
              <>
                {/* Backdrop */}
                <motion.div
                  initial={{ opacity: 0 }}
                  animate={{ opacity: 1 }}
                  exit={{ opacity: 0 }}
                  onClick={() => setShowApiKeyModal(false)}
                  className="fixed inset-0 bg-black/60 backdrop-blur-sm z-[9999]"
                />

                {/* Modal */}
                <motion.div
                  initial={{ opacity: 0, scale: 0.95, y: 20 }}
                  animate={{ opacity: 1, scale: 1, y: 0 }}
                  exit={{ opacity: 0, scale: 0.95, y: 20 }}
                  className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 w-[90%] max-w-[420px] z-[10000] px-4"
                >
                  <div className="bg-[#0a0a0a] border border-white/10 rounded-xl shadow-2xl overflow-hidden">
                    {/* Header */}
                    <div className="px-4 py-3 bg-gradient-to-r from-[var(--color-accent)]/20 to-purple-500/20 border-b border-white/10">
                      <div className="flex items-center gap-2.5">
                        <div className="w-8 h-8 rounded-lg bg-[var(--color-accent)]/20 flex items-center justify-center flex-shrink-0">
                          <Key size={16} className="text-[var(--color-accent)]" />
                        </div>
                        <div className="flex-1">
                          <h3 className="text-sm font-semibold text-white">Generate API Key</h3>
                          <p className="text-[11px] text-white/50 mt-0.5">
                            Create universal access key
                          </p>
                        </div>
                      </div>
                    </div>

                    {/* Content */}
                    <div className="px-4 py-3 space-y-3">
                      {/* Explanation */}
                      <p className="text-xs text-white/70 leading-relaxed">
                        Create an API key to trigger and interact with your agent from external
                        applications.
                      </p>

                      {/* Permissions Box */}
                      <div className="p-3 rounded-lg bg-blue-500/10 border border-blue-500/20">
                        <div className="flex items-center gap-1.5 mb-2">
                          <Settings size={12} className="text-blue-400" />
                          <p className="text-[10px] font-semibold text-blue-300 uppercase tracking-wider">
                            Permissions
                          </p>
                        </div>
                        <ul className="space-y-1.5">
                          <li className="flex items-center gap-1.5 text-[11px] text-white/70">
                            <Zap size={10} className="text-blue-400" />
                            Execute workflows
                          </li>
                          <li className="flex items-center gap-1.5 text-[11px] text-white/70">
                            <Eye size={10} className="text-blue-400" />
                            Read execution results
                          </li>
                          <li className="flex items-center gap-1.5 text-[11px] text-white/70">
                            <Code size={10} className="text-blue-400" />
                            Upload files
                          </li>
                        </ul>
                      </div>

                      {/* Warning */}
                      <div className="p-2.5 rounded-lg bg-amber-500/10 border border-amber-500/20">
                        <p className="text-[11px] text-amber-200/90 leading-relaxed">
                          <strong className="font-semibold">Important:</strong> Keep this key
                          secure. Anyone with this key can execute your agent.
                        </p>
                      </div>
                    </div>

                    {/* Actions */}
                    <div className="px-4 py-3 bg-white/5 border-t border-white/10 flex items-center gap-2">
                      <button
                        onClick={() => setShowApiKeyModal(false)}
                        className="flex-1 px-3 py-2 rounded-lg text-xs font-medium text-white/60 hover:text-white hover:bg-white/5 transition-colors"
                      >
                        Cancel
                      </button>
                      <button
                        onClick={async () => {
                          setShowApiKeyModal(false);
                          await handleDeploy();
                        }}
                        disabled={loadingApiKey}
                        className="flex-1 px-3 py-2 rounded-lg text-xs font-medium bg-[var(--color-accent)] hover:bg-[var(--color-accent)]/80 text-white transition-colors disabled:opacity-50 disabled:cursor-not-allowed flex items-center justify-center gap-1.5"
                      >
                        {loadingApiKey ? (
                          <>
                            <div className="w-3 h-3 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                            Generating...
                          </>
                        ) : (
                          <>
                            <Key size={14} />
                            Generate Key
                          </>
                        )}
                      </button>
                    </div>
                  </div>
                </motion.div>
              </>
            )}
          </AnimatePresence>
        </>
      )}
    </AnimatePresence>
  );
}
