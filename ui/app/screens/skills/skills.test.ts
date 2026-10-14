import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { LibrarySkill, SkillRegistryStatus, SkillUsage } from '../../api/extTypes.ts';
import {
  addAllText,
  addSelectedText,
  addedText,
  audienceText,
  byRole,
  coverageText,
  formatCount,
  inWords,
  isGiven,
  libraryBlurb,
  libraryGroups,
  libraryStateText,
  listOr,
  newInLibrary,
  noSkillChanges,
  notInProject,
  onTeam,
  reaches,
  recommendationBadge,
  registryStatusText,
  registryTileText,
  registryUpdates,
  rolesMatch,
  scanText,
  shortDescription,
  skillBrand,
  skillStateText,
  skillTone,
  sortUsage,
  syncChanges,
  toggleSkill,
  usageTokens,
} from './skills.ts';

const lib = (over: Partial<LibrarySkill>): LibrarySkill => ({
  id: 'l',
  registryId: 'anthropic-skills',
  repo: 'anthropics/skills',
  name: 'pdf',
  description: 'PDF tools',
  license: 'MIT',
  globs: [],
  roles: [],
  sourcePath: 'skills/pdf',
  pinnedCommit: 'abc',
  contentHash: 'h',
  hasScripts: false,
  status: 'available',
  blockReason: '',
  risk: 0,
  flags: [],
  gone: false,
  updated: '',
  ...over,
});

const status = (over: Partial<SkillRegistryStatus>): SkillRegistryStatus => ({
  registryId: 'anthropic-skills',
  repo: 'anthropics/skills',
  enabled: true,
  lastCommit: 'abc',
  checkedAt: '2026-10-13T08:00:00Z',
  skills: 12,
  blocked: 0,
  added: [],
  changed: [],
  projects: {},
  ...over,
});

const usage = (over: Partial<SkillUsage>): SkillUsage => ({
  name: 'pdf',
  registryId: 'anthropic-skills',
  origin: 'registry',
  status: 'accepted',
  loaded: 0,
  listed: 0,
  opened: 0,
  tasks: 0,
  tokensLoaded: 0,
  tokensListed: 0,
  recommendation: '',
  reason: '',
  ...over,
});

describe('Skills helpers', () => {
  it('reports a scan with counts and the problems verbatim', () => {
    expect(
      scanText({
        found: 2,
        accepted: 0,
        offered: 1,
        blocked: 1,
        removed: 0,
        errors: null,
      }),
    ).toBe('Found 2: 1 offered, 0 accepted, 1 blocked, 0 removed.');
    expect(
      scanText({
        found: 0,
        accepted: 0,
        offered: 0,
        blocked: 0,
        removed: 0,
        errors: ['bad yaml', 'too big'],
      }),
    ).toContain('Problems: bad yaml; too big');
  });

  it('maps each registry to its brand; a repository skill and an unknown registry have none', () => {
    expect(skillBrand('cloudflare-skills', 'registry')).toBe('cloudflare');
    expect(skillBrand('microsoft-skills')).toBe('microsoft');
    expect(skillBrand('msdocs-agent-skills')).toBe('microsoft');
    expect(skillBrand('msdocs-mcp-skills')).toBe('microsoft');
    expect(skillBrand('msgraph-community')).toBe('microsoft');
    expect(skillBrand('vercel-agent-skills')).toBe('vercel');
    expect(skillBrand('anthropic-skills')).toBe('anthropic');
    expect(skillBrand('openai-skills')).toBe('openai');
    expect(skillBrand('stripe-ai')).toBe('stripe');
    expect(skillBrand('remix')).toBe('remix');
    expect(skillBrand('react-router')).toBe('remix');
    expect(skillBrand('someone/else')).toBe('');
    expect(skillBrand('anthropic-skills', 'repo')).toBe('');
    expect(skillBrand('anthropic-skills', 'repository')).toBe('');
    expect(skillBrand(undefined, 'registry')).toBe('');
  });

  it('writes a project skill\'s state: accepted with a check and who gets it, offered, rejected, blocked, or an update in the Library', () => {
    expect(skillStateText({ status: 'accepted', audience: 'team', agents: [] }, false)).toEqual({ text: 'Accepted ✓ · whole team', tone: 'ok' });
    expect(skillStateText({ status: 'accepted', audience: 'team', agents: ['a'] }, false).text).toBe('Accepted ✓ · whole team');
    expect(skillStateText({ status: 'accepted', audience: 'agents', agents: ['a', 'b'] }, false)).toEqual({ text: 'Accepted ✓ · 2 agents', tone: 'ok' });
    expect(skillStateText({ status: 'accepted', audience: 'agents', agents: ['a'] }, false).text).toBe('Accepted ✓ · 1 agent');
    expect(skillStateText({ status: 'accepted', audience: 'agents', agents: null }, false).text).toBe('Accepted ✓ · 0 agents');
    expect(skillStateText({ status: 'offered', audience: 'team', agents: [] }, false)).toEqual({ text: 'Offered', tone: 'warn' });
    expect(skillStateText({ status: 'rejected', audience: 'team', agents: [] }, false)).toEqual({ text: 'Rejected', tone: '' });
    expect(skillStateText({ status: 'blocked', audience: 'team', agents: [] }, false)).toEqual({ text: 'Blocked', tone: 'bad' });
    expect(skillStateText({ status: 'accepted', audience: 'team', agents: [] }, true)).toEqual({ text: 'Update in the Library', tone: 'warn' });
  });

  it('names the project skills whose library text is newer, from every registry', () => {
    const sts = [
      status({ projects: { p1: { updates: [{ name: 'pdf', description: '' }] }, p2: { updates: [{ name: 'other', description: '' }] } } }),
      status({ registryId: 'cloudflare-skills', projects: { p1: { updates: [{ name: 'wrangler', description: '' }] } } }),
      status({ registryId: 'x', projects: null }),
    ];
    expect([...registryUpdates(sts, 'p1')].sort()).toEqual(['pdf', 'wrangler']);
    expect([...registryUpdates(sts, 'p2')]).toEqual(['other']);
    expect(registryUpdates(undefined, 'p1').size).toBe(0);
    expect(registryUpdates([status({ projects: { p1: { updates: null } } })], 'p1').size).toBe(0);
  });

  it('lists the library skills a project does not have yet, grouped by registry with counts', () => {
    const library = [lib({}), lib({ id: 'x', name: 'xlsx' }), lib({ id: 'w', name: 'wrangler', registryId: 'cloudflare-skills', repo: 'cloudflare/skills' })];
    const left = notInProject(library, [{ name: 'pdf' }]);
    expect(left.map((l) => l.name)).toEqual(['xlsx', 'wrangler']);
    expect(libraryGroups(library)).toEqual([
      { registryId: 'anthropic-skills', repo: 'anthropics/skills', count: 2 },
      { registryId: 'cloudflare-skills', repo: 'cloudflare/skills', count: 1 },
    ]);
    expect(libraryGroups([])).toEqual([]);
  });

  it('describes a library skill: what it is with its license, and available or blocked with the reason', () => {
    expect(libraryBlurb(lib({}))).toBe('PDF tools · MIT');
    expect(libraryBlurb(lib({ license: '' }))).toBe('PDF tools');
    expect(libraryBlurb(lib({ description: '', license: '' }))).toBe('');
    expect(libraryStateText(lib({}))).toEqual({ text: 'Available', tone: '' });
    expect(libraryStateText(lib({ gone: true }))).toEqual({ text: 'Available · no longer upstream', tone: 'warn' });
    expect(libraryStateText(lib({ status: 'blocked', blockReason: 'contains a secret' }))).toEqual({ text: 'Blocked: contains a secret', tone: 'bad' });
    expect(libraryStateText(lib({ status: 'blocked', blockReason: '' })).text).toBe('Blocked: the core did not say why');
  });

  it('says what adding a whole registry did, naming blocked and skipped only when there were any, and what adding one did', () => {
    const none = { added: ['a', 'b'], unchanged: ['c'], blocked: [], skipped: [] };
    expect(addAllText(none)).toBe('2 added, 1 unchanged');
    expect(addAllText({ ...none, blocked: [{ name: 'x', reason: 'secret' }] })).toBe('2 added, 1 unchanged, 1 blocked');
    expect(addAllText({ ...none, skipped: [{ name: 'y', reason: 'invalid' }, { name: 'z', reason: 'invalid' }] })).toBe('2 added, 1 unchanged, 2 skipped');
    expect(addAllText({ ...none, added: null as never, blocked: null as never })).toBe('0 added, 1 unchanged'); // the core may send null lists
    expect(addedText('wrangler', 'gocalc')).toBe('wrangler was added to gocalc and is accepted ✓');
  });

  it('describes a registry in the Library: synced with counts, not synced yet, failed, or disabled', () => {
    const st = status({});
    expect(registryStatusText(st)).toEqual({ text: `12 skills in the Library · synced ${new Date(st.checkedAt!).toLocaleString()}`, tone: 'ok' });
    expect(registryStatusText(status({ skills: 1, blocked: 1 })).text).toBe(`1 skill in the Library (1 blocked) · synced ${new Date(st.checkedAt!).toLocaleString()}`);
    expect(registryStatusText(status({ checkedAt: undefined }))).toEqual({ text: 'Not synced yet', tone: 'warn' });
    expect(registryStatusText(undefined)).toEqual({ text: 'Not synced yet', tone: 'warn' });
    expect(registryStatusText(status({ lastError: 'GitHub rate limit' }))).toEqual({ text: 'Last sync failed: GitHub rate limit', tone: 'bad' });
    expect(registryStatusText(st, false)).toEqual({ text: 'Disabled: not synced', tone: '' });
    expect(registryStatusText(status({ enabled: false })).text).toBe('Disabled: not synced');
    expect(syncChanges(status({ added: [{ name: 'a', description: '' }], changed: null }))).toEqual({ added: [{ name: 'a', description: '' }], changed: [] });
    expect(syncChanges(undefined)).toEqual({ added: [], changed: [] });
  });

  it('keeps a registry tile to one short line: counts, blocked, what is new; the details stay in the panel', () => {
    expect(registryTileText(status({}))).toEqual({ text: '12 skills', tone: 'ok' });
    expect(registryTileText(status({ skills: 20, blocked: 3 }))).toEqual({ text: '20 skills · 3 blocked', tone: 'ok' });
    expect(registryTileText(status({ skills: 20, added: [{ name: 'a', description: '' }, { name: 'b', description: '' }] }))).toEqual({ text: '20 skills · 2 new', tone: 'warn' });
    expect(registryTileText(status({ skills: 1 })).text).toBe('1 skill');
    expect(registryTileText(status({ lastError: 'resolve MicrosoftDocs/Agent-Skills@HEAD: page exceeds crawl.max_page_bytes' }))).toEqual({ text: 'Sync failed', tone: 'bad' });
    expect(registryTileText(status({ checkedAt: undefined }))).toEqual({ text: 'Not synced yet', tone: 'warn' });
    expect(registryTileText(status({}), false)).toEqual({ text: 'Disabled', tone: '' });
    const fresh = newInLibrary([status({ registryId: 'cf', added: [{ name: 'wrangler', description: '' }] }), status({ added: null })]);
    expect([...fresh]).toEqual(['cf/wrangler']);
  });

  it("shows a description's first sentence, short, until the row is opened", () => {
    expect(shortDescription('Creates algorithmic art with p5.js. Use when the user asks for generative art, flow fields or particles.')).toBe('Creates algorithmic art with p5.js.');
    expect(shortDescription('No full stop here')).toBe('No full stop here');
    expect(shortDescription('  Spaces\n and   lines.  ')).toBe('Spaces and lines.');
    const long = 'A'.repeat(150);
    expect(shortDescription(long, 40)).toBe(`${'A'.repeat(39)}…`);
    expect(shortDescription('Version 1.2 is supported. More text.')).toBe('Version 1.2 is supported.');
  });

  it('names what the add button adds', () => {
    expect(addSelectedText(['wrangler'], 'gocalc')).toBe('Add wrangler to gocalc');
    expect(addSelectedText(['a', 'b', 'c'], 'gocalc')).toBe('Add 3 skills to gocalc');
  });

  it('sorts usage rows: the ones to remove first, then by tokens spent, then by name; tokens add loaded and listed', () => {
    const rows = [
      usage({ name: 'keep-big', tokensLoaded: 9000, tokensListed: 10, recommendation: 'keep' }),
      usage({ name: 'rm-small', tokensLoaded: 10, recommendation: 'remove', reason: 'never opened' }),
      usage({ name: 'rm-big', tokensListed: 500, recommendation: 'remove' }),
      usage({ name: 'b-none', tokensLoaded: 100 }),
      usage({ name: 'a-none', tokensLoaded: 100 }),
    ];
    expect(sortUsage(rows).map((u) => u.name)).toEqual(['rm-big', 'rm-small', 'keep-big', 'a-none', 'b-none']);
    expect(usageTokens(usage({ tokensLoaded: 1200, tokensListed: 34 }))).toBe(1234);
    expect(formatCount(1234567)).toBe('1,234,567');
    expect(formatCount(0)).toBe('0');
    expect(recommendationBadge('remove')).toEqual({ tone: 'err', text: 'remove' });
    expect(recommendationBadge('narrow')).toEqual({ tone: 'warn', text: 'narrow' });
    expect(recommendationBadge('keep')).toEqual({ tone: 'ok', text: 'keep' });
    expect(recommendationBadge('')).toEqual({ tone: 'muted', text: 'no data yet' });
  });

  it('tones, coverage and lists', () => {
    expect(skillTone('accepted')).toBe('ok');
    expect(skillTone('offered')).toBe('warn');
    expect(skillTone('blocked')).toBe('err');
    expect(skillTone('rejected')).toBe('muted');
    expect(coverageText({ files: 2, tool: 1 })).toBe('2 delivered as rule files, 1 through a tool.');
    expect(coverageText(undefined)).toBe('');
    expect(listOr(null)).toBe('all');
    expect(listOr(['a', 'b'])).toBe('a, b');
  });

  it('matches roles as the core does: none listed is every role, case and spaces ignored', () => {
    expect(rolesMatch([], 'anything')).toBe(true);
    expect(rolesMatch(null, '')).toBe(true);
    expect(rolesMatch(['Frontend'], ' frontend ')).toBe(true);
    expect(rolesMatch(['UX Designer', 'QA'], 'qa')).toBe(true);
    expect(rolesMatch(['Frontend'], 'backend')).toBe(false);
  });

  it('decides who a skill reaches like internal/skills Reaches', () => {
    const team = { audience: 'team', roles: ['Frontend'], agents: ['agt_b'] };
    expect(reaches(team, 'agt_a', 'frontend')).toBe(true); // by role
    expect(reaches(team, 'agt_b', 'backend')).toBe(true); // given on top of the team
    expect(reaches(team, 'agt_c', 'backend')).toBe(false);
    expect(byRole(team, 'frontend')).toBe(true);
    expect(byRole(team, 'backend')).toBe(false);
    const chosen = { audience: 'agents', roles: [], agents: ['agt_b'] };
    expect(reaches(chosen, 'agt_b', 'backend')).toBe(true);
    expect(reaches(chosen, 'agt_a', 'backend')).toBe(false); // roles do not count for chosen agents
    expect(byRole(chosen, 'backend')).toBe(false);
    expect(reaches({ audience: 'agents', roles: null, agents: null }, '', 'qa')).toBe(false);
  });

  it('says who gets a skill in words', () => {
    const names: Record<string, string> = { agt_a: 'Ada', agt_b: 'Bob', agt_c: 'Cy' };
    const name = (id: string) => names[id] ?? id;
    expect(audienceText({ audience: 'team', roles: [], agents: [] }, name)).toBe('The whole team');
    expect(audienceText({ audience: 'team', roles: ['Database', 'Backend'], agents: ['agt_a'] }, name)).toBe('Every Database or Backend agent, and Ada');
    expect(audienceText({ audience: 'agents', roles: ['QA'], agents: ['agt_a', 'agt_b', 'agt_c'] }, name)).toBe('Only Ada, Bob and Cy');
    expect(audienceText({ audience: 'agents', roles: null, agents: null }, name)).toBe('Nobody: no agent is chosen yet');
    expect(inWords([])).toBe('');
    expect(inWords(['a', 'b'], 'or')).toBe('a or b');
  });

  it('leaves out agents removed from the team once the team is known', () => {
    const k = { audience: 'agents', roles: [], agents: ['agt_a', 'agt_gone'] };
    expect(onTeam(k, [{ id: 'agt_a' }]).agents).toEqual(['agt_a']);
    expect(onTeam(k, []).agents).toEqual(['agt_a', 'agt_gone']);
  });

  it('keeps only real changes when skills are ticked and unticked', () => {
    let c = noSkillChanges();
    c = toggleSkill(c, 'new', false, true); // give a skill the agent did not have
    c = toggleSkill(c, 'old', true, false); // take one it had
    expect(c).toEqual({ add: ['new'], remove: ['old'] });
    expect(isGiven(c, 'new', false)).toBe(true);
    expect(isGiven(c, 'old', true)).toBe(false);
    expect(isGiven(c, 'kept', true)).toBe(true);
    c = toggleSkill(toggleSkill(c, 'new', false, false), 'old', true, true); // and back: nothing to do
    expect(c).toEqual({ add: [], remove: [] });
    expect(toggleSkill(toggleSkill(noSkillChanges(), 'x', false, true), 'x', false, true)).toEqual({ add: ['x'], remove: [] });
  });
});
