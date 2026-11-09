// The team lead (internal/core/ext_lead.go, internal/pipeline/lead_service.go): a stand-up on demand and the record of the
// lead's interventions. Like the rest of the client: no behavior beyond turning Go's nil slices into empty arrays.

import type { Transport } from './transport.ts';
import type { Task } from './types.ts';

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
  /** watchdog | iteration_limit | repeated_revise | stalled | unassigned | requested (a person asked: leadSplit) */
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

/**
 * core.SplitResult: the lead's answer to a person's request to split a task. action "split" made createdTasks (in BACKLOG,
 * until a person moves them on); "escalate" is the lead saying why the task should not be split (message, reason). Message and
 * reason are agent output: shown inert.
 */
export interface SplitResult {
  action: 'split' | 'escalate';
  message: string;
  reason: string;
  leadId: string;
  leadName: string;
  createdTasks: Task[];
  interventionId: string;
  /** The lead's answers could not be used twice; the decision became an escalation. */
  invalid?: boolean;
}

/** A project's stand-up schedule (H11): off by default; when on, the lead posts once each weekday from hourLocal (the PC's time). */
export interface StandupSchedule {
  projectId: string;
  enabled: boolean;
  hourLocal: number;
}

export interface LeadClient {
  /** The lead posts a stand-up in #general (licensed). */
  leadStandup(projectId: string): Promise<StandupResult>;
  leadStandupScheduleGet(projectId: string): Promise<StandupSchedule>;
  /** Only the person sets it: a scheduled stand-up is a paid lead turn each weekday. */
  leadStandupScheduleSet(s: StandupSchedule): Promise<StandupSchedule>;
  leadInterventions(projectId: string, taskId?: string): Promise<LeadIntervention[]>;
  /** Asks the project's team lead to split the task (licensed; refused without a lead on the team). */
  leadSplit(taskId: string): Promise<SplitResult>;
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

export function createLeadClient(t: Transport): LeadClient {
  return {
    leadStandup: (projectId) => t.call('lead.standup', { projectId }),
    leadStandupScheduleGet: (projectId) => t.call('lead.standup.schedule.get', { projectId }),
    leadStandupScheduleSet: (s) => t.call('lead.standup.schedule.set', s),
    leadSplit: async (taskId) => {
      const r = await t.call<SplitResult>('lead.split', { taskId });
      return { ...r, createdTasks: arr(r.createdTasks) };
    },
    leadInterventions: async (projectId, taskId = '') =>
      arr(await t.call<LeadIntervention[] | null>('lead.interventions', { projectId, taskId })).map((x) => ({ ...x, createdTasks: arr(x.createdTasks) })),
  };
}
