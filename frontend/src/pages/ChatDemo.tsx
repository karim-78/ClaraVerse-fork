import { useState } from 'react';
import {
  ChatBubble,
  ChatInput,
  CodeBlock,
  MarkdownRenderer,
  TypingIndicator,
  MessageActions,
  StreamingText,
  Button,
  Badge,
  Modal,
  Toast,
  Tabs,
  Typography,
  Card,
} from '@/components/design-system';
import type { Tab } from '@/components/design-system';
import './ChatDemo.css';

interface Message {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  timestamp: string;
}

export const ChatDemo = () => {
  const [messages, setMessages] = useState<Message[]>([
    {
      id: '1',
      role: 'assistant',
      content:
        "Hello! I'm your AI assistant. I can help you with code, markdown formatting, and more. Try asking me something!",
      timestamp: '10:00 AM',
    },
  ]);
  const [isTyping, setIsTyping] = useState(false);
  const [showModal, setShowModal] = useState(false);
  const [showToast, setShowToast] = useState(false);
  const [activeTab, setActiveTab] = useState('chat');
  const [isStreaming, setIsStreaming] = useState(false);

  const tabs: Tab[] = [
    { id: 'chat', label: 'Chat', icon: '💬' },
    { id: 'components', label: 'Components', icon: '🧩' },
    { id: 'examples', label: 'Examples', icon: '📝' },
  ];

  const handleSendMessage = (message: string) => {
    const userMessage: Message = {
      id: Date.now().toString(),
      role: 'user',
      content: message,
      timestamp: new Date().toLocaleTimeString('en-US', {
        hour: '2-digit',
        minute: '2-digit',
      }),
    };

    setMessages(prev => [...prev, userMessage]);
    setIsTyping(true);

    // Simulate AI response
    setTimeout(() => {
      setIsTyping(false);
      setIsStreaming(true);

      const aiMessage: Message = {
        id: (Date.now() + 1).toString(),
        role: 'assistant',
        content: getAIResponse(message),
        timestamp: new Date().toLocaleTimeString('en-US', {
          hour: '2-digit',
          minute: '2-digit',
        }),
      };

      setMessages(prev => [...prev, aiMessage]);
    }, 2000);
  };

  const getAIResponse = (userMessage: string): string => {
    if (userMessage.toLowerCase().includes('code')) {
      return 'Here\'s a simple example:\n\n```typescript\nfunction greet(name: string): string {\n  return `Hello, ${name}!`;\n}\n\nconsole.log(greet("World"));\n```\n\nThis TypeScript function demonstrates basic syntax with type annotations.';
    }
    if (userMessage.toLowerCase().includes('markdown')) {
      return '# Markdown Features\n\n**Bold text**, *italic text*, and ***bold italic***.\n\n- Bullet point 1\n- Bullet point 2\n  - Nested item\n\n1. Numbered list\n2. Another item\n\n[Links work too](https://example.com)\n\n> Blockquotes for emphasis\n\nInline `code` is also supported!';
    }
    return `I received your message: "${userMessage}". I can help with code examples, markdown formatting, and more. Try asking about "code" or "markdown"!`;
  };

  const handleCopyMessage = (content: string) => {
    navigator.clipboard.writeText(content);
    setShowToast(true);
  };

  const exampleCode = `function fibonacci(n: number): number {
  if (n <= 1) return n;
  return fibonacci(n - 1) + fibonacci(n - 2);
}

console.log(fibonacci(10)); // 55`;

  const exampleMarkdown = `# AI Chat Components

This design system includes **everything** you need for a production AI chat app:

- Chat bubbles with user/AI variants
- Code blocks with syntax highlighting
- Markdown rendering
- Typing indicators
- Message actions
- Streaming text animations

All components follow the **Rose Pink** design system with a premium dark theme.`;

  return (
    <div className="chat-demo">
      {/* Header */}
      <div className="chat-demo-header">
        <div>
          <Typography variant="h3">DobbyAI Chat</Typography>
          <Typography variant="sm" className="chat-demo-subtitle">
            Complete AI Chat Design System
          </Typography>
        </div>
        <div className="chat-demo-header-actions">
          <Badge variant="success" dot>
            Online
          </Badge>
          <Button variant="secondary" size="sm" onClick={() => setShowModal(true)}>
            Settings
          </Button>
        </div>
      </div>

      {/* Tabs */}
      <Tabs tabs={tabs} activeTab={activeTab} onChange={setActiveTab} />

      {/* Content */}
      <div className="chat-demo-content">
        {activeTab === 'chat' && (
          <>
            <div className="chat-demo-messages">
              {messages.map(msg => (
                <ChatBubble
                  key={msg.id}
                  role={msg.role}
                  content={
                    msg.role === 'assistant' &&
                    isStreaming &&
                    msg.id === messages[messages.length - 1]?.id ? (
                      <StreamingText
                        text={msg.content}
                        speed={20}
                        onComplete={() => setIsStreaming(false)}
                      />
                    ) : msg.content.includes('```') || msg.content.includes('#') ? (
                      <MarkdownRenderer content={msg.content} />
                    ) : (
                      msg.content
                    )
                  }
                  timestamp={msg.timestamp}
                  actions={
                    msg.role === 'assistant' ? (
                      <MessageActions
                        onCopy={() => handleCopyMessage(msg.content)}
                        onRegenerate={() => console.log('Regenerate')}
                      />
                    ) : undefined
                  }
                />
              ))}
              {isTyping && <TypingIndicator />}
            </div>
            <div className="chat-demo-input">
              <ChatInput onSubmit={handleSendMessage} placeholder="Ask me anything..." />
            </div>
          </>
        )}

        {activeTab === 'components' && (
          <div className="chat-demo-showcase">
            <Card variant="glass">
              <Typography variant="h5">Available Components</Typography>
              <div className="component-list">
                <Badge variant="accent">ChatBubble</Badge>
                <Badge variant="accent">ChatInput</Badge>
                <Badge variant="accent">CodeBlock</Badge>
                <Badge variant="accent">MarkdownRenderer</Badge>
                <Badge variant="accent">TypingIndicator</Badge>
                <Badge variant="accent">MessageActions</Badge>
                <Badge variant="accent">StreamingText</Badge>
                <Badge variant="accent">Modal</Badge>
                <Badge variant="accent">Toast</Badge>
                <Badge variant="accent">Tabs</Badge>
              </div>
            </Card>

            <Card variant="feature">
              <Typography variant="h6">Code Block Example</Typography>
              <CodeBlock code={exampleCode} language="typescript" />
            </Card>

            <Card variant="feature">
              <Typography variant="h6">Markdown Rendering</Typography>
              <MarkdownRenderer content={exampleMarkdown} />
            </Card>
          </div>
        )}

        {activeTab === 'examples' && (
          <div className="chat-demo-showcase">
            <Card variant="glass">
              <Typography variant="h5">Try These Examples</Typography>
              <div className="example-buttons">
                <Button
                  variant="secondary"
                  onClick={() => handleSendMessage('Show me a code example')}
                >
                  Request Code Example
                </Button>
                <Button
                  variant="secondary"
                  onClick={() => handleSendMessage('Show me markdown features')}
                >
                  Request Markdown Demo
                </Button>
                <Button variant="secondary" onClick={() => setShowToast(true)}>
                  Show Toast Notification
                </Button>
              </div>
            </Card>

            <Card variant="feature" icon="✨">
              <Typography variant="h6">Streaming Text Demo</Typography>
              <StreamingText
                text="This text appears character by character with a blinking cursor, perfect for simulating AI responses in real-time!"
                speed={40}
              />
            </Card>

            <Card variant="feature" icon="⌨️">
              <Typography variant="h6">Typing Indicator</Typography>
              <TypingIndicator text="AI is thinking" />
            </Card>
          </div>
        )}
      </div>

      {/* Modal */}
      <Modal
        isOpen={showModal}
        onClose={() => setShowModal(false)}
        title="Settings"
        footer={
          <>
            <Button variant="secondary" onClick={() => setShowModal(false)}>
              Cancel
            </Button>
            <Button variant="primary" onClick={() => setShowModal(false)}>
              Save Changes
            </Button>
          </>
        }
      >
        <Typography variant="base">
          Configure your AI chat settings here. This is a demo modal showcasing the Modal component.
        </Typography>
        <div style={{ marginTop: 'var(--space-6)' }}>
          <Badge variant="info">Demo Feature</Badge>
        </div>
      </Modal>

      {/* Toast */}
      {showToast && (
        <div className="toast-container">
          <Toast
            variant="success"
            title="Success!"
            message="Message copied to clipboard"
            onClose={() => setShowToast(false)}
          />
        </div>
      )}
    </div>
  );
};
