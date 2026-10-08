import { type Context, Hono } from "hono";
import {
  createItem,
  deleteItem,
  getItem,
  type Item,
  listItems,
  setAttachmentKey,
  updateItem,
  validateTitle,
} from "../lib/items";
import { requireAuth } from "./middleware";
import { type AppEnv, HttpError } from "./types";

export const MAX_ATTACHMENT_BYTES = 1024 * 1024;
export const ATTACHMENT_TYPES = ["text/plain", "image/png", "image/jpeg", "application/pdf"] as const;

export const items = new Hono<AppEnv>();
items.use("*", requireAuth);

function parseId(raw: string): number {
  const id = Number(raw);
  if (!Number.isSafeInteger(id) || id < 1) throw new HttpError(404, "not_found", "Item not found.");
  return id;
}

async function requireItem(c: Context<AppEnv>, raw: string): Promise<Item> {
  const item = await getItem(c.env.DB, c.get("user").id, parseId(raw));
  if (!item) throw new HttpError(404, "not_found", "Item not found.");
  return item;
}

items.get("/", async (c) => c.json({ items: await listItems(c.env.DB, c.get("user").id) }));

items.post("/", async (c) => {
  const body = await c.req.json<{ title?: unknown }>().catch(() => ({}) as { title?: unknown });
  const title = validateTitle(body.title);
  if (!title) throw new HttpError(400, "invalid_title", "title must be a string of 1 to 200 characters.");
  return c.json({ item: await createItem(c.env.DB, c.get("user").id, title) }, 201);
});

items.get("/:id", async (c) => c.json({ item: await requireItem(c, c.req.param("id")) }));

items.patch("/:id", async (c) => {
  const id = parseId(c.req.param("id"));
  const body = await c.req.json<{ title?: unknown; done?: unknown }>().catch(() => ({}) as { title?: unknown; done?: unknown });
  const patch: { title?: string; done?: boolean } = {};
  if (body.title !== undefined) {
    const title = validateTitle(body.title);
    if (!title) throw new HttpError(400, "invalid_title", "title must be a string of 1 to 200 characters.");
    patch.title = title;
  }
  if (body.done !== undefined) {
    if (typeof body.done !== "boolean") throw new HttpError(400, "invalid_done", "done must be a boolean.");
    patch.done = body.done;
  }
  if (patch.title === undefined && patch.done === undefined) {
    throw new HttpError(400, "empty_patch", "Provide title and/or done.");
  }
  const item = await updateItem(c.env.DB, c.get("user").id, id, patch);
  if (!item) throw new HttpError(404, "not_found", "Item not found.");
  return c.json({ item });
});

items.delete("/:id", async (c) => {
  const removed = await deleteItem(c.env.DB, c.get("user").id, parseId(c.req.param("id")));
  if (!removed) throw new HttpError(404, "not_found", "Item not found.");
  if (removed.attachmentKey) await c.env.ATTACHMENTS.delete(removed.attachmentKey);
  return c.body(null, 204);
});

// --- R2 feature: one attachment per item -----------------------------------

items.put("/:id/attachment", async (c) => {
  const user = c.get("user");
  const item = await requireItem(c, c.req.param("id"));

  const contentType = (c.req.header("Content-Type") ?? "").split(";")[0]?.trim().toLowerCase() ?? "";
  if (!(ATTACHMENT_TYPES as readonly string[]).includes(contentType)) {
    throw new HttpError(415, "unsupported_type", `Content-Type must be one of: ${ATTACHMENT_TYPES.join(", ")}`);
  }
  const declared = Number(c.req.header("Content-Length") ?? "0");
  if (declared > MAX_ATTACHMENT_BYTES) {
    throw new HttpError(413, "too_large", `Attachments are limited to ${MAX_ATTACHMENT_BYTES} bytes.`);
  }
  const bytes = await c.req.arrayBuffer();
  if (bytes.byteLength > MAX_ATTACHMENT_BYTES) {
    throw new HttpError(413, "too_large", `Attachments are limited to ${MAX_ATTACHMENT_BYTES} bytes.`);
  }

  const key = `attachments/${user.id}/${item.id}`;
  await c.env.ATTACHMENTS.put(key, bytes, { httpMetadata: { contentType } });
  await setAttachmentKey(c.env.DB, user.id, item.id, key);
  return c.json({ attachment: { key, size: bytes.byteLength, contentType } }, 201);
});

items.get("/:id/attachment", async (c) => {
  const item = await requireItem(c, c.req.param("id"));
  if (!item.attachmentKey) throw new HttpError(404, "no_attachment", "This item has no attachment.");
  const object = await c.env.ATTACHMENTS.get(item.attachmentKey);
  if (!object) throw new HttpError(404, "no_attachment", "This item has no attachment.");
  return new Response(object.body, {
    headers: {
      "Content-Type": object.httpMetadata?.contentType ?? "application/octet-stream",
      "Content-Length": String(object.size),
      // User-supplied content: never let the browser sniff or render it inline.
      "Content-Disposition": "attachment",
      "X-Content-Type-Options": "nosniff",
    },
  });
});
