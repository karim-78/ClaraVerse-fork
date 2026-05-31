import { useState } from 'react';
import {
  Button,
  Card,
  Input,
  Textarea,
  Badge,
  Progress,
  Skeleton,
  Typography,
  ChatBubble,
  ChatInput,
  TypingIndicator,
  MessageActions,
  StreamingText,
  CodeBlock,
  MarkdownRenderer,
  Modal,
  Toast,
  Tabs,
  Select,
  Checkbox,
  RadioGroup,
  Switch,
  SearchInput,
  Slider,
  FileUpload,
  Tooltip,
  Spinner,
  DropdownMenu,
  Avatar,
  Divider,
  Alert,
  Accordion,
  Breadcrumb,
  Pagination,
  EmptyState,
} from '@/components/design-system';
import type { Tab } from '@/components/design-system';
import './DesignSystem.css';

export const DesignSystem = () => {
  const [inputValue, setInputValue] = useState('');
  const [textareaValue, setTextareaValue] = useState('');
  const [hasInputError] = useState(false);
  const [progress] = useState(65);
  const [isLoading, setIsLoading] = useState(false);
  const [showModal, setShowModal] = useState(false);
  const [showToast, setShowToast] = useState(false);
  const [activeTab, setActiveTab] = useState('tab1');

  // New component states
  const [selectValue, setSelectValue] = useState('');
  const [checkboxChecked, setCheckboxChecked] = useState(false);
  const [radioValue, setRadioValue] = useState('option1');
  const [switchChecked, setSwitchChecked] = useState(false);
  const [searchValue, setSearchValue] = useState('');
  const [sliderValue, setSliderValue] = useState(50);
  const [currentPage, setCurrentPage] = useState(1);

  const handleLoadingClick = () => {
    setIsLoading(true);
    setTimeout(() => setIsLoading(false), 2000);
  };

  const tabs: Tab[] = [
    { id: 'tab1', label: 'Tab 1', icon: '🎨' },
    { id: 'tab2', label: 'Tab 2', icon: '🚀' },
    { id: 'tab3', label: 'Tab 3', icon: '✨' },
  ];

  return (
    <div className="design-system">
      {/* Hero Section */}
      <section className="ds-section ds-hero">
        <Typography variant="display" gradient align="center">
          ClaraVerse Design System
        </Typography>
        <Typography variant="lg" align="center" className="ds-hero-subtitle">
          Premium Dark Theme with Rose Pink Accent
        </Typography>
        <div className="ds-hero-badges">
          <Badge variant="accent" dot>
            v1.0.0
          </Badge>
          <Badge variant="success">Production Ready</Badge>
          <Badge variant="info">TypeScript</Badge>
        </div>
      </section>

      {/* Typography Section */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Typography
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Fluid, responsive typography system with Apple-inspired design
        </Typography>

        <div className="ds-showcase-grid">
          <Card variant="glass">
            <Typography variant="display" gradient>
              Display
            </Typography>
            <Typography variant="h1">Heading 1</Typography>
            <Typography variant="h2">Heading 2</Typography>
            <Typography variant="h3">Heading 3</Typography>
            <Typography variant="h4">Heading 4</Typography>
            <Typography variant="h5">Heading 5</Typography>
            <Typography variant="h6">Heading 6</Typography>
          </Card>

          <Card variant="glass">
            <Typography variant="xl">Extra Large Text</Typography>
            <Typography variant="lg">Large Text</Typography>
            <Typography variant="base">Base Text (Default)</Typography>
            <Typography variant="sm">Small Text</Typography>
            <Typography variant="xs">Extra Small Text</Typography>
            <div style={{ marginTop: '1rem' }}>
              <Typography variant="base" weight="bold">
                Font Weights:
              </Typography>
              <Typography variant="base" weight="light">
                Light
              </Typography>
              <Typography variant="base" weight="normal">
                Normal
              </Typography>
              <Typography variant="base" weight="medium">
                Medium
              </Typography>
              <Typography variant="base" weight="semibold">
                Semibold
              </Typography>
              <Typography variant="base" weight="bold">
                Bold
              </Typography>
            </div>
          </Card>
        </div>
      </section>

      {/* Buttons Section */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Buttons
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Interactive buttons with smooth animations and multiple variants
        </Typography>

        <div className="ds-showcase-grid">
          <Card variant="feature" title="Button Variants">
            <div className="ds-button-group">
              <Button variant="primary">Primary Button</Button>
              <Button variant="secondary">Secondary Button</Button>
              <Button variant="ghost">Ghost Button</Button>
            </div>
          </Card>

          <Card variant="feature" title="Button Sizes">
            <div className="ds-button-group">
              <Button size="sm">Small</Button>
              <Button size="md">Medium</Button>
              <Button size="lg">Large</Button>
              <Button size="xl">Extra Large</Button>
            </div>
          </Card>

          <Card variant="feature" title="Button States">
            <div className="ds-button-group">
              <Button variant="primary" onClick={handleLoadingClick} loading={isLoading}>
                {isLoading ? 'Loading...' : 'Click Me'}
              </Button>
              <Button variant="primary" disabled>
                Disabled
              </Button>
              <Button variant="secondary" fullWidth>
                Full Width
              </Button>
            </div>
          </Card>
        </div>
      </section>

      {/* Cards Section */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Cards
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Glassmorphic cards with elegant hover effects
        </Typography>

        <div className="ds-showcase-grid">
          <Card
            variant="glass"
            icon="🚀"
            title="Glass Card"
            description="Glassmorphism effect with backdrop blur and subtle hover animation"
          />

          <Card
            variant="feature"
            icon="✨"
            title="Feature Card"
            description="Perfect for showcasing features with gradient shine effect on hover"
          />

          <Card
            variant="widget"
            icon="📊"
            title="Widget Card"
            description="Compact card for dashboard widgets and small components"
          />
        </div>
      </section>

      {/* Inputs Section */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Inputs & Forms
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Form inputs with focus states and validation
        </Typography>

        <div className="ds-showcase-grid">
          <Card variant="glass">
            <Input
              label="Email Address"
              type="email"
              placeholder="you@example.com"
              value={inputValue}
              onChange={e => setInputValue(e.target.value)}
              helperText="We'll never share your email"
            />
            <Input
              label="Password"
              type="password"
              placeholder="Enter your password"
              error={hasInputError ? 'Password is required' : undefined}
            />
            <Input label="Disabled Input" placeholder="Disabled" disabled />
          </Card>

          <Card variant="glass">
            <Textarea
              label="Message"
              placeholder="Enter your message here..."
              value={textareaValue}
              onChange={e => setTextareaValue(e.target.value)}
              helperText="Maximum 500 characters"
            />
          </Card>
        </div>
      </section>

      {/* New Form Controls Section */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Form Controls
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Advanced form components including selects, checkboxes, radios, and switches
        </Typography>

        <div className="ds-showcase-grid">
          <Card variant="glass">
            <Typography variant="h6">Select Dropdown</Typography>
            <Select
              label="Country"
              options={[
                { value: 'us', label: 'United States' },
                { value: 'uk', label: 'United Kingdom' },
                { value: 'ca', label: 'Canada' },
                { value: 'au', label: 'Australia' },
              ]}
              value={selectValue}
              onChange={setSelectValue}
              placeholder="Select a country..."
            />
            <Select
              label="Disabled Select"
              options={[{ value: 'disabled', label: 'Disabled' }]}
              disabled
            />
          </Card>

          <Card variant="glass">
            <Typography variant="h6">Checkboxes</Typography>
            <Checkbox
              label="Accept terms and conditions"
              checked={checkboxChecked}
              onChange={setCheckboxChecked}
            />
            <Checkbox label="Subscribe to newsletter" defaultChecked />
            <Checkbox label="Disabled checkbox" disabled />
            <Checkbox label="Indeterminate state" indeterminate />
          </Card>

          <Card variant="glass">
            <Typography variant="h6">Radio Buttons</Typography>
            <RadioGroup
              name="options"
              label="Select an option"
              options={[
                { value: 'option1', label: 'Option 1' },
                { value: 'option2', label: 'Option 2' },
                { value: 'option3', label: 'Option 3' },
              ]}
              value={radioValue}
              onChange={setRadioValue}
            />
          </Card>

          <Card variant="glass">
            <Typography variant="h6">Switch Toggle</Typography>
            <Switch
              label="Enable notifications"
              checked={switchChecked}
              onChange={setSwitchChecked}
            />
            <Switch label="Small size" size="sm" defaultChecked />
            <Switch label="Large size" size="lg" />
            <Switch label="Disabled" disabled />
          </Card>
        </div>
      </section>

      {/* Search & Input Components */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Search & Advanced Inputs
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Specialized input components for search, range selection, and file uploads
        </Typography>

        <div className="ds-showcase-grid">
          <Card variant="glass">
            <Typography variant="h6">Search Input</Typography>
            <SearchInput
              value={searchValue}
              onChange={setSearchValue}
              placeholder="Search..."
              onSearch={value => console.log('Search:', value)}
            />
          </Card>

          <Card variant="glass">
            <Typography variant="h6">Slider / Range</Typography>
            <Slider
              label="Volume"
              value={sliderValue}
              onChange={setSliderValue}
              min={0}
              max={100}
              showValue
            />
          </Card>

          <Card variant="glass" style={{ gridColumn: '1 / -1' }}>
            <Typography variant="h6">File Upload</Typography>
            <FileUpload
              accept="image/*,.pdf"
              multiple
              maxSize={5 * 1024 * 1024}
              onUpload={files => console.log('Files:', files)}
            />
          </Card>
        </div>
      </section>

      {/* UI Components Section */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          UI Components
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Essential UI elements including tooltips, spinners, avatars, and more
        </Typography>

        <div className="ds-showcase-grid">
          <Card variant="glass">
            <Typography variant="h6">Tooltips</Typography>
            <div style={{ display: 'flex', gap: 'var(--space-4)', flexWrap: 'wrap' }}>
              <Tooltip content="Tooltip on top" position="top">
                <Button variant="secondary" size="sm">
                  Top
                </Button>
              </Tooltip>
              <Tooltip content="Tooltip on bottom" position="bottom">
                <Button variant="secondary" size="sm">
                  Bottom
                </Button>
              </Tooltip>
              <Tooltip content="Tooltip on left" position="left">
                <Button variant="secondary" size="sm">
                  Left
                </Button>
              </Tooltip>
              <Tooltip content="Tooltip on right" position="right">
                <Button variant="secondary" size="sm">
                  Right
                </Button>
              </Tooltip>
            </div>
          </Card>

          <Card variant="glass">
            <Typography variant="h6">Spinners</Typography>
            <div style={{ display: 'flex', gap: 'var(--space-4)', alignItems: 'center' }}>
              <Spinner size="xs" />
              <Spinner size="sm" />
              <Spinner size="md" />
              <Spinner size="lg" />
            </div>
            <div style={{ display: 'flex', gap: 'var(--space-4)', marginTop: 'var(--space-4)' }}>
              <Spinner variant="accent" label="Loading..." />
              <Spinner variant="success" />
            </div>
          </Card>

          <Card variant="glass">
            <Typography variant="h6">Avatars</Typography>
            <div style={{ display: 'flex', gap: 'var(--space-4)', alignItems: 'center' }}>
              <Avatar name="John Doe" size="xs" status="online" />
              <Avatar name="Jane Smith" size="sm" status="busy" />
              <Avatar name="Bob Wilson" size="md" status="away" />
              <Avatar name="Alice Brown" size="lg" status="offline" />
            </div>
          </Card>

          <Card variant="glass">
            <Typography variant="h6">Dropdown Menu</Typography>
            <DropdownMenu
              trigger={<Button variant="secondary">Open Menu</Button>}
              items={[
                { label: 'Edit', icon: '✏️', onClick: () => console.log('Edit') },
                { label: 'Duplicate', icon: '📋', onClick: () => console.log('Duplicate') },
                { label: '', divider: true },
                { label: 'Delete', icon: '🗑️', danger: true, onClick: () => console.log('Delete') },
              ]}
            />
          </Card>
        </div>
      </section>

      {/* Dividers & Alerts */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Dividers & Feedback
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Visual separators and user feedback components
        </Typography>

        <Card variant="glass">
          <Typography variant="h6">Dividers</Typography>
          <Divider />
          <Divider label="OR" />
          <Divider label="Section Break" />
        </Card>

        <div
          style={{
            marginTop: 'var(--space-8)',
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--space-4)',
          }}
        >
          <Alert
            variant="info"
            title="Information"
            message="This is an informational alert with a title and message."
            dismissible
          />
          <Alert
            variant="success"
            title="Success!"
            message="Your changes have been saved successfully."
            dismissible
          />
          <Alert
            variant="warning"
            title="Warning"
            message="Please review your changes before proceeding."
          />
          <Alert
            variant="error"
            title="Error"
            message="An error occurred while processing your request."
            dismissible
          />
        </div>
      </section>

      {/* Navigation Components */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Navigation Components
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Breadcrumbs, pagination, and accordions for content organization
        </Typography>

        <Card variant="glass">
          <Typography variant="h6">Breadcrumbs</Typography>
          <Breadcrumb
            items={[
              { label: 'Home', onClick: () => console.log('Home') },
              { label: 'Products', onClick: () => console.log('Products') },
              { label: 'Shoes', onClick: () => console.log('Shoes') },
              { label: 'Running Shoes' },
            ]}
          />
        </Card>

        <Card variant="glass">
          <Typography variant="h6">Pagination</Typography>
          <Pagination
            currentPage={currentPage}
            totalPages={10}
            onPageChange={setCurrentPage}
            showFirstLast
          />
        </Card>

        <Card variant="glass">
          <Typography variant="h6">Accordion</Typography>
          <Accordion
            items={[
              {
                id: '1',
                title: 'What is ClaraVerse?',
                content:
                  'ClaraVerse is a modern design system with a beautiful rose pink accent color.',
              },
              {
                id: '2',
                title: 'How do I get started?',
                content: 'Simply import the components you need and start building amazing UIs!',
              },
              {
                id: '3',
                title: 'Is it customizable?',
                content: 'Yes! All components support custom styling through className props.',
              },
            ]}
            defaultOpenItems={['1']}
          />
        </Card>
      </section>

      {/* Empty State */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Empty State
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Placeholder for no-data scenarios
        </Typography>

        <Card variant="glass">
          <EmptyState
            icon={
              <svg width="80" height="80" viewBox="0 0 80 80" fill="none">
                <circle cx="40" cy="40" r="30" stroke="currentColor" strokeWidth="2" />
                <path
                  d="M30 45L35 50L50 35"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
            }
            title="No items found"
            description="Get started by creating your first item."
            action={<Button variant="primary">Create Item</Button>}
          />
        </Card>
      </section>

      {/* Badges Section */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Badges
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Status indicators and labels with various styles
        </Typography>

        <Card variant="glass">
          <div className="ds-badge-showcase">
            <div className="ds-badge-row">
              <Typography variant="sm" weight="medium">
                Default:
              </Typography>
              <Badge>Default Badge</Badge>
              <Badge dot>With Dot</Badge>
            </div>

            <div className="ds-badge-row">
              <Typography variant="sm" weight="medium">
                Accent:
              </Typography>
              <Badge variant="accent">Accent</Badge>
              <Badge variant="accent" dot>
                Active
              </Badge>
            </div>

            <div className="ds-badge-row">
              <Typography variant="sm" weight="medium">
                Success:
              </Typography>
              <Badge variant="success">Success</Badge>
              <Badge variant="success" dot>
                Online
              </Badge>
            </div>

            <div className="ds-badge-row">
              <Typography variant="sm" weight="medium">
                Warning:
              </Typography>
              <Badge variant="warning">Warning</Badge>
              <Badge variant="warning" dot>
                Pending
              </Badge>
            </div>

            <div className="ds-badge-row">
              <Typography variant="sm" weight="medium">
                Error:
              </Typography>
              <Badge variant="error">Error</Badge>
              <Badge variant="error" dot>
                Failed
              </Badge>
            </div>

            <div className="ds-badge-row">
              <Typography variant="sm" weight="medium">
                Info:
              </Typography>
              <Badge variant="info">Info</Badge>
              <Badge variant="info" dot>
                Beta
              </Badge>
            </div>
          </div>
        </Card>
      </section>

      {/* Progress Section */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Progress
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Animated progress bars with gradient fill
        </Typography>

        <Card variant="glass">
          <Progress value={progress} showLabel label="Upload Progress" />
          <Progress value={85} showLabel label="Processing" />
          <Progress value={45} />
          <Progress value={100} showLabel label="Complete" />
        </Card>
      </section>

      {/* Loading States Section */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Loading States
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Skeleton loaders for async content
        </Typography>

        <div className="ds-showcase-grid">
          <Card variant="glass">
            <Typography variant="h6">Text Skeleton</Typography>
            <Skeleton variant="text" count={3} />
          </Card>

          <Card variant="glass">
            <Typography variant="h6">Profile Skeleton</Typography>
            <div style={{ display: 'flex', gap: '1rem', alignItems: 'center' }}>
              <Skeleton variant="circular" width={60} height={60} />
              <div style={{ flex: 1 }}>
                <Skeleton variant="text" width="60%" />
                <Skeleton variant="text" width="40%" />
              </div>
            </div>
          </Card>

          <Card variant="glass">
            <Typography variant="h6">Card Skeleton</Typography>
            <Skeleton variant="rectangular" height={120} />
            <Skeleton variant="text" count={2} />
          </Card>
        </div>
      </section>

      {/* Color Palette Section */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Color Palette
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Rose Pink accent with dark mode surfaces
        </Typography>

        <div className="ds-color-grid">
          <Card variant="widget" hoverable={false}>
            <div className="ds-color-swatch ds-color-accent"></div>
            <Typography variant="sm" weight="medium">
              Accent
            </Typography>
            <Typography variant="xs">#e91e63</Typography>
          </Card>

          <Card variant="widget" hoverable={false}>
            <div className="ds-color-swatch ds-color-success"></div>
            <Typography variant="sm" weight="medium">
              Success
            </Typography>
            <Typography variant="xs">#30d158</Typography>
          </Card>

          <Card variant="widget" hoverable={false}>
            <div className="ds-color-swatch ds-color-warning"></div>
            <Typography variant="sm" weight="medium">
              Warning
            </Typography>
            <Typography variant="xs">#ffd60a</Typography>
          </Card>

          <Card variant="widget" hoverable={false}>
            <div className="ds-color-swatch ds-color-error"></div>
            <Typography variant="sm" weight="medium">
              Error
            </Typography>
            <Typography variant="xs">#ff453a</Typography>
          </Card>

          <Card variant="widget" hoverable={false}>
            <div className="ds-color-swatch ds-color-info"></div>
            <Typography variant="sm" weight="medium">
              Info
            </Typography>
            <Typography variant="xs">#64d2ff</Typography>
          </Card>
        </div>
      </section>

      {/* Chat Components Section */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Chat Components
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Complete AI chat interface components with real-time interactions
        </Typography>

        <div className="ds-showcase-grid">
          <Card variant="glass">
            <Typography variant="h6">Chat Bubbles</Typography>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-4)' }}>
              <ChatBubble
                role="user"
                content="Hello! This is a user message."
                timestamp="10:30 AM"
              />
              <ChatBubble
                role="assistant"
                content="Hi! I'm the AI assistant. I can help you with code, markdown, and more!"
                timestamp="10:31 AM"
                actions={
                  <MessageActions
                    onCopy={() => console.log('Copy')}
                    onRegenerate={() => console.log('Regenerate')}
                  />
                }
              />
            </div>
          </Card>

          <Card variant="glass">
            <Typography variant="h6">Typing Indicator</Typography>
            <TypingIndicator text="AI is thinking" />
          </Card>

          <Card variant="glass">
            <Typography variant="h6">Streaming Text</Typography>
            <StreamingText
              text="This text appears character by character with a blinking cursor!"
              speed={40}
            />
          </Card>
        </div>

        <Card variant="feature">
          <Typography variant="h6">Chat Input</Typography>
          <ChatInput onSubmit={msg => console.log('Sent:', msg)} placeholder="Type a message..." />
        </Card>
      </section>

      {/* Code & Markdown Section */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Code & Markdown
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Syntax highlighting and markdown rendering for rich content
        </Typography>

        <Card variant="glass">
          <Typography variant="h6">Code Block with Syntax Highlighting</Typography>
          <CodeBlock
            code={`function fibonacci(n: number): number {
  if (n <= 1) return n;
  return fibonacci(n - 1) + fibonacci(n - 2);
}

console.log(fibonacci(10)); // 55`}
            language="typescript"
            fileName="fibonacci.ts"
          />
        </Card>

        <Card variant="glass">
          <Typography variant="h6">Markdown Renderer</Typography>
          <MarkdownRenderer
            content={`# Markdown Support

**Bold text**, *italic text*, and \`inline code\`.

- Bullet point 1
- Bullet point 2
  - Nested item

\`\`\`javascript
const greeting = "Hello World";
console.log(greeting);
\`\`\`

> Blockquotes work too!

[Links are supported](https://example.com)`}
          />
        </Card>
      </section>

      {/* Modals & Feedback Section */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Modals & Feedback
        </Typography>
        <Typography variant="base" className="ds-section-description">
          Dialogs, notifications, and navigation components
        </Typography>

        <div className="ds-showcase-grid">
          <Card variant="feature">
            <Typography variant="h6">Modal/Dialog</Typography>
            <Button variant="primary" onClick={() => setShowModal(true)}>
              Open Modal
            </Button>
          </Card>

          <Card variant="feature">
            <Typography variant="h6">Toast Notification</Typography>
            <Button variant="secondary" onClick={() => setShowToast(true)}>
              Show Toast
            </Button>
          </Card>

          <Card variant="feature">
            <Typography variant="h6">Tabs Navigation</Typography>
            <Tabs tabs={tabs} activeTab={activeTab} onChange={setActiveTab} />
            <div style={{ marginTop: 'var(--space-4)' }}>
              <Typography variant="sm">Active tab: {activeTab}</Typography>
            </div>
          </Card>
        </div>
      </section>

      {/* Design Principles */}
      <section className="ds-section">
        <Typography variant="h2" className="ds-section-title">
          Design Principles
        </Typography>

        <div className="ds-principles-grid">
          <Card variant="feature" icon="🌙">
            <Typography variant="h5">Dark First</Typography>
            <Typography variant="sm">
              Designed exclusively for dark mode with subtle borders and glassmorphism
            </Typography>
          </Card>

          <Card variant="feature" icon="🌸">
            <Typography variant="h5">Rose Pink Accent</Typography>
            <Typography variant="sm">
              Strategic use of #e91e63 for maximum visual impact on key interactions
            </Typography>
          </Card>

          <Card variant="feature" icon="✨">
            <Typography variant="h5">Smooth Animations</Typography>
            <Typography variant="sm">
              300ms transitions with cubic-bezier easing for Apple-like smoothness
            </Typography>
          </Card>

          <Card variant="feature" icon="🎨">
            <Typography variant="h5">Glassmorphism</Typography>
            <Typography variant="sm">
              Backdrop blur and semi-transparent surfaces for layered depth
            </Typography>
          </Card>

          <Card variant="feature" icon="♿">
            <Typography variant="h5">Accessible</Typography>
            <Typography variant="sm">
              WCAG AA compliant with focus states and reduced motion support
            </Typography>
          </Card>

          <Card variant="feature" icon="📱">
            <Typography variant="h5">Responsive</Typography>
            <Typography variant="sm">
              Mobile-first design with fluid typography using clamp()
            </Typography>
          </Card>
        </div>
      </section>

      {/* Footer */}
      <section className="ds-footer">
        <Typography variant="sm" align="center">
          Built with ❤️ for ClaraVerse | v1.0.0 Rose Pink Edition
        </Typography>
        <Typography variant="xs" align="center">
          @badboysm890
        </Typography>
      </section>

      {/* Modal */}
      <Modal
        isOpen={showModal}
        onClose={() => setShowModal(false)}
        title="Example Modal"
        footer={
          <>
            <Button variant="secondary" onClick={() => setShowModal(false)}>
              Cancel
            </Button>
            <Button variant="primary" onClick={() => setShowModal(false)}>
              Confirm
            </Button>
          </>
        }
      >
        <Typography variant="base">
          This is a modal dialog with backdrop blur and smooth animations. Perfect for
          confirmations, settings, or any overlay content.
        </Typography>
      </Modal>

      {/* Toast */}
      {showToast && (
        <div
          style={{
            position: 'fixed',
            top: 'var(--space-6)',
            right: 'var(--space-6)',
            zIndex: 'var(--z-tooltip)',
          }}
        >
          <Toast
            variant="success"
            title="Success!"
            message="This is a toast notification with auto-dismiss"
            onClose={() => setShowToast(false)}
          />
        </div>
      )}
    </div>
  );
};
