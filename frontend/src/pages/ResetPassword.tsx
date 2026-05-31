import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button } from '@/components/design-system';
import { Input } from '@/components/design-system';
import { authService } from '@/services/authService';
import './Onboarding.css';

export const ResetPassword = () => {
  const navigate = useNavigate();
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [isSuccess, setIsSuccess] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setIsLoading(true);
    setError(null);

    // Validate passwords match
    if (newPassword !== confirmPassword) {
      setError('Passwords do not match');
      setIsLoading(false);
      return;
    }

    // Validate password length
    if (newPassword.length < 8) {
      setError('Password must be at least 8 characters');
      setIsLoading(false);
      return;
    }

    try {
      const { error: updateError } = await authService.updatePassword(newPassword);

      if (updateError) {
        setError(updateError.message);
        setIsLoading(false);
        return;
      }

      // Success
      setIsSuccess(true);
      setError(null);

      // Auto-redirect after 3 seconds
      setTimeout(() => {
        navigate('/');
      }, 3000);
    } catch {
      setError('An unexpected error occurred. Please try again.');
      setIsLoading(false);
    }
  };

  return (
    <div className="onboarding-container">
      {/* Left side: Single image (60%) */}
      <div className="onboarding-left">
        <div className="onboarding-image-container">
          <img src="/image-1.webp" alt="ClaraVerse" className="onboarding-image" />
        </div>
      </div>

      {/* Right side: Reset Password Form (40%) */}
      <div className="onboarding-auth">
        <div className="auth-form-container">
          <div className="auth-form-content">
            <div className="auth-form-header">
              <h2>{isSuccess ? 'Password Updated!' : 'Set New Password'}</h2>
              <p>
                {isSuccess
                  ? 'Your password has been successfully updated. Redirecting to dashboard...'
                  : 'Enter your new password below'}
              </p>
            </div>

            {error && <div className="auth-form-error">{error}</div>}
            {isSuccess && (
              <div className="auth-form-success">
                Password successfully updated! Redirecting in 3 seconds...
              </div>
            )}

            {!isSuccess && (
              <form onSubmit={handleSubmit} className="auth-form-fields">
                <Input
                  type="password"
                  placeholder="New Password"
                  value={newPassword}
                  onChange={e => setNewPassword(e.target.value)}
                  required
                  disabled={isLoading}
                  minLength={8}
                />

                <Input
                  type="password"
                  placeholder="Confirm New Password"
                  value={confirmPassword}
                  onChange={e => setConfirmPassword(e.target.value)}
                  required
                  disabled={isLoading}
                  minLength={8}
                />

                <Button
                  type="submit"
                  variant="primary"
                  size="lg"
                  disabled={isLoading}
                  className="auth-submit-button"
                >
                  {isLoading ? 'Updating Password...' : 'Update Password'}
                </Button>
              </form>
            )}

            <div className="auth-form-footer">
              <p>
                Remember your password?{' '}
                <button
                  type="button"
                  className="auth-toggle-button"
                  onClick={() => navigate('/signin')}
                  disabled={isLoading || isSuccess}
                >
                  Sign In
                </button>
              </p>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};
