import { createBrowserRouter, RouterProvider, Outlet, Navigate } from 'react-router-dom';
import { useEffect } from 'react';
import {
  Dashboard,
  Onboarding,
  Chat,
  LumaUI,
  Notebooks,
  Agents,
  Community,
  Settings,
  DesignSystem,
  Credentials,
  PrivacyPolicy,
  Skills,
  SkillEditor,
  Nexus,
  DeviceAuth,
} from '@/pages';
import { ResetPassword } from '@/pages/ResetPassword';
import { ProtectedRoute, AdminRoute } from '@/components/auth';
import { AdminLayout } from '@/components/admin';
import {
  Dashboard as AdminDashboard,
  ProviderManagement,
  Analytics,
  ModelManagement,
  UserManagement,
  Traces,
  SystemModels,
  E2BSettings,
} from '@/pages/admin';
import { ErrorBoundary } from '@/components/ErrorBoundary';
import { useAuthStore } from '@/store/useAuthStore';

const router = createBrowserRouter([
  {
    // Root route wrapper with ErrorBoundary for all routes
    element: <Outlet />,
    errorElement: (
      <ErrorBoundary>
        <div />
      </ErrorBoundary>
    ),
    children: [
      {
        path: '/',
        element: (
          <ProtectedRoute>
            <Dashboard />
          </ProtectedRoute>
        ),
      },
      {
        path: '/signin',
        element: <Onboarding />,
      },
      {
        path: '/reset-password',
        element: <ResetPassword />,
      },
      {
        path: '/privacy',
        element: <PrivacyPolicy />,
      },
      {
        path: '/chat',
        element: (
          <ProtectedRoute>
            <Chat />
          </ProtectedRoute>
        ),
      },
      {
        path: '/chat/:chatId',
        element: (
          <ProtectedRoute>
            <Chat />
          </ProtectedRoute>
        ),
      },
      {
        path: '/artifacts',
        element: (
          <ProtectedRoute>
            <Chat />
          </ProtectedRoute>
        ),
      },
      {
        path: '/luma',
        element: (
          <ProtectedRoute>
            <LumaUI />
          </ProtectedRoute>
        ),
      },
      {
        path: '/notebooks',
        element: (
          <ProtectedRoute>
            <Notebooks />
          </ProtectedRoute>
        ),
      },
      {
        path: '/agents',
        element: (
          <ProtectedRoute>
            <Agents />
          </ProtectedRoute>
        ),
      },
      {
        path: '/agents/builder/:agentId',
        element: (
          <ProtectedRoute>
            <Agents />
          </ProtectedRoute>
        ),
      },
      {
        path: '/agents/deployed/:agentId',
        element: (
          <ProtectedRoute>
            <Agents />
          </ProtectedRoute>
        ),
      },
      {
        path: '/community',
        element: (
          <ProtectedRoute>
            <Community />
          </ProtectedRoute>
        ),
      },
      {
        path: '/device',
        element: (
          <ProtectedRoute>
            <DeviceAuth />
          </ProtectedRoute>
        ),
      },
      {
        path: '/settings',
        element: (
          <ProtectedRoute>
            <Settings />
          </ProtectedRoute>
        ),
      },
      {
        path: '/design-system',
        element: (
          <ProtectedRoute>
            <DesignSystem />
          </ProtectedRoute>
        ),
      },
      {
        path: '/credentials',
        element: (
          <ProtectedRoute>
            <Credentials />
          </ProtectedRoute>
        ),
      },
      {
        path: '/skills',
        element: (
          <ProtectedRoute>
            <Skills />
          </ProtectedRoute>
        ),
      },
      {
        path: '/skills/new',
        element: (
          <ProtectedRoute>
            <SkillEditor />
          </ProtectedRoute>
        ),
      },
      {
        path: '/skills/:skillId/edit',
        element: (
          <ProtectedRoute>
            <SkillEditor />
          </ProtectedRoute>
        ),
      },
      {
        path: '/nexus',
        element: (
          <ProtectedRoute>
            <Nexus />
          </ProtectedRoute>
        ),
      },
      {
        path: '/nexus/:projectId',
        element: (
          <ProtectedRoute>
            <Nexus />
          </ProtectedRoute>
        ),
      },
      {
        path: '/admin',
        element: (
          <AdminRoute>
            <AdminLayout />
          </AdminRoute>
        ),
        children: [
          { path: 'dashboard', element: <AdminDashboard /> },
          { path: 'providers', element: <ProviderManagement /> },
          { path: 'analytics', element: <Analytics /> },
          { path: 'models', element: <ModelManagement /> },
          { path: 'system-models', element: <SystemModels /> },
          { path: 'code-execution', element: <E2BSettings /> },
          { path: 'users', element: <UserManagement /> },
          { path: 'traces', element: <Traces /> },
          { path: '', element: <Navigate to="dashboard" replace /> },
        ],
      },
    ],
  },
]);

export const AppRouter = () => {
  const { initialize } = useAuthStore();

  // Initialize auth on mount
  useEffect(() => {
    initialize();
  }, [initialize]);

  return <RouterProvider router={router} />;
};
