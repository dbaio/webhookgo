<script lang="ts">
  // Abuse reports sent from the /abuse page, one card per reported URL, with
  // the actions that close them: delete the URL, disable its owner, or dismiss.
  import Ban from '@lucide/svelte/icons/ban';
  import Check from '@lucide/svelte/icons/check';
  import ExternalLink from '@lucide/svelte/icons/external-link';
  import Flag from '@lucide/svelte/icons/flag';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Lock from '@lucide/svelte/icons/lock';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import ScanSearch from '@lucide/svelte/icons/scan-search';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import { onMount } from 'svelte';
  import { EMAIL } from '../../lib/account';
  import { adminApi, type ReportGroup, type ReportStatus, type ResolveAction } from '../../lib/admin';
  import { app } from '../../lib/state.svelte';
  import type { Page } from '../../lib/types';
  import FormError from '../FormError.svelte';
  import ConfirmDialog from './ConfirmDialog.svelte';
  import Pager from './Pager.svelte';
  import When from './When.svelte';

  /** Called after reports were closed, so the open count elsewhere follows. */
  let { onchange }: { onchange: () => void } = $props();

  const PER_PAGE = 20;
  const count = new Intl.NumberFormat();

  // --- List ----------------------------------------------------------------------
  let status = $state<ReportStatus | 'all'>('open');
  let res = $state.raw<Page<ReportGroup> | null>(null);
  let loading = $state(false);
  let error = $state('');
  let seq = 0;

  async function load(page = res?.current_page ?? 1) {
    const n = ++seq;
    loading = true;
    error = '';
    try {
      const r = await adminApi.listReports({ page, perPage: PER_PAGE, status });
      if (n !== seq) return;
      // The last URL of the last page was handled: show the page before.
      if (!r.data.length && page > 1 && r.total > 0) return load(Math.ceil(r.total / PER_PAGE));
      res = r;
    } catch (e) {
      if (n === seq) error = `Could not load the reports: ${(e as Error).message}`;
    } finally {
      if (n === seq) loading = false;
    }
  }

  onMount(() => {
    load(1);
  });

  // --- Actions -------------------------------------------------------------------
  const TITLES: Record<ResolveAction, string> = { delete_url: 'Delete URL?', disable_owner: 'Disable owner?', dismiss: 'Dismiss reports?' };
  const ACTIONS: Record<ResolveAction, string> = { delete_url: 'Delete URL', disable_owner: 'Disable owner', dismiss: 'Dismiss' };
  let pending = $state<{ action: ResolveAction; group: ReportGroup } | null>(null);
  let note = $state('');
  let busy = $state(false);
  let actionError = $state('');

  function ask(action: ResolveAction, group: ReportGroup) {
    note = '';
    actionError = '';
    pending = { action, group };
  }

  async function confirm() {
    if (!pending || busy) return;
    const { action, group } = pending;
    busy = true;
    actionError = '';
    try {
      const r = await adminApi.resolveReports(group.token_id, action, note.trim());
      const closed = r.updated === 1 ? '1 report closed' : `${count.format(r.updated)} reports closed`;
      app.toast(
        action === 'delete_url' ? `URL deleted, ${closed}` : action === 'disable_owner' ? `${group.owner?.email} disabled, ${closed}` : closed,
        'success',
      );
      pending = null;
    } catch (e) {
      actionError = (e as Error).message;
    } finally {
      busy = false;
    }
    if (!pending) {
      load();
      onchange();
    }
  }

  // --- Display -------------------------------------------------------------------
  const STATUS: Record<ReportStatus, { label: string; tone: string }> = {
    open: { label: 'Open', tone: 'warn' },
    actioned: { label: 'Actioned', tone: 'ok' },
    dismissed: { label: 'Dismissed', tone: '' },
  };
  /** The URL as it is now: live (and private when owned), or what its record says. */
  function stateOf(g: ReportGroup) {
    if (g.token) return g.owner ? { label: 'Private', tone: '', lock: true } : { label: 'Active', tone: 'ok' };
    if (g.record?.status === 'deleted') return { label: 'Deleted', tone: 'bad' };
    if (g.record?.status === 'expired') return { label: 'Expired', tone: 'warn' };
    return { label: 'Not found', tone: '' };
  }
  const reports = (n: number) => (n === 1 ? '1 report' : `${count.format(n)} reports`);
</script>

<div class="panel">
  <div class="toolbar">
    <label class="filter">
      <span class="sr-only">Status</span>
      <select class="input" bind:value={status} onchange={() => load(1)}>
        <option value="open">Open</option>
        <option value="actioned">Actioned</option>
        <option value="dismissed">Dismissed</option>
        <option value="all">All</option>
      </select>
    </label>
    {#if loading}<LoaderCircle class="spin muted" size={16} />{/if}
    <span class="grow"></span>
    <button type="button" class="btn sm" disabled={loading} onclick={() => load(1)}><RefreshCw /> Refresh</button>
  </div>

  <FormError message={error} />

  {#each res?.data ?? [] as g (g.token_id)}
    {@const st = stateOf(g)}
    {@const t = g.token}
    {@const r = g.record}
    <article class="card group" aria-label={`Reports of ${g.token_id}`}>
      <header class="group-head">
        <span class="chip {st.tone}">{#if st.lock}<Lock />{/if}{st.label}</span>
        <code class="id">{g.token_id}</code>
        {#if t?.description}<span class="desc">{t.description}</span>{/if}
        <span class="grow"></span>
        {#if g.open > 0}
          <span class="chip warn"><Flag /> {g.open === 1 ? '1 open' : `${count.format(g.open)} open`}</span>
        {:else}
          <span class="chip"><Check /> Handled</span>
        {/if}
        <a class="btn ghost icon xs" href={`#urls/${g.token_id}`} title="Details and record" aria-label={`Details of ${g.token_id}`}><ScanSearch /></a>
        {#if t}
          <a
            class="btn ghost icon xs"
            href={`/#/${g.token_id}`}
            target="_blank"
            rel="noopener"
            title="Open in the viewer (logged when it isn't yours)"
            aria-label={`Open ${g.token_id} in the viewer`}><ExternalLink /></a
          >
        {/if}
      </header>

      <dl class="facts">
        <dt>Owner</dt>
        <dd>{#if g.owner}{g.owner.email}{:else}<span class="muted">anonymous</span>{/if}</dd>
        <dt>Created</dt>
        <dd>
          <When iso={t?.created_at ?? r?.created_at} />
          {#if t?.ip || r?.ip}<span class="muted">{' from '}</span><span class="mono">{t?.ip || r?.ip}</span>{/if}
        </dd>
        <dt>Requests</dt>
        <dd>
          {#if r}
            <strong class="num-value">{count.format(r.requests)}</strong> received{#if r.last_request_at}<span class="muted">{', last '}</span><When
                iso={r.last_request_at}
              />{/if}
          {:else if t}
            <strong class="num-value">{count.format(t.request_count)}</strong> stored
          {:else}
            <span class="muted">–</span>
          {/if}
        </dd>
      </dl>

      <ul class="reports" aria-label={reports(g.reports.length)}>
        {#each g.reports as rep (rep.id)}
          {@const s = STATUS[rep.status] ?? { label: rep.status, tone: '' }}
          <li class="report" class:closed={rep.status !== 'open'}>
            <div class="report-head">
              <When iso={rep.created_at} />
              <span class="mono ip">{rep.reporter_ip}</span>
              {#if rep.contact}
                {#if EMAIL.test(rep.contact)}<a class="contact" href={`mailto:${rep.contact}`}>{rep.contact}</a>{:else}<span class="contact">{rep.contact}</span>{/if}
              {/if}
              <span class="grow"></span>
              <span class="chip {s.tone}">{s.label}</span>
            </div>
            {#if rep.message}<p class="message">{rep.message}</p>{:else}<p class="message muted">No message.</p>{/if}
            {#if rep.status !== 'open'}
              <p class="handled">
                {s.label}{#if rep.handled_by_email}{' by '}{rep.handled_by_email}{/if}{#if rep.handled_at}{' '}<When iso={rep.handled_at} />{/if}{#if rep.note}<span
                    class="note">{rep.note}</span
                  >{/if}
              </p>
            {/if}
          </li>
        {/each}
      </ul>

      {#if g.open > 0}
        <footer class="group-actions">
          <button type="button" class="btn sm danger" onclick={() => ask('delete_url', g)}><Trash2 /> Delete URL</button>
          {#if g.owner}
            <button type="button" class="btn sm danger" onclick={() => ask('disable_owner', g)}><Ban /> Disable owner</button>
          {/if}
          <button type="button" class="btn sm" onclick={() => ask('dismiss', g)}><Check /> Dismiss</button>
        </footer>
      {/if}
    </article>
  {:else}
    <div class="card none">
      {#if loading}<LoaderCircle class="spin" size={14} /> Loading…{:else if !error}{status === 'open'
          ? 'No open reports. '
          : 'No reports here. '}{#if app.config.accounts}Visitors report URLs on <a href="/abuse" target="_blank" rel="noopener">/abuse</a>.{/if}{/if}
    </div>
  {/each}

  {#if res && res.total > 0}
    <div class="card pager-card"><Pager page={res} {loading} label="Pages of reported URLs" onpage={load} /></div>
  {/if}
</div>

<ConfirmDialog
  open={!!pending}
  title={pending ? TITLES[pending.action] : ''}
  action={pending ? ACTIONS[pending.action] : ''}
  danger={pending?.action !== 'dismiss'}
  {busy}
  error={actionError}
  onconfirm={confirm}
  onclose={() => (pending = null)}
>
  {#if pending}
    {@const g = pending.group}
    <p>
      {#if g.token?.description}<strong>{g.token.description}</strong>{/if}
      <code class="id">{g.token_id}</code>
    </p>
    {#if pending.action === 'delete_url'}
      <p>
        It stops answering at once (<code>410 Gone</code>), and its stored requests are erased.
        {#if g.owner}It belongs to <strong>{g.owner.email}</strong>.{:else}It is anonymous.{/if}
        It can't be undone.
      </p>
    {:else if pending.action === 'disable_owner'}
      <p>
        <strong>{g.owner?.email}</strong> is signed out everywhere and can't sign in until you enable the account again. Their
        account and URLs are kept, this one included.
      </p>
    {:else}
      <p>Nothing happens to the URL{#if g.owner}{' or to '}<strong>{g.owner.email}</strong>{/if}.</p>
    {/if}
    <p>
      {g.open === 1 ? 'The open report' : `The ${count.format(g.open)} open reports`} of this URL
      {g.open === 1 ? 'is' : 'are'} marked {pending.action === 'dismiss' ? 'dismissed' : 'actioned'}, with your note.
    </p>
    <div class="field">
      <label for="resolve-note">Note <span class="muted">(optional)</span></label>
      <textarea id="resolve-note" class="input text" rows="2" maxlength="2000" bind:value={note}></textarea>
    </div>
  {/if}
</ConfirmDialog>

<style>
  .filter {
    flex: 1 1 160px;
    max-width: 220px;
    min-width: 0;
  }
  .filter .input {
    height: 30px;
    font-size: 13px;
  }
  @media (pointer: coarse) {
    .filter .input {
      height: 44px;
    }
  }
  .group {
    display: grid;
    gap: 12px;
    padding: 14px 16px 16px;
  }
  .group-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 8px;
    min-width: 0;
  }
  .id {
    font-size: 12.5px;
    overflow-wrap: anywhere;
  }
  .desc {
    max-width: 280px;
    overflow: hidden;
    font-weight: 500;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .facts {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: 4px 18px;
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
  .num-value {
    font-variant-numeric: tabular-nums;
  }
  .reports {
    display: grid;
    gap: 8px;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .report {
    display: grid;
    gap: 6px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-left: 3px solid var(--warning);
    border-radius: var(--radius-sm);
    background: var(--bg-sunken);
    font-size: 13px;
  }
  .report.closed {
    border-left-color: var(--border-strong);
  }
  .report-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 10px;
    color: var(--text-2);
    font-size: 12.5px;
  }
  .report-head .chip {
    height: 20px;
    font-size: 11.5px;
  }
  .ip {
    font-size: 12px;
  }
  .contact {
    overflow-wrap: anywhere;
  }
  .message {
    margin: 0;
    overflow-wrap: anywhere;
    white-space: pre-wrap;
  }
  .handled {
    margin: 0;
    color: var(--text-3);
    font-size: 12px;
  }
  .note {
    display: block;
    margin-top: 2px;
    color: var(--text-2);
    overflow-wrap: anywhere;
    white-space: pre-wrap;
  }
  .group-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    padding-top: 12px;
    border-top: 1px solid var(--border);
  }
  .none {
    padding: 28px 16px;
    color: var(--text-3);
    text-align: center;
  }
  .none :global(svg) {
    vertical-align: -2px;
  }
  .pager-card :global(.pager) {
    border-top: 0;
  }
  .text {
    min-height: 60px;
    font-family: inherit;
    font-size: 13px;
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
      margin-top: 6px;
    }
  }
</style>
