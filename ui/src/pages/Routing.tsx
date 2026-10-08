import { useState } from 'react';
import type { RouteRule } from '../api/extTypes';
import { Markdown } from '../lib/Markdown';
import { useAction, numOrUndef } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote, Page } from '../components/common';
import { money } from '../components/Explain';

function RuleEditor({ rule, onSaved, onCancel }: { rule: RouteRule; onSaved: () => void; onCancel: () => void }) {
  const client = useClient();
  const act = useAction();
  const [priority, setPriority] = useState(String(rule.priority));
  const [when, setWhen] = useState(rule.when);
  const [then, setThen] = useState(JSON.stringify(rule.then, null, 2));
  const [scope, setScope] = useState(rule.scope);
  const [enabled, setEnabled] = useState(rule.enabled);
  const save = async () => {
    let parsed: RouteRule['then'];
    try {
      parsed = JSON.parse(then) as RouteRule['then'];
    } catch (e) {
      act.run('parse', async () => {
        throw new Error(`The action is not valid JSON: ${(e as Error).message}`);
      });
      return;
    }
    const r = await act.run('save', () => client.routeRulesSet({ ...rule, scope, priority: numOrUndef(priority) ?? 0, when, then: parsed, enabled }));
    if (r) onSaved();
  };
  return (
    <div className="form">
      <ErrorNote error={act.error} what="Rule not saved" />
      <label>
        Scope (workspace or project:&lt;id&gt;)
        <input value={scope} onChange={(e) => setScope(e.target.value)} />
      </label>
      <label>
        Priority
        <input inputMode="numeric" value={priority} onChange={(e) => setPriority(e.target.value)} />
      </label>
      <label>
        When (CEL expression)
        <textarea value={when} onChange={(e) => setWhen(e.target.value)} rows={2} spellCheck={false} />
      </label>
      <label>
        Then (action as JSON: prefer_tier, require_tier, require_capability, deny, escalate_to, handoff, set_effort, set_team_size, penalty)
        <textarea value={then} onChange={(e) => setThen(e.target.value)} rows={5} spellCheck={false} />
      </label>
      <label className="inline">
        <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} /> Enabled
      </label>
      <p className="muted">Saving is checked against version {rule.version}; if the rule changed since you read it, the core refuses and you reload.</p>
      <div className="form-actions">
        <button className="primary" onClick={save} disabled={act.busy !== ''}>
          Save rule
        </button>
        <button onClick={onCancel}>Cancel</button>
      </div>
    </div>
  );
}

export function RulesPanel() {
  const client = useClient();
  const act = useAction();
  const q = useLive(() => client.routeRulesList(), [], (e) => e.type === 'routing.rule_changed');
  const [editing, setEditing] = useState<string | undefined>();
  const [adding, setAdding] = useState(false);
  const rules = [...(q.data ?? [])].sort((a, b) => b.priority - a.priority || a.id.localeCompare(b.id));
  return (
    <section aria-label="Routing rules">
      <h2>Routing rules</h2>
      <p className="muted">Rules are evaluated by the router; invariants (data class, quota, budget hard limit) always win. Only you can change them.</p>
      <ErrorNote error={q.error ?? act.error} what="Rules" />
      <button onClick={() => setAdding(true)}>Add a rule</button>
      {adding ? (
        <RuleEditor
          rule={{ id: '', scope: 'workspace', priority: 0, when: 'true', then: {}, enabled: false, version: 0 }}
          onSaved={() => { setAdding(false); q.reload(); }}
          onCancel={() => setAdding(false)}
        />
      ) : null}
      <ul className="rule-list">
        {rules.map((r) => (
          <li key={r.id} data-testid="route-rule" data-rule={r.id}>
            <div>
              <Badge tone={r.enabled ? 'ok' : 'muted'}>{r.enabled ? 'enabled' : 'disabled'}</Badge> <code>{r.id}</code> <span className="muted">{r.scope}, priority {r.priority}, v{r.version}</span>
            </div>
            <div>
              when <code>{r.when}</code>
            </div>
            <div>
              then <code>{JSON.stringify(r.then)}</code>
            </div>
            {editing === r.id ? (
              <RuleEditor rule={r} onSaved={() => { setEditing(undefined); q.reload(); }} onCancel={() => setEditing(undefined)} />
            ) : (
              <div className="form-actions">
                <button onClick={() => setEditing(r.id)}>Edit</button>
                <button onClick={async () => { await act.run('toggle', () => client.routeRulesSet({ ...r, enabled: !r.enabled })); q.reload(); }}>{r.enabled ? 'Disable' : 'Enable'}</button>
                <button onClick={async () => { await act.run('delete', () => client.routeRulesDelete(r.id)); q.reload(); }}>Delete</button>
              </div>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

const LEVELS = ['cheapest', 'fast', 'balanced', 'quality', 'maximum_quality'];
const RUN_KINDS = ['agent_task', 'review', 'consult', 'summarize', 'classify'];

export function PreviewPanel() {
  const client = useClient();
  const act = useAction();
  const [runKind, setRunKind] = useState('agent_task');
  const [level, setLevel] = useState('balanced');
  const [role, setRole] = useState('backend');
  const [ceiling, setCeiling] = useState('private');
  const [result, setResult] = useState<Awaited<ReturnType<typeof client.routePreview>> | undefined>();
  return (
    <section aria-label="Preview a routing decision">
      <h2>Preview a decision</h2>
      <p className="muted">A dry run with the same rules, providers, quotas and budgets as a real decision. Nothing is recorded and no quota is taken.</p>
      <div className="form-actions">
        <label>
          Run kind
          <select aria-label="Run kind" value={runKind} onChange={(e) => setRunKind(e.target.value)}>
            {RUN_KINDS.map((k) => <option key={k}>{k}</option>)}
          </select>
        </label>
        <label>
          Level
          <select aria-label="Level" value={level} onChange={(e) => setLevel(e.target.value)}>
            {LEVELS.map((k) => <option key={k}>{k}</option>)}
          </select>
        </label>
        <label>
          Role
          <input value={role} onChange={(e) => setRole(e.target.value)} />
        </label>
        <label>
          Context
          <select aria-label="Context" value={ceiling} onChange={(e) => setCeiling(e.target.value)}>
            <option value="private">private (repository content)</option>
            <option value="public_docs">public documentation only</option>
          </select>
        </label>
        <button className="primary" disabled={act.busy !== ''} onClick={async () => setResult(await act.run('preview', () => client.routePreview({ RunKind: runKind, Level: level, Role: role, ContextCeiling: ceiling })))}>
          Preview
        </button>
      </div>
      <ErrorNote error={act.error} what="Preview failed" />
      {result ? (
        <div data-testid="route-preview" data-blocked={result.blocked}>
          {result.blocked ? (
            <p className="error-note">Blocked: {result.reason}</p>
          ) : (
            <>
              <p>
                Would use <code>{result.decision?.Provider}/{result.decision?.Model || 'default'}</code> (effort {result.decision?.Effort || 'not set'}), estimated cost {money(result.decision?.EstCostUSD)}.
              </p>
              {result.explain ? <Markdown source={result.explain} /> : null}
            </>
          )}
          {(result.alternatives ?? []).length > 0 ? (
            <ul>
              {(result.alternatives ?? []).map((a, i) => (
                <li key={i}>
                  {a.Provider}/{a.Model} rejected: {a.Rejected} {a.Detail}
                </li>
              ))}
            </ul>
          ) : null}
          <p className="muted">Recorded: no.</p>
        </div>
      ) : null}
    </section>
  );
}

export function CheapModelPanel() {
  const client = useClient();
  const providers = useLive(() => client.providerList(), [], (e) => e.type.startsWith('provider.'));
  const act = useAction();
  const [out, setOut] = useState<Record<string, Awaited<ReturnType<typeof client.routeCheapSelect>>>>({});
  const startup = useAction();
  const [startupOn, setStartupOn] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const cheap = (providers.data ?? []).filter((p) => p.tier === 'cheap' && p.dataClass === 'private');
  return (
    <section aria-label="Cheap model selection">
      <h2>Cheap model selection</h2>
      <p className="muted">
        For each enabled cheap, private provider Drydock can test the cheapest few models with a small tool-call check and pick a default and a fallback. The check sends a few
        synthetic requests to the provider and can cost a little.
      </p>
      <ErrorNote error={act.error} what="Selection failed" />
      {providers.data && cheap.length === 0 ? <p className="muted">No enabled cheap private provider. Add one under Providers.</p> : null}
      <ul>
        {cheap.map((p) => (
          <li key={p.id} data-testid="cheap-provider">
            <strong>{p.name}</strong>{' '}
            <CheapStored providerId={p.id} fresh={out[p.id]} />{' '}
            <button
              disabled={act.busy !== ''}
              onClick={async () => {
                const r = await act.run(p.id, () => client.routeCheapSelect(p.id, false));
                if (r) setOut({ ...out, [p.id]: r });
              }}
            >
              Probe and select now
            </button>
          </li>
        ))}
      </ul>
      <h3>Probe at startup</h3>
      <ErrorNote error={startup.error} />
      {!confirm ? (
        <button onClick={() => setConfirm(true)}>Change the startup probe...</button>
      ) : (
        <div className="warn-note">
          <p>The startup probe sends a few synthetic requests to each cheap private provider every time Drydock starts. That can cost a little. It is off unless you turn it on.</p>
          <div className="form-actions">
            <button className="primary" onClick={async () => { const r = await startup.run('on', () => client.routeProbeStartup(true)); if (r) { setStartupOn(true); setConfirm(false); } }}>Turn on</button>
            <button onClick={async () => { const r = await startup.run('off', () => client.routeProbeStartup(false)); if (r) { setStartupOn(false); setConfirm(false); } }}>Turn off</button>
            <button onClick={() => setConfirm(false)}>Cancel</button>
          </div>
        </div>
      )}
      <p role="status" className="muted">{startupOn ? 'Startup probe: on (set in this session).' : 'Startup probe: the core does not report its current value; set it here.'}</p>
    </section>
  );
}

function CheapStored({ providerId, fresh }: { providerId: string; fresh?: { default?: string; fallback?: string; message: string } }) {
  const client = useClient();
  const q = useLive(() => client.routeCheapGet(providerId), [providerId, fresh?.message], () => false);
  const sel = fresh ?? q.data?.selection ?? undefined;
  if (!sel) return <span className="muted">no selection yet</span>;
  return (
    <span data-testid="cheap-selection">
      default <code>{sel.default || 'none passed'}</code>, fallback <code>{sel.fallback || 'none'}</code> <span className="muted">{sel.message}</span>
    </span>
  );
}

export default function RoutingView() {
  return (
    <Page title="Routing">
      <p className="muted">
        Agent turns still use each agent&apos;s configured model; the router decides for the app&apos;s own model calls and records a decision for every turn so outcomes can be
        compared. Where a decision was static, the explanation says so.
      </p>
      <RulesPanel />
      <PreviewPanel />
      <CheapModelPanel />
    </Page>
  );
}
