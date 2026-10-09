// Skills: instructions agents read. Skills found in the repository or imported from an allowlisted registry are offered;
// nothing is applied until the human accepts it. Skill text is outside text, rendered inert; scripts are never run and
// requested tool permissions are recorded and ignored.

import { css, on, type Handle } from 'remix/component';
import { Cpu, FileCode2, GitBranch, Plus, ScanSearch, ShieldAlert, ShieldCheck } from 'lucide';
import type { Skill, SkillCandidate, SkillRegistry, SkillScan } from '../../api/extTypes.ts';
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
import { t } from '../../ui/tokens.ts';
import { coverageText, listOr, scanText, skillTone } from './skills.ts';

const gridCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))',
  gap: t.s3,
  alignItems: 'start',
});
const skillCss = css({
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
  '&[data-status="offered"]': {
    borderColor: `color-mix(in srgb, ${t.warning} 45%, ${t.border})`,
  },
  '&[data-status="blocked"]': {
    borderColor: `color-mix(in srgb, ${t.danger} 45%, ${t.border})`,
  },
});
const iconWrap = css({
  display: 'inline-grid',
  placeItems: 'center',
  width: '24px',
  height: '24px',
  borderRadius: t.rMd,
  background: t.bgActive,
  color: t.textMuted,
});

/** A skill from outside is offered, never applied: the human accepts or rejects each one. */
export function SkillCard(
  handle: Handle<{
    s: Skill;
    onAccept: () => void;
    onReject: () => void;
    onRelease: () => void;
    busy: boolean;
  }>,
) {
  return () => {
    const { s, onAccept, onReject, onRelease, busy } = handle.props;
    return (
      <article mix={skillCss} data-testid="skill" data-skill={s.name} data-status={s.status}>
        <header>
          <span mix={iconWrap}>
            <Icon icon={FileCode2} size={14} />
          </span>
          <h3>{s.name}</h3>
          <Badge tone={skillTone(s.status)}>{s.status}</Badge>
          <Badge title="Where the skill came from">{s.origin}</Badge>
          {(s.risk ?? 0) > 0 ? <Badge tone="warn">risk {s.risk}</Badge> : null}
          {s.hasScripts ? (
            <Badge tone="warn" title="The skill directory contains scripts. Scripts are never run by Drydock.">
              has scripts
            </Badge>
          ) : null}
        </header>
        {s.description ? <p class="desc">{s.description}</p> : null}
        <dl mix={kv}>
          <dt>Applies to roles</dt>
          <dd>{listOr(s.roles)}</dd>
          <dt>Paths</dt>
          <dd>{listOr(s.globs)}</dd>
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
              <Button variant="primary" size="sm" icon={ShieldCheck} disabled={busy} mix={on('click', onAccept)}>
                Accept {s.name}
              </Button>
              <Button size="sm" disabled={busy} mix={on('click', onReject)}>
                Reject {s.name}
              </Button>
            </>
          ) : null}
          {s.status === 'accepted' ? (
            <Button size="sm" disabled={busy} mix={on('click', onReject)}>
              Withdraw (reject) {s.name}
            </Button>
          ) : null}
          {s.status === 'rejected' ? (
            <Button size="sm" disabled={busy} mix={on('click', onAccept)}>
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
        </div>
      </article>
    );
  };
}

const regList = css({
  margin: '0',
  padding: '0',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
  '& > li': {
    display: 'grid',
    gap: t.s2,
    padding: `${t.s3} ${t.s4}`,
    borderTop: `1px solid ${t.border}`,
    minWidth: '0',
  },
  '& > li:first-child': { borderTop: '0' },
  '& .head': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    flexWrap: 'wrap',
  },
  '& .meta': { color: t.textMuted, fontSize: t.textSm },
  '& .actions': { display: 'flex', gap: t.s2, flexWrap: 'wrap' },
});
const candList = css({
  display: 'grid',
  gap: t.s2,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& li': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s3,
    flexWrap: 'wrap',
    padding: `${t.s2} ${t.s3}`,
    border: `1px solid ${t.border}`,
    borderRadius: t.rMd,
    background: t.bgApp,
  },
  '& .what': { flex: '1', minWidth: '200px', display: 'grid', gap: '2px' },
  '& .what span': { color: t.textMuted, fontSize: t.textSm },
});
const formCss = css({ display: 'grid', gap: t.s4 });

/** The registry allowlist: skills are imported only from GitHub repositories allowed here, pinned to a commit. */
function Registries(handle: Handle<{ projectId: string; onImported: () => void }>) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.skillRegistries() });
  const act = action(handle);
  let cands: Record<string, SkillCandidate[]> = {};
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
      q.reload();
      void handle.update();
    }
  };
  return () => {
    const regs: SkillRegistry[] = q.data ?? [];
    return (
      <Section
        label="Skill registries"
        title="Registries (allowlist)"
        description="Skills are imported only from GitHub repositories you have allowed here, pinned to a commit. An imported skill is offered; you still accept it."
        actions={
          adding ? undefined : (
            <Button size="sm" icon={Plus} mix={on('click', () => set(() => (adding = true)))}>
              Allow another repository
            </Button>
          )
        }
      >
        <ErrorNote error={q.error ?? act.error} />
        {regs.length > 0 ? (
          <ul mix={regList}>
            {regs.map((r) => (
              <li key={r.id} data-testid="registry">
                <div class="head">
                  <Icon icon={GitBranch} size={14} />
                  <code>{r.repo}</code>
                  {r.root ? <span mix={muted}>/{r.root}</span> : null}
                  <Badge tone={r.enabled ? 'ok' : 'muted'}>{r.enabled ? 'enabled' : 'disabled'}</Badge>
                  {r.curated ? <Badge>curated</Badge> : <Badge tone="warn">added by you</Badge>}
                </div>
                <div class="meta">
                  trust {r.trust}
                  {r.license ? `, license ${r.license}` : ''}
                </div>
                <div class="actions">
                  <Button
                    size="sm"
                    mix={on('click', async () => {
                      await act.run('enable', () => s.client.skillRegistryEnable(r.id, !r.enabled));
                      q.reload();
                    })}
                  >
                    {r.enabled ? 'Disable' : 'Enable'}
                  </Button>
                  <Button
                    size="sm"
                    disabled={!r.enabled}
                    mix={on('click', async () => {
                      const c = await act.run('candidates', () => s.client.skillCandidates(r.id));
                      if (c) set(() => (cands = { ...cands, [r.id]: c.skills }));
                    })}
                  >
                    List skills to import
                  </Button>
                  {!r.curated ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      mix={on('click', async () => {
                        await act.run('remove', () => s.client.skillRegistryRemove(r.id));
                        q.reload();
                      })}
                    >
                      Remove
                    </Button>
                  ) : null}
                </div>
                {(cands[r.id] ?? []).length > 0 ? (
                  <ul mix={candList} aria-label={`Skills in ${r.repo}`}>
                    {(cands[r.id] ?? []).map((c) => (
                      <li key={c.name} data-testid="skill-candidate">
                        <div class="what">
                          <strong>
                            {c.name} {c.valid ? null : <Badge tone="err">invalid</Badge>} {c.hasScripts ? <Badge tone="warn">has scripts</Badge> : null}
                          </strong>
                          <span>{c.description || c.problem}</span>
                        </div>
                        <Button
                          size="sm"
                          disabled={!c.valid || act.busy !== ''}
                          mix={on('click', async () => {
                            await act.run('import', () => s.client.skillImport(handle.props.projectId, r.id, c.name));
                            handle.props.onImported();
                          })}
                        >
                          Import {c.name} (it will be offered)
                        </Button>
                      </li>
                    ))}
                  </ul>
                ) : null}
              </li>
            ))}
          </ul>
        ) : q.data ? (
          <p mix={muted}>No repository is allowed yet.</p>
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
              label="I understand skills from this repository are third-party text that agents will read if I accept them"
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
  let scan: SkillScan | undefined;

  const decide = (fn: (p: string, n: string) => Promise<Skill>, name: string) => async () => {
    const projectId = pid();
    if (!projectId) return;
    await act.run(name, () => fn(projectId, name));
    q.reload();
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
    const deliveries = del.data ?? [];
    return (
      <Page
        title="Skills"
        icon={Cpu}
        subtitle={q.data ? `${skills.length} skill${skills.length === 1 ? '' : 's'}${offered ? `, ${offered} waiting for you` : ''}` : undefined}
        actions={
          <Button icon={ScanSearch} disabled={act.busy !== ''} mix={on('click', () => void runScan())}>
            Scan the repository for skills
          </Button>
        }
      >
        <p mix={muted}>
          Skills are instructions agents read. Skills found in the repository or imported from a registry are offered to you; nothing is applied until you
          accept it. Accepted skills are delivered to agents as path-scoped rule files that stay out of the diff.
        </p>
        <ErrorNote error={q.error ?? act.error} what="Skills" />
        {scan ? (
          <Notice tone="info" role="status" testId="skill-scan">
            {scanText(scan)}
          </Notice>
        ) : null}
        {q.loading && !q.data ? <Loading /> : null}
        {q.data && skills.length === 0 ? (
          <div mix={card}>
            <Empty title="No skills yet." icon={Cpu}>
              <p>Scan the repository, or import one from a registry.</p>
            </Empty>
          </div>
        ) : null}
        {skills.length > 0 ? (
          <div mix={gridCss}>
            {skills.map((sk) => (
              <SkillCard
                key={sk.id}
                s={sk}
                busy={act.busy !== ''}
                onAccept={decide(s.client.skillAccept, sk.name)}
                onReject={decide(s.client.skillReject, sk.name)}
                onRelease={decide(s.client.skillRelease, sk.name)}
              />
            ))}
          </div>
        ) : null}
        {projectId ? <Registries key={projectId} projectId={projectId} onImported={() => q.reload()} /> : null}
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
