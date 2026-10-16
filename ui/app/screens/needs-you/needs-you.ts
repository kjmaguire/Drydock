import type { NeedsYouItem } from '../../api/types.ts';
import { routes } from '../../routes.ts';

export const KIND_LABEL: Record<string, string> = {
  approval: 'Approval',
  review: 'Review',
  question: 'Question',
  failed_check: 'Failed check',
  watchdog_pause: 'Watchdog pause',
};

/** Where clicking an item goes. Items are derived by the core on every query; nothing here is stored. */
export function needsTarget(it: NeedsYouItem): string {
  // a tool call an agent wants to make: its exact arguments and the Approve button are on the project's Integrations page
  if (it.kind === 'approval' && /^mcp_call:/.test(it.title)) return routes.projectIntegrations.href();
  if (it.taskId) return routes.task.href({ id: it.taskId });
  if (it.roomId) return routes.room.href({ id: it.roomId });
  // an approval with no task is an outbound write to an integration: its exact text is on the project's Integrations page
  if (it.kind === 'approval') return routes.projectIntegrations.href();
  return routes.needsYou.href();
}

/** A plain-language hint for the kinds of item that need more than a click. Derived from the title; the title is outside text, so it only selects a fixed hint. */
export function needsHint(it: NeedsYouItem): string {
  if (it.kind === 'approval' && /^Budget limit/i.test(it.title)) return 'A budget stopped the next agent turn. Open the task to raise the limit and resume, or stop the run.';
  if (it.kind === 'approval' && /^The repository's git configuration changed/i.test(it.title))
    return "Drydock stopped running git in the repository: its git configuration, hooks or attributes changed. Open the task to see which items changed; trust them only if you made or expect the change.";
  if (it.kind === 'review' && /^Accept the spec/i.test(it.title)) return 'The spec gate: nothing is built until you accept the spec.';
  if (/^Iteration limit reached|^The reviewer keeps asking for the same change/i.test(it.title))
    return 'The task cannot go on by itself. Open it to continue with a fresh loop budget (and guidance, if you like) or to stop it.';
  if (/^The team lead asks for a human decision/i.test(it.title)) return "The team lead stopped and asks you. Open the task to read its recommendation, then continue with guidance or stop.";
  if (/^Watchdog paused/i.test(it.title)) return 'The watchdog stopped an agent turn. Open the task to see why, then send guidance and continue, or stop.';
  // the question a blocking gate asks when none of its checks could judge (pipeline.noVerdict, gate "verdict")
  if (it.kind === 'question' && /^No check gave a result for /i.test(it.title))
    return 'No check could judge this work, so nothing says it is fine. Open the task to check again, go on without a result this time, or stop.';
  if (it.kind === 'approval' && /^mcp_call:/.test(it.title)) return "An agent wants to call a tool that is not marked read-only. Open the project's Integrations page to see the exact call and approve or reject it.";
  if (it.kind === 'approval' && /^Permission request: /.test(it.title))
    return "A worker asks to use a tool its rules do not allow, and its turn waits. Open the task to see the exact tool and input; allow it once or refuse.";
  if (it.kind === 'approval' && !it.taskId && !it.roomId) return "An outbound write to a connected service. Open the project's Integrations page to read the exact text and approve or decline.";
  return '';
}
