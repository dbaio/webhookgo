// The admin panel's API (/api/admin/*, admins with a session only) and its types.
import { ApiError } from './api';
import { app } from './state.svelte';
import type { Page, Role, Token, User } from './types';

export interface AdminStats {
  users: { total: number; admins: number; disabled: number };
  urls: { owned: number };
  reports: { open: number };
  /** From Redis INFO. maxmemory 0 = no limit. */
  redis: { used_memory: number; maxmemory: number; evicted_keys: number };
  database: { size_bytes: number };
  version: string;
}

export interface AdminUser extends User {
  disabled_at: string | null;
  url_count: number;
}

/** A one-time link: an invitation or a password reset. */
export interface AdminLink {
  link: string;
  expires_at: string;
}

export interface Owner {
  id: string;
  email: string;
}

/** Which URLs the admin list shows: private ones, or anonymous (public) ones. */
export type UrlKind = 'owned' | 'anonymous';

/** A URL in the admin list; owner is null for an anonymous one. */
export interface AdminUrl extends Token {
  owner: Owner | null;
  latest_request_at: string | null;
  /** It answers with its own HTML, XML or SVG page: the usual sign of phishing. */
  serves_page: boolean;
}

/** What the live anonymous URLs created from one address add up to. */
export interface CreatorStats {
  ip: string;
  urls: number;
  /** Of those, the ones that serve a page. */
  pages: number;
  /** Stored in them now. */
  requests: number;
  latest_created_at: string;
}

/** How the anonymous URLs are ordered: newest first, or the most requests first. */
export type UrlSort = 'newest' | 'requests';

/** What is kept about a URL (wh:{id}:audit), as `webhookgo audit` prints it. */
export interface AuditRecord {
  uuid: string;
  status: 'live' | 'expired' | 'deleted' | string;
  created_at?: string;
  ip?: string;
  user_agent?: string;
  requests: number;
  first_request_at?: string;
  last_request_at?: string;
  deleted_at?: string;
  deleted_ip?: string;
  deleted_user_agent?: string;
  kept_until?: string;
}

/** Any UUID looked up: a live URL, a record of a gone one, both or neither. */
export interface UrlLookup {
  token: Token | null;
  owner: Owner | null;
  record: AuditRecord | null;
}

export type ReportStatus = 'open' | 'actioned' | 'dismissed';

/** One abuse report of a URL, sent from the /abuse page. */
export interface AbuseReport {
  id: string;
  reporter_ip: string;
  contact: string;
  message: string;
  status: ReportStatus;
  created_at: string;
  /** Empty while open, or once the admin who handled it is deleted. */
  handled_by_email: string;
  handled_at: string | null;
  note: string;
}

/** Every report of one URL, newest first, with what is known about the URL now. */
export interface ReportGroup extends UrlLookup {
  token_id: string;
  reports: AbuseReport[];
  open: number;
  first_at: string;
  last_at: string;
}

/** How the reports of a URL are closed: by deleting it, disabling its owner, or doing nothing. */
export type ResolveAction = 'delete_url' | 'disable_owner' | 'dismiss';

export interface LogEntry {
  id: string;
  actor_id: string | null;
  /** A copy of the actor's address; "cli" for the command line, "setup" for the first admin. */
  actor_email: string;
  action: string;
  target_type: 'user' | 'url' | 'link' | string;
  target_id: string;
  details: Record<string, unknown> | null;
  ip: string | null;
  created_at: string;
}

/** The actions the log filter offers. */
export const LOG_ACTIONS = [
  'user.invite',
  'user.role',
  'user.disable',
  'user.enable',
  'user.reset_link',
  'user.delete',
  'url.view',
  'url.delete',
  'report.resolve',
  'setup',
  'cli.invite',
  'cli.reset',
];

export const ADMIN_PATH = '/admin';
export const LOGIN_FOR_ADMIN = `/login?next=${encodeURIComponent(ADMIN_PATH)}`;

/** The first UUID in a bare ID or in any text holding one (a webhook URL, a viewer link). */
export function findUUID(text: string): string {
  return text.match(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i)?.[0].toLowerCase() ?? '';
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, 'Network error — is the server running?');
  }
  // Refused: the session ended (signed out elsewhere, disabled) or is no
  // longer an admin's. Ask who is signed in: the page then sends a signed-out
  // viewer to sign in, and shows "Admins only" to anyone else.
  if (res.status === 401 || res.status === 403) app.loadUser().catch(() => {});
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    /* not JSON */
  }
  if (!res.ok) throw new ApiError(res.status, (data as { error?: string } | null)?.error ?? `HTTP ${res.status}`, data);
  return data as T;
}

const query = (params: Record<string, string | number | undefined>) => {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== '') p.set(k, String(v));
  const s = p.toString();
  return s ? `?${s}` : '';
};

export interface AdminListParams {
  page?: number;
  perPage?: number;
  q?: string;
}

export const adminApi = {
  stats: () => call<AdminStats>('GET', '/api/admin/stats'),

  listUsers: ({ page = 1, perPage = 25, q = '' }: AdminListParams = {}) =>
    call<Page<AdminUser>>('GET', `/api/admin/users${query({ q, page, per_page: perPage })}`),
  invite: (email: string, role: Role) => call<AdminLink>('POST', '/api/admin/users', { email, role }),
  updateUser: (id: string, change: { role?: Role; disabled?: boolean }) =>
    call<AdminUser>('PATCH', `/api/admin/users/${id}`, change),
  resetLink: (id: string) => call<AdminLink>('POST', `/api/admin/users/${id}/reset`),
  deleteUser: (id: string) => call<void>('DELETE', `/api/admin/users/${id}`),

  listUrls: ({
    page = 1,
    perPage = 25,
    q = '',
    owner = '',
    kind = 'owned',
    pages = false,
    sort = 'newest',
  }: AdminListParams & { owner?: string; kind?: UrlKind; pages?: boolean; sort?: UrlSort } = {}) =>
    call<Page<AdminUrl>>(
      'GET',
      `/api/admin/urls${query({ kind: kind === 'owned' ? '' : kind, q, owner, pages: pages ? 1 : undefined, sort: sort === 'newest' ? '' : sort, page, per_page: perPage })}`,
    ),
  /** The addresses that created the most live anonymous URLs (the top 20), and how many URLs were read. */
  topIps: () => call<{ data: CreatorStats[]; urls: number }>('GET', '/api/admin/ips'),
  lookupUrl: (id: string) => call<UrlLookup>('GET', `/api/admin/urls/${id}`),
  deleteUrl: (id: string) => call<void>('DELETE', `/api/admin/urls/${id}`),

  listReports: ({ page = 1, perPage = 20, status = 'open' }: Omit<AdminListParams, 'q'> & { status?: ReportStatus | 'all' } = {}) =>
    call<Page<ReportGroup>>('GET', `/api/admin/reports${query({ status, page, per_page: perPage })}`),
  resolveReports: (tokenId: string, action: ResolveAction, note = '') =>
    call<{ updated: number }>('POST', '/api/admin/reports/resolve', { token_id: tokenId, action, note }),

  log: ({ page = 1, perPage = 50, action = '', actor = '' }: Omit<AdminListParams, 'q'> & { action?: string; actor?: string } = {}) =>
    call<Page<LogEntry>>('GET', `/api/admin/log${query({ action, actor, page, per_page: perPage })}`),
};
