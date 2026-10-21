import { api, ApiError, isUUID } from './api';
import { bytes } from './format';
import { defaultForward, forward, type ForwardResult, type ForwardSettings } from './forward';
import { read, write } from './storage';
import type { AppConfig, FullRequest, LiveEvent, Summary, Token, TokenInput } from './types';

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

export interface HistoryEntry {
  id: string;
  description: string;
  created_at: string;
  last_seen: string;
}

export interface Toast {
  id: number;
  message: string;
  kind: 'info' | 'success' | 'error';
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

class Store {
  config = $state<AppConfig>({
    app_name: 'webhookgo',
    version: '',
    public_url: '',
    max_requests: 500,
    max_body_size: 2 << 20,
    max_delay: 30,
    token_ttl: 7 * 86400,
  });
  prefs = $state<Prefs>({ ...defaultPrefs, ...read<Partial<Prefs>>('wh:prefs', {}) });
  history = $state<HistoryEntry[]>(read<HistoryEntry[]>('wh:history', []));
  toasts = $state<Toast[]>([]);
  now = $state(Date.now());
  mobile = $state(typeof matchMedia !== 'undefined' && matchMedia(MOBILE).matches);
  dialog = $state<Dialog>(null);

  token = $state<Token | null>(null);
  status = $state<'loading' | 'ready' | 'missing' | 'error'>('loading');
  error = $state('');
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

  async init() {
    setInterval(() => (this.now = Date.now()), 5_000);
    const mq = matchMedia(MOBILE);
    mq.addEventListener('change', () => (this.mobile = mq.matches));
    try {
      this.config = await api.config();
    } catch {
      /* keep defaults */
    }
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
      await this.createToken();
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
      if (e instanceof ApiError && e.status === 404) {
        this.forget(id);
        if (quiet) return false;
        this.token = null;
        this.status = 'missing';
        return false;
      }
      if (quiet) return false;
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
    try {
      const t = await api.createToken(input);
      this.#remember(t);
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
    else await this.createToken();
  }

  #remember(t: Token) {
    const now = new Date().toISOString();
    const entry: HistoryEntry = { id: t.uuid, description: t.description, created_at: t.created_at, last_seen: now };
    this.history = [entry, ...this.history.filter((h) => h.id !== t.uuid)].slice(0, 30);
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
      if (e instanceof ApiError && e.status === 404) {
        this.status = 'missing';
        this.token = null;
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
      if (e instanceof ApiError && e.status === 404) {
        this.#disconnect();
        this.status = 'missing';
        this.token = null;
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

  // --- Misc -----------------------------------------------------------------------

  savePrefs() {
    write('wh:prefs', $state.snapshot(this.prefs));
  }

  toast(message: string, kind: Toast['kind'] = 'info', ms = 3200) {
    const id = ++this.#toastSeq;
    this.toasts = [...this.toasts.slice(-3), { id, message, kind }];
    setTimeout(() => this.dismiss(id), kind === 'error' ? ms * 2 : ms);
  }

  dismiss(id: number) {
    this.toasts = this.toasts.filter((t) => t.id !== id);
  }
}

export const app = new Store();
