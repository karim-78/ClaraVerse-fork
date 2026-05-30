import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useParams, useNavigate, Link } from 'react-router-dom';
import {
  ChevronLeft,
  ChevronRight,
  Check,
  Copy,
  History,
  Code,
  Monitor,
  Plug,
  Server,
  Wrench,
} from 'lucide-react';
import { Button, Input, Textarea, Switch, Spinner } from '@/components/design-system';
import { Select } from '@/components/design-system';
import { useSkillStore } from '@/store/useSkillStore';
import { fetchTools, searchTools } from '@/services/toolsService';
import type { Tool, ToolCategory } from '@/services/toolsService';
import type { CreateSkillRequest } from '@/services/skillService';
import './SkillEditor.css';

const CATEGORY_OPTIONS = [
  { value: 'research', label: 'Research & Web' },
  { value: 'communication', label: 'Communication' },
  { value: 'project-management', label: 'Project Management' },
  { value: 'data', label: 'Data & Analytics' },
  { value: 'content', label: 'Content Creation' },
  { value: 'productivity', label: 'Productivity' },
  { value: 'sales', label: 'Sales & CRM' },
  { value: 'code', label: 'Code & DevOps' },
  { value: 'database', label: 'Database & Storage' },
];

const MODE_OPTIONS = [
  { value: 'auto', label: 'Auto (route messages automatically)' },
  { value: 'manual', label: 'Manual (activate explicitly)' },
];

/** Derived MCP server info from tool grouping */
interface MCPServer {
  name: string;
  toolCount: number;
}

export const SkillEditor = () => {
  const { skillId } = useParams<{ skillId: string }>();
  const navigate = useNavigate();
  const isEditMode = Boolean(skillId);

  const { getSkill, createSkill, updateSkill, enableSkill, disableSkill, isSkillEnabled } =
    useSkillStore();

  // Form state
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [systemPrompt, setSystemPrompt] = useState('');
  const [category, setCategory] = useState('research');
  const [mode, setMode] = useState<'auto' | 'manual'>('auto');
  const [keywords, setKeywords] = useState('');
  const [triggerPatterns, setTriggerPatterns] = useState('');
  const [enabled, setEnabled] = useState(true);
  const [version, setVersion] = useState('');
  const [lastUpdate, setLastUpdate] = useState('');

  // Tool + server selection
  const [selectedTools, setSelectedTools] = useState<Set<string>>(new Set());
  const [selectedServers, setSelectedServers] = useState<Set<string>>(new Set());
  const [allTools, setAllTools] = useState<Tool[]>([]);
  const [deviceFilter, setDeviceFilter] = useState('');
  const [integrationFilter, setIntegrationFilter] = useState('');

  // UI state
  const [saving, setSaving] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);

  // Flatten tool categories into a single list
  const flattenTools = useCallback((categories: ToolCategory[]) => {
    return categories.flatMap(cat => cat.tools);
  }, []);

  // Load tools + skill data on mount
  useEffect(() => {
    const load = async () => {
      setLoading(true);
      try {
        const toolsResponse = await fetchTools();
        setAllTools(flattenTools(toolsResponse.categories));

        if (isEditMode && skillId) {
          const skill = await getSkill(skillId);
          setName(skill.name);
          setDescription(skill.description);
          setSystemPrompt(skill.system_prompt);
          setCategory(skill.category);
          setMode(skill.mode);
          setKeywords(skill.keywords?.join(', ') ?? '');
          setTriggerPatterns(skill.trigger_patterns?.join(', ') ?? '');
          setEnabled(isSkillEnabled(skillId));
          setVersion(skill.version);
          setLastUpdate(skill.updated_at);
          setSelectedTools(new Set(skill.required_tools ?? []));
          setSelectedServers(new Set(skill.preferred_servers ?? []));
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load data');
      } finally {
        setLoading(false);
      }
    };
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [skillId, isEditMode]);

  // Derive MCP servers by grouping mcp_local tools by category (category = server name)
  const mcpServers = useMemo(() => {
    const serverMap = new Map<string, number>();
    for (const tool of allTools) {
      if (tool.source === 'mcp_local') {
        const serverName = tool.category || 'mcp';
        serverMap.set(serverName, (serverMap.get(serverName) ?? 0) + 1);
      }
    }
    return Array.from(serverMap.entries())
      .map(([name, toolCount]): MCPServer => ({ name, toolCount }))
      .sort((a, b) => a.name.localeCompare(b.name));
  }, [allTools]);

  // Filter MCP servers
  const filteredServers = useMemo(() => {
    if (!deviceFilter) return mcpServers;
    const q = deviceFilter.toLowerCase();
    return mcpServers.filter(s => s.name.toLowerCase().includes(q));
  }, [mcpServers, deviceFilter]);

  // Integration tools = builtin + composio (individual tool cards)
  const integrationTools = useMemo(
    () => allTools.filter(t => t.source === 'builtin' || t.source === 'composio'),
    [allTools]
  );

  const filteredIntegrationTools = useMemo(
    () => (integrationFilter ? searchTools(integrationTools, integrationFilter) : integrationTools),
    [integrationTools, integrationFilter]
  );

  const handleServerToggle = useCallback((serverName: string) => {
    setSelectedServers(prev => {
      const next = new Set(prev);
      if (next.has(serverName)) {
        next.delete(serverName);
      } else {
        next.add(serverName);
      }
      return next;
    });
  }, []);

  const handleToolToggle = useCallback((toolName: string) => {
    setSelectedTools(prev => {
      const next = new Set(prev);
      if (next.has(toolName)) {
        next.delete(toolName);
      } else {
        next.add(toolName);
      }
      return next;
    });
  }, []);

  const totalSelected = selectedTools.size + selectedServers.size;

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(systemPrompt);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // silently fail
    }
  }, [systemPrompt]);

  const handleSave = async () => {
    if (!name.trim()) {
      setError('Skill name is required');
      return;
    }
    if (!systemPrompt.trim()) {
      setError('System prompt is required');
      return;
    }

    setSaving(true);
    setError(null);
    try {
      const payload: CreateSkillRequest = {
        name: name.trim(),
        description: description.trim(),
        icon: 'code',
        category,
        system_prompt: systemPrompt.trim(),
        required_tools: Array.from(selectedTools),
        preferred_servers: Array.from(selectedServers),
        keywords: keywords
          .split(',')
          .map(k => k.trim())
          .filter(Boolean),
        trigger_patterns: triggerPatterns
          .split(',')
          .map(t => t.trim())
          .filter(Boolean),
        mode,
      };

      if (isEditMode && skillId) {
        await updateSkill(skillId, payload);
        if (enabled) {
          await enableSkill(skillId);
        } else {
          await disableSkill(skillId);
        }
      } else {
        const newSkill = await createSkill(payload);
        if (enabled) {
          await enableSkill(newSkill.id);
        }
      }

      navigate('/skills');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save skill');
    } finally {
      setSaving(false);
    }
  };

  // Ctrl+S keyboard shortcut
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        e.preventDefault();
        handleSave();
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    name,
    description,
    systemPrompt,
    category,
    mode,
    keywords,
    triggerPatterns,
    selectedTools,
    selectedServers,
  ]);

  if (loading) {
    return (
      <div className="skill-editor__loading">
        <Spinner size="lg" />
        <span>Loading skill editor...</span>
      </div>
    );
  }

  return (
    <div className="skill-editor">
      {/* ── Top Bar ────────────────────────────────────────────────── */}
      <header className="skill-editor__topbar">
        <div className="skill-editor__topbar-left">
          <Link to="/skills" className="skill-editor__back">
            <ChevronLeft size={16} />
            Back to Library
          </Link>
          <h1 className="skill-editor__topbar-title">
            {isEditMode ? 'Configure Skill' : 'Create Skill'}
          </h1>
        </div>
        <div className="skill-editor__topbar-actions">
          <Button variant="ghost" onClick={() => navigate('/skills')}>
            Cancel
          </Button>
          <Button variant="primary" onClick={handleSave} disabled={saving}>
            {saving ? 'Saving...' : 'Save Changes'}
          </Button>
        </div>
      </header>

      <div className="skill-editor__body">
        {/* ── Sidebar ──────────────────────────────────────────────── */}
        <aside className="skill-editor__sidebar">
          <div className="skill-editor__sidebar-icon">
            <Code size={28} color="#fff" />
          </div>

          <div className="skill-editor__field">
            <label className="skill-editor__label">Skill Name</label>
            <Input
              value={name}
              onChange={e => setName(e.target.value)}
              placeholder="e.g., Code Runner"
            />
          </div>

          <div className="skill-editor__field">
            <label className="skill-editor__label">Description</label>
            <Textarea
              value={description}
              onChange={e => setDescription(e.target.value)}
              placeholder="What does this skill do?"
              rows={4}
            />
          </div>

          <hr className="skill-editor__sidebar-divider" />

          <div>
            <div className="skill-editor__enabled-row">
              <span className="skill-editor__enabled-label">Enabled</span>
              <Switch checked={enabled} onChange={setEnabled} />
            </div>
            <p className="skill-editor__enabled-hint">
              {enabled ? 'Currently active in library' : 'Disabled — not active'}
            </p>
          </div>

          <hr className="skill-editor__sidebar-divider" />

          {/* Advanced settings (collapsible) */}
          <div>
            <button
              className={`skill-editor__advanced-toggle ${advancedOpen ? 'skill-editor__advanced-toggle--open' : ''}`}
              onClick={() => setAdvancedOpen(!advancedOpen)}
            >
              <ChevronRight size={14} />
              Advanced
            </button>
            {advancedOpen && (
              <div className="skill-editor__advanced-content">
                <div className="skill-editor__field">
                  <label className="skill-editor__label">Category</label>
                  <Select options={CATEGORY_OPTIONS} value={category} onChange={setCategory} />
                </div>
                <div className="skill-editor__field">
                  <label className="skill-editor__label">Mode</label>
                  <Select
                    options={MODE_OPTIONS}
                    value={mode}
                    onChange={v => setMode(v as 'auto' | 'manual')}
                  />
                </div>
                <div className="skill-editor__field">
                  <label className="skill-editor__label">Keywords</label>
                  <Input
                    value={keywords}
                    onChange={e => setKeywords(e.target.value)}
                    placeholder="Comma-separated: review, code"
                  />
                </div>
                <div className="skill-editor__field">
                  <label className="skill-editor__label">Trigger Patterns</label>
                  <Input
                    value={triggerPatterns}
                    onChange={e => setTriggerPatterns(e.target.value)}
                    placeholder="Comma-separated: review this"
                  />
                </div>
              </div>
            )}
          </div>

          {isEditMode && (
            <>
              <hr className="skill-editor__sidebar-divider" />
              <div className="skill-editor__meta">
                <span className="skill-editor__label">Version</span>
                <span className="skill-editor__meta-value">{version || '1.0.0'}</span>
              </div>
              <div className="skill-editor__meta">
                <span className="skill-editor__label">Last Update</span>
                <span className="skill-editor__meta-value">
                  {lastUpdate ? new Date(lastUpdate).toLocaleDateString() : '—'}
                </span>
              </div>
            </>
          )}
        </aside>

        {/* ── Main Content ─────────────────────────────────────────── */}
        <main className="skill-editor__main">
          {error && <div className="skill-editor__error">{error}</div>}

          {/* Tool Selection */}
          <section>
            <div className="skill-editor__tool-header">
              <h2 className="skill-editor__tool-title">Tool Selection</h2>
              <span className="skill-editor__tool-count">
                {totalSelected} tool{totalSelected !== 1 ? 's' : ''} selected
              </span>
            </div>

            {/* Device Tools — MCP server cards */}
            <div className="skill-editor__tool-section">
              <div className="skill-editor__tool-section-header">
                <span className="skill-editor__tool-section-label">
                  <Monitor size={14} />
                  Device Tools
                </span>
                <div className="skill-editor__tool-section-filter">
                  <Input
                    value={deviceFilter}
                    onChange={e => setDeviceFilter(e.target.value)}
                    placeholder="Filter device tools..."
                  />
                </div>
              </div>
              <div className="skill-editor__tool-grid">
                {filteredServers.length === 0 && (
                  <div className="skill-editor__tool-empty">
                    {deviceFilter
                      ? 'No servers match your filter.'
                      : 'No MCP servers connected. Connect servers in Dobby\u2019s Claw.'}
                  </div>
                )}
                {filteredServers.map(server => (
                  <ServerCard
                    key={server.name}
                    server={server}
                    selected={selectedServers.has(server.name)}
                    onToggle={() => handleServerToggle(server.name)}
                  />
                ))}
              </div>
            </div>

            {/* Integration Tools — individual tool cards */}
            <IntegrationToolSection
              tools={filteredIntegrationTools}
              selectedTools={selectedTools}
              onToggle={handleToolToggle}
              filter={integrationFilter}
              onFilterChange={setIntegrationFilter}
            />
          </section>

          {/* System Prompt — fills remaining vertical space */}
          <section className="skill-editor__prompt-section">
            <div className="skill-editor__prompt-header">
              <div>
                <h2 className="skill-editor__prompt-title">System Prompt</h2>
                <p className="skill-editor__prompt-subtitle">
                  Define the core instructions and constraints for this skill.
                </p>
              </div>
              <div className="skill-editor__prompt-actions">
                <button className="skill-editor__prompt-btn" title="Prompt history">
                  <History size={14} />
                  History
                </button>
                <button className="skill-editor__prompt-btn" onClick={handleCopy}>
                  {copied ? <Check size={14} /> : <Copy size={14} />}
                  {copied ? 'Copied' : 'Copy'}
                </button>
              </div>
            </div>

            <div className="skill-editor__prompt-editor-wrap">
              <div className="skill-editor__prompt-dots">
                <span className="skill-editor__prompt-dot skill-editor__prompt-dot--red" />
                <span className="skill-editor__prompt-dot skill-editor__prompt-dot--yellow" />
                <span className="skill-editor__prompt-dot skill-editor__prompt-dot--green" />
              </div>
              <textarea
                className="skill-editor__prompt-textarea"
                value={systemPrompt}
                onChange={e => setSystemPrompt(e.target.value)}
                placeholder="// Write your system prompt here..."
              />
              <div className="skill-editor__prompt-hint">
                <span>
                  Press <kbd>ctrl</kbd> + <kbd>S</kbd> to save
                </span>
              </div>
            </div>
          </section>

        </main>
      </div>
    </div>
  );
};

/* ── MCP Server Card ─────────────────────────────────────────────────────── */

interface ServerCardProps {
  server: MCPServer;
  selected: boolean;
  onToggle: () => void;
}

function ServerCard({ server, selected, onToggle }: ServerCardProps) {
  // Derive a short display name from the full server name
  const displayName = useMemo(() => {
    const n = server.name;
    // Strip common prefixes like @org/server-
    const lastSlash = n.lastIndexOf('/');
    const short = lastSlash >= 0 ? n.slice(lastSlash + 1) : n;
    // Remove "server-" prefix if present
    return short.replace(/^server-/, '');
  }, [server.name]);

  return (
    <div
      className={`skill-editor__tool-card ${selected ? 'skill-editor__tool-card--selected' : ''}`}
      onClick={onToggle}
      role="button"
      tabIndex={0}
      onKeyDown={e => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onToggle();
        }
      }}
    >
      {selected && (
        <span className="skill-editor__tool-check">
          <Check size={12} />
        </span>
      )}
      <div className="skill-editor__tool-card-top">
        <div className="skill-editor__tool-icon">
          <Server size={16} />
        </div>
        <h4 className="skill-editor__tool-name">{displayName}</h4>
      </div>
      <p className="skill-editor__tool-desc">
        {server.toolCount} tool{server.toolCount !== 1 ? 's' : ''} available
      </p>
    </div>
  );
}

/* ── Integration Tool Section ────────────────────────────────────────────── */

const CARD_MIN_WIDTH = 180;
const GRID_GAP = 8;

interface IntegrationToolSectionProps {
  tools: Tool[];
  selectedTools: Set<string>;
  onToggle: (name: string) => void;
  filter: string;
  onFilterChange: (value: string) => void;
}

function IntegrationToolSection({
  tools,
  selectedTools,
  onToggle,
  filter,
  onFilterChange,
}: IntegrationToolSectionProps) {
  const gridRef = useRef<HTMLDivElement>(null);
  const [maxVisible, setMaxVisible] = useState(6);

  // Measure grid width and derive how many cards fit in one row
  useEffect(() => {
    const el = gridRef.current;
    if (!el) return;
    const observer = new ResizeObserver(entries => {
      for (const entry of entries) {
        const width = entry.contentRect.width;
        const cols = Math.max(1, Math.floor((width + GRID_GAP) / (CARD_MIN_WIDTH + GRID_GAP)));
        setMaxVisible(cols);
      }
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  // When not filtering, show selected tools first + fill remaining slots up to one row
  const visibleTools = useMemo(() => {
    if (filter) return tools;
    const selected = tools.filter(t => selectedTools.has(t.name));
    const unselected = tools.filter(t => !selectedTools.has(t.name));
    const remaining = maxVisible - selected.length;
    return [...selected, ...unselected.slice(0, Math.max(0, remaining))];
  }, [tools, selectedTools, filter, maxVisible]);

  const hiddenCount = filter ? 0 : Math.max(0, tools.length - visibleTools.length);

  return (
    <div className="skill-editor__tool-section">
      <div className="skill-editor__tool-section-header">
        <span className="skill-editor__tool-section-label">
          <Plug size={14} />
          Integration Tools
        </span>
        <div className="skill-editor__tool-section-filter">
          <Input
            value={filter}
            onChange={e => onFilterChange(e.target.value)}
            placeholder="Filter integrations..."
          />
        </div>
      </div>
      <div className="skill-editor__tool-grid" ref={gridRef}>
        {visibleTools.length === 0 && (
          <div className="skill-editor__tool-empty">
            {filter ? 'No tools match your filter.' : 'No integration tools available.'}
          </div>
        )}
        {visibleTools.map(tool => (
          <ToolCard
            key={tool.name}
            tool={tool}
            selected={selectedTools.has(tool.name)}
            onToggle={() => onToggle(tool.name)}
          />
        ))}
      </div>
      {hiddenCount > 0 && (
        <p className="skill-editor__tool-more">
          +{hiddenCount} more — use search to find specific tools
        </p>
      )}
    </div>
  );
}

/* ── Tool Card sub-component ─────────────────────────────────────────────── */

interface ToolCardProps {
  tool: Tool;
  selected: boolean;
  onToggle: () => void;
}

function ToolCard({ tool, selected, onToggle }: ToolCardProps) {
  return (
    <div
      className={`skill-editor__tool-card ${selected ? 'skill-editor__tool-card--selected' : ''}`}
      onClick={onToggle}
      role="button"
      tabIndex={0}
      onKeyDown={e => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onToggle();
        }
      }}
    >
      {selected && (
        <span className="skill-editor__tool-check">
          <Check size={12} />
        </span>
      )}
      <div className="skill-editor__tool-card-top">
        <div className="skill-editor__tool-icon">
          <Wrench size={16} />
        </div>
        <h4 className="skill-editor__tool-name">{tool.display_name}</h4>
      </div>
      <p className="skill-editor__tool-desc">{tool.description}</p>
    </div>
  );
}
