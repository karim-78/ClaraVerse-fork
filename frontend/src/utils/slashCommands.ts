/**
 * Slash commands — power-user presets that swap a system-prompt prefix
 * before sending. Parsing only; UI command-palette is intentionally out of
 * scope for v1 (a follow-up can render an autocomplete on `/` typed).
 *
 * Usage in the chat input:
 *
 *   /web what's openai's latest model?
 *   /code refactor this snippet to use async/await
 *   /sql top 10 customers by revenue this quarter
 *   /research compare anthropic vs openai pricing in 2026
 *
 * Each command (a) strips its prefix from the visible message, (b) prepends
 * a focused instruction onto the system prompt for that single turn.
 */

export interface SlashCommandMatch {
  /** Bare command word, e.g. "web" */
  name: string;
  /** Whatever the user typed after the command. May be empty. */
  rest: string;
  /** System-prompt prefix to apply for this turn only. */
  systemPrefix: string;
  /** Optional short label rendered above the input as confirmation. */
  hint: string;
}

interface CommandDef {
  description: string;
  hint: string;
  systemPrefix: string;
}

// The catalogue. Keep prefixes short, decisive, in second-person so they
// stack cleanly on top of the regular system prompt.
const COMMANDS: Record<string, CommandDef> = {
  web: {
    description: 'Quick web research — search + scrape for a fresh answer.',
    hint: 'Web research mode',
    systemPrefix:
      'This message is a /web command. Use search_web and scrape_web liberally to ground your answer in current sources. Cite each claim inline as [Source](url). Keep the response tight; quote the source for any factual claim.',
  },
  code: {
    description: 'Coding task — write, run, or debug Python.',
    hint: 'Code mode',
    systemPrefix:
      'This message is a /code command. Treat it as a coding task. Prefer run_python (the persistent sandbox) over written-but-untested code. Show the code you ran, then the output. Keep prose minimal.',
  },
  sql: {
    description: 'SQL query — explain or generate.',
    hint: 'SQL mode',
    systemPrefix:
      'This message is a /sql command. The user wants SQL. If a connection is configured, use run_sql. Otherwise output a portable SQL query in a fenced ```sql block, explain key joins/filters in one paragraph, and ask which dialect or schema to target if ambiguous.',
  },
  research: {
    description: 'Deeper research — multi-source comparison.',
    hint: 'Research mode',
    systemPrefix:
      'This message is a /research command. Treat it as a multi-source investigation. Consider spawning a subagent (spawn_subagent) for each independent sub-question to keep your own context lean. Synthesize a structured report with sections (Findings / Evidence / Caveats), and cite every claim.',
  },
  data: {
    description: 'Analyze attached data — load + summarize.',
    hint: 'Data analysis mode',
    systemPrefix:
      'This message is a /data command. The user has data they want analyzed. Use run_python in the persistent sandbox: load the file(s) from /data/, run df.info() and df.describe() first, then answer the actual question. Use display_df() for tables and plt for charts so the user can see them.',
  },
};

/**
 * Try to parse a slash command off the front of a message.
 * Returns null when the input is not a known slash command.
 */
export function parseSlashCommand(input: string): SlashCommandMatch | null {
  const trimmed = input.trimStart();
  if (!trimmed.startsWith('/')) return null;
  // Pull the command word: letters/digits, ends at whitespace or newline
  const match = trimmed.match(/^\/([a-zA-Z][a-zA-Z0-9_-]*)(?:\s+([\s\S]*))?$/);
  if (!match) return null;
  const [, name, rest] = match;
  const def = COMMANDS[name.toLowerCase()];
  if (!def) return null;
  return {
    name: name.toLowerCase(),
    rest: (rest ?? '').trim(),
    systemPrefix: def.systemPrefix,
    hint: def.hint,
  };
}

/** Used by an eventual autocomplete UI to render the catalogue. */
export function listSlashCommands(): Array<{ name: string; description: string }> {
  return Object.entries(COMMANDS).map(([name, def]) => ({
    name,
    description: def.description,
  }));
}
