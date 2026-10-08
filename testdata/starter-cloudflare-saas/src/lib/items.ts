// Data access for items (D1). Every query is scoped by user_id so one user can
// never read or modify another user's rows.

export interface Item {
  id: number;
  title: string;
  done: boolean;
  attachmentKey: string | null;
  createdAt: string;
}

interface ItemRow {
  id: number;
  title: string;
  done: number;
  attachment_key: string | null;
  created_at: string;
}

const COLUMNS = "id, title, done, attachment_key, created_at";

function toItem(row: ItemRow): Item {
  return {
    id: row.id,
    title: row.title,
    done: row.done === 1,
    attachmentKey: row.attachment_key,
    createdAt: row.created_at,
  };
}

export const MAX_TITLE_LENGTH = 200;

export function validateTitle(input: unknown): string | null {
  if (typeof input !== "string") return null;
  const title = input.trim();
  return title.length >= 1 && title.length <= MAX_TITLE_LENGTH ? title : null;
}

export async function listItems(db: D1Database, userId: string): Promise<Item[]> {
  const { results } = await db
    .prepare(`SELECT ${COLUMNS} FROM items WHERE user_id = ?1 ORDER BY created_at DESC, id DESC`)
    .bind(userId)
    .all<ItemRow>();
  return results.map(toItem);
}

export async function getItem(db: D1Database, userId: string, id: number): Promise<Item | null> {
  const row = await db.prepare(`SELECT ${COLUMNS} FROM items WHERE id = ?1 AND user_id = ?2`).bind(id, userId).first<ItemRow>();
  return row ? toItem(row) : null;
}

export async function createItem(db: D1Database, userId: string, title: string): Promise<Item> {
  const row = await db
    .prepare(`INSERT INTO items (user_id, title) VALUES (?1, ?2) RETURNING ${COLUMNS}`)
    .bind(userId, title)
    .first<ItemRow>();
  if (!row) throw new Error("insert returned no row");
  return toItem(row);
}

export async function updateItem(
  db: D1Database,
  userId: string,
  id: number,
  patch: { title?: string; done?: boolean },
): Promise<Item | null> {
  const row = await db
    .prepare(
      `UPDATE items SET title = COALESCE(?3, title), done = COALESCE(?4, done)
       WHERE id = ?1 AND user_id = ?2 RETURNING ${COLUMNS}`,
    )
    .bind(id, userId, patch.title ?? null, patch.done === undefined ? null : patch.done ? 1 : 0)
    .first<ItemRow>();
  return row ? toItem(row) : null;
}

export async function setAttachmentKey(db: D1Database, userId: string, id: number, key: string | null): Promise<void> {
  await db.prepare("UPDATE items SET attachment_key = ?3 WHERE id = ?1 AND user_id = ?2").bind(id, userId, key).run();
}

/** Deletes the row and returns the removed item (so callers can clean up R2), or null if absent. */
export async function deleteItem(db: D1Database, userId: string, id: number): Promise<Item | null> {
  const row = await db
    .prepare(`DELETE FROM items WHERE id = ?1 AND user_id = ?2 RETURNING ${COLUMNS}`)
    .bind(id, userId)
    .first<ItemRow>();
  return row ? toItem(row) : null;
}
