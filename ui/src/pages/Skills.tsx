import { useState } from 'react';
import type { Skill, SkillCandidate, SkillRegistry } from '../api/extTypes';
import { Markdown } from '../lib/Markdown';
import { formatDateTime } from '../lib/format';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote, Page } from '../components/common';

const statusTone = (s: string) => (s === 'accepted' ? 'ok' : s === 'offered' ? 'warn' : s === 'blocked' ? 'err' : 'muted');

/** A skill from outside is offered, never applied: the human accepts or rejects each one. */
export function SkillCard({ s, onAccept, onReject, onRelease, busy }: { s: Skill; onAccept: () => void; onReject: () => void; onRelease: () => void; busy: boolean }) {
  return (
    <article className="card" data-testid="skill" data-skill={s.name} data-status={s.status}>
      <header className="card-head">
        <h3>{s.name}</h3>
        <Badge tone={statusTone(s.status)}>{s.status}</Badge>
        <Badge title="Where the skill came from">{s.origin}</Badge>
        {(s.risk ?? 0) > 0 ? <Badge tone="warn">risk {s.risk}</Badge> : null}
        {s.hasScripts ? <Badge tone="warn" title="The skill directory contains scripts. Scripts are never run by Drydock.">has scripts</Badge> : null}
      </header>
      <p>{s.description}</p>
      <dl className="kv">
        <dt>Applies to roles</dt>
        <dd>{(s.roles ?? []).join(', ') || 'all'}</dd>
        <dt>Paths</dt>
        <dd>{(s.globs ?? []).join(', ') || 'all'}</dd>
        {s.pinnedCommit ? (
          <>
            <dt>Pinned commit</dt>
            <dd><code>{s.pinnedCommit.slice(0, 12)}</code></dd>
          </>
        ) : null}
        {s.decidedBy ? (
          <>
            <dt>Decided by</dt>
            <dd>{s.decidedBy}</dd>
          </>
        ) : null}
      </dl>
      {(s.flags ?? []).length > 0 ? <p className="warn-note">Flags: {(s.flags ?? []).join(', ')}</p> : null}
      {s.blockReason ? <p className="error-note">{s.blockReason}</p> : null}
      {s.allowedToolsIgnored ? <p className="muted">This skill asks for tool permissions. They are recorded and ignored: a skill cannot widen permissions.</p> : null}
      {s.body ? (
        <details>
          <summary>Skill text (outside text, inert)</summary>
          <Markdown source={s.body} maxChars={20000} />
        </details>
      ) : null}
      <div className="form-actions">
        {s.status === 'offered' ? (
          <>
            <button className="primary" disabled={busy} onClick={onAccept}>
              Accept {s.name}
            </button>
            <button disabled={busy} onClick={onReject}>
              Reject {s.name}
            </button>
          </>
        ) : null}
        {s.status === 'accepted' ? (
          <button disabled={busy} onClick={onReject}>
            Withdraw (reject) {s.name}
          </button>
        ) : null}
        {s.status === 'rejected' ? (
          <button disabled={busy} onClick={onAccept}>
            Accept {s.name}
          </button>
        ) : null}
        {s.status === 'blocked' ? (
          <button disabled={busy} onClick={onRelease} title="Override the block after reading the reason. Recorded in the audit log.">
            Release {s.name} anyway
          </button>
        ) : null}
      </div>
    </article>
  );
}

function Registries({ projectId, onImported }: { projectId: string; onImported: () => void }) {
  const client = useClient();
  const q = useLive(() => client.skillRegistries(), [], () => false);
  const act = useAction();
  const [cands, setCands] = useState<Record<string, SkillCandidate[]>>({});
  const [adding, setAdding] = useState(false);
  const [repo, setRepo] = useState('');
  const [rid, setRid] = useState('');
  const [license, setLicense] = useState('');
  const [ack, setAck] = useState(false);
  const regs: SkillRegistry[] = q.data ?? [];
  return (
    <section aria-label="Skill registries">
      <h2>Registries (allowlist)</h2>
      <p className="muted">Skills are imported only from GitHub repositories you have allowed here, pinned to a commit. An imported skill is offered; you still accept it.</p>
      <ErrorNote error={q.error ?? act.error} />
      <ul>
        {regs.map((r) => (
          <li key={r.id} data-testid="registry">
            <code>{r.repo}</code> {r.root ? <span className="muted">/{r.root}</span> : null} <Badge tone={r.enabled ? 'ok' : 'muted'}>{r.enabled ? 'enabled' : 'disabled'}</Badge> {r.curated ? <Badge>curated</Badge> : <Badge tone="warn">added by you</Badge>}{' '}
            <span className="muted">trust {r.trust}{r.license ? `, license ${r.license}` : ''}</span>
            <div className="form-actions">
              <button onClick={async () => { await act.run('enable', () => client.skillRegistryEnable(r.id, !r.enabled)); q.reload(); }}>{r.enabled ? 'Disable' : 'Enable'}</button>
              <button disabled={!r.enabled} onClick={async () => { const c = await act.run('candidates', () => client.skillCandidates(r.id)); if (c) setCands({ ...cands, [r.id]: c.skills }); }}>
                List skills to import
              </button>
              {!r.curated ? <button onClick={async () => { await act.run('remove', () => client.skillRegistryRemove(r.id)); q.reload(); }}>Remove</button> : null}
            </div>
            {(cands[r.id] ?? []).map((c) => (
              <div key={c.name} className="gh-review">
                <strong>{c.name}</strong> {c.valid ? null : <Badge tone="err">invalid</Badge>} {c.hasScripts ? <Badge tone="warn">has scripts</Badge> : null}
                <div className="muted">{c.description || c.problem}</div>
                <button disabled={!c.valid || act.busy !== ''} onClick={async () => { await act.run('import', () => client.skillImport(projectId, r.id, c.name)); onImported(); }}>
                  Import {c.name} (it will be offered)
                </button>
              </div>
            ))}
          </li>
        ))}
      </ul>
      {adding ? (
        <div className="form">
          <label>
            Registry id
            <input value={rid} onChange={(e) => setRid(e.target.value)} />
          </label>
          <label>
            GitHub repository (owner/name)
            <input value={repo} onChange={(e) => setRepo(e.target.value)} />
          </label>
          <label>
            License (SPDX id, if known)
            <input value={license} onChange={(e) => setLicense(e.target.value)} />
          </label>
          <label className="inline">
            <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} /> I understand skills from this repository are third-party text that agents will read if I accept them
          </label>
          <div className="form-actions">
            <button className="primary" disabled={!ack || !repo.trim() || !rid.trim() || act.busy !== ''} onClick={async () => { const r = await act.run('add', () => client.skillRegistryAdd({ id: rid.trim(), repo: repo.trim(), license: license.trim() || undefined, trust: 'user' }, ack)); if (r) { setAdding(false); q.reload(); } }}>
              Add to the allowlist
            </button>
            <button onClick={() => setAdding(false)}>Cancel</button>
          </div>
        </div>
      ) : (
        <button onClick={() => setAdding(true)}>Allow another repository</button>
      )}
    </section>
  );
}

export default function SkillsView({ projectId }: { projectId: string }) {
  const client = useClient();
  const q = useLive(() => client.skillList(projectId), [projectId], (e) => e.type.startsWith('skill.'));
  const del = useLive(() => client.skillDeliveries(projectId), [projectId], (e) => e.type.startsWith('skill.') || e.type === 'task.completed');
  const cov = useLive(() => client.skillCoverage(projectId), [projectId], (e) => e.type.startsWith('skill.'));
  const act = useAction();
  const [scan, setScan] = useState<Awaited<ReturnType<typeof client.skillScan>> | undefined>();
  const skills: Skill[] = q.data ?? [];
  const decide = (fn: (p: string, n: string) => Promise<Skill>, name: string) => async () => {
    await act.run(name, () => fn(projectId, name));
    q.reload();
  };
  return (
    <Page title="Skills">
      <p className="muted">
        Skills are instructions agents read. Skills found in the repository or imported from a registry are offered to you; nothing is applied until you accept it. Accepted
        skills are delivered to agents as path-scoped rule files that stay out of the diff.
      </p>
      <div className="form-actions">
        <button disabled={act.busy !== ''} onClick={async () => { const r = await act.run('scan', () => client.skillScan(projectId)); if (r) setScan(r); q.reload(); }}>
          Scan the repository for skills
        </button>
      </div>
      <ErrorNote error={q.error ?? act.error} what="Skills" />
      {scan ? (
        <p role="status" data-testid="skill-scan">
          Found {scan.found}: {scan.offered} offered, {scan.accepted} accepted, {scan.blocked} blocked, {scan.removed} removed.
          {(scan.errors ?? []).length > 0 ? ` Problems: ${(scan.errors ?? []).join('; ')}` : ''}
        </p>
      ) : null}
      {q.data && skills.length === 0 ? <p className="muted">No skills yet. Scan the repository, or import one from a registry.</p> : null}
      <div className="cards">
        {skills.map((s) => (
          <SkillCard key={s.id} s={s} busy={act.busy !== ''} onAccept={decide(client.skillAccept, s.name)} onReject={decide(client.skillReject, s.name)} onRelease={decide(client.skillRelease, s.name)} />
        ))}
      </div>
      <Registries projectId={projectId} onImported={q.reload} />
      <h2>Deliveries</h2>
      <p className="muted" data-testid="skill-coverage">
        {cov.data ? `${cov.data.files} delivered as rule files, ${cov.data.tool} through a tool.` : ''}
      </p>
      {(del.data ?? []).length === 0 ? <p className="muted">No skill has been delivered to an agent yet.</p> : null}
      <table className="table" aria-label="Skill deliveries">
        <tbody>
          {(del.data ?? []).map((d, i) => (
            <tr key={i}>
              <td>{formatDateTime(d.ts)}</td>
              <td>{d.skill}</td>
              <td>
                <a href={`#/task/${encodeURIComponent(d.taskId)}`}>{d.taskId}</a>
              </td>
              <td>
                {d.method}, {d.fidelity}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </Page>
  );
}
