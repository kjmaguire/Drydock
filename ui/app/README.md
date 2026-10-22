# Drydock UI (Remix 3)

The UI is a browser-only Remix 3 app hosted by the Wails v3 shell (WebView2) or, in development, by the Go core's HTTP dev
transport. It is a **projection** of the core: every screen is an RPC read plus the events that invalidate it. Nothing in
the UI is authoritative.

- Components: `remix/component` (JSX, but **not React**: setup runs once, the returned function renders).
- Routing: `remix/routes` + `remix/router` + `remix/spa` (path routes, the browser Navigation API). The Go side serves
  `index.html` for app paths (`internal/uiassets`).
- Build: plain Vite 8 with Pitlane's Remix plugin in SPA mode (`@pitlane/vite-plugin-remix`, `remix({ server: false })`):
  `npm run dev` hot-swaps an edited component and keeps its state (an edit to a component's setup scope remounts it).
  Run it against a core with `DRYDOCK_DEV_URL=http://127.0.0.1:PORT npm run dev`.
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
  api/                transport + typed client (framework-free); *.gen.ts are generated from Go types (go run ./cmd/drydock-tsgen; never edit them)
  state/              eventStore, live reads (live.ts), services/query/action/watch (services.tsx),
                      models: ui.ts (panels, dock, palette, theme), workspace.ts (projects, license), project.ts (the selected project's
                      shared reads; its task list comes with the project's task count: taskList, taskTotal, taskCoverage)
  lib/                pure helpers (format, composer, route, theme, graphLayout, permission, task-total, limits, poll, envname)
  ui/                 the design kit: tokens, icon, button, form, feedback, data, layout, dialog, tabs, menu, popover, markdown,
                      virtual-list (windowed feeds; its arithmetic in virtual.ts), windowed-list (long lists read from the top)
  shell/              sidebar (with the project switcher), hub pills and rails, details panel, dock host, license banner, the command
                      menu (palette.tsx, Ctrl+K: a chip for the task of the page, grouped options from palette-items.ts with the
                      prefixes > # @, the "What happens" panel from task.assignPreview; G then I, T or C goes to Inbox, Tasks, Crew)
  screens/<area>/     one folder per screen area, each with its tests
  screens/needs-you/  the workspace's Inbox (/needs-you: Needs you, Running, Done today in every project, the selected item's detail
                      with its answers; helpers in needs-you.ts) and My tasks (/my-tasks)
  screens/overview/   the project's Overview (greeting, needs you, the crew, tasks in motion, rooms, Insights; helpers in overview.ts)
  screens/tasks/      the project board (Board / List / Timeline of task.summaries; tasks.ts: columns, cards, checks bar, filters,
                      timeline), the task page with its step bar (steps.ts), the review panel
  screens/knowledge/  the Knowledge hub: ask box, source cards with counts, requirements coverage, recently learned (knowledge.ts)
  screens/team/       Crew (team-screen.tsx, graphite P97): one project's crew (/team) or every project's (/crew, cards grouped by
                      project): the header's figures, Templates and New agent (N) as dialogs, Today's timeline (crew-timeline.tsx; its
                      window, ticks and blocks in crew.ts) and a card per agent; an agent's own page (/team/:id, agent-screen.tsx: a
                      hero with chips, Inspect, Edit and Remove, then Overview (the lead's stand-up and interventions on the lead's
                      page), Skills & tools, Memory and History); the inspector stays in the details panel
  screens/models/     Models on this PC (/models, a workspace page): the Serving card, Memory (each GPU split into the model and its
                      KV cache, the RAM), Engine, the model table with the Hub search ("/"), the server settings (helpers in models.ts)
  screens/chats/      the Chats section: list, conversation (ChatFeed: chat.get + chat.* events), new chat dialog (Ctrl+N), a chat's
                      properties (chat-properties.tsx: CLI, folder, library skills and MCP servers, its MCP approvals), a waiting
                      request as an inline card (chat-asks.tsx), Move to a project (chat-move.tsx)
  styles/app.css      design tokens (light-dark()) + base layer for native elements
  test/               labelled test doubles (fakes.ts) and render helpers (render.tsx)
```

## Component rules

```tsx
import { css, on, type Handle } from 'remix/component';

export function Thing(handle: Handle<{ id: string }>) {
  const s = services(handle);            // client, store, ui, workspace, project
  watch(handle, s.project);              // re-render when the model changes
  const q = query(handle, {              // live read: on mount, on matching events, when the stream reopens after a break
    load: () => s.client.taskGet(handle.props.id),
    when: (e) => e.taskId === handle.props.id,
    key: () => handle.props.id,          // re-read from scratch when this changes
  });                                    // q.set(v) shows an action's own answer instead of reading again (a read in flight is dropped)
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
  A host whose widget handles its own keys carries `data-own-keys`: global shortcuts (`lib/keys.ts` `ownsKeys`) skip it.
- Raw HTML is never rendered. Markdown goes through `ui/markdown.tsx` (no innerHTML).
- Links: `<a href={routes.x.href(...)}>`. Programmatic: `navigate(href)` from `remix/component`.
  External links (`https://...`) need `target="_blank"` and `data-rmx-document`.
- Forms: `on('submit', (e) => { e.preventDefault(); ... })`. The CSP has `form-action 'none'`: a native submission is blocked.
- Imports use explicit extensions (`./x.ts`, `./y.tsx`), as Node ESM and the Remix repo do.
- Dropdowns are the kit's `Select` (`ui/select.tsx`), never a native `<select>`: WebView2 draws the native popup itself,
  in the system style and where the OS puts it. Two free-text fields keep a native suggestion list (`<input list>`): the
  agent's role and an integration setting's choices; they take typed text first. `Select` is controlled (`value` + `onChange`, the
  options as `{ value, label, hint?, disabled? }`); a model name goes through `screens/shared/model-field.tsx` (the
  adapter's aliases as a list, "Another model..." for a typed full name). In tests, `choose(control, value)` from
  `test/render.tsx` picks an option (`type()` does the same on a dropdown), `optionsOf()` and `valueOf()` read it back; in
  Playwright, `choose(locator, value | { label })` and `optionsOf(locator, value?)` from `e2e/helpers.ts`, and `data-value` on
  the button carries the value.
- Each `css()` rule sits in its own cascade layer and a later layer wins regardless of specificity: a parent's
  `&:hover .child` loses to the child's own `css()`. Put the ancestor selector in the child's rule (`':is(.msg:hover) &'`).
- The design tokens (app.css) are not loaded in browser tests: assert on attributes, not computed colors.
- `css()` treats only keys starting with `&`, `@`, `:`, `[` or `.` as nested selectors. An ancestor selector must be
  written `':is(#dock-panel) &'`; `'#dock-panel &'` is silently dropped.
- remix test 1.0.0's browser runner breaks a relative dynamic `import('./x.ts')` (its import rewriting drops the quotes).
  A module that browser tests load must lazy-load with `import.meta.glob('./x.ts')` instead (Vite still splits it).

## Design

Graphite (decision P97, after the next generation of Linear; it replaced P64's look and top bar): a near-black canvas
(`--bg-app`) with raised greys for surfaces, one periwinkle accent, amber for "waiting on you", teal for "passed"; the light
theme is the same page by day (every colour is a `light-dark()` token in `styles/app.css`, and `styles/contrast.test.ts` holds
every text pair at 4.5:1). Inter for text, JetBrains Mono (`t.mono`) for task keys (`GOC-41`), paths, numbers and commands. Type
sizes are only 12, 12.5, 13, 15, 18 and 22 px (`t.text*`; `e2e/density.spec.ts` checks the page against them); radii 5, 7, 10, 14.
The shell is the sidebar, the page (with the hub's pills, and a rail on Rooms and Settings), and the details panel beside a
conversation:

- **Sidebar** (`shell/sidebar.tsx`, landmark "Main"; the arrow keys, Home and End move through its links): the workspace
  switcher ("Switch project", the kit `Menu`), search (Ctrl+K), **Inbox** (`/needs-you`, the count of what needs you in every
  project), **Running**, **My tasks**, **Chats**, the projects (the open one shows its tabs **Overview, Tasks, Rooms, Crew,
  Knowledge** under it; another is one line), the workspace's **Security**, **Connections**, **Models on this PC** and
  **Library**, then **Settings**, the **Guide**, the local engine's state and the event stream. "[" (never while typing) or its
  button folds it to a 56 px rail of icons whose labels stay the links' names (`title` and accessible name); the choice is kept
  (`drydock.sidebar`, `UiModel.sidebarRail`), and a window under 1024 px starts folded, a phone-sized one with a 44 px rail.
- **Hubs** (`shell/hubnav.tsx`): a tab's sub-pages are pills (landmark "Section"); the Rooms rail lists the rooms (landmark
  "Rooms", `data-rooms`); the Settings rail lists every settings page (landmark "Settings"). The old test ids live on:
  `nav-tasks` is the tab, `nav-pulls` its pill, `nav-license` a rail link; `e2e/helpers.ts` opens the hub first when a link is
  not on the page.
- **Rooms are named without "#"**: a small lucide icon (`roomIcon(kind)` in `ui/icon.tsx`), then the name; a direct
  message shows the agent's avatar. A link's accessible name starts with the room name (an unread count adds ", 3 unread").
- **Avatars** (`Avatar` in `ui/data.tsx`): an agent is a disc in a stable per-id hue with its first letter, drawn by CSS from
  `data-letters` so the name is not read twice; presence is always in words beside it. People are round with initials;
  projects and tools are rounded squares with initials.
- **Pages** (`Page` in `ui/layout.tsx`) have a top bar: the title (18 px), the subtitle, the page's own tabs and filters
  (`bar`) and the actions on the right; the body scrolls, at most 1320 px wide. A conversation screen (`flush`) keeps its bar.
- **Messages** (`screens/room/message-item.tsx`) read like a compact comment thread: an avatar, one author line (name, a
  quiet outlined role label, time), consecutive messages from the same author grouped under it, a faint hover tint.
- **Keys** are shown as `<kbd>` and work as named: single letters only without Ctrl, Alt or Meta and never while typing.

Use the kit before writing CSS:

- `Page` (header bar with title, icon, subtitle, actions; `mark` instead of the icon for what the page belongs to, a project's
  colour square; `bar` for the page's own view tabs and filters after the title; scrolling body; `head` draws the head itself
  around the h1 id it is given; `head` with `flush` draws the whole page, its own bar and scrolling columns: the task page),
  `Section`, `card`, `listCard`, `tableCard`,
  `row`, `stack`, `cardGrid` (layout.tsx).
- `Button` (primary, secondary, ghost, danger, link; sizes md, sm), `IconButton`, `buttonStyle()` for links that look like
  buttons (button.tsx).
- `Field` (label tied by id; children receives the id; `error` shows a `role="alert"` message under the control), `errorAttrs(id, error)`
  (spread on the control: `aria-invalid` and `aria-describedby`), `errorCss` (for an error outside a `Field`), `formGrid`, `formStack`,
  `formActions`, `Checkbox`, `Switch` (form.tsx). Native `input`, `textarea`, `button`, `table`, `kbd`, `code`, `pre` are styled by the base layer.
- **A number or a name a person types is checked on the page before anything is sent.** `lib/limits.ts` reads a limit exactly
  (`parseDollars`, `parseWhole`, `parseInteger`, `parsePercent`: empty is "not set", anything that is not a plain number is an
  error, never "no limit"; audit 2026-10-16, M25) and `lib/envname.ts` checks an environment variable name against the core's rule
  (L38). A form keeps the message in a variable, shows it with `Field error` and `errorAttrs`, clears it when the field is typed in,
  sends nothing, and moves the keyboard to the first control with `aria-invalid="true"` in `handle.queueTask`. A message about a
  line of a box names the line (`boxLines` counts them as the box shows them) and never repeats what is on it.
- `Select` (select.tsx): a themed dropdown, a `role="combobox"` button showing the chosen option with a chevron, over an
  anchored `listbox` of `option`s drawn with the tokens in both themes (check on the chosen one, a quiet `hint`, disabled
  options). Keyboard: arrows, Home, End, Enter, Space, Escape, letters jump to a match (and choose it while closed). The
  `Field` id names it; `label` gives an accessible name without one; `size="sm"` for toolbars, `name` for a form value.
  `selectValues(strings)` and `selectOptions(pairs)` build the options.
- `ErrorNote` (role="alert"), `Notice`, `Loading`, `Spinner`, `Empty` (feedback.tsx).
- **A permission mode that skips approvals** (`bypassPermissions`, `auto`, `dontAsk`; `lib/permission.ts`) is never chosen without the
  person's confirmation, wherever it is chosen. Under the select put `<SkipApprovals mode confirmed onConfirm error?>`
  (`screens/shared/skip-approvals.tsx`): the plain-words warning and the box "I understand this agent will act without asking me
  first". Keep the mode the box was ticked for in the form (`let confirmedMode = ''`: another mode asks again), keep the submit
  button off with `needsConfirmation(mode, confirmedMode)`, spread `confirmationFor(mode, confirmedMode)` into the request (it adds
  `confirmSkipsApprovals: true` only for such a mode that was ticked), and show the core's "needs your confirmation" refusal beside
  the box (`splitConfirmation(act.error)`: `confirm` goes to `SkipApprovals`, `other` to the form's `ErrorNote`). `<SkipsBadge mode>` is
  the persistent red "skips approvals" on an agent, template or crew card, row or header that has such a mode.
- **A list the core cuts says so.** `task.list` returns only the newest `TASK_LIST_LIMIT` tasks; `s.project.taskCoverage` says
  whether `s.project.taskList` is all of them, and a screen that presents the list as the whole (a board, a picker, counts) puts
  `<TaskTotalNotice>what the cut means here</TaskTotalNotice>` (`screens/shared/task-total.tsx`) where the list is. The same goes for
  an agent's usage rows (`UsageTable`'s `total`, the sums come from `usageTotals`, never from adding up the rows listed).
- `ErrorBoundary` (error-boundary.tsx): guards the components in its children (a component that throws in setup or render is replaced by the error's text with Try again and Reload; one nested inside a guarded component is not caught, the runtime reports it). `ClippedText` (clipped-text.tsx): text a server wrote, clipped to about 300 characters with Show more.
- `Badge`, `StatusChip`, `PresenceDot`, `Avatar`, `SourceBadge`, `kv`, `muted`, `faint`, `mono` (data.tsx).
- `Dialog` (native `<dialog>`), `Tabs` (underlined; `pills` for a page's sections), `Menu`, `Popover` (an anchored panel: Escape and an outside click close it), `Markdown`.
- `VirtualList` (virtual-list.tsx) for a long feed: create it in setup with `onChange: () => handle.update()`, render
  `list.rows(items, key, render)` inside the scroller (before reading `list.quiet` for the log's `aria-live`), put
  `ref(list.scroller)` and `on('scroll', () => list.onScroll())` on the scroller and `handle.queueTask(() => list.settle())`
  in render. Only the rows near the view are in the DOM; `list.stick` says the reader is at the bottom, `list.toBottom()`
  jumps there. Row keys must name one place in the list (it anchors the view on them).
- `WindowedList` (windowed-list.tsx) for a long list read from the top (findings, the lines of a patch, an agent's skills and
  notes; audit 2026-10-16, L37): give it `count`, `rowKey(i)`, `renderRow(i)` (for an array, spread `listOf(items, key, render)`), a
  `label` and a `role` (`list`, `table`, `region`; the rows carry the matching roles). Up to 100 rows are drawn whole, in the page; more get a named scroller (`tabindex="0"`) of a
  `maxHeight` that draws the rows near the view. Select all, Copy and find-in-page reach only the rows drawn.
- `pollWhile` (lib/poll.ts) for a read that repeats: it waits for each read to settle, then waits at least 500 ms (twice the time
  the read took when longer, at most 5 s), so reads never pile up behind a busy core. `Query.reload()` and `Live.reload()` return
  a promise that settles with the read.
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

A controlled field (`value={x}`) whose `input` handler only assigns a variable is put back to its old value by the runtime: the handler
must re-render (`void handle.update()`, or `set()`/`redraw()` helpers that do). `type()` sets a whole text at once and can not see
that; `typeKeys(el, text, r)` types one key at a time, waits for the render and throws as soon as the field loses a key.
