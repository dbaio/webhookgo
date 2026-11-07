<script lang="ts">
  // The URL switcher, at the start of the top bar's URL: whether the open URL
  // is private or public (with "Make private"), and every URL of yours, to
  // switch to. Without a URL open, a plain "Your URLs" button. Shortcut: h.
  import Check from '@lucide/svelte/icons/check';
  import ChevronDown from '@lucide/svelte/icons/chevron-down';
  import CloudUpload from '@lucide/svelte/icons/cloud-upload';
  import Globe from '@lucide/svelte/icons/globe';
  import List from '@lucide/svelte/icons/list';
  import Lock from '@lucide/svelte/icons/lock';
  import LogIn from '@lucide/svelte/icons/log-in';
  import X from '@lucide/svelte/icons/x';
  import { ago, duration, shortId } from '../lib/format';
  import { app, type HistoryEntry } from '../lib/state.svelte';
  import AccountUrls from './AccountUrls.svelte';
  import Dropdown from './Dropdown.svelte';
  import Modal from './Modal.svelte';

  // Signed in, the account's own URLs come from the server; this browser's
  // list keeps the rest: anonymous URLs, and other people's that an admin opened.
  const local = $derived(app.user ? app.history.filter((h) => h.owner_id !== app.user?.id) : app.history);
  const others = $derived(app.user ? local.some((h) => h.id !== app.token?.uuid) : app.history.length > 1);
  let version = $state(0); // reloads the account's list after a save

  // Saving takes a URL from anyone else who uses it: ask first.
  let saving = $state<HistoryEntry[] | null>(null);
  const one = $derived(saving?.length === 1);

  async function save() {
    const entries = saving;
    saving = null;
    if (entries && (await app.claim(entries))) version++;
  }

  // The open URL, with accounts on: private or public, and what that means.
  const t = $derived(app.token);
  const status = $derived(
    !t || !app.config.accounts
      ? null
      : t.owner_id
        ? {
            label: 'Private',
            about: `Only ${t.owner_id === app.user?.id ? 'you' : 'its owner'} and the admins can see its requests. It doesn't expire.`,
          }
        : {
            label: 'Public',
            about: `Anyone with the link can see its requests and change it. It expires after ${duration(app.config.token_ttl)} without use.`,
          },
  );
  const entry = $derived(t ? app.history.find((h) => h.id === t.uuid) : undefined);
  const title = $derived(status ? `${status.label}: ${status.about} Your URLs (h)` : 'Your URLs (h)');
</script>

<Dropdown id="url-menu" class={t ? 'btn url-menu' : 'btn ghost sm url-menu-plain'} align="start" {title} width={app.user ? 360 : 340}>
  {#snippet button()}
    {#if !t}<List /> <span class="label">Your URLs</span>
    {:else if status}{#if t.owner_id}<Lock />{:else}<Globe />{/if} <span class="label">{status.label}</span>
    {:else}<List /> <span class="label">URLs</span>{/if}
    <ChevronDown />
  {/snippet}
  {#snippet children(close)}
    {#if t && status}
      <div class="url-status">
        <span class="url-status-head">{#if t.owner_id}<Lock />{:else}<Globe />{/if} This URL is {status.label.toLowerCase()}</span>
        <p>{status.about}</p>
        {#if !t.owner_id}
          {#if app.user && entry?.claim_key}
            <button type="button" class="btn primary sm" disabled={app.claiming} onclick={() => ((saving = [entry]), close())}><Lock /> Make private</button>
          {:else if app.user}
            <p class="note">Only the browser that created it can make it private.</p>
          {:else if app.userLoaded}
            <a class="btn sm" href={`/login?next=${t.uuid}`}><LogIn /> Sign in to make it private</a>
          {/if}
        {/if}
      </div>
      <div class="menu-sep"></div>
    {/if}
    {#if app.user}
      <AccountUrls {close} {version} />
      <div class="menu-sep"></div>
      <div class="label-row">
        <div class="menu-label">On this browser</div>
        {#if app.claimable.length > 1}
          <button type="button" class="btn ghost xs" disabled={app.claiming} title="Save every URL below to your account" onclick={() => ((saving = app.claimable), close())}>
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
              {#if h.owner_id}<span class="lock" title="Private: only its owner and the admins can see it"><Lock /></span>
              {:else if app.config.accounts}<span class="lock" title="Public: anyone with the link can see it"><Globe /></span>{/if}
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
            onclick={() => ((saving = [h]), close())}><CloudUpload /></button
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
    {#if app.user}
      <div class="menu-sep"></div>
      <a class="menu-item" href="/account#account-urls" onclick={close}><List /> <span class="grow">See all your URLs</span></a>
    {:else if app.config.accounts && app.userLoaded}
      <div class="menu-sep"></div>
      <p class="note pad">Sign in to keep your URLs on any browser, private.</p>
    {/if}
  {/snippet}
</Dropdown>

<Modal
  open={!!saving}
  title={one ? 'Save this URL to your account?' : `Save these ${saving?.length ?? 0} URLs to your account?`}
  onclose={() => (saving = null)}
>
  <p class="confirm">
    {one ? "It becomes private and won't expire." : "They become private and won't expire."}
    Only for URLs you created: anyone else using {one ? 'it' : 'them'} can still send requests to {one ? 'it' : 'them'}, but can no longer
    see them or change {one ? 'it' : 'them'}.
  </p>
  {#snippet footer()}
    <span class="spacer"></span>
    <button type="button" class="btn" onclick={() => (saving = null)}>Cancel</button>
    <button type="button" class="btn primary" disabled={app.claiming} onclick={save}><CloudUpload /> Save</button>
  {/snippet}
</Modal>

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
  /* No hover on touch: the remove button stays, and the text makes room for it. */
  @media (hover: none) {
    .remove {
      opacity: 1;
    }
    .row:has(.remove) .menu-item {
      padding-right: 44px;
    }
    .claimable:has(.remove) .menu-item {
      padding-right: 80px;
    }
    .claimable .remove {
      right: 42px;
    }
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
  .confirm {
    font-size: 13.5px;
    line-height: 1.5;
  }
  .url-status {
    display: grid;
    justify-items: start;
    gap: 6px;
    padding: 8px 10px 10px;
    font-size: 12.5px;
  }
  .url-status-head {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-weight: 600;
  }
  .url-status-head :global(svg) {
    width: 14px;
    height: 14px;
  }
  .url-status p {
    color: var(--text-2);
    line-height: 1.45;
  }
  .note {
    color: var(--text-3);
    font-size: 12px;
  }
  .note.pad {
    padding: 4px 10px 6px;
  }
  /* The trigger, inside the top bar's URL: a status chip that opens the menu. */
  :global(.btn.url-menu) {
    flex: none;
    gap: 4px;
    height: 24px;
    padding: 0 5px 0 7px;
    border-radius: 6px;
    background: var(--bg-elev);
    color: var(--text-2);
    font-size: 12px;
    font-weight: 600;
  }
  :global(.btn.url-menu svg) {
    width: 12px;
    height: 12px;
  }
  /* Tablets share the top bar's one row; phones have one for the URL. */
  @media (min-width: 641px) and (max-width: 899px), (max-width: 350px) {
    :global(.btn.url-menu .label) {
      display: none;
    }
  }
  .spacer {
    flex: 1;
  }
</style>
