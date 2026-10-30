<script lang="ts">
  import CircleCheck from '@lucide/svelte/icons/circle-check';
  import Eraser from '@lucide/svelte/icons/eraser';
  import ExternalLink from '@lucide/svelte/icons/external-link';
  import Flag from '@lucide/svelte/icons/flag';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Lock from '@lucide/svelte/icons/lock';
  import Search from '@lucide/svelte/icons/search';
  import Send from '@lucide/svelte/icons/send';
  import ShieldAlert from '@lucide/svelte/icons/shield-alert';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import Webhook from '@lucide/svelte/icons/webhook';
  import { onMount } from 'svelte';
  import { api, ApiError } from './lib/api';
  import { ago, bytes, dateTime, duration } from './lib/format';
  import { app } from './lib/state.svelte';
  import type { Token } from './lib/types';
  import FormError from './components/FormError.svelte';
  import Toasts from './components/Toasts.svelte';

  interface Row {
    id: string;
    /** `private`: it belongs to an account, so only the admins can act on it (accounts on). */
    state: 'waiting' | 'loading' | 'live' | 'private' | 'deleted' | 'missing' | 'error';
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
      } else if (e instanceof ApiError && e.status === 403) {
        row.state = 'private';
      } else {
        row.state = 'error';
        row.error = (e as Error).message;
      }
    }
  }

  async function lookupAll() {
    const seq = ++run;
    sent = false;
    retry = '';
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

  // --- Report to the administrators (accounts on) ----------------------------------
  // Private URLs can only be acted on by the admins; live anonymous ones can
  // be reported too, by anyone who would rather not delete them alone.
  let message = $state('');
  let email = $state('');
  let sending = $state(false);
  let sent = $state(false);
  let retry = $state('');
  let reported = $state(new Set<string>());

  const reportable = $derived(rows.filter((r) => r.state === 'private' || r.state === 'live').map((r) => r.id));
  const pending = $derived(reportable.filter((id) => !reported.has(id)));
  const urls = (n: number) => `${n} URL${n === 1 ? '' : 's'}`;

  function focusReport() {
    const el = document.getElementById('report-message');
    el?.scrollIntoView({ block: 'center', behavior: 'smooth' });
    el?.focus({ preventScroll: true });
  }

  async function sendReport() {
    if (sending || !pending.length) return;
    const ids = pending;
    sending = true;
    sent = false;
    retry = '';
    try {
      await api.reportAbuse(ids, message.trim(), email.trim());
      reported = new Set([...reported, ...ids]);
      sent = true;
    } catch (e) {
      if (e instanceof ApiError && e.status === 429) {
        const secs = Number(/(\d+)s$/.exec(e.message)?.[1] ?? 0);
        retry = `Too many reports from your address. Try again in ${secs >= 120 ? `${Math.ceil(secs / 60)} minutes` : secs ? `${secs} seconds` : 'a while'}.`;
      } else {
        retry = `Could not send the report: ${(e as Error).message}`;
      }
    } finally {
      sending = false;
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
        {#if app.config.accounts}
          URLs that belong to accounts are private: report them below and an administrator will review them.
        {/if}
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
                  {:else if row.state === 'private'}
                    <div class="row-actions">
                      <button type="button" class="btn xs" title="Report it to the administrators, below" onclick={focusReport}>
                        <Flag /> Report
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
                {:else if row.state === 'private'}
                  <td><span class="chip"><Lock /> Private</span></td>
                  <td colspan="4" class="muted">
                    Private URL. It belongs to an account on this server, so only its administrators can act on it.
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

    {#if app.config.accounts && reportable.length}
      <section id="abuse-report" class="card form report" aria-labelledby="report-title">
        <h2 id="report-title"><Flag size={16} /> Report to the administrators</h2>
        <p class="hint">
          This sends the {urls(reportable.length)} above that are active or private. The administrators see your IP
          address with the report.
        </p>
        <label class="label" for="report-message">What is wrong with these URLs?</label>
        <textarea
          id="report-message"
          class="input text"
          rows="4"
          maxlength="2000"
          placeholder="Optional, but it helps: what do they serve or collect, and where did you find them?"
          bind:value={message}
        ></textarea>
        <label class="label" for="report-contact">Your email, if you want an answer</label>
        <input id="report-contact" class="input" type="email" maxlength="254" autocomplete="email" spellcheck="false" bind:value={email} />
        {#if sent}
          <p class="thanks" role="status">
            <CircleCheck size={16} />
            <span>Thank you. The administrators will review {reported.size === 1 ? 'this URL' : 'these URLs'}{email.trim() ? ' and may write to you' : ''}.</span>
          </p>
        {/if}
        <FormError message={retry} />
        <div class="actions">
          <span class="hint grow">{pending.length ? 'Reporting a URL does not delete it.' : 'Every URL above has been reported.'}</span>
          <button type="button" class="btn primary sm" disabled={sending || !pending.length} onclick={sendReport}>
            {#if sending}<LoaderCircle class="spin" />{:else}<Send />{/if}
            {pending.length || !reported.size ? `Report ${urls(pending.length)}` : 'Reported'}
          </button>
        </div>
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
  .report h2 {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 15px;
    letter-spacing: -0.01em;
  }
  .report h2 :global(svg) {
    color: var(--warning);
  }
  .report .label {
    margin-top: 4px;
  }
  .report .text {
    min-height: 84px;
    font-family: inherit;
    font-size: 13px;
  }
  .thanks {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 9px 12px;
    border-radius: var(--radius-sm);
    background: var(--success-soft);
    color: var(--success);
    font-size: 13px;
  }
  .thanks :global(svg) {
    flex: none;
    margin-top: 1px;
  }
</style>
