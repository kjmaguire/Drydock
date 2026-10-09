// Form pieces shared by the add-agent form, the agent editor and the template form: model and effort, the Claude Code best
// practices (logic and panel), model routing, and the capability list of an adapter.

import { css, on, type Handle } from 'remix/component';
import { Check, Minus, Plus, Route, Sparkles, X } from 'lucide';
import type { ClaudePractices } from '../../api/libraryClient.ts';
import type { Capabilities, RouteModel } from '../../api/types.ts';
import { services } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { Field } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { CAP_LABEL, defaultRoutes, presetUntouched } from './team.ts';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AnyHandle = Handle<any, any>;

/** Model and effort inputs. Effort is a list when the adapter is known to accept only certain levels (a wrong one would fail
 *  the run); model is free text with the adapter's aliases offered, because full model names are accepted too. */
export function ModelEffortFields(
  handle: Handle<{ caps: Capabilities | undefined; model: string; effort: string; onModel: (v: string) => void; onEffort: (v: string) => void }>,
) {
  const listId = `models-${handle.id}`;
  return () => {
    const { caps, model, effort, onModel, onEffort } = handle.props;
    const canModel = caps?.modelSelection === true;
    const canEffort = caps?.effortControl === true;
    const efforts = caps?.efforts ?? [];
    const models = caps?.models ?? [];
    return (
      <>
        <Field label="Model">
          {(id) => (
            <>
              <input
                id={id}
                value={model}
                disabled={!canModel}
                list={models.length > 0 ? listId : undefined}
                spellcheck={false}
                autocomplete="off"
                placeholder={canModel ? 'adapter default' : 'model selection not verified for this adapter'}
                mix={on('input', (e) => onModel(e.currentTarget.value))}
              />
              {models.length > 0 ? (
                <datalist id={listId}>
                  {models.map((m) => (
                    <option key={m} value={m} />
                  ))}
                </datalist>
              ) : null}
            </>
          )}
        </Field>
        <Field label="Effort">
          {(id) =>
            efforts.length > 0 ? (
              <select id={id} aria-label="Effort" value={effort} disabled={!canEffort} mix={on('change', (e) => onEffort(e.currentTarget.value))}>
                <option value="">adapter default</option>
                {efforts.map((x) => (
                  <option key={x} value={x}>
                    {x}
                  </option>
                ))}
                {effort && !efforts.includes(effort) ? <option value={effort}>{effort} (not accepted)</option> : null}
              </select>
            ) : (
              <input
                id={id}
                aria-label="Effort"
                value={effort}
                disabled={!canEffort}
                spellcheck={false}
                autocomplete="off"
                placeholder={canEffort ? 'adapter default' : 'effort control not verified for this adapter'}
                mix={on('input', (e) => onEffort(e.currentTarget.value))}
              />
            )
          }
        </Field>
      </>
    );
  };
}

export interface PracticesFields {
  model: () => string;
  effort: () => string;
  set: (model: string, effort: string) => void;
}

export interface Practices {
  /** What Drydock will tell a Claude agent in this role; undefined for another adapter or an empty role. */
  readonly info: ClaudePractices | undefined;
}

/**
 * Claude Code best practices, applied automatically to Claude agents (call in setup). Reading `info` during render follows
 * the adapter and role: a change asks the core again after 200ms. With `fields`, the model and effort are kept at the
 * recommended values for the role until the human changes them (a suggestion, never forced: the fields stay editable and
 * the recommendation stays visible). Without `fields` (an existing agent) the recommendation is only shown.
 */
export function claudePractices(handle: AnyHandle, opts: { adapter: () => string; role: () => string; fields?: PracticesFields; delayMs?: number }): Practices {
  const client = services(handle).client;
  let info: ClaudePractices | undefined;
  let key: string | undefined;
  let gen = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let applied: { model: string; effort: string } | undefined;
  handle.signal.addEventListener('abort', () => clearTimeout(timer));

  const apply = (x: ClaudePractices) => {
    const f = opts.fields;
    if (!f || !presetUntouched(f.model(), f.effort(), applied)) return;
    applied = { model: x.preset.model, effort: x.preset.effort };
    f.set(x.preset.model, x.preset.effort);
  };

  const sync = () => {
    const role = opts.role().trim();
    const next = opts.adapter() === 'claude' && role ? role : '';
    if (next === key) return;
    key = next;
    clearTimeout(timer);
    const mine = ++gen;
    if (!next) {
      info = undefined;
      return;
    }
    timer = setTimeout(() => {
      // a failure to ask (the core is gone, the call is missing) must never surface as an unhandled error from a timer
      Promise.resolve()
        .then(() => client.claudePractices(next))
        .then((x) => {
          if (mine !== gen || handle.signal.aborted) return;
          info = x;
          apply(x);
          void handle.update();
        })
        .catch(() => undefined);
    }, opts.delayMs ?? 200);
  };

  return {
    get info() {
      sync();
      return info;
    },
  };
}

const practicesCss = css({
  display: 'grid',
  gap: t.s2,
  gridColumn: '1 / -1',
  background: t.accentSoft,
  borderColor: `color-mix(in srgb, ${t.accent} 30%, transparent)`,
  '& legend': { display: 'inline-flex', alignItems: 'center', gap: '6px', color: t.accentText },
  '& p': { margin: '0', fontSize: t.textSm },
  '& pre': { whiteSpace: 'pre-wrap', marginTop: t.s2, maxHeight: '260px' },
});

const inlineCheck = css({ display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textMd, cursor: 'pointer', '& input': { margin: '0' } });

/** The switch and the explanation. Shown only for the claude adapter (when there is a recommendation). */
export function ClaudePracticesPanel(handle: Handle<{ info: ClaudePractices | undefined; on: boolean; onChange: (v: boolean) => void }>) {
  return () => {
    const { info, on: checked, onChange } = handle.props;
    if (!info) return null;
    return (
      <fieldset mix={practicesCss} data-testid="claude-practices">
        <legend>
          <Icon icon={Sparkles} size={14} />
          Claude Code best practices
        </legend>
        <label mix={inlineCheck}>
          <input type="checkbox" aria-label="Follow Claude Code best practices" checked={checked} mix={on('change', (e) => onChange(e.currentTarget.checked))} />
          Follow Claude Code best practices (recommended)
        </label>
        <p mix={muted}>
          Recommended for a {info.role} ({info.family}): model <code>{info.preset.model}</code>, effort <code>{info.preset.effort}</code>. {info.preset.why} A new agent
          starts at these values; change them freely.
        </p>
        <details>
          <summary>What this adds to the agent's instructions (version {info.version})</summary>
          <pre tabIndex={0} data-testid="claude-practices-text">
            {info.text}
          </pre>
          <p mix={muted}>It is short on purpose: every line is paid for on every run. Your own purpose text comes after it.</p>
        </details>
      </fieldset>
    );
  };
}

const routesCss = css({
  display: 'grid',
  gap: t.s3,
  gridColumn: '1 / -1',
  '& legend': { display: 'inline-flex', alignItems: 'center', gap: '6px' },
  '& p': { margin: '0', fontSize: t.textSm },
});
const routeRow = css({ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) 120px auto', gap: t.s2, alignItems: 'center' });

/**
 * The models the router may choose between for this agent's turns (internal/core/turnrouting.go). Off (empty) by default:
 * the agent then always runs its model. With a list the router starts on a cheap model and escalates to a premium one when
 * the work gets stuck, always on this agent's own CLI.
 */
export function RouteModelsField(handle: Handle<{ routes: RouteModel[]; onRoutes: (r: RouteModel[]) => void; suggestions: readonly string[] }>) {
  const listId = `route-models-${handle.id}`;
  return () => {
    const { routes, onRoutes, suggestions } = handle.props;
    const enabled = routes.length > 0;
    const edit = (i: number, p: Partial<RouteModel>) => onRoutes(routes.map((x, j) => (j === i ? { ...x, ...p } : x)));
    return (
      <fieldset mix={routesCss} data-testid="route-models">
        <legend>
          <Icon icon={Route} size={14} />
          Model routing
        </legend>
        <label mix={inlineCheck}>
          <input type="checkbox" checked={enabled} mix={on('change', (e) => onRoutes(e.currentTarget.checked ? defaultRoutes(suggestions) : []))} />
          Let the router choose this agent's model for each turn
        </label>
        {enabled ? (
          <>
            <p mix={muted}>
              It starts on a cheap model and moves to a premium one after three cheap tries that fail their checks, or when the reviewer keeps sending premium work
              back. It only chooses from this list, on this agent's own CLI. Every choice and its reason is in the task's run explanation.
            </p>
            {routes.map((r, i) => (
              <div key={i} mix={routeRow}>
                <input aria-label={`Routed model ${i + 1}`} value={r.model} list={listId} spellcheck={false} autocomplete="off" mix={on('input', (e) => edit(i, { model: e.currentTarget.value }))} />
                <select aria-label={`Tier of routed model ${i + 1}`} value={r.tier} mix={on('change', (e) => edit(i, { tier: e.currentTarget.value as RouteModel['tier'] }))}>
                  <option value="cheap">cheap</option>
                  <option value="premium">premium</option>
                </select>
                <IconButton icon={X} label={`Remove routed model ${i + 1}`} mix={on('click', () => onRoutes(routes.filter((_, j) => j !== i)))} />
              </div>
            ))}
            {routes.length < 6 ? (
              <div>
                <Button size="sm" icon={Plus} mix={on('click', () => onRoutes([...routes, { model: '', tier: 'premium' }]))}>
                  Add a model
                </Button>
              </div>
            ) : null}
            <datalist id={listId}>
              {suggestions.map((m) => (
                <option key={m} value={m} />
              ))}
            </datalist>
          </>
        ) : null}
      </fieldset>
    );
  };
}

const capsCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(170px, 1fr))',
  gap: '4px 12px',
  margin: `${t.s2} 0 0`,
  padding: '0',
  listStyle: 'none',
  fontSize: t.textSm,
  '& li': { display: 'flex', alignItems: 'center', gap: '6px', minWidth: '0' },
  '& li[data-verified="true"] svg': { color: t.success },
  '& li[data-verified="false"] svg': { color: t.textFaint },
});

/** Capabilities are shown as verified or "not verified": a missing or false capability is never claimed as absent. */
export function CapabilityList(handle: Handle<{ caps: Capabilities | undefined }>) {
  return () => {
    const caps = handle.props.caps;
    return (
      <ul mix={capsCss} aria-label="Capabilities">
        {CAP_LABEL.map(([k, label]) => {
          const ok = caps?.[k] === true;
          return (
            <li key={k} data-cap={k} data-verified={String(ok)}>
              <Icon icon={ok ? Check : Minus} size={13} />
              <span>
                {label}: <span mix={ok ? undefined : muted}>{ok ? 'verified' : 'not verified'}</span>
              </span>
            </li>
          );
        })}
      </ul>
    );
  };
}
