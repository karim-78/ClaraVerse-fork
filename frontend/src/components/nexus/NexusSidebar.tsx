import { useMemo, useCallback } from 'react';
import {
  Bot,
  CalendarClock,
  Settings,
  Home,
  MessageSquare,
  Plus,
  Folder,
  Globe,
  Code,
  Terminal,
  Cpu,
  Database,
  Layers,
  Bug,
  Cloud,
  Shield,
  Zap,
  Wrench,
  Hammer,
  FlaskConical,
  Rocket,
  Lightbulb,
  Target,
  Compass,
  BookOpen,
  GraduationCap,
  FileText,
  Briefcase,
  Wallet,
  ShoppingCart,
  Truck,
  Megaphone,
  Users,
  MessageCircle,
  Palette,
  Camera,
  Film,
  Music,
  Headphones,
  Gamepad2,
  Map as MapIcon,
  Heart,
  Star,
  Crown,
  Gift,
  Puzzle,
  Bookmark,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { Sidebar } from '@/components/ui/Sidebar';
import type { NavItem, FooterLink } from '@/components/ui/Sidebar';
import { useNexusStore } from '@/store/useNexusStore';
import styles from './Nexus.module.css';

const NEXUS_FOOTER_LINKS: FooterLink[] = [
  { href: '/', label: 'Home', icon: Home, ariaLabel: 'Navigate to home' },
  { href: '/chat', label: 'Chat', icon: MessageSquare, ariaLabel: 'Navigate to chat' },
];

const ICON_MAP: Record<string, LucideIcon> = {
  folder: Folder,
  globe: Globe,
  code: Code,
  terminal: Terminal,
  cpu: Cpu,
  database: Database,
  layers: Layers,
  bug: Bug,
  cloud: Cloud,
  shield: Shield,
  zap: Zap,
  wrench: Wrench,
  hammer: Hammer,
  'flask-conical': FlaskConical,
  rocket: Rocket,
  lightbulb: Lightbulb,
  target: Target,
  compass: Compass,
  'book-open': BookOpen,
  'graduation-cap': GraduationCap,
  'file-text': FileText,
  briefcase: Briefcase,
  wallet: Wallet,
  'shopping-cart': ShoppingCart,
  truck: Truck,
  megaphone: Megaphone,
  users: Users,
  'message-circle': MessageCircle,
  palette: Palette,
  camera: Camera,
  film: Film,
  music: Music,
  headphones: Headphones,
  'gamepad-2': Gamepad2,
  home: Home,
  map: MapIcon,
  heart: Heart,
  star: Star,
  crown: Crown,
  gift: Gift,
  puzzle: Puzzle,
};

/** Cache of stable icon components keyed by "iconName:color" */
const iconCache = new Map<string, React.FC<{ size?: number; strokeWidth?: number }>>();

/** Creates a stable colored Lucide icon component for a project (cached) */
function makeProjectIcon(iconName: string, color: string) {
  const key = `${iconName}:${color}`;
  const cached = iconCache.get(key);
  if (cached) return cached;
  const Icon = ICON_MAP[iconName] || Folder;
  const ProjectIcon = ({ size = 18 }: { size?: number; strokeWidth?: number }) => (
    <Icon size={size} color={color} />
  );
  ProjectIcon.displayName = `ProjectIcon(${iconName})`;
  iconCache.set(key, ProjectIcon);
  return ProjectIcon;
}

interface NexusSidebarProps {
  isOpen?: boolean;
  onOpenChange?: (open: boolean) => void;
  onNewProject?: () => void;
  onSelectProject?: (id: string) => void;
}

export function NexusSidebar({
  isOpen,
  onOpenChange,
  onNewProject,
  onSelectProject,
}: NexusSidebarProps) {
  const activeView = useNexusStore(s => s.activeView);
  const setActiveView = useNexusStore(s => s.setActiveView);
  const projects = useNexusStore(s => s.projects);
  const tasks = useNexusStore(s => s.tasks);
  const activeProjectId = useNexusStore(s => s.activeProjectId);
  const setActiveProjectId = useNexusStore(s => s.setActiveProjectId);
  const bridgeConnected = useNexusStore(s => s.bridgeConnected);

  const handleProjectClick = useCallback(
    (id: string) => {
      if (onSelectProject) {
        onSelectProject(id);
      } else {
        setActiveProjectId(id);
      }
    },
    [onSelectProject, setActiveProjectId]
  );

  const navItems: NavItem[] = useMemo(() => {
    const items: NavItem[] = [];

    // Projects as primary nav
    for (const project of projects) {
      const pt = tasks.filter(t => t.project_id === project.id);
      const executing = pt.filter(
        t => t.status === 'executing' || t.status === 'waiting_input'
      ).length;
      const pending = pt.filter(t => t.status === 'pending').length;
      const failed = pt.filter(t => t.status === 'failed').length;
      const activeCount = executing + pending;

      let badgeVariant: 'default' | 'success' | 'warning' | 'error' = 'default';
      if (failed > 0) badgeVariant = 'error';
      else if (executing > 0) badgeVariant = 'warning';
      else if (activeCount === 0 && pt.length > 0) badgeVariant = 'success';

      items.push({
        id: `project-${project.id}`,
        label: project.name,
        icon: makeProjectIcon(project.icon || 'folder', project.color || '#2196F3'),
        isActive: activeView === 'project' && activeProjectId === project.id,
        onClick: () => handleProjectClick(project.id),
        badge: activeCount > 0 ? activeCount : undefined,
        badgeVariant,
      });
    }

    if (onNewProject) {
      items.push({
        id: 'add-project',
        label: 'New Project',
        icon: Plus,
        onClick: onNewProject,
      });
    }

    // Utilities
    items.push(
      {
        id: 'saves',
        label: 'Saved',
        icon: Bookmark,
        isActive: activeView === 'saves',
        onClick: () => setActiveView('saves'),
      },
      {
        id: 'daemons',
        label: 'Daemons',
        icon: Bot,
        isActive: activeView === 'daemons',
        onClick: () => setActiveView('daemons'),
      },
      {
        id: 'routines',
        label: 'Routines',
        icon: CalendarClock,
        isActive: activeView === 'routines',
        onClick: () => setActiveView('routines'),
      },
      {
        id: 'settings',
        label: 'Settings',
        icon: Settings,
        isActive: activeView === 'settings',
        onClick: () => setActiveView('settings'),
      }
    );

    return items;
  }, [
    activeView,
    setActiveView,
    projects,
    tasks,
    activeProjectId,
    handleProjectClick,
    onNewProject,
  ]);

  const isCollapsed = !isOpen;

  const bridgeStatusSlot = useMemo(
    () =>
      isCollapsed ? (
        <div
          className={styles.bridgeStatusCompact}
          title={bridgeConnected ? 'Dobby Companion: Connected' : 'Dobby Companion: Offline'}
        >
          <Terminal
            size={14}
            className={`${styles.bridgeStatusIcon} ${
              bridgeConnected
                ? styles.bridgeStatusIconConnected
                : styles.bridgeStatusIconDisconnected
            }`}
          />
        </div>
      ) : (
        <div className={styles.bridgeStatus}>
          <div className={styles.bridgeStatusRow}>
            <Terminal
              size={14}
              className={`${styles.bridgeStatusIcon} ${
                bridgeConnected
                  ? styles.bridgeStatusIconConnected
                  : styles.bridgeStatusIconDisconnected
              }`}
            />
            <span className={styles.bridgeStatusLabel}>Dobby Companion</span>
            <span className={styles.bridgeStatusValue}>
              {bridgeConnected ? 'Connected' : 'Offline'}
            </span>
          </div>
          {!bridgeConnected && (
            <div className={styles.bridgeSetupHint}>
              Install &amp; run the companion:
              <code className={styles.bridgeSetupCode}>
                curl -fsSL
                https://gist.githubusercontent.com/claraverse-space/87a840d4a462c2787ce958691fa267b4/raw/install.sh
                | bash
              </code>
            </div>
          )}
        </div>
      ),
    [bridgeConnected, isCollapsed]
  );

  return (
    <Sidebar
      brandName="Nexus"
      navItems={navItems}
      recentChats={[]}
      footerLinks={NEXUS_FOOTER_LINKS}
      isOpen={isOpen}
      onOpenChange={onOpenChange}
      statusSlot={bridgeStatusSlot}
    />
  );
}
