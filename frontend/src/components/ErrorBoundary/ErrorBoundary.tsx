import { Component, type ErrorInfo, type ReactNode } from 'react';
import { Copy, RefreshCw, ChevronDown, ChevronUp, Check } from 'lucide-react';
import ErrorClara from '@/assets/mascot/Error_Clara.png';
import styles from './ErrorBoundary.module.css';

interface Props {
  children: ReactNode;
}

interface State {
  hasError: boolean;
  error: Error | null;
  errorInfo: ErrorInfo | null;
  showDetails: boolean;
  copySuccess: boolean;
  sendingReport: boolean;
  reportSent: boolean;
  reportError: string | null;
  sendDiagnostics: boolean;
}

const DISCORD_WEBHOOK_URL = import.meta.env.VITE_DISCORD_ERROR_WEBHOOK || '';

class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props);
    this.state = {
      hasError: false,
      error: null,
      errorInfo: null,
      showDetails: false,
      copySuccess: false,
      sendingReport: false,
      reportSent: false,
      reportError: null,
      sendDiagnostics: true, // Default to sending diagnostics
    };
  }

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo): void {
    this.setState({ errorInfo });
    console.error('ErrorBoundary caught an error:', error, errorInfo);

    // Error logged to console above
  }

  generateErrorReport = (): string => {
    const { error, errorInfo } = this.state;
    const timestamp = new Date().toISOString();
    const appVersion = import.meta.env.VITE_APP_VERSION || 'unknown';

    return `
═══════════════════════════════════════════════════
           CLARAVERSE ERROR REPORT
═══════════════════════════════════════════════════

📅 Timestamp: ${timestamp}
🌐 URL: ${window.location.href}
📱 Browser: ${navigator.userAgent}
📦 App Version: ${appVersion}

───────────────────────────────────────────────────
❌ ERROR MESSAGE
───────────────────────────────────────────────────
${error?.message || 'Unknown error'}

───────────────────────────────────────────────────
📚 STACK TRACE
───────────────────────────────────────────────────
${error?.stack || 'No stack trace available'}

───────────────────────────────────────────────────
🧩 COMPONENT STACK
───────────────────────────────────────────────────
${errorInfo?.componentStack || 'No component stack available'}

═══════════════════════════════════════════════════
`.trim();
  };

  handleCopyReport = async (): Promise<void> => {
    try {
      const report = this.generateErrorReport();
      await navigator.clipboard.writeText(report);
      this.setState({ copySuccess: true });
      setTimeout(() => this.setState({ copySuccess: false }), 2000);
    } catch (err) {
      console.error('Failed to copy report:', err);
    }
  };

  sendToDiscord = async (): Promise<boolean> => {
    if (!DISCORD_WEBHOOK_URL) {
      console.warn('Discord webhook not configured');
      return false;
    }

    const { error, errorInfo } = this.state;
    const timestamp = new Date().toISOString();

    // Truncate stack trace if too long for Discord embed
    const stackTrace = error?.stack || 'No stack trace';
    const truncatedStack =
      stackTrace.length > 1000 ? stackTrace.substring(0, 1000) + '\n... (truncated)' : stackTrace;

    const payload = {
      embeds: [
        {
          title: '🚨 DobbyAI Error Report',
          color: 15158332, // Red color
          fields: [
            {
              name: '❌ Error',
              value: `\`\`\`${error?.message || 'Unknown error'}\`\`\``,
              inline: false,
            },
            {
              name: '🌐 URL',
              value: window.location.href,
              inline: true,
            },
            {
              name: '📅 Time',
              value: timestamp,
              inline: true,
            },
            {
              name: '📱 Browser',
              value: navigator.userAgent.substring(0, 100) + '...',
              inline: false,
            },
          ],
          description: `**Stack Trace:**\n\`\`\`\n${truncatedStack}\n\`\`\``,
          footer: {
            text: `Component: ${errorInfo?.componentStack?.split('\n')[1]?.trim() || 'Unknown'}`,
          },
        },
      ],
    };

    try {
      const response = await fetch(DISCORD_WEBHOOK_URL, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });

      return response.ok;
    } catch (err) {
      console.error('Failed to send to Discord:', err);
      return false;
    }
  };

  handleRestartAndSend = async (): Promise<void> => {
    const { sendDiagnostics } = this.state;

    this.setState({ sendingReport: true, reportError: null });

    // Send diagnostics if checkbox is checked
    if (sendDiagnostics && DISCORD_WEBHOOK_URL) {
      await this.sendToDiscord();
    }

    // Restart to chat page
    window.location.href = '/chat';
  };

  toggleDetails = (): void => {
    this.setState(prev => ({ showDetails: !prev.showDetails }));
  };

  toggleSendDiagnostics = (): void => {
    this.setState(prev => ({ sendDiagnostics: !prev.sendDiagnostics }));
  };

  render(): ReactNode {
    const { hasError, error, errorInfo, showDetails, copySuccess, sendingReport, sendDiagnostics } =
      this.state;
    const { children } = this.props;

    if (!hasError) {
      return children;
    }

    return (
      <div className={styles.container}>
        <div className={styles.content}>
          {/* Mascot */}
          <img src={ErrorClara} alt="Dobby looking apologetic" className={styles.mascot} />

          {/* Error Message */}
          <h1 className={styles.title}>Oops! Something went wrong</h1>
          <p className={styles.subtitle}>
            Dobby ran into an unexpected error. You can help us fix this by sending an error report.
          </p>

          {/* Diagnostics Checkbox */}
          <label className={styles.checkboxLabel}>
            <input
              type="checkbox"
              checked={sendDiagnostics}
              onChange={this.toggleSendDiagnostics}
              className={styles.checkbox}
            />
            <span>Send anonymous diagnostics to help improve Dobby</span>
          </label>

          {/* Privacy Note */}
          <p className={styles.privacyNote}>
            Error reports are sent to DobbyAI's Discord and contain only technical error
            information (error message, stack trace). No personal data or chat content is included.
          </p>

          {/* Primary Action - Restart & Send */}
          <button
            onClick={this.handleRestartAndSend}
            className={styles.primaryButton}
            disabled={sendingReport}
          >
            {sendingReport ? (
              <>
                <span className={styles.spinner} />
                Restarting...
              </>
            ) : (
              <>
                <RefreshCw size={18} />
                {sendDiagnostics ? 'Restart & Send Diagnostics' : 'Restart Dobby'}
              </>
            )}
          </button>

          {/* Secondary Action - Copy Report */}
          <button
            onClick={this.handleCopyReport}
            className={styles.secondaryButton}
            disabled={copySuccess}
          >
            {copySuccess ? (
              <>
                <Check size={18} />
                Copied!
              </>
            ) : (
              <>
                <Copy size={18} />
                Copy Error Report
              </>
            )}
          </button>

          {/* Technical Details Toggle */}
          <button onClick={this.toggleDetails} className={styles.detailsToggle}>
            {showDetails ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
            {showDetails ? 'Hide' : 'Show'} Technical Details
          </button>

          {/* Technical Details */}
          {showDetails && (
            <div className={styles.details}>
              <div className={styles.detailSection}>
                <h3>Error Message</h3>
                <pre>{error?.message || 'Unknown error'}</pre>
              </div>
              <div className={styles.detailSection}>
                <h3>Stack Trace</h3>
                <pre>{error?.stack || 'No stack trace available'}</pre>
              </div>
              {errorInfo?.componentStack && (
                <div className={styles.detailSection}>
                  <h3>Component Stack</h3>
                  <pre>{errorInfo.componentStack}</pre>
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    );
  }
}

export default ErrorBoundary;
