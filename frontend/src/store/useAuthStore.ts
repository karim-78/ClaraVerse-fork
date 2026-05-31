import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { User, Session } from '@/services/authService';
import { authService } from '@/services/authService';
import { migrateLegacyData, clearUserData } from '@/services/dataBackupService';

interface AuthState {
  user: User | null;
  session: Session | null;
  accessToken: string | null;
  refreshToken: string | null;
  isLoading: boolean;
  isAuthenticated: boolean;
  isAdmin: boolean;
  sessionExpiredReason: string | null;
  setUser: (user: User | null) => void;
  setSession: (session: Session | null) => void;
  setLoading: (isLoading: boolean) => void;
  initialize: () => Promise<void>;
  signOut: () => Promise<void>;
  forceLogout: (reason?: string) => void;
  clearSessionExpiredReason: () => void;
  getAccessToken: () => string | null;
  checkSession: () => Promise<boolean>;
  refreshSession: () => Promise<void>;
  checkAdminStatus: () => Promise<void>;
}

// Flags to prevent infinite loops and multiple initializations
let isInitializing = false;
let hasInitialized = false;
let refreshIntervalId: ReturnType<typeof setInterval> | null = null;
let authStateUnsubscribe: (() => void) | null = null;

export const useAuthStore = create<AuthState>()(
  persist(
    (set, get) => ({
      user: null,
      session: null,
      accessToken: null,
      refreshToken: null,
      isLoading: true,
      isAuthenticated: false,
      isAdmin: false,
      sessionExpiredReason: null,

      setUser: user =>
        set({
          user,
          isAuthenticated: !!user,
        }),

      setSession: session =>
        set({
          session,
          accessToken: session?.access_token ?? null,
          refreshToken: session?.refresh_token ?? null,
          user: session?.user ?? null,
          isAuthenticated: !!session?.user,
        }),

      setLoading: isLoading => set({ isLoading }),

      initialize: async () => {
        // Prevent multiple simultaneous or repeated initializations
        if (isInitializing) {
          console.log('🔄 Auth initialization already in progress, skipping...');
          return;
        }

        if (hasInitialized) {
          console.log('✅ Auth already initialized, skipping...');
          set({ isLoading: false });
          return;
        }

        isInitializing = true;

        try {
          set({ isLoading: true });

          // Get current session with tokens
          const { session } = await authService.getSession();

          if (session) {
            const userId = session.user.id;

            // Check if session is valid
            const isValid = await authService.isSessionValid();

            if (!isValid) {
              // Try to refresh the session
              const { session: refreshedSession } = await authService.refreshSession();

              if (refreshedSession) {
                set({
                  session: refreshedSession,
                  user: refreshedSession.user,
                  accessToken: refreshedSession.access_token,
                  refreshToken: refreshedSession.refresh_token,
                  isAuthenticated: true,
                });

                // Check admin status before setting isLoading to false
                await get().checkAdminStatus();

                set({ isLoading: false });
              } else {
                set({
                  user: null,
                  session: null,
                  accessToken: null,
                  refreshToken: null,
                  isAuthenticated: false,
                  isLoading: false,
                });
                return;
              }
            } else {
              set({
                session,
                user: session.user,
                accessToken: session.access_token,
                refreshToken: session.refresh_token,
                isAuthenticated: true,
              });

              // Check admin status after successful authentication (BEFORE setting isLoading to false)
              await get().checkAdminStatus();

              // Now set loading to false after admin check completes
              set({ isLoading: false });
            }

            // Migrate legacy data (one-time migration on first auth)
            migrateLegacyData(userId);

            // Note: With user-specific IndexedDB and cloud sync (MongoDB),
            // local backup/restore is no longer needed. Each user's data is:
            // 1. Isolated in their own IndexedDB database (claraverse-chats-{userId})
            // 2. Synced to cloud (MongoDB) if cloud mode is enabled

            // Set up automatic session refresh (every 30 minutes) - only once
            if (!refreshIntervalId) {
              refreshIntervalId = setInterval(
                () => {
                  get().checkSession();
                },
                30 * 60 * 1000
              );
            }
          } else {
            set({ isLoading: false });
          }

          // Listen for auth changes and update tokens - only set up once
          if (!authStateUnsubscribe) {
            authStateUnsubscribe = authService.onAuthStateChange(user => {
              if (user) {
                authService.getSession().then(({ session }) => {
                  set({
                    user,
                    session,
                    accessToken: session?.access_token ?? null,
                    refreshToken: session?.refresh_token ?? null,
                    isAuthenticated: true,
                  });
                });
              } else {
                set({
                  user: null,
                  session: null,
                  accessToken: null,
                  refreshToken: null,
                  isAuthenticated: false,
                });
              }
            });
          }

          hasInitialized = true;
        } catch (error) {
          console.error('Error initializing auth:', error);
          set({ isLoading: false });
        } finally {
          isInitializing = false;
        }
      },

      signOut: async () => {
        const { user } = get();

        // Clear user-specific localStorage data and reset IndexedDB connection
        // Note: Chats are preserved in user-specific IndexedDB and cloud (MongoDB)
        // No backup needed - each user has isolated storage that persists
        if (user?.id) {
          clearUserData(user.id);
          console.log('🗑️ User session data cleared');
        }

        // Clean up interval and listener
        if (refreshIntervalId) {
          clearInterval(refreshIntervalId);
          refreshIntervalId = null;
        }
        if (authStateUnsubscribe) {
          authStateUnsubscribe();
          authStateUnsubscribe = null;
        }

        // Reset initialization flags for next login
        hasInitialized = false;

        await authService.signOut();
        set({
          user: null,
          session: null,
          accessToken: null,
          refreshToken: null,
          isAuthenticated: false,
          isAdmin: false,
        });
      },

      forceLogout: (reason?: string) => {
        const message = reason || 'Your session has expired. Please sign in again.';

        // Clean up interval and listener
        if (refreshIntervalId) {
          clearInterval(refreshIntervalId);
          refreshIntervalId = null;
        }
        if (authStateUnsubscribe) {
          authStateUnsubscribe();
          authStateUnsubscribe = null;
        }

        // Reset initialization flags for next login
        hasInitialized = false;

        // Clear auth state immediately
        set({
          user: null,
          session: null,
          accessToken: null,
          refreshToken: null,
          isAuthenticated: false,
          isAdmin: false,
          sessionExpiredReason: message,
          isLoading: false,
        });

        // Also sign out from Supabase (fire and forget)
        authService.signOut().catch(err => {
          console.error('Error signing out from auth service:', err);
        });

        // Redirect to login page
        // Store the current path to redirect back after login
        // Note: Toast will be shown on login page via sessionExpiredReason
        const currentPath = window.location.pathname + window.location.search;
        const redirectUrl =
          currentPath !== '/' && currentPath !== '/signin'
            ? `?redirect=${encodeURIComponent(currentPath)}`
            : '';
        window.location.href = `/signin${redirectUrl}`;
      },

      clearSessionExpiredReason: () => {
        set({ sessionExpiredReason: null });
      },

      getAccessToken: () => {
        return get().accessToken;
      },

      checkSession: async () => {
        try {
          const isValid = await authService.isSessionValid();

          if (!isValid) {
            // Try to refresh the session
            const { session, error } = await authService.refreshSession();

            if (error || !session) {
              // Session is invalid and couldn't be refreshed - force logout
              get().forceLogout('Your session has expired. Please sign in again.');
              return false;
            }

            // Update with refreshed session
            set({
              session,
              user: session.user,
              accessToken: session.access_token,
              refreshToken: session.refresh_token,
              isAuthenticated: true,
            });
          }

          return true;
        } catch (error) {
          console.error('Error checking session:', error);
          return false;
        }
      },

      refreshSession: async () => {
        try {
          const { session, error } = await authService.refreshSession();

          if (error || !session) {
            console.error('Failed to refresh session:', error);
            // Session refresh failed - force logout
            get().forceLogout('Your session has expired. Please sign in again.');
            return;
          }

          set({
            session,
            user: session.user,
            accessToken: session.access_token,
            refreshToken: session.refresh_token,
            isAuthenticated: true,
          });
        } catch (error) {
          console.error('Error refreshing session:', error);
          // Unexpected error during refresh - force logout
          get().forceLogout('An error occurred. Please sign in again.');
        }
      },

      checkAdminStatus: async () => {
        try {
          // Import api dynamically to avoid circular dependency
          const { api } = await import('@/services/api');
          const response = await api.get<{ is_admin: boolean }>('/api/admin/me');
          set({ isAdmin: response.is_admin });
        } catch {
          // Fallback: check user role from auth response (community edition)
          const user = get().user;
          if (user?.role === 'admin') {
            set({ isAdmin: true });
          } else {
            set({ isAdmin: false });
          }
        }
      },
    }),
    {
      name: 'auth-storage',
      partialize: state => ({
        user: state.user,
        session: state.session,
        accessToken: state.accessToken,
        refreshToken: state.refreshToken,
        isAuthenticated: state.isAuthenticated,
        isAdmin: state.isAdmin,
      }),
    }
  )
);
