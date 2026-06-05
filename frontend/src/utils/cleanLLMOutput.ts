/**
 * Cleans up common LLM output issues to improve markdown rendering.
 *
 * Common LLM markdown issues (from research):
 * ✅ HANDLED (safe fixes):
 *   - Missing space after ## headers: ###Header -> ### Header (not # to avoid #123, #fff, #include)
 *   - Headers stuck to content: text## Header -> text\n\n## Header
 *   - Horizontal rules stuck to headers: ---## -> ---\n\n##
 *   - Unicode box pipes in tables: │ (U+2502) -> | (ASCII) for proper table rendering
 *
 * ❌ NOT HANDLED (too risky for false positives):
 *   - Single # headers: Could be issue refs (#123), hex colors (#fff), C preprocessor (#include)
 *   - Emphasis spacing: * text * could be a list item with text
 *   - Bold/italic at line start: *text could be a list item
 *   - Unclosed code blocks: Could break during streaming mid-block
 *   - Mixed list markers: Usually renders fine, fixing could break intentional formatting
 *
 * Note: Most formatting is handled via system prompt instructions.
 * This function only fixes structural issues that break markdown parsing.
 */

export function cleanLLMOutput(content: string): string {
  let result = content;

  // Fix headers missing space after # symbols (###Header -> ### Header)
  // Only fix 2-6 # to avoid false positives with #123 (issues), #fff (hex), #include, etc.
  result = result.replace(/^(#{2,6})([^\s#])/gm, '$1 $2');

  // Fix horizontal rules stuck to headers (---## -> ---\n\n##)
  result = result.replace(/^(---+)(#+\s)/gm, '$1\n\n$2');
  result = result.replace(/(\n---+)(#+\s)/g, '$1\n\n$2');

  // Fix headers stuck to previous content without newline
  // Exclude # from preceding char to avoid breaking ###Header after first fix
  result = result.replace(/([^\n#])(#{1,6}\s+\S)/g, '$1\n\n$2');

  // Fix Unicode box-drawing pipes in tables (│ U+2502 -> | ASCII)
  // LLMs sometimes use fancy Unicode characters instead of standard markdown
  result = result.replace(/│/g, '|');
  result = result.replace(/┃/g, '|'); // Also handle heavy vertical line

  return result;
}

/**
 * Light cleanup for streaming content.
 * Only fixes critical structural issues to avoid visual jumps.
 */
export function cleanLLMOutputLight(content: string): string {
  let result = content;

  // Fix headers missing space after # symbols (###Header -> ### Header)
  // Only fix 2-6 # to avoid false positives with #123 (issues), #fff (hex), #include, etc.
  result = result.replace(/^(#{2,6})([^\s#])/gm, '$1 $2');

  // Fix horizontal rules stuck to headers
  result = result.replace(/^(---+)(#+\s)/gm, '$1\n\n$2');
  result = result.replace(/(\n---+)(#+\s)/g, '$1\n\n$2');

  // Fix Unicode box-drawing pipes in tables (│ U+2502 -> | ASCII)
  result = result.replace(/│/g, '|');
  result = result.replace(/┃/g, '|');

  // Heal an unclosed code fence while streaming so the rest of the message
  // doesn't render as raw markdown inside a half-open code block. If the count
  // of ``` fences is odd, append a temporary closing fence.
  const fenceCount = (result.match(/^```/gm) || []).length;
  if (fenceCount % 2 === 1) {
    result += '\n```';
  }

  return result;
}
