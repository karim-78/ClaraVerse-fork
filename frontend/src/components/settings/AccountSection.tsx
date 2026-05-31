import React, { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import {
  User,
  Palette,
  Type,
  Cloud,
  HardDrive,
  Shield,
  Download,
  Database,
  Lock,
  AlertTriangle,
  Trash2,
  Check,
  Copy,
  Calendar,
  MessageSquare,
  FileText,
  ExternalLink,
  Loader2,
} from 'lucide-react';
import { useAuthStore } from '@/store/useAuthStore';
import { useSettingsStore } from '@/store/useSettingsStore';
import { api } from '@/services/api';
import { toast } from '@/store/useToastStore';

export interface AccountSectionProps {
  /** Callback when settings change */
  onSave?: () => void;
}

/**
 * Account settings section component.
 * Displays profile info, preferences, data export, and account deletion.
 */
export const AccountSection: React.FC<AccountSectionProps> = ({ onSave: _onSave }) => {
  const navigate = useNavigate();
  const { user, signOut } = useAuthStore();
  const { theme, fontSize, chatPrivacyMode } = useSettingsStore();

  const [deleteConfirmation, setDeleteConfirmation] = useState('');
  const [isDeleting, setIsDeleting] = useState(false);
  const [isExporting, setIsExporting] = useState(false);
  const [copiedId, setCopiedId] = useState(false);

  const handleExportData = async () => {
    setIsExporting(true);
    try {
      const response = await api.get<Record<string, unknown>>('/api/user/data');

      // Add export metadata
      const exportData = {
        ...response,
        export_metadata: {
          exported_at: new Date().toISOString(),
          user_email: user?.email,
          format_version: '1.0',
        },
      };

      const blob = new Blob([JSON.stringify(exportData, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `claraverse-data-export-${new Date().toISOString().split('T')[0]}.json`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      toast.success('Data exported successfully!');
    } catch (error: unknown) {
      console.error('Failed to export data:', error);
      if (error instanceof Error && error.message.includes('404')) {
        toast.error('Export endpoint not available. Please ensure the backend is running.');
      } else {
        const errorMessage = error instanceof Error ? error.message : 'Unknown error occurred';
        toast.error(`Failed to export data: ${errorMessage}`);
      }
    } finally {
      setIsExporting(false);
    }
  };

  const handleDeleteAccount = async () => {
    if (deleteConfirmation.trim().toLowerCase() !== 'delete my account') {
      toast.error('Please type "delete my account" to confirm');
      return;
    }

    setIsDeleting(true);
    try {
      await api.deleteWithBody('/user/account', { confirmation: deleteConfirmation });
      toast.success('Account deleted successfully');
      await signOut();
      navigate('/');
    } catch (error) {
      console.error('Failed to delete account:', error);
      toast.error('Failed to delete account. Please try again.');
    } finally {
      setIsDeleting(false);
    }
  };

  const handleCopyId = async () => {
    if (user?.id) {
      await navigator.clipboard.writeText(user.id);
      setCopiedId(true);
      setTimeout(() => setCopiedId(false), 2000);
    }
  };

  const getInitials = (email: string | undefined) => {
    if (!email) return '?';
    return email.charAt(0).toUpperCase();
  };

  const formatTheme = (t: string) => t.charAt(0).toUpperCase() + t.slice(1);
  const formatFontSize = (f: string) => f.charAt(0).toUpperCase() + f.slice(1);
  const formatPrivacyMode = (m: string | null) => {
    if (!m) return 'Not set';
    return m === 'local' ? 'Local Only' : 'Cloud Sync';
  };

  return (
    <section className="settings-section account-section">
      <header className="account-header">
        <div className="account-title-section">
          <h1 className="account-main-title">Account</h1>
          <p className="account-subtitle">
            Manage your profile, preferences, export data, or delete your account.
          </p>
        </div>
      </header>

      <div className="account-tab-layout">
        {/* LEFT COLUMN */}
        <div className="account-column">
          {/* Profile Card */}
          <div className="account-card">
            <div className="account-card-header">
              <User size={18} />
              <h3>Profile</h3>
            </div>
            <div className="account-profile-content">
              <div className="account-avatar-large">{getInitials(user?.email)}</div>
              <div className="account-profile-details">
                <span className="account-email-large">{user?.email || 'Not signed in'}</span>
                <div className="account-id-row">
                  <span className="account-id-label">User ID:</span>
                  <code className="account-id-value">{user?.id?.slice(0, 12) || 'N/A'}...</code>
                  <button className="account-copy-btn" onClick={handleCopyId} title="Copy full ID">
                    {copiedId ? <Check size={14} /> : <Copy size={14} />}
                  </button>
                </div>
                {user?.created_at && (
                  <div className="account-member-since">
                    <Calendar size={14} />
                    <span>Member since {new Date(user.created_at).toLocaleDateString()}</span>
                  </div>
                )}
              </div>
            </div>
          </div>

          {/* Preferences Card */}
          <div className="account-card">
            <div className="account-card-header">
              <Palette size={18} />
              <h3>Preferences</h3>
            </div>
            <div className="account-preferences-grid">
              <div className="account-pref-item">
                <Palette size={16} />
                <div className="account-pref-content">
                  <span className="account-pref-label">Theme</span>
                  <span className="account-pref-value">{formatTheme(theme)}</span>
                </div>
              </div>
              <div className="account-pref-item">
                <Type size={16} />
                <div className="account-pref-content">
                  <span className="account-pref-label">Font Size</span>
                  <span className="account-pref-value">{formatFontSize(fontSize)}</span>
                </div>
              </div>
              <div className="account-pref-item">
                {chatPrivacyMode === 'cloud' ? <Cloud size={16} /> : <HardDrive size={16} />}
                <div className="account-pref-content">
                  <span className="account-pref-label">Storage</span>
                  <span className="account-pref-value">{formatPrivacyMode(chatPrivacyMode)}</span>
                </div>
              </div>
            </div>
          </div>

          {/* Privacy Policy Card */}
          <div className="account-card account-privacy-card">
            <div className="account-card-content-row">
              <Shield size={20} className="account-privacy-icon" />
              <div>
                <h3>Privacy Policy</h3>
                <p>Learn how we handle your data and your rights under GDPR</p>
              </div>
            </div>
            <Link to="/privacy" className="account-link-btn">
              View Policy <ExternalLink size={14} />
            </Link>
          </div>
        </div>

        {/* RIGHT COLUMN */}
        <div className="account-column">
          {/* Data Export Card */}
          <div className="account-card account-export-card">
            <div className="account-card-header">
              <Download size={18} />
              <h3>Export Your Data</h3>
            </div>
            <p className="account-export-description">
              Download all your data in JSON format. This includes:
            </p>
            <ul className="account-export-list">
              <li>
                <MessageSquare size={14} /> All conversations and messages
              </li>
              <li>
                <FileText size={14} /> Uploaded file metadata
              </li>
              <li>
                <Database size={14} /> User preferences and settings
              </li>
            </ul>
            <button
              className="account-export-btn"
              onClick={handleExportData}
              disabled={isExporting}
            >
              {isExporting ? (
                <>
                  <Loader2 size={16} className="animate-spin" />
                  Exporting...
                </>
              ) : (
                <>
                  <Download size={16} />
                  Export My Data
                </>
              )}
            </button>
            <p className="account-export-note">
              <Lock size={12} />
              GDPR Article 20 - Right to Data Portability
            </p>
          </div>

          {/* Storage Stats Card */}
          <div className="account-card">
            <div className="account-card-header">
              <Database size={18} />
              <h3>Storage</h3>
            </div>
            <div className="account-stats-grid">
              <div className="account-stat-item">
                <div className="account-stat-icon">
                  {chatPrivacyMode === 'cloud' ? <Cloud size={20} /> : <HardDrive size={20} />}
                </div>
                <div className="account-stat-content">
                  <span className="account-stat-label">Storage Mode</span>
                  <span className="account-stat-value">
                    {chatPrivacyMode === 'cloud' ? 'Cloud Sync' : 'Local Storage'}
                  </span>
                </div>
              </div>
              <div className="account-stat-item">
                <div className="account-stat-icon">
                  <Lock size={20} />
                </div>
                <div className="account-stat-content">
                  <span className="account-stat-label">Encryption</span>
                  <span className="account-stat-value">AES-256-GCM</span>
                </div>
              </div>
            </div>
          </div>

          {/* Danger Zone */}
          <div className="account-danger-zone">
            <div className="danger-zone-header">
              <AlertTriangle size={20} />
              <h3>Danger Zone</h3>
            </div>

            <div className="danger-zone-content">
              <div className="danger-warning">
                <p>
                  <strong>This action is permanent and cannot be undone.</strong>
                </p>
                <p>Deleting your account will permanently remove:</p>
                <ul>
                  <li>All your agents and workflows</li>
                  <li>All execution history</li>
                  <li>All API keys you've created</li>
                  <li>All saved credentials and integrations</li>
                  <li>All conversations and messages</li>
                  <li>All uploaded files</li>
                  <li>Your user preferences and settings</li>
                </ul>
              </div>

              <div className="delete-confirmation">
                <label htmlFor="delete-confirm">
                  Type <code>delete my account</code> to confirm:
                </label>
                <input
                  id="delete-confirm"
                  type="text"
                  value={deleteConfirmation}
                  onChange={e => setDeleteConfirmation(e.target.value)}
                  placeholder="delete my account"
                  className="delete-confirm-input"
                  disabled={isDeleting}
                />
              </div>

              <button
                className="delete-account-btn"
                onClick={handleDeleteAccount}
                disabled={
                  isDeleting || deleteConfirmation.trim().toLowerCase() !== 'delete my account'
                }
              >
                {isDeleting ? (
                  <>
                    <Loader2 size={16} className="animate-spin" />
                    Deleting Account...
                  </>
                ) : (
                  <>
                    <Trash2 size={16} />
                    Delete My Account
                  </>
                )}
              </button>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
};
