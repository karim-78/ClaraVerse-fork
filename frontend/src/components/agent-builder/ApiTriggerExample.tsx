import { useState } from 'react';
import { Copy, Check, ChevronDown, ChevronUp, Key } from 'lucide-react';

interface ApiTriggerExampleProps {
  agentId: string;
  /** Whether this agent uses file inputs */
  hasFileInput?: boolean;
}

export function ApiTriggerExample({ agentId, hasFileInput = false }: ApiTriggerExampleProps) {
  const [copied, setCopied] = useState(false);
  const [expanded, setExpanded] = useState(false);

  // Use the backend API URL, not the frontend URL
  const baseUrl = import.meta.env.VITE_API_BASE_URL || window.location.origin;

  // Upload file (API key needs "upload" scope) - only for file-based agents
  // Uses /api/external/upload which has open CORS for external access
  const uploadCmd = `curl -X POST ${baseUrl}/api/external/upload \\
  -H "X-API-Key: YOUR_API_KEY" \\
  -F "file=@image.png"`;

  // Simple trigger (for text-based agents)
  const simpleTriggerCmd = `curl -X POST ${baseUrl}/api/trigger/${agentId} \\
  -H "X-API-Key: YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"input": {"message": "your input here"}}'`;

  // File trigger (for file-based agents)
  const fileTriggerCmd = `curl -X POST ${baseUrl}/api/trigger/${agentId} \\
  -H "X-API-Key: YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "input": {
      "file_id": "FILE_ID_FROM_UPLOAD",
      "filename": "image.png",
      "mime_type": "image/png"
    }
  }'`;

  // Check execution status
  const statusCmd = `curl ${baseUrl}/api/trigger/status/EXECUTION_ID \\
  -H "X-API-Key: YOUR_API_KEY"`;

  // Full scripts for copy
  const simpleFullScript = `# Step 1: Trigger the agent
${simpleTriggerCmd}

# Step 2: Check execution status (use executionId from step 1)
${statusCmd}`;

  const fileFullScript = `# Step 1: Upload file
${uploadCmd}

# Step 2: Trigger workflow (use file_id from step 1)
${fileTriggerCmd}

# Step 3: Poll for results (use executionId from step 2)
${statusCmd}`;

  const handleCopy = () => {
    navigator.clipboard.writeText(hasFileInput ? fileFullScript : simpleFullScript);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  // For simple agents, show trigger + status in main view
  if (!hasFileInput) {
    return (
      <div className="space-y-3">
        {/* Trigger example */}
        <div>
          <p className="text-[10px] text-[var(--color-text-tertiary)] mb-1">
            <strong>1. Trigger Agent</strong>
          </p>
          <div className="relative">
            <pre className="p-3 bg-[var(--color-bg-tertiary)] rounded-lg text-[10px] font-mono overflow-x-auto whitespace-pre-wrap text-[var(--color-text-secondary)]">
              {simpleTriggerCmd}
            </pre>
          </div>
        </div>

        {/* Status check example */}
        <div>
          <p className="text-[10px] text-[var(--color-text-tertiary)] mb-1">
            <strong>2. Check Status</strong> (use executionId from response)
          </p>
          <div className="relative">
            <pre className="p-3 bg-[var(--color-bg-tertiary)] rounded-lg text-[10px] font-mono overflow-x-auto whitespace-pre-wrap text-[var(--color-text-secondary)]">
              {statusCmd}
            </pre>
            <button
              onClick={handleCopy}
              className="absolute top-2 right-2 p-1.5 rounded bg-[var(--color-surface)] hover:bg-[var(--color-surface-hover)] transition-colors"
              title="Copy full script"
            >
              {copied ? (
                <Check size={12} className="text-green-400" />
              ) : (
                <Copy size={12} className="text-[var(--color-text-tertiary)]" />
              )}
            </button>
          </div>
        </div>

        {/* Link to API keys */}
        <p className="text-[10px] text-[var(--color-text-tertiary)] flex items-center gap-1">
          <Key size={10} />
          Create an API key with "execute" scope in the API Keys tab
        </p>
      </div>
    );
  }

  // For file-based agents, show the expandable full flow
  return (
    <div className="space-y-3">
      {/* Quick trigger example */}
      <div className="relative">
        <pre className="p-3 bg-[var(--color-bg-tertiary)] rounded-lg text-[10px] font-mono overflow-x-auto whitespace-pre-wrap text-[var(--color-text-secondary)]">
          {fileTriggerCmd}
        </pre>
        <button
          onClick={handleCopy}
          className="absolute top-2 right-2 p-1.5 rounded bg-[var(--color-surface)] hover:bg-[var(--color-surface-hover)] transition-colors"
          title="Copy full script"
        >
          {copied ? (
            <Check size={12} className="text-green-400" />
          ) : (
            <Copy size={12} className="text-[var(--color-text-tertiary)]" />
          )}
        </button>
      </div>

      {/* Expandable full flow */}
      <button
        onClick={() => setExpanded(!expanded)}
        className="flex items-center gap-1 text-[10px] text-[var(--color-accent)] hover:underline"
      >
        {expanded ? <ChevronUp size={12} /> : <ChevronDown size={12} />}
        {expanded ? 'Hide' : 'Show'} full workflow (upload → trigger → poll)
      </button>

      {expanded && (
        <div className="space-y-3 p-3 bg-[var(--color-bg-tertiary)] rounded-lg">
          <div>
            <p className="text-[10px] text-[var(--color-text-tertiary)] mb-1">
              <strong>Step 1:</strong> Upload file (requires "upload" scope)
            </p>
            <pre className="text-[9px] font-mono text-[var(--color-text-secondary)] overflow-x-auto whitespace-pre-wrap">
              {uploadCmd}
            </pre>
          </div>

          <div>
            <p className="text-[10px] text-[var(--color-text-tertiary)] mb-1">
              <strong>Step 2:</strong> Trigger workflow with file_id
            </p>
            <pre className="text-[9px] font-mono text-[var(--color-text-secondary)] overflow-x-auto whitespace-pre-wrap">
              {fileTriggerCmd}
            </pre>
          </div>

          <div>
            <p className="text-[10px] text-[var(--color-text-tertiary)] mb-1">
              <strong>Step 3:</strong> Poll for completion
            </p>
            <pre className="text-[9px] font-mono text-[var(--color-text-secondary)] overflow-x-auto whitespace-pre-wrap">
              {statusCmd}
            </pre>
          </div>
        </div>
      )}

      {/* Link to API keys */}
      <p className="text-[10px] text-[var(--color-text-tertiary)] flex items-center gap-1">
        <Key size={10} />
        Create an API key with "upload" + "execute" scopes in the API Keys tab
      </p>
    </div>
  );
}
