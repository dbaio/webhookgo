import type { AppConfig, FullRequest, LinkInfo, Page, RedeemInput, Summary, Token, TokenInput, TokenItem, User } from './types';

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public data?: unknown,
  ) {
    super(message);
  }
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
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    /* not JSON */
  }
  if (!res.ok) {
    const msg = (data as { error?: string } | null)?.error ?? `HTTP ${res.status}`;
    throw new ApiError(res.status, msg, data);
  }
  return data as T;
}

export interface ListParams {
  page?: number;
  perPage?: number;
  q?: string;
  method?: string;
  sorting?: 'newest' | 'oldest';
}

export const api = {
  config: () => call<AppConfig>('GET', '/api/config'),
  createToken: (input: TokenInput = {}) => call<Token>('POST', '/token', input),
  getToken: (id: string) => call<Token>('GET', `/token/${id}`),
  updateToken: (id: string, input: TokenInput) => call<Token>('PUT', `/token/${id}`, input),
  deleteToken: (id: string) => call<void>('DELETE', `/token/${id}`),
  listRequests: (id: string, { page = 1, perPage = 50, q = '', method = '', sorting = 'newest' }: ListParams = {}) => {
    const p = new URLSearchParams({ page: String(page), per_page: String(perPage), sorting, summary: '1' });
    if (q) p.set('q', q);
    if (method) p.set('method', method);
    return call<Page<Summary>>('GET', `/token/${id}/requests?${p}`);
  },
  getRequest: (id: string, rid: string) => call<FullRequest>('GET', `/token/${id}/request/${rid}`),
  deleteRequest: (id: string, rid: string) => call<{ status: boolean }>('DELETE', `/token/${id}/request/${rid}`),
  clearRequests: (id: string) => call<{ status: boolean }>('DELETE', `/token/${id}/request`),
  rawUrl: (id: string, rid: string, download = false) =>
    `/token/${id}/request/${rid}/raw${download ? '?download=1' : ''}`,
  streamUrl: (id: string) => `/token/${id}/stream`,

  // Accounts (only with ACCOUNTS=on; /api/me answers null otherwise)
  me: () => call<{ user: User | null }>('GET', '/api/me').then((r) => r.user),
  login: (email: string, password: string) =>
    call<{ user: User }>('POST', '/api/login', { email, password }).then((r) => r.user),
  logout: () => call<void>('POST', '/api/logout'),
  inspectLink: (token: string) => call<LinkInfo>('POST', '/api/links/inspect', { token }),
  redeemLink: (input: RedeemInput) => call<{ user: User }>('POST', '/api/links/redeem', input).then((r) => r.user),
  updateAccount: (input: { name: string }) => call<{ user: User }>('PATCH', '/api/account', input).then((r) => r.user),
  changePassword: (current: string, next: string) =>
    call<void>('POST', '/api/account/password', { current_password: current, new_password: next }),
  logoutOthers: () => call<void>('POST', '/api/account/logout-others'),

  // Private URLs (accounts on)
  listTokens: ({ page = 1, perPage = 20, q = '' }: Pick<ListParams, 'page' | 'perPage' | 'q'> = {}) => {
    const p = new URLSearchParams({ page: String(page), per_page: String(perPage) });
    if (q) p.set('q', q);
    return call<Page<TokenItem>>('GET', `/token?${p}`);
  },
  claimToken: (id: string, claimKey: string) => call<Token>('POST', `/token/${id}/claim`, { claim_key: claimKey }),
  releaseToken: (id: string) => call<Token>('POST', `/token/${id}/release`),
};

export const isUUID = (s: string | undefined): s is string =>
  !!s && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(s);
