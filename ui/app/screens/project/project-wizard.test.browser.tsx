import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { LicenseView } from '../../api/pkgTypes.ts';
import type { Project, SetupRequest } from '../../api/types.ts';
import { expiredLicense, lic, unconfiguredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { ProjectCreateScreen } from './project-create-screen.tsx';
import { adapterInfo, CATALOG, catalogServer, prebuilt } from './wizard-fixtures.ts';

afterEach(cleanup);

const project: Project = { id: 'p9', name: 'Payments', repoPath: '', hasRepo: false, defaultBranch: '', mergeStrategy: 'merge', mode: 'local_merge', brief: '', created: 0 };

function services(view: LicenseView = lic(), client: Partial<Client> = {}) {
  const s = testServices({
    projectList: async () => [],
    licenseStatus: async () => view,
    appInfo: async () => ({}) as never,
    githubStatus: async () => ({ installed: false, loggedIn: false, login: '', error: 'the GitHub CLI (gh) is not installed: install it from https://cli.github.com and run `gh auth login`' }),
    agentCatalogList: async () => CATALOG.map((e) => prebuilt(e.id, { codex: false })),
    adapterList: async () => [adapterInfo('claude', true, ['opus', 'sonnet']), adapterInfo('gemini', true, ['gemini-2.5-pro']), adapterInfo('codex', false)],
    agentTemplateList: async () => [],
    mcpLibList: async () => [],
    mcpCatalog: async () => [catalogServer('cloudflare-docs', { display: 'Cloudflare docs', auth: 'none' }), catalogServer('linear', { display: 'Linear', auth: 'oauth' }), catalogServer('known', { inLibrary: true })],
    ...client,
  });
  s.workspace.start();
  return s;
}

type R = ReturnType<typeof mount>;
const btn = (r: R, name: string) => byText<HTMLButtonElement>(r.container, 'button', name);
const stepBtn = (r: R, name: string) => Array.from(r.$$('nav[aria-label="New project steps"] button')).find((b) => b.textContent!.endsWith(name)) as HTMLButtonElement;
const current = (r: R) => r.$('form')!.getAttribute('data-step');
const next = async (r: R) => {
  await r.act(() => btn(r, 'Next').click());
};
const members = (r: R) => Array.from(r.$$('[data-testid="team-member"]')) as HTMLElement[];
const radio = (r: R, label: string) => Array.from(r.$$('[role="radiogroup"][aria-label="Repository source"] label')).find((l) => l.textContent!.trim() === label)!.querySelector('input')!;

/** Basics with a name, Repository "Later", through Connectors, to the Team step. */
async function toTeam(r: R) {
  type(byLabel(r.container, 'Project name'), 'Payments');
  await next(r);
  await r.act(() => radio(r, 'Later').click());
  await next(r);
  await next(r);
  await waitFor(() => expect(current(r)).toBe('Team'), r);
}

describe('New project wizard', () => {
  it('shows the five steps, starts at Basics and opens a step only after the earlier ones are complete', async () => {
    const r = mount(<ProjectCreateScreen />, services());
    expect(r.$('h1')!.textContent).toContain('New project');
    const names = Array.from(r.$$('nav[aria-label="New project steps"] button')).map((b) => b.textContent!.replace(/^\d/, ''));
    expect(names).toEqual(['Basics', 'Repository', 'Connectors', 'Team', 'Review']);
    expect(stepBtn(r, 'Basics').getAttribute('aria-current')).toBe('step');
    expect(stepBtn(r, 'Repository').disabled).toBe(false);
    expect(stepBtn(r, 'Connectors').disabled).toBe(true); // the repository step is not complete yet
    // the brief counts its characters
    type(byLabel<HTMLTextAreaElement>(r.container, 'Brief'), 'A payments API. Keep changes small.');
    r.root.flush();
    expect(r.$('[data-testid="brief-count"]')!.textContent).toBe('35 / 8000');
    type(byLabel<HTMLTextAreaElement>(r.container, 'Brief'), 'x'.repeat(8001));
    r.root.flush();
    expect(r.$('[data-testid="brief-count"]')!.getAttribute('data-over')).toBe('true');
    expect(btn(r, 'Next').disabled).toBe(true);
  });

  it('first run says welcome and still uses the wizard', async () => {
    const r = mount(<ProjectCreateScreen first />, services());
    expect(r.$('h1')!.textContent).toContain('Welcome to Drydock');
    expect(r.$('[aria-label="How Drydock works"]')).not.toBeNull();
    expect(r.$('form')!.getAttribute('aria-label')).toBe('Create your first project');
  });

  it('Repository: a local path, Later with its note (a name is then required), and GitHub explained when gh is missing', async () => {
    const r = mount(<ProjectCreateScreen />, services());
    await next(r);
    expect(current(r)).toBe('Repository');
    expect(radio(r, 'Local folder').checked).toBe(true);
    expect(btn(r, 'Next').disabled).toBe(true);
    type(byLabel(r.container, 'Repository path'), 'C:\\src\\pay');
    r.root.flush();
    expect(btn(r, 'Next').disabled).toBe(false);
    // pull requests need GitHub
    const delivery = byLabel<HTMLSelectElement>(r.container, 'Delivery');
    expect(Array.from(delivery.options).find((o) => o.value === 'pr_flow')!.disabled).toBe(true);

    await r.act(() => radio(r, 'Later').click());
    expect(r.$('[data-testid="repo-later"]')!.textContent).toContain('attach a repository later in Project settings');
    expect(() => byLabel(r.container, 'Delivery')).toThrow();
    expect(r.$('.problem')!.textContent).toContain('needs a name');
    expect(btn(r, 'Next').disabled).toBe(true);

    await r.act(() => radio(r, 'GitHub').click());
    await waitFor(() => expect(r.$('[data-testid="github-unavailable"]')!.textContent).toContain('the GitHub CLI (gh) is not installed'), r);
    expect(r.$('[data-testid="github-unavailable"]')!.textContent).toContain('Choose Local folder or Later instead.');
  });

  it('Connectors: library servers and built-in ones not yet in the library, with the sign-in note', async () => {
    const r = mount(
      <ProjectCreateScreen />,
      services(lic(), { mcpLibList: async () => [{ id: 'lib1', display: 'Postgres', transport: 'stdio', argv: ['pg-mcp'], secretEnv: [], hasToken: false, guard: true, readOnlyTools: [], tools: [], discoveredAt: 0, projects: [], createdAt: 0, updatedAt: 0 }] }),
    );
    type(byLabel(r.container, 'Project name'), 'Payments');
    await next(r);
    await r.act(() => radio(r, 'Later').click());
    await next(r);
    await waitFor(() => byLabel(r.container, /^Postgres/), r);
    expect(r.container.textContent).toContain('In your library');
    expect(r.container.textContent).toContain('Built in');
    const linear = byLabel<HTMLInputElement>(r.container, /^Linear/);
    expect(linear.closest('label')!.textContent).toContain('Needs sign-in after adding.');
    expect(byLabel(r.container, /^Cloudflare docs/).closest('label')!.textContent).toContain('No sign-in needed.');
    expect(() => byLabel(r.container, /^known/)).toThrow(); // already in the library: listed there
  });

  it('Team: suggests a Team Lead, an engineer and a Code Reviewer; rows edit the CLI settings; gallery, custom agent and remove', async () => {
    const r = mount(<ProjectCreateScreen />, services());
    await toTeam(r);
    await waitFor(() => expect(members(r).map((m) => m.getAttribute('data-ref'))).toEqual(['team-lead', 'backend-engineer', 'code-reviewer']), r);
    expect(r.$('[data-testid="team-list"]')!.getAttribute('data-ready')).toBe('true');
    const lead = members(r)[0]!;
    const cli = byLabel<HTMLSelectElement>(lead, 'Agent CLI');
    expect(cli.value).toBe('claude');
    expect(byLabel<HTMLInputElement>(lead, 'Model').value).toBe('opus');
    // the CLI that is not installed is offered disabled, with the reason
    const codex = Array.from(cli.options).find((o) => o.value === 'codex')!;
    expect(codex.disabled).toBe(true);
    expect(codex.textContent).toContain('not installed');
    type(cli, 'gemini');
    r.root.flush();
    expect(byLabel<HTMLInputElement>(members(r)[0]!, 'Model').value).toBe('gemini-2.5-pro');
    // remove the reviewer, add the UI Engineer from the gallery
    await r.act(() => btn(r, 'Remove Code Reviewer from the team').click());
    expect(members(r)).toHaveLength(2);
    await r.act(() => btn(r, 'Add UI Engineer').click());
    expect(members(r).map((m) => m.getAttribute('data-ref'))).toEqual(['team-lead', 'backend-engineer', 'ui-engineer']);
    expect(members(r)[2]!.textContent).toContain('4 skills');
    // a custom agent
    await r.act(() => (r.$('details summary') as HTMLElement).click());
    type(byLabel(r.container, 'Agent name'), 'Ada');
    type(byLabel(r.container, 'Role'), 'backend');
    r.root.flush();
    await r.act(() => btn(r, 'Add to team').click());
    expect(members(r).map((m) => m.getAttribute('data-source'))).toEqual(['prebuilt', 'prebuilt', 'prebuilt', 'custom']);
    // remove all, and the suggestion does not come back
    await r.act(() => btn(r, 'Remove all').click());
    expect(members(r)).toHaveLength(0);
    await r.act(() => btn(r, 'Back').click());
    await next(r);
    expect(members(r)).toHaveLength(0);
    expect(byLabel<HTMLInputElement>(r.container, /^Install the skills these agents come with/).checked).toBe(true);
  });

  it('Review: summarizes, creates with project.setup, lists the warnings and opens the project', async () => {
    const sent: SetupRequest[] = [];
    let lists = 0;
    const navigated: string[] = [];
    const s = services(lic(), {
      projectSetup: async (req) => (
        sent.push(req),
        { project, agents: [{ id: 'a1', name: 'Team Lead' } as never], warnings: ['connector linear: sign in on the Library page'] }
      ),
      projectList: async () => (lists++, lists > 1 ? [project] : []),
    });
    const r = mount(<ProjectCreateScreen onNavigate={(h) => navigated.push(h)} />, s);
    type(byLabel(r.container, 'Project name'), ' Payments ');
    type(byLabel<HTMLTextAreaElement>(r.container, 'Brief'), 'Small steps.');
    await next(r);
    await r.act(() => radio(r, 'Later').click());
    await next(r);
    await waitFor(() => byLabel(r.container, /^Linear/), r);
    await r.act(() => byLabel<HTMLInputElement>(r.container, /^Linear/).click());
    await next(r);
    await waitFor(() => expect(members(r)).toHaveLength(3), r);
    await r.act(() => btn(r, 'Remove Backend Engineer from the team').click());
    await r.act(() => btn(r, 'Remove Code Reviewer from the team').click());
    await r.act(() => byLabel<HTMLInputElement>(r.container, /^Install the skills these agents come with/).click());
    await next(r);
    expect(current(r)).toBe('Review');
    const summary = r.$('dl[aria-label="Summary"]')!.textContent!;
    expect(summary).toContain('Payments');
    expect(summary).toContain('None yet (attach one later in Project settings)');
    expect(summary).toContain('Linear');
    expect(summary).toContain('Team Lead');
    expect(summary).toContain('Do not install skills');
    await r.act(() => btn(r, 'Create project').click());
    await waitFor(() => expect(r.$('[data-testid="setup-result"]')).not.toBeNull(), r);
    expect(sent).toEqual([
      {
        name: 'Payments',
        brief: 'Small steps.',
        repo: { kind: 'none' },
        mode: 'local_merge',
        libraryServers: [],
        catalogServers: ['linear'],
        agents: [{ prebuilt: 'team-lead', name: 'Team Lead', adapter: 'claude', model: 'opus', effort: 'high', permissionMode: '' }],
        installSkills: false,
      },
    ]);
    expect(r.$('[data-testid="setup-warnings"]')!.textContent).toContain('connector linear: sign in on the Library page');
    expect(r.$('[data-testid="setup-result"]')!.textContent).toContain('1 agent added: Team Lead');
    // the shell keeps the first-run wizard on screen while it shows the result
    expect(s.workspace.setupOpen).toBe(true);
    await r.act(() => btn(r, 'Open project').click());
    expect(s.workspace.setupOpen).toBe(false);
    await waitFor(() => expect(navigated).toEqual(['/']), r);
    await waitFor(() => expect(s.workspace.projectId).toBe('p9'), r);
  });

  it('a license that is not valid turns Create off with the reason; a core refusal is shown verbatim', async () => {
    const r = mount(<ProjectCreateScreen />, services(expiredLicense));
    await waitFor(() => expect(r.$('[data-testid="readonly-note"]')).not.toBeNull(), r);
    await toTeam(r);
    await next(r);
    const create = btn(r, 'Create project');
    expect(create.disabled).toBe(true);
    expect(create.title).toBe(expiredLicense.banner!);

    const refusal = '/x is not a usable Git repository: not a git repository';
    const r2 = mount(
      <ProjectCreateScreen />,
      services(unconfiguredLicense, {
        projectSetup: async () => {
          throw new Error(refusal);
        },
      }),
    );
    await waitFor(() => expect(r2.services.workspace.license.view).toBeTruthy(), r2);
    expect(r2.$('[data-testid="readonly-note"]')).toBeNull();
    await next(r2);
    type(byLabel(r2.container, 'Repository path'), '/x');
    r2.root.flush();
    await next(r2);
    await next(r2);
    await waitFor(() => expect(members(r2)).toHaveLength(3), r2);
    await next(r2);
    await r2.act(() => btn(r2, 'Create project').click());
    await waitFor(() => expect(r2.$('[role="alert"]')!.textContent).toContain(refusal), r2);
  });
});
