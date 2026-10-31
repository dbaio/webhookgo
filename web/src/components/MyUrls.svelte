<script lang="ts">
  // The "Your URLs" section of the account page: every URL the user owns,
  // newest first, with a search on their names, more on demand, and a
  // confirmation to delete one.
  import ExternalLink from '@lucide/svelte/icons/external-link';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Search from '@lucide/svelte/icons/search';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import { onMount, untrack } from 'svelte';
  import { api, ApiError } from '../lib/api';
  import { ago } from '../lib/format';
  import { app } from '../lib/state.svelte';
  import type { TokenItem } from '../lib/types';
  import CopyButton from './CopyButton.svelte';
  import FormError from './FormError.svelte';
  import Modal from './Modal.svelte';

  const PER_PAGE = 20;
  const SEARCH_FROM = 5; // URLs before the search box shows
  const day = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' });
  const count = new Intl.NumberFormat();
  const requests = (n: number) => `${count.format(n)} request${n === 1 ? '' : 's'}`;

  let q = $state('');
  let items = $state.raw<TokenItem[]>([]);
  let total = $state(0);
  let all = $state(0); // the total without a search
  let more = $state(false);
  let loading = $state(true);
  let error = $state('');
  let seq = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;

  async function load(reset: boolean) {
    const n = ++seq;
    // Derive the page from what is loaded, as the history menu does.
    const page = reset ? 1 : Math.floor(items.length / PER_PAGE) + 1;
    loading = true;
    error = '';
    try {
      const res = await api.listTokens({ page, perPage: PER_PAGE, q: q.trim() });
      if (n !== seq) return;
      const seen = new Set(reset ? [] : items.map((t) => t.uuid));
      items = [...(reset ? [] : items), ...res.data.filter((t) => !seen.has(t.uuid))];
      total = res.total;
      if (!q.trim()) all = total;
      more = !res.is_last_page;
    } catch (e) {
      if (n === seq) error = (e as Error).message;
    } finally {
      if (n === seq) loading = false;
    }
  }

  $effect(() => {
    untrack(() => load(true));
  });
  onMount(() => () => clearTimeout(timer));

  function search() {
    clearTimeout(timer);
    timer = setTimeout(() => load(true), 220);
  }

  // --- Delete ------------------------------------------------------------------
  let doomed = $state<TokenItem | null>(null);
  let deleteError = $state('');
  let deleting = $state(false);

  async function remove() {
    const t = doomed;
    if (!t || deleting) return;
    deleteError = '';
    deleting = true;
    try {
      await api.deleteToken(t.uuid);
      app.toast('URL deleted', 'success');
    } catch (e) {
      if (!(e instanceof ApiError && (e.status === 404 || e.status === 410))) {
        deleteError = (e as Error).message;
        return;
      }
      app.toast('That URL was already gone', 'info');
    } finally {
      deleting = false;
    }
    app.forget(t.uuid);
    items = items.filter((x) => x.uuid !== t.uuid);
    total = Math.max(0, total - 1);
    all = Math.max(0, all - 1);
    doomed = null;
  }
</script>

<section class="card" aria-labelledby="account-urls">
  <div class="row">
    <div class="grow">
      <h2 id="account-urls">Your URLs</h2>
      <p class="hint">
        Private: only you and the admins can open them. They don't expire; their requests do, as on any URL. Also in the history menu, on
        any browser.
      </p>
    </div>
  </div>

  {#if all > SEARCH_FROM}
    <label class="search">
      <Search />
      <input class="input" type="search" placeholder="Search by name" aria-label="Search your URLs" bind:value={q} oninput={search} />
    </label>
  {/if}

  {#if items.length}
    <ul class="urls" aria-busy={loading}>
      {#each items as t (t.uuid)}
        <li>
          <span class="grow">
            <a class="name" href={`/#/${t.uuid}`}>{#if t.description}{t.description}{:else}<span class="untitled">Untitled</span>{/if}</a>
            <span class="sub mono">{t.uuid}</span>
            <span class="sub">
              {requests(t.request_count)}{t.latest_request_at ? ` · last ${ago(t.latest_request_at, app.now)}` : ''} · created {day.format(
                new Date(t.created_at),
              )}
            </span>
          </span>
          <span class="actions">
            <CopyButton text={t.url} title="Copy the URL" toast="URL copied" />
            <a class="btn ghost icon xs" href={`/#/${t.uuid}`} title="Open its requests" aria-label={`Open ${t.uuid}`}><ExternalLink /></a>
            <button
              type="button"
              class="btn ghost icon xs danger"
              title="Delete"
              aria-label={`Delete ${t.uuid}`}
              onclick={() => ((deleteError = ''), (doomed = t))}><Trash2 /></button
            >
          </span>
        </li>
      {/each}
    </ul>
  {:else if loading}
    <p class="none"><LoaderCircle class="spin" size={14} /> Loading…</p>
  {:else if !error}
    <p class="none">{q.trim() ? 'No URLs match.' : 'No URLs yet. The ones you create while signed in are kept here.'}</p>
  {/if}

  {#if error}
    <p class="none bad">
      Could not load your URLs: {error}
      <button type="button" class="btn ghost xs" onclick={() => load(!items.length)}>Try again</button>
    </p>
  {:else if more}
    <button type="button" class="btn ghost sm more" disabled={loading} onclick={() => load(false)}>
      {#if loading}<LoaderCircle class="spin" />{/if} Load more <span class="muted">({count.format(items.length)} of {count.format(total)})</span>
    </button>
  {/if}
</section>

<Modal open={!!doomed} title="Delete this URL?" onclose={() => (doomed = null)}>
  {#if doomed}
    <p class="confirm-text">
      {#if doomed.description}<strong>{doomed.description}</strong> ({/if}<span class="mono">{doomed.uuid}</span>{#if doomed.description}){/if}
      stops answering at once (<code>404</code>){doomed.request_count
        ? `, and ${doomed.request_count === 1 ? 'its stored request is' : `its ${count.format(doomed.request_count)} stored requests are`} erased`
        : ''}. This can't be undone.
    </p>
    <FormError message={deleteError} />
  {/if}
  {#snippet footer()}
    <span class="grow"></span>
    <button type="button" class="btn" onclick={() => (doomed = null)}>Cancel</button>
    <button type="button" class="btn danger solid" disabled={deleting} onclick={remove}>
      {#if deleting}<LoaderCircle class="spin" />{/if} Delete
    </button>
  {/snippet}
</Modal>

<style>
  section {
    display: grid;
    gap: 14px;
    padding: 18px 20px 20px;
  }
  h2 {
    font-size: 15px;
    font-weight: 650;
    letter-spacing: -0.01em;
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 10px 16px;
  }
  .row .hint {
    margin-top: 4px;
  }
  .grow {
    flex: 1;
    min-width: 220px;
  }
  code {
    font-family: var(--mono);
    font-size: 0.92em;
  }
  .search {
    position: relative;
    display: block;
  }
  .search :global(svg) {
    position: absolute;
    top: 9px;
    left: 10px;
    width: 15px;
    height: 15px;
    color: var(--text-3);
    pointer-events: none;
  }
  .search .input {
    padding-left: 32px;
  }
  .urls {
    display: grid;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .urls li {
    display: flex;
    align-items: center;
    gap: 8px 12px;
    padding: 12px 0;
    border-top: 1px solid var(--border);
  }
  .urls li .grow {
    display: grid;
    gap: 2px;
    min-width: 0;
  }
  .name {
    overflow: hidden;
    color: var(--text);
    font-weight: 600;
    text-decoration: none;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .name:hover {
    color: var(--accent-strong);
  }
  .untitled {
    color: var(--text-3);
    font-weight: 500;
  }
  .sub {
    overflow: hidden;
    color: var(--text-3);
    font-size: 12px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .actions {
    display: flex;
    flex: none;
    gap: 2px;
  }
  .none {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    color: var(--text-3);
    font-size: 13px;
  }
  .bad {
    color: var(--danger);
  }
  .more {
    justify-self: center;
  }
  .muted {
    color: var(--text-3);
    font-weight: 400;
  }
  .confirm-text {
    font-size: 13.5px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }
  @media (max-width: 480px) {
    section {
      padding: 16px;
    }
  }
</style>
