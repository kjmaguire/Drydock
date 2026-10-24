// Add a provider. The API key lives only in its (uncontrolled) input: it is read once on submit and the field is cleared
// before the request leaves, so it is never kept in component state or rendered. A refused request means typing it again.
// The core's validation text is shown verbatim.

import { css, on, ref, type Handle } from 'remix/component';
import { Plus } from 'lucide';
import type { Provider } from '../../api/types.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, services, watch } from '../../state/services.tsx';
import { Disclosure } from '../../ui/disclosure.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Checkbox, Field, formActions, formGrid } from '../../ui/form.tsx';
import { Select, selectOptions } from '../../ui/select.tsx';
import { card } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { ATTESTATIONS, emptyProviderForm, FLAVORS, newProviderRequest, type ProviderFormValues } from './providers.ts';

const wide = css({ gridColumn: '1 / -1' });
// two fields to a row in the Models page's detail (name and kind, address and key, data class and tier), one on a narrow window:
// the four of formGrid left the kind and the data class too narrow for their choices
const gridCss = css({ gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', '@media (max-width: 719px)': { gridTemplateColumns: 'minmax(0, 1fr)' } });
const fieldsetCss = css({
  display: 'grid',
  gap: t.s2,
  gridColumn: '1 / -1',
  margin: '0',
  padding: `${t.s3} ${t.s4}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  '& legend': { padding: `0 ${t.s1}`, fontSize: t.textSm, fontWeight: '500', color: t.text },
});
type Option = [value: string, label: string];

/** `onCreated` gets the provider the core created (when it answered with one), so the screen can open it. */
export function ProviderForm(handle: Handle<{ onCreated: (p?: Provider) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const v: ProviderFormValues = emptyProviderForm();
  let keyInput: HTMLInputElement | undefined;
  const redraw = () => void handle.update();

  const submit = async () => {
    const apiKey = keyInput?.value ?? '';
    if (keyInput) keyInput.value = '';
    const body = newProviderRequest(v, apiKey);
    let ok = false;
    let made: Provider | undefined;
    await act.run('create', async () => {
      made = (await s.client.providerCreate(body)) ?? undefined;
      ok = true;
    });
    if (ok) {
      v.name = '';
      v.baseUrl = '';
      v.att = {};
      redraw();
      handle.props.onCreated(made);
    }
  };

  const text = (label: string, key: 'name' | 'baseUrl' | 'maxConcurrent' | 'hourlyCost' | 'fingerprint', opts: { required?: boolean; placeholder?: string; mode?: string; mono?: boolean } = {}) => (
    <Field label={label}>
      {(id) => (
        <input
          id={id}
          value={v[key]}
          required={opts.required}
          placeholder={opts.placeholder}
          inputMode={opts.mode}
          spellcheck={opts.mono ? false : undefined}
          mix={on('input', (e) => {
            v[key] = e.currentTarget.value;
            redraw();
          })}
        />
      )}
    </Field>
  );

  const select = <K extends 'kind' | 'dataClass' | 'tier' | 'flavor'>(label: string, key: K, options: Option[]) => (
    <Field label={label}>
      {(id) => (
        <Select
          id={id}
          value={v[key]}
          options={selectOptions(options)}
          onChange={(val) => {
            v[key] = val as ProviderFormValues[K];
            redraw();
          }}
        />
      )}
    </Field>
  );

  return () => {
    const ro = s.workspace.license;
    return (
      <form
        autocomplete="off"
        aria-label="Add a provider"
        mix={[
          card,
          formGrid,
          gridCss,
          on('submit', (e) => {
            e.preventDefault();
            void submit();
          }),
        ]}
      >
        <div mix={wide}>
          <ErrorNote error={act.error} what="Provider not created" />
          <ReadOnlyNote />
        </div>
        {text('Provider name', 'name', { required: true })}
        {select('Kind', 'kind', [
          ['aggregator', 'aggregator (for example OpenRouter)'],
          ['direct', 'direct (a model vendor’s own API)'],
          ['host', 'host (a server you run: Ollama, vLLM, ...)'],
        ])}
        {text('Base URL', 'baseUrl', { required: true, placeholder: 'https://api.example.com/v1', mono: true })}
        <Field label="API key (optional for a local host)" hint="Stored in the Windows Credential Manager; never shown again.">
          {(id) => <input id={id} type="password" autocomplete="new-password" spellcheck={false} name="provider-api-key" mix={ref((n: HTMLInputElement) => (keyInput = n))} />}
        </Field>
        {select('Data class', 'dataClass', [
          ['private', 'private (may receive repository content)'],
          ['trainable', 'trainable (public documentation questions only)'],
        ])}
        {select('Tier', 'tier', [
          ['premium', 'premium'],
          ['cheap', 'cheap'],
          ['free', 'free'],
        ])}
        <fieldset mix={fieldsetCss}>
          <legend>Attestations (only what is true)</legend>
          {ATTESTATIONS.map(([k, label]) => (
            <Checkbox
              key={k}
              label={label}
              checked={!!v.att[k]}
              onChange={(on_) => {
                v.att = { ...v.att, [k]: on_ };
                redraw();
              }}
            />
          ))}
        </fieldset>
        <Disclosure summary="Advanced" wide>
          <div mix={formGrid}>
            {select('Flavor', 'flavor', [['', 'detect from the URL'], ...FLAVORS.map((f): Option => [f, f])])}
            {text('Max concurrent requests (0 = default)', 'maxConcurrent', { mode: 'numeric' })}
            {text('Hourly cost in USD (hosts)', 'hourlyCost', { mode: 'decimal' })}
            {text('Pinned TLS fingerprint (SHA-256, self-signed hosts)', 'fingerprint', { mono: true })}
          </div>
        </Disclosure>
        <div mix={formActions}>
          <Button
            type="submit"
            variant="primary"
            icon={Plus}
            disabled={act.busy !== '' || ro.readOnly || !v.name.trim() || !v.baseUrl.trim()}
            title={ro.readOnly ? ro.reason : undefined}
          >
            Add provider
          </Button>
        </div>
      </form>
    );
  };
}
