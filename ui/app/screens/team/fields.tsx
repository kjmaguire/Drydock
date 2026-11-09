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
import { Select, selectValues, type SelectOption } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { ModelField } from '../shared/model-field.tsx';
import { effortList, modelList } from './agent-edit.ts';
import { CAP_LABEL, defaultRoutes, presetUntouched } from './team.ts';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AnyHandle = Handle<any, any>;

/** The effort levels an adapter accepts as options: adapter default first, then each level; a value outside the list is
 *  kept visible and marked, so a saved agent's effort is never silently replaced. */
export const effortOptions = (efforts: readonly string[], effort: string): SelectOption[] => [
  { value: '', label: 'adapter default' },
  ...selectValues(efforts),
  ...(effort && !efforts.includes(effort) ? [{ value: effort, label: effort, hint: 'not accepted' }] : []),
];

/** Model and effort inputs. Effort is a list when the adapter is known to accept only certain levels (a wrong one would fail
 *  the run); model is the adapter's aliases as a list with a typed entry, because full model names are accepted too. They are
 *  never disabled while the capabilities are unknown (still loading, or not read): the known lists of `adapter` are offered, or
 *  typed text is taken (decision P137); only capabilities that were read and say "not verified" turn a field off. */
export function ModelEffortFields(
  handle: Handle<{ caps: Capabilities | undefined; adapter?: string; model: string; effort: string; onModel: (v: string) => void; onEffort: (v: string) => void }>,
) {
  return () => {
    const { caps, adapter = '', model, effort, onModel, onEffort } = handle.props;
    const canModel = caps === undefined || caps.modelSelection === true;
    const canEffort = caps === undefined || caps.effortControl === true;
    const efforts = effortList(adapter, caps);
    const models = modelList(adapter, caps);
    return (
      <>
        <Field label="Model">
          {(id) => (
            <ModelField
              id={id}
              value={model}
              models={canModel ? models : []}
              emptyLabel="adapter default"
              disabled={!canModel}
              placeholder={canModel ? 'adapter default' : 'model selection not verified for this adapter'}
              onChange={onModel}
            />
          )}
        </Field>
        <Field label="Effort">
          {(id) =>
            efforts.length > 0 ? (
              <Select id={id} label="Effort" value={effort} disabled={!canEffort} options={effortOptions(efforts, effort)} onChange={onEffort} />
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
const routeRow = css({ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) 120px auto', gap: t.s2, alignItems: 'start' });
const TIERS: SelectOption[] = [
  { value: 'cheap', label: 'cheap' },
  { value: 'premium', label: 'premium' },
];

/**
 * The models the router may choose between for every turn of this agent (internal/core/turnrouting.go, decision P63). Off (empty) by default:
 * the agent then always runs its model. With a list the router starts on a cheap model and escalates to a premium one when
 * the work gets stuck, always on this agent's own CLI.
 */
export function RouteModelsField(handle: Handle<{ routes: RouteModel[]; onRoutes: (r: RouteModel[]) => void; suggestions: readonly string[] }>) {
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
              back. It routes every turn of this agent (task work, reviews, template steps and room replies), only from this list, on this agent's own CLI.
              Every choice and its reason is recorded: Why this model? in the run, the task and the room.
            </p>
            {routes.map((r, i) => (
              <div key={i} mix={routeRow}>
                <ModelField label={`Routed model ${i + 1}`} value={r.model} models={suggestions} placeholder="Choose a model" onChange={(v) => edit(i, { model: v })} />
                <Select
                  label={`Tier of routed model ${i + 1}`}
                  value={r.tier}
                  options={TIERS}
                  onChange={(v) => edit(i, { tier: v as RouteModel['tier'] })}
                />
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
