import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { hubOf, parseRoute } from '../lib/route.ts';
import { searchSettings, sectionOf, SETTINGS_SECTIONS } from './settings-sections.ts';

const titles = (q: string) => searchSettings(q).map((h) => h.section.title);

describe('the Settings sections (decision P140)', () => {
  it('are the ten sections of the mockup, in order, each with a unique id, test id and address', () => {
    expect(SETTINGS_SECTIONS.map((s) => s.title)).toEqual([
      'General',
      'Agent CLIs',
      'Models',
      'Routing & budgets',
      'Integrations',
      'GitHub',
      'Security scan',
      'Backups & data',
      'Shortcuts',
      'Help & about',
    ]);
    for (const key of ['id', 'testId', 'href'] as const) {
      const all = SETTINGS_SECTIONS.map((s) => s[key]);
      expect([key, new Set(all).size]).toEqual([key, all.length]);
    }
    // the test ids the old pages had live on, on the section links
    expect(SETTINGS_SECTIONS.find((s) => s.id === 'agent-clis')!.testId).toBe('nav-side-agents');
    expect(SETTINGS_SECTIONS.find((s) => s.id === 'models')!.testId).toBe('nav-side-models');
    expect(SETTINGS_SECTIONS.find((s) => s.id === 'integrations')!.testId).toBe('nav-side-integrations');
    expect(SETTINGS_SECTIONS.find((s) => s.id === 'security-scan')!.testId).toBe('nav-security-scan');
  });

  it('map every old address to its section, and every section page is in the Settings hub (the Guide excepted)', () => {
    const at = (p: string) => sectionOf(parseRoute(p))?.id;
    expect(at('/settings')).toBe('general');
    expect(at('/agents')).toBe('agent-clis');
    expect(at('/agents/claude')).toBe('agent-clis');
    expect(at('/connections')).toBe('agent-clis');
    expect(at('/models')).toBe('models');
    expect(at('/models/this-pc')).toBe('models');
    expect(at('/providers')).toBe('models');
    expect(at('/routing')).toBe('routing');
    expect(at('/integrations/linear')).toBe('integrations');
    expect(at('/mcp')).toBe('integrations');
    expect(at('/github')).toBe('github');
    expect(at('/security-scan')).toBe('security-scan');
    expect(at('/backups')).toBe('backups');
    expect(at('/data')).toBe('backups');
    expect(at('/settings/shortcuts')).toBe('shortcuts');
    expect(at('/settings/about')).toBe('help');
    expect(at('/support')).toBe('help');
    expect(at('/license')).toBe('help');
    expect(at('/tasks')).toBe(undefined);
    expect(at('/library')).toBe(undefined);
    for (const s of SETTINGS_SECTIONS) {
      // a section's link opens its first page
      expect([s.id, s.href]).toEqual([s.id, s.pages[0]!.href]);
      for (const p of s.pages) {
        const r = parseRoute(p.href);
        if (r.name === 'guide') continue;
        expect([p.href, hubOf(r), sectionOf(r)?.id]).toEqual([p.href, 'settings', s.id]);
      }
    }
  });

  it('search keeps the sections whose title or rows match, and lists the rows it found', () => {
    expect(titles('')).toEqual(SETTINGS_SECTIONS.map((s) => s.title));
    expect(searchSettings('').every((h) => h.rows.length === 0)).toBe(true);
    // a section's title, any case
    expect(titles('github')).toEqual(['GitHub']);
    expect(titles('MODELS')).toEqual(['Models']);
    // a row: the section it is in, with the row named
    const theme = searchSettings('theme');
    expect(theme.map((h) => [h.section.title, h.rows])).toEqual([['General', ['Theme']]]);
    expect(searchSettings('claude').map((h) => [h.section.id, h.rows])).toEqual([['agent-clis', ['Claude Code']]]);
    // "&" reads as "and", and every word must be found
    expect(titles('routing and budgets')).toEqual(['Routing & budgets']);
    expect(titles('backups data')).toEqual(['Backups & data']);
    // a page of a section is found by its label, and its row links there
    expect(searchSettings('export').map((h) => [h.section.id, h.rows])).toEqual([['backups', ['Export', 'Export and import']]]);
    expect(titles('license')).toEqual(['Help & about']);
    expect(titles('sign')).toContain('Agent CLIs');
    expect(titles('zzz nothing')).toEqual([]);
  });
});
