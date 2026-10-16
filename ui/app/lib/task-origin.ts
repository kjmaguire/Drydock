// Where a task came from, as the core records it (tasks.Task.Origin, audit 2026-10-16, M27). A task the team lead split off
// carries "lead:N", N being how many steps it is from a task a person made; it waits in the backlog until a person moves it on.
// Every other task has no origin. The value comes from the core: anything that is not exactly that shape is no origin here.

const LEAD_ORIGIN = /^lead:([1-9][0-9]{0,2})$/;

/** N of "lead:N" for a task the team lead split off, or undefined for any other value (absent, empty, not a string, or any other text). */
export function leadStep(origin: unknown): number | undefined {
  if (typeof origin !== 'string') return undefined;
  const m = LEAD_ORIGIN.exec(origin);
  return m?.[1] === undefined ? undefined : Number(m[1]);
}

/** The words of the badge and of the sentence under it on the task page. */
export const LEAD_ORIGIN_LABEL = 'Split off by the team lead';
export const LEAD_ORIGIN_WAITS = 'It waits in the backlog until you move it on; the lead cannot start it by itself.';

/** The tooltip of the badge. */
export function leadStepText(step: number): string {
  return `Step ${step} from a task you made`;
}
