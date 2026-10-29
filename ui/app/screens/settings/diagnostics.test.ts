import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import {
  backfillProblem,
  backfillWords,
  COMPRESSION_OPTIONS,
  compressionWords,
  CONTENT_WARNING,
  crashWords,
  dateDaysAgo,
  MAX_BACKFILL_ROWS,
  sinceFromDate,
} from './diagnostics.ts';

describe('diagnostics words (decision P121)', () => {
  it('says whether the bodies are compressed and why', () => {
    expect(COMPRESSION_OPTIONS.map((o) => o.value)).toEqual(['', 'gzip', 'none']);
    expect(compressionWords({ compression: '', gzip: true })).toBe('Bodies are compressed with gzip (automatic).');
    expect(compressionWords({ compression: 'none', gzip: false })).toBe('Bodies are not compressed (you chose none).');
    expect(compressionWords({ gzip: false })).toBe('Bodies are not compressed (automatic).');
  });

  it('reads a date field as the start of that local day and writes one back', () => {
    const now = new Date(2026, 9, 29, 15, 30).getTime();
    expect(dateDaysAgo(now, 7)).toBe('2026-10-22');
    expect(sinceFromDate('2026-10-22')).toBe(new Date(2026, 9, 22).getTime());
    expect(sinceFromDate('2026-02-30')).toBe(undefined);
    expect(sinceFromDate('yesterday')).toBe(undefined);
  });

  it('bounds what was recorded before as the core does: a date within the 30 days kept, and at most 1000 rows', () => {
    const now = new Date(2026, 9, 29, 12).getTime();
    expect(backfillProblem(sinceFromDate('2026-10-22'), 500, now)).toBe(undefined);
    expect(backfillProblem(undefined, 500, now)).toBe('Give the date to send from.');
    expect(backfillProblem(now + 86_400_000, 500, now)).toBe('The date to send from is in the future.');
    expect(backfillProblem(sinceFromDate('2026-09-01'), 500, now)).toBe('Telemetry keeps 30 days: choose a date within them.');
    expect(backfillProblem(sinceFromDate('2026-10-22'), MAX_BACKFILL_ROWS + 1, now)).toBe('Send between 1 and 1000 rows.');
    expect(backfillProblem(sinceFromDate('2026-10-22'), 2.5, now)).toBe('Send between 1 and 1000 rows.');
  });

  it('words the last backfill, with what the limit left out', () => {
    const date = () => '22 Oct';
    expect(backfillWords({ at: 1, sinceMs: 2, maxRows: 6, queued: 6, left: 14 }, date)).toBe(
      'Also sent what was recorded before: 6 rows since 22 Oct queued; 14 more rows in that time were left out by the limit of 6.',
    );
    expect(backfillWords({ at: 1, sinceMs: 2, maxRows: 500, queued: 1, left: 0 }, date)).toBe('Also sent what was recorded before: 1 row since 22 Oct queued.');
  });

  it('says where a crash report goes, and that off keeps none', () => {
    expect(crashWords('local', 0)).toBe(
      'When Drydock stops unexpectedly, a report stays on this computer. It leaves only in a support bundle you send. None is kept now.',
    );
    expect(crashWords('off', 2)).toBe('Off: a crash leaves no report, and support bundles carry none. 2 reports kept now (Support page).');
  });

  it('warns that content can hold code, that secrets are masked and that the agents are never included', () => {
    for (const want of ['can hold code', 'Secrets are masked', 'whoever runs that collector can read it', "agents' own sessions are never included"]) {
      expect(CONTENT_WARNING).toContain(want);
    }
  });
});
