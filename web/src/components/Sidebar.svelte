<script lang="ts">
  import Search from '@lucide/svelte/icons/search';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import X from '@lucide/svelte/icons/x';
  import CheckCheck from '@lucide/svelte/icons/check-check';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import { app } from '../lib/state.svelte';
  import { duration } from '../lib/format';
  import RequestItem from './RequestItem.svelte';

  const METHODS = ['', 'GET', 'POST', 'PUT', 'PATCH', 'DELETE'];
  let confirming = $state(false);
  let confirmTimer: ReturnType<typeof setTimeout> | undefined;

  function clearAll() {
    if (!confirming) {
      confirming = true;
      clearTimeout(confirmTimer);
      confirmTimer = setTimeout(() => (confirming = false), 3000);
      return;
    }
    confirming = false;
    app.clearRequests();
  }

  // Load the next page when the end of the list scrolls into view.
  function sentinel(node: HTMLElement) {
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) app.loadMore();
    });
    io.observe(node);
    return { destroy: () => io.disconnect() };
  }

  const liveLabel = $derived({ live: 'Live', polling: 'Live (polling)', connecting: 'Connecting…', offline: 'Offline' }[app.live]);
</script>

<aside class="sidebar">
  <div class="head">
    <div class="title">
      <h2>Requests</h2>
      <span class="count">
        {#if app.filtering}{app.matched} of {app.total}{:else}{app.total}{/if}
      </span>
    </div>
    <div class="tools">
      {#if app.unread.size}
        <button type="button" class="btn ghost icon sm" title="Mark all as read" aria-label="Mark all as read" onclick={() => app.markAllRead()}>
          <CheckCheck />
        </button>
      {/if}
      <button
        type="button"
        class="btn sm"
        class:ghost={!confirming}
        class:icon={!confirming}
        class:confirm={confirming}
        title="Delete all requests"
        aria-label="Delete all requests"
        disabled={!app.total}
        onclick={clearAll}
      >
        <Trash2 />{#if confirming}<span>Delete all?</span>{/if}
      </button>
    </div>
  </div>

  <div class="filters">
    <label class="search">
      <Search size={15} />
      <input
        id="search"
        class="input"
        type="search"
        placeholder="Search body, headers, IP…"
        autocomplete="off"
        spellcheck="false"
        value={app.search}
        oninput={(e) => app.setSearch(e.currentTarget.value)}
        onkeydown={(e) => {
          if (e.key === 'Escape') {
            app.setSearch('');
            e.currentTarget.blur();
          }
        }}
      />
      {#if app.search}
        <button type="button" class="btn ghost icon xs clear" aria-label="Clear search" onclick={() => app.setSearch('')}><X /></button>
      {:else}
        <kbd class="hint-key">/</kbd>
      {/if}
    </label>
    <div class="methods" role="radiogroup" aria-label="Filter by method">
      {#each METHODS as m (m)}
        <button type="button" role="radio" aria-checked={app.method === m} class:on={app.method === m} onclick={() => app.setMethod(m)}>
          {m || 'All'}
        </button>
      {/each}
    </div>
  </div>

  <div class="list">
    {#each app.requests as r (r.uuid)}
      <RequestItem {r} selected={r.uuid === app.selectedId} unread={app.unread.has(r.uuid)} />
    {/each}
    {#if app.hasMore}
      <div class="more" use:sentinel>
        <LoaderCircle size={16} class="spin" /> Loading more…
      </div>
    {/if}
    {#if !app.requests.length && !app.listLoading}
      <p class="none">
        {#if app.filtering}No requests match these filters.{:else}No requests yet.{/if}
      </p>
    {/if}
  </div>

  <footer>
    <span class="live"><span class="live-dot {app.live}"></span>{liveLabel}</span>
    <span
      class="muted"
      title={app.token?.expires_at === null
        ? "Older requests are dropped beyond this limit, and all of them after this long without new ones; the private URL itself doesn't expire"
        : 'Older requests are dropped beyond this limit; the URL expires after this long without use'}
    >
      Last {app.config.max_requests} · {duration(app.config.token_ttl)}
    </span>
  </footer>
</aside>

<style>
  .sidebar {
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
    height: 100%;
    background: var(--bg-elev);
  }
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 12px 12px 6px 16px;
  }
  .title {
    display: flex;
    align-items: baseline;
    gap: 8px;
  }
  h2 {
    font-size: 14px;
    font-weight: 650;
  }
  .count {
    color: var(--text-3);
    font-size: 12.5px;
    font-variant-numeric: tabular-nums;
  }
  .tools {
    display: flex;
    gap: 4px;
  }
  .filters {
    display: grid;
    gap: 8px;
    padding: 6px 12px 10px;
    border-bottom: 1px solid var(--border);
  }
  .search {
    position: relative;
    display: flex;
    align-items: center;
  }
  .search > :global(svg) {
    position: absolute;
    left: 10px;
    color: var(--text-3);
    pointer-events: none;
  }
  .search .input {
    height: 34px;
    padding-left: 32px;
    padding-right: 34px;
    background: var(--bg-sunken);
  }
  .search .input:focus {
    background: var(--bg-elev);
  }
  .search input::-webkit-search-cancel-button {
    display: none;
  }
  .clear,
  .hint-key {
    position: absolute;
    right: 6px;
  }
  .hint-key {
    right: 8px;
    pointer-events: none;
  }
  @media (hover: none) {
    .hint-key {
      display: none;
    }
  }
  .methods {
    display: flex;
    gap: 4px;
    overflow-x: auto;
    scrollbar-width: none;
  }
  .methods button {
    flex: none;
    height: 24px;
    padding: 0 9px;
    border: 1px solid var(--border);
    border-radius: 999px;
    background: transparent;
    color: var(--text-2);
    font: 600 11px var(--mono);
    cursor: pointer;
  }
  .methods button:hover {
    border-color: var(--border-strong);
    color: var(--text);
  }
  .methods button.on {
    border-color: transparent;
    background: var(--text);
    color: var(--bg-elev);
  }
  @media (pointer: coarse) {
    .search .input {
      height: 44px;
    }
    .methods button {
      height: 34px;
      padding: 0 12px;
    }
  }
  .list {
    flex: 1;
    min-height: 0;
    padding: 8px 0;
    overflow-y: auto;
    overscroll-behavior: contain;
  }
  .more {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    padding: 14px;
    color: var(--text-3);
    font-size: 12.5px;
  }
  .none {
    padding: 32px 16px;
    color: var(--text-3);
    font-size: 13px;
    text-align: center;
  }
  footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 9px 16px;
    border-top: 1px solid var(--border);
    font-size: 12px;
    padding-bottom: max(9px, env(safe-area-inset-bottom));
  }
  .live {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    color: var(--text-2);
    font-weight: 500;
  }
  /* A phone on its side: the height goes to the requests. */
  @media (max-height: 480px) {
    .filters {
      grid-template-columns: repeat(2, minmax(0, 1fr));
      align-items: center;
    }
    .head {
      padding-top: 4px;
      padding-bottom: 0;
    }
    footer {
      display: none;
    }
  }
</style>
