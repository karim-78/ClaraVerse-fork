import { useState, useEffect, useMemo } from 'react';
import {
  X,
  Sunrise,
  BookOpen,
  Bell,
  FolderSearch,
  Pencil,
  Search,
  ChevronDown,
  ChevronRight,
  Check,
  Wrench,
  Play,
  Loader2,
} from 'lucide-react';
import { useClawStore } from '@/store/useClawStore';
import './RoutineBuilder.css';
import { useModelStore } from '@/store/useModelStore';
import { ROUTINE_TEMPLATES, testRoutine } from '@/services/clawService';
import type { CreateRoutineRequest, AvailableTool } from '@/services/clawService';

const TEMPLATE_ICONS: Record<string, React.ReactNode> = {
  sunrise: <Sunrise size={18} />,
  'book-open': <BookOpen size={18} />,
  bell: <Bell size={18} />,
  'folder-search': <FolderSearch size={18} />,
};

interface RoutineBuilderProps {
  editingId: string | null;
  onClose: () => void;
}

export const RoutineBuilder = ({ editingId, onClose }: RoutineBuilderProps) => {
  const { routines, createRoutine, updateRoutine, telegramConnected, toolCategories, fetchTools } =
    useClawStore();
  const { models, fetchModels, selectedModelId } = useModelStore();

  const [tab, setTab] = useState<'templates' | 'custom'>(editingId ? 'custom' : 'templates');
  const [selectedTemplate, setSelectedTemplate] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Form state
  const [name, setName] = useState('');
  const [prompt, setPrompt] = useState('');
  const [frequency, setFrequency] = useState('daily');
  const [time, setTime] = useState('09:00');
  const [customCron, setCustomCron] = useState('');
  const [deliveryMethod, setDeliveryMethod] = useState<'telegram' | 'store'>(
    telegramConnected ? 'telegram' : 'store'
  );
  const [modelId, setModelId] = useState<string>(selectedModelId || '');

  // Tool selection state
  const [selectedTools, setSelectedTools] = useState<Set<string>>(new Set());
  const [toolSearch, setToolSearch] = useState('');
  const [expandedCategories, setExpandedCategories] = useState<Set<string>>(new Set());

  // Fetch tools and models on mount
  useEffect(() => {
    if (toolCategories.length === 0) fetchTools();
    if (models.length === 0) fetchModels();
  }, [toolCategories.length, fetchTools, models.length, fetchModels]);

  // Load editing routine
  useEffect(() => {
    if (editingId) {
      const routine = routines.find(r => r.id === editingId);
      if (routine) {
        setName(routine.name);
        setPrompt(routine.prompt);
        setDeliveryMethod(routine.deliveryMethod);
        setCustomCron(routine.cronExpression);
        setFrequency('custom');
        if (routine.modelId) {
          setModelId(routine.modelId);
        }
        if (routine.enabledTools?.length) {
          setSelectedTools(new Set(routine.enabledTools));
        }
      }
    }
  }, [editingId, routines]);

  // Filter tools by search
  const filteredCategories = useMemo(() => {
    if (!toolSearch.trim()) return toolCategories;
    const q = toolSearch.toLowerCase();
    return toolCategories
      .map(cat => ({
        ...cat,
        tools: cat.tools.filter(
          (t: AvailableTool) =>
            t.name.toLowerCase().includes(q) ||
            t.display_name.toLowerCase().includes(q) ||
            t.description.toLowerCase().includes(q)
        ),
      }))
      .filter(cat => cat.tools.length > 0);
  }, [toolCategories, toolSearch]);

  const toggleTool = (toolName: string) => {
    setSelectedTools(prev => {
      const next = new Set(prev);
      if (next.has(toolName)) {
        next.delete(toolName);
      } else {
        next.add(toolName);
      }
      return next;
    });
  };

  const toggleCategory = (categoryName: string) => {
    const cat = toolCategories.find(c => c.name === categoryName);
    if (!cat) return;
    const allSelected = cat.tools.every((t: AvailableTool) => selectedTools.has(t.name));
    setSelectedTools(prev => {
      const next = new Set(prev);
      for (const tool of cat.tools) {
        if (allSelected) {
          next.delete(tool.name);
        } else {
          next.add(tool.name);
        }
      }
      return next;
    });
  };

  const toggleCategoryExpanded = (categoryName: string) => {
    setExpandedCategories(prev => {
      const next = new Set(prev);
      if (next.has(categoryName)) {
        next.delete(categoryName);
      } else {
        next.add(categoryName);
      }
      return next;
    });
  };

  // Test state
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<string | null>(null);
  const [testError, setTestError] = useState<string | null>(null);

  const handleTest = async () => {
    if (!prompt.trim()) {
      setError('Write instructions before testing');
      return;
    }

    setTesting(true);
    setTestResult(null);
    setTestError(null);
    setError(null);

    try {
      const result = await testRoutine({
        name: name.trim() || undefined,
        prompt: prompt.trim(),
        modelId: modelId || undefined,
        enabledTools: selectedTools.size > 0 ? Array.from(selectedTools) : undefined,
      });
      setTestResult(result);
    } catch (err) {
      setTestError(err instanceof Error ? err.message : 'Test failed');
    } finally {
      setTesting(false);
    }
  };

  const handleSelectTemplate = (templateId: string) => {
    setSelectedTemplate(templateId);
    const template = ROUTINE_TEMPLATES.find(t => t.id === templateId);
    if (template) {
      setName(template.name);
      setPrompt(template.prompt);
      // Pre-select required tools from template
      if (template.requiredTools.length > 0) {
        setSelectedTools(new Set(template.requiredTools));
      }
      // Parse cron to set frequency/time
      const parts = template.cronExpression.split(' ');
      if (parts.length === 5) {
        const [min, hour] = parts;
        if (hour !== '*' && min !== '*') {
          setTime(
            `${parseInt(hour, 10).toString().padStart(2, '0')}:${parseInt(min, 10).toString().padStart(2, '0')}`
          );
          setFrequency('daily');
        } else {
          setCustomCron(template.cronExpression);
          setFrequency('custom');
        }
      }
      setTab('custom');
    }
  };

  const buildCronExpression = (): string => {
    if (frequency === 'custom') return customCron;

    const [h, m] = time.split(':').map(Number);

    switch (frequency) {
      case 'hourly':
        return `0 * * * *`;
      case 'daily':
        return `${m} ${h} * * *`;
      case 'weekdays':
        return `${m} ${h} * * 1-5`;
      case 'weekly':
        return `${m} ${h} * * 1`;
      default:
        return `${m} ${h} * * *`;
    }
  };

  const handleSave = async () => {
    if (!name.trim()) {
      setError('Name is required');
      return;
    }
    if (!prompt.trim()) {
      setError('Prompt/instructions are required');
      return;
    }
    if (frequency === 'custom' && !customCron.trim()) {
      setError('Cron expression is required');
      return;
    }

    setSaving(true);
    setError(null);

    try {
      const cronExpression = buildCronExpression();

      const enabledTools = selectedTools.size > 0 ? Array.from(selectedTools) : undefined;

      if (editingId) {
        await updateRoutine(editingId, {
          name: name.trim(),
          prompt: prompt.trim(),
          cronExpression,
          deliveryMethod,
          modelId: modelId || undefined,
          enabledTools: enabledTools ?? [],
        });
      } else {
        const data: CreateRoutineRequest = {
          name: name.trim(),
          prompt: prompt.trim(),
          cronExpression,
          timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
          deliveryMethod,
          modelId: modelId || undefined,
          enabledTools,
          template: selectedTemplate || 'custom',
        };
        await createRoutine(data);
      }
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save routine');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="claw-modal-overlay" onClick={onClose}>
      <div className="claw-modal" onClick={e => e.stopPropagation()}>
        <div className="claw-modal-header">
          <h3 className="claw-modal-title">{editingId ? 'Edit Routine' : 'New Routine'}</h3>
          <button className="claw-modal-close" onClick={onClose}>
            <X size={18} />
          </button>
        </div>

        {/* Tabs (only for create) */}
        {!editingId && (
          <div className="claw-tabs">
            <button
              className={`claw-tab ${tab === 'templates' ? 'active' : ''}`}
              onClick={() => setTab('templates')}
            >
              Templates
            </button>
            <button
              className={`claw-tab ${tab === 'custom' ? 'active' : ''}`}
              onClick={() => setTab('custom')}
            >
              Custom
            </button>
          </div>
        )}

        {/* Templates Tab */}
        {tab === 'templates' && !editingId && (
          <div className="claw-template-grid">
            {ROUTINE_TEMPLATES.map(template => (
              <div
                key={template.id}
                className={`claw-template-card ${selectedTemplate === template.id ? 'selected' : ''}`}
                onClick={() => handleSelectTemplate(template.id)}
              >
                <div className="claw-template-icon">
                  {TEMPLATE_ICONS[template.icon] || <Pencil size={18} />}
                </div>
                <div className="claw-template-info">
                  <div className="claw-template-name">{template.name}</div>
                  <div className="claw-template-desc">{template.description}</div>
                </div>
              </div>
            ))}
          </div>
        )}

        {/* Custom/Edit Tab */}
        {(tab === 'custom' || editingId) && (
          <>
            <div className="claw-form-group">
              <label className="claw-form-label">Name</label>
              <input
                className="claw-input"
                value={name}
                onChange={e => {
                  setName(e.target.value);
                  setError(null);
                }}
                placeholder="Morning Briefing"
              />
            </div>

            <div className="claw-form-group">
              <label className="claw-form-label">Instructions</label>
              <textarea
                className="claw-textarea"
                value={prompt}
                onChange={e => {
                  setPrompt(e.target.value);
                  setError(null);
                }}
                placeholder="Tell Dobby what to do when this routine runs..."
              />
              <p className="claw-form-help">
                Dobby will execute these instructions with access to your enabled tools.
              </p>
            </div>

            <div className="claw-form-group">
              <label className="claw-form-label">Schedule</label>
              <div className="claw-schedule-row">
                <div>
                  <select
                    className="claw-select"
                    value={frequency}
                    onChange={e => setFrequency(e.target.value)}
                  >
                    <option value="hourly">Every hour</option>
                    <option value="daily">Daily</option>
                    <option value="weekdays">Weekdays</option>
                    <option value="weekly">Weekly (Monday)</option>
                    <option value="custom">Custom cron</option>
                  </select>
                </div>
                {frequency !== 'hourly' && frequency !== 'custom' && (
                  <div>
                    <input
                      className="claw-input"
                      type="time"
                      value={time}
                      onChange={e => setTime(e.target.value)}
                    />
                  </div>
                )}
              </div>
              {frequency === 'custom' && (
                <div style={{ marginTop: '0.5rem' }}>
                  <input
                    className="claw-input"
                    value={customCron}
                    onChange={e => {
                      setCustomCron(e.target.value);
                      setError(null);
                    }}
                    placeholder="0 9 * * 1-5"
                  />
                  <p className="claw-form-help">
                    Standard cron format: minute hour day month weekday
                  </p>
                </div>
              )}
            </div>

            <div className="claw-form-group">
              <label className="claw-form-label">Deliver results via</label>
              <select
                className="claw-select"
                value={deliveryMethod}
                onChange={e => setDeliveryMethod(e.target.value as 'telegram' | 'store')}
              >
                <option value="telegram" disabled={!telegramConnected}>
                  Telegram{!telegramConnected ? ' (not connected)' : ''}
                </option>
                <option value="store">In-app (view in history)</option>
              </select>
            </div>

            <div className="claw-form-group">
              <label className="claw-form-label">Model</label>
              <select
                className="claw-select"
                value={modelId}
                onChange={e => setModelId(e.target.value)}
              >
                <option value="">Default</option>
                {models.map(m => (
                  <option key={m.id} value={m.id}>
                    {m.display_name || m.name}
                    {m.provider_name ? ` (${m.provider_name})` : ''}
                  </option>
                ))}
              </select>
              <p className="claw-form-help">
                Which model to use for this routine. Leave as Default to use your primary model.
              </p>
            </div>

            {/* Tool Picker */}
            <div className="claw-form-group">
              <label className="claw-form-label">
                <Wrench size={14} style={{ marginRight: '0.375rem', verticalAlign: '-2px' }} />
                Tools
                {selectedTools.size > 0 && (
                  <span className="routine-tool-count">{selectedTools.size} selected</span>
                )}
              </label>
              <p className="claw-form-help" style={{ marginTop: 0, marginBottom: '0.5rem' }}>
                Choose which tools this routine can use. Leave empty for no tool access.
              </p>

              {/* Selected tool chips */}
              {selectedTools.size > 0 && (
                <div className="routine-selected-chips">
                  {Array.from(selectedTools).map(toolName => (
                    <span
                      key={toolName}
                      className="claw-selected-tool-chip"
                      onClick={() => toggleTool(toolName)}
                    >
                      {toolName}
                      <X size={10} />
                    </span>
                  ))}
                  <button
                    className="routine-clear-tools"
                    onClick={() => setSelectedTools(new Set())}
                  >
                    Clear all
                  </button>
                </div>
              )}

              {/* Search */}
              <div className="routine-tool-search">
                <Search size={13} />
                <input
                  value={toolSearch}
                  onChange={e => setToolSearch(e.target.value)}
                  placeholder="Search tools..."
                />
              </div>

              {/* Tool list by category */}
              <div className="routine-tool-list">
                {filteredCategories.length === 0 && (
                  <div className="routine-tool-empty">
                    {toolCategories.length === 0
                      ? 'No tools available. Connect MCP or integrations first.'
                      : 'No tools match your search.'}
                  </div>
                )}
                {filteredCategories.map(cat => {
                  const isExpanded = expandedCategories.has(cat.name) || toolSearch.trim() !== '';
                  const selectedInCat = cat.tools.filter((t: AvailableTool) =>
                    selectedTools.has(t.name)
                  ).length;
                  const allSelected =
                    cat.tools.length > 0 &&
                    cat.tools.every((t: AvailableTool) => selectedTools.has(t.name));

                  return (
                    <div key={cat.name} className="routine-tool-category">
                      <button
                        className="claw-tool-category-header"
                        onClick={() => toggleCategoryExpanded(cat.name)}
                      >
                        <span className="claw-tool-category-name">
                          {isExpanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
                          {cat.name}
                          <span className="claw-tool-category-count">
                            {selectedInCat > 0 ? `${selectedInCat}/` : ''}
                            {cat.count}
                          </span>
                        </span>
                        <span
                          className={`routine-cat-toggle ${allSelected ? 'all' : selectedInCat > 0 ? 'partial' : ''}`}
                          onClick={e => {
                            e.stopPropagation();
                            toggleCategory(cat.name);
                          }}
                        >
                          {allSelected ? 'Deselect all' : 'Select all'}
                        </span>
                      </button>
                      {isExpanded && (
                        <div className="claw-tool-category-tools">
                          {cat.tools.map((tool: AvailableTool) => {
                            const isSelected = selectedTools.has(tool.name);
                            return (
                              <button
                                key={tool.name}
                                className={`claw-tool-item ${isSelected ? 'selected' : ''}`}
                                onClick={() => toggleTool(tool.name)}
                              >
                                <span className="claw-tool-item-check">
                                  {isSelected && <Check size={10} />}
                                </span>
                                <span className="claw-tool-item-info">
                                  <span className="claw-tool-item-name">
                                    {tool.display_name || tool.name}
                                  </span>
                                  <span className="claw-tool-item-source">{tool.source}</span>
                                </span>
                              </button>
                            );
                          })}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>

            {/* Test Result */}
            {(testResult || testError || testing) && (
              <div className="routine-test-result">
                <div className="routine-test-result-header">
                  Test Result
                  {testResult && (
                    <button
                      className="routine-test-dismiss"
                      onClick={() => {
                        setTestResult(null);
                        setTestError(null);
                      }}
                    >
                      <X size={14} />
                    </button>
                  )}
                </div>
                <div className="routine-test-result-body">
                  {testing && (
                    <div className="routine-test-loading">
                      <Loader2 size={16} className="routine-spin" />
                      Running routine...
                    </div>
                  )}
                  {testError && <div className="routine-test-error">{testError}</div>}
                  {testResult && <div className="routine-test-output">{testResult}</div>}
                </div>
              </div>
            )}

            {error && (
              <div
                style={{
                  padding: '0.5rem 0.75rem',
                  background: 'rgba(239, 68, 68, 0.1)',
                  border: '1px solid rgba(239, 68, 68, 0.2)',
                  borderRadius: '0.5rem',
                  color: '#ef4444',
                  fontSize: '0.8125rem',
                  marginBottom: '1rem',
                }}
              >
                {error}
              </div>
            )}

            <div className="claw-wizard-actions">
              <button className="claw-btn claw-btn-secondary" onClick={onClose}>
                Cancel
              </button>
              <button
                className="claw-btn claw-btn-ghost"
                onClick={handleTest}
                disabled={testing || saving || !prompt.trim()}
              >
                {testing ? (
                  <>
                    <Loader2 size={14} className="routine-spin" />
                    Testing...
                  </>
                ) : (
                  <>
                    <Play size={14} />
                    Test Run
                  </>
                )}
              </button>
              <button className="claw-btn claw-btn-primary" onClick={handleSave} disabled={saving}>
                {saving ? 'Saving...' : editingId ? 'Save Changes' : 'Create Routine'}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
};
