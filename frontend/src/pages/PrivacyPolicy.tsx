import { Link } from 'react-router-dom';
import { ChevronLeft } from 'lucide-react';

export const PrivacyPolicy = () => {
  return (
    <div className="privacy-policy-page">
      <header className="privacy-policy-header">
        <Link to="/" className="privacy-policy-back-link">
          <ChevronLeft size={20} />
        </Link>
        <h1 className="privacy-policy-title">Privacy Policy</h1>
      </header>

      <main className="privacy-policy-container">
        <article className="privacy-policy-content">
          {/* Hero Section */}
          <div className="privacy-hero">
            <h1>Privacy Policy</h1>
            <p className="privacy-tagline">
              We believe in transparency. Here's exactly what we do with your data.
            </p>
            <p className="privacy-updated">Last updated: December 25, 2024</p>
          </div>

          {/* Quick Summary */}
          <section className="privacy-summary">
            <h2>The Short Version</h2>
            <p>
              We collect only what's necessary to provide ClaraVerse services. Your data is
              encrypted, you can export it anytime, and you can delete everything with one click. We
              don't sell your data. Ever.
            </p>
          </section>

          {/* Data We Collect */}
          <section className="privacy-section">
            <h2>1. Data We Collect</h2>

            <h3>Account Information</h3>
            <p>
              User ID and email address provided by your authentication provider (Supabase). This is
              the minimum needed to identify your account.
            </p>

            <h3>Content You Create</h3>
            <ul>
              <li>
                <strong>Conversations</strong> — Chat messages and conversation history
              </li>
              <li>
                <strong>Uploaded Files</strong> — Images, PDFs, CSV, Excel, JSON, and other files
                you upload for AI processing
              </li>
              <li>
                <strong>Agents & Workflows</strong> — Custom AI agents and workflows you build
              </li>
              <li>
                <strong>Credentials</strong> — Encrypted API keys for external providers you
                configure
              </li>
            </ul>

            <h3>Usage Data</h3>
            <p>
              Timestamps, model selections, and feature usage patterns. This helps us improve the
              service and debug issues.
            </p>
          </section>

          {/* How We Use Your Data */}
          <section className="privacy-section">
            <h2>2. How We Use Your Data</h2>
            <ul>
              <li>To provide and operate ClaraVerse services</li>
              <li>To process your requests through AI model providers</li>
              <li>To sync your data across devices (when you enable cloud sync)</li>
              <li>To authenticate and secure your account</li>
              <li>To improve the service based on usage patterns</li>
            </ul>
            <p className="privacy-legal">
              <strong>Legal Basis:</strong> Legitimate interest in providing the service (GDPR
              Article 6(1)(f))
            </p>
          </section>

          {/* Data Retention */}
          <section className="privacy-section">
            <h2>3. Data Retention</h2>
            <table className="privacy-table">
              <thead>
                <tr>
                  <th>Data Type</th>
                  <th>Retention Period</th>
                  <th>Notes</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td>Temporary Files</td>
                  <td>30 minutes</td>
                  <td>Auto-deleted after processing</td>
                </tr>
                <tr>
                  <td>Local Conversations</td>
                  <td>Until you clear them</td>
                  <td>Stored only on your device</td>
                </tr>
                <tr>
                  <td>Cloud-Synced Data</td>
                  <td>Until you delete it</td>
                  <td>AES-256 encrypted at rest</td>
                </tr>
                <tr>
                  <td>Audit Logs</td>
                  <td>90 days</td>
                  <td>Security & compliance only</td>
                </tr>
              </tbody>
            </table>
          </section>

          {/* Your Rights */}
          <section className="privacy-section">
            <h2>4. Your Rights (GDPR)</h2>
            <p>You have full control over your data:</p>

            <div className="privacy-rights">
              <div className="privacy-right">
                <h4>Right to Access</h4>
                <p>Download all your personal data at any time from Settings → Account.</p>
                <code>Article 15</code>
              </div>
              <div className="privacy-right">
                <h4>Right to Erasure</h4>
                <p>Delete your account and all associated data permanently.</p>
                <code>Article 17</code>
              </div>
              <div className="privacy-right">
                <h4>Right to Portability</h4>
                <p>Export your data in machine-readable JSON format.</p>
                <code>Article 20</code>
              </div>
            </div>
          </section>

          {/* Third Parties */}
          <section className="privacy-section">
            <h2>5. Third-Party Services</h2>

            <div className="privacy-third-party">
              <h4>Supabase</h4>
              <p>Authentication and user management</p>
              <span className="privacy-data-shared">Data shared: User ID, email, auth tokens</span>
            </div>

            <div className="privacy-third-party">
              <h4>AI Model Providers</h4>
              <p>Processing chat messages and generating responses</p>
              <span className="privacy-data-shared">
                Data shared: Messages and file content you send. Provider varies based on your
                selection (OpenAI, Anthropic, Google, etc.) <br /> (Note: TEE Provider never store
                data and they cannot access it as well)
              </span>
            </div>
          </section>

          {/* Security */}
          <section className="privacy-section">
            <h2>6. Security Measures</h2>
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
          <section className="privacy-section">
            <h2>7. Cookies</h2>
            <p>
              We use minimal cookies for authentication purposes only. No tracking cookies, no
              third-party analytics, no advertising cookies.
            </p>
          </section>

          {/* Changes */}
          <section className="privacy-section">
            <h2>8. Changes to This Policy</h2>
            <p>
              We'll notify you of significant changes via email or in-app notification. Continued
              use after changes constitutes acceptance.
            </p>
          </section>

          {/* Contact */}
          <section className="privacy-section privacy-contact">
            <h2>9. Contact Us</h2>
            <p>Questions about this policy or want to exercise your rights?</p>
            <a href="mailto:privacy@claraverse.app" className="privacy-email-link">
              privacy@claraverse.app
            </a>
          </section>

          {/* Footer */}
          <footer className="privacy-footer">
            <p>
              This policy is written in plain language because we believe privacy policies shouldn't
              require a law degree to understand.
            </p>
          </footer>
        </article>
      </main>

      <style>{`
        .privacy-policy-page {
          min-height: 100vh;
          background: #0a0a0a;
          color: #e4e4e7;
        }

        .privacy-policy-header {
          display: flex;
          align-items: center;
          gap: 1rem;
          padding: 1.25rem 2rem;
          border-bottom: 1px solid rgba(255, 255, 255, 0.08);
          background: rgba(0, 0, 0, 0.5);
          backdrop-filter: blur(10px);
          position: sticky;
          top: 0;
          z-index: 100;
        }

        .privacy-policy-back-link {
          display: flex;
          align-items: center;
          justify-content: center;
          width: 36px;
          height: 36px;
          border-radius: 8px;
          background: rgba(255, 255, 255, 0.05);
          color: #a1a1aa;
          transition: all 0.2s;
        }

        .privacy-policy-back-link:hover {
          background: rgba(255, 255, 255, 0.1);
          color: #fff;
        }

        .privacy-policy-title {
          font-size: 1.125rem;
          font-weight: 500;
          color: #fff;
        }

        .privacy-policy-container {
          max-width: 720px;
          margin: 0 auto;
          padding: 3rem 1.5rem 4rem;
        }

        .privacy-policy-content {
          font-size: 1rem;
          line-height: 1.7;
        }

        /* Hero */
        .privacy-hero {
          margin-bottom: 3rem;
          padding-bottom: 2rem;
          border-bottom: 1px solid rgba(255, 255, 255, 0.08);
        }

        .privacy-hero h1 {
          font-size: 2.5rem;
          font-weight: 700;
          color: #fff;
          margin: 0 0 0.75rem;
          letter-spacing: -0.02em;
        }

        .privacy-tagline {
          font-size: 1.25rem;
          color: #a1a1aa;
          margin: 0 0 1rem;
        }

        .privacy-updated {
          font-size: 0.875rem;
          color: #71717a;
          margin: 0;
        }

        /* Summary */
        .privacy-summary {
          background: rgba(233, 30, 99, 0.05);
          border: 1px solid rgba(233, 30, 99, 0.15);
          border-radius: 12px;
          padding: 1.5rem;
          margin-bottom: 3rem;
        }

        .privacy-summary h2 {
          font-size: 1rem;
          font-weight: 600;
          color: #e91e63;
          margin: 0 0 0.75rem;
          text-transform: uppercase;
          letter-spacing: 0.05em;
        }

        .privacy-summary p {
          margin: 0;
          color: #d4d4d8;
        }

        /* Sections */
        .privacy-section {
          margin-bottom: 2.5rem;
        }

        .privacy-section h2 {
          font-size: 1.375rem;
          font-weight: 600;
          color: #fff;
          margin: 0 0 1.25rem;
          padding-bottom: 0.75rem;
          border-bottom: 1px solid rgba(255, 255, 255, 0.08);
        }

        .privacy-section h3 {
          font-size: 1rem;
          font-weight: 600;
          color: #e4e4e7;
          margin: 1.5rem 0 0.5rem;
        }

        .privacy-section h3:first-of-type {
          margin-top: 0;
        }

        .privacy-section h4 {
          font-size: 0.9375rem;
          font-weight: 600;
          color: #e4e4e7;
          margin: 0 0 0.25rem;
        }

        .privacy-section p {
          color: #a1a1aa;
          margin: 0 0 1rem;
        }

        .privacy-section ul {
          list-style: none;
          padding: 0;
          margin: 0 0 1rem;
        }

        .privacy-section ul li {
          position: relative;
          padding-left: 1.25rem;
          margin-bottom: 0.5rem;
          color: #a1a1aa;
        }

        .privacy-section ul li::before {
          content: '';
          position: absolute;
          left: 0;
          top: 0.6em;
          width: 5px;
          height: 5px;
          border-radius: 50%;
          background: #e91e63;
        }

        .privacy-section ul li strong {
          color: #e4e4e7;
        }

        .privacy-legal {
          font-size: 0.875rem;
          padding: 0.75rem 1rem;
          background: rgba(255, 255, 255, 0.03);
          border-radius: 8px;
          margin-top: 1rem;
        }

        .privacy-legal strong {
          color: #e91e63;
        }

        /* Table */
        .privacy-table {
          width: 100%;
          border-collapse: collapse;
          font-size: 0.875rem;
          margin: 1rem 0;
        }

        .privacy-table th,
        .privacy-table td {
          text-align: left;
          padding: 0.75rem 1rem;
          border-bottom: 1px solid rgba(255, 255, 255, 0.06);
        }

        .privacy-table th {
          font-weight: 500;
          color: #71717a;
          font-size: 0.75rem;
          text-transform: uppercase;
          letter-spacing: 0.05em;
        }

        .privacy-table td {
          color: #a1a1aa;
        }

        .privacy-table td:first-child {
          color: #e4e4e7;
          font-weight: 500;
        }

        .privacy-table tr:last-child td {
          border-bottom: none;
        }

        /* Rights */
        .privacy-rights {
          display: grid;
          gap: 1rem;
          margin-top: 1rem;
        }

        .privacy-right {
          padding: 1rem 1.25rem;
          background: rgba(255, 255, 255, 0.02);
          border: 1px solid rgba(255, 255, 255, 0.06);
          border-radius: 10px;
        }

        .privacy-right p {
          font-size: 0.875rem;
          margin: 0.25rem 0 0.75rem;
        }

        .privacy-right code {
          display: inline-block;
          font-family: ui-monospace, monospace;
          font-size: 0.75rem;
          padding: 0.25rem 0.5rem;
          background: rgba(233, 30, 99, 0.1);
          border-radius: 4px;
          color: #e91e63;
        }

        /* Third Party */
        .privacy-third-party {
          padding: 1rem 1.25rem;
          background: rgba(255, 255, 255, 0.02);
          border: 1px solid rgba(255, 255, 255, 0.06);
          border-radius: 10px;
          margin-bottom: 0.75rem;
        }

        .privacy-third-party h4 {
          margin-bottom: 0.25rem;
        }

        .privacy-third-party p {
          font-size: 0.875rem;
          margin: 0 0 0.5rem;
        }

        .privacy-data-shared {
          display: block;
          font-size: 0.8125rem;
          color: #71717a;
        }

        /* Contact */
        .privacy-contact {
          text-align: center;
          padding: 2rem;
          background: rgba(255, 255, 255, 0.02);
          border: 1px solid rgba(255, 255, 255, 0.06);
          border-radius: 12px;
        }

        .privacy-contact h2 {
          border-bottom: none;
          padding-bottom: 0;
        }

        .privacy-email-link {
          display: inline-block;
          padding: 0.75rem 1.5rem;
          background: #e91e63;
          color: #fff;
          font-weight: 500;
          border-radius: 8px;
          text-decoration: none;
          transition: background 0.2s;
        }

        .privacy-email-link:hover {
          background: #d81b60;
        }

        /* Footer */
        .privacy-footer {
          margin-top: 3rem;
          padding-top: 2rem;
          border-top: 1px solid rgba(255, 255, 255, 0.08);
          text-align: center;
        }

        .privacy-footer p {
          font-size: 0.875rem;
          color: #52525b;
          font-style: italic;
        }

        @media (max-width: 640px) {
          .privacy-policy-container {
            padding: 2rem 1rem 3rem;
          }

          .privacy-hero h1 {
            font-size: 2rem;
          }

          .privacy-tagline {
            font-size: 1.125rem;
          }

          .privacy-table {
            font-size: 0.8125rem;
          }

          .privacy-table th,
          .privacy-table td {
            padding: 0.625rem 0.75rem;
          }
        }
      `}</style>
    </div>
  );
};
