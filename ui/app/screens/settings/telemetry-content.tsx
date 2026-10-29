// Content export (decision P121): a switch of its own, off by default. It adds the prompts and completions of Drydock's own model
// calls to the telemetry, with secrets masked, as the OpenTelemetry GenAI conventions name them. Turning it on first shows what it
// sends, and only "Turn on content export" in that dialog turns it on (the core refuses it without that acknowledgement).

import { on, type Handle } from 'remix/component';
import { TriangleAlert } from 'lucide';
import type { Consent, TelemetryStatus } from '../../api/extTypes.ts';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { formActions, Switch } from '../../ui/form.tsx';
import { CONTENT_WARNING } from './diagnostics.ts';
import { panel, Row } from './settings-kit.tsx';

export function ContentExport(handle: Handle<{ content: Consent; onChanged: (st: TelemetryStatus) => void }>) {
  const s = services(handle);
  const act = action(handle);
  let asking = false;

  const set = async (enabled: boolean, acknowledged: boolean) => {
    const r = await act.run('content', () => s.client.telemetryContentSet(enabled, acknowledged));
    if (r) handle.props.onChanged(r);
    asking = false;
    void handle.update();
  };

  return () => {
    const { content } = handle.props;
    const busy = act.busy !== '';
    return (
      <div mix={panel} data-testid="content-export" data-enabled={String(content.enabled)}>
        <Row
          label="Send prompts and completions (content export)"
          description={
            content.enabled ? (
              <span mix={muted}>on, turned on by {content.changedBy ?? 'you'}: Drydock's own model calls carry what they were given and answered, secrets masked</span>
            ) : (
              'Off unless you turn it on. Adds what Drydock’s own model calls were given and answered, with secrets masked.'
            )
          }
        >
          <Switch
            label="Content export"
            checked={content.enabled}
            disabled={busy}
            onChange={(v) => (v ? ((asking = true), void handle.update()) : void set(false, false))}
          />
        </Row>
        <ErrorNote error={act.error} what="Content export" />
        <Dialog open={asking} title="Turn on content export?" label="Turn on content export" onClose={() => ((asking = false), void handle.update())}>
          <Notice tone="warn" role="note">
            {CONTENT_WARNING}
          </Notice>
          <p mix={muted}>You can turn it off here at any time; from then on no content is recorded or sent, including what is still waiting to be sent.</p>
          <div mix={formActions}>
            <Button variant="primary" icon={TriangleAlert} disabled={busy} mix={on('click', () => void set(true, true))}>
              Turn on content export
            </Button>
            <Button variant="ghost" disabled={busy} mix={on('click', () => ((asking = false), void handle.update()))}>
              Cancel
            </Button>
          </div>
        </Dialog>
      </div>
    );
  };
}
