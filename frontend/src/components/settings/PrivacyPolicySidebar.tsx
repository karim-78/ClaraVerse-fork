import './PrivacyPolicySidebar.css';

export const PrivacyPolicySidebar = () => {
  return (
    <aside className="privacy-policy-sidebar">
      <div className="privacy-policy-sidebar-header">
        <h2>Privacy Policy</h2>
        <p className="privacy-policy-updated">Last updated: December 25, 2024</p>
      </div>

      <div className="privacy-policy-sidebar-content">
        {/* Quick Summary */}
        <section className="policy-summary">
          <h3>The Short Version</h3>
          <p>
            We collect only what's necessary to provide ClaraVerse services. Your data is encrypted,
            you can export it anytime, and you can delete everything with one click. We don't sell
            your data. Ever.
          </p>
        </section>

        {/* Data We Collect */}
        <section className="policy-section">
          <h3>1. Data We Collect</h3>

          <h4>Account Information</h4>
          <p>
            User ID and email address provided by your authentication provider (Supabase). This is
            the minimum needed to identify your account.
          </p>

          <h4>Content You Create</h4>
          <ul>
            <li>
              <strong>Conversations</strong> — Chat messages and conversation history
            </li>
            <li>
              <strong>Uploaded Files</strong> — Images, PDFs, CSV, Excel, JSON, and other files you
              upload for AI processing
            </li>
            <li>
              <strong>Agents & Workflows</strong> — Custom AI agents and workflows you build
            </li>
            <li>
              <strong>Credentials</strong> — Encrypted API keys for external providers you configure
            </li>
          </ul>

          <h4>Usage Data</h4>
          <p>
            Timestamps, model selections, and feature usage patterns. This helps us improve the
            service and debug issues.
          </p>
        </section>

        {/* How We Use Your Data */}
        <section className="policy-section">
          <h3>2. How We Use Your Data</h3>
          <ul>
            <li>To provide and operate ClaraVerse services</li>
            <li>To process your requests through AI model providers</li>
            <li>To sync your data across devices (when you enable cloud sync)</li>
            <li>To authenticate and secure your account</li>
            <li>To improve the service based on usage patterns</li>
          </ul>
          <p className="policy-legal">
            <strong>Legal Basis:</strong> Legitimate interest in providing the service (GDPR Article
            6(1)(f))
          </p>
        </section>

        {/* Data Retention */}
        <section className="policy-section">
          <h3>3. Data Retention</h3>
          <div className="policy-table-wrapper">
            <table className="policy-table">
              <thead>
                <tr>
                  <th>Data Type</th>
                  <th>Retention</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td>Temporary Files - (PDF, Files Uploaded and Images)</td>
                  <td>30 minutes</td>
                </tr>
                <tr>
                  <td>
                    Local Conversations - (There stored in your browser never accessible by
                    claraverse)
                  </td>
                  <td>Until cleared</td>
                </tr>
                <tr>
                  <td>Cloud-Synced Data - (If cross device sync is enabled)</td>
                  <td>Until deleted</td>
                </tr>
                <tr>
                  <td>Audit Logs - (Only Contains Timestamps, User ID, and Action Type)</td>
                  <td>90 days</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        {/* Your Rights */}
        <section className="policy-section">
          <h3>4. Your Rights (GDPR)</h3>
          <p>You have full control over your data:</p>

          <div className="policy-rights">
            <div className="policy-right">
              <h4>Right to Access</h4>
              <p>Download all your personal data at any time from Settings.</p>
              <code>Article 15</code>
            </div>
            <div className="policy-right">
              <h4>Right to Erasure</h4>
              <p>Delete your account and all associated data permanently.</p>
              <code>Article 17</code>
            </div>
            <div className="policy-right">
              <h4>Right to Portability</h4>
              <p>Export your data in machine-readable JSON format.</p>
              <code>Article 20</code>
            </div>
          </div>
        </section>

        {/* Third Parties */}
        <section className="policy-section">
          <h3>5. Third-Party Services</h3>

          <div className="policy-third-party">
            <h4>Supabase</h4>
            <p>Authentication and user management</p>
            <span className="policy-data-shared">Data shared: User ID, email, auth tokens</span>
          </div>

          <div className="policy-third-party">
            <h4>AI Model Providers</h4>
            <p>Processing chat messages and generating responses</p>
            <span className="policy-data-shared">
              Data shared: Messages and file content you send. Provider varies based on your
              selection (OpenAI, Anthropic, Google, etc.)
              <br />
              (Note: TEE Provider never store data and they cannot access it as well)
            </span>
          </div>
        </section>

        {/* Security */}
        <section className="policy-section">
          <h3>6. Security Measures</h3>
          <ul>
            <li>AES-256-GCM encryption for sensitive data at rest</li>
            <li>Bcrypt hashing for API key storage</li>
            <li>JWT-based authentication with secure token handling</li>
            <li>HTTPS encryption for all data in transit</li>
            <li>Rate limiting and DDoS protection</li>
            <li>Automatic data expiration for temporary files</li>
          </ul>
        </section>

        {/* Cookies */}
        <section className="policy-section">
          <h3>7. Cookies</h3>
          <p>
            We use minimal cookies for authentication purposes only. No tracking cookies, no
            third-party analytics, no advertising cookies.
          </p>
        </section>

        {/* Changes */}
        <section className="policy-section">
          <h3>8. Changes to This Policy</h3>
          <p>
            We'll notify you of significant changes via email or in-app notification. Continued use
            after changes constitutes acceptance.
          </p>
        </section>

        {/* Contact */}
        <section className="policy-section policy-contact">
          <h3>9. Contact Us</h3>
          <p>Questions about this policy or want to exercise your rights?</p>
          <a href="mailto:privacy@claraverse.app" className="policy-email-link">
            privacy@claraverse.app
          </a>
        </section>

        {/* Footer */}
        <footer className="policy-footer">
          <p>
            This policy is written in plain language because we believe privacy policies shouldn't
            require a law degree to understand.
          </p>
        </footer>
      </div>
    </aside>
  );
};
