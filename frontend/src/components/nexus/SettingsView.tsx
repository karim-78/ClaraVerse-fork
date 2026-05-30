import { memo, useState, useMemo, useCallback } from 'react';
import {
  Brain,
  Sparkles,
  Database,
  ChevronDown,
  ChevronRight,
  Plus,
  X,
  Zap,
  Bot,
  FileText,
  User,
  Clock,
  Settings,
} from 'lucide-react';
import { useNexusStore } from '@/store/useNexusStore';
import { Badge } from '@/components/design-system';
import type { NexusClientMessage, PersonaFact } from '@/types/nexus';
import styles from './Nexus.module.css';

interface SettingsViewProps {
  send: (msg: NexusClientMessage) => void;
}

const categoryColors: Record<string, 'accent' | 'success' | 'warning' | 'info'> = {
  personality: 'accent',
  communication: 'info',
  expertise: 'success',
  boundaries: 'warning',
};

const memoryCategoryColors: Record<string, 'accent' | 'success' | 'warning' | 'info'> = {
  user_fact: 'success',
  status_log: 'info',
  daemon_output: 'accent',
  task_result: 'warning',
};

const engramTypeIcons: Record<string, React.ReactNode> = {
  task_result: <FileText size={12} />,
  daemon_output: <Bot size={12} />,
  user_fact: <User size={12} />,
  status_log: <Clock size={12} />,
};

const engramTypeVariants: Record<string, 'default' | 'accent' | 'success' | 'info'> = {
  task_result: 'success',
  daemon_output: 'accent',
  user_fact: 'info',
  status_log: 'default',
};

export const SettingsView = memo(function SettingsView({ send }: SettingsViewProps) {
  const persona = useNexusStore((s) => s.persona);
  const engrams = useNexusStore((s) => s.engrams);
  const brainMemories = useNexusStore((s) => s.brainMemories);
  const session = useNexusStore((s) => s.session);
  const bridgeConnected = useNexusStore((s) => s.bridgeConnected);

  const [memoriesOpen, setMemoriesOpen] = useState(false);
  const [skillsOpen, setSkillsOpen] = useState(false);
  const [activityOpen, setActivityOpen] = useState(false);
  const [adding, setAdding] = useState(false);
  const [newContent, setNewContent] = useState('');
  const [newCategory, setNewCategory] = useState('personality');

  const cortexMemories = useMemo(
    () => brainMemories.filter((e) => e.source === 'tui_sync'),
    [brainMemories]
  );
  const cortexSkills = useMemo(
    () => brainMemories.filter((e) => e.source === 'tui_skill'),
    [brainMemories]
  );

  const personaGrouped = useMemo(
    () =>
      persona.reduce<Record<string, PersonaFact[]>>((acc, f) => {
        (acc[f.category] ??= []).push(f);
        return acc;
      }, {}),
    [persona]
  );

  const handleAddFact = useCallback(() => {
    if (!newContent.trim()) return;
    send({
      type: 'update_persona',
      facts: [{ category: newCategory, content: newContent.trim(), action: 'create' }],
    });
    setNewContent('');
    setAdding(false);
  }, [newContent, newCategory, send]);

  const handleDeleteFact = useCallback(
    (fact: PersonaFact) => {
      send({
        type: 'update_persona',
        facts: [{ id: fact.id, category: fact.category, content: fact.content, action: 'delete' }],
      });
    },
    [send]
  );

  return (
    <div className={styles.settingsView}>
      <div className={styles.settingsHeader}>
        <Settings size={20} />
        <h2 className={styles.settingsTitle}>Settings</h2>
      </div>

      {/* Cortex Brain Section */}
      <div className={styles.settingsSection}>
        <div className={styles.settingsSectionTitle}>
          <Brain size={16} />
          Cortex Brain
          <Badge variant={bridgeConnected ? 'success' : 'default'}>
            {bridgeConnected ? 'Synced' : 'Offline'}
          </Badge>
        </div>

        <div className={styles.cortexGrid}>
          <div className={styles.cortexStatCard}>
            <span className={styles.cortexStatValue}>{cortexMemories.length}</span>
            <span className={styles.cortexStatLabel}>Memories</span>
          </div>
          <div className={styles.cortexStatCard}>
            <span className={styles.cortexStatValue}>{cortexSkills.length}</span>
            <span className={styles.cortexStatLabel}>Skills</span>
          </div>
          <div className={styles.cortexStatCard}>
            <span className={styles.cortexStatValue}>{engrams.length}</span>
            <span className={styles.cortexStatLabel}>Activity</span>
          </div>
          <div className={styles.cortexStatCard}>
            <span className={styles.cortexStatValue}>{session?.total_tasks ?? 0}</span>
            <span className={styles.cortexStatLabel}>Tasks</span>
          </div>
        </div>

        {/* Memories expandable */}
        <div className={styles.settingsAccordion}>
          <button
            className={styles.settingsAccordionBtn}
            onClick={() => setMemoriesOpen(!memoriesOpen)}
          >
            {memoriesOpen ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
            <Zap size={13} />
            Memories ({cortexMemories.length})
          </button>
          {memoriesOpen && (
            <div className={styles.settingsAccordionContent}>
              {cortexMemories.length === 0 ? (
                <div className={styles.settingsEmpty}>
                  {bridgeConnected ? 'No memories synced yet' : 'Connect desktop agent to sync'}
                </div>
              ) : (
                cortexMemories.slice(0, 20).map((m) => (
                  <div key={m.id} className={styles.settingsMemoryItem}>
                    <Badge variant={memoryCategoryColors[m.type] ?? 'default'}>
                      {m.type.replace('_', ' ')}
                    </Badge>
                    <span className={styles.settingsMemoryContent}>{m.summary || m.value}</span>
                  </div>
                ))
              )}
            </div>
          )}
        </div>

        {/* Skills expandable */}
        <div className={styles.settingsAccordion}>
          <button
            className={styles.settingsAccordionBtn}
            onClick={() => setSkillsOpen(!skillsOpen)}
          >
            {skillsOpen ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
            <Sparkles size={13} />
            Skills ({cortexSkills.length})
          </button>
          {skillsOpen && (
            <div className={styles.settingsAccordionContent}>
              {cortexSkills.length === 0 ? (
                <div className={styles.settingsEmpty}>No skills promoted yet</div>
              ) : (
                cortexSkills.map((s) => (
                  <div key={s.id} className={styles.settingsMemoryItem}>
                    <Badge variant="accent">{s.key.replace('tui_skill_', '')}</Badge>
                    <span className={styles.settingsMemoryContent}>
                      {s.summary || s.type.replace('_', ' ')}
                    </span>
                  </div>
                ))
              )}
            </div>
          )}
        </div>

        {/* Activity expandable */}
        <div className={styles.settingsAccordion}>
          <button
            className={styles.settingsAccordionBtn}
            onClick={() => setActivityOpen(!activityOpen)}
          >
            {activityOpen ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
            <Database size={13} />
            Recent Activity ({engrams.length})
          </button>
          {activityOpen && (
            <div className={styles.settingsAccordionContent}>
              {engrams.length === 0 ? (
                <div className={styles.settingsEmpty}>No recent activity</div>
              ) : (
                engrams.slice(0, 15).map((e) => (
                  <div key={e.id} className={styles.settingsMemoryItem}>
                    <Badge
                      variant={engramTypeVariants[e.type] ?? 'default'}
                      icon={engramTypeIcons[e.type]}
                    >
                      {e.source.replace(/^daemon_/, '')}
                    </Badge>
                    <span className={styles.settingsMemoryContent}>{e.summary || e.value}</span>
                  </div>
                ))
              )}
            </div>
          )}
        </div>
      </div>

      {/* Persona Section */}
      <div className={styles.settingsSection}>
        <div className={styles.settingsSectionTitle}>
          <Sparkles size={16} />
          Persona
          <button
            className={styles.settingsAddBtn}
            onClick={() => setAdding(!adding)}
            title="Add fact"
          >
            <Plus size={14} />
          </button>
        </div>

        <p className={styles.settingsDescription}>
          Facts about your preferences and how Dobby should interact with you.
        </p>

        {adding && (
          <div className={styles.settingsPersonaForm}>
            <select
              className={styles.settingsSelect}
              value={newCategory}
              onChange={(e) => setNewCategory(e.target.value)}
            >
              <option value="personality">Personality</option>
              <option value="communication">Communication</option>
              <option value="expertise">Expertise</option>
              <option value="boundaries">Boundaries</option>
            </select>
            <input
              className={styles.settingsInput}
              value={newContent}
              onChange={(e) => setNewContent(e.target.value)}
              placeholder="e.g. Dobby is concise and direct"
              onKeyDown={(e) => e.key === 'Enter' && handleAddFact()}
            />
            <div className={styles.settingsFormActions}>
              <button className={styles.settingsFormBtn} onClick={handleAddFact}>
                Add
              </button>
              <button
                className={styles.settingsFormBtnCancel}
                onClick={() => setAdding(false)}
              >
                Cancel
              </button>
            </div>
          </div>
        )}

        {Object.entries(personaGrouped).map(([cat, facts]) => (
          <div key={cat} className={styles.settingsPersonaGroup}>
            <Badge variant={categoryColors[cat] ?? 'default'}>{cat}</Badge>
            {facts.map((f) => (
              <div key={f.id} className={styles.settingsPersonaFact}>
                <span>{f.content}</span>
                {f.source === 'user_explicit' && (
                  <button
                    className={styles.settingsPersonaDelete}
                    onClick={() => handleDeleteFact(f)}
                    title="Remove"
                  >
                    <X size={12} />
                  </button>
                )}
              </div>
            ))}
          </div>
        ))}

        {persona.length === 0 && !adding && (
          <div className={styles.settingsEmpty}>No persona facts yet</div>
        )}
      </div>

      {/* Engram Section */}
      <div className={styles.settingsSection}>
        <div className={styles.settingsSectionTitle}>
          <Database size={16} />
          Engram
        </div>

        <p className={styles.settingsDescription}>
          Recent memory entries from daemon activity and task results.
        </p>

        {engrams.length === 0 ? (
          <div className={styles.settingsEmpty}>No engram entries yet</div>
        ) : (
          engrams.slice(0, 15).map((e) => (
            <div key={e.id} className={styles.settingsEngramEntry}>
              <div className={styles.settingsEngramHeader}>
                <Badge
                  variant={engramTypeVariants[e.type] ?? 'default'}
                  icon={engramTypeIcons[e.type]}
                >
                  {e.type.replace('_', ' ')}
                </Badge>
                <span className={styles.settingsEngramSource}>{e.source}</span>
              </div>
              {e.summary && <p className={styles.settingsEngramSummary}>{e.summary}</p>}
            </div>
          ))
        )}
      </div>
    </div>
  );
});
