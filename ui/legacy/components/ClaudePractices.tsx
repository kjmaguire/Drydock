import { useEffect, useRef, useState } from 'react';
import type { ClaudePractices } from '../api/libraryClient';
import { useClient } from '../state/context';

/**
 * Claude Code best practices, applied automatically to Claude agents. This loads what Drydock will tell an agent in `role`, and
 * keeps the model and effort fields at the recommended values for that role until the human changes them (a suggestion, never
 * forced: the fields stay editable and the recommendation stays visible).
 */
export function useClaudePractices(adapterId: string, role: string, model: string, effort: string, setModel: (v: string) => void, setEffort: (v: string) => void) {
  const client = useClient();
  const [info, setInfo] = useState<ClaudePractices | undefined>();
  const applied = useRef<{ model: string; effort: string } | undefined>(undefined);
  const r = role.trim();
  useEffect(() => {
    if (adapterId !== 'claude' || !r) {
      setInfo(undefined);
      return;
    }
    let live = true;
    const timer = setTimeout(() => {
      // a failure to ask (the core is gone, the call is missing) must never surface as an unhandled error from a timer
      Promise.resolve()
        .then(() => client.claudePractices(r))
        .then((x) => live && setInfo(x))
        .catch(() => undefined);
    }, 200);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [client, adapterId, r]);
  useEffect(() => {
    if (!info) return;
    const last = applied.current;
    const untouched = (model === '' && effort === '') || (last !== undefined && model === last.model && effort === last.effort);
    if (!untouched) return;
    applied.current = { model: info.preset.model, effort: info.preset.effort };
    if (model !== info.preset.model) setModel(info.preset.model);
    if (effort !== info.preset.effort) setEffort(info.preset.effort);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info]);
  return info;
}

/** The switch and the explanation. Shown only for the claude adapter. */
export function ClaudePracticesPanel({ info, on, onChange }: { info: ClaudePractices | undefined; on: boolean; onChange: (v: boolean) => void }) {
  if (!info) return null;
  return (
    <fieldset data-testid="claude-practices">
      <legend>Claude Code best practices</legend>
      <label className="inline">
        <input type="checkbox" aria-label="Follow Claude Code best practices" checked={on} onChange={(e) => onChange(e.target.checked)} /> Follow Claude Code best practices (recommended)
      </label>
      <p className="muted">
        Recommended for a {info.role} ({info.family}): model <code>{info.preset.model}</code>, effort <code>{info.preset.effort}</code>. {info.preset.why} A new agent starts at these values; change them freely.
      </p>
      <details>
        <summary>What this adds to the agent&apos;s instructions (version {info.version})</summary>
        <pre className="md-code" tabIndex={0} style={{ whiteSpace: 'pre-wrap' }} data-testid="claude-practices-text">{info.text}</pre>
        <p className="muted">It is short on purpose: every line is paid for on every run. Your own purpose text comes after it.</p>
      </details>
    </fieldset>
  );
}
