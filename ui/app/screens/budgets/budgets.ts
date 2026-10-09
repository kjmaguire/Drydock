// Pure helpers for the Budgets screen: limit text, the request a form sends, and the tone of a state.

import type { Budget, BudgetSet, BudgetStatus } from '../../api/extTypes.ts';
import type { Tone } from '../../ui/tokens.ts';
import { numOrUndef } from '../shared/budget-pause.tsx';

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
    usd: initial?.limitUsd != null ? String(initial.limitUsd) : '',
    tokens: initial?.limitTokens != null ? String(initial.limitTokens) : '',
    turns: initial?.limitTurns != null ? String(initial.limitTurns) : '',
    warn: initial ? String(Math.round(initial.warnPct * 100)) : '80',
    note: initial?.note ?? '',
  };
}

/**
 * The budget.set request: the warn percentage as a fraction, unset limits as null (the core refuses a budget with none and
 * its words are shown), and a "*" ceiling carries the project it belongs to. An edit is checked against its version.
 */
export function budgetRequest(v: BudgetFormValues, projectId: string, initial?: Budget): BudgetSet {
  const scopeId = v.scopeId.trim();
  return {
    id: initial?.id,
    version: initial?.version,
    scope: v.scope,
    scopeId,
    projectId: scopeId === '*' ? projectId : undefined,
    limitUsd: numOrUndef(v.usd) ?? null,
    limitTokens: numOrUndef(v.tokens) ?? null,
    limitTurns: numOrUndef(v.turns) ?? null,
    warnPct: (numOrUndef(v.warn) ?? 80) / 100,
    enabled: initial?.enabled ?? true,
    note: v.note,
  };
}

/** "s" when n is not 1. */
export const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;
