import { describe, expect, it } from "vitest";
import { api, json, login, postJson } from "./helpers";

interface Item {
  id: number;
  title: string;
  done: boolean;
  attachmentKey: string | null;
}

describe("items API (D1)", () => {
  it("requires authentication on every route", async () => {
    for (const [method, path] of [
      ["GET", "/api/items"],
      ["POST", "/api/items"],
      ["GET", "/api/items/1"],
      ["PATCH", "/api/items/1"],
      ["DELETE", "/api/items/1"],
    ] as const) {
      const response = await api(path, { method });
      expect(response.status, `${method} ${path}`).toBe(401);
      expect((await json<{ error: { code: string } }>(response)).error.code).toBe("unauthorized");
    }
  });

  it("creates, lists, reads, updates and deletes an item", async () => {
    const cookie = await login("crud@example.com");

    const created = await postJson("/api/items", { title: "  Write the spec " }, cookie);
    expect(created.status).toBe(201);
    const { item } = await json<{ item: Item }>(created);
    expect(item).toMatchObject({ title: "Write the spec", done: false, attachmentKey: null });

    const list = await json<{ items: Item[] }>(await api("/api/items", {}, cookie));
    expect(list.items.map((i) => i.id)).toEqual([item.id]);

    const read = await json<{ item: Item }>(await api(`/api/items/${item.id}`, {}, cookie));
    expect(read.item.title).toBe("Write the spec");

    const patched = await postJson(`/api/items/${item.id}`, { done: true, title: "Ship the spec" }, cookie, "PATCH");
    expect(patched.status).toBe(200);
    expect((await json<{ item: Item }>(patched)).item).toMatchObject({ done: true, title: "Ship the spec" });

    expect((await api(`/api/items/${item.id}`, { method: "DELETE" }, cookie)).status).toBe(204);
    expect((await api(`/api/items/${item.id}`, {}, cookie)).status).toBe(404);
    expect((await json<{ items: Item[] }>(await api("/api/items", {}, cookie))).items).toEqual([]);
  });

  it("lists newest items first", async () => {
    const cookie = await login("order@example.com");
    for (const title of ["first", "second", "third"]) await postJson("/api/items", { title }, cookie);
    const { items } = await json<{ items: Item[] }>(await api("/api/items", {}, cookie));
    expect(items.map((i) => i.title)).toEqual(["third", "second", "first"]);
  });

  it("validates input", async () => {
    const cookie = await login("validate@example.com");
    for (const body of [{}, { title: "" }, { title: "   " }, { title: 7 }, { title: "x".repeat(201) }]) {
      const response = await postJson("/api/items", body, cookie);
      expect(response.status, JSON.stringify(body).slice(0, 40)).toBe(400);
      expect((await json<{ error: { code: string } }>(response)).error.code).toBe("invalid_title");
    }
    const created = await json<{ item: Item }>(await postJson("/api/items", { title: "ok" }, cookie));
    expect((await postJson(`/api/items/${created.item.id}`, {}, cookie, "PATCH")).status).toBe(400);
    expect((await postJson(`/api/items/${created.item.id}`, { done: "yes" }, cookie, "PATCH")).status).toBe(400);
    expect((await api("/api/items/abc", {}, cookie)).status).toBe(404);
    expect((await api("/api/items/0", {}, cookie)).status).toBe(404);
  });

  it("isolates users: one user cannot see, change or delete another user's item", async () => {
    const alice = await login("alice-iso@example.com");
    const bob = await login("bob-iso@example.com");
    const { item } = await json<{ item: Item }>(await postJson("/api/items", { title: "alice only" }, alice));

    expect((await json<{ items: Item[] }>(await api("/api/items", {}, bob))).items).toEqual([]);
    expect((await api(`/api/items/${item.id}`, {}, bob)).status).toBe(404);
    expect((await postJson(`/api/items/${item.id}`, { done: true }, bob, "PATCH")).status).toBe(404);
    expect((await api(`/api/items/${item.id}`, { method: "DELETE" }, bob)).status).toBe(404);

    const still = await json<{ item: Item }>(await api(`/api/items/${item.id}`, {}, alice));
    expect(still.item).toMatchObject({ title: "alice only", done: false });
  });

  it("treats SQL metacharacters in titles as data", async () => {
    const cookie = await login("sqli@example.com");
    const title = "'); DROP TABLE items; --";
    const { item } = await json<{ item: Item }>(await postJson("/api/items", { title }, cookie));
    expect(item.title).toBe(title);
    expect((await json<{ items: Item[] }>(await api("/api/items", {}, cookie))).items).toHaveLength(1);
  });
});
