// The controls a person uses on a task and its agents (internal/core/ext_control.go): steer the running agent, pause and
// resume the task, and see what an assignment would do (the command menu's "What happens" panel); and their word on each
// acceptance criterion (internal/core/ext_criteria.go). Handing a task on and taking
// it on yourself are taskAssign (client.ts); asking the team lead to split a task is leadSplit (leadClient.ts). Like the rest of
// the client: no behavior beyond turning Go's nil slices into empty arrays.

import type { Transport } from './transport.ts';
import type { CriterionStatus, TaskDetail, TaskStatus } from './types.ts';

/**
 * core.SteerResult. delivered "now": the running turn was interrupted at its current step and goes on, in the same session,
 * with the message; "next": no turn was running (the checks were, or the task is paused), so it goes with the next turn.
 */
export interface SteerResult {
  delivered: 'now' | 'next';
  /** The message in the task's channel, posted as the person's (secrets redacted). */
  messageId: string;
}

/** core.QueuedTask: a task in an agent's queue. */
export interface QueuedTask {
  id: string;
  seq: number;
  title: string;
  status: TaskStatus;
}

/** core.PreviewAgent: the agent an assignment names. */
export interface PreviewAgent {
  id: string;
  name: string;
  role: string;
  adapter: string;
  model: string;
  /** Where its CLI runs, the project's default applied: native or wsl. */
  runMode: string;
  wslDistro?: string;
  /** The agent's stored status. */
  status: string;
  /** What it is doing now (Working, Idle, Paused, ...), when known. */
  presence?: string;
}

/** core.AssignPreview: what assigning a task would do. Changes nothing. */
export interface AssignPreview {
  taskId: string;
  assignee: string;
  /** The assignee is the person (app.info's human): no agent works on the task. agent, queue and waits are then empty. */
  person: boolean;
  /** The branch the task's worktree is (or will be) on: drydock/<task id>-<slug of its title>. */
  branch: string;
  /** The task already has its worktree: the work goes on there. */
  hasWorktree: boolean;
  /** The template the task runs (iterate-until-verified when it names none). */
  template: string;
  specGate: boolean;
  testFirst: boolean;
  /** Check kinds, in the order they run: build/test/lint/typecheck as detected, secrets, deps, then e2e, scans, preview, ci. */
  checks: string[];
  /** local_merge | pr_flow */
  mode: string;
  /** The branch is pushed to GitHub (pr_flow). */
  pushes: boolean;
  /** False: a pull-request project pushes after the independent review without asking (except a push that changes CI). */
  asksBeforePush: boolean;
  /** Always true: nothing merges before a person approves. */
  asksBeforeMerge: boolean;
  /** Who would review the work (taskAssign's rule when no reviewer is named); '' when nobody can. */
  reviewer: string;
  /** A run is being driven now: assigning stops its current turn and the new agent takes over in the same worktree. */
  stopsRun: boolean;
  agent?: PreviewAgent;
  /** The agent's other open tasks, oldest first (it works on them side by side). */
  queue: QueuedTask[];
  /** Started now, the task would wait before its agent works: waitReason says why. */
  waits: boolean;
  waitReason?: string;
  /** The earlier task it waits for (one changing the same code); absent when it waits for a free agent slot. */
  waitsFor?: QueuedTask;
}

export interface ControlClient {
  /** A person's message to the task's running agent, read before its next step. Refused when the task has no active run. */
  taskSteer(id: string, message: string): Promise<SteerResult>;
  /** Stops the task's run where it is (the worktree stays); refused when nothing runs or the run waits for the person. */
  taskPause(id: string): Promise<TaskDetail>;
  /** Goes on with a paused task from where it was (licensed). */
  taskResume(id: string): Promise<TaskDetail>;
  /** What assigning the task to `assignee` (an agent id, or app.info's human) would do. */
  taskAssignPreview(id: string, assignee: string): Promise<AssignPreview>;
  /**
   * The person's word on one acceptance criterion (its place in task.acceptance, from 0): met, not_met, waived (note: why) or
   * open (their earlier word no longer counts). Returns every criterion's status after it. The core records the person as the
   * author; confirming and waiving are licensed, saying not met and reopening are not.
   */
  taskCriterionMark(taskId: string, index: number, mark: 'met' | 'not_met' | 'waived' | 'open', note?: string): Promise<CriterionStatus[]>;
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

const detail = (d: TaskDetail): TaskDetail => ({ ...d, evidence: arr(d.evidence), reviews: arr(d.reviews), pending: arr(d.pending) });

export function createControlClient(t: Transport): ControlClient {
  return {
    taskSteer: (id, message) => t.call('task.steer', { id, message }),
    taskPause: async (id) => detail(await t.call<TaskDetail>('task.pause', { id })),
    taskResume: async (id) => detail(await t.call<TaskDetail>('task.resume', { id })),
    taskAssignPreview: async (id, assignee) => {
      const p = await t.call<AssignPreview>('task.assignPreview', { id, assignee });
      return { ...p, checks: arr(p.checks), queue: arr(p.queue) };
    },
    taskCriterionMark: async (taskId, index, mark, note) => arr(await t.call<CriterionStatus[]>('task.criterion.mark', { taskId, index, mark, note: note ?? '' })),
  };
}
