import { useCallback, useRef, useState } from 'react';

/** Run an async action with a busy flag and the error it threw (shown verbatim by the caller). One at a time. */
export function useAction() {
  const [busy, setBusy] = useState('');
  const [error, setError] = useState<Error | undefined>();
  const live = useRef(true);
  void live;
  const run = useCallback(async <T,>(label: string, fn: () => Promise<T>): Promise<T | undefined> => {
    setBusy(label);
    setError(undefined);
    try {
      return await fn();
    } catch (e) {
      setError(e as Error);
      return undefined;
    } finally {
      setBusy('');
    }
  }, []);
  return { busy, error, run, clear: () => setError(undefined) };
}

/** Parse a number field: '' means unset. NaN is returned as undefined so the core never sees it. */
export function numOrUndef(s: string): number | undefined {
  const t = s.trim();
  if (t === '') return undefined;
  const n = Number(t);
  return Number.isFinite(n) ? n : undefined;
}
