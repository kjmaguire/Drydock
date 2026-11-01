// Pure helpers for the Budgets screen: limit text, the request a form sends, and the tone of a state.

import type { Budget, BudgetSet, BudgetStatus } from '../../api/extTypes.ts';
import { numberText, parseDollars, parsePercent, parseWhole } from '../../lib/limits.ts';
import type { Tone } from '../../ui/tokens.ts';

export function limitsText(b: Budget): string {
  const parts: string[] = [];
  if (b.limitUsd != null) parts.push(`$${b.limitUsd}`);
  if (b.limitTokens != null) parts.push(`${b.limitTokens.toLocaleString()} tokens`);
  if (b.limitTurns != null) parts.push(`${b.limitTurns} turns`);
  return parts.join(', ') || 'no limit';
}

/** The tone of a budget state; the words are always shown next to it. */
export function stateTone(state: BudgetStatus['state']): Tone {
  return state === 'exceeded' ? 'err' : state === 'warning' ? 'warn' : 'ok';
}

/** The scopes a budget can limit, in the order the form lists them (the workflow scope: P118). */
export const BUDGET_SCOPES = ['project', 'workflow', 'task', 'run', 'agent'];

/**
 * The scope id when the form's scope changes: a workflow budget starts on the first workflow the core runs, and leaving the
 * workflow scope gives the project's own id back to a project budget (a workflow's name is no project, task, run or agent id).
 */
export function scopeIdAfter(from: string, to: string, scopeId: string, projectId: string, workflows: readonly string[]): string {
  if (to === from) return scopeId;
  if (to === 'workflow') return workflows[0] ?? '';
  if (from === 'workflow') return to === 'project' ? projectId : '';
  return scopeId;
}

export interface BudgetFormValues {
  scope: string;
  scopeId: string;
  usd: string;
  tokens: string;
  turns: string;
  warn: string;
  note: string;
}

export function formValues(projectId: string, initial?: Budget): BudgetFormValues {
  return {
    scope: initial?.scope ?? 'project',
    scopeId: initial?.scopeId ?? projectId,
    usd: initial?.limitUsd != null ? numberText(initial.limitUsd) : '',
    tokens: initial?.limitTokens != null ? numberText(initial.limitTokens) : '',
    turns: initial?.limitTurns != null ? numberText(initial.limitTurns) : '',
    warn: initial ? String(Math.round(initial.warnPct * 100)) : '80',
    note: initial?.note ?? '',
  };
}

// What a limit field says when its text is not a plain number (M25): the unit and an example, in plain words. The same three
// limits are raised on a budget pause (shared/budget-pause.tsx).
export const USD_ERROR = 'Enter the limit in dollars, for example 12.50.';
export const TOKENS_ERROR = 'Enter the limit in tokens as a whole number, for example 200000.';
export const TURNS_ERROR = 'Enter the limit in turns as a whole number, for example 40.';
export const WARN_ERROR = 'Enter the warning point as a whole number of percent from 1 to 99, for example 80.';

/** Which limit fields hold text that is not a number, and what to tell the person under each. */
export interface LimitErrors {
  usd?: string;
  tokens?: string;
  turns?: string;
}

/**
 * The dollars, tokens and turns fields read strictly: an empty one is "no limit on that one" (`undefined`), a plain number is the
 * limit, and anything else ("5 dollars", "$5", "1,000", "1e999", "abc") is an error to show under its field. Nothing is sent
 * while any field has one.
 */
export function parseLimits(f: { usd: string; tokens: string; turns: string }): { ok: true; usd?: number; tokens?: number; turns?: number } | { ok: false; errors: LimitErrors } {
  const usd = parseDollars(f.usd, USD_ERROR);
  const tokens = parseWhole(f.tokens, 1, TOKENS_ERROR);
  const turns = parseWhole(f.turns, 1, TURNS_ERROR);
  if (!usd.ok || !tokens.ok || !turns.ok) {
    return {
      ok: false,
      errors: { usd: usd.ok ? undefined : usd.error, tokens: tokens.ok ? undefined : tokens.error, turns: turns.ok ? undefined : turns.error },
    };
  }
  return { ok: true, usd: usd.value, tokens: tokens.value, turns: turns.value };
}

export interface BudgetFormErrors extends LimitErrors {
  warn?: string;
}

/**
 * The budget.set request for the form, or what is wrong with it. The warn percentage goes as a fraction (empty is the default
 * 80), unset limits as null (the core refuses a budget with none and its words are shown), and a "*" ceiling carries the project
 * it belongs to. An edit is checked against its version. A mistyped limit is an error under its field, never a missing limit.
 */
export function parseBudgetForm(v: BudgetFormValues, projectId: string, initial?: Budget): { ok: true; request: BudgetSet } | { ok: false; errors: BudgetFormErrors } {
  const limits = parseLimits(v);
  const warn = parsePercent(v.warn, WARN_ERROR);
  if (!limits.ok || !warn.ok) return { ok: false, errors: { ...(limits.ok ? {} : limits.errors), warn: warn.ok ? undefined : warn.error } };
  const scopeId = v.scopeId.trim();
  return {
    ok: true,
    request: {
      id: initial?.id,
      version: initial?.version,
      scope: v.scope,
      scopeId,
      // a "*" ceiling and a workflow budget belong to the project they are made in (a workflow runs in every project)
      projectId: scopeId === '*' || v.scope === 'workflow' ? projectId : undefined,
      limitUsd: limits.usd ?? null,
      limitTokens: limits.tokens ?? null,
      limitTurns: limits.turns ?? null,
      warnPct: (warn.value ?? 80) / 100,
      enabled: initial?.enabled ?? true,
      note: v.note,
    },
  };
}

/** "s" when n is not 1. */
export const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;
