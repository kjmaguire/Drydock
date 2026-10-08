import { describe, expect, it } from "vitest";
import { MAX_ATTACHMENT_BYTES } from "../src/api/items";
import { api, env, json, login, postJson } from "./helpers";

async function newItem(cookie: string): Promise<number> {
  const { item } = await json<{ item: { id: number } }>(await postJson("/api/items", { title: "with file" }, cookie));
  return item.id;
}

describe("R2 attachments", () => {
  it("stores an attachment in R2, records the key in D1 and serves it back safely", async () => {
    const cookie = await login("files@example.com");
    const id = await newItem(cookie);

    const put = await api(
      `/api/items/${id}/attachment`,
      { method: "PUT", headers: { "Content-Type": "text/plain" }, body: "hello r2" },
      cookie,
    );
    expect(put.status).toBe(201);
    const { attachment } = await json<{ attachment: { key: string; size: number; contentType: string } }>(put);
    expect(attachment).toMatchObject({ size: 8, contentType: "text/plain" });

    const stored = await env.ATTACHMENTS.get(attachment.key);
    expect(await stored?.text()).toBe("hello r2");

    const item = await json<{ item: { attachmentKey: string } }>(await api(`/api/items/${id}`, {}, cookie));
    expect(item.item.attachmentKey).toBe(attachment.key);

    const get = await api(`/api/items/${id}/attachment`, {}, cookie);
    expect(get.status).toBe(200);
    expect(await get.text()).toBe("hello r2");
    expect(get.headers.get("Content-Type")).toBe("text/plain");
    expect(get.headers.get("Content-Disposition")).toBe("attachment");
    expect(get.headers.get("X-Content-Type-Options")).toBe("nosniff");
  });

  it("rejects unsupported content types and oversized bodies", async () => {
    const cookie = await login("files-bad@example.com");
    const id = await newItem(cookie);
    const html = await api(
      `/api/items/${id}/attachment`,
      { method: "PUT", headers: { "Content-Type": "text/html" }, body: "<script>" },
      cookie,
    );
    expect(html.status).toBe(415);
    const big = await api(
      `/api/items/${id}/attachment`,
      { method: "PUT", headers: { "Content-Type": "application/pdf" }, body: new Uint8Array(MAX_ATTACHMENT_BYTES + 1) },
      cookie,
    );
    expect(big.status).toBe(413);
  });

  it("returns 404 when there is no attachment, and does not expose another user's attachment", async () => {
    const owner = await login("files-owner@example.com");
    const other = await login("files-other@example.com");
    const id = await newItem(owner);
    expect((await api(`/api/items/${id}/attachment`, {}, owner)).status).toBe(404);
    await api(`/api/items/${id}/attachment`, { method: "PUT", headers: { "Content-Type": "text/plain" }, body: "secret" }, owner);
    expect((await api(`/api/items/${id}/attachment`, {}, other)).status).toBe(404);
    expect(
      (await api(`/api/items/${id}/attachment`, { method: "PUT", headers: { "Content-Type": "text/plain" }, body: "x" }, other))
        .status,
    ).toBe(404);
  });

  it("deleting an item removes its object from R2", async () => {
    const cookie = await login("files-delete@example.com");
    const id = await newItem(cookie);
    const put = await api(
      `/api/items/${id}/attachment`,
      { method: "PUT", headers: { "Content-Type": "image/png" }, body: new Uint8Array([137, 80, 78, 71]) },
      cookie,
    );
    const { attachment } = await json<{ attachment: { key: string } }>(put);
    expect(await env.ATTACHMENTS.head(attachment.key)).not.toBeNull();
    expect((await api(`/api/items/${id}`, { method: "DELETE" }, cookie)).status).toBe(204);
    expect(await env.ATTACHMENTS.head(attachment.key)).toBeNull();
  });
});
