// A clone's progress with Cancel (B12): "Cloning gocalc… 42%" from the core's github.clone_progress events (the repository,
// git's phase and the percent, nothing else), and Cancel, which ends the clone (github.cloneCancel): the core kills gh and the
// git it started, removes the half-made folder, and the clone's own call answers that it was cancelled. Used where a clone
// runs: New project's Create, the start box's Enter with a repository, and Attach a repository.

import { css, on, type Handle } from 'remix/component';
import type { DrydockEvent } from '../../api/types.ts';
import { services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { t } from '../../ui/tokens.ts';

const rowCss = css({ display: 'inline-flex', alignItems: 'center', gap: t.s2, minWidth: '0', color: t.textMuted, fontSize: t.textSm });

export interface CloneStep {
  phase: string;
  percent: number;
}

/** The clone's progress from one event, when it is about this repository. */
export function cloneStepOf(e: DrydockEvent, repo: string): CloneStep | undefined {
  if (e.type !== 'github.clone_progress') return undefined;
  const d = (e.data ?? {}) as { repo?: unknown; phase?: unknown; percent?: unknown };
  if (typeof d.repo !== 'string' || d.repo.toLowerCase() !== repo.trim().toLowerCase()) return undefined;
  const percent = Number(d.percent);
  if (!Number.isFinite(percent)) return undefined;
  return { phase: typeof d.phase === 'string' ? d.phase : '', percent: Math.max(0, Math.min(100, Math.round(percent))) };
}

/** "Cloning gocalc… 42%", or "Cloning gocalc…" before git says how far it is. */
export function cloneLine(repo: string, step: CloneStep | undefined): string {
  const name = repo.split('/').pop() || repo;
  return step ? `Cloning ${name}… ${step.percent}%` : `Cloning ${name}…`;
}

export function CloneProgress(handle: Handle<{ repo: string }>) {
  const s = services(handle);
  let step: CloneStep | undefined;
  let cancelling = false;
  const off = s.store.onEvent((e) => {
    const next = cloneStepOf(e, handle.props.repo);
    if (next) {
      step = next;
      void handle.update();
    }
  });
  handle.signal.addEventListener('abort', () => void off());
  const cancel = async () => {
    cancelling = true;
    void handle.update();
    try {
      await s.client.githubCloneCancel(handle.props.repo);
    } catch {
      cancelling = false; // the clone may have ended meanwhile; its own answer says how
      void handle.update();
    }
  };
  return () => (
    <span mix={rowCss}>
      <span role="status" data-testid="clone-progress" title={step?.phase || undefined}>
        {cancelling ? 'Cancelling the clone…' : cloneLine(handle.props.repo, step)}
      </span>
      <Button type="button" size="sm" variant="ghost" disabled={cancelling} data-testid="clone-cancel" mix={on('click', () => void cancel())}>
        Cancel
      </Button>
    </span>
  );
}
