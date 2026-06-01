import { useEffect } from 'react';
import { useAdminStore } from '@/store/useAdminStore';
import { Users, MessageSquare, Activity, Zap, Server, Box } from 'lucide-react';
import { UpdateBanner } from './UpdateBanner';

export const Dashboard = () => {
  const { overviewStats, isLoadingStats, fetchOverviewStats } = useAdminStore();

  useEffect(() => {
    fetchOverviewStats();
  }, [fetchOverviewStats]);

  if (isLoadingStats) {
    return (
      <div className="space-y-6">
        <h1 className="text-3xl font-bold text-[var(--color-text-primary)]">Admin Dashboard</h1>
        <p className="text-[var(--color-text-secondary)]">Loading overview...</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold text-[var(--color-text-primary)]">Admin Dashboard</h1>
        <p className="text-[var(--color-text-secondary)] mt-2">
          Overview of system statistics and analytics
        </p>
      </div>

      {/* Auto-update banner — renders only when an update is available
          (or while applying / on error). Self-contained polling +
          apply flow; no parent wiring needed. */}
      <UpdateBanner />

      {/* Stats Cards */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        <StatsCard
          title="Total Users"
          value={overviewStats?.total_users ?? 0}
          icon={<Users size={24} />}
          iconColor="text-[var(--color-accent)]"
        />
        <StatsCard
          title="Active Chats"
          value={overviewStats?.active_chats ?? 0}
          icon={<MessageSquare size={24} />}
          iconColor="text-[var(--color-success)]"
        />
        <StatsCard
          title="Total Messages"
          value={overviewStats?.total_messages ?? 0}
          icon={<Activity size={24} />}
          iconColor="text-[var(--color-info)]"
        />
        <StatsCard
          title="API Calls Today"
          value={overviewStats?.api_calls_today ?? 0}
          icon={<Zap size={24} />}
          iconColor="text-[var(--color-warning)]"
        />
        <StatsCard
          title="Active Providers"
          value={overviewStats?.active_providers ?? 0}
          icon={<Server size={24} />}
          iconColor="text-[var(--color-accent)]"
        />
        <StatsCard
          title="Total Models"
          value={overviewStats?.total_models ?? 0}
          icon={<Box size={24} />}
          iconColor="text-[var(--color-info)]"
        />
      </div>

      {/* Quick Links */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <div
          className="bg-[var(--color-surface)] rounded-lg p-6"
          style={{ backdropFilter: 'blur(20px)' }}
        >
          <h2 className="text-lg font-semibold text-[var(--color-text-primary)] mb-4">
            Quick Actions
          </h2>
          <div className="space-y-3">
            <a
              href="/admin/providers"
              className="block p-3 rounded-lg bg-[var(--color-surface-hover)] hover:bg-[var(--color-accent-light)] transition-colors"
            >
              <p className="text-sm font-medium text-[var(--color-text-primary)]">
                Manage Providers
              </p>
              <p className="text-xs text-[var(--color-text-tertiary)] mt-1">
                View and configure AI providers
              </p>
            </a>
            <a
              href="/admin/analytics"
              className="block p-3 rounded-lg bg-[var(--color-surface-hover)] hover:bg-[var(--color-accent-light)] transition-colors"
            >
              <p className="text-sm font-medium text-[var(--color-text-primary)]">View Analytics</p>
              <p className="text-xs text-[var(--color-text-tertiary)] mt-1">
                Detailed insights and metrics
              </p>
            </a>
            <a
              href="/admin/models"
              className="block p-3 rounded-lg bg-[var(--color-surface-hover)] hover:bg-[var(--color-accent-light)] transition-colors"
            >
              <p className="text-sm font-medium text-[var(--color-text-primary)]">
                Model Management
              </p>
              <p className="text-xs text-[var(--color-text-tertiary)] mt-1">
                Configure model settings
              </p>
            </a>
          </div>
        </div>

        <div
          className="bg-[var(--color-surface)] rounded-lg p-6"
          style={{ backdropFilter: 'blur(20px)' }}
        >
          <h2 className="text-lg font-semibold text-[var(--color-text-primary)] mb-4">
            System Status
          </h2>
          <div className="space-y-4">
            <div className="flex items-center justify-between">
              <span className="text-sm text-[var(--color-text-secondary)]">Providers Active</span>
              <span className="text-sm font-bold text-[var(--color-success)]">
                {overviewStats?.active_providers ?? 0}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-sm text-[var(--color-text-secondary)]">Models Available</span>
              <span className="text-sm font-bold text-[var(--color-text-primary)]">
                {overviewStats?.total_models ?? 0}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-sm text-[var(--color-text-secondary)]">Users Registered</span>
              <span className="text-sm font-bold text-[var(--color-text-primary)]">
                {overviewStats?.total_users ?? 0}
              </span>
            </div>
            <div className="pt-4" style={{ borderTop: '1px solid var(--color-surface-hover)' }}>
              <div className="flex items-center gap-2">
                <div className="w-2 h-2 rounded-full bg-[var(--color-success)] animate-pulse" />
                <span className="text-xs text-[var(--color-text-secondary)]">
                  All systems operational
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};

interface StatsCardProps {
  title: string;
  value: number;
  icon: React.ReactNode;
  iconColor: string;
}

const StatsCard = ({ title, value, icon, iconColor }: StatsCardProps) => {
  return (
    <div
      className="bg-[var(--color-surface)] rounded-lg p-6"
      style={{ backdropFilter: 'blur(20px)' }}
    >
      <div className="flex items-center justify-between">
        <div>
          <p className="text-sm font-medium text-[var(--color-text-tertiary)]">{title}</p>
          <p className="text-3xl font-bold mt-2 text-[var(--color-text-primary)]">
            {value.toLocaleString()}
          </p>
        </div>
        <div className={iconColor}>{icon}</div>
      </div>
    </div>
  );
};
