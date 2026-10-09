// A thin progress bar for a used fraction (budgets, quota windows, eval rates). The bar is decoration for the words next to
// it: callers always print the number too, and the bar carries the same number for assistive technology.

import { css, type Handle } from 'remix/component';
import { t, tone, type Tone } from '../../ui/tokens.ts';

const trackCss = css({
  position: 'relative',
  height: '6px',
  borderRadius: t.rFull,
  background: t.bgActive,
  overflow: 'hidden',
  minWidth: '0',
});

const fillCss = css({
  position: 'absolute',
  inset: '0 auto 0 0',
  borderRadius: t.rFull,
  transition: `width ${t.med} ${t.ease}`,
});

const markCss = css({ position: 'absolute', top: '0', bottom: '0', width: '2px', background: t.bgSurface });

/** Clamp a fraction to 0..1 for drawing (the text keeps the real value, which can be over 100%). */
export function drawFraction(v: number | null | undefined): number {
  if (v === null || v === undefined || !Number.isFinite(v) || v <= 0) return 0;
  return Math.min(1, v);
}

export function Meter(handle: Handle<{ value: number | null | undefined; label: string; tone?: Tone; mark?: number; testId?: string }>) {
  return () => {
    const { value, label, tone: tn = 'accent', mark, testId } = handle.props;
    const f = drawFraction(value);
    const now = value === null || value === undefined || !Number.isFinite(value) ? undefined : Math.round(value * 100);
    return (
      <div
        mix={trackCss}
        role="progressbar"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={now === undefined ? undefined : Math.min(100, Math.max(0, now))}
        aria-valuetext={label}
        data-testid={testId}
      >
        <div mix={fillCss} style={{ width: `${Math.round(f * 1000) / 10}%`, background: tone[tn].fg }} />
        {mark !== undefined && mark > 0 && mark < 1 ? <span mix={markCss} aria-hidden="true" style={{ left: `${mark * 100}%` }} /> : null}
      </div>
    );
  };
}
