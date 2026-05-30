import { Outlet, NavLink, useNavigate } from 'react-router-dom';
import { useState } from 'react';
import { useAuthStore } from '@/store/useAuthStore';
import faviconIcon from '@/assets/favicon-32x32.png';
import {
  LayoutDashboard,
  Plug,
  BarChart3,
  Box,
  Users,
  Settings,
  Code,
  ChevronLeft,
  ChevronRight,
  LogOut,
  ArrowLeft,
  Activity,
} from 'lucide-react';

export const AdminLayout = () => {
  const [isSidebarOpen, setIsSidebarOpen] = useState(true);
  const { user, signOut } = useAuthStore();
  const navigate = useNavigate();

  const handleSignOut = async () => {
    await signOut();
    navigate('/signin');
  };

  const navItems = [
    { path: '/admin/dashboard', label: 'Dashboard', icon: LayoutDashboard },
    { path: '/admin/providers', label: 'Providers', icon: Plug },
    { path: '/admin/analytics', label: 'Analytics', icon: BarChart3 },
    { path: '/admin/models', label: 'Models', icon: Box },
    { path: '/admin/system-models', label: 'System Models', icon: Settings },
    { path: '/admin/code-execution', label: 'Code Execution', icon: Code },
    { path: '/admin/users', label: 'Users', icon: Users },
    { path: '/admin/traces', label: 'Traces', icon: Activity },
  ];

  return (
    <div className="flex h-screen bg-[var(--color-background)]">
      {/* Sidebar */}
      <aside
        className={`${
          isSidebarOpen ? 'w-64' : 'w-20'
        } bg-[var(--color-surface)] transition-all duration-300 flex flex-col`}
        style={{ backdropFilter: 'blur(20px)' }}
      >
        {/* Sidebar Header */}
        <div className="h-16 flex items-center justify-between px-4">
          {isSidebarOpen ? (
            <div className="flex items-center gap-3">
              <img src={faviconIcon} alt="Dobby" className="w-8 h-8" />
              <h1 className="text-xl font-semibold bg-gradient-to-r from-[var(--color-accent)] to-[var(--color-accent-hover)] bg-clip-text text-transparent">
                Admin
              </h1>
            </div>
          ) : (
            <div className="flex items-center justify-center w-full">
              <img src={faviconIcon} alt="Dobby" className="w-8 h-8" />
            </div>
          )}
          {isSidebarOpen && (
            <button
              onClick={() => setIsSidebarOpen(false)}
              className="p-1.5 rounded-lg hover:bg-[var(--color-surface-hover)] text-[var(--color-text-tertiary)] transition-colors"
              aria-label="Collapse sidebar"
            >
              <ChevronLeft size={18} />
            </button>
          )}
        </div>

        {/* Navigation */}
        <nav className="flex-1 py-4 space-y-1 px-3 overflow-y-auto">
          {navItems.map(item => {
            const Icon = item.icon;
            return (
              <NavLink
                key={item.path}
                to={item.path}
                className={({ isActive }) =>
                  `group flex items-center gap-3 ${isSidebarOpen ? 'px-3' : 'justify-center px-3'} py-2.5 rounded-lg text-sm font-medium transition-all duration-200 ${
                    isActive
                      ? 'bg-[var(--color-accent-light)] text-[var(--color-accent)]'
                      : 'text-[var(--color-text-secondary)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text-primary)]'
                  }`
                }
                title={!isSidebarOpen ? item.label : undefined}
              >
                <Icon size={20} strokeWidth={2} />
                {isSidebarOpen && <span>{item.label}</span>}
              </NavLink>
            );
          })}

          {/* Expand button when collapsed */}
          {!isSidebarOpen && (
            <button
              onClick={() => setIsSidebarOpen(true)}
              className="w-full flex items-center justify-center px-3 py-2.5 rounded-lg text-sm font-medium text-[var(--color-text-tertiary)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text-primary)] transition-all duration-200 mt-2"
              aria-label="Expand sidebar"
              title="Expand sidebar"
            >
              <ChevronRight size={20} strokeWidth={2} />
            </button>
          )}
        </nav>

        {/* Sidebar Footer */}
        <div className="p-4">
          {isSidebarOpen ? (
            <div className="space-y-3">
              <div className="text-sm">
                <p className="font-medium text-[var(--color-text-primary)] truncate">
                  {user?.email}
                </p>
                <p className="text-xs text-[var(--color-text-tertiary)] mt-0.5">Administrator</p>
              </div>
              <button
                onClick={handleSignOut}
                className="w-full flex items-center justify-center gap-2 px-3 py-2 rounded-lg bg-[var(--color-surface-hover)] hover:bg-[var(--color-surface-elevated)] text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] transition-all duration-200 text-sm font-medium"
              >
                <LogOut size={16} />
                <span>Sign Out</span>
              </button>
            </div>
          ) : (
            <button
              onClick={handleSignOut}
              className="p-2 rounded-lg hover:bg-[var(--color-surface-hover)] text-[var(--color-text-tertiary)] w-full flex items-center justify-center transition-colors"
              aria-label="Sign out"
              title="Sign out"
            >
              <LogOut size={20} />
            </button>
          )}
        </div>
      </aside>

      {/* Main Content */}
      <div className="flex-1 flex flex-col overflow-hidden">
        {/* Top Header */}
        <header className="h-16 bg-[var(--color-surface)] flex items-center justify-between px-6">
          <div className="flex items-center gap-3">
            <div className="px-3 py-1 bg-[var(--color-accent-light)] text-[var(--color-accent)] rounded-full text-xs font-semibold uppercase tracking-wide">
              Admin
            </div>
          </div>

          <button
            onClick={() => navigate('/')}
            className="flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium text-[var(--color-text-secondary)] hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-hover)] transition-all duration-200"
          >
            <ArrowLeft size={16} />
            <span>Back to App</span>
          </button>
        </header>

        {/* Page Content */}
        <main className="flex-1 overflow-y-auto p-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
};
