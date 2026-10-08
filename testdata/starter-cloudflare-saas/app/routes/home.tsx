import { data, Form, redirect } from "react-router";
import { authenticate } from "../../src/lib/auth";
import { createItem, deleteItem, listItems, updateItem, validateTitle } from "../../src/lib/items";
import type { Route } from "./+types/home";

export function meta() {
  return [{ title: "Items | Starter SaaS" }];
}

export async function loader({ request, context }: Route.LoaderArgs) {
  const { env } = context.cloudflare;
  const user = await authenticate(request, env);
  if (!user) throw redirect("/login");
  return { user, items: await listItems(env.DB, user.id) };
}

export async function action({ request, context }: Route.ActionArgs) {
  const { env } = context.cloudflare;
  const user = await authenticate(request, env);
  if (!user) throw redirect("/login");

  const form = await request.formData();
  const intent = form.get("intent");
  const id = Number(form.get("id"));

  if (intent === "create") {
    const title = validateTitle(form.get("title"));
    if (!title) return data({ error: "Title must be 1 to 200 characters." }, { status: 400 });
    await createItem(env.DB, user.id, title);
    return { error: null };
  }
  if (intent === "toggle" && Number.isSafeInteger(id)) {
    await updateItem(env.DB, user.id, id, { done: form.get("done") === "true" });
    return { error: null };
  }
  if (intent === "delete" && Number.isSafeInteger(id)) {
    const removed = await deleteItem(env.DB, user.id, id);
    if (removed?.attachmentKey) await env.ATTACHMENTS.delete(removed.attachmentKey);
    return { error: null };
  }
  return data({ error: "Unknown action." }, { status: 400 });
}

export default function Home({ loaderData, actionData }: Route.ComponentProps) {
  const { user, items } = loaderData;
  return (
    <>
      <header>
        <h1>Items</h1>
        <span>
          <span className="muted" data-testid="user-email">
            {user.email}
          </span>{" "}
          <Form method="post" action="/logout" className="inline">
            <button type="submit">Sign out</button>
          </Form>
        </span>
      </header>
      <main>
        <Form method="post" className="row" key={items.length}>
          <input type="hidden" name="intent" value="create" />
          <input type="text" name="title" placeholder="New item" aria-label="New item title" maxLength={200} required />
          <button type="submit" className="primary">
            Add
          </button>
        </Form>
        {actionData?.error ? (
          <p className="error" role="alert">
            {actionData.error}
          </p>
        ) : null}

        {items.length === 0 ? (
          <p className="muted">No items yet. Add your first one above.</p>
        ) : (
          <ul className="items" aria-label="Items">
            {items.map((item) => (
              <li key={item.id} className={item.done ? "done" : undefined} data-testid="item">
                <Form method="post" className="inline">
                  <input type="hidden" name="intent" value="toggle" />
                  <input type="hidden" name="id" value={item.id} />
                  <input type="hidden" name="done" value={String(!item.done)} />
                  <button type="submit" aria-label={item.done ? `Mark "${item.title}" not done` : `Mark "${item.title}" done`}>
                    {item.done ? "Undo" : "Done"}
                  </button>
                </Form>
                <span className="title">{item.title}</span>
                <Form method="post" className="inline">
                  <input type="hidden" name="intent" value="delete" />
                  <input type="hidden" name="id" value={item.id} />
                  <button type="submit" aria-label={`Delete "${item.title}"`}>
                    Delete
                  </button>
                </Form>
              </li>
            ))}
          </ul>
        )}

        <p className="stub">
          Billing is a stub: <code>POST /api/billing/checkout</code> returns a fake checkout payload. No payments are processed.
        </p>
      </main>
    </>
  );
}
