import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Copy,
  Check,
  Key,
  Terminal,
  Upload,
  RefreshCw,
  FileJson,
  Code2,
  Sparkles,
  ExternalLink,
} from 'lucide-react';
import { cn } from '@/lib/utils';

interface AgentDocsPanelProps {
  agentId: string;
  agentName?: string;
  agentDescription?: string;
  hasFileInput?: boolean;
}

// Platform definitions for AI prompt generator - ClaraVerse first as featured option
const AI_PLATFORMS = [
  {
    id: 'claraverse',
    name: 'ClaraVerse',
    // ClaraVerse uses Sparkles icon (rendered inline, not favicon)
    favicon: null,
    color: '#8B5CF6',
    description: 'Build instantly with ClaraVerse Chat',
    featured: true,
  },
  {
    id: 'bolt',
    name: 'Bolt.new',
    // Bolt uses a lightning bolt icon - using SimpleIcons CDN
    favicon: 'https://cdn.simpleicons.org/lightning/20B2AA',
    color: '#20B2AA',
    description: 'Full-stack web app generator',
    featured: false,
  },
  {
    id: 'v0',
    name: 'v0.dev',
    // v0 is Vercel's product - using Vercel icon
    favicon: 'https://cdn.simpleicons.org/vercel/white',
    color: '#FFFFFF',
    description: 'Vercel AI UI generator',
    featured: false,
  },
  {
    id: 'replit',
    name: 'Replit Agent',
    // Replit icon from SimpleIcons CDN
    favicon: 'https://cdn.simpleicons.org/replit/F26207',
    color: '#F26207',
    description: 'AI-powered development',
    featured: false,
  },
  {
    id: 'cursor',
    name: 'Cursor',
    // Cursor icon - using a code bracket icon
    favicon: 'https://www.cursor.com/favicon.ico',
    color: '#7C3AED',
    description: 'AI code editor',
    featured: false,
  },
  {
    id: 'lovable',
    name: 'Lovable',
    // Lovable uses a heart icon
    favicon: 'https://lovable.dev/img/background/pulse.webp',
    color: '#EC4899',
    description: 'AI app builder',
    featured: false,
  },
] as const;

type PlatformId = (typeof AI_PLATFORMS)[number]['id'];

export function AgentDocsPanel({
  agentId,
  agentName = 'My Agent',
  agentDescription = '',
  hasFileInput = false,
}: AgentDocsPanelProps) {
  const navigate = useNavigate();
  const [copiedSection, setCopiedSection] = useState<string | null>(null);
  const [selectedPlatform, setSelectedPlatform] = useState<PlatformId | null>(null);

  const baseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:3001';

  // Generate platform-specific prompts
  const generatePrompt = (platformId: PlatformId): string => {
    const agentInfo = `
## Agent Details
- **Name**: ${agentName}
- **Agent ID**: ${agentId}
- **Description**: ${agentDescription || 'An AI-powered agent'}
- **API Base URL**: ${baseUrl}
- **Accepts File Input**: ${hasFileInput ? 'Yes' : 'No'}
`;

    const apiEndpoints = `
## API Endpoints
${
  hasFileInput
    ? `
### 1. Upload File (Required First Step)
**This agent requires file input. Upload a file before triggering the agent.**
\`\`\`
POST ${baseUrl}/api/external/upload
Headers:
  - X-API-Key: YOUR_API_KEY
Body: multipart/form-data with "file" field
Response:
  {
    "file_id": "f_abc123",
    "filename": "document.pdf",
    "mime_type": "application/pdf",
    "size": 12345,
    "expires_at": "2024-01-01T12:30:00Z"
  }
\`\`\`

**Supported file types:** Images (PNG, JPG, WebP), Documents (PDF, DOCX, PPTX), Data (CSV, XLSX, JSON), Audio (MP3, WAV)
**File expiration:** Files expire 30 minutes after upload. Re-upload if expired.

### 2. Trigger Agent Execution
\`\`\`
POST ${baseUrl}/api/trigger/${agentId}
Headers:
  - X-API-Key: YOUR_API_KEY
  - Content-Type: application/json
Body:
  {
    "input": {
      "file_id": "f_abc123",
      "filename": "document.pdf",
      "mime_type": "application/pdf"
    }
  }
Response:
  { "executionId": "exec_xyz789", "status": "running" }
\`\`\`
`
    : `
### 1. Trigger Agent Execution
\`\`\`
POST ${baseUrl}/api/trigger/${agentId}
Headers:
  - X-API-Key: YOUR_API_KEY
  - Content-Type: application/json
Body:
  { "input": { "message": "your input text here" } }
Response:
  { "executionId": "exec_xyz789", "status": "running" }
\`\`\`
`
}
### ${hasFileInput ? '3' : '2'}. Poll for Execution Status
\`\`\`
GET ${baseUrl}/api/trigger/status/{executionId}
Headers:
  - X-API-Key: YOUR_API_KEY
Response:
  {
    "status": "completed" | "running" | "failed",
    "result": "Agent response text...",
    "artifacts": [...],
    "files": [...]
  }
\`\`\`

## Polling Best Practices

**Safe Polling Strategy:**
- Start polling 500ms after triggering
- Poll every **1.5-2 seconds** (recommended)
- Maximum polling duration: **5 minutes** (then show timeout error)
- Stop polling when status is "completed" or "failed"

**Implementation Example:**
\`\`\`typescript
async function pollForResult(executionId: string, maxAttempts = 150) {
  for (let attempt = 0; attempt < maxAttempts; attempt++) {
    const response = await fetch(\`${baseUrl}/api/trigger/status/\${executionId}\`, {
      headers: { 'X-API-Key': API_KEY }
    });
    const data = await response.json();

    if (data.status === 'completed') {
      return { success: true, data };
    }
    if (data.status === 'failed') {
      return { success: false, error: data.error || 'Execution failed' };
    }

    // Wait 2 seconds before next poll
    await new Promise(resolve => setTimeout(resolve, 2000));
  }

  return { success: false, error: 'Timeout: Agent took too long to respond' };
}
\`\`\`

**Status Values:**
- \`running\` - Agent is still processing (keep polling)
- \`completed\` - Success! Result is ready
- \`failed\` - Error occurred (check error field)
- \`partial\` - Partial result available (can display intermediate results)
`;

    const commonRequirements = `
## Requirements
1. Store API key securely (environment variable, not in code)
2. Implement polling with 1-2 second intervals for status checks
3. Handle loading states while agent is processing
4. Display results including any artifacts (charts, images) and generated files
5. Handle errors gracefully with user-friendly messages
`;

    const outputExample = `
## Example Response & Rendering

### Sample API Response
\`\`\`json
{
  "status": "completed",
  "output": {
    "Block Name": {
      "output": {
        "response": "Here is the analysis of your request...\\n\\n**Key Findings:**\\n- Point 1: Lorem ipsum...\\n- Point 2: Data shows...\\n\\nThe conclusion is that..."
      }
    }
  }
}
\`\`\`

**Note:** The \`output\` field contains results keyed by block name. Each block has an \`output.response\` field with the actual content. For agents with multiple blocks, you'll see multiple entries.

### How to Extract and Render the Response

#### 1. Extract Result from Output Structure
\`\`\`tsx
// Get the response from the output structure
function extractResponse(data: any): string {
  if (!data.output) return '';

  // Get the first block's output (or iterate through all blocks)
  const blocks = Object.values(data.output) as any[];
  if (blocks.length === 0) return '';

  // Extract the response text from the block
  return blocks[0]?.output?.response || '';
}

const responseText = extractResponse(data);
\`\`\`

#### 2. Render Result Text (Markdown)
The response contains markdown-formatted text. Render it using a markdown library:
\`\`\`tsx
import ReactMarkdown from 'react-markdown';

<div className="prose dark:prose-invert">
  <ReactMarkdown>{responseText}</ReactMarkdown>
</div>
\`\`\`

#### 3. Loading & Status States
\`\`\`tsx
{status === "running" && (
  <div className="flex items-center gap-2">
    <Spinner className="animate-spin" />
    <span>Processing...</span>
  </div>
)}

{status === "failed" && (
  <div className="text-red-500 p-4 border border-red-200 rounded">
    <AlertIcon /> Error: {response.error || "Execution failed"}
  </div>
)}
\`\`\`
${
  hasFileInput
    ? `
#### 5. File Upload Component
Since this agent requires file input, implement a file upload component:
\`\`\`tsx
function FileUpload({ onFileUploaded }: { onFileUploaded: (fileInfo: FileInfo) => void }) {
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleFileSelect = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;

    // Validate file size (max 10MB)
    if (file.size > 10 * 1024 * 1024) {
      setError('File too large. Maximum size is 10MB.');
      return;
    }

    setUploading(true);
    setError(null);

    try {
      const formData = new FormData();
      formData.append('file', file);

      const response = await fetch(\`\${BASE_URL}/api/external/upload\`, {
        method: 'POST',
        headers: { 'X-API-Key': API_KEY },
        body: formData,
      });

      if (!response.ok) throw new Error('Upload failed');

      const data = await response.json();
      onFileUploaded({
        file_id: data.file_id,
        filename: data.filename,
        mime_type: data.mime_type,
        size: data.size,
      });
    } catch (err) {
      setError('Failed to upload file. Please try again.');
    } finally {
      setUploading(false);
    }
  };

  return (
    <div className="border-2 border-dashed rounded-lg p-6 text-center">
      <input
        type="file"
        onChange={handleFileSelect}
        accept="image/*,.pdf,.docx,.pptx,.csv,.xlsx,.json"
        disabled={uploading}
        className="hidden"
        id="file-upload"
      />
      <label htmlFor="file-upload" className="cursor-pointer">
        {uploading ? (
          <span>Uploading...</span>
        ) : (
          <span>Click to upload or drag and drop</span>
        )}
      </label>
      {error && <p className="text-red-500 mt-2">{error}</p>}
    </div>
  );
}
\`\`\`
`
    : ''
}`;

    switch (platformId) {
      case 'bolt':
        return `Build a modern web application frontend for the "${agentName}" AI agent.

${agentInfo}
${apiEndpoints}
${outputExample}
${commonRequirements}

## UI Requirements for Bolt.new
- Create a clean, modern React application with Tailwind CSS
- Include an input form where users can ${hasFileInput ? 'upload files or ' : ''}enter text
- Show a loading spinner/animation while the agent processes
- Display results in a nicely formatted card with markdown support
- If the response includes artifacts (images/charts), display them inline
- If the response includes downloadable files, show download buttons
- Add a history sidebar to show previous requests
- Use a dark theme with accent colors
- Make it fully responsive for mobile

## Technical Implementation
- Use React with TypeScript
- Use fetch API for requests
- Store API key in .env file (VITE_API_KEY)
- Implement proper error boundaries
- Add toast notifications for success/error states

Start by creating the main chat interface component.`;

      case 'v0':
        return `Create a beautiful UI for the "${agentName}" AI agent integration.

${agentInfo}
${apiEndpoints}
${outputExample}
${commonRequirements}

## UI Requirements for v0.dev
- Design a sleek, minimal interface using shadcn/ui components
- Create an input area with ${hasFileInput ? 'file upload dropzone and ' : ''}text input
- Add a submit button with loading state
- Display results in a clean card with proper typography
- Support markdown rendering in responses
- Show artifacts (images/charts) in a gallery format
- Include file download buttons if files are generated
- Use a modern color scheme (dark mode preferred)
- Ensure accessibility (ARIA labels, keyboard navigation)

## Component Structure
- InputSection: ${hasFileInput ? 'File upload + ' : ''}text input + submit button
- ResultsCard: Formatted response with markdown
- ArtifactsGallery: Display generated images/charts
- FileDownloads: List of downloadable files
- LoadingState: Skeleton/spinner during processing

Focus on creating polished, production-ready components.`;

      case 'replit':
        return `Create a full-stack application for the "${agentName}" AI agent.

${agentInfo}
${apiEndpoints}
${outputExample}
${commonRequirements}

## Requirements for Replit
- Build a complete web application (frontend + backend proxy)
- Frontend: React or vanilla JS with modern CSS
- Backend: Node.js/Express proxy to handle API key securely
- Include ${hasFileInput ? 'file upload functionality and ' : ''}text input
- Implement real-time status polling
- Display formatted results with support for:
  - Text/markdown responses
  - Image artifacts (base64 or URLs)
  - Downloadable files
- Add session persistence to save history
- Include error handling and retry logic

## Project Structure
\`\`\`
/frontend
  - index.html
  - styles.css
  - app.js
/backend
  - server.js (Express proxy)
  - routes/agent.js
.env (API_KEY)
\`\`\`

Create a working prototype that's ready to deploy.`;

      case 'cursor':
        return `Help me build a React application to integrate with the "${agentName}" AI agent.

${agentInfo}
${apiEndpoints}
${outputExample}
${commonRequirements}

## Implementation Guide for Cursor

### Step 1: Set up the project structure
Create a new React + TypeScript project with these key files:
- src/hooks/useAgentAPI.ts - Custom hook for API calls
- src/components/AgentChat.tsx - Main interface component
- src/components/ResultDisplay.tsx - Response renderer
${hasFileInput ? '- src/components/FileUpload.tsx - File upload component' : ''}
- src/lib/api.ts - API client functions

### Step 2: Create the API client
\`\`\`typescript
// src/lib/api.ts
const API_KEY = import.meta.env.VITE_API_KEY;
const BASE_URL = "${baseUrl}";

export async function triggerAgent(input: ${hasFileInput ? '{ file_id: string; filename: string; mime_type: string } | ' : ''}{ message: string }) {
  const response = await fetch(\`\${BASE_URL}/api/trigger/${agentId}\`, {
    method: 'POST',
    headers: {
      'X-API-Key': API_KEY,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ input }),
  });
  return response.json();
}

export async function getStatus(executionId: string) {
  const response = await fetch(\`\${BASE_URL}/api/trigger/status/\${executionId}\`, {
    headers: { 'X-API-Key': API_KEY },
  });
  return response.json();
}
${
  hasFileInput
    ? `
export async function uploadFile(file: File) {
  const formData = new FormData();
  formData.append('file', file);
  const response = await fetch(\`\${BASE_URL}/api/external/upload\`, {
    method: 'POST',
    headers: { 'X-API-Key': API_KEY },
    body: formData,
  });
  return response.json();
}
`
    : ''
}
\`\`\`

### Step 3: Create the main component
Build a chat-like interface with input, loading states, and result display.

### Step 4: Add polling logic
Implement a useEffect or custom hook that polls the status endpoint every 1-2 seconds until completion.

Please help me implement each of these components step by step.`;

      case 'lovable':
        return `Build a beautiful, user-friendly application for the "${agentName}" AI agent.

${agentInfo}
${apiEndpoints}
${outputExample}
${commonRequirements}

## Design Requirements for Lovable
- Create an intuitive, visually appealing interface
- Use smooth animations and transitions
- Include ${hasFileInput ? 'drag-and-drop file upload with ' : ''}a clean input form
- Show engaging loading animations while processing
- Display results with:
  - Beautiful typography for text responses
  - Image gallery for artifacts
  - Styled download cards for files
- Add subtle micro-interactions
- Implement a conversation history panel
- Support both light and dark themes

## User Experience Flow
1. User lands on clean homepage with input area
2. User ${hasFileInput ? 'uploads a file or ' : ''}types their request
3. Animated loading state shows progress
4. Results appear with smooth fade-in animation
5. User can copy, download, or share results
6. Previous conversations accessible in sidebar

## Technical Stack
- React with TypeScript
- Framer Motion for animations
- Tailwind CSS for styling
- React Query for API state management

Focus on creating a delightful user experience that makes the AI agent easy and enjoyable to use.`;

      case 'claraverse': {
        const apiKeyToUse = 'YOUR_API_KEY_HERE';
        const apiKeyNote = `**API Key:** Copy your API key from the **API Keys** tab and replace \`YOUR_API_KEY_HERE\` in the code below.`;

        return `Build a standalone HTML frontend for the "${agentName}" AI agent using Tailwind CSS via CDN.

${agentInfo}
${apiKeyNote}
${apiEndpoints}
${outputExample}
${commonRequirements}

## IMPORTANT: Output Format for ClaraVerse

**Please provide the complete HTML code in a single code block.** Use \`\`\`html to start the code block and include the entire file content so I can copy it easily.

## Requirements for ClaraVerse (Standalone HTML)

Create a **single HTML file** that:
- Uses Tailwind CSS via CDN (\`<script src="https://cdn.tailwindcss.com"></script>\`)
- Works without any build tools or npm - just open in browser
- Is self-contained with all JavaScript inline
- Has a modern, clean design with dark mode
- **Pre-configure the API key:** \`${apiKeyToUse}\`

## HTML Structure
\`\`\`html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>${agentName} - AI Agent</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <script>
    tailwind.config = {
      darkMode: 'class',
      theme: { extend: {} }
    }
  </script>
</head>
<body class="dark bg-gray-900 min-h-screen">
  <!-- Your UI here -->
  <script>
    // API Configuration - IMPORTANT: Replace with your actual API key
    const API_KEY = '${apiKeyToUse}';
    const BASE_URL = '${baseUrl}';
    const AGENT_ID = '${agentId}';

    // Your JavaScript here
  </script>
</body>
</html>
\`\`\`

## UI Features to Include
- Clean input area for ${hasFileInput ? 'file upload and ' : ''}text input
- Submit button with loading spinner while processing
- Results display area with:
  - Formatted text/markdown responses
  - Image display for artifacts (base64)
  - Download buttons for generated files
- Error handling with user-friendly messages
- Responsive design (mobile-friendly)
- Dark theme with accent colors

## JavaScript Implementation
- Use fetch API for all requests
- Implement polling for status (every 2 seconds)
- The API key is already configured at the top: \`${apiKeyToUse}\`
- Add proper error handling and loading states

## Note
Wrap the complete HTML code in a single \`\`\`html code block. The code should be complete and ready to save as an .html file and open in a browser.`;
      }

      default:
        return '';
    }
  };

  const handleCopy = (text: string, section: string) => {
    navigator.clipboard.writeText(text);
    setCopiedSection(section);
    setTimeout(() => setCopiedSection(null), 2000);
  };

  // API Examples
  const triggerCmd = `curl -X POST ${baseUrl}/api/trigger/${agentId} \\
  -H "X-API-Key: YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"input": {"message": "your input here"}}'`;

  const triggerWithFileCmd = `curl -X POST ${baseUrl}/api/trigger/${agentId} \\
  -H "X-API-Key: YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "input": {
      "file_id": "FILE_ID_FROM_UPLOAD",
      "filename": "document.pdf",
      "mime_type": "application/pdf"
    }
  }'`;

  const uploadCmd = `curl -X POST ${baseUrl}/api/external/upload \\
  -H "X-API-Key: YOUR_API_KEY" \\
  -F "file=@your-file.png"`;

  const statusCmd = `curl ${baseUrl}/api/trigger/status/EXECUTION_ID \\
  -H "X-API-Key: YOUR_API_KEY"`;

  const responseExample = `{
  "status": "completed",
  "output": {
    "Content Generator": {
      "output": {
        "response": "Your agent's response text..."
      }
    }
  }
}

// Note: The "output" field contains results keyed by block name.
// Each block has an "output.response" field with the actual content.`;

  const pythonExample = `import requests
import time

API_KEY = "your_api_key_here"
BASE_URL = "${baseUrl}"

# Trigger the agent
response = requests.post(
    f"{BASE_URL}/api/trigger/${agentId}",
    headers={
        "X-API-Key": API_KEY,
        "Content-Type": "application/json"
    },
    json={"input": {"message": "your input here"}}
)

data = response.json()
execution_id = data.get("executionId")

# Poll for results
while True:
    result = requests.get(
        f"{BASE_URL}/api/trigger/status/{execution_id}",
        headers={"X-API-Key": API_KEY}
    ).json()

    if result["status"] in ["completed", "failed"]:
        # Extract response from output structure
        output = result.get("output", {})
        for block_name, block_data in output.items():
            response_text = block_data.get("output", {}).get("response", "")
            print(f"{block_name}: {response_text}")
        break

    time.sleep(2)`;

  const jsExample = `const API_KEY = "your_api_key_here";
const BASE_URL = "${baseUrl}";

// Trigger the agent
const response = await fetch(\`\${BASE_URL}/api/trigger/${agentId}\`, {
  method: "POST",
  headers: {
    "X-API-Key": API_KEY,
    "Content-Type": "application/json"
  },
  body: JSON.stringify({ input: { message: "your input here" } })
});

const { executionId } = await response.json();

// Poll for results
const pollStatus = async () => {
  const data = await fetch(
    \`\${BASE_URL}/api/trigger/status/\${executionId}\`,
    { headers: { "X-API-Key": API_KEY } }
  ).then(r => r.json());

  if (data.status === "completed" || data.status === "failed") {
    return data;
  }

  await new Promise(r => setTimeout(r, 2000));
  return pollStatus();
};

const result = await pollStatus();

// Extract response from output structure
const output = result.output || {};
for (const [blockName, blockData] of Object.entries(output)) {
  const responseText = blockData?.output?.response || "";
  console.log(\`\${blockName}: \${responseText}\`);
}`;

  return (
    <div className="p-6 space-y-6">
      {/* Header */}
      <div>
        <h2 className="text-lg font-semibold text-[var(--color-text-primary)] mb-1">
          API Documentation
        </h2>
        <p className="text-sm text-[var(--color-text-secondary)]">
          Integrate this agent into your applications using the REST API.
        </p>
      </div>

      {/* AI Prompt Generator */}
      <Section title="Build UI with AI" icon={<Sparkles size={16} />}>
        <p className="text-xs text-[var(--color-text-secondary)] mb-4">
          Generate ready-to-use prompts for popular AI coding platforms to build a frontend for this
          agent.
        </p>

        {/* Platform Icon Buttons - click to copy prompt */}
        <div className="flex flex-wrap gap-2 mb-2">
          {AI_PLATFORMS.map(platform => {
            const isCopied = copiedSection === `prompt-${platform.id}`;
            return (
              <button
                key={platform.id}
                onClick={() => {
                  const prompt = generatePrompt(platform.id);
                  handleCopy(prompt, `prompt-${platform.id}`);
                  setSelectedPlatform(platform.id);
                }}
                className={cn(
                  'flex items-center gap-2 rounded-lg border transition-all',
                  platform.featured
                    ? 'px-4 py-2.5 shadow-lg shadow-purple-500/25 hover:shadow-purple-500/40'
                    : 'px-3 py-2',
                  isCopied
                    ? 'border-green-500 bg-green-500/20 ring-2 ring-green-500/30'
                    : platform.featured
                      ? 'border-purple-500/50 bg-purple-500/10 hover:border-purple-500 hover:bg-purple-500/20'
                      : 'border-[var(--color-border)] hover:border-[var(--color-accent)] hover:bg-[var(--color-surface)]'
                )}
                title={`Copy prompt for ${platform.name}`}
              >
                {isCopied ? (
                  <Check size={platform.featured ? 18 : 16} className="text-green-400" />
                ) : platform.id === 'claraverse' ? (
                  <Sparkles size={platform.featured ? 18 : 16} className="text-purple-500" />
                ) : (
                  platform.favicon && (
                    <img
                      src={platform.favicon}
                      alt={platform.name}
                      className="w-4 h-4 object-contain"
                      onError={e => {
                        (e.target as HTMLImageElement).style.display = 'none';
                      }}
                    />
                  )
                )}
                <span
                  className={cn(
                    'font-medium transition-colors',
                    platform.featured ? 'text-sm' : 'text-xs',
                    isCopied
                      ? 'text-green-400'
                      : platform.featured
                        ? 'text-purple-400'
                        : 'text-[var(--color-text-secondary)]'
                  )}
                >
                  {isCopied ? 'Copied!' : platform.name}
                </span>
                {platform.featured && !isCopied && (
                  <span className="ml-1 px-1.5 py-0.5 text-[10px] font-semibold bg-purple-500/30 text-purple-300 rounded">
                    Recommended
                  </span>
                )}
              </button>
            );
          })}
        </div>

        {/* Success message after copying */}
        {selectedPlatform && copiedSection === `prompt-${selectedPlatform}` && (
          <div className="flex items-center gap-2 p-3 bg-green-500/10 border border-green-500/30 rounded-lg">
            <Check size={16} className="text-green-400 flex-shrink-0" />
            <p className="text-xs text-green-400">
              Prompt copied! Paste it into{' '}
              <strong>{AI_PLATFORMS.find(p => p.id === selectedPlatform)?.name}</strong> to generate
              a frontend for this agent.
              {selectedPlatform !== 'claraverse' && (
                <span className="text-[var(--color-text-tertiary)]">
                  {' '}
                  Don't forget to add your API key from the <strong>API Keys</strong> tab.
                </span>
              )}
            </p>
          </div>
        )}

        {/* ClaraVerse special action - Generate Now button */}
        {selectedPlatform === 'claraverse' && (
          <div className="mt-3 pt-3 border-t border-[var(--color-border)]">
            <button
              onClick={() => {
                const prompt = generatePrompt('claraverse');
                navigate(`/chat?prompt=${encodeURIComponent(prompt)}`);
              }}
              className="flex items-center justify-center gap-2 w-full px-4 py-2.5 rounded-lg text-sm font-medium transition-all bg-purple-500/20 text-purple-300 hover:bg-purple-500/30 border border-purple-500/50"
            >
              <ExternalLink size={16} />
              Or Generate with ClaraVerse Chat
            </button>
          </div>
        )}
      </Section>

      {/* Quick Start */}
      <Section title="Quick Start" icon={<Terminal size={16} />}>
        <div className="space-y-4">
          <div>
            <StepLabel step={1} label="Create an API Key" />
            <p className="text-xs text-[var(--color-text-secondary)] mb-2">
              Go to the <strong>API Keys</strong> tab and create a key with "execute" scope.
            </p>
          </div>

          <div>
            <StepLabel step={2} label="Trigger the Agent" />
            <CodeBlock
              code={triggerCmd}
              onCopy={() => handleCopy(triggerCmd, 'trigger')}
              copied={copiedSection === 'trigger'}
            />
          </div>

          <div>
            <StepLabel step={3} label="Check Execution Status" />
            <p className="text-xs text-[var(--color-text-secondary)] mb-2">
              Use the{' '}
              <code className="px-1 py-0.5 bg-[var(--color-surface)] rounded text-[10px]">
                executionId
              </code>{' '}
              from the trigger response:
            </p>
            <CodeBlock
              code={statusCmd}
              onCopy={() => handleCopy(statusCmd, 'status')}
              copied={copiedSection === 'status'}
            />
          </div>
        </div>
      </Section>

      {/* File Upload (if agent uses files) */}
      {hasFileInput && (
        <Section title="File Upload" icon={<Upload size={16} />}>
          <p className="text-xs text-[var(--color-text-secondary)] mb-3">
            This agent accepts file inputs. Upload files first, then trigger with the file_id.
          </p>

          <div className="space-y-4">
            <div>
              <StepLabel step={1} label="Upload File" />
              <p className="text-xs text-[var(--color-text-tertiary)] mb-2">
                Requires API key with "upload" scope.
              </p>
              <CodeBlock
                code={uploadCmd}
                onCopy={() => handleCopy(uploadCmd, 'upload')}
                copied={copiedSection === 'upload'}
              />
            </div>

            <div>
              <StepLabel step={2} label="Trigger with File" />
              <CodeBlock
                code={triggerWithFileCmd}
                onCopy={() => handleCopy(triggerWithFileCmd, 'trigger-file')}
                copied={copiedSection === 'trigger-file'}
              />
            </div>
          </div>
        </Section>
      )}

      {/* Response Format */}
      <Section title="Response Format" icon={<FileJson size={16} />}>
        <p className="text-xs text-[var(--color-text-secondary)] mb-3">
          Successful executions return a structured response with block outputs keyed by block name.
        </p>
        <CodeBlock
          code={responseExample}
          language="json"
          onCopy={() => handleCopy(responseExample, 'response')}
          copied={copiedSection === 'response'}
        />

        <div className="mt-4 space-y-2">
          <ResponseField
            name="status"
            type="string"
            description="completed | failed | running | partial"
          />
          <ResponseField
            name="output"
            type="object"
            description="Block outputs keyed by block name (e.g., 'Content Generator')"
          />
          <ResponseField
            name="output.[block].output.response"
            type="string"
            description="The text response from each block"
          />
          <ResponseField
            name="error"
            type="string"
            description="Error message if status is 'failed'"
          />
        </div>
      </Section>

      {/* Code Examples */}
      <Section title="Code Examples" icon={<Code2 size={16} />}>
        <div className="space-y-4">
          <div>
            <p className="text-xs font-medium text-[var(--color-text-secondary)] mb-2">Python</p>
            <CodeBlock
              code={pythonExample}
              language="python"
              onCopy={() => handleCopy(pythonExample, 'python')}
              copied={copiedSection === 'python'}
            />
          </div>

          <div>
            <p className="text-xs font-medium text-[var(--color-text-secondary)] mb-2">
              JavaScript / Node.js
            </p>
            <CodeBlock
              code={jsExample}
              language="javascript"
              onCopy={() => handleCopy(jsExample, 'js')}
              copied={copiedSection === 'js'}
            />
          </div>
        </div>
      </Section>

      {/* API Key Scopes */}
      <Section title="API Key Scopes" icon={<Key size={16} />}>
        <div className="space-y-2">
          <ScopeItem
            scope="execute"
            description="Required to trigger agent executions and check status"
          />
          <ScopeItem scope="upload" description="Required to upload files for file-based agents" />
          <ScopeItem
            scope="read"
            description="Required to read execution history and agent details"
          />
        </div>
      </Section>

      {/* Rate Limits */}
      <Section title="Rate Limits & Best Practices" icon={<RefreshCw size={16} />}>
        <ul className="space-y-2 text-xs text-[var(--color-text-secondary)]">
          <li className="flex items-start gap-2">
            <span className="text-[var(--color-accent)]">•</span>
            <span>Poll status endpoint every 1-2 seconds, not more frequently</span>
          </li>
          <li className="flex items-start gap-2">
            <span className="text-[var(--color-accent)]">•</span>
            <span>Set reasonable timeouts (agents may take 30s+ for complex tasks)</span>
          </li>
          <li className="flex items-start gap-2">
            <span className="text-[var(--color-accent)]">•</span>
            <span>Store API keys securely, never expose in client-side code</span>
          </li>
          <li className="flex items-start gap-2">
            <span className="text-[var(--color-accent)]">•</span>
            <span>Use webhook outputs for real-time notifications instead of polling</span>
          </li>
        </ul>
      </Section>
    </div>
  );
}

// Helper Components
function Section({
  title,
  icon,
  children,
}: {
  title: string;
  icon: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="border border-[var(--color-border)] rounded-xl overflow-hidden">
      <div className="px-4 py-3 bg-[var(--color-surface)] border-b border-[var(--color-border)] flex items-center gap-2">
        <span className="text-[var(--color-accent)]">{icon}</span>
        <h3 className="text-sm font-medium text-[var(--color-text-primary)]">{title}</h3>
      </div>
      <div className="p-4">{children}</div>
    </div>
  );
}

function StepLabel({ step, label }: { step: number; label: string }) {
  return (
    <p className="text-xs font-medium text-[var(--color-text-secondary)] mb-2 flex items-center gap-2">
      <span className="w-5 h-5 rounded-full bg-[var(--color-accent)] text-white text-[10px] flex items-center justify-center font-bold">
        {step}
      </span>
      {label}
    </p>
  );
}

function CodeBlock({
  code,
  language: _language = 'bash',
  onCopy,
  copied,
}: {
  code: string;
  language?: string;
  onCopy: () => void;
  copied: boolean;
}) {
  return (
    <div className="relative group">
      <pre
        className={cn(
          'p-3 bg-[var(--color-bg-tertiary)] rounded-lg text-[11px] font-mono overflow-x-auto',
          'text-[var(--color-text-secondary)] leading-relaxed'
        )}
      >
        {code}
      </pre>
      <button
        onClick={onCopy}
        className={cn(
          'absolute top-2 right-2 p-1.5 rounded transition-all',
          'bg-[var(--color-surface)] hover:bg-[var(--color-surface-hover)]',
          'opacity-0 group-hover:opacity-100'
        )}
        title="Copy to clipboard"
      >
        {copied ? (
          <Check size={12} className="text-[var(--color-accent)]" />
        ) : (
          <Copy size={12} className="text-[var(--color-text-tertiary)]" />
        )}
      </button>
    </div>
  );
}

function ResponseField({
  name,
  type,
  description,
}: {
  name: string;
  type: string;
  description: string;
}) {
  return (
    <div className="flex items-start gap-2 text-xs">
      <code className="px-1.5 py-0.5 bg-[var(--color-surface)] rounded text-[var(--color-accent)] font-mono">
        {name}
      </code>
      <span className="text-[var(--color-text-tertiary)]">{type}</span>
      <span className="text-[var(--color-text-secondary)]">— {description}</span>
    </div>
  );
}

function ScopeItem({ scope, description }: { scope: string; description: string }) {
  return (
    <div className="flex items-start gap-3 p-2 rounded-lg bg-[var(--color-surface)]">
      <code className="px-2 py-0.5 bg-[var(--color-bg-tertiary)] rounded text-[10px] font-mono text-[var(--color-accent)]">
        {scope}
      </code>
      <span className="text-xs text-[var(--color-text-secondary)]">{description}</span>
    </div>
  );
}
