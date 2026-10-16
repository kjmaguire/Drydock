// The team lead (internal/core/ext_lead.go, internal/pipeline/lead_service.go): a stand-up on demand and the record of the
// lead's interventions. Like the rest of the client: no behavior beyond turning Go's nil slices into empty arrays.

import type { Transport } from './transport.ts';

/** pipeline.StandupResult: the lead posted a stand-up in #general. */
export interface StandupResult {
  messageId: string;
  text: string;
  /** Tasks the stand-up covered. */
  tasks: number;
}

/** pipeline.Intervention: one time the lead stepped in on a task. Message and reason are agent output: shown inert. */
export interface LeadIntervention {
  id: string;
  projectId: string;
  taskId: string;
  runId: string;
  leadAgentId: string;
  /** watchdog | iteration_limit | repeated_revise | stalled | unassigned */
  trigger: string;
  /** What the lead decided (continue, redirect, escalate, stop, assign, split, ...). */
  action: string;
  message: string;
  reason: string;
  targetAgent?: string;
  createdTasks: string[];
  evidence?: string;
  /** The lead's answer could not be applied as given (recorded, not acted on). */
  invalid: boolean;
  messageId?: string;
  createdAt: number;
}

export interface LeadClient {
  /** The lead posts a stand-up in #general (licensed). */
  leadStandup(projectId: string): Promise<StandupResult>;
  leadInterventions(projectId: string, taskId?: string): Promise<LeadIntervention[]>;
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

export function createLeadClient(t: Transport): LeadClient {
  return {
    leadStandup: (projectId) => t.call('lead.standup', { projectId }),
    leadInterventions: async (projectId, taskId = '') =>
      arr(await t.call<LeadIntervention[] | null>('lead.interventions', { projectId, taskId })).map((x) => ({ ...x, createdTasks: arr(x.createdTasks) })),
  };
}
