// Typed client for the Chats section (docs/chats.md, decision P29): the human's own conversations with an agent CLI,
// kept with every message, continued in the CLI's session, forked, searched and attached to later messages.

import type { Transport } from './transport.ts';

export type ChatStatus = 'idle' | 'running' | 'error';
export type ChatRole = 'user' | 'assistant' | 'tool' | 'denied' | 'error' | 'system';

export interface Chat {
  id: string;
  /** Empty: a chat without a project. */
  projectId: string;
  title: string;
  adapter: string;
  model: string;
  effort: string;
  permissionMode: string;
  /** True for a mode that skips approvals (bypassPermissions, auto, dontAsk): the UI must show it. */
  skipsApprovals: boolean;
  cwd: string;
  /** The CLI's own session id; empty before the first turn. */
  sessionId: string;
  allowedTools: string[];
  readHistory: boolean;
  status: ChatStatus | string;
  lastError: string;
  pinned: boolean;
  archived: boolean;
  archivedAt: number;
  forkedFrom: string;
  createdAt: number;
  updatedAt: number;
  tokensIn: number;
  tokensOut: number;
  /** null when no turn reported a cost (unknown, never zero). */
  costUsd: number | null;
  /** Turns that reported no cost: non-zero means costUsd covers only the other turns. */
  costUnknownTurns: number;
}

export interface ChatListItem extends Chat {
  /** The start of the latest user or assistant message (at most 160 characters). */
  preview: string;
  messageCount: number;
}

export interface ChatMessage {
  id: string;
  chatId: string;
  seq: number;
  role: ChatRole | string;
  body: string;
  toolName?: string;
  /** JSON, at most 4 KB; a longer one is {truncated: true, preview}. */
  toolInput?: unknown;
  turnId?: string;
  createdAt: number;
  /** The rule chat.allowTool would need (Claude chats only; empty when no safe exact rule exists). */
  suggestedRule?: string;
}

export interface ChatDetail {
  chat: Chat;
  /** The latest 200, oldest first. */
  messages: ChatMessage[];
  hasMore: boolean;
}

/** chat.messages: a page of older messages. */
export interface ChatPage {
  messages: ChatMessage[];
  hasMore: boolean;
}

export interface ChatHit {
  chatId: string;
  title: string;
  /** 0 = the title matched. */
  seq: number;
  role?: string;
  /** Matches in [brackets]. */
  snippet: string;
  archived: boolean;
}

export interface ChatCreate {
  projectId?: string;
  cwd?: string;
  title?: string;
  adapter?: string;
  model?: string;
  effort?: string;
  permissionMode?: string;
  readHistory?: boolean;
}

export interface ChatUpdate {
  id: string;
  title?: string;
  pinned?: boolean;
  archived?: boolean;
  model?: string;
  effort?: string;
  permissionMode?: string;
  readHistory?: boolean;
}

/** chat.turn data. */
export interface ChatTurnEnd {
  chatId: string;
  turnId: string;
  status: 'done' | 'error' | 'stopped';
  usage?: { inputTokens: number; outputTokens: number; costUsd: number | null };
  sessionId?: string;
  resumed?: boolean;
  fallback?: boolean;
  error?: string;
}

export interface ChatClient {
  /** limit: at most this many (1-500; omitted = 500). */
  chatList(p?: { projectId?: string; query?: string; includeArchived?: boolean; limit?: number }): Promise<ChatListItem[]>;
  chatCreate(p: ChatCreate): Promise<Chat>;
  chatGet(id: string): Promise<ChatDetail>;
  /** Older messages, oldest first; hasMore says whether still older ones exist. */
  chatMessages(id: string, beforeSeq?: number, limit?: number): Promise<ChatPage>;
  /** Returns the turn id; the turn runs in the background. */
  chatSend(id: string, text: string, attach?: string[]): Promise<{ turnId: string }>;
  chatStop(id: string): Promise<{ stopped: boolean }>;
  chatUpdate(u: ChatUpdate): Promise<Chat>;
  chatDelete(id: string): Promise<void>;
  chatSearch(query: string, projectId?: string, limit?: number): Promise<ChatHit[]>;
  chatFork(id: string, uptoSeq?: number): Promise<Chat>;
  chatAllowTool(id: string, tool: string): Promise<Chat>;
  chatDisallowTool(id: string, tool: string): Promise<Chat>;
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

/** Go's nil slices arrive as null. */
export function normalizeChat<T extends Chat>(c: T): T {
  return { ...c, allowedTools: arr(c.allowedTools) };
}

export function createChatClient(t: Transport): ChatClient {
  return {
    chatList: async (p = {}) =>
      arr(await t.call<ChatListItem[] | null>('chat.list', { projectId: p.projectId ?? '', query: p.query ?? '', includeArchived: p.includeArchived ?? false, ...(p.limit ? { limit: p.limit } : {}) })).map(normalizeChat),
    chatCreate: async (p) => normalizeChat(await t.call<Chat>('chat.create', p)),
    chatGet: async (id) => {
      const d = await t.call<ChatDetail>('chat.get', { id });
      return { chat: normalizeChat(d.chat), messages: arr(d.messages), hasMore: !!d.hasMore };
    },
    chatMessages: async (id, beforeSeq = 0, limit = 100) => {
      const p = await t.call<ChatPage | null>('chat.messages', { id, beforeSeq, limit });
      return { messages: arr(p?.messages), hasMore: !!p?.hasMore };
    },
    chatSend: (id, text, attach = []) => t.call('chat.send', { id, text, attach }),
    chatStop: (id) => t.call('chat.stop', { id }),
    chatUpdate: async (u) => normalizeChat(await t.call<Chat>('chat.update', u)),
    chatDelete: async (id) => {
      await t.call('chat.delete', { id });
    },
    chatSearch: async (query, projectId = '', limit = 50) => arr(await t.call<ChatHit[] | null>('chat.search', { query, projectId, limit })),
    chatFork: async (id, uptoSeq = 0) => normalizeChat(await t.call<Chat>('chat.fork', { id, uptoSeq })),
    chatAllowTool: async (id, tool) => normalizeChat(await t.call<Chat>('chat.allowTool', { id, tool })),
    chatDisallowTool: async (id, tool) => normalizeChat(await t.call<Chat>('chat.disallowTool', { id, tool })),
  };
}
