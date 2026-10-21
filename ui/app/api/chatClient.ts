// Typed client for the Chats section (docs/chats.md, decision P29): the human's own conversations with an agent CLI,
// kept with every message, continued in the CLI's session, forked, searched and attached to later messages.

import { encodeBase64 } from './attachClient.ts';
import { decodeBase64 } from './dockClient.ts';
import { isRecord } from './guards.ts';
import type { Transport } from './transport.ts';

export type ChatStatus = 'idle' | 'running' | 'error';
export type ChatRole = 'user' | 'assistant' | 'tool' | 'denied' | 'error' | 'system';

export interface Chat {
  id: string;
  /** Empty: a chat without a project. */
  projectId: string;
  title: string;
  adapter: string;
  /** Where the CLI runs: empty on Windows, else the WSL distro whose own install every turn starts inside (decision P41). */
  wslDistro?: string;
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
  /** Workspace library skills chosen for this chat (decision P60); its CLI reads them through Drydock's MCP gateway. */
  skills?: ChatSkillRef[];
  /** Workspace library MCP servers granted to this chat's sessions, with the tools each may call (decision P60). */
  mcpServers?: ChatMcpGrant[];
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
  /** Raised by the core with every write to the chat: of two snapshots of a chat, the higher version is the newer one. */
  version: number;
}

/** A workspace library skill chosen for a chat. */
export interface ChatSkillRef {
  registryId: string;
  name: string;
}

/** A workspace library MCP server granted to a chat; empty tools on a create or update: the server's tools as known now. */
export interface ChatMcpGrant {
  serverId: string;
  tools: string[];
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
  /** How a tool call ended: 'ok', 'failed', or absent while it runs (and when it reported no result). */
  toolStatus?: 'ok' | 'failed' | string;
  /** What the tool call returned, redacted and bounded (8 KB). */
  toolResult?: string;
  /** How long the tool call took, in milliseconds. */
  toolMs?: number;
  /** The agent's reasoning before this tool call or answer (P89), redacted and bounded. */
  thinking?: string;
  /** The files the person sent with this message (P90). */
  attachments?: ChatAttachment[];
}

/** A file attached to a chat message: text is quoted to the agent, a picture given beside the message, anything else named. */
export interface ChatAttachment {
  id: string;
  name: string;
  mime: string;
  size: number;
  kind: 'text' | 'image' | 'file' | string;
}

/** chat.tool_result: how one tool call of a chat ended. */
export interface ChatToolResult {
  chatId: string;
  messageId: string;
  status: string;
  result: string;
  ms: number;
}
export const isChatToolResult = (v: unknown): v is ChatToolResult =>
  isRecord(v) && typeof v.chatId === 'string' && typeof v.messageId === 'string' && typeof v.status === 'string' && typeof v.result === 'string' && typeof v.ms === 'number';

/** A page of messages, oldest first; hasMore says whether still older ones exist. */
export interface ChatPage {
  messages: ChatMessage[];
  hasMore: boolean;
}

/** chat.get: the chat and its latest 200 messages. */
export interface ChatDetail extends ChatPage {
  chat: Chat;
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
  /** Run the CLI inside this WSL distro (its own install and sign-in); omitted: on Windows. */
  wslDistro?: string;
  model?: string;
  effort?: string;
  permissionMode?: string;
  readHistory?: boolean;
  skills?: ChatSkillRef[];
  mcpServers?: ChatMcpGrant[];
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
  /** Where the chat runs (decision P60): a change starts a new session given the conversation so far. */
  adapter?: string;
  /** '' runs it on Windows. */
  wslDistro?: string;
  /** '' is the chat's private folder; only for a chat without a project. */
  cwd?: string;
  skills?: ChatSkillRef[];
  mcpServers?: ChatMcpGrant[];
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

/** The checks the live feed makes on what chat.* events carry (audit 2026-10-16, L43): the fields the screens call methods on or sort by. */
export const isChatMessage = (v: unknown): v is ChatMessage =>
  isRecord(v) && typeof v.id === 'string' && typeof v.chatId === 'string' && Number.isInteger(v.seq) && typeof v.role === 'string' && typeof v.body === 'string';
export const isChat = (v: unknown): v is Chat =>
  isRecord(v) && typeof v.id === 'string' && typeof v.title === 'string' && typeof v.adapter === 'string' && typeof v.status === 'string' && (v.version === undefined || typeof v.version === 'number');
export const isChatTurnEnd = (v: unknown): v is ChatTurnEnd =>
  isRecord(v) && typeof v.chatId === 'string' && typeof v.turnId === 'string' && (v.status === 'done' || v.status === 'error' || v.status === 'stopped');

/** A tool call the chat's CLI waits to be allowed (an ACP agent asking over its protocol); only the person answers it. */
export interface ChatAsk {
  id: string;
  chatId: string;
  turnId: string;
  /** The note in the chat that records the request. */
  messageId: string;
  tool: string;
  /** The call in one line, as the person reads it (secrets redacted). */
  summary: string;
  /** The call's input as stored (redacted, bounded). */
  input?: unknown;
  created: number;
  /** When it is refused if nobody answered. */
  expiresAt: number;
}

export const isChatAsk = (v: unknown): v is ChatAsk =>
  isRecord(v) && typeof v.id === 'string' && typeof v.chatId === 'string' && typeof v.summary === 'string' && typeof v.expiresAt === 'number';

export interface ChatClient {
  /** limit: at most this many (1-500; omitted = 500). */
  chatList(p?: { projectId?: string; query?: string; includeArchived?: boolean; limit?: number }): Promise<ChatListItem[]>;
  chatCreate(p: ChatCreate): Promise<Chat>;
  chatGet(id: string): Promise<ChatDetail>;
  /** Older messages, oldest first; hasMore says whether still older ones exist. */
  chatMessages(id: string, beforeSeq?: number, limit?: number): Promise<ChatPage>;
  /** The turn runs in the background; version is the chat's version once the turn was claimed (a snapshot at or after it shows the turn). */
  /** files go with the message (P90): at most 4, each at most 10 MB; a picture only to an agent that takes pictures */
  chatSend(id: string, text: string, attach?: string[], files?: { name: string; data: Uint8Array }[]): Promise<{ turnId: string; version: number }>;
  /** A file attached to a chat message, with its bytes. */
  chatAttachment(id: string): Promise<ChatAttachment & { data: Uint8Array }>;
  chatStop(id: string): Promise<{ stopped: boolean }>;
  chatUpdate(u: ChatUpdate): Promise<Chat>;
  chatDelete(id: string): Promise<void>;
  chatSearch(query: string, projectId?: string, limit?: number): Promise<ChatHit[]>;
  chatFork(id: string, uptoSeq?: number): Promise<Chat>;
  /** Replaces the person's message at seq (and everything after it) with text, or the same text when none, and runs the turn again (P89). */
  chatResend(id: string, seq: number, text?: string): Promise<{ turnId: string; version: number }>;
  chatAllowTool(id: string, tool: string): Promise<Chat>;
  chatDisallowTool(id: string, tool: string): Promise<Chat>;
  /** The tool calls the chat's CLI is waiting on, oldest first. */
  chatAsks(id: string): Promise<ChatAsk[]>;
  /** Answers one: allow lets exactly that call run once. */
  chatAnswer(id: string, askId: string, allow: boolean): Promise<void>;
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

/** Go's nil slices arrive as null. */
export function normalizeChat<T extends Chat>(c: T): T {
  return { ...c, allowedTools: arr(c.allowedTools), skills: arr(c.skills), mcpServers: arr(c.mcpServers).map((g) => ({ ...g, tools: arr(g.tools) })) };
}

const page = (p: ChatPage | null | undefined): ChatPage => ({ messages: arr(p?.messages), hasMore: !!p?.hasMore });

/** The title shown for a chat: its own, else "New chat". */
export const chatTitle = (c: Pick<Chat, 'title'>): string => c.title.trim() || 'New chat';

export function createChatClient(t: Transport): ChatClient {
  return {
    chatList: async (p = {}) =>
      arr(await t.call<ChatListItem[] | null>('chat.list', { projectId: p.projectId ?? '', query: p.query ?? '', includeArchived: p.includeArchived ?? false, ...(p.limit ? { limit: p.limit } : {}) })).map(normalizeChat),
    chatCreate: async (p) => normalizeChat(await t.call<Chat>('chat.create', p)),
    chatGet: async (id) => {
      const d = await t.call<ChatDetail>('chat.get', { id });
      return { chat: normalizeChat(d.chat), ...page(d) };
    },
    chatMessages: async (id, beforeSeq = 0, limit = 100) => page(await t.call<ChatPage | null>('chat.messages', { id, beforeSeq, limit })),
    chatSend: (id, text, attach = [], files = []) =>
      t.call('chat.send', files.length ? { id, text, attach, files: files.map((f) => ({ name: f.name, data: encodeBase64(f.data) })) } : { id, text, attach }),
    chatAttachment: async (id) => {
      const r = await t.call<ChatAttachment & { data: string | null }>('chat.attachment', { id });
      return { ...r, data: decodeBase64(r.data) };
    },
    chatStop: (id) => t.call('chat.stop', { id }),
    chatUpdate: async (u) => normalizeChat(await t.call<Chat>('chat.update', u)),
    chatDelete: async (id) => {
      await t.call('chat.delete', { id });
    },
    chatSearch: async (query, projectId = '', limit = 50) => arr(await t.call<ChatHit[] | null>('chat.search', { query, projectId, limit })),
    chatFork: async (id, uptoSeq = 0) => normalizeChat(await t.call<Chat>('chat.fork', { id, uptoSeq })),
    chatResend: (id, seq, text = '') => t.call('chat.resend', { id, seq, text }),
    chatAllowTool: async (id, tool) => normalizeChat(await t.call<Chat>('chat.allowTool', { id, tool })),
    chatDisallowTool: async (id, tool) => normalizeChat(await t.call<Chat>('chat.disallowTool', { id, tool })),
    chatAsks: async (id) => arr(await t.call<ChatAsk[] | null>('chat.asks', { id })).filter(isChatAsk),
    chatAnswer: async (id, askId, allow) => {
      await t.call('chat.answer', { id, askId, allow });
    },
  };
}
