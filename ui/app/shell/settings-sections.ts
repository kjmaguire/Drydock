// One Settings (decision P140): the sections of the Settings page, in the order its left list shows them. Each section is one
// or more of the screens that were pages of their own; every one keeps its address (an old link, a doc or a test opens the same
// screen, now inside the Settings frame), so this registry is also the map from a route to its section. A section of more than
// one screen shows them as pills over the page (Backups & data, Help & about).
//
// To add a section: add an entry here (its title, the address it opens, the route names it holds, a test id, and the rows a
// search should find), give its screen a route in routes.ts with the hub 'settings' (lib/route.ts HUB_OF) and an action in
// actions/controller.tsx. The left list, the search, the pills and the command menu read this list; nothing else needs a change.

import type { Route } from '../lib/route.ts';
import { routes } from '../routes.ts';

/** One screen of a section: a pill over the page when the section has more than one. */
export interface SettingsPage {
  label: string;
  href: string;
  testId: string;
  /** The route names this screen is (an alias, such as /connections, is the screen it leads to). */
  on: readonly Route['name'][];
}

export interface SettingsSection {
  id: string;
  title: string;
  /** Where the section's link goes: its first screen. */
  href: string;
  testId: string;
  pages: readonly SettingsPage[];
  /** What the section holds, in the words its rows use: the search matches them and lists the ones it found. */
  rows: readonly string[];
}

const one = (label: string, href: string, testId: string, on: Route['name'][]): SettingsPage[] => [{ label, href, testId, on }];

export const SETTINGS_SECTIONS: readonly SettingsSection[] = [
  {
    id: 'general',
    title: 'General',
    href: routes.settings.href(),
    // the test id of the old "Settings and about" page, which General is
    testId: 'nav-settings-about',
    pages: one('General', routes.settings.href(), 'nav-settings-general', ['settings']),
    rows: ['Appearance', 'Theme', 'Light and dark', 'What agents inherit', 'Environment variables', 'Telemetry', 'Diagnostics', 'Updates', 'Not built yet'],
  },
  {
    id: 'agent-clis',
    title: 'Agent CLIs',
    href: routes.agents.href(),
    testId: 'nav-side-agents',
    pages: one('Agent CLIs', routes.agents.href(), 'nav-agent-clis', ['agents', 'connections']),
    rows: ['Claude Code', 'Codex', 'Gemini CLI', 'Custom CLI', 'Sign in', 'Test connection', 'Install and update', 'WSL', 'MCP gateway'],
  },
  {
    id: 'models',
    title: 'Models',
    href: routes.models.href(),
    testId: 'nav-side-models',
    pages: one('Models', routes.models.href(), 'nav-models', ['models', 'providers']),
    rows: ['Models on this PC', 'Local engine', 'GPU memory', 'Hugging Face search', 'Providers', 'Add a provider', 'API keys', 'OpenRouter'],
  },
  {
    id: 'routing',
    title: 'Routing & budgets',
    href: routes.routing.href(),
    testId: 'nav-routing',
    pages: one('Routing', routes.routing.href(), 'nav-routing-page', ['routing']),
    rows: ['Routing rules', 'Workspace level', 'Turn limits', 'Which model answers', 'Preview a turn', 'Budgets'],
  },
  {
    id: 'integrations',
    title: 'Integrations',
    href: routes.integrations.href(),
    testId: 'nav-side-integrations',
    pages: one('Integrations', routes.integrations.href(), 'nav-integrations-page', ['integrations', 'mcp']),
    rows: ['MCP servers', 'Your servers', 'Add an MCP server', 'Agent tools', 'Issue sync', 'Linear', 'Jira', 'Slack', 'Sentry', 'Notion'],
  },
  {
    id: 'github',
    title: 'GitHub',
    href: routes.github.href(),
    testId: 'nav-github',
    pages: one('GitHub', routes.github.href(), 'nav-github-page', ['github']),
    rows: ['GitHub CLI sign-in', 'Issue sync', 'MCP server for agents', 'Repositories', 'CI runners'],
  },
  {
    id: 'security-scan',
    title: 'Security scan',
    href: routes.securityScan.href(),
    testId: 'nav-security-scan',
    pages: one('Security scan', routes.securityScan.href(), 'nav-security-scan-page', ['security-scan']),
    rows: ['Scan a folder', 'Scan a repository', 'Secrets', 'Vulnerabilities', 'Findings', 'Rules'],
  },
  {
    id: 'backups',
    title: 'Backups & data',
    href: routes.backups.href(),
    testId: 'nav-backups',
    pages: [
      { label: 'Backups', href: routes.backups.href(), testId: 'nav-backups-page', on: ['backups'] },
      { label: 'Export and import', href: routes.data.href(), testId: 'nav-data', on: ['data'] },
    ],
    rows: ['Backups', 'Back up now', 'Restore', 'Export', 'Import', 'Packages'],
  },
  {
    id: 'shortcuts',
    title: 'Shortcuts',
    href: routes.settingsShortcuts.href(),
    testId: 'nav-shortcuts',
    pages: one('Shortcuts', routes.settingsShortcuts.href(), 'nav-shortcuts-page', ['settings-shortcuts']),
    rows: ['Keyboard shortcuts', 'Change a shortcut', 'Command menu', 'Reset all'],
  },
  {
    id: 'help',
    title: 'Help & about',
    href: routes.settingsAbout.href(),
    testId: 'nav-help',
    pages: [
      { label: 'About', href: routes.settingsAbout.href(), testId: 'nav-about', on: ['settings-about'] },
      { label: 'Support bundle', href: routes.support.href(), testId: 'nav-support', on: ['support'] },
      { label: 'License', href: routes.license.href(), testId: 'nav-license', on: ['license'] },
      // the Guide is a page of its own (no Settings frame); its pill leaves Settings
      { label: 'Guide', href: routes.guide.href(), testId: 'nav-guide', on: ['guide'] },
    ],
    rows: ['Version', 'Licenses', 'About this session', 'Data location', 'Support bundle', 'Crash reports', 'License', 'Guide', 'Help'],
  },
];

/** The section a route belongs to (an old address names the section that now holds its screen), if any. */
export function sectionOf(route: Pick<Route, 'name'>): SettingsSection | undefined {
  return SETTINGS_SECTIONS.find((s) => s.pages.some((p) => p.on.includes(route.name)));
}

/** A section the search found, with the rows of it that matched (none when the title matched alone). */
export interface SettingsHit {
  section: SettingsSection;
  rows: string[];
}

const norm = (s: string): string => s.toLowerCase().replace(/&/g, 'and').replace(/\s+/g, ' ').trim();

/**
 * The sections a search keeps: every word of the query must be found in the section's title or in one of its rows (or a page's
 * label). An empty query keeps every section with no rows listed. Order is the registry's.
 */
export function searchSettings(query: string, sections: readonly SettingsSection[] = SETTINGS_SECTIONS): SettingsHit[] {
  const words = norm(query).split(' ').filter(Boolean);
  if (!words.length) return sections.map((section) => ({ section, rows: [] }));
  const has = (text: string) => words.every((w) => norm(text).includes(w));
  const out: SettingsHit[] = [];
  for (const section of sections) {
    const rows = [...section.rows, ...section.pages.map((p) => p.label).filter((l) => l !== section.title)].filter((r, i, a) => a.indexOf(r) === i && has(r));
    const all = norm([section.title, ...section.rows, ...section.pages.map((p) => p.label)].join(' '));
    if (rows.length || has(section.title) || words.every((w) => all.includes(w))) out.push({ section, rows });
  }
  return out;
}
