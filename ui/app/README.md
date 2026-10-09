# Drydock UI (Remix 3)

The UI is a browser-only Remix 3 app hosted by the Wails v3 shell (WebView2) or, in development, by the Go core's HTTP dev
transport. It is a **projection** of the core: every screen is an RPC read plus the events that invalidate it. Nothing in
the UI is authoritative.

- Components: `remix/component` (JSX, but **not React**: setup runs once, the returned function renders).
- Routing: `remix/routes` + `remix/router` + `remix/spa` (path routes, the browser Navigation API). The Go side serves
  `index.html` for app paths (`internal/uiassets`).
- Headless widgets: `@remix-run/ui` (tabs, menu, toggle, ...), wrapped in `app/ui/`.
- Icons: `lucide` icon nodes through `app/ui/icon.tsx`. Font: Inter Variable (bundled).
- Tests: `remix test` (`npm test`): `*.test.ts` run in Node, `*.test.browser.tsx` in a real Chromium page.
  The end-to-end suite (`npm run e2e`, Playwright) drives the built app against the real Go core.

Read the installed Remix docs before using an unfamiliar API: `node_modules/remix/INDEX.md`, then the guide or package
README it points to. They are canonical for the installed version.

## Layout

```
app/
  main.tsx            boot: transport, event store, services, run(router)
  routes.ts           the URL contract (routes.task.href({ id }))
  router.tsx          createAppRouter(services): render transform = <Services><Shell url>{content}</Shell></Services>
  actions/controller.tsx  route actions: lazy-import the screen module, render it with the params
  api/                transport + typed client (framework-free)
  state/              eventStore, live reads (live.ts), services/query/action/watch (services.tsx),
                      models: ui.ts (panels, dock, palette, theme), workspace.ts (projects, license), project.ts
  lib/                pure helpers (format, composer, route, theme, graphLayout)
  ui/                 the design kit: tokens, icon, button, form, feedback, data, layout, dialog, tabs, menu, markdown
  shell/              rail, sidebar, details panel, dock host, command palette, license banner
  screens/<area>/     one folder per screen area, each with its tests
  styles/app.css      design tokens (light-dark()) + base layer for native elements
  test/               labelled test doubles (fakes.ts) and render helpers (render.tsx)
```

## Component rules

```tsx
import { css, on, type Handle } from 'remix/component';

export function Thing(handle: Handle<{ id: string }>) {
  const s = services(handle);            // client, store, ui, workspace, project
  watch(handle, s.project);              // re-render when the model changes
  const q = query(handle, {              // live read: on mount, on matching events, on reconnect
    load: () => s.client.taskGet(handle.props.id),
    when: (e) => e.taskId === handle.props.id,
    key: () => handle.props.id,          // re-read from scratch when this changes
  });
  const act = action(handle);            // busy flag + error for async actions
  let open = false;                      // local UI state: plain variables + handle.update()

  return () => (                         // render: read handle.props HERE, not in setup
    <Page title="Thing">
      <ErrorNote error={q.error ?? act.error} />
      <Button variant="primary" disabled={!!act.busy} mix={on('click', () => act.run('save', () => s.client.x()))}>
        {act.busy === 'save' ? 'Saving...' : 'Save'}
      </Button>
    </Page>
  );
}
```

- Events go through `mix={on('click', fn)}`. **`onClick`-style props are silently dropped.**
- `ref((node, signal) => ...)` for DOM access; pass `{ signal: handle.signal }` to global listeners.
- Never call `handle.update()` during render. Post-render work goes in `handle.queueTask(fn)`.
- Numbers in `style`/`css()` get `px` appended except unitless properties: write `flex: '1'`, `fontWeight: '600'`.
- Static styles: module-level `const x = css({...})`. Per-render values: `style={{ width: `${w}px` }}`.
- Conditional mixins go **last** in a `mix` array (slots are matched by index).
- Imperative widgets (Monaco, xterm): render an **empty** host element, create the widget in `ref`, dispose on the signal.
- Raw HTML is never rendered. Markdown goes through `ui/markdown.tsx` (no innerHTML).
- Links: `<a href={routes.x.href(...)}>`. Programmatic: `navigate(href)` from `remix/component`.
  External links (`https://...`) need `target="_blank"` and `data-rmx-document`.
- Forms: `on('submit', (e) => { e.preventDefault(); ... })`. The CSP has `form-action 'none'`: a native submission is blocked.
- Imports use explicit extensions (`./x.ts`, `./y.tsx`), as Node ESM and the Remix repo do.

## Design

Dense and quiet (Linear), chat-first (Discord, Slack). Use the kit before writing CSS:

- `Page` (header bar with title, icon, subtitle, actions; scrolling body), `Section`, `card`, `listCard`, `tableCard`,
  `row`, `stack`, `cardGrid` (layout.tsx).
- `Button` (primary, secondary, ghost, danger, link; sizes md, sm), `IconButton`, `buttonStyle()` for links that look like
  buttons (button.tsx).
- `Field` (label tied by id; children receives the id), `formGrid`, `formStack`, `formActions`, `Checkbox`, `Switch`
  (form.tsx). Native `input`, `select`, `textarea`, `button`, `table`, `kbd`, `code`, `pre` are styled by the base layer.
- `ErrorNote` (role="alert"), `Notice`, `Loading`, `Spinner`, `Empty` (feedback.tsx).
- `Badge`, `StatusChip`, `PresenceDot`, `Avatar`, `SourceBadge`, `kv`, `muted`, `faint`, `mono` (data.tsx).
- `Dialog` (native `<dialog>`), `Tabs`, `Menu`, `Markdown`.
- Colors only through tokens (`t.text`, `t.bgSurface`, `tone.ok`, ...), never hex values in screens. Both themes come
  from the same tokens.

## The accessibility contract

Labels, accessible names, roles and `data-testid` values are the contract the end-to-end suite (and assistive technology)
relies on. A rebuilt screen keeps every label text, button name, heading and test id the old one had unless the e2e test
is changed in the same commit.

## Tests

```tsx
import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';

afterEach(cleanup);

it('saves', async () => {
  const calls: unknown[] = [];
  const s = testServices({ taskGet: async () => detail, taskUpdate: async (p) => void calls.push(p) });
  const r = mount(<Thing id="t1" />, s);
  await waitFor(() => byText(r.container, 'button', 'Save'), r);
  type(byLabel(r.container, 'Title'), 'New');
  await r.act(() => byText(r.container, 'button', 'Save').click());
  await waitFor(() => expect(calls.length).toBe(1), r);
});
```

`testServices(partialClient)` builds services around a partial client (a labelled test double); `s.emit({ type })`
delivers an event. Call `s.workspace.start(); s.project.start()` when the component reads project data.
