// The Crew page's "Today" timeline (graphite, decision P97): a time axis from 08:00 (or the first run's hour) to now, one lane
// per agent with a block per run (working now, finished, failed, cancelled) and per wait on the person, placed by time
// (crew.ts does the geometry), then the legend. Each block says what it is in its title and to assistive tech.

import { css, type Handle } from 'remix/component';
import type { AgentLane } from '../../api/inboxClient.ts';
import { t } from '../../ui/tokens.ts';
import { laneBlocks, timelineTicks, type BlockKind, type TimeWindow } from './crew.ts';

/** The fill of each kind of block: tokens only, mixed with the surface where the mockup has a darker shade of one. */
export const BLOCK_FILL: Record<BlockKind, string> = {
  working: t.accent,
  finished: `color-mix(in srgb, ${t.accent} 32%, ${t.bgSurface})`,
  failed: `color-mix(in srgb, ${t.dangerSolid} 40%, ${t.bgSurface})`,
  cancelled: t.borderStrong,
  waiting: t.warning,
  waited: `color-mix(in srgb, ${t.warning} 35%, ${t.bgSurface})`,
};

const LEGEND: [BlockKind, string][] = [
  ['working', 'working now'],
  ['finished', 'finished run'],
  ['failed', 'failed run'],
  ['waiting', 'waiting on you'],
];

const sectionCss = css({
  display: 'flex',
  flexDirection: 'column',
  gap: '6px',
  padding: `18px ${t.s6}`,
  borderBottom: `1px solid ${t.border}`,
  '& .lane': { display: 'grid', gridTemplateColumns: '120px minmax(0, 1fr)', alignItems: 'center', gap: t.s3, minHeight: '28px' },
  '& .head': { minHeight: '20px' },
  '& .today': { color: t.textFaint, fontSize: t.textXs, fontWeight: '500' },
  '& .axis': { position: 'relative', height: '16px', color: t.textFaint, font: `500 ${t.textXs} / 16px ${t.mono}` },
  '& .axis > span': { position: 'absolute', top: '0', transform: 'translateX(-50%)', whiteSpace: 'nowrap' },
  '& .axis > span:first-child': { transform: 'none' },
  '& .axis > span:last-child': { transform: 'translateX(-100%)' },
  '& .name': { display: 'flex', alignItems: 'center', gap: '6px', minWidth: '0', fontSize: t.textMd, whiteSpace: 'nowrap' },
  '& .name > .n': { overflow: 'hidden', textOverflow: 'ellipsis' },
  '& .name > .gone': { color: t.textFaint, fontSize: t.textXs },
  '& .sq': { flex: 'none', width: '8px', height: '8px', borderRadius: '2px' },
  '& .track': { position: 'relative', height: '16px', margin: '0', padding: '0', listStyle: 'none', borderRadius: '4px', background: t.bgHover },
  '& .blk': { position: 'absolute', top: '0', height: '16px', borderRadius: '4px' },
  '& .blk[data-kind="waiting"], & .blk[data-kind="waited"]': { zIndex: '1' },
  '& .legend': { display: 'flex', flexWrap: 'wrap', gap: `${t.s1} ${t.s4}`, margin: '0', padding: '0 0 0 132px', listStyle: 'none', color: t.textFaint, fontSize: t.textXs },
  '& .legend li': { display: 'flex', alignItems: 'center', gap: '6px' },
  '& .legend i': { width: '10px', height: '10px', borderRadius: '3px' },
  '@media (max-width: 719px)': { '& .lane': { gridTemplateColumns: '84px minmax(0, 1fr)' }, '& .legend': { paddingLeft: '0' } },
});

export interface TimelineLane {
  lane: AgentLane;
  /** The colour of the lane's project, on the workspace's page (each lane says which project the agent is on). */
  project?: { name: string; color: string };
}

export function TodayTimeline(handle: Handle<{ lanes: TimelineLane[]; window: TimeWindow; now: number; keyOf: (lane: AgentLane, seq: number) => string }>) {
  return () => {
    const { lanes, window: w, now, keyOf } = handle.props;
    const ticks = timelineTicks(w);
    return (
      <section mix={sectionCss} aria-label="Today" data-testid="crew-timeline" data-start={w.start} data-end={w.end}>
        <div class="lane head">
          <span class="today">Today</span>
          <div class="axis" aria-hidden="true" data-testid="crew-axis">
            {ticks.map((k) => (
              <span key={k.label} style={{ left: `${k.left}%` }}>
                {k.label}
              </span>
            ))}
          </div>
        </div>
        {lanes.map(({ lane, project }) => {
          const blocks = laneBlocks(lane, w, now, (seq) => keyOf(lane, seq));
          return (
            <div key={lane.agentId} class="lane" data-testid="crew-lane" data-agent={lane.agentId}>
              <span class="name" title={project ? `${lane.name} (${project.name})` : lane.name}>
                {project ? <i class="sq" aria-hidden="true" style={{ background: project.color }} /> : null}
                <span class="n">{lane.name}</span>
                {lane.archived ? <span class="gone">removed</span> : null}
              </span>
              <ul class="track" aria-label={`${lane.name} today`}>
                {blocks.length === 0 ? <li class="sr-only">Nothing yet today.</li> : null}
                {blocks.map((b) => (
                  <li
                    key={b.key}
                    class="blk"
                    data-testid="crew-block"
                    data-kind={b.kind}
                    data-left={b.left.toFixed(2)}
                    data-width={b.width.toFixed(2)}
                    title={b.label}
                    style={{ left: `${b.left}%`, width: `${b.width}%`, background: BLOCK_FILL[b.kind] }}
                  >
                    <span class="sr-only">{b.label}</span>
                  </li>
                ))}
              </ul>
            </div>
          );
        })}
        <ul class="legend" aria-label="Legend" data-testid="crew-legend">
          {LEGEND.map(([k, words]) => (
            <li key={k}>
              <i aria-hidden="true" style={{ background: BLOCK_FILL[k] }} />
              {words}
            </li>
          ))}
        </ul>
      </section>
    );
  };
}
