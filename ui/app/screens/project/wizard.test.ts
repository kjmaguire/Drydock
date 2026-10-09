import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { AdapterInfo } from '../../api/types.ts';
import { adapterChoices, authNote, buildSetup, canOpen, cloneTarget, effectiveMode, emptyWizard, firstIncomplete, memberFromPrebuilt, pushedAgo, stepProblem, suggestTeam, uniqueName } from './wizard.ts';
import { prebuilt } from './wizard-fixtures.ts';

const adapter = (id: string, installed: boolean): AdapterInfo => ({ id, detection: { adapter: id, installed, status: installed ? 'ready' : 'not_installed' }, capabilities: {} as never });

describe('new-project wizard rules', () => {
  it('needs a name only when there is no repository, and checks each repository kind', () => {
    const s = emptyWizard();
    expect(stepProblem(s, 'Basics')).toBeUndefined();
    expect(stepProblem(s, 'Repository')).toBe('Enter the folder of the local repository.');
    s.localPath = 'C:\\src\\calc';
    expect(stepProblem(s, 'Repository')).toBeUndefined();
    s.repo = 'none';
    expect(stepProblem(s, 'Repository')).toContain('needs a name');
    s.name = 'later';
    expect(stepProblem(s, 'Repository')).toBeUndefined();
    s.repo = 'github';
    expect(stepProblem(s, 'Repository')).toBe('Choose a GitHub repository.');
    s.github = { fullName: 'acme/pay', name: 'pay' } as never;
    expect(stepProblem(s, 'Repository')).toBe('Choose the folder to clone into.');
    s.cloneDir = 'C:\\Users\\k\\source\\repos';
    expect(stepProblem(s, 'Repository')).toBeUndefined();
    s.brief = 'x'.repeat(8001);
    expect(stepProblem(s, 'Basics')).toContain('8000');
    expect(stepProblem(s, 'Review')).toContain('Basics:');
  });

  it('opens a step only after the earlier ones are complete', () => {
    const s = emptyWizard();
    expect(canOpen(s, 'Repository')).toBe(true);
    expect(canOpen(s, 'Connectors')).toBe(false);
    expect(firstIncomplete(s)).toBe('Repository');
    s.repo = 'none';
    s.name = 'x';
    expect(canOpen(s, 'Review')).toBe(true);
    expect(firstIncomplete(s)).toBe('Review');
  });

  it('checks the team: names, custom agents complete, no duplicates', () => {
    const s = emptyWizard();
    s.team = [{ key: 'a', source: 'custom', name: 'Ada', role: '', adapter: 'claude', model: '', effort: '', permissionMode: '', purpose: '' }];
    expect(stepProblem(s, 'Team')).toBe('Ada needs a role and an agent CLI.');
    s.team[0]!.role = 'backend';
    expect(stepProblem(s, 'Team')).toBeUndefined();
    s.team.push({ ...s.team[0]!, key: 'b', name: 'ada ' });
    expect(stepProblem(s, 'Team')).toContain('Two agents are called "Ada"');
  });

  it('builds the setup request: repository kinds, mode, connectors and the three kinds of agents', () => {
    const s = emptyWizard();
    s.name = ' Calc ';
    s.brief = ' A calculator. ';
    s.localPath = ' /src/calc ';
    s.mode = 'pr_flow';
    s.catalogServers = ['cloudflare-docs'];
    s.libraryServers = ['lib1'];
    s.team = [
      { ...memberFromPrebuilt(prebuilt('team-lead'), []), model: 'opus' },
      { key: 't', source: 'template', ref: 'tpl1', name: 'From tpl', role: 'x', adapter: 'claude', model: '', effort: '', permissionMode: '', purpose: '' },
      { key: 'c', source: 'custom', name: 'Ada', role: 'backend', adapter: 'gemini', model: 'gemini-2.5-pro', effort: '', permissionMode: 'default', purpose: ' Writes Go. ' },
    ];
    s.installSkills = false;
    const req = buildSetup(s);
    // pull requests need GitHub: a local repository merges locally
    expect(req).toEqual({
      name: 'Calc',
      brief: 'A calculator.',
      repo: { kind: 'local', path: '/src/calc' },
      mode: 'local_merge',
      libraryServers: ['lib1'],
      catalogServers: ['cloudflare-docs'],
      agents: [
        { prebuilt: 'team-lead', name: 'Team Lead', adapter: 'claude', model: 'opus', effort: 'high', permissionMode: '' },
        { template: 'tpl1', name: 'From tpl' },
        { name: 'Ada', adapter: 'gemini', model: 'gemini-2.5-pro', effort: '', permissionMode: 'default', role: 'backend', purpose: 'Writes Go.' },
      ],
      installSkills: false,
    });
    s.repo = 'github';
    s.github = { fullName: 'acme/pay', name: 'pay' } as never;
    s.cloneDir = '/home/k/src';
    expect(buildSetup(s).repo).toEqual({ kind: 'github', github: 'acme/pay', cloneDir: '/home/k/src' });
    expect(buildSetup(s).mode).toBe('pr_flow');
    s.repo = 'none';
    expect(buildSetup(s).repo).toEqual({ kind: 'none' });
    expect(effectiveMode(s)).toBe('local_merge');
  });

  it('suggests a Team Lead, an engineer and a Code Reviewer, with unique names', () => {
    const team = suggestTeam([prebuilt('code-reviewer'), prebuilt('team-lead'), prebuilt('backend-engineer'), prebuilt('ui-engineer')]);
    expect(team.map((m) => [m.ref, m.name])).toEqual([
      ['team-lead', 'Team Lead'],
      ['backend-engineer', 'Backend Engineer'],
      ['code-reviewer', 'Code Reviewer'],
    ]);
    expect(memberFromPrebuilt(prebuilt('team-lead'), team).name).toBe('Team Lead 2');
    expect(uniqueName('Ada', [{ name: 'ada' }, { name: 'Ada 2' }])).toBe('Ada 3');
  });

  it('moves a prebuilt agent to a usable alternative when its preferred CLI is missing', () => {
    const e = prebuilt('team-lead', { claude: false, gemini: true });
    const m = memberFromPrebuilt(e, []);
    expect(m.adapter).toBe('gemini');
    expect(m.model).toBe('gemini-2.5-pro');
  });

  it('orders agent CLIs preferred first and disables missing ones with the reason', () => {
    const e = prebuilt('team-lead', { claude: true, gemini: false });
    const c = adapterChoices([adapter('codex', true), adapter('claude', true), adapter('gemini', false)], e, 'claude');
    expect(c.map((x) => [x.id, x.disabled])).toEqual([
      ['claude', false],
      ['gemini', true],
      ['codex', false],
    ]);
    expect(c[0]!.label).toBe('claude (recommended)');
    expect(c[1]!.label).toContain('not installed');
    // without an entry: the adapters as detected
    expect(adapterChoices([adapter('gemini', false)], undefined, '').map((x) => x.disabled)).toEqual([true]);
  });

  it('formats the clone target, the pushed date and the connector sign-in note', () => {
    expect(cloneTarget('C:\\Users\\k\\source\\repos\\', { name: 'pay' })).toBe('C:\\Users\\k\\source\\repos\\pay');
    expect(cloneTarget('/home/k/src', { name: 'pay' })).toBe('/home/k/src/pay');
    expect(cloneTarget('', { name: 'pay' })).toBe('');
    const now = Date.parse('2026-10-09T12:00:00Z');
    expect(pushedAgo('2026-10-09T08:00:00Z', now)).toBe('pushed today');
    expect(pushedAgo('2026-10-06T08:00:00Z', now)).toBe('pushed 3 days ago');
    expect(pushedAgo('2025-01-01T00:00:00Z', now)).toBe('pushed 1 year ago');
    expect(authNote('oauth', false)).toBe('Needs sign-in after adding.');
    expect(authNote('bearer', true)).toContain('Works without a token');
    expect(authNote('none', false)).toBe('No sign-in needed.');
  });
});
