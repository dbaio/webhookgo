<script lang="ts">
  import ExternalLink from '@lucide/svelte/icons/external-link';
  import Globe from '@lucide/svelte/icons/globe';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Lock from '@lucide/svelte/icons/lock';
  import Network from '@lucide/svelte/icons/network';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import ScanSearch from '@lucide/svelte/icons/scan-search';
  import Search from '@lucide/svelte/icons/search';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import X from '@lucide/svelte/icons/x';
  import { onMount, untrack } from 'svelte';
  import { adminApi, findUUID, type AdminUrl, type CreatorStats, type Owner, type UrlKind, type UrlLookup, type UrlSort } from '../../lib/admin';
  import { ApiError } from '../../lib/api';
  import { duration } from '../../lib/format';
  import { app } from '../../lib/state.svelte';
  import type { Page } from '../../lib/types';
  import FormError from '../FormError.svelte';
  import ConfirmDialog from './ConfirmDialog.svelte';
  import Pager from './Pager.svelte';
  import When from './When.svelte';

  interface Props {
    /** The ID looked up (from the address: #urls/<id>). */
    lookup: string;
    /** Only this user's URLs in the list. */
    owner: Owner | null;
  }
  let { lookup, owner = $bindable() }: Props = $props();

  // Private URLs in the list, or anonymous ones: #urls/private, #urls/public.
  // A lookup (#urls/<id>) keeps the list as it is.
  const kindIn = (hash: string) => ({ '#urls/public': 'anonymous', '#urls/private': 'owned' })[hash] as UrlKind | undefined;
  let kind = $state<UrlKind>(kindIn(location.hash) ?? 'owned');
  onMount(() => {
    const follow = () => (kind = kindIn(location.hash) ?? kind);
    addEventListener('hashchange', follow);
    return () => removeEventListener('hashchange', follow);
  });

  const PER_PAGE = 25;
  const count = new Intl.NumberFormat();
  const show = (id: string) => (location.hash = `#urls/${id}`);

  // --- Lookup --------------------------------------------------------------------
  let text = $state('');
  let found = $state.raw<UrlLookup | null>(null);
  let foundId = $state('');
  let finding = $state(false);
  let findError = $state('');
  let findSeq = 0;

  async function find(id: string) {
    const n = ++findSeq;
    finding = true;
    findError = '';
    try {
      const r = await adminApi.lookupUrl(id);
      if (n !== findSeq) return;
      found = r;
      foundId = id;
    } catch (e) {
      if (n !== findSeq) return;
      if (e instanceof ApiError && e.status === 404) {
        found = { token: null, owner: null, record: null };
        foundId = id;
      } else {
        found = null;
        findError = `Could not look it up: ${(e as Error).message}`;
      }
    } finally {
      if (n === findSeq) finding = false;
    }
  }

  // Follow the address: a lookup from the list, the log or the back button.
  $effect(() => {
    const id = lookup;
    untrack(() => {
      if (id) {
        if (findUUID(text) !== id) text = id;
        find(id);
        // From a row far down the list: bring the result into view.
        document.getElementById('lookup')?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
      } else {
        findSeq++;
        found = null;
        foundId = '';
        finding = false;
      }
    });
  });

  function submit(e: SubmitEvent) {
    e.preventDefault();
    findError = '';
    const id = findUUID(text);
    if (!id) {
      findError = text.trim() ? 'No URL ID in that text. Paste a webhook URL, a viewer link or a bare ID.' : 'Paste a URL or an ID to look up.';
      return;
    }
    if (id === lookup) find(id);
    else show(id);
  }

  function clearLookup() {
    text = '';
    findError = '';
    location.hash = '#urls';
    document.getElementById('lookup-text')?.focus();
  }

  // --- List ----------------------------------------------------------------------
  let q = $state('');
  let res = $state.raw<Page<AdminUrl> | null>(null);
  let loading = $state(false);
  let error = $state('');
  let seq = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;

  async function load(page = res?.current_page ?? 1) {
    const n = ++seq;
    loading = true;
    error = '';
    try {
      const pub = kind === 'anonymous';
      const r = await adminApi.listUrls({
        page,
        perPage: PER_PAGE,
        q: q.trim(),
        kind,
        owner: pub ? '' : owner?.id,
        pages: pub && pagesOnly,
        sort: pub ? sort : 'newest',
      });
      if (n !== seq) return;
      if (!r.data.length && page > 1 && r.total > 0) return load(Math.ceil(r.total / PER_PAGE));
      res = r;
    } catch (e) {
      if (n === seq) error = `Could not load URLs: ${(e as Error).message}`;
    } finally {
      if (n === seq) loading = false;
    }
  }

  onMount(() => () => clearTimeout(timer));

  // Reload from the first page when the owner filter or the kind changes
  // (and at first).
  $effect(() => {
    void owner?.id;
    void kind;
    untrack(() => load(1));
  });

  function search() {
    clearTimeout(timer);
    timer = setTimeout(() => load(1), 250);
  }

  function showKind(k: UrlKind) {
    if (k === 'anonymous') owner = null;
    location.hash = k === 'anonymous' ? '#urls/public' : '#urls/private';
  }

  // Every anonymous URL created from one address.
  function searchIP(ip: string) {
    clearTimeout(timer);
    q = ip;
    load(1);
  }
  const anonymous = $derived(kind === 'anonymous');
  const noun = $derived(anonymous ? 'public URLs' : 'private URLs');

  // --- Abuse signals (public URLs) -------------------------------------------------
  // Each of these reads every public URL on the server.
  let pagesOnly = $state(false);
  let sort = $state<UrlSort>('newest');
  let showIps = $state(false);
  let ips = $state.raw<{ data: CreatorStats[]; urls: number } | null>(null);
  let ipsError = $state('');

  async function loadIps() {
    ips = null;
    ipsError = '';
    try {
      ips = await adminApi.topIps();
    } catch (e) {
      ipsError = `Could not count them: ${(e as Error).message}`;
    }
  }

  function toggleIps() {
    showIps = !showIps;
    if (showIps) loadIps();
  }
  const PAGE_HINT = 'It answers with its own HTML, XML or SVG page: the usual sign of phishing';

  // --- Delete --------------------------------------------------------------------
  let doomed = $state<{ id: string; description: string; owner: Owner | null; requests: number } | null>(null);
  let deleting = $state(false);
  let deleteError = $state('');

  function askDelete(d: NonNullable<typeof doomed>) {
    deleteError = '';
    doomed = d;
  }

  async function remove() {
    if (!doomed || deleting) return;
    const id = doomed.id;
    deleting = true;
    deleteError = '';
    try {
      await adminApi.deleteUrl(id);
      app.toast('URL deleted', 'success');
      doomed = null;
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        app.toast('That URL was already gone', 'info');
        doomed = null;
      } else {
        deleteError = (e as Error).message;
      }
    } finally {
      deleting = false;
    }
    if (!doomed) {
      if (foundId === id) find(id);
      load();
    }
  }

  // --- Lookup result -------------------------------------------------------------
  const STATUS: Record<string, { label: string; tone: string }> = {
    live: { label: 'Active', tone: 'ok' },
    deleted: { label: 'Deleted', tone: 'bad' },
    expired: { label: 'Expired', tone: 'warn' },
  };
  const verdict = $derived.by(() => {
    if (!found) return null;
    if (found.token) return STATUS.live;
    if (found.record) return STATUS[found.record.status] ?? { label: found.record.status, tone: '' };
    return { label: 'Not found', tone: '' };
  });
  const plural = (n: number, one: string) => `${count.format(n)} ${one}${n === 1 ? '' : 's'}`;
</script>

{#snippet ownerCell(o: Owner | null)}
  {#if o}
    <button type="button" class="btn ghost xs owner" title={`Show only ${o.email}'s URLs`} onclick={() => (owner = o)}>{o.email}</button>
  {:else}
    <span class="muted">Anonymous</span>
  {/if}
{/snippet}

<div class="panel">
  <section id="lookup" class="card lookup" aria-labelledby="lookup-title">
    <form class="lookup-form" novalidate onsubmit={submit}>
      <label id="lookup-title" class="label" for="lookup-text">Look up any URL</label>
      <div class="lookup-row">
        <input
          id="lookup-text"
          class="input mono"
          spellcheck="false"
          autocomplete="off"
          placeholder={`${app.config.public_url || location.origin}/00000000-0000-0000-0000-000000000000`}
          aria-describedby="lookup-hint"
          bind:value={text}
        />
        <button type="submit" class="btn primary sm" disabled={finding}>
          {#if finding}<LoaderCircle class="spin" />{:else}<Search />{/if} Look up
        </button>
        {#if foundId || text}
          <button type="button" class="btn ghost icon sm" title="Clear" aria-label="Clear the lookup" onclick={clearLookup}><X /></button>
        {/if}
      </div>
      <span id="lookup-hint" class="hint">
        A webhook URL, a viewer link or a bare ID, anonymous or private. Deleted and expired URLs show their record{app.config.retention > 0
          ? `, kept for ${duration(app.config.retention)}`
          : ''}.
      </span>
    </form>

    <FormError message={findError} />

    {#if found && verdict}
      {@const t = found.token}
      {@const r = found.record}
      {@const o = found.owner}
      <div class="result" aria-live="polite">
        <div class="result-head">
          <span class="chip {verdict.tone}">{verdict.label}</span>
          {#if t && o}<span class="chip"><Lock /> Private</span>{/if}
          <code class="id">{foundId}</code>
          <span class="grow"></span>
          {#if t}
            <a
              class="btn sm"
              href={`/#/${foundId}`}
              target="_blank"
              rel="noopener"
              title={!o || o.id !== app.user?.id ? "Open its requests in the viewer. Opening a URL that isn't yours is logged." : 'Open its requests in the viewer'}
            >
              <ExternalLink /> Open
            </a>
            <button
              type="button"
              class="btn sm danger"
              onclick={() => askDelete({ id: foundId, description: t.description, owner: o, requests: t.request_count })}
            >
              <Trash2 /> Delete
            </button>
          {/if}
        </div>

        {#if !t && !r}
          <p class="muted">No URL and no record with this ID. It never existed, or its record is gone too.</p>
        {:else}
          <dl class="facts">
            {#if t}
              <dt>Name</dt>
              <dd>{#if t.description}{t.description}{:else}<span class="muted">–</span>{/if}</dd>
              <dt>Owner</dt>
              <dd>{@render ownerCell(o)}</dd>
            {/if}
            <dt>Created</dt>
            <dd>
              <When iso={t?.created_at ?? r?.created_at} />
              {#if t?.ip || r?.ip}<span class="muted">{' from '}</span><span class="mono">{t?.ip || r?.ip}</span>{/if}
              {#if t?.user_agent || r?.user_agent}<small class="ua">{t?.user_agent || r?.user_agent}</small>{/if}
            </dd>
            <dt>Requests</dt>
            <dd>
              {#if r}
                <strong class="num-value">{count.format(r.requests)}</strong> received
                {#if t}<span class="muted">· {plural(t.request_count, 'request')} stored</span>{/if}
              {:else if t}
                <strong class="num-value">{count.format(t.request_count)}</strong> stored
              {/if}
              {#if r?.first_request_at || r?.last_request_at}
                <small>
                  first <When iso={r.first_request_at} /> · last <When iso={r.last_request_at} />
                </small>
              {/if}
            </dd>
            {#if t}
              <dt>Expires</dt>
              <dd>
                {#if t.expires_at}<When iso={t.expires_at} />{:else}<span class="muted">Never: it belongs to an account</span>{/if}
              </dd>
            {/if}
            {#if r?.deleted_at}
              <dt>Deleted</dt>
              <dd>
                <When iso={r.deleted_at} />
                {#if r.deleted_ip}<span class="muted">{' from '}</span><span class="mono">{r.deleted_ip}</span>{/if}
                {#if r.deleted_user_agent}<small class="ua">{r.deleted_user_agent}</small>{/if}
              </dd>
            {/if}
            {#if r?.kept_until}
              <dt>Record kept until</dt>
              <dd><When iso={r.kept_until} /></dd>
            {/if}
            {#if !r}
              <dt>Record</dt>
              <dd class="muted">None kept{app.config.retention > 0 ? '' : ': this server keeps no records (RETENTION is 0)'}.</dd>
            {/if}
          </dl>
        {/if}
      </div>
    {/if}
  </section>

  <div class="toolbar">
    <div class="seg" role="group" aria-label="Which URLs">
      <button type="button" class:on={!anonymous} aria-pressed={!anonymous} onclick={() => showKind('owned')}><Lock /> Private</button>
      <button type="button" class:on={anonymous} aria-pressed={anonymous} onclick={() => showKind('anonymous')}><Globe /> Public</button>
    </div>
    <label class="search">
      <Search />
      <input class="input" type="search" placeholder="Search by name, exact ID or IP" aria-label={`Search ${noun}`} bind:value={q} oninput={search} />
    </label>
    {#if anonymous}
      <button
        type="button"
        class="chip toggle"
        class:on={pagesOnly}
        aria-pressed={pagesOnly}
        title="Only the URLs that answer with their own HTML, XML or SVG page: the usual sign of phishing"
        onclick={() => ((pagesOnly = !pagesOnly), load(1))}><TriangleAlert /> Serve a page</button
      >
      <select class="input sort" aria-label="Order" bind:value={sort} onchange={() => load(1)}>
        <option value="newest">Newest first</option>
        <option value="requests">Most requests first</option>
      </select>
      <button type="button" class="btn sm" aria-expanded={showIps} aria-controls="top-ips" onclick={toggleIps}><Network /> Top IPs</button>
    {/if}
    {#if owner && !anonymous}
      <button type="button" class="chip filter" title="Show every owner's URLs" onclick={() => (owner = null)}>
        Owner: {owner.email} <X />
      </button>
    {/if}
    {#if loading}<LoaderCircle class="spin muted" size={16} />{/if}
  </div>

  <FormError message={error} />

  {#if anonymous && showIps}
    <section id="top-ips" class="card table-card ips" aria-labelledby="top-ips-title">
      <div class="ips-head">
        <h3 id="top-ips-title">Addresses that created the most public URLs</h3>
        <span class="muted">{ips ? `${count.format(ips.urls)} live URLs read` : ''}</span>
        <span class="grow"></span>
        <button type="button" class="btn ghost icon xs" title="Count again" aria-label="Count again" onclick={loadIps}><RefreshCw /></button>
        <button type="button" class="btn ghost icon xs" title="Close" aria-label="Close" onclick={() => (showIps = false)}><X /></button>
      </div>
      {#if ipsError}
        <FormError message={ipsError} />
      {:else if !ips}
        <p class="ips-note"><LoaderCircle class="spin" size={14} /> Reading every public URL…</p>
      {:else if !ips.data.length}
        <p class="ips-note">No public URLs.</p>
      {:else}
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Address</th>
                <th class="num">URLs</th>
                <th class="num">Serve a page</th>
                <th class="num">Requests</th>
                <th>Latest created</th>
              </tr>
            </thead>
            <tbody>
              {#each ips.data as c (c.ip)}
                <tr>
                  <td>
                    {#if c.ip}
                      <button type="button" class="btn ghost xs owner mono" title={`Show every public URL created from ${c.ip}`} onclick={() => searchIP(c.ip)}
                        >{c.ip}</button
                      >
                    {:else}<span class="muted">Unknown</span>{/if}
                  </td>
                  <td class="num">{count.format(c.urls)}</td>
                  <td class="num">{#if c.pages}<span class="chip warn flag" title={PAGE_HINT}>{count.format(c.pages)}</span>{:else}0{/if}</td>
                  <td class="num">{count.format(c.requests)}</td>
                  <td><When iso={c.latest_created_at} /></td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </section>
  {/if}

  <div class="card table-card">
    <div class="table-wrap">
      <table>
        <caption class="sr-only">
          {anonymous ? 'Public URLs' : 'Private URLs'}{owner && !anonymous ? ` of ${owner.email}` : ''}{anonymous && pagesOnly
            ? ' that serve a page'
            : ''}, {anonymous && sort === 'requests' ? 'most requests first' : 'newest first'}
        </caption>
        <thead>
          <tr>
            <th>URL</th>
            <th>{anonymous ? 'Created from' : 'Owner'}</th>
            <th class="num">Requests</th>
            <th>Last request</th>
            {#if anonymous}<th>Expires</th>{/if}
            <th>Created</th>
            <th><span class="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          {#each res?.data ?? [] as u (u.uuid)}
            <tr class:on={u.uuid === foundId}>
              <td class="url">
                <span class="title"
                  >{#if u.serves_page}<span class="chip warn flag" title={PAGE_HINT}>serves a page</span>{/if}{#if u.description}{u.description}{:else}<span
                      class="muted">Untitled</span
                    >{/if}</span
                >
                <code>{u.uuid}</code>
              </td>
              <td>
                {#if anonymous}
                  {#if u.ip}
                    <button type="button" class="btn ghost xs owner mono" title={`Show every public URL created from ${u.ip}`} onclick={() => searchIP(u.ip)}
                      >{u.ip}</button
                    >
                  {:else}<span class="muted">–</span>{/if}
                {:else}
                  {@render ownerCell(u.owner)}
                {/if}
              </td>
              <td class="num">{count.format(u.request_count)}</td>
              <td><When iso={u.latest_request_at} empty="None yet" /></td>
              {#if anonymous}<td><When iso={u.expires_at} /></td>{/if}
              <td><When iso={u.created_at} /></td>
              <td class="actions">
                <div class="row-actions">
                  <a class="btn ghost icon xs" href={`#urls/${u.uuid}`} title="Details and record" aria-label={`Details of ${u.uuid}`}><ScanSearch /></a>
                  <a
                    class="btn ghost icon xs"
                    href={`/#/${u.uuid}`}
                    target="_blank"
                    rel="noopener"
                    title="Open in the viewer (logged when it isn't yours)"
                    aria-label={`Open ${u.uuid} in the viewer`}><ExternalLink /></a
                  >
                  <button
                    type="button"
                    class="btn ghost icon xs danger"
                    title="Delete"
                    aria-label={`Delete ${u.uuid}`}
                    onclick={() => askDelete({ id: u.uuid, description: u.description, owner: u.owner, requests: u.request_count })}
                  >
                    <Trash2 />
                  </button>
                </div>
              </td>
            </tr>
          {:else}
            <tr>
              <td colspan={anonymous ? 7 : 6} class="none">
                {#if loading}<LoaderCircle class="spin" size={14} /> Loading…{:else if !error}{q.trim() || (owner && !anonymous) || (anonymous && pagesOnly)
                    ? `No ${noun} match.`
                    : `No ${noun} yet.`}{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <Pager page={res} {loading} label={`Pages of ${noun}`} onpage={load} />
  </div>
</div>

<ConfirmDialog
  open={!!doomed}
  title="Delete URL?"
  action="Delete URL"
  danger
  busy={deleting}
  error={deleteError}
  onconfirm={remove}
  onclose={() => (doomed = null)}
>
  {#if doomed}
    {@const d = doomed}
    <p>
      {#if d.description}<strong>{d.description}</strong>{/if}
      <code class="id">{d.id}</code>
    </p>
    <p>
      It stops answering at once (<code>{d.owner ? '404' : '410 Gone'}</code>){d.requests === 1
        ? ', and its stored request is erased'
        : d.requests > 1
          ? `, and its ${count.format(d.requests)} stored requests are erased`
          : ''}.
      {#if d.owner}It belongs to <strong>{d.owner.email}</strong>.{:else}It is anonymous.{/if}
      It can't be undone.
    </p>
  {/if}
</ConfirmDialog>

<style>
  .lookup {
    display: grid;
    gap: 12px;
    padding: 16px;
  }
  .lookup-form {
    display: grid;
    gap: 6px;
  }
  .lookup-row {
    display: flex;
    gap: 6px;
  }
  .lookup-row .input {
    flex: 1;
    min-width: 0;
  }
  .lookup-row .btn {
    height: 34px;
  }
  .lookup-row .btn.icon {
    width: 34px;
  }
  @media (pointer: coarse) {
    .lookup-row .btn {
      height: 44px;
    }
    .lookup-row .btn.icon {
      width: 44px;
    }
  }
  .result {
    display: grid;
    gap: 12px;
    padding-top: 14px;
    border-top: 1px solid var(--border);
  }
  .result-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
  }
  .id {
    overflow-wrap: anywhere;
  }
  .facts {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: 8px 18px;
    margin: 0;
    font-size: 13px;
  }
  .facts dt {
    color: var(--text-3);
    font-weight: 500;
  }
  .facts dd {
    min-width: 0;
    margin: 0;
    overflow-wrap: anywhere;
  }
  .facts small {
    display: block;
    color: var(--text-3);
    font-size: 12px;
  }
  .ua {
    overflow-wrap: anywhere;
  }
  .num-value {
    font-variant-numeric: tabular-nums;
  }
  .owner {
    max-width: 260px;
    padding: 0 4px;
    margin: -2px -4px;
    overflow: hidden;
    color: var(--accent-strong);
    font-weight: 400;
    text-overflow: ellipsis;
  }
  .filter {
    height: 28px;
    max-width: 100%;
    padding: 0 6px 0 10px;
    border-color: transparent;
    background: var(--accent-soft);
    color: var(--accent-strong);
    font-size: 12.5px;
    cursor: pointer;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .filter :global(svg) {
    width: 14px;
    height: 14px;
  }
  .url .title {
    display: block;
    max-width: 280px;
    overflow: hidden;
    font-weight: 500;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .url code {
    color: var(--text-3);
    font-size: 11.5px;
    white-space: nowrap;
  }
  tr.on td {
    background: var(--bg-active);
  }
  .flag {
    height: 18px;
    margin-right: 6px;
    font-size: 11px;
    vertical-align: 1px;
  }
  .toggle {
    height: 28px;
    padding: 0 10px;
    font-size: 12.5px;
    cursor: pointer;
  }
  .toggle :global(svg) {
    width: 13px;
    height: 13px;
  }
  .toggle.on {
    border-color: transparent;
    background: var(--warning-soft);
    color: var(--warning);
  }
  .sort {
    width: auto;
    height: 28px;
    padding-left: 8px;
    font-size: 12.5px;
  }
  .ips-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 10px;
    padding: 12px 14px;
    border-bottom: 1px solid var(--border);
  }
  .ips-head h3 {
    font-size: 13.5px;
    font-weight: 600;
  }
  .ips-head .muted {
    font-size: 12px;
  }
  .ips-note {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 12px 14px;
    color: var(--text-3);
    font-size: 13px;
  }
  .row-actions {
    display: flex;
    justify-content: flex-end;
    gap: 2px;
  }
  .grow {
    flex: 1;
  }
  @media (max-width: 560px) {
    .facts {
      grid-template-columns: minmax(0, 1fr);
      gap: 2px;
    }
    .facts dd + dt {
      margin-top: 8px;
    }
  }
</style>
