<script lang="ts">
  // The "API keys" section of the account page: the user's keys, a dialog
  // that makes one and shows it this once, and a confirmation to revoke.
  import KeyRound from '@lucide/svelte/icons/key-round';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Plus from '@lucide/svelte/icons/plus';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import { tick, untrack } from 'svelte';
  import { NAME_MAX } from '../lib/account';
  import { api } from '../lib/api';
  import { ago } from '../lib/format';
  import { app } from '../lib/state.svelte';
  import type { ApiKey, NewApiKey } from '../lib/types';
  import CopyButton from './CopyButton.svelte';
  import FormError from './FormError.svelte';
  import Modal from './Modal.svelte';

  const day = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' });
  // The dialog is shown by an effect of the Modal: focus once that has run,
  // or the browser's default focus (the close button) wins.
  const autofocus = (node: HTMLElement) => {
    tick().then(() => node.focus());
  };

  let keys = $state.raw<ApiKey[]>([]);
  let loading = $state(true);
  let error = $state('');

  async function load() {
    loading = true;
    error = '';
    try {
      keys = await api.listKeys();
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    untrack(load);
  });

  // --- New key -----------------------------------------------------------------
  // The dialog asks for a name, then shows the key once: closing it is the
  // only way back, so a key is never shown again by accident.
  let creating = $state(false);
  let name = $state('');
  let createError = $state('');
  let busy = $state(false);
  let created = $state<NewApiKey | null>(null);

  const base = $derived(app.config.public_url || location.origin);
  const example = $derived(created ? `curl -s -X POST -H "Api-Key: ${created.key}" ${base}/token` : '');

  function openCreate() {
    name = '';
    createError = '';
    created = null;
    creating = true;
  }

  async function create() {
    if (busy) return;
    createError = '';
    const n = name.trim();
    if (!n) {
      createError = 'Give the key a name, such as where it will be used.';
      return document.getElementById('key-name')?.focus();
    }
    busy = true;
    try {
      const k = await api.createKey(n);
      created = k;
      // The list keeps what the server would list: everything but the key.
      keys = [{ id: k.id, name: k.name, prefix: k.prefix, created_at: k.created_at, last_used_at: k.last_used_at }, ...keys];
    } catch (e) {
      createError = (e as Error).message;
    } finally {
      busy = false;
    }
  }

  // --- Revoke ------------------------------------------------------------------
  let revoking = $state<ApiKey | null>(null);
  let revokeError = $state('');
  let revokeBusy = $state(false);

  async function revoke() {
    const k = revoking;
    if (!k || revokeBusy) return;
    revokeError = '';
    revokeBusy = true;
    try {
      await api.revokeKey(k.id);
      keys = keys.filter((x) => x.id !== k.id);
      app.toast('API key revoked', 'success');
      revoking = null;
    } catch (e) {
      revokeError = (e as Error).message;
    } finally {
      revokeBusy = false;
    }
  }
</script>

<section class="card" aria-labelledby="account-keys">
  <div class="row">
    <div class="grow">
      <h2 id="account-keys">API keys</h2>
      <p class="hint">
        Scripts and CI jobs use a key instead of signing in: <code>Api-Key: wgo_…</code> on the <code>/token</code> routes. A URL
        created with a key is private, like one created here.
      </p>
    </div>
    <button type="button" class="btn sm" onclick={openCreate}><Plus /> New key</button>
  </div>

  {#if loading}
    <p class="none"><LoaderCircle class="spin" size={14} /> Loading…</p>
  {:else if error}
    <p class="none bad">
      Could not load your keys: {error}
      <button type="button" class="btn ghost xs" onclick={load}>Try again</button>
    </p>
  {:else if keys.length}
    <ul class="keys">
      {#each keys as k (k.id)}
        <li>
          <span class="grow">
            <span class="name">{k.name}</span>
            <span class="sub mono">wgo_{k.prefix}…</span>
            <span class="sub">
              created {day.format(new Date(k.created_at))} · {k.last_used_at ? `last used ${ago(k.last_used_at, app.now)}` : 'never used'}
            </span>
          </span>
          <button type="button" class="btn danger sm" onclick={() => ((revokeError = ''), (revoking = k))}>Revoke</button>
        </li>
      {/each}
    </ul>
  {:else}
    <p class="none">No keys yet.</p>
  {/if}
</section>

<Modal open={creating} title={created ? 'Your new API key' : 'New API key'} onclose={() => (creating = false)}>
  {#if created}
    <div class="shown">
      <p class="warn"><TriangleAlert size={16} /> <span>This is the only time the key is shown. Copy it now and keep it somewhere safe.</span></p>
      <div class="field">
        <span class="label">{created.name}</span>
        <div class="secret">
          <code class="mono">{created.key}</code>
          <CopyButton text={created.key} label="Copy" class="btn sm" toast="API key copied" />
        </div>
      </div>
      <div class="field">
        <span class="label">For example, create a private URL from a script</span>
        <div class="secret">
          <code class="mono">{example}</code>
          <CopyButton text={example} label="Copy" class="btn sm" toast="Command copied" />
        </div>
        <span class="hint">Then <code>GET {base}/token/&lt;id&gt;/wait</code> with the same header to wait for a request.</span>
      </div>
    </div>
  {:else}
    <form
      id="key-form"
      class="form"
      novalidate
      onsubmit={(e) => {
        e.preventDefault();
        create();
      }}
    >
      <div class="field">
        <label for="key-name">Name</label>
        <input id="key-name" class="input" placeholder="CI, laptop, deploy script…" maxlength={NAME_MAX} bind:value={name} use:autofocus />
        <span class="hint">So you can tell your keys apart later. The key itself is shown once, after this step.</span>
      </div>
      <FormError message={createError} />
    </form>
  {/if}
  {#snippet footer()}
    {#if created}
      <span class="grow"></span>
      <button type="button" class="btn primary" onclick={() => (creating = false)}>Done</button>
    {:else}
      <span class="grow"></span>
      <button type="button" class="btn" onclick={() => (creating = false)}>Cancel</button>
      <button type="submit" form="key-form" class="btn primary" disabled={busy}>
        {#if busy}<LoaderCircle class="spin" />{:else}<KeyRound />{/if} Create key
      </button>
    {/if}
  {/snippet}
</Modal>

<Modal open={!!revoking} title="Revoke this API key?" onclose={() => (revoking = null)}>
  {#if revoking}
    <p class="confirm-text">
      <strong>{revoking.name}</strong> (<span class="mono">wgo_{revoking.prefix}…</span>) stops working at once: anything still using it gets
      <code>401</code>. This can't be undone.
    </p>
    <FormError message={revokeError} />
  {/if}
  {#snippet footer()}
    <span class="grow"></span>
    <button type="button" class="btn" onclick={() => (revoking = null)}>Cancel</button>
    <button type="button" class="btn danger solid" disabled={revokeBusy} onclick={revoke}>
      {#if revokeBusy}<LoaderCircle class="spin" />{/if} Revoke
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
  .keys {
    display: grid;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .keys li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 16px;
    padding: 12px 0;
    border-top: 1px solid var(--border);
  }
  .keys li .grow {
    display: grid;
    gap: 2px;
  }
  .name {
    font-weight: 600;
    overflow-wrap: anywhere;
  }
  .sub {
    color: var(--text-3);
    font-size: 12px;
  }
  .sub.mono {
    font-size: 12px;
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
  .form,
  .shown {
    display: grid;
    gap: 14px;
  }
  .warn {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 9px 12px;
    border-radius: var(--radius-sm);
    background: var(--warning-soft);
    color: var(--warning);
    font-size: 13px;
  }
  .warn :global(svg) {
    flex: none;
    margin-top: 1px;
  }
  .secret {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .secret code {
    flex: 1;
    min-width: 0;
    padding: 8px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg-sunken);
    font-size: 12.5px;
    line-height: 1.5;
    overflow-wrap: anywhere;
    user-select: all;
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
    .keys li .btn {
      width: 100%;
    }
  }
</style>
