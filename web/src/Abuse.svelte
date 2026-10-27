<script lang="ts">
  import Eraser from '@lucide/svelte/icons/eraser';
  import ExternalLink from '@lucide/svelte/icons/external-link';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Search from '@lucide/svelte/icons/search';
  import ShieldAlert from '@lucide/svelte/icons/shield-alert';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import Webhook from '@lucide/svelte/icons/webhook';
  import { onMount } from 'svelte';
  import { api, ApiError } from './lib/api';
  import { ago, bytes, dateTime, duration } from './lib/format';
  import { app } from './lib/state.svelte';
  import type { Token } from './lib/types';
  import Toasts from './components/Toasts.svelte';

  interface Row {
    id: string;
    state: 'waiting' | 'loading' | 'live' | 'deleted' | 'missing' | 'error';
    token?: Token;
    first?: string; // oldest stored request
    last?: string; // newest stored request
    deletedAt?: string;
    error?: string;
    confirm?: boolean;
    busy?: boolean;
  }

  const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi;
  const CONCURRENCY = 3; // a few lookups at a time stay well within the API rate limit

  let text = $state('');
  let rows = $state<Row[]>([]);
  let now = $state(Date.now());
  let run = 0; // bumped by every lookup and clear, so stale answers are dropped

  const ids = $derived([...new Set((text.match(UUID) ?? []).map((s) => s.toLowerCase()))]);
  const contact = $derived(app.config.abuse_contact.trim());
  const contactHref = $derived(
    /^(https?|mailto):/i.test(contact) ? contact : /^[^\s@/:]+@[^\s@/:]+$/.test(contact) ? `mailto:${contact}` : '',
  );

  onMount(() => {
    api.config().then((c) => (app.config = c), () => {});
    const timer = setInterval(() => (now = Date.now()), 30_000);
    return () => clearInterval(timer);
  });

  $effect(() => {
    document.title = `Report abuse · ${app.config.app_name}`;
  });

  async function lookup(row: Row, seq: number) {
    row.state = 'loading';
    try {
      const token = await api.getToken(row.id);
      const [oldest, newest] = await Promise.all([
        api.listRequests(row.id, { perPage: 1, sorting: 'oldest' }),
        api.listRequests(row.id, { perPage: 1, sorting: 'newest' }),
      ]);
      if (seq !== run) return;
      row.token = token;
      row.first = oldest.data[0]?.created_at;
      row.last = newest.data[0]?.created_at;
      row.state = 'live';
    } catch (e) {
      if (seq !== run) return;
      if (e instanceof ApiError && e.status === 410) {
        row.state = 'deleted';
        row.deletedAt = (e.data as { deleted_at?: string } | undefined)?.deleted_at;
      } else if (e instanceof ApiError && e.status === 404) {
        row.state = 'missing';
      } else {
        row.state = 'error';
        row.error = (e as Error).message;
      }
    }
  }

  async function lookupAll() {
    const seq = ++run;
    rows = ids.map((id) => ({ id, state: 'waiting' }));
    let next = 0;
    const worker = async () => {
      while (seq === run && next < rows.length) await lookup(rows[next++], seq);
    };
    await Promise.all(Array.from({ length: CONCURRENCY }, worker));
  }

  function clear() {
    run++;
    text = '';
    rows = [];
    document.getElementById('abuse-urls')?.focus();
  }

  async function remove(row: Row) {
    if (!row.confirm) {
      row.confirm = true;
      return;
    }
    row.busy = true;
    try {
      await api.deleteToken(row.id);
      row.state = 'deleted';
      row.deletedAt = new Date().toISOString();
      app.forget(row.id);
      app.toast('URL deleted', 'success');
    } catch (e) {
      app.toast(`Could not delete the URL: ${(e as Error).message}`, 'error');
    } finally {
      row.busy = false;
      row.confirm = false;
    }
  }

  function onkeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && ids.length) {
      e.preventDefault();
      lookupAll();
    }
  }

  const servesHtml = (t: Token) => /html|xml|svg/i.test(t.default_content_type) && t.default_content.trim() !== '';

  const short = new Intl.DateTimeFormat(undefined, { dateStyle: 'short', timeStyle: 'short' });
  const shortUTC = new Intl.DateTimeFormat(undefined, { dateStyle: 'short', timeStyle: 'short', timeZone: 'UTC' });
  const compact = (iso: string) => (app.prefs.utc ? `${shortUTC.format(new Date(iso))} UTC` : short.format(new Date(iso)));
</script>

{#snippet when(iso: string | undefined, label = '')}
  {#if iso}
    <span class="when" title={`${dateTime(iso, app.prefs.utc)} (${ago(iso, now)})`}>{label}{compact(iso)}</span>
  {:else if !label}
    <span class="muted">–</span>
  {/if}
{/snippet}

<div class="page">
  <header class="bar">
    <a class="brand" href="/" title={`Open ${app.config.app_name}`}>
      <span class="logo"><Webhook size={18} /></span>
      <span class="name">{app.config.app_name}</span>
    </a>
  </header>

  <main>
    <section class="intro">
      <h1><ShieldAlert size={22} /> Report abuse</h1>
      <p class="lead">
        Found a {app.config.app_name} URL used for phishing, malware or spam? Look it up here and delete it.
      </p>
      <p>
        Anyone who has a URL can delete it. It stops answering at once (<code>410 Gone</code>), and the requests it
        captured, which may hold victims' data, are erased.
      </p>
      {#if app.config.retention > 0}
        <p>
          A record of each URL is kept for {duration(app.config.retention)} after it expires or is deleted, to help
          investigations: when and from which IP address it was created and deleted, and how many requests it got.
          Captured requests are not part of it.
        </p>
      {/if}
      {#if contact}
        <p>
          Something else to report, or need these records for an investigation? Contact
          {#if contactHref}<a href={contactHref}>{contact}</a>{:else}{contact}{/if}.
        </p>
      {/if}
    </section>

    <section class="card form">
      <label class="label" for="abuse-urls">URLs or IDs</label>
      <textarea
        id="abuse-urls"
        class="input"
        rows="6"
        spellcheck="false"
        placeholder={`${app.config.public_url || location.origin}/00000000-0000-0000-0000-000000000000`}
        bind:value={text}
        {onkeydown}
      ></textarea>
      <div class="actions">
        <span class="hint grow">
          {ids.length ? `${ids.length} unique ID${ids.length === 1 ? '' : 's'} found` : 'Paste URLs, IDs or a whole message: every ID in it is picked out.'}
        </span>
        <button type="button" class="btn sm" disabled={!text && !rows.length} onclick={clear}><Eraser /> Clear</button>
        <button type="button" class="btn primary sm" disabled={!ids.length} title="Look up (Ctrl+Enter)" onclick={lookupAll}>
          <Search /> Look up
        </button>
      </div>
    </section>

    {#if rows.length}
      <section class="card results">
        <table>
          <thead>
            <tr>
              <th>URL</th>
              <th>Status</th>
              <th>Created</th>
              <th>Created from</th>
              <th>Requests</th>
              <th>Response</th>
            </tr>
          </thead>
          <tbody>
            {#each rows as row (row.id)}
              <tr class:gone={row.state === 'deleted'}>
                <td class="id">
                  <code>{row.id}</code>
                  {#if row.token?.description}<small>{row.token.description}</small>{/if}
                  {#if row.state === 'live'}
                    <div class="row-actions">
                      <a class="btn ghost xs" href={`/#/${row.id}`} target="_blank" rel="noopener" title="Open its requests in a new tab">
                        <ExternalLink /> View
                      </a>
                      <button
                        type="button"
                        class="btn xs"
                        class:danger={!row.confirm}
                        class:confirm={row.confirm}
                        disabled={row.busy}
                        onclick={() => remove(row)}
                        onblur={() => (row.confirm = false)}
                      >
                        <Trash2 /> {row.confirm ? 'Click again to delete' : 'Delete'}
                      </button>
                    </div>
                  {/if}
                </td>
                {#if row.state === 'live' && row.token}
                  {@const t = row.token}
                  <td><span class="chip ok">Active</span></td>
                  <td>{@render when(t.created_at)}<small>{ago(t.created_at, now)}</small></td>
                  <td>
                    <span class="mono">{t.ip || '–'}</span>
                    {#if t.user_agent}<small class="ua" title={t.user_agent}>{t.user_agent}</small>{/if}
                  </td>
                  <td>
                    <strong class="count">{t.request_count}</strong>
                    <small>{@render when(row.first, 'first ')}</small>
                    <small>{@render when(row.last, 'last ')}</small>
                  </td>
                  <td>
                    <span class="mono">{t.default_status}</span> · {t.default_content_type || 'text/plain'}
                    <small>
                      {bytes(new TextEncoder().encode(t.default_content).length)}
                      {#if servesHtml(t)}<span class="chip warn flag">serves a page</span>{/if}
                    </small>
                  </td>
                {:else if row.state === 'deleted'}
                  <td><span class="chip bad">Deleted</span></td>
                  <td colspan="4">
                    {#if row.deletedAt}{@render when(row.deletedAt, 'on ')} <span class="muted">({ago(row.deletedAt, now)})</span>{:else}<span class="muted">This URL was deleted.</span>{/if}
                  </td>
                {:else if row.state === 'missing'}
                  <td><span class="chip">Not found</span></td>
                  <td colspan="4" class="muted">It never existed, or it expired after {duration(app.config.token_ttl)} without use.</td>
                {:else if row.state === 'error'}
                  <td><span class="chip warn">Error</span></td>
                  <td colspan="4" class="muted">{row.error}</td>
                {:else}
                  <td colspan="5" class="muted">
                    {#if row.state === 'loading'}<LoaderCircle class="spin" size={14} /> Looking up…{:else}Waiting…{/if}
                  </td>
                {/if}
              </tr>
            {/each}
          </tbody>
        </table>
      </section>
    {/if}
  </main>
</div>

<Toasts />

<style>
  .page {
    height: 100%;
    overflow-y: auto;
  }
  .bar {
    display: flex;
    align-items: center;
    height: var(--topbar-h);
    padding: 0 14px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-elev);
  }
  .brand {
    display: inline-flex;
    align-items: center;
    gap: 9px;
    color: var(--text);
    text-decoration: none !important;
  }
  .logo {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    border-radius: 9px;
    background: linear-gradient(140deg, var(--accent), color-mix(in srgb, var(--accent) 60%, #6d5dfc));
    color: #fff;
  }
  .name {
    font-size: 15px;
    font-weight: 700;
    letter-spacing: -0.015em;
  }
  main {
    display: grid;
    gap: 18px;
    max-width: 1180px;
    margin: 0 auto;
    padding: 28px 16px 48px;
  }
  .intro {
    display: grid;
    gap: 10px;
    max-width: 720px;
    color: var(--text-2);
  }
  .intro h1 {
    display: flex;
    align-items: center;
    gap: 9px;
    color: var(--text);
    font-size: 22px;
    letter-spacing: -0.015em;
  }
  .intro h1 :global(svg) {
    color: var(--warning);
  }
  .lead {
    color: var(--text);
    font-size: 15px;
  }
  code {
    font: 12.5px var(--mono);
  }
  .form {
    display: grid;
    gap: 8px;
    max-width: 720px;
    padding: 16px;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
  }
  .grow {
    flex: 1;
    min-width: 200px;
  }
  .results {
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
  }
  th,
  td {
    padding: 10px 12px;
    border-bottom: 1px solid var(--border);
    text-align: left;
    vertical-align: top;
  }
  th {
    color: var(--text-3);
    font-size: 12px;
    font-weight: 600;
    white-space: nowrap;
  }
  tbody tr:last-child td {
    border-bottom: 0;
  }
  td small {
    display: block;
    color: var(--text-3);
    font-size: 11.5px;
  }
  .when {
    white-space: nowrap;
  }
  .count {
    font-variant-numeric: tabular-nums;
  }
  .id code {
    white-space: nowrap;
  }
  .ua {
    max-width: 220px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .gone .id code {
    color: var(--text-3);
    text-decoration: line-through;
  }
  .row-actions {
    display: flex;
    gap: 4px;
    margin-top: 6px;
  }
  td :global(.spin) {
    vertical-align: -2px;
  }
  .flag {
    height: 18px;
    margin-left: 4px;
    font-size: 11px;
  }
</style>
