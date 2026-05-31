import { Link, useNavigate } from 'react-router-dom';
import { LogOut, User } from 'lucide-react';
import { useAuthStore } from '@/store/useAuthStore';
import { Button } from '@/components/design-system';

export const Header = () => {
  const { user, signOut } = useAuthStore();
  const navigate = useNavigate();

  const handleSignOut = async () => {
    await signOut();
    navigate('/signin');
  };

  return (
    <header
      style={{
        background: 'var(--color-surface)',
        borderBottom: '1px solid var(--color-border)',
        backdropFilter: 'var(--backdrop-nav)',
      }}
    >
      <nav className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
        <div className="flex h-16 justify-between items-center">
          <div className="flex items-center">
            <Link
              to="/"
              style={{
                fontSize: 'var(--text-xl)',
                fontWeight: 'var(--font-weight-bold)',
                color: 'var(--color-text-primary)',
                textDecoration: 'none',
              }}
            >
              ClaraVerse
            </Link>
          </div>
          <div className="flex gap-4 items-center">
            <Link
              to="/"
              style={{
                color: 'var(--color-text-secondary)',
                textDecoration: 'none',
                transition: 'color var(--transition-fast)',
              }}
              onMouseOver={e => (e.currentTarget.style.color = 'var(--color-accent)')}
              onMouseOut={e => (e.currentTarget.style.color = 'var(--color-text-secondary)')}
            >
              Home
            </Link>
            <Link
              to="/design-system"
              style={{
                color: 'var(--color-text-secondary)',
                textDecoration: 'none',
                transition: 'color var(--transition-fast)',
              }}
              onMouseOver={e => (e.currentTarget.style.color = 'var(--color-accent)')}
              onMouseOut={e => (e.currentTarget.style.color = 'var(--color-text-secondary)')}
            >
              Design System
            </Link>
            {user && (
              <div className="flex items-center gap-3">
                <div className="flex items-center gap-2 text-sm text-muted-foreground">
                  <User size={16} />
                  <span>{user.email}</span>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={handleSignOut}
                  style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}
                >
                  <LogOut size={16} />
                  Sign Out
                </Button>
              </div>
            )}
          </div>
        </div>
      </nav>
    </header>
  );
};
