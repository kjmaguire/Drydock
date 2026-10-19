// Routing rules as an ordered list (evaluation order: highest priority first). Each rule shows when it applies and what it
// does; the editor saves against the rule's version, so a stale edit is refused by the core and its words are shown.

import { css, on, type Handle } from 'remix/component';
import { ListOrdered, Pencil, Plus, Trash2 } from 'lucide';
import type { RouteRule } from '../../api/extTypes.ts';
import { parseInteger } from '../../lib/limits.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, faint, muted } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Checkbox, errorAttrs, Field, formActions } from '../../ui/form.tsx';
import { card, Section } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { ConfirmAction } from '../shared/confirm-action.tsx';
import { actionChips, MAX_CHAIN, newRule, parseChain, parseThen, RUN_KINDS, sortRules, splitChain, withChain } from './routing.ts';

const editorCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))',
  gap: t.s4,
  '& .wide': { gridColumn: '1 / -1' },
  '& textarea': { fontFamily: t.mono, fontSize: t.textSm },
});

/** What the priority says when its text is not a whole number (M25): a typo is never saved as priority 0. */
export const PRIORITY_ERROR = 'Enter the priority as a whole number, for example 10. A rule with a higher number is checked first.';

function RuleEditor(handle: Handle<{ rule: RouteRule; onSaved: () => void; onCancel: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  const r0 = handle.props.rule;
  let priority = String(r0.priority);
  let priorityError: string | undefined;
  let when = r0.when;
  const split = splitChain(r0.then);
  let then = JSON.stringify(split.rest, null, 2);
  let chain = split.chain;
  let scope = r0.scope;
  let enabled = r0.enabled;

  const save = async (button: HTMLElement) => {
    const { rule, onSaved } = handle.props;
    // an empty priority is 0; text that is not a whole number is told under the field and nothing is sent
    const rank = parseInteger(priority, PRIORITY_ERROR);
    if (!rank.ok) {
      priorityError = rank.error;
      act.clear();
      handle.queueTask(() => button.closest('[data-testid="rule-editor"]')?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
      return;
    }
    priorityError = undefined;
    const parsed = parseThen(then);
    if (!parsed.ok) {
      await act.run('parse', async () => {
        throw new Error(parsed.error);
      });
      return;
    }
    const action = withChain(parsed.value, parseChain(chain));
    const r = await act.run('save', () => s.client.routeRulesSet({ ...rule, scope, priority: rank.value ?? 0, when, then: action, enabled }));
    if (r) onSaved();
  };

  const set = (fn: (v: string) => void) => on('input', (e: Event & { currentTarget: HTMLInputElement | HTMLTextAreaElement }) => {
    fn(e.currentTarget.value);
    void handle.update();
  });

  return () => {
    const { rule, onCancel } = handle.props;
    return (
      <div mix={[card, editorCss]} data-testid="rule-editor">
        <div class="wide">
          <ErrorNote error={act.error} what="Rule not saved" />
        </div>
        <Field label="Scope (workspace or project:<id>)">{(id) => <input id={id} value={scope} spellcheck={false} mix={set((v) => (scope = v))} />}</Field>
        <Field label="Priority" error={priorityError}>
          {(id) => <input id={id} inputMode="numeric" value={priority} {...errorAttrs(id, priorityError)} mix={set((v) => ((priority = v), (priorityError = undefined)))} />}
        </Field>
        <div class="wide">
          <Field label="When (CEL expression)">{(id) => <textarea id={id} rows={2} spellcheck={false} value={when} mix={set((v) => (when = v))} />}</Field>
          <p mix={faint} data-testid="run-kinds-hint">
            run.kind is one of {RUN_KINDS.map((k) => `'${k}'`).join(', ')}; for example <code>run.kind == 'room_reply'</code> for an agent&apos;s replies in the
            room.
          </p>
        </div>
        <div class="wide">
          <Field label="Then (action as JSON: prefer_tier, require_tier, require_capability, deny, escalate_to, handoff, set_effort, set_team_size, penalty)">
            {(id) => <textarea id={id} rows={5} spellcheck={false} value={then} mix={set((v) => (then = v))} />}
          </Field>
        </div>
        <div class="wide">
          <Field label={`Fallback chain (models, in order, at most ${MAX_CHAIN})`}>
            {(id) => <input id={id} value={chain} spellcheck={false} placeholder="none" mix={set((v) => (chain = v))} />}
          </Field>
          <p mix={faint}>
            Used only when the chosen model is unavailable before the turn starts: at most 2 hops, the same tier or cheaper.
          </p>
        </div>
        <div class="wide">
          <Checkbox
            label="Enabled"
            checked={enabled}
            onChange={(v) => {
              enabled = v;
              void handle.update();
            }}
          />
        </div>
        <p class="wide" mix={faint}>
          Saving is checked against version {rule.version}; if the rule changed since you read it, the core refuses and you reload.
        </p>
        <div mix={formActions}>
          <Button variant="primary" disabled={act.busy !== ''} mix={on('click', (e) => void save(e.currentTarget))}>
            Save rule
          </Button>
          <Button mix={on('click', () => onCancel())}>Cancel</Button>
        </div>
      </div>
    );
  };
}

const listCss = css({
  display: 'grid',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
  '& > li': { display: 'grid', gap: t.s3, padding: `${t.s3} ${t.s4}`, borderTop: `1px solid ${t.border}`, minWidth: '0' },
  '& > li:first-child': { borderTop: '0' },
});
const ruleRow = css({ display: 'grid', gridTemplateColumns: '36px 1fr auto', gap: t.s3, alignItems: 'start', minWidth: '0' });
const prio = css({
  display: 'grid',
  placeItems: 'center',
  height: '28px',
  borderRadius: t.rMd,
  background: t.bgActive,
  color: t.text,
  fontSize: t.textSm,
  fontWeight: '600',
  fontVariantNumeric: 'tabular-nums',
});
const bodyCss = css({
  display: 'grid',
  gap: '6px',
  minWidth: '0',
  fontSize: t.textMd,
  '& .head': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
  '& .line': { display: 'flex', alignItems: 'baseline', gap: t.s2, flexWrap: 'wrap', minWidth: '0' },
  '& .kw': { width: '34px', flex: 'none', color: t.textFaint, fontSize: t.textXs, fontWeight: '600', textTransform: 'uppercase', letterSpacing: '0.04em' },
  '& code': { overflowWrap: 'anywhere' },
});
const actionsCss = css({ display: 'flex', gap: t.s1, flexWrap: 'wrap', justifyContent: 'flex-end' });

export function RulesPanel(handle: Handle) {
  const s = services(handle);
  const act = action(handle);
  const q = query(handle, { load: () => s.client.routeRulesList(), when: (e) => e.type === 'routing.rule_changed' });
  let editing: string | undefined;
  let adding = false;
  const redraw = () => void handle.update();

  return () => {
    const rules = sortRules(q.data ?? []);
    return (
      <Section
        title="Routing rules"
        label="Routing rules"
        description="Highest priority first. Invariants (data class, quota, budget limit) always win."
        actions={
          <Button
            size="sm"
            icon={Plus}
            mix={on('click', () => {
              adding = true;
              redraw();
            })}
          >
            Add a rule
          </Button>
        }
      >
        <ErrorNote error={q.error ?? act.error} what="Rules" />
        {adding ? (
          <RuleEditor
            rule={newRule()}
            onSaved={() => {
              adding = false;
              redraw();
              q.reload();
            }}
            onCancel={() => {
              adding = false;
              redraw();
            }}
          />
        ) : null}
        {q.loading && !q.data ? <Loading /> : null}
        {q.data && rules.length === 0 && !adding ? (
          <div mix={card}>
            <Empty title="No routing rules" icon={ListOrdered}>
              <p>Without rules the router uses its defaults and the invariants.</p>
            </Empty>
          </div>
        ) : null}
        {rules.length > 0 ? (
          <ol mix={listCss} aria-label="Rules in evaluation order">
            {rules.map((r) => (
              <li key={r.id} data-testid="route-rule" data-rule={r.id}>
                <div mix={ruleRow}>
                  <span mix={prio} title={`priority ${r.priority}`}>
                    {r.priority}
                  </span>
                  <div mix={bodyCss}>
                    <div class="head">
                      <Badge tone={r.enabled ? 'ok' : 'muted'}>{r.enabled ? 'enabled' : 'disabled'}</Badge>
                      <code>{r.id}</code>
                      <span mix={muted}>
                        {r.scope}, v{r.version}
                      </span>
                    </div>
                    <div class="line">
                      <span class="kw">when</span>
                      <code>{r.when}</code>
                    </div>
                    <div class="line">
                      <span class="kw">then</span>
                      {actionChips(r.then).map((c) => (
                        <Badge key={c.key} tone={c.tone} title={JSON.stringify(r.then)}>
                          {c.text}
                        </Badge>
                      ))}
                    </div>
                  </div>
                  {editing === r.id ? null : (
                    <div mix={actionsCss}>
                      <Button
                        size="sm"
                        variant="ghost"
                        icon={Pencil}
                        mix={on('click', () => {
                          editing = r.id;
                          redraw();
                        })}
                      >
                        Edit
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        mix={on('click', async () => {
                          await act.run('toggle', () => s.client.routeRulesSet({ ...r, enabled: !r.enabled }));
                          q.reload();
                        })}
                      >
                        {r.enabled ? 'Disable' : 'Enable'}
                      </Button>
                      <ConfirmAction
                        label="Delete"
                        icon={Trash2}
                        yes="Yes, delete"
                        yesName={`Yes, delete the rule "${r.id}"`}
                        keepName={`Keep the rule "${r.id}"`}
                        question={`Delete the rule "${r.id}"? It stops applying at once and cannot be brought back.`}
                        disabled={act.busy !== ''}
                        testId="route-rule-delete"
                        onConfirm={async () => {
                          await act.run('delete', () => s.client.routeRulesDelete(r.id));
                          q.reload();
                        }}
                      />
                    </div>
                  )}
                </div>
                {editing === r.id ? (
                  <RuleEditor
                    rule={r}
                    onSaved={() => {
                      editing = undefined;
                      redraw();
                      q.reload();
                    }}
                    onCancel={() => {
                      editing = undefined;
                      redraw();
                    }}
                  />
                ) : null}
              </li>
            ))}
          </ol>
        ) : null}
      </Section>
    );
  };
}
