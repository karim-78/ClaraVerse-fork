import * as React from 'react';
import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
// import { Github } from 'lucide-react'; // Removed - social login disabled
import { Button } from '@/components/design-system';
import { Input } from '@/components/design-system';
import { authService } from '@/services/authService';
import { useAuthStore } from '@/store/useAuthStore';
import './AuthForm.css';

interface AuthFormProps {
  defaultMode?: 'signin' | 'signup';
  redirectUrl?: string;
  className?: string;
}

export const AuthForm: React.FC<AuthFormProps> = ({
  defaultMode = 'signin',
  redirectUrl = '/',
  className = '',
}) => {
  const [mode, setMode] = useState<'signin' | 'signup' | 'forgot-password'>(defaultMode);
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [username, setUsername] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const navigate = useNavigate();
  const { setUser } = useAuthStore();

  React.useEffect(() => {
    setMode(defaultMode);
    setEmail('');
    setPassword('');
    setConfirmPassword('');
    setUsername('');
    setError(null);
    setSuccess(null);
  }, [defaultMode]);

  const handleEmailAuth = async (e: React.FormEvent) => {
    e.preventDefault();
    setIsLoading(true);
    setError(null);

    try {
      // Validation for sign-up mode
      if (mode === 'signup') {
        if (!username.trim()) {
          setError('Username is required');
          setIsLoading(false);
          return;
        }

        if (username.length < 3) {
          setError('Username must be at least 3 characters');
          setIsLoading(false);
          return;
        }

        if (password !== confirmPassword) {
          setError('Passwords do not match');
          setIsLoading(false);
          return;
        }

        if (password.length < 8) {
          setError('Password must be at least 8 characters');
          setIsLoading(false);
          return;
        }
      }

      let response;

      if (mode === 'signin') {
        // Sign in mode
        response = await authService.signIn(email, password);

        if (response.error) {
          setError(response.error.message || 'Invalid email or password');
          setIsLoading(false);
          return;
        }
      } else {
        // Sign up mode
        response = await authService.signUp(email, password, username.trim());

        if (response.error) {
          setError(response.error.message);
          setIsLoading(false);
          return;
        }
      }

      if (response.user && response.session) {
        setUser(response.user);
        // Store session with tokens
        useAuthStore.getState().setSession(response.session);
        setError(null);
        // Check admin status before navigating so AdminRoute has the correct state
        await useAuthStore.getState().checkAdminStatus();
        // Redirect admin users to admin dashboard, others to regular redirect URL
        if (useAuthStore.getState().isAdmin) {
          navigate('/admin/dashboard');
        } else {
          navigate(redirectUrl);
        }
      } else if (mode === 'signup' && response.user) {
        setError('Account created! Please check your email to confirm.');
        setIsLoading(false);
      }
    } catch {
      setError('An unexpected error occurred. Please try again.');
      setIsLoading(false);
    }
  };

  // Social login disabled - not functional in v2.0 (local auth only)
  // const handleGithubAuth = async () => {
  //   setIsLoading(true);
  //   setError(null);

  //   try {
  //     const { error } = await authService.signInWithGithub();
  //     if (error) {
  //       setError(error.message);
  //       setIsLoading(false);
  //     }
  //     // OAuth redirect happens automatically
  //   } catch {
  //     setError('Failed to sign in with GitHub.');
  //     setIsLoading(false);
  //   }
  // };

  // const handleGoogleAuth = async () => {
  //   setIsLoading(true);
  //   setError(null);

  //   try {
  //     const { error } = await authService.signInWithGoogle();
  //     if (error) {
  //       setError(error.message);
  //       setIsLoading(false);
  //     }
  //     // OAuth redirect happens automatically
  //   } catch {
  //     setError('Failed to sign in with Google.');
  //     setIsLoading(false);
  //   }
  // };

  const handleForgotPassword = async (e: React.FormEvent) => {
    e.preventDefault();
    setIsLoading(true);
    setError(null);
    setSuccess(null);

    try {
      if (!email) {
        setError('Please enter your email address');
        setIsLoading(false);
        return;
      }

      const { error } = await authService.resetPassword(email);

      if (error) {
        setError(error.message);
      } else {
        setSuccess('Password reset link sent! Please check your email.');
        setEmail('');
      }
    } catch {
      setError('Failed to send password reset email. Please try again.');
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className={`auth-form-container ${className}`}>
      <div className="auth-form-content">
        <div className="auth-form-header">
          <h2>
            {mode === 'signin'
              ? 'Welcome Back'
              : mode === 'signup'
                ? 'Create Account'
                : 'Reset Password'}
          </h2>
          <p>
            {mode === 'signin'
              ? 'Sign in to continue to ClaraVerse'
              : mode === 'signup'
                ? 'Sign up to get started with ClaraVerse'
                : 'Enter your email to receive a password reset link'}
          </p>
        </div>

        {error && <div className="auth-form-error">{error}</div>}
        {success && <div className="auth-form-success">{success}</div>}

        {/* Social login disabled - not functional in v2.0 (local auth only) */}
        {/* {mode !== 'forgot-password' && (
          <>
            <div className="auth-form-social">
              <Button
                variant="secondary"
                size="lg"
                onClick={handleGithubAuth}
                disabled={isLoading}
                className="auth-social-button auth-github-button"
              >
                <Github className="auth-social-icon" />
                Continue with GitHub
              </Button>

              <Button
                variant="secondary"
                size="lg"
                onClick={handleGoogleAuth}
                disabled={isLoading}
                className="auth-social-button auth-google-button"
              >
                <svg
                  width="18"
                  height="18"
                  viewBox="0 0 48 48"
                  className="auth-social-icon"
                  xmlns="http://www.w3.org/2000/svg"
                >
                  <path
                    fill="#EA4335"
                    d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z"
                  />
                  <path
                    fill="#4285F4"
                    d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z"
                  />
                  <path
                    fill="#FBBC05"
                    d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z"
                  />
                  <path
                    fill="#34A853"
                    d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.15 1.45-4.92 2.3-8.16 2.3-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z"
                  />
                  <path fill="none" d="M0 0h48v48H0z" />
                </svg>
                Continue with Google
              </Button>
            </div>

            <div className="auth-form-divider">
              <span>or</span>
            </div>
          </>
        )} */}

        <form
          onSubmit={mode === 'forgot-password' ? handleForgotPassword : handleEmailAuth}
          className="auth-form-fields"
        >
          {mode === 'signup' && (
            <Input
              type="text"
              placeholder="Username"
              value={username}
              onChange={e => setUsername(e.target.value)}
              required
              disabled={isLoading}
              minLength={3}
            />
          )}

          <Input
            type="email"
            placeholder="Email"
            value={email}
            onChange={e => setEmail(e.target.value)}
            required
            disabled={isLoading}
          />

          {mode !== 'forgot-password' && (
            <Input
              type="password"
              placeholder="Password"
              value={password}
              onChange={e => setPassword(e.target.value)}
              required
              disabled={isLoading}
              minLength={mode === 'signup' ? 8 : 6}
            />
          )}

          {mode === 'signup' && (
            <Input
              type="password"
              placeholder="Confirm Password"
              value={confirmPassword}
              onChange={e => setConfirmPassword(e.target.value)}
              required
              disabled={isLoading}
              minLength={8}
            />
          )}

          {mode === 'signin' && (
            <div className="auth-forgot-password">
              <button
                type="button"
                className="auth-link-button"
                onClick={() => setMode('forgot-password')}
                disabled={isLoading}
              >
                Forgot password?
              </button>
            </div>
          )}

          <Button
            type="submit"
            variant="primary"
            size="lg"
            disabled={isLoading}
            className="auth-submit-button"
          >
            {isLoading
              ? 'Loading...'
              : mode === 'signin'
                ? 'Sign In'
                : mode === 'signup'
                  ? 'Sign Up'
                  : 'Send Reset Link'}
          </Button>
        </form>

        <div className="auth-form-footer">
          <p>
            {mode === 'forgot-password' ? (
              <>
                Remember your password?{' '}
                <button
                  type="button"
                  className="auth-toggle-button"
                  onClick={() => setMode('signin')}
                  disabled={isLoading}
                >
                  Sign In
                </button>
              </>
            ) : mode === 'signin' ? (
              <>
                Don&apos;t have an account?{' '}
                <button
                  type="button"
                  className="auth-toggle-button"
                  onClick={() => setMode('signup')}
                  disabled={isLoading}
                >
                  Sign Up
                </button>
              </>
            ) : (
              <>
                Already have an account?{' '}
                <button
                  type="button"
                  className="auth-toggle-button"
                  onClick={() => setMode('signin')}
                  disabled={isLoading}
                >
                  Sign In
                </button>
              </>
            )}
          </p>
        </div>
      </div>
    </div>
  );
};
