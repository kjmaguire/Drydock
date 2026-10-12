// Plain data fixtures for tests (no behavior). Override any field with the `over` argument.

import type { BudgetStatus } from '../api/extTypes.ts';
import type { LicenseView } from '../api/pkgTypes.ts';
import type { UsageRow } from '../api/types.ts';

export const lic = (over: Partial<LicenseView> = {}): LicenseView => ({
  configured: true,
  devMode: false,
  state: 'active',
  readOnly: false,
  message: 'paid license valid for about 40 more day(s).',
  daysLeft: 40,
  expiresAt: 1893456000,
  kind: 'paid',
  plan: 'pro',
  name: 'Ada',
  subject: 'ada@example.com',
  licenseId: 'lic_1',
  channels: ['stable'],
  clockSuspect: false,
  ...over,
});

export const unconfiguredLicense = lic({
  configured: false,
  devMode: true,
  state: 'none',
  readOnly: false,
  daysLeft: 0,
  expiresAt: undefined,
  kind: undefined,
  plan: undefined,
  name: undefined,
  subject: undefined,
  licenseId: undefined,
  message: 'Licensing is not configured in this build. This is a development or unlicensed build, so every feature is allowed.',
  banner: undefined,
});

export const expiredLicense = lic({
  state: 'expired',
  readOnly: true,
  daysLeft: 0,
  message: 'License expired. Drydock is read-only.',
  banner: 'License expired: Drydock is read-only. You can read, export and back up everything; nothing has been deleted. Import a renewed license to continue.',
});

export const usage = (over: Partial<UsageRow> = {}): UsageRow => ({
  ts: 1_700_000_000_000,
  taskId: 't1',
  agentId: 'a1',
  provider: 'claude',
  model: 'm',
  tier: 'premium',
  inputTokens: 10,
  outputTokens: 5,
  cachedTokens: 2,
  cacheWriteTokens: 0,
  reasoningTokens: 0,
  costUsd: 0.0123,
  source: 'reported',
  cacheHit: false,
  ...over,
});

export const budget = (over: Partial<BudgetStatus['budget']> = {}): BudgetStatus['budget'] => ({
  id: 'bud_1',
  scope: 'project',
  scopeId: 'prj_1',
  limitUsd: 5,
  warnPct: 0.8,
  enabled: true,
  version: 1,
  createdAt: 0,
  updatedAt: 0,
  updatedBy: 'human:kyle',
  ...over,
});

export const budgetStatus = (over: Partial<BudgetStatus> = {}): BudgetStatus => ({
  budget: budget(),
  instanceId: 'prj_1',
  pct: 0.5,
  binding: 'usd',
  pctUsd: 0.5,
  state: 'ok',
  costComplete: true,
  costLabel: 'reported',
  message: '',
  spend: { rows: 3, reportedUsd: 2.5, estimatedUsd: 0, unknownCostRows: 0, inputTokens: 1000, outputTokens: 500, cachedTokens: 100, turns: 3 },
  ...over,
});
