// Skills: instructions agents read. The workspace Library fills itself from every enabled registry (decision P48); a project
// adds skills from the Library, and adding is the person's acceptance. Skills found in the repository are offered and accepted
// here. Skill text is outside text, rendered inert; scripts are never run and requested tool permissions are recorded and
// ignored. Usage says which skills earn their tokens (decision P49).

import { css, on, type Handle } from 'remix/component';
import { BookOpen, Cpu, GitBranch, Plus, RefreshCw, ScanSearch, ShieldAlert, ShieldCheck, Users } from 'lucide';
import type { LibrarySkill, Skill, SkillDelivery, SkillRegistry, SkillRegistryStatus, SkillScan } from '../../api/extTypes.ts';
import type { Agent } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, kv, muted } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox, Field, formActions, formGrid } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section, card, tableCard } from '../../ui/layout.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { emptyLine, Tile, tileGrid } from '../../ui/tiles.tsx';
import { t } from '../../ui/tokens.ts';
import { AudienceEditor } from './audience.tsx';
import { Recommendations } from './recommendations.tsx';
import { pickKey, ProjectSkillRows, SkillLogo, SkillRows } from './skill-rows.tsx';
import {
  addSelectedText,
  addedText,
  audienceText,
  coverageText,
  formatCount,
  inWords,
  libraryGroups,
  listOr,
  newInLibrary,
  notInProject,
  onTeam,
  recommendationBadge,
  registryStatusText,
  registryTileText,
  registryUpdates,
  scanText,
  skillBrand,
  skillTone,
  sortUsage,
  syncChanges,
  usageTokens,
} from './skills.ts';

const panelCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  '& header': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    flexWrap: 'wrap',
    minWidth: '0',
  },
  '& h3': {
    fontSize: t.textMd,
    fontWeight: '600',
    fontFamily: t.mono,
    overflowWrap: 'anywhere',
  },
  '& .desc': { fontSize: t.textMd },
  '& .note': { fontSize: t.textSm, color: t.textMuted },
  '& .actions': { display: 'flex', gap: t.s2, flexWrap: 'wrap' },
  '& details': { fontSize: t.textMd },
  '& details > summary': { fontSize: t.textSm, marginBottom: t.s2 },
  '& .reasons': { margin: '0', padding: '0', listStyle: 'none', color: t.textMuted, fontSize: t.textSm, display: 'grid', gap: '2px' },
  '& .changes': { margin: '0', padding: '0', listStyle: 'none', display: 'grid', gap: '2px', fontSize: t.textSm },
  '& .changes span': { color: t.textMuted },
  '&[data-status="offered"]': {
    borderColor: `color-mix(in srgb, ${t.warning} 45%, ${t.border})`,
  },
  '&[data-status="blocked"]': {
    borderColor: `color-mix(in srgb, ${t.danger} 45%, ${t.border})`,
  },
  // inside an open row of the project's list: the row is the card, so the panel has no frame of its own
  '&[data-inline]': { border: '0', borderRadius: '0', padding: '0', background: 'transparent' },
});
const confirmCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  padding: `${t.s2} ${t.s3}`,
  borderRadius: t.rMd,
  background: t.dangerSoft,
  fontSize: t.textMd,
});
const chipsCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  '& button': {
    height: '28px',
    padding: '0 10px',
    borderRadius: t.rFull,
    border: `1px solid ${t.border}`,
    background: t.bgSurface,
    color: t.textMuted,
    fontSize: t.textSm,
    cursor: 'pointer',
  },
  '& button[aria-pressed="true"]': { borderColor: t.accent, background: t.accentSoft, color: t.text },
  '& button:focus-visible': { boxShadow: t.focus, outline: 'none' },
});
const rowCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' });
// the agents a Library skill is added for, in one wrapping row of boxes
const forCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: `${t.s1} ${t.s4}`,
  margin: '0',
  padding: '0',
  border: '0',
  minWidth: '0',
  '& legend': { float: 'left', marginRight: t.s3, fontSize: t.textSm, fontWeight: '500', color: t.text },
});
const formCss = css({ display: 'grid', gap: t.s4 });
const usageCss = css({
  '& td, & th': { whiteSpace: 'nowrap' },
  '& td.num': { textAlign: 'right', fontVariantNumeric: 'tabular-nums' },
  '& td.why': { whiteSpace: 'normal', color: t.textMuted, fontSize: t.textSm, minWidth: '200px' },
  '& td .rec': { display: 'inline-flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
});

/** The selected project skill: its text, who gets it, the decision buttons and its deliveries. */
export function SkillPanel(
  handle: Handle<{
    s: Skill;
    onAccept: () => void;
    onReject: () => void;
    onRelease: () => void;
    /** The Library holds newer text for this skill; onUpdate adds it again from the Library. */
    update?: boolean;
    onUpdate?: () => void;
    /** The project's team, for who gets the skill. */
    agents?: readonly Agent[];
    /** Set who gets the skill; resolves true when it was saved. */
    onAudience?: (agents: string[]) => Promise<boolean>;
    deliveries?: readonly SkillDelivery[];
    busy: boolean;
    /** Rendered inside the open row of the project's list: no frame of its own. */
    inline?: boolean;
  }>,
) {
  let editing = false;
  let confirming = false;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  return () => {
    const { s, onAccept, onReject, onRelease, update, onUpdate, busy, agents = [], onAudience, deliveries = [], inline } = handle.props;
    const nameOf = (id: string) => agents.find((a) => a.id === id)?.name ?? id;
    const who = audienceText(onTeam(s, agents), nameOf);
    const mine = deliveries.filter((d) => d.skill === s.name);
    return (
      <article mix={panelCss} data-testid="skill" data-skill={s.name} data-status={s.status} data-inline={inline ? '' : undefined} aria-label={s.name}>
        <header>
          {inline ? null : <SkillLogo brand={skillBrand(s.registryId, s.origin)} />}
          <h3>{s.name}</h3>
          <Badge tone={skillTone(s.status)} testId="skill-status">
            {s.status === 'accepted' ? 'accepted ✓' : s.status}
          </Badge>
          <Badge title="Where the skill came from">{s.origin === 'registry' && s.registryId ? s.registryId : s.origin}</Badge>
          {update ? (
            <Badge tone="warn" testId="skill-update">
              update in the Library
            </Badge>
          ) : null}
          {(s.risk ?? 0) > 0 ? <Badge tone="warn">risk {s.risk}</Badge> : null}
          {s.hasScripts ? (
            <Badge tone="warn" title="The skill directory contains scripts. Scripts are never run by Drydock.">
              has scripts
            </Badge>
          ) : null}
        </header>
        {s.description ? <p class="desc">{s.description}</p> : null}
        <dl mix={kv}>
          <dt>Goes to</dt>
          <dd data-testid="skill-audience-text">{s.status === 'accepted' || (s.agents ?? []).length === 0 ? who : `${who}, once accepted`}</dd>
          <dt>Roles it names</dt>
          <dd>{listOr(s.roles)}</dd>
          <dt>Paths</dt>
          <dd>{listOr(s.globs)}</dd>
          {s.license ? (
            <>
              <dt>License</dt>
              <dd>{s.license}</dd>
            </>
          ) : null}
          {s.pinnedCommit ? (
            <>
              <dt>Pinned commit</dt>
              <dd>
                <code>{s.pinnedCommit.slice(0, 12)}</code>
              </dd>
            </>
          ) : null}
          {s.decidedBy ? (
            <>
              <dt>Decided by</dt>
              <dd>{s.decidedBy}</dd>
            </>
          ) : null}
        </dl>
        {(s.flags ?? []).length > 0 ? <Notice tone="warn">Flags: {(s.flags ?? []).join(', ')}</Notice> : null}
        {s.blockReason ? <ErrorNote error={s.blockReason} /> : null}
        {s.allowedToolsIgnored ? (
          <p class="note">This skill asks for tool permissions. They are recorded and ignored: a skill cannot widen permissions.</p>
        ) : null}
        {s.body ? (
          <details>
            <summary>Skill text (outside text, inert)</summary>
            <Markdown source={s.body} maxChars={20000} />
          </details>
        ) : null}
        <div class="actions">
          {s.status === 'offered' ? (
            <>
              <Button variant="primary" size="sm" icon={ShieldCheck} disabled={busy} data-testid="skill-accept" mix={on('click', onAccept)}>
                Accept {s.name}
              </Button>
              <Button size="sm" disabled={busy} data-testid="skill-reject" mix={on('click', onReject)}>
                Reject {s.name}
              </Button>
            </>
          ) : null}
          {s.status === 'accepted' && update && onUpdate ? (
            <Button variant="primary" size="sm" icon={RefreshCw} disabled={busy} data-testid="skill-update-from-library" mix={on('click', onUpdate)}>
              Update from the Library
            </Button>
          ) : null}
          {s.status === 'accepted' && !confirming ? (
            <Button size="sm" disabled={busy} data-testid="skill-stop" mix={on('click', () => set(() => (confirming = true)))}>
              Stop using {s.name}
            </Button>
          ) : null}
          {s.status === 'rejected' ? (
            <Button size="sm" disabled={busy} data-testid="skill-accept" mix={on('click', onAccept)}>
              Accept {s.name}
            </Button>
          ) : null}
          {s.status === 'blocked' ? (
            <Button
              size="sm"
              variant="danger"
              icon={ShieldAlert}
              disabled={busy}
              title="Override the block after reading the reason. Recorded in the audit log."
              mix={on('click', onRelease)}
            >
              Release {s.name} anyway
            </Button>
          ) : null}
          {onAudience && !editing && (s.status === 'accepted' || s.status === 'offered') ? (
            <Button size="sm" variant="ghost" icon={Users} disabled={busy} mix={on('click', () => set(() => (editing = true)))}>
              Change who gets {s.name}
            </Button>
          ) : null}
        </div>
        {confirming ? (
          <div mix={confirmCss} data-testid="skill-stop-confirm">
            <span>Stop using {s.name}? Agents no longer get it from the next run on; you can accept it again later.</span>
            <Button
              size="sm"
              variant="danger"
              disabled={busy}
              mix={on('click', () => {
                set(() => (confirming = false));
                onReject();
              })}
            >
              Yes, stop using it
            </Button>
            <Button size="sm" mix={on('click', () => set(() => (confirming = false)))}>
              Keep
            </Button>
          </div>
        ) : null}
        {onAudience && editing ? (
          <AudienceEditor
            skill={onTeam(s, agents)}
            agents={agents}
            busy={busy}
            onCancel={() => set(() => (editing = false))}
            onSave={async (chosen) => {
              if (await onAudience(chosen)) set(() => (editing = false));
            }}
          />
        ) : null}
        <p class="note" data-testid="skill-deliveries">
          {mine.length === 0
            ? 'Not delivered to an agent yet.'
            : `Delivered ${mine.length} time${mine.length === 1 ? '' : 's'}, last ${formatDateTime(mine[0]!.ts)} (${mine[0]!.method}, ${mine[0]!.fidelity}).`}
        </p>
      </article>
    );
  };
}

/** The Library's skills not yet in this project, grouped by registry; Add to project is the acceptance (decision P48). */
function AddFromLibrary(
  handle: Handle<{
    projectId: string;
    projectName: string;
    skills: readonly Skill[];
    statuses: readonly SkillRegistryStatus[];
    /** The project's team: a skill is added for the agents chosen among them (decision P76). */
    agents: readonly Agent[];
    /** Bumped by the page when a registry was synced from elsewhere on it: the Library is re-read. */
    epoch?: number;
    onAdded: (name: string) => void;
  }>,
) {
  const s = services(handle);
  const lib = query(handle, {
    load: () => s.client.skillLibraryList(),
    key: () => handle.props.epoch ?? 0,
    when: (e) => e.type === 'skill.library_synced' || e.type === 'skill.registry_updates',
  });
  const act = action(handle);
  let registry = '';
  let picked = new Set<string>();
  // the agents the skills are added for: each agent is a specialist, so a skill goes to the ones that need it, never to all
  let forAgents: string[] = [];
  let added: { names: string[]; project: string; who: string } | undefined;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const addMany = async (ls: readonly LibrarySkill[]) => {
    const { projectId, projectName, onAdded, agents } = handle.props;
    const to = forAgents.filter((id) => agents.some((a) => a.id === id));
    if (to.length === 0) return;
    const done: string[] = [];
    await act.run('add', async () => {
      for (const l of ls) {
        await s.client.skillLibraryAdd(projectId, l.registryId, l.name, 'agents', to);
        done.push(l.name);
      }
      return true;
    });
    if (done.length > 0) {
      const who = inWords(to.map((id) => agents.find((a) => a.id === id)?.name ?? id));
      set(() => {
        added = { names: done, project: projectName, who };
        picked = new Set();
      });
      onAdded(done.length === 1 ? done[0]! : '');
    }
  };
  const sync = async () => {
    await act.run('sync', () => s.client.skillRegistryCheck());
    lib.reload();
  };
  return () => {
    const { skills, projectName, statuses, agents } = handle.props;
    const to = forAgents.filter((id) => agents.some((a) => a.id === id));
    const all = notInProject(lib.data ?? [], skills);
    const groups = libraryGroups(all);
    const chosen = groups.some((g) => g.registryId === registry) ? registry : '';
    const shown = chosen ? all.filter((l) => l.registryId === chosen) : all;
    const library = lib.data ?? [];
    const pickedSkills = all.filter((l) => picked.has(pickKey(l)));
    const have = new Set(skills.map((k) => k.name));
    return (
      <Section
        label="Add from the Library"
        title="Add from the Library"
        description="Pick skills and the agents that need them: adding is your acceptance."
      >
        <ErrorNote error={lib.error ?? act.error} what="Library" />
        {added ? (
          <Notice tone="ok" role="status" testId="skill-added">
            {added.names.length === 1 ? addedText(added.names[0]!, added.project) : `${added.names.length} skills were added to ${added.project} and are accepted ✓: ${added.names.join(', ')}`} For {added.who}.
          </Notice>
        ) : null}
        {lib.loading && !lib.data ? <Loading /> : null}
        {lib.data && library.length === 0 ? (
          <p mix={emptyLine} data-testid="library-empty">
            <Icon icon={BookOpen} size={14} />
            <strong>The Library is filling from your registries...</strong> It syncs at start and twice a day.
            <Button size="sm" icon={RefreshCw} disabled={act.busy !== ''} mix={on('click', () => void sync())}>
              {act.busy === 'sync' ? 'Syncing...' : 'Sync now'}
            </Button>
          </p>
        ) : null}
        {lib.data && library.length > 0 && all.length === 0 ? (
          <p mix={emptyLine} data-testid="library-all-added">
            <Icon icon={ShieldCheck} size={14} />
            <strong>Everything in the Library is in this project.</strong> {library.length} skill{library.length === 1 ? '' : 's'}.
          </p>
        ) : null}
        {all.length > 0 ? (
          <>
            <div mix={rowCss}>
              <div mix={chipsCss} role="group" aria-label="Registry">
                <button type="button" aria-pressed={chosen === '' ? 'true' : 'false'} mix={on('click', () => set(() => (registry = '')))}>
                  All ({all.length})
                </button>
                {groups.map((g) => (
                  <button key={g.registryId} type="button" aria-pressed={chosen === g.registryId ? 'true' : 'false'} data-registry={g.registryId} mix={on('click', () => set(() => (registry = g.registryId)))}>
                    {g.repo} ({g.count})
                  </button>
                ))}
              </div>
            </div>
            <fieldset mix={forCss} data-testid="library-for-agents">
              <legend>For</legend>
              {agents.length === 0 ? <span class="note">Add an agent to the crew first: skills are given to agents.</span> : null}
              {agents.map((a) => (
                <Checkbox
                  key={a.id}
                  label={`${a.name} (${a.role})`}
                  checked={to.includes(a.id)}
                  onChange={(v) => set(() => (forAgents = v ? [...to.filter((x) => x !== a.id), a.id] : to.filter((x) => x !== a.id)))}
                />
              ))}
            </fieldset>
            <div mix={rowCss}>
              <Button
                variant="primary"
                size="sm"
                icon={Plus}
                disabled={act.busy !== '' || pickedSkills.length === 0 || to.length === 0}
                data-testid="library-add-picked"
                mix={on('click', () => void addMany(pickedSkills))}
              >
                {act.busy === 'add'
                  ? 'Adding...'
                  : pickedSkills.length === 0
                    ? 'Pick skills to add'
                    : to.length === 0
                      ? 'Choose the agents first'
                      : addSelectedText(pickedSkills.map((l) => l.name), projectName)}
              </Button>
              {pickedSkills.length > 0 ? (
                <Button size="sm" variant="ghost" mix={on('click', () => set(() => (picked = new Set())))}>
                  Clear
                </Button>
              ) : null}
            </div>
            <SkillRows
              skills={shown}
              picked={picked}
              onPick={(k, yes) =>
                set(() => {
                  picked = new Set(picked);
                  if (yes) picked.add(k);
                  else picked.delete(k);
                })
              }
              have={have}
              fresh={newInLibrary(statuses)}
              projectName={projectName}
              label="Skills in the Library"
              testId="library-skill-tile"
              actions={(l) => (
                <Button
                  variant="primary"
                  size="sm"
                  icon={Plus}
                  disabled={act.busy !== '' || to.length === 0}
                  title={to.length === 0 ? 'Choose the agents it is for above' : undefined}
                  data-testid="library-add"
                  mix={on('click', () => void addMany([l]))}
                >
                  {act.busy === 'add' ? 'Adding...' : `Add to ${projectName}`}
                </Button>
              )}
            />
          </>
        ) : null}
      </Section>
    );
  };
}

/** Which skills earn their tokens: loads, listings, on-demand reads, tasks, tokens, and the core's recommendation (decision P49). */
function Usage(handle: Handle<{ projectId: string; onChanged: () => void }>) {
  const s = services(handle);
  const q = query(handle, {
    load: () => s.client.skillUsage(handle.props.projectId),
    key: () => handle.props.projectId,
    when: (e) => e.type.startsWith('skill.') || e.type === 'task.completed',
  });
  const act = action(handle);
  const stop = async (name: string) => {
    const r = await act.run('stop', () => s.client.skillReject(handle.props.projectId, name));
    if (r) {
      q.reload();
      handle.props.onChanged();
    }
  };
  return () => {
    const rows = sortUsage(q.data ?? []);
    return (
      <Section
        label="Skill usage"
        title="Usage"
        description="What each skill cost and whether it was read. Nothing changes until you act on a recommendation."
      >
        <ErrorNote error={q.error ?? act.error} what="Usage" />
        {q.loading && !q.data ? <Loading /> : null}
        {q.data && rows.length === 0 ? <p mix={muted}>No skill has been used in a run yet.</p> : null}
        {rows.length > 0 ? (
          <div mix={[tableCard, usageCss]}>
            <table aria-label="Skill usage">
              <thead>
                <tr>
                  <th scope="col">Skill</th>
                  <th scope="col">Loaded</th>
                  <th scope="col">Listed</th>
                  <th scope="col">Opened</th>
                  <th scope="col">Tasks</th>
                  <th scope="col">Tokens</th>
                  <th scope="col">Last used</th>
                  <th scope="col">Recommendation</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((u) => {
                  const rec = recommendationBadge(u.recommendation);
                  return (
                    <tr key={u.name} data-testid="skill-usage-row" data-skill={u.name} data-recommendation={u.recommendation}>
                      <td>
                        <code>{u.name}</code>
                      </td>
                      <td class="num">{formatCount(u.loaded)}</td>
                      <td class="num">{formatCount(u.listed)}</td>
                      <td class="num">{formatCount(u.opened)}</td>
                      <td class="num">{formatCount(u.tasks)}</td>
                      <td class="num" title={`${formatCount(u.tokensLoaded)} loaded + ${formatCount(u.tokensListed)} listed`} data-testid="skill-usage-tokens">
                        {formatCount(usageTokens(u))}
                      </td>
                      <td>{u.lastUsed ? formatDateTime(u.lastUsed) : 'never'}</td>
                      <td class="why">
                        <span class="rec">
                          <Badge tone={rec.tone} testId="skill-recommendation">
                            {rec.text}
                          </Badge>
                          {u.reason ? <span>{u.reason}</span> : null}
                          {u.recommendation === 'remove' && u.status !== 'rejected' ? (
                            <Button size="sm" variant="danger" disabled={act.busy !== ''} mix={on('click', () => void stop(u.name))}>
                              Stop using {u.name}
                            </Button>
                          ) : null}
                        </span>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}
      </Section>
    );
  };
}

/** The registry allowlist: the Library fills from these GitHub repositories, pinned to a commit, at start and twice a day. */
function Registries(handle: Handle<{ projectId: string; statuses: readonly SkillRegistryStatus[]; statusError: Error | undefined; onSynced: () => void }>) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.skillRegistries() });
  const act = action(handle);
  let selected = '';
  let adding = false;
  let repo = '';
  let rid = '';
  let license = '';
  let ack = false;
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };
  const add = async () => {
    const r = await act.run('add', () =>
      s.client.skillRegistryAdd(
        {
          id: rid.trim(),
          repo: repo.trim(),
          license: license.trim() || undefined,
          trust: 'user',
        },
        ack,
      ),
    );
    if (r) {
      adding = false;
      selected = r.id;
      q.reload();
      void handle.update();
    }
  };
  const sync = async (registryId: string) => {
    await act.run('sync', () => s.client.skillRegistryCheck(registryId));
    handle.props.onSynced();
  };
  return () => {
    const regs: SkillRegistry[] = q.data ?? [];
    const { statuses, statusError, projectId } = handle.props;
    const open = regs.find((r) => r.id === selected);
    const openStatus = open ? statuses.find((x) => x.registryId === open.id) : undefined;
    const changes = syncChanges(openStatus);
    const updates = open ? (openStatus?.projects?.[projectId]?.updates ?? []) : [];
    return (
      <Section
        label="Skill registries"
        title="Registries"
        description="Pinned to a commit and synced at start and twice a day. Nothing reaches a project until you add it."
        actions={
          adding ? undefined : (
            <Button size="sm" icon={Plus} mix={on('click', () => set(() => (adding = true)))}>
              Allow another repository
            </Button>
          )
        }
      >
        <ErrorNote error={q.error ?? statusError ?? act.error} />
        {regs.length > 0 ? (
          <ul mix={tileGrid} aria-label="Skill registries">
            {regs.map((r) => {
              const status = statuses.find((x) => x.registryId === r.id);
              const st = registryTileText(status, r.enabled);
              return (
                <Tile
                  key={r.id}
                  title={r.repo}
                  logo={skillBrand(r.id, 'registry') || undefined}
                  icon={GitBranch}
                  blurb={r.curated ? 'Curated' : 'Added by you'}
                  state={st.text}
                  tone={st.tone}
                  selected={selected === r.id}
                  onSelect={() => set(() => (selected = selected === r.id ? '' : r.id))}
                  testId="registry-tile"
                  data={{ registry: r.id, enabled: String(r.enabled) }}
                />
              );
            })}
          </ul>
        ) : q.data ? (
          <p mix={muted}>No repository is allowed yet.</p>
        ) : null}
        {open ? (
          <article mix={panelCss} data-testid="registry" data-registry={open.id} aria-label={open.repo}>
            <header>
              <SkillLogo brand={skillBrand(open.id, 'registry')} icon={GitBranch} />
              <h3>{open.repo}</h3>
              {open.root ? <span mix={muted}>/{open.root}</span> : null}
              <Badge tone={open.enabled ? 'ok' : 'muted'}>{open.enabled ? 'enabled' : 'disabled'}</Badge>
              {open.curated ? <Badge>curated</Badge> : <Badge tone="warn">added by you</Badge>}
              <Badge title="Trust level">trust {open.trust}</Badge>
              {open.license ? <Badge title="License">{open.license}</Badge> : null}
            </header>
            {open.notes ? <p class="note">{open.notes}</p> : null}
            <p class="note" data-testid="registry-status">
              {registryStatusText(openStatus, open.enabled).text}
              {openStatus?.lastCommit ? (
                <>
                  {' · at '}
                  <code>{openStatus.lastCommit.slice(0, 12)}</code>
                </>
              ) : null}
            </p>
            {openStatus?.lastError ? <ErrorNote error={`Last sync failed: ${openStatus.lastError}`} /> : null}
            {changes.added.length > 0 ? (
              <>
                <p class="note">New in the Library after the last sync:</p>
                <ul class="changes" aria-label={`Skills the last sync added from ${open.repo}`}>
                  {changes.added.map((c) => (
                    <li key={`a-${c.name}`} data-testid="registry-added">
                      <code>{c.name}</code> <Badge tone="ok">new</Badge> {c.description ? <span>{c.description}</span> : null}
                    </li>
                  ))}
                </ul>
              </>
            ) : null}
            {changes.changed.length > 0 ? (
              <>
                <p class="note">Changed in the Library after the last sync:</p>
                <ul class="changes" aria-label={`Skills the last sync changed from ${open.repo}`}>
                  {changes.changed.map((c) => (
                    <li key={`c-${c.name}`} data-testid="registry-changed">
                      <code>{c.name}</code> <Badge tone="warn">changed</Badge> {c.description ? <span>{c.description}</span> : null}
                    </li>
                  ))}
                </ul>
              </>
            ) : null}
            {updates.length > 0 ? (
              <p class="note" data-testid="registry-project-updates">
                This project is behind the Library on {updates.map((u) => u.name).join(', ')}: open each skill above and press Update from the Library.
              </p>
            ) : null}
            {changes.added.length + changes.changed.length === 0 && openStatus?.checkedAt && !openStatus.lastError ? <p class="note">The last sync found nothing new or changed.</p> : null}
            <div class="actions">
              <Button
                size="sm"
                icon={RefreshCw}
                disabled={!open.enabled || act.busy !== ''}
                data-testid="registry-check-now"
                title="Sync the Library from this repository now instead of waiting for the next scheduled sync"
                mix={on('click', () => void sync(open.id))}
              >
                {act.busy === 'sync' ? 'Syncing...' : 'Sync now'}
              </Button>
              <Button
                size="sm"
                mix={on('click', async () => {
                  await act.run('enable', () => s.client.skillRegistryEnable(open.id, !open.enabled));
                  q.reload();
                })}
              >
                {open.enabled ? 'Disable' : 'Enable'}
              </Button>
              {!open.curated ? (
                <Button
                  size="sm"
                  variant="ghost"
                  mix={on('click', async () => {
                    await act.run('remove', () => s.client.skillRegistryRemove(open.id));
                    set(() => (selected = ''));
                    q.reload();
                  })}
                >
                  Remove
                </Button>
              ) : null}
            </div>
          </article>
        ) : null}
        {adding ? (
          <form
            mix={[
              card,
              formCss,
              on('submit', (e) => {
                e.preventDefault();
                if (ack && repo.trim() && rid.trim() && !act.busy) void add();
              }),
            ]}
          >
            <div mix={formGrid}>
              <Field label="Registry id">{(id) => <input id={id} value={rid} mix={on('input', (e) => set(() => (rid = e.currentTarget.value)))} />}</Field>
              <Field label="GitHub repository (owner/name)">
                {(id) => <input id={id} value={repo} placeholder="owner/name" mix={on('input', (e) => set(() => (repo = e.currentTarget.value)))} />}
              </Field>
              <Field label="License (SPDX id, if known)">
                {(id) => <input id={id} value={license} mix={on('input', (e) => set(() => (license = e.currentTarget.value)))} />}
              </Field>
            </div>
            <Checkbox
              label="I understand skills from this repository are third-party text that agents will read if I add them to a project"
              checked={ack}
              onChange={(v) => set(() => (ack = v))}
            />
            <div mix={formActions}>
              <Button type="submit" variant="primary" disabled={!ack || !repo.trim() || !rid.trim() || act.busy !== ''}>
                Add to the allowlist
              </Button>
              <Button variant="ghost" mix={on('click', () => set(() => (adding = false)))}>
                Cancel
              </Button>
            </div>
          </form>
        ) : null}
      </Section>
    );
  };
}

export function SkillsScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  const act = action(handle);
  const pid = () => s.workspace.projectId;
  const on_ = { key: pid, enabled: () => !!pid() };
  const q = query(handle, {
    load: () => s.client.skillList(pid()!),
    when: (e) => e.type.startsWith('skill.'),
    ...on_,
  });
  const del = query(handle, {
    load: () => s.client.skillDeliveries(pid()!),
    when: (e) => e.type.startsWith('skill.') || e.type === 'task.completed',
    ...on_,
  });
  const cov = query(handle, {
    load: () => s.client.skillCoverage(pid()!),
    when: (e) => e.type.startsWith('skill.'),
    ...on_,
  });
  // The Library's view of each registry: what the last sync brought, and which project skills are behind the Library.
  const st = query(handle, { load: () => s.client.skillRegistryStatus(), when: (e) => e.type === 'skill.library_synced' || e.type === 'skill.registry_updates' });
  let scan: SkillScan | undefined;
  let selected = '';
  let synced = 0;
  // skills picked for this project in the workspace Library (Library > Skills): offered at the top, once
  let picks = pid() ? s.ui.takeSkillPicks(pid()!) : [];
  const set = (f: () => void) => {
    f();
    void handle.update();
  };

  const decide = (fn: (p: string, n: string) => Promise<Skill>, name: string) => async () => {
    const projectId = pid();
    if (!projectId) return;
    await act.run(name, () => fn(projectId, name));
    q.reload();
  };
  const update = (sk: Skill) => async () => {
    const projectId = pid();
    if (!projectId || !sk.registryId) return;
    await act.run('update', () => s.client.skillLibraryAdd(projectId, sk.registryId!, sk.name));
    q.reload();
    st.reload();
  };
  const runScan = async () => {
    const projectId = pid();
    if (!projectId) return;
    const r = await act.run('scan', () => s.client.skillScan(projectId));
    if (r) scan = r;
    q.reload();
    void handle.update();
  };

  return () => {
    const skills: Skill[] = q.data ?? [];
    const offered = skills.filter((x) => x.status === 'offered').length;
    const projectId = pid();
    const projectName = s.workspace.projectList.find((p) => p.id === projectId)?.name ?? projectId ?? '';
    const deliveries = del.data ?? [];
    const updates = projectId ? registryUpdates(st.data, projectId) : new Set<string>();
    return (
      <Page
        title="Skills"
        icon={Cpu}
        guide="skills"
        subtitle={q.data ? `${skills.length} skill${skills.length === 1 ? '' : 's'}${offered ? `, ${offered} waiting for you` : ''}` : undefined}
        actions={
          <Button icon={ScanSearch} disabled={act.busy !== ''} mix={on('click', () => void runScan())}>
            Scan the repository for skills
          </Button>
        }
      >
        <ErrorNote error={q.error ?? act.error} what="Skills" />
        {scan ? (
          <Notice tone="info" role="status" testId="skill-scan">
            {scanText(scan)}
          </Notice>
        ) : null}
        {projectId ? (
          <Recommendations
            key={`rec-${projectId}`}
            projectId={projectId}
            projectName={projectName}
            agents={s.project.agentList}
            picks={picks}
            onPicksDone={() => set(() => (picks = []))}
            onChanged={() => {
              q.reload();
              st.reload();
            }}
          />
        ) : null}
        <Section label="Skills in this project" title="In this project" description={q.data && skills.length > 0 ? 'Click a skill to read it, see who gets it, or stop using it.' : undefined}>
          {q.loading && !q.data ? <Loading /> : null}
          {q.data && skills.length === 0 ? (
            <div mix={card}>
              <Empty title="No skills in this project yet." icon={Cpu}>
                <p>Add one from the Library below, or scan the repository for its own skills.</p>
              </Empty>
            </div>
          ) : null}
          {skills.length > 0 ? (
            <ProjectSkillRows
              skills={skills}
              updates={updates}
              open={selected}
              onToggle={(name) => set(() => (selected = selected === name ? '' : name))}
              label="Skills in this project"
              testId="skill-tile"
              panel={(open) => (
                <SkillPanel
                  key={open.id}
                  inline
                  s={open}
                  busy={act.busy !== ''}
                  update={updates.has(open.name)}
                  onUpdate={update(open)}
                  onAccept={decide(s.client.skillAccept, open.name)}
                  onReject={decide(s.client.skillReject, open.name)}
                  onRelease={decide(s.client.skillRelease, open.name)}
                  agents={s.project.agentList}
                  deliveries={deliveries}
                  onAudience={async (agents) => {
                    const projectId = pid();
                    if (!projectId) return false;
                    const ok = !!(await act.run('audience', () => s.client.skillAudience(projectId, open.name, 'agents', agents)));
                    q.reload();
                    return ok;
                  }}
                />
              )}
            />
          ) : null}
        </Section>
        {projectId ? (
          <AddFromLibrary
            key={`lib-${projectId}`}
            projectId={projectId}
            projectName={projectName}
            skills={skills}
            statuses={st.data ?? []}
            agents={s.project.agentList}
            epoch={synced}
            onAdded={(name) => {
              if (name) set(() => (selected = name));
              q.reload();
              st.reload();
            }}
          />
        ) : null}
        {projectId ? <Usage key={`usage-${projectId}`} projectId={projectId} onChanged={() => q.reload()} /> : null}
        {projectId ? (
          <Registries
            key={`reg-${projectId}`}
            projectId={projectId}
            statuses={st.data ?? []}
            statusError={st.error}
            onSynced={() => {
              st.reload();
              set(() => synced++);
            }}
          />
        ) : null}
        <Section title="Deliveries" description={<span data-testid="skill-coverage">{coverageText(cov.data)}</span>}>
          {deliveries.length === 0 ? (
            <p mix={muted}>No skill has been delivered to an agent yet.</p>
          ) : (
            <div mix={tableCard}>
              <table aria-label="Skill deliveries">
                <thead>
                  <tr>
                    <th scope="col">When</th>
                    <th scope="col">Skill</th>
                    <th scope="col">Task</th>
                    <th scope="col">How</th>
                  </tr>
                </thead>
                <tbody>
                  {deliveries.map((d, i) => (
                    <tr key={i}>
                      <td>{formatDateTime(d.ts)}</td>
                      <td>
                        <code>{d.skill}</code>
                      </td>
                      <td>
                        <a href={routes.task.href({ id: d.taskId })}>{d.taskId}</a>
                      </td>
                      <td>
                        {d.method}, {d.fidelity}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Section>
      </Page>
    );
  };
}
