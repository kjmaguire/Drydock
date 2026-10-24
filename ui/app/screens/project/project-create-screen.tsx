// The new-project wizard (/project/new, and the first run when there is no project): Basics (name, brief), Repository
// (GitHub, a local folder, or later), Connectors (MCP servers), Team (prebuilt, custom and template agents with their CLI
// settings) and Review. Create sends one project.setup request; the core validates it before creating anything and reports
// what failed afterwards as warnings, which the result lists before the project is opened.

import { css, navigate, on, ref, type Handle } from 'remix/component';
import { Check, FolderGit2, FolderPlus, GitMerge, ShieldCheck, Users } from 'lucide';
import { PROJECT_BRIEF_MAX, type ProjectMode, type SetupResult } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox, Field } from '../../ui/form.tsx';
import { Select } from '../../ui/select.tsx';
import { Icon, type IconNode } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { MODES } from './project.ts';
import { RepoPicker } from './repo-picker.tsx';
import { TeamBuilder } from './team-builder.tsx';
import { FolderField } from './folder-field.tsx';
import { authNote, buildSetup, canOpen, cloneTarget, currentCheck, effectiveMode, emptyWizard, STEPS, stepProblem, suggestTeam, type RepoChoice, type Step } from './wizard.ts';

const wrapCss = css({ display: 'grid', gap: t.s5, width: '100%', maxWidth: '880px', margin: `${t.s4} auto ${t.s8}` });
const introCss = css({ margin: '0', color: t.textMuted, fontSize: t.textMd, lineHeight: '1.6', maxWidth: '640px' });

const stepperCss = css({
  '& ol': { display: 'flex', alignItems: 'center', gap: t.s2, margin: '0', padding: '0', listStyle: 'none', flexWrap: 'wrap' },
  '& li': { display: 'flex', alignItems: 'center', gap: t.s2 },
  '& li + li::before': { content: '""', width: '24px', height: '1px', background: t.border },
  '& button': {
    display: 'inline-flex',
    alignItems: 'center',
    gap: t.s2,
    height: '28px',
    padding: `0 ${t.s2} 0 4px`,
    border: '0',
    borderRadius: t.rMd,
    background: 'transparent',
    color: t.textMuted,
    fontSize: t.textMd,
    fontWeight: '500',
  },
  '& button:hover:not(:disabled)': { background: t.bgHover, color: t.text },
  '& button:disabled': { color: t.textFaint, cursor: 'default' },
  '& button[aria-current="step"]': { color: t.text, background: t.bgActive },
  '& .num': {
    display: 'inline-grid',
    placeItems: 'center',
    width: '20px',
    height: '20px',
    borderRadius: '50%',
    border: `1px solid ${t.borderStrong}`,
    fontSize: t.textXs,
    fontWeight: '600',
    fontVariantNumeric: 'tabular-nums',
  },
  '& button[aria-current="step"] .num': { borderColor: t.text, background: t.text, color: t.bgApp },
  '& button[data-done="true"] .num': { borderColor: t.border, background: t.bgActive, color: t.textMuted },
});

const bodyCss = css({
  display: 'grid',
  gap: t.s4,
  padding: t.s5,
  border: `1px solid ${t.border}`,
  borderRadius: t.rXl,
  background: t.bgSurface,
  minWidth: '0',
  '& > h2': { margin: '0', fontSize: t.textLg, fontWeight: '600', outline: 'none' },
  '& > .lead': { margin: '-8px 0 0', color: t.textMuted, fontSize: t.textSm },
  '& input:not([type=checkbox]):not([type=radio]), & textarea': { width: '100%' },
});
const footCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  '& .problem': { color: t.textMuted, fontSize: t.textSm },
  '& .end': { marginLeft: 'auto', display: 'flex', gap: t.s2 },
});
const countCss = css({ display: 'flex', justifyContent: 'space-between', gap: t.s3, '& .n': { fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' }, '& .n[data-over="true"]': { color: t.danger } });

const segCss = css({
  display: 'inline-flex',
  padding: '2px',
  gap: '2px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgApp,
  width: 'fit-content',
  '& label': { position: 'relative', display: 'inline-flex', alignItems: 'center', gap: '6px', height: '28px', padding: `0 ${t.s3}`, borderRadius: t.rSm, color: t.textMuted, fontWeight: '500', cursor: 'pointer' },
  '& label:hover': { color: t.text },
  '& label:has(input:checked)': { background: t.bgRaised, color: t.text, boxShadow: t.shadowSm },
  '& label:has(input:focus-visible)': { boxShadow: t.focus },
  // the radio covers its label, invisible: the whole segment is the control (and stays clickable for tests and AT)
  '& input': { position: 'absolute', inset: '0', width: '100%', height: '100%', margin: '0', opacity: '0', cursor: 'pointer' },
});

const connListCss = css({ display: 'grid', gap: t.s1, '& h3': { margin: `${t.s2} 0 0`, fontSize: t.textXs, fontWeight: '600', color: t.textFaint, letterSpacing: '0.04em', textTransform: 'uppercase' } });

const reviewCss = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(120px, max-content) 1fr',
  gap: `${t.s3} ${t.s5}`,
  margin: '0',
  '& dt': { color: t.textMuted, fontSize: t.textSm },
  '& dd': { margin: '0', minWidth: '0', overflowWrap: 'anywhere' },
  '& ul': { margin: '0', paddingLeft: '18px' },
  '& code': { fontSize: t.textSm },
  '& .brief': { whiteSpace: 'pre-wrap', color: t.textMuted, maxHeight: '120px', overflow: 'auto' },
});

const resultCss = css({ display: 'grid', gap: t.s4, '& h2': { margin: '0', fontSize: t.textLg, fontWeight: '600', display: 'flex', alignItems: 'center', gap: t.s2 }, '& ul': { margin: '4px 0 0', paddingLeft: '18px' } });

const pointsCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fit, minmax(190px, 1fr))',
  gap: t.s3,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& li': { display: 'flex', alignItems: 'flex-start', gap: t.s2, color: t.textMuted, fontSize: t.textSm },
  '& li > span:first-child': { display: 'inline-flex', marginTop: '2px', color: t.textFaint },
  '& strong': { color: t.text, fontWeight: '500' },
});

const POINTS: [IconNode, string, string][] = [
  [Users, 'A team of real agents.', 'Each agent is a coding CLI on this machine with a role.'],
  [FolderGit2, 'Separate worktrees.', 'Each task works in its own worktree and branch.'],
  [GitMerge, 'You approve every merge.', 'Nothing lands on your branch until you say so.'],
  [ShieldCheck, 'Your code stays here.', 'Drydock never uploads source code.'],
];

const REPO_CHOICES: [RepoChoice, string][] = [
  ['github', 'GitHub'],
  ['local', 'Local folder'],
  ['none', 'Later'],
];

/** `onNavigate` replaces the router's navigate (tests). */
export function ProjectCreateScreen(handle: Handle<{ first?: boolean; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const st = emptyWizard();
  let step: Step = 'Basics';
  let repoTouched = false;
  let result: SetupResult | undefined;
  let heading: HTMLElement | undefined;
  let moved = false;

  handle.signal.addEventListener('abort', () => s.workspace.setSetupOpen(false));
  const gh = query(handle, { load: () => s.client.githubStatus() });
  const catalog = query(handle, { load: () => s.client.agentCatalogList() });
  const adapters = query(handle, { load: () => s.client.adapterList() });
  const templates = query(handle, { load: () => s.client.agentTemplateList() });
  const library = query(handle, { load: () => s.client.mcpLibList() });
  const mcpCatalog = query(handle, { load: () => s.client.mcpCatalog() });

  const update = () => void handle.update();
  const go = (to: Step) => {
    step = to;
    moved = true;
    update();
  };
  const toggle = (list: string[], id: string, on: boolean) => (on ? [...list.filter((x) => x !== id), id] : list.filter((x) => x !== id));

  const create = async () => {
    // before the setup starts: it creates the project first, and the shell must keep this wizard (not swap in the new
    // project's pages) until it has shown what the setup did
    s.workspace.setSetupOpen(true);
    const r = await act.run('create', () => s.client.projectSetup(buildSetup(st)));
    if (handle.signal.aborted) return; // the person left for another page meanwhile
    if (!r) {
      s.workspace.setSetupOpen(false);
      return;
    }
    result = r;
    s.workspace.projects.reload();
    update();
  };

  const open = () => {
    if (!result) return;
    s.workspace.setSetupOpen(false);
    s.workspace.selectProject(result.project.id);
    s.workspace.projects.reload();
    if (handle.props.onNavigate) handle.props.onNavigate(routes.home.href());
    else void navigate(routes.home.href());
  };

  const basics = () => {
    const n = st.brief.trim().length;
    return (
      <>
        <Field label="Project name" hint="Required for a project without a repository; otherwise it defaults to the repository's name.">
          {(id) => <input id={id} value={st.name} autocomplete="off" placeholder="e.g. Payments API" data-autofocus mix={on('input', (e) => ((st.name = e.currentTarget.value), update()))} />}
        </Field>
        <Field
          label="Brief"
          hint={
            <span mix={countCss}>
              <span>What is this project? How should the team work? Every agent reads this.</span>
              <span class="n" data-over={n > PROJECT_BRIEF_MAX ? 'true' : undefined} data-testid="brief-count">
                {n} / {PROJECT_BRIEF_MAX}
              </span>
            </span>
          }
        >
          {(id) => (
            <textarea
              id={id}
              rows={6}
              value={st.brief}
              placeholder="What is this project? How should the team work?"
              mix={on('input', (e) => ((st.brief = e.currentTarget.value), update()))}
            />
          )}
        </Field>
      </>
    );
  };

  const repository = () => (
    <>
      <div role="radiogroup" aria-label="Repository source" mix={segCss}>
        {REPO_CHOICES.map(([v, label]) => (
          <label key={v}>
            <input type="radio" name={`repo-${handle.id}`} value={v} checked={st.repo === v} mix={on('change', () => ((st.repo = v), (repoTouched = true), update()))} />
            {label}
          </label>
        ))}
      </div>
      <div key={`repo-${st.repo}`} style={{ display: 'grid', gap: 'var(--space-4)', minWidth: '0' }}>
      {st.repo === 'github' ? (
        <RepoPicker
          selected={st.github}
          cloneDir={st.cloneDir}
          onSelect={(r) => ((st.github = r), update())}
          onCloneDir={(d) => ((st.cloneDir = d), update())}
          unavailableHint="Choose Local folder or Later instead."
        />
      ) : st.repo === 'local' ? (
        <FolderField
          value={st.localPath}
          onChange={(v) => ((st.localPath = v), update())}
          check={st.localCheck}
          onCheck={(c) => ((st.localCheck = c), update())}
          initRepo={st.initRepo}
          onInitRepo={(v) => ((st.initRepo = v), update())}
        />
      ) : (
        <Notice tone="info" testId="repo-later">
          You can attach a repository later in Project settings; until then agents can plan and talk but not change code.
        </Notice>
      )}
      {st.repo !== 'none' ? (
        <Field label="Delivery" hint={st.repo === 'github' ? 'How an approved task reaches your branch.' : 'Pull requests need a GitHub repository.'}>
          {(id) => (
            <Select
              id={id}
              value={effectiveMode(st)}
              options={MODES.map(([v, label]) => ({ value: v, label, disabled: v === 'pr_flow' && st.repo !== 'github' }))}
              onChange={(v) => ((st.mode = v as ProjectMode), update())}
            />
          )}
        </Field>
      ) : null}
      </div>
    </>
  );

  const connectors = () => {
    const lib = library.data ?? [];
    const cat = (mcpCatalog.data ?? []).filter((c) => !c.inLibrary);
    if ((library.loading && !library.data) || (mcpCatalog.loading && !mcpCatalog.data)) return <Loading label="Loading connectors..." />;
    return (
      <div mix={connListCss}>
        <ErrorNote error={library.error ?? mcpCatalog.error} what="Could not list connectors" />
        {lib.length === 0 && cat.length === 0 ? (
          <Empty title="No connectors" icon={FolderGit2}>
            <p>Add MCP servers on the Integrations page later.</p>
          </Empty>
        ) : null}
        {lib.length > 0 ? <h3>In your library</h3> : null}
        {lib.map((x) => (
          <Checkbox
            key={x.id}
            label={x.display}
            description={x.transport === 'http' ? x.url : (x.argv ?? []).join(' ')}
            checked={st.libraryServers.includes(x.id)}
            onChange={(v) => ((st.libraryServers = toggle(st.libraryServers, x.id, v)), update())}
          />
        ))}
        {cat.length > 0 ? <h3>Built in</h3> : null}
        {cat.map((x) => (
          <Checkbox
            key={x.id}
            label={x.display}
            description={`${x.description} ${authNote(x.auth, x.authOptional)}`}
            checked={st.catalogServers.includes(x.id)}
            onChange={(v) => ((st.catalogServers = toggle(st.catalogServers, x.id, v)), update())}
          />
        ))}
      </div>
    );
  };

  /** Offers the default team once the catalog has answered (or failed); true when it has. */
  const offerTeam = () => {
    const ready = !!catalog.data || !!catalog.error;
    if (!st.suggested && ready) {
      st.suggested = true;
      if (st.team.length === 0) st.team = suggestTeam(catalog.data ?? []);
    }
    return ready;
  };

  const team = () => {
    const ready = offerTeam();
    return (
      <>
        <ErrorNote error={catalog.error} what="Could not load the prebuilt agents" />
        {!ready ? <Loading label="Loading agents..." /> : null}
        <TeamBuilder
          team={st.team}
          catalog={catalog.data ?? []}
          adapters={adapters.data ?? []}
          templates={templates.data ?? []}
          installSkills={st.installSkills}
          ready={ready}
          onChange={(x) => ((st.team = x), update())}
          onInstallSkills={(v) => ((st.installSkills = v), update())}
        />
      </>
    );
  };

  const review = () => {
    const libNames = st.libraryServers.map((id) => library.data?.find((x) => x.id === id)?.display ?? id);
    const catNames = st.catalogServers.map((id) => mcpCatalog.data?.find((x) => x.id === id)?.display ?? id);
    const repoText =
      st.repo === 'github' && st.github ? (
        <span>
          GitHub <strong>{st.github.fullName}</strong>, cloned to <code>{cloneTarget(st.cloneDir, st.github)}</code>
        </span>
      ) : st.repo === 'local' ? (
        <span>
          <code>{st.localPath.trim()}</code>
          {st.initRepo && currentCheck(st)?.state !== 'repository' ? <span data-testid="review-init"> (Drydock makes a new Git repository here, branch main)</span> : null}
        </span>
      ) : (
        <span>None yet (attach one later in Project settings)</span>
      );
    return (
      <>
        <dl mix={reviewCss} aria-label="Summary">
          <dt>Name</dt>
          <dd>{st.name.trim() || <span style={{ color: 'var(--text-faint)' }}>from the repository</span>}</dd>
          <dt>Brief</dt>
          <dd>{st.brief.trim() ? <div class="brief">{st.brief.trim()}</div> : <span style={{ color: 'var(--text-faint)' }}>none</span>}</dd>
          <dt>Repository</dt>
          <dd>{repoText}</dd>
          {st.repo !== 'none' ? (
            <>
              <dt>Delivery</dt>
              <dd>{MODES.find(([v]) => v === effectiveMode(st))?.[1]}</dd>
            </>
          ) : null}
          <dt>Connectors</dt>
          <dd>{libNames.length + catNames.length === 0 ? 'none' : [...libNames, ...catNames].join(', ')}</dd>
          <dt>Team</dt>
          <dd>
            {st.team.length === 0 ? (
              'no agents (add them on the Team page later)'
            ) : (
              <ul>
                {st.team.map((m) => (
                  <li key={m.key}>
                    <strong>{m.name.trim()}</strong> ({m.role}) {m.source === 'template' ? 'from a template' : [m.adapter, m.model, m.effort].filter(Boolean).join(' · ')}
                  </li>
                ))}
              </ul>
            )}
          </dd>
          <dt>Skills</dt>
          <dd>{st.installSkills ? 'Install the skills the prebuilt agents come with' : 'Do not install skills'}</dd>
        </dl>
        <ErrorNote error={act.error} what="Project not created" />
      </>
    );
  };

  const resultView = (r: SetupResult) => (
    <section mix={[bodyCss, resultCss]} aria-label="Project created" data-testid="setup-result">
      <h2>
        <Icon icon={Check} size={18} />
        {r.project.name} is ready
      </h2>
      <p style={{ margin: '0', color: 'var(--text-muted)' }}>
        {r.clone ? (
          <>
            Cloned to <code>{r.clone.path}</code>.{' '}
          </>
        ) : null}
        {r.agents.length === 0 ? 'No agents yet.' : `${r.agents.length} ${r.agents.length === 1 ? 'agent' : 'agents'} added: ${r.agents.map((a) => a.name).join(', ')}.`}
      </p>
      {r.warnings.length > 0 ? (
        <Notice tone="warn" title="Some things need your attention." testId="setup-warnings">
          <ul>
            {r.warnings.map((w, i) => (
              <li key={i}>{w}</li>
            ))}
          </ul>
        </Notice>
      ) : null}
      <div mix={footCss}>
        <span class="end">
          <Button variant="primary" icon={FolderGit2} mix={on('click', open)}>
            Open project
          </Button>
        </span>
      </div>
    </section>
  );

  const LEAD: Record<Step, string> = {
    Basics: 'Name the project and tell the team what it is about.',
    Repository: 'The code the agents work on. You can also start without one.',
    Connectors: 'MCP servers the project can use. Agents are granted them individually.',
    Team: 'The agents of this project and the agent CLI each one runs.',
    Review: 'Check everything, then create the project.',
  };

  return () => {
    const first = !!handle.props.first;
    const ro = s.workspace.license;
    if (!repoTouched && gh.data?.loggedIn && st.repo === 'local' && !st.localPath) st.repo = 'github';
    const i = STEPS.indexOf(step);
    // the default team is offered before the step's problem is read: a suggested agent can be one that needs confirming
    if (step === 'Team') offerTeam();
    const problem = stepProblem(st, step);
    if (moved) {
      moved = false;
      handle.queueTask(() => heading?.focus());
    }
    return (
      <Page title={first ? 'Welcome to Drydock' : 'New project'} icon={FolderPlus}>
        <div mix={wrapCss}>
          {first && !result ? (
            <>
              <p mix={introCss}>Drydock runs a team of real coding agents on your machine. Set up a project in a few steps; a repository can come now or later.</p>
              <ul mix={pointsCss} aria-label="How Drydock works">
                {POINTS.map(([icon, head, text]) => (
                  <li key={head}>
                    <span>
                      <Icon icon={icon} size={15} />
                    </span>
                    <span>
                      <strong>{head}</strong> {text}
                    </span>
                  </li>
                ))}
              </ul>
            </>
          ) : null}
          <ReadOnlyNote />
          {result ? (
            resultView(result)
          ) : (
            <>
              <nav mix={stepperCss} aria-label="New project steps">
                <ol>
                  {STEPS.map((x, j) => {
                    const done = j < i && !stepProblem(st, x);
                    return (
                      <li key={x}>
                        <button type="button" aria-current={x === step ? 'step' : undefined} data-done={done ? 'true' : undefined} disabled={x !== step && !canOpen(st, x)} mix={on('click', () => go(x))}>
                          <span class="num" aria-hidden="true">
                            {done ? <Icon icon={Check} size={11} strokeWidth={2.5} /> : j + 1}
                          </span>
                          {x}
                        </button>
                      </li>
                    );
                  })}
                </ol>
              </nav>
              <form
                mix={[bodyCss, on('submit', (e) => {
                    e.preventDefault();
                    if (step === 'Review') void create();
                    else if (!problem) go(STEPS[i + 1]!);
                  })]}
                aria-label={first ? 'Create your first project' : 'New project'}
                data-step={step}
              >
                <h2 tabIndex={-1} mix={ref((n) => (heading = n as HTMLElement))}>
                  {step}
                  {step === 'Team' && st.team.length > 0 ? (
                    <>
                      {' '}
                      <Badge tone="muted">{String(st.team.length)}</Badge>
                    </>
                  ) : null}
                </h2>
                <p class="lead">{LEAD[step]}</p>
                <div key={`step-${step}`} style={{ display: 'grid', gap: 'var(--space-4)', minWidth: '0' }}>
                  {step === 'Basics' ? basics() : step === 'Repository' ? repository() : step === 'Connectors' ? connectors() : step === 'Team' ? team() : review()}
                </div>
                <div mix={footCss}>
                  {i > 0 ? (
                    <Button variant="secondary" mix={on('click', () => go(STEPS[i - 1]!))}>
                      Back
                    </Button>
                  ) : null}
                  {problem ? (
                    <span class="problem" role="status">
                      {problem}
                    </span>
                  ) : null}
                  <span class="end">
                    {step === 'Review' ? (
                      <Button type="submit" variant="primary" icon={FolderPlus} disabled={!!act.busy || ro.readOnly || !!problem} title={ro.readOnly ? ro.reason : undefined}>
                        {act.busy ? 'Creating...' : 'Create project'}
                      </Button>
                    ) : (
                      <Button type="submit" variant="primary" disabled={!!problem}>
                        Next
                      </Button>
                    )}
                  </span>
                </div>
              </form>
            </>
          )}
        </div>
      </Page>
    );
  };
}
