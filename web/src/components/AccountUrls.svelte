<script lang="ts">
  // The signed-in user's URLs in the URL menu, from the server: newest
  // first, a search on their names and more on demand. It loads each time the
  // menu opens, and again when `version` changes.
  import Check from '@lucide/svelte/icons/check';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Search from '@lucide/svelte/icons/search';
  import { untrack } from 'svelte';
  import { api } from '../lib/api';
  import { ago, shortId } from '../lib/format';
  import { app } from '../lib/state.svelte';
  import type { TokenItem } from '../lib/types';

  let { close, version }: { close: () => void; version: number } = $props();

  const PER_PAGE = 20;
  let q = $state('');
  let items = $state.raw<TokenItem[]>([]);
  let more = $state(false);
  let loading = $state(false);
  let error = $state('');
  let seq = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;

  async function load(reset: boolean) {
    const n = ++seq;
    // Derive the page from what is loaded, as the request list does.
    const page = reset ? 1 : Math.floor(items.length / PER_PAGE) + 1;
    loading = true;
    error = '';
    try {
      const res = await api.listTokens({ page, perPage: PER_PAGE, q: q.trim() });
      if (n !== seq) return;
      const seen = new Set(reset ? [] : items.map((t) => t.uuid));
      items = [...(reset ? [] : items), ...res.data.filter((t) => !seen.has(t.uuid))];
      more = !res.is_last_page;
    } catch (e) {
      if (n === seq) error = (e as Error).message;
    } finally {
      if (n === seq) loading = false;
    }
  }

  $effect(() => {
    void version;
    untrack(() => load(true));
  });

  function search() {
    clearTimeout(timer);
    timer = setTimeout(() => load(true), 220);
  }
</script>

<div class="menu-label">Your URLs</div>
<label class="search">
  <Search />
  <input class="input" type="search" placeholder="Search by name" aria-label="Search your URLs" bind:value={q} oninput={search} />
</label>

{#each items as t (t.uuid)}
  {@const current = t.uuid === app.token?.uuid}
  <button
    type="button"
    class="menu-item"
    onclick={() => {
      close();
      if (!current) app.go(t.uuid);
    }}
  >
    <span class="grow">
      <span class="title">{t.description || shortId(t.uuid)}</span>
      <span class="sub mono">{t.uuid}</span>
      <span class="sub">
        {t.latest_request_at ? `last request ${ago(t.latest_request_at, app.now)}` : `created ${ago(t.created_at, app.now)}, no requests yet`}
      </span>
    </span>
    {#if current}<Check />{/if}
  </button>
{:else}
  {#if loading}
    <p class="none"><LoaderCircle class="spin" size={14} /> Loading…</p>
  {:else if !error}
    <p class="none">{q.trim() ? 'No URLs match.' : 'No URLs yet. The ones you create are kept here, on any browser.'}</p>
  {/if}
{/each}

{#if error}
  <p class="none bad">
    Could not load your URLs: {error}
    <button type="button" class="btn ghost xs" onclick={() => load(!items.length)}>Try again</button>
  </p>
{:else if more}
  <button type="button" class="menu-item more" disabled={loading} onclick={() => load(false)}>
    {#if loading}<LoaderCircle class="spin" />{/if} Load more
  </button>
{/if}

<style>
  .search {
    position: relative;
    display: block;
    margin: 2px 4px 4px;
  }
  .search :global(svg) {
    position: absolute;
    top: 8px;
    left: 9px;
    width: 14px;
    height: 14px;
    color: var(--text-3);
    pointer-events: none;
  }
  .search .input {
    height: 30px;
    padding-left: 29px;
    font-size: 13px;
  }
  @media (pointer: coarse) {
    .search :global(svg) {
      top: 15px;
    }
    .search .input {
      height: 44px;
    }
  }
  .grow {
    display: grid;
    gap: 1px;
  }
  .title {
    font-weight: 600;
  }
  .sub {
    overflow: hidden;
    color: var(--text-3);
    font-size: 11.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .sub.mono {
    font-size: 11px;
  }
  .none {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    padding: 8px 10px;
    color: var(--text-3);
    font-size: 13px;
  }
  .bad {
    color: var(--danger);
  }
  .more {
    justify-content: center;
    color: var(--accent-strong);
    font-weight: 500;
  }
</style>
