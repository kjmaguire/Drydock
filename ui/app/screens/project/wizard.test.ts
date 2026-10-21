import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { AdapterInfo } from '../../api/types.ts';
import type { RepoFolder } from '../../api/folderClient.ts';
import { adapterChoices, authNote, buildSetup, canOpen, cloneTarget, currentCheck, effectiveMode, emptyWizard, firstIncomplete, folderHint, folderProblem, memberFromPrebuilt, memberMode, pushedAgo, stepProblem, suggestTeam, uniqueName, type TeamMember } from './wizard.ts';
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

  it('counts a CLI installed only in a WSL distro as available and names the distro (P58)', () => {
    const e = prebuilt('team-lead', { claude: false, gemini: false, codex: false });
    e.availability.adapters[0] = { adapter: 'claude', preferred: true, installed: true, usable: true, status: 'degraded', wslDistro: 'Ubuntu-26.04' };
    e.availability.preferredUsable = true;
    const c = adapterChoices([adapter('claude', false)], e, 'claude');
    expect(c[0]).toEqual({ id: 'claude', label: 'claude (recommended) - in WSL Ubuntu-26.04', disabled: false, reason: undefined, wslDistro: 'Ubuntu-26.04' });
    expect(memberFromPrebuilt(e, []).adapter).toBe('claude');
    // a custom member: adapter.list says where it is
    const inWSL: AdapterInfo = { ...adapter('claude', false), wslDistro: 'Ubuntu-26.04' };
    const custom = adapterChoices([inWSL, adapter('codex', false)], undefined, '');
    expect(custom.map((x) => [x.label, x.disabled])).toEqual([
      ['claude - in WSL Ubuntu-26.04', false],
      ['codex - not installed on this machine', true],
    ]);
  });

  it('asks for "Create a Git repository here" when the folder is not a repository, and sends it (P58)', () => {
    const s = emptyWizard();
    s.localPath = ' "D:\\Projects\\DryDock - Test Project" ';
    const check = (state: RepoFolder['state'], message = ''): RepoFolder => ({ path: 'D:\\Projects\\DryDock - Test Project', state, entries: 0, canInit: state === 'folder' || state === 'missing', message });
    // no answer yet: the core checks again when the project is made
    expect(stepProblem(s, 'Repository')).toBeUndefined();
    s.localCheck = check('missing');
    expect(currentCheck(s)?.state).toBe('missing');
    expect(stepProblem(s, 'Repository')).toContain('Create a Git repository here');
    s.initRepo = true;
    expect(stepProblem(s, 'Repository')).toBeUndefined();
    expect(buildSetup(s).repo).toEqual({ kind: 'local', path: '"D:\\Projects\\DryDock - Test Project"', init: true });
    s.localCheck = check('inside', 'D:\\Projects\\DryDock - Test Project is inside the Git repository at D:\\Projects.');
    expect(stepProblem(s, 'Repository')).toContain('inside the Git repository');
    s.localCheck = check('repository');
    expect(stepProblem(s, 'Repository')).toBeUndefined();
    // a check of an earlier text does not count
    s.localPath = 'D:\\Other';
    expect(currentCheck(s)).toBeUndefined();
    expect(folderProblem(check('folder'), false)).toContain('is not a Git repository');
    expect(folderProblem(check('no_branch', 'commit once'), true)).toBe('commit once');
  });

  it('recommends a Dev Drive on Windows and names the ones found', () => {
    expect(folderHint(undefined)).not.toContain('Dev Drive');
    expect(folderHint({ canPick: false, windows: false, devDrives: [] })).not.toContain('Dev Drive');
    expect(folderHint({ canPick: true, windows: true, devDrives: [] })).toContain('No Dev Drive was found on this PC.');
    expect(folderHint({ canPick: true, windows: true, devDrives: [], devDriveReason: 'no fixed drive on this PC uses ReFS, and a Dev Drive always does' })).toContain(
      'No Dev Drive was found on this PC: no fixed drive on this PC uses ReFS, and a Dev Drive always does.',
    );
    const h = folderHint({ canPick: true, windows: true, devDrives: [{ letter: 'D:', trusted: true }] });
    expect(h).toContain('Windows Dev Drive is recommended');
    expect(h).toContain('Dev Drive D: found.');
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

  describe('a permission mode that skips approvals needs the person\'s confirmation (M1)', () => {
    const custom = (over: Partial<TeamMember> = {}): TeamMember => ({ key: 'c', source: 'custom', name: 'Ada', role: 'backend', adapter: 'claude', model: '', effort: '', permissionMode: '', purpose: '', ...over });
    // a catalog entry that comes with a mode that skips approvals (none does today; the core would refuse it unconfirmed)
    const lead = (): ReturnType<typeof prebuilt> => {
      const e = prebuilt('team-lead');
      return { ...e, preferred: { ...e.preferred, permissionMode: 'auto' } };
    };

    it('is read from the member as the core decides it: its own mode, else the catalog entry\'s on the entry\'s own CLI', () => {
      expect(memberMode(custom({ permissionMode: 'bypassPermissions' }))).toBe('bypassPermissions');
      expect(memberMode(custom())).toBe('');
      const m = memberFromPrebuilt(lead(), []);
      expect(m.permissionMode).toBe('auto');
      expect(memberMode(m)).toBe('auto');
      // "default" chosen on the entry's own CLI: the core still gives the entry's mode, so that is what is confirmed
      expect(memberMode({ ...m, permissionMode: '' })).toBe('auto');
      // another CLI has no entry mode (a permission mode is a Claude Code setting)
      expect(memberMode({ ...m, permissionMode: '', adapter: 'gemini' })).toBe('');
      expect(memberMode({ ...m, source: 'template', permissionMode: 'dontAsk' })).toBe('dontAsk');
    });

    it('keeps the Team step from being complete until the box is ticked for the mode, and says which agent and what to do', () => {
      const s = emptyWizard();
      s.localPath = '/src/calc';
      s.team = [custom({ permissionMode: 'bypassPermissions' })];
      expect(stepProblem(s, 'Team')).toBe('Ada acts without asking (bypassPermissions): tick "I understand this agent will act without asking me first" to go on, or choose another permission mode.');
      expect(stepProblem(s, 'Review')).toContain('Team: Ada acts without asking');
      s.team[0]!.confirmed = 'bypassPermissions';
      expect(stepProblem(s, 'Team')).toBeUndefined();
      // choosing another such mode asks again; a mode that keeps approvals does not
      s.team[0]!.permissionMode = 'dontAsk';
      expect(stepProblem(s, 'Team')).toContain('(dontAsk)');
      s.team[0]!.permissionMode = 'plan';
      expect(stepProblem(s, 'Team')).toBeUndefined();
      // the entry's own mode counts for a prebuilt agent left at "default"
      s.team = [{ ...memberFromPrebuilt(lead(), []), permissionMode: '' }];
      expect(stepProblem(s, 'Team')).toContain('Team Lead acts without asking (auto)');
      s.team[0]!.confirmed = 'auto';
      expect(stepProblem(s, 'Team')).toBeUndefined();
    });

    it('sends confirmSkipsApprovals: true for the agents whose mode was confirmed, and for no other', () => {
      const s = emptyWizard();
      s.localPath = '/src/calc';
      const entry = lead();
      s.team = [
        { ...memberFromPrebuilt(entry, []), confirmed: 'auto' },
        { key: 't', source: 'template', ref: 'tpl1', name: 'From tpl', role: 'x', adapter: 'claude', model: '', effort: '', permissionMode: 'bypassPermissions', purpose: '', confirmed: 'bypassPermissions' },
        custom({ name: 'Ada', permissionMode: 'dontAsk', confirmed: 'dontAsk' }),
        custom({ key: 'd', name: 'Bob', permissionMode: 'plan', confirmed: 'dontAsk' }), // ticked, then the mode was changed to one that keeps approvals
        custom({ key: 'e', name: 'Cy', permissionMode: 'acceptEdits' }),
      ];
      const agents = buildSetup(s).agents;
      expect(agents.map((a) => [a.name, a.permissionMode, a.confirmSkipsApprovals])).toEqual([
        ['Team Lead', 'auto', true],
        ['From tpl', undefined, true],
        ['Ada', 'dontAsk', true],
        ['Bob', 'plan', undefined],
        ['Cy', 'acceptEdits', undefined],
      ]);
      expect('confirmSkipsApprovals' in agents[3]!).toBe(false);
      expect('confirmSkipsApprovals' in agents[4]!).toBe(false);
      // a box ticked for another mode that skips approvals does not carry over
      s.team = [custom({ permissionMode: 'auto', confirmed: 'bypassPermissions' })];
      expect('confirmSkipsApprovals' in buildSetup(s).agents[0]!).toBe(false);
    });
  });
});
