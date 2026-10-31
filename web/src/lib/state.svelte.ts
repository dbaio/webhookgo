import { api, ApiError, isUUID } from './api';
import { bytes, duration } from './format';
import { defaultForward, forward, type ForwardResult, type ForwardSettings } from './forward';
import { read, readSession, write, writeSession } from './storage';
import type { AppConfig, FullRequest, LiveEvent, Summary, Token, TokenInput, User } from './types';

export interface Prefs {
  theme: 'system' | 'light' | 'dark';
  autoSelect: boolean; // jump to each new request as it arrives
  pretty: boolean; // format JSON / XML bodies
  wrap: boolean; // wrap long lines in bodies
  notify: boolean; // desktop notification when the tab is hidden
  utc: boolean; // times in UTC instead of the browser's time zone
  sidebarWidth: number;
  collapsed: Record<string, boolean>;
}

const defaultPrefs: Prefs = {
  theme: 'system',
  autoSelect: false,
  pretty: true,
  wrap: true,
  notify: false,
  utc: false,
  sidebarWidth: 340,
  collapsed: {},
};

// The anonymous URLs this tab created in the last hour: signing in from the
// tab saves them to the account. Only this tab's, and only recent ones: on a
// shared computer, the browser's other URLs may be someone else's.
const MADE_HERE = 'wh:made-here';
const MADE_HERE_FOR = 60 * 60_000;
interface Made {
  id: string;
  at: number; // ms
}

export interface HistoryEntry {
  id: string;
  description: string;
  created_at: string;
  last_seen: string;
  owner_id?: string; // the account it belonged to when last seen here
  claim_key?: string; // saves this anonymous URL to an account; never shown
}

export interface Toast {
  id: number;
  message: string;
  kind: 'info' | 'success' | 'error';
  action?: ToastAction;
}

export interface ToastAction {
  label: string;
  run: () => void;
}

export type Dialog = null | 'settings' | 'forward' | 'shortcuts';

const PER_PAGE = 50;
const DETAIL_CACHE = 150;
// The server sends an event as soon as the stream opens. A stream that opens
// but stays silent this long is being held back by something in between.
const STALL_MS = 4000;
const POLL_MS = 3000;
const POLL_HIDDEN_MS = 15_000;
const POLL_PAGE = 20;
const MOBILE = '(max-width: 899px)';
const HISTORY_MAX = 30;

/** The URL expired, never existed or was deleted. */
const gone = (e: unknown) => e instanceof ApiError && (e.status === 404 || e.status === 410);

/** A history entry for a URL as the server describes it now. */
function entryFor(t: Token, prev: HistoryEntry | undefined, last_seen: string): HistoryEntry {
  const entry: HistoryEntry = { id: t.uuid, description: t.description, created_at: t.created_at, last_seen };
  // The claim key comes only once, and a URL saved to an account no longer needs it.
  const claim_key = t.claim_key ?? prev?.claim_key;
  if (t.owner_id) entry.owner_id = t.owner_id;
  else if (claim_key) entry.claim_key = claim_key;
  return entry;
}

// Past the limit the oldest entries go, those saved to an account first: the
// server lists them anyway, while a lost claim key can't be recovered.
function trim(list: HistoryEntry[]): HistoryEntry[] {
  while (list.length > HISTORY_MAX) {
    const i = list.findLastIndex((h, j) => j > 0 && !!h.owner_id);
    list.splice(i > 0 ? i : list.length - 1, 1);
  }
  return list;
}

class Store {
  config = $state<AppConfig>({
    app_name: 'webhookgo',
    version: '',
    public_url: '',
    max_requests: 500,
    max_body_size: 2 << 20,
    max_delay: 30,
    token_ttl: 7 * 86400,
    retention: 0,
    abuse_contact: '',
    accounts: false,
    anonymous_urls: true,
  });
  prefs = $state<Prefs>({ ...defaultPrefs, ...read<Partial<Prefs>>('wh:prefs', {}) });
  history = $state<HistoryEntry[]>(read<HistoryEntry[]>('wh:history', []));
  toasts = $state<Toast[]>([]);
  now = $state(Date.now());
  mobile = $state(typeof matchMedia !== 'undefined' && matchMedia(MOBILE).matches);
  dialog = $state<Dialog>(null);
  user = $state<User | null>(null); // who is signed in, with accounts on
  userLoaded = $state(false); // /api/me has answered: until then the top bar shows neither "Sign in" nor an avatar
  claiming = $state(false); // saving URLs from this browser to the account

  token = $state<Token | null>(null);
  // private: the URL belongs to an account this viewer isn't signed in to.
  // welcome: nothing to open, and only signed-in users create URLs here.
  status = $state<'loading' | 'ready' | 'missing' | 'private' | 'welcome' | 'error'>('loading');
  error = $state('');
  privateId = $state(''); // the URL shown as private
  live = $state<'connecting' | 'live' | 'polling' | 'offline'>('connecting');

  requests = $state.raw<Summary[]>([]);
  total = $state(0); // requests stored for this URL
  matched = $state(0); // requests matching the current filter
  hasMore = $state(false);
  listLoading = $state(false);
  search = $state('');
  method = $state(''); // '' = any

  selectedId = $state<string | null>(null);
  details = $state.raw<Record<string, FullRequest>>({});
  unread = $state.raw<Set<string>>(new Set());
  forwardSettings = $state<ForwardSettings>({ ...defaultForward });
  forwardResults = $state.raw<Record<string, ForwardResult>>({});
  forwarding = $state.raw<Set<string>>(new Set());

  selected = $derived(this.selectedId ? (this.details[this.selectedId] ?? null) : null);
  filtering = $derived(this.search.trim() !== '' || this.method !== '');
  webhookUrl = $derived(this.token ? `${this.config.public_url || location.origin}/${this.token.uuid}` : '');
  empty = $derived(this.status === 'ready' && this.total === 0 && !this.filtering && this.requests.length === 0);
  // With ANONYMOUS_URLS=off only signed-in users create URLs. An older server
  // that doesn't send the flag lets anyone.
  canCreate = $derived(!this.config.accounts || this.config.anonymous_urls !== false || !!this.user);
  /** Anonymous URLs created on this browser that the signed-in user can save to their account. */
  claimable = $derived(this.user ? this.history.filter((h) => h.claim_key && !h.owner_id) : []);

  #es: EventSource | null = null;
  #retry = 0;
  #retryTimer: ReturnType<typeof setTimeout> | undefined;
  #stallTimer: ReturnType<typeof setTimeout> | undefined;
  #pollTimer: ReturnType<typeof setTimeout> | undefined;
  #searchTimer: ReturnType<typeof setTimeout> | undefined;
  #listSeq = 0;
  #openSeq = 0;
  #liveDuringLoad: Summary[] | null = null; // requests that arrived while a reset load was in flight
  #toastSeq = 0;
  #userReady: Promise<void> = Promise.resolve(); // settles once /api/me has answered

  async init() {
    setInterval(() => (this.now = Date.now()), 5_000);
    const mq = matchMedia(MOBILE);
    mq.addEventListener('change', () => (this.mobile = mq.matches));
    try {
      this.config = await api.config();
    } catch {
      /* keep defaults */
    }
    if (this.config.accounts)
      this.#userReady = this.loadUser().then(
        () => this.#keepMadeHere(),
        () => void (this.userLoaded = true),
      );
    window.addEventListener('hashchange', () => this.route());
    await this.route();
  }

  // --- Routing: #/<token>[/<request>] ---------------------------------------

  #parseHash() {
    const [t, r] = location.hash.replace(/^#\/?/, '').split('/');
    return { token: isUUID(t) ? t.toLowerCase() : undefined, request: isUUID(r) ? r.toLowerCase() : undefined };
  }

  #setHash(token: string, request?: string | null) {
    const h = `#/${token}${request ? `/${request}` : ''}`;
    if (location.hash !== h) history.replaceState(null, '', h);
  }

  async route() {
    const { token, request } = this.#parseHash();
    if (!token) {
      // Reopen the most recent URL that still exists, else create one. Stop as
      // soon as the user navigates somewhere themselves.
      for (const h of this.history.slice(0, 3)) {
        if (await this.open(h.id, undefined, true)) return;
        if (this.#parseHash().token) return;
      }
      // Then the account's newest URL, which may come from another browser.
      await this.#userReady;
      if (this.#parseHash().token) return;
      if (this.user) {
        const newest = await api.listTokens({ perPage: 1 }).then((p) => p.data[0], () => undefined);
        if (newest && (await this.open(newest.uuid, undefined, true))) return;
        if (this.#parseHash().token) return;
      }
      if (this.canCreate) await this.createToken();
      else this.#welcome();
      return;
    }
    if (token !== this.token?.uuid) await this.open(token, request);
    else if (request && request !== this.selectedId) this.select(request);
  }

  go(tokenId: string) {
    location.hash = `#/${tokenId}`;
  }

  // --- URL (token) lifecycle --------------------------------------------------

  async open(id: string, requestId?: string, quiet = false): Promise<boolean> {
    // A newer open() (e.g. a quick second click in the history) wins.
    const seq = ++this.#openSeq;
    this.#disconnect();
    this.#reset();
    this.status = 'loading';
    try {
      const t = await api.getToken(id);
      if (seq !== this.#openSeq) return false;
      this.token = t;
      this.total = t.request_count;
      this.status = 'ready';
      this.forwardSettings = { ...defaultForward, ...read<Partial<ForwardSettings>>(`wh:forward:${id}`, {}) };
      this.#remember(t);
      this.#setHash(id, requestId);
      this.#connect();
      await this.loadList(true);
      if (seq !== this.#openSeq) return false;
      if (requestId) this.select(requestId);
      else if (!this.mobile && this.requests[0]) this.select(this.requests[0].uuid);
      return true;
    } catch (e) {
      if (seq !== this.#openSeq) return false;
      if (gone(e)) {
        this.forget(id);
        if (quiet) return false;
        this.token = null;
        this.status = 'missing';
        return false;
      }
      if (quiet) return false;
      if (this.#denied(e)) {
        this.#private(id); // kept in the history: signing in may open it
        return false;
      }
      this.status = 'error';
      this.error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  #reset() {
    this.token = null;
    this.requests = [];
    this.total = 0;
    this.matched = 0;
    this.hasMore = false;
    this.selectedId = null;
    this.details = {};
    this.unread = new Set();
    this.forwardResults = {};
    this.search = '';
    this.method = '';
  }

  async createToken(input: TokenInput = {}): Promise<Token | null> {
    if (!this.canCreate) {
      this.toast('Sign in to create URLs', 'error', 3200, { label: 'Sign in', run: () => location.assign('/login') });
      return null;
    }
    try {
      const t = await api.createToken(input);
      this.#remember(t);
      if (t.claim_key) this.#madeHere(t.uuid);
      this.go(t.uuid);
      return t;
    } catch (e) {
      this.toast(`Could not create a URL: ${(e as Error).message}`, 'error');
      if (!this.token) {
        this.status = 'error';
        this.error = (e as Error).message;
      }
      return null;
    }
  }

  async saveToken(input: TokenInput): Promise<boolean> {
    if (!this.token) return false;
    try {
      this.token = await api.updateToken(this.token.uuid, input);
      this.#remember(this.token);
      this.toast('Response settings saved', 'success');
      return true;
    } catch (e) {
      this.toast((e as Error).message, 'error');
      return false;
    }
  }

  async deleteToken() {
    if (!this.token) return;
    const id = this.token.uuid;
    try {
      await api.deleteToken(id);
    } catch (e) {
      this.toast((e as Error).message, 'error');
      return;
    }
    this.forget(id);
    this.toast('URL deleted');
    const next = this.history[0];
    if (next) this.go(next.id);
    else if (this.canCreate) await this.createToken();
    else this.#welcome();
  }

  /** Makes a private URL anonymous again. Its new claim key goes to the history, so it can be saved back. */
  async releaseToken(): Promise<boolean> {
    if (!this.token) return false;
    try {
      this.token = await api.releaseToken(this.token.uuid);
      this.#remember(this.token);
      this.toast(`The URL is anonymous now: anyone with it can see it, and it expires after ${duration(this.config.token_ttl)} without use`, 'success', 5000);
      return true;
    } catch (e) {
      this.toast((e as Error).message, 'error');
      return false;
    }
  }

  // The URL belongs to an account this viewer isn't signed in to (nor an admin).
  #denied(e: unknown) {
    return this.config.accounts && e instanceof ApiError && e.status === 403;
  }

  #private(id: string) {
    this.#disconnect();
    this.token = null;
    this.privateId = id;
    this.status = 'private';
    this.dialog = null;
  }

  // Nothing to show, and only signed-in users create URLs: say so instead.
  #welcome() {
    this.#disconnect();
    this.#reset();
    this.status = 'welcome';
    if (location.hash) history.replaceState(null, '', location.pathname + location.search);
  }

  #remember(t: Token) {
    const entry = entryFor(t, this.history.find((h) => h.id === t.uuid), new Date().toISOString());
    this.history = trim([entry, ...this.history.filter((h) => h.id !== t.uuid)]);
    write('wh:history', this.history);
  }

  forget(id: string) {
    this.history = this.history.filter((h) => h.id !== id);
    write('wh:history', this.history);
  }

  clearHistory() {
    this.history = this.history.filter((h) => h.id === this.token?.uuid);
    write('wh:history', this.history);
  }

  // --- Request list -----------------------------------------------------------

  async loadList(reset = false) {
    if (!this.token) return;
    const id = this.token.uuid;
    const seq = ++this.#listSeq;
    // Derive the page from what is loaded: live inserts and deletes shift
    // offsets, and duplicates are dropped below.
    const page = reset ? 1 : Math.floor(this.requests.length / PER_PAGE) + 1;
    this.listLoading = true;
    if (reset) this.#liveDuringLoad = [];
    try {
      const res = await api.listRequests(id, { page, perPage: PER_PAGE, q: this.search.trim(), method: this.method });
      if (seq !== this.#listSeq || this.token?.uuid !== id) return;
      if (reset) {
        // The snapshot may predate requests pushed live meanwhile: keep those.
        const ids = new Set(res.data.map((r) => r.uuid));
        const missed = (this.#liveDuringLoad ?? []).filter((r) => !ids.has(r.uuid));
        this.requests = [...missed, ...res.data];
        res.total += missed.length;
      } else {
        const seen = new Set(this.requests.map((r) => r.uuid));
        this.requests = [...this.requests, ...res.data.filter((r) => !seen.has(r.uuid))];
      }
      this.matched = res.total;
      if (!this.filtering) this.total = res.total;
      this.hasMore = !res.is_last_page;
    } catch (e) {
      if (seq === this.#listSeq) this.toast(`Could not load requests: ${(e as Error).message}`, 'error');
    } finally {
      if (seq === this.#listSeq) {
        this.listLoading = false;
        this.#liveDuringLoad = null;
      }
    }
  }

  loadMore() {
    if (this.hasMore && !this.listLoading) this.loadList();
  }

  setSearch(q: string) {
    this.search = q;
    clearTimeout(this.#searchTimer);
    this.#searchTimer = setTimeout(() => this.loadList(true), 220);
  }

  setMethod(m: string) {
    this.method = m;
    this.loadList(true);
  }

  async select(id: string | null) {
    this.selectedId = id;
    if (!this.token) return;
    const tokenId = this.token.uuid;
    this.#setHash(tokenId, id);
    if (!id) return;
    if (this.unread.has(id)) {
      const u = new Set(this.unread);
      u.delete(id);
      this.unread = u;
    }
    if (this.details[id]) return;
    try {
      const r = await api.getRequest(tokenId, id);
      this.#cache(r);
    } catch (e) {
      if (this.selectedId !== id) return;
      this.toast(e instanceof ApiError && e.status === 404 ? 'That request no longer exists' : (e as Error).message, 'error');
      this.selectedId = null;
      this.#setHash(tokenId);
    }
  }

  #cache(r: FullRequest) {
    const next = { ...this.details, [r.uuid]: r };
    const keys = Object.keys(next).filter((k) => k !== this.selectedId);
    if (keys.length > DETAIL_CACHE) for (const k of keys.slice(0, keys.length - DETAIL_CACHE)) delete next[k];
    this.details = next;
  }

  async #full(id: string): Promise<FullRequest | null> {
    if (this.details[id]) return this.details[id];
    if (!this.token) return null;
    try {
      const r = await api.getRequest(this.token.uuid, id);
      this.#cache(r);
      return r;
    } catch {
      return null;
    }
  }

  step(delta: number) {
    const list = this.requests;
    if (!list.length) return;
    const i = list.findIndex((r) => r.uuid === this.selectedId);
    const next = i < 0 ? 0 : Math.min(list.length - 1, Math.max(0, i + delta));
    this.select(list[next].uuid);
    if (next >= list.length - 3) this.loadMore();
  }

  #removeLocal(id: string) {
    const i = this.requests.findIndex((r) => r.uuid === id);
    if (i >= 0) {
      this.requests = this.requests.filter((r) => r.uuid !== id);
      this.matched = Math.max(0, this.matched - 1);
    }
    if (this.selectedId === id) {
      const neighbour = this.requests[Math.min(i, this.requests.length - 1)];
      this.select(neighbour && !this.mobile ? neighbour.uuid : null);
    }
    if (this.unread.has(id)) {
      const u = new Set(this.unread);
      u.delete(id);
      this.unread = u;
    }
  }

  async deleteRequest(id: string) {
    if (!this.token) return;
    const tokenId = this.token.uuid;
    this.#removeLocal(id);
    this.total = Math.max(0, this.total - 1);
    try {
      await api.deleteRequest(tokenId, id);
    } catch (e) {
      this.toast((e as Error).message, 'error');
      this.loadList(true);
    }
  }

  async clearRequests() {
    if (!this.token) return;
    try {
      await api.clearRequests(this.token.uuid);
      this.#clearLocal();
      this.toast('All requests deleted');
    } catch (e) {
      this.toast((e as Error).message, 'error');
    }
  }

  #clearLocal() {
    this.requests = [];
    this.total = 0;
    this.matched = 0;
    this.hasMore = false;
    this.unread = new Set();
    this.details = {};
    this.forwardResults = {};
    if (this.selectedId) this.select(null);
  }

  markAllRead() {
    this.unread = new Set();
  }

  // --- Live updates (Server-Sent Events) --------------------------------------

  #connect() {
    if (!this.token) return;
    const id = this.token.uuid;
    this.live = 'connecting';
    const es = new EventSource(api.streamUrl(id));
    this.#es = es;
    let first = true;
    es.onopen = () => {
      clearTimeout(this.#stallTimer);
      this.#stallTimer = setTimeout(() => {
        if (this.#es !== es) return;
        es.close();
        this.#es = null;
        this.#poll(id);
      }, STALL_MS);
    };
    es.onmessage = (m) => {
      if (this.#es !== es) return es.close();
      clearTimeout(this.#stallTimer);
      let ev: LiveEvent;
      try {
        ev = JSON.parse(m.data) as LiveEvent;
      } catch {
        return;
      }
      if (ev.type === 'connected') {
        this.live = 'live';
        this.#retry = 0;
        if (!first) this.loadList(true); // resynchronise after a drop
        first = false;
        return;
      }
      if (ev.type === 'resync') {
        this.loadList(true); // the server may have missed events
        return;
      }
      this.#handle(ev);
    };
    es.onerror = () => {
      if (this.#es !== es) return es.close();
      if (es.readyState !== EventSource.CLOSED) {
        this.live = 'connecting'; // the browser retries by itself
        return;
      }
      // The server answered with an error: back off and check the URL still exists.
      es.close();
      this.#es = null;
      this.live = 'offline';
      const delay = Math.min(30_000, 1000 * 2 ** this.#retry++);
      this.#retryTimer = setTimeout(() => this.#reconnect(id), delay);
    };
  }

  async #reconnect(id: string) {
    if (this.token?.uuid !== id || this.#es) return;
    try {
      await api.getToken(id);
      if (this.token?.uuid === id && !this.#es) this.#connect();
    } catch (e) {
      if (this.token?.uuid !== id) return;
      if (gone(e)) {
        this.status = 'missing';
        this.token = null;
      } else if (this.#denied(e)) {
        this.#private(id); // e.g. a stream that reconnected after a claim or a release
      } else {
        this.#retryTimer = setTimeout(() => this.#reconnect(id), Math.min(30_000, 1000 * 2 ** this.#retry++));
      }
    }
  }

  #disconnect() {
    clearTimeout(this.#retryTimer);
    clearTimeout(this.#stallTimer);
    clearTimeout(this.#pollTimer);
    this.#es?.close();
    this.#es = null;
  }

  // Polling fallback, for paths that hold back streamed responses (Cloudflare
  // Quick Tunnels, some corporate proxies): new requests are picked up from
  // the list instead. Changes made in other tabs show up on the next reload.
  #poll(id: string) {
    this.live = 'polling';
    const tick = async () => {
      if (this.token?.uuid !== id || this.live !== 'polling' || this.#es) return;
      await this.#pollOnce(id);
      if (this.token?.uuid === id && this.live === 'polling' && !this.#es)
        this.#pollTimer = setTimeout(tick, document.hidden ? POLL_HIDDEN_MS : POLL_MS);
    };
    clearTimeout(this.#pollTimer);
    this.#pollTimer = setTimeout(tick, POLL_MS);
  }

  async #pollOnce(id: string) {
    if (this.listLoading) return;
    try {
      const res = await api.listRequests(id, { perPage: POLL_PAGE, q: this.search.trim(), method: this.method });
      if (this.token?.uuid !== id || this.listLoading) return;
      const known = new Set(this.requests.map((r) => r.uuid));
      const fresh = res.data.filter((r) => !known.has(r.uuid));
      if (!fresh.length) return;
      // More arrived than one page holds: reload rather than leave a gap.
      if (fresh.length === res.data.length && res.total > res.data.length) return this.loadList(true);
      for (const r of fresh.reverse())
        this.#handle({ type: 'request.created', request: r, total: this.filtering ? this.total + 1 : res.total });
    } catch (e) {
      if (this.token?.uuid !== id) return;
      if (gone(e)) {
        this.#disconnect();
        this.status = 'missing';
        this.token = null;
      } else if (this.#denied(e)) {
        this.#private(id);
      }
    }
  }

  #handle(ev: LiveEvent) {
    switch (ev.type) {
      case 'request.created': {
        const r = ev.request;
        this.total = ev.total;
        let list = this.requests;
        if (ev.evicted?.length) {
          const gone = new Set(ev.evicted);
          list = list.filter((x) => !gone.has(x.uuid));
        }
        if (this.search.trim()) {
          clearTimeout(this.#searchTimer);
          this.#searchTimer = setTimeout(() => this.loadList(true), 400);
        } else if ((!this.method || this.method === r.method) && !list.some((x) => x.uuid === r.uuid)) {
          list = [r, ...list];
          this.matched = this.filtering ? this.matched + 1 : ev.total;
          this.#liveDuringLoad?.unshift(r);
        }
        this.requests = list;
        const jump = this.prefs.autoSelect || (!this.selectedId && !this.mobile);
        if (jump && list[0]?.uuid === r.uuid) this.select(r.uuid);
        else this.unread = new Set(this.unread).add(r.uuid);
        this.#notify(r);
        if (this.forwardSettings.auto && this.forwardSettings.url) this.forward(r.uuid, true);
        break;
      }
      case 'request.deleted':
        this.total = ev.total;
        this.#removeLocal(ev.id);
        break;
      case 'requests.cleared':
        this.#clearLocal();
        break;
      case 'token.updated':
        this.token = ev.token;
        this.#remember(ev.token);
        break;
      case 'token.deleted':
        this.#disconnect();
        this.token = null;
        this.status = 'missing';
        break;
      case 'token.claimed':
      case 'token.released':
        if (this.token) this.#recheck(this.token.uuid);
        break;
    }
  }

  // Who may see the URL changed: ask again, as this viewer. The server also
  // ends the stream, so it reconnects with the new permissions.
  async #recheck(id: string) {
    try {
      const t = await api.getToken(id);
      if (this.token?.uuid !== id) return;
      this.token = t;
      this.#remember(t);
    } catch (e) {
      if (this.token?.uuid !== id) return;
      if (this.#denied(e)) this.#private(id);
      else if (gone(e)) {
        this.#disconnect();
        this.token = null;
        this.status = 'missing';
      }
    }
  }

  #notify(r: Summary) {
    if (!this.prefs.notify || !document.hidden || typeof Notification === 'undefined') return;
    if (Notification.permission !== 'granted') return;
    const n = new Notification(`${r.method} ${r.path}`, {
      body: `${r.ip} · ${bytes(r.size)} · ${this.config.app_name}`,
      tag: r.uuid,
      icon: '/favicon.svg',
    });
    n.onclick = () => {
      window.focus();
      this.select(r.uuid);
      n.close();
    };
  }

  async setNotify(on: boolean) {
    if (on) {
      if (typeof Notification === 'undefined') {
        this.toast('Notifications need HTTPS (or localhost) and a supporting browser', 'error');
        return;
      }
      if ((await Notification.requestPermission()) !== 'granted') {
        this.toast('Notification permission was denied', 'error');
        return;
      }
    }
    this.prefs.notify = on;
    this.savePrefs();
  }

  // --- Forwarding ---------------------------------------------------------------

  saveForward(s: ForwardSettings) {
    this.forwardSettings = s;
    if (this.token) write(`wh:forward:${this.token.uuid}`, s);
  }

  async forward(id: string, auto = false) {
    if (!this.forwardSettings.url) {
      this.dialog = 'forward';
      return;
    }
    const req = await this.#full(id);
    if (!req) return;
    this.forwarding = new Set(this.forwarding).add(id);
    const res = await forward(req, this.forwardSettings);
    const f = new Set(this.forwarding);
    f.delete(id);
    this.forwarding = f;
    this.forwardResults = { ...this.forwardResults, [id]: res };
    if (!res.ok || !auto) {
      const msg = res.error ?? `Forwarded to ${res.url} → ${res.status} ${res.statusText} (${res.ms} ms)`;
      this.toast(msg, res.ok ? 'success' : 'error');
    }
  }

  // --- Accounts -------------------------------------------------------------------

  /** Asks the server who is signed in. Throws when it can't be reached. */
  async loadUser() {
    this.user = await api.me();
    this.userLoaded = true;
  }

  /** Ends this browser's session and starts over from the home page. */
  async signOut() {
    try {
      await api.logout();
    } catch (e) {
      this.toast(`Could not sign out: ${(e as Error).message}`, 'error');
      return;
    }
    location.assign('/');
  }

  #madeHere(id: string) {
    const now = Date.now();
    const made = readSession<Made[]>(MADE_HERE, []).filter((m) => now - m.at < MADE_HERE_FOR);
    writeSession(MADE_HERE, [...made, { id, at: now }].slice(-20));
  }

  // Signed in from this tab: save the URLs it created anonymously in the last
  // hour to the account, with an undo. Other URLs on this browser are saved
  // only when asked (the URL menu), as they may be someone else's.
  async #keepMadeHere() {
    if (!this.user) return;
    const now = Date.now();
    const made = readSession<Made[]>(MADE_HERE, []).filter((m) => now - m.at < MADE_HERE_FOR);
    writeSession(MADE_HERE, []); // once: an undo must not be redone on the next load
    const entries = this.claimable.filter((h) => made.some((m) => m.id === h.id));
    if (!entries.length || this.claiming) return;
    const { saved, why } = await this.#claimEach(entries);
    if (!saved.length) {
      this.toast(`Could not save the URL you made before signing in: ${why}`, 'error');
      return;
    }
    const message =
      saved.length === 1
        ? "The URL you made before signing in is saved to your account: it's private now and won't expire."
        : `The ${saved.length} URLs you made before signing in are saved to your account: they're private now and won't expire.`;
    this.toast(message, 'success', 10_000, { label: 'Undo', run: () => this.#release(saved) });
  }

  /** Saves anonymous URLs from this browser to the signed-in account, one at a time. Returns how many were saved. */
  async claim(entries: HistoryEntry[]): Promise<number> {
    const wanted = entries.filter((h) => h.claim_key);
    if (this.claiming || !wanted.length) return 0;
    const { saved, why } = await this.#claimEach(wanted);
    const n = wanted.length;
    if (saved.length === n) this.toast(n === 1 ? 'URL saved to your account' : `${n} URLs saved to your account`, 'success');
    else if (saved.length) this.toast(`Saved ${saved.length} of ${n} URLs. The rest: ${why}`, 'error');
    else this.toast(`Could not save ${n === 1 ? 'the URL' : 'the URLs'}: ${why}`, 'error');
    return saved.length;
  }

  // Claims each entry in turn: the IDs saved, and why the others were not.
  async #claimEach(wanted: HistoryEntry[]): Promise<{ saved: string[]; why: string }> {
    this.claiming = true;
    const saved: string[] = [];
    let why = '';
    for (const h of wanted) {
      try {
        const t = await api.claimToken(h.id, h.claim_key!);
        if (this.token?.uuid === t.uuid) this.token = t;
        this.#patch(h.id, (e) => entryFor(t, e, e.last_seen)); // in place: not a visit
        saved.push(h.id);
      } catch (e) {
        const status = e instanceof ApiError ? e.status : 0;
        if (gone(e)) {
          this.forget(h.id);
          why ||= 'deleted, expired or already in an account';
        } else if (status === 409 || (status === 403 && /claim key/i.test((e as Error).message))) {
          // Someone's already, or the key went stale with a release elsewhere: no use keeping it.
          this.#patch(h.id, (e) => ({ ...e, claim_key: undefined }));
          why ||= 'deleted, expired or already in an account';
        } else {
          why = (e as Error).message; // the account's limit, or the server can't be reached
          break;
        }
      }
    }
    this.claiming = false;
    return { saved, why };
  }

  // Makes URLs anonymous again: the undo of saving them on sign-in.
  async #release(ids: string[]) {
    let failed = '';
    for (const id of ids) {
      try {
        const t = await api.releaseToken(id);
        if (this.token?.uuid === t.uuid) this.token = t;
        this.#patch(id, (e) => entryFor(t, e, e.last_seen));
      } catch (e) {
        failed ||= (e as Error).message;
      }
    }
    if (failed) this.toast(`Could not make it anonymous again: ${failed}`, 'error');
    else this.toast(ids.length === 1 ? 'The URL is anonymous again' : 'The URLs are anonymous again', 'success');
  }

  #patch(id: string, change: (h: HistoryEntry) => HistoryEntry) {
    this.history = this.history.map((h) => (h.id === id ? change(h) : h));
    write('wh:history', this.history);
  }

  // --- Misc -----------------------------------------------------------------------

  savePrefs() {
    write('wh:prefs', $state.snapshot(this.prefs));
  }

  toast(message: string, kind: Toast['kind'] = 'info', ms = 3200, action?: ToastAction) {
    const id = ++this.#toastSeq;
    this.toasts = [...this.toasts.slice(-3), { id, message, kind, action }];
    setTimeout(() => this.dismiss(id), kind === 'error' ? ms * 2 : ms);
  }

  dismiss(id: number) {
    this.toasts = this.toasts.filter((t) => t.id !== id);
  }
}

export const app = new Store();
