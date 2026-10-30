<script lang="ts">
  import Check from '@lucide/svelte/icons/check';
  import CloudUpload from '@lucide/svelte/icons/cloud-upload';
  import History from '@lucide/svelte/icons/history';
  import Lock from '@lucide/svelte/icons/lock';
  import X from '@lucide/svelte/icons/x';
  import { ago, shortId } from '../lib/format';
  import { app, type HistoryEntry } from '../lib/state.svelte';
  import AccountUrls from './AccountUrls.svelte';
  import Dropdown from './Dropdown.svelte';

  // Signed in, the account's own URLs come from the server; this browser's
  // list keeps the rest: anonymous URLs, and other people's that an admin opened.
  const local = $derived(app.user ? app.history.filter((h) => h.owner_id !== app.user?.id) : app.history);
  const others = $derived(app.user ? local.some((h) => h.id !== app.token?.uuid) : app.history.length > 1);
  let version = $state(0); // reloads the account's list after a save

  async function save(entries: HistoryEntry[]) {
    if (await app.claim(entries)) version++;
  }
</script>

<Dropdown class="btn ghost icon sm" title="Your URLs" width={app.user ? 340 : 320}>
  {#snippet button()}<History />{/snippet}
  {#snippet children(close)}
    {#if app.user}
      <AccountUrls {close} {version} />
      <div class="menu-sep"></div>
      <div class="label-row">
        <div class="menu-label">On this browser</div>
        {#if app.claimable.length > 1}
          <button type="button" class="btn ghost xs" disabled={app.claiming} title="Save every URL below to your account" onclick={() => save(app.claimable)}>
            <CloudUpload /> Save all
          </button>
        {/if}
      </div>
    {:else}
      <div class="menu-label">Your URLs</div>
    {/if}
    {#each local as h (h.id)}
      {@const current = h.id === app.token?.uuid}
      {@const claimable = !!app.user && !!h.claim_key && !h.owner_id}
      <div class="row" class:claimable>
        <button
          type="button"
          class="menu-item"
          onclick={() => {
            close();
            if (!current) app.go(h.id);
          }}
        >
          <span class="grow">
            <span class="title">
              {#if h.owner_id}<span class="lock" title="Private: only its owner and the admins can see it"><Lock /></span>{/if}
              {h.description || shortId(h.id)}
            </span>
            <span class="sub mono">{h.id}</span>
            <span class="sub">created {ago(h.created_at, app.now)}</span>
          </span>
          {#if current}<Check />{/if}
        </button>
        {#if claimable}
          <button
            type="button"
            class="btn ghost icon xs save"
            disabled={app.claiming}
            title="Save to my account"
            aria-label="Save to my account"
            onclick={() => save([h])}><CloudUpload /></button
          >
        {/if}
        {#if !current}
          <button type="button" class="btn ghost icon xs remove" title="Remove from history" aria-label="Remove from history" onclick={() => app.forget(h.id)}><X /></button>
        {/if}
      </div>
    {:else}
      <p class="none">{app.user ? 'No other URLs on this browser.' : 'No URLs yet.'}</p>
    {/each}
    {#if others}
      <div class="menu-sep"></div>
      <button type="button" class="menu-item" onclick={() => app.clearHistory()}><X /> Clear other URLs from this browser</button>
    {/if}
  {/snippet}
</Dropdown>

<style>
  .row {
    position: relative;
  }
  .grow {
    display: grid;
    gap: 1px;
  }
  .title {
    font-weight: 600;
  }
  .lock :global(svg) {
    width: 12px;
    height: 12px;
    margin-right: 2px;
    vertical-align: -1px;
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
  .remove,
  .save {
    position: absolute;
    top: 8px;
    right: 6px;
  }
  .remove {
    opacity: 0;
  }
  .row:hover .remove,
  .remove:focus-visible {
    opacity: 1;
  }
  /* Saving stays in view; removing shows on hover, beside it. */
  .claimable .menu-item {
    padding-right: 36px;
  }
  .claimable .remove {
    right: 32px;
  }
  .save {
    color: var(--accent-strong);
  }
  .label-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding-right: 4px;
  }
  .label-row .btn {
    margin-top: 4px;
  }
  .none {
    padding: 8px 10px;
    color: var(--text-3);
    font-size: 13px;
  }
</style>
