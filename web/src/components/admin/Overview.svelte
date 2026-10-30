<script lang="ts">
  import Database from '@lucide/svelte/icons/database';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Lock from '@lucide/svelte/icons/lock';
  import MemoryStick from '@lucide/svelte/icons/memory-stick';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import ShieldCheck from '@lucide/svelte/icons/shield-check';
  import Tag from '@lucide/svelte/icons/tag';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import UserX from '@lucide/svelte/icons/user-x';
  import Users from '@lucide/svelte/icons/users';
  import { onMount } from 'svelte';
  import { adminApi, type AdminStats } from '../../lib/admin';
  import { bytes, clock } from '../../lib/format';
  import { app } from '../../lib/state.svelte';
  import FormError from '../FormError.svelte';

  let stats = $state.raw<AdminStats | null>(null);
  let loading = $state(false);
  let error = $state('');
  let updated = $state('');

  async function load() {
    loading = true;
    error = '';
    try {
      stats = await adminApi.stats();
      updated = new Date().toISOString();
    } catch (e) {
      error = `Could not load the numbers: ${(e as Error).message}`;
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    load();
  });

  const count = new Intl.NumberFormat();
  const used = $derived(stats ? stats.redis.used_memory : 0);
  const max = $derived(stats ? stats.redis.maxmemory : 0);
  const share = $derived(max > 0 ? Math.min(1, used / max) : 0);
  const level = $derived(share >= 0.9 ? 'bad' : share >= 0.75 ? 'warn' : 'ok');
</script>

<div class="overview">
  <div class="toolbar">
    <span class="grow hint">{updated ? `Updated ${clock(updated, app.prefs.utc)}` : ''}</span>
    <button type="button" class="btn sm" disabled={loading} onclick={load}>
      {#if loading}<LoaderCircle class="spin" />{:else}<RefreshCw />{/if} Refresh
    </button>
  </div>

  <FormError message={error} />

  {#if stats}
    {@const s = stats}
    <div class="tiles">
      <a class="card tile link" href="#users">
        <span class="icon"><Users /></span>
        <span class="value">{count.format(s.users.total)}</span>
        <span class="label">Users</span>
      </a>
      <a class="card tile link" href="#users">
        <span class="icon"><ShieldCheck /></span>
        <span class="value">{count.format(s.users.admins)}</span>
        <span class="label">Admins</span>
      </a>
      <a class="card tile link" href="#users">
        <span class="icon" class:warn={s.users.disabled > 0}><UserX /></span>
        <span class="value">{count.format(s.users.disabled)}</span>
        <span class="label">Disabled</span>
      </a>
      <a class="card tile link" href="#urls">
        <span class="icon"><Lock /></span>
        <span class="value">{count.format(s.urls.owned)}</span>
        <span class="label">Private URLs</span>
      </a>
    </div>

    <div class="tiles">
      <section class="card tile wide" aria-labelledby="admin-redis">
        <span class="icon"><MemoryStick /></span>
        <span class="value">
          {bytes(used)}
          {#if max > 0}<small>of {bytes(max)}</small>{/if}
        </span>
        <span id="admin-redis" class="label">Redis memory</span>
        {#if max > 0}
          <div
            class="meter {level}"
            role="meter"
            aria-label="Redis memory used"
            aria-valuemin={0}
            aria-valuemax={max}
            aria-valuenow={used}
            aria-valuetext={`${bytes(used)} of ${bytes(max)} (${Math.round(share * 100)}%)`}
          >
            <span style:width={`${Math.max(share * 100, used > 0 ? 1 : 0)}%`}></span>
          </div>
          <span class="hint">{Math.round(share * 100)}% of maxmemory</span>
        {:else}
          <span class="hint">No maxmemory set: Redis may use all of the host's memory.</span>
        {/if}
        <span class="evicted" class:warn={s.redis.evicted_keys > 0}>
          {#if s.redis.evicted_keys > 0}<TriangleAlert />{/if}
          <span>
            <strong>{count.format(s.redis.evicted_keys)}</strong>
            {s.redis.evicted_keys === 1 ? 'key' : 'keys'} evicted since Redis started{s.redis.evicted_keys > 0
              ? ': it ran out of room, so some URLs or requests may be missing'
              : ''}
          </span>
        </span>
      </section>
      <section class="card tile" aria-labelledby="admin-db">
        <span class="icon"><Database /></span>
        <span class="value">{bytes(s.database.size_bytes)}</span>
        <span id="admin-db" class="label">Database size</span>
      </section>
      <section class="card tile" aria-labelledby="admin-version">
        <span class="icon"><Tag /></span>
        <span class="value mono version" title={s.version}>{s.version || '–'}</span>
        <span id="admin-version" class="label">Version</span>
      </section>
    </div>
  {:else if loading}
    <p class="loading"><LoaderCircle class="spin" size={16} /> Loading…</p>
  {/if}
</div>

<style>
  .overview {
    display: grid;
    gap: 14px;
  }
  .tiles {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 12px;
  }
  .tile {
    display: grid;
    align-content: start;
    gap: 2px;
    min-width: 0;
    padding: 14px 16px 16px;
    color: var(--text);
  }
  .tile.link {
    text-decoration: none;
    transition:
      border-color 0.12s,
      background 0.12s;
  }
  .tile.link:hover {
    border-color: var(--border-strong);
    background: var(--bg-hover);
  }
  .wide {
    grid-column: span 2;
  }
  .icon {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    margin-bottom: 8px;
    border-radius: 8px;
    background: var(--accent-soft);
    color: var(--accent-strong);
  }
  .icon.warn {
    background: var(--warning-soft);
    color: var(--warning);
  }
  .icon :global(svg) {
    width: 16px;
    height: 16px;
  }
  .value {
    overflow: hidden;
    font-size: 24px;
    font-variant-numeric: tabular-nums;
    font-weight: 650;
    letter-spacing: -0.02em;
    line-height: 1.2;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .value small {
    color: var(--text-3);
    font-size: 14px;
    font-weight: 500;
    letter-spacing: 0;
  }
  .version {
    font-size: 17px;
    line-height: 1.7;
  }
  .label {
    color: var(--text-2);
    font-size: 13px;
    font-weight: 500;
  }
  .tile .hint {
    margin-top: 4px;
  }
  .evicted {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    margin-top: 8px;
    padding-top: 8px;
    border-top: 1px solid var(--border);
    color: var(--text-2);
    font-size: 12.5px;
  }
  .evicted strong {
    font-variant-numeric: tabular-nums;
  }
  .evicted.warn {
    color: var(--warning);
  }
  .evicted :global(svg) {
    flex: none;
    width: 14px;
    height: 14px;
    margin-top: 2px;
  }
  .grow {
    flex: 1;
  }
  .meter {
    height: 8px;
    margin-top: 10px;
    overflow: hidden;
    border-radius: 999px;
    background: var(--bg-sunken);
    box-shadow: inset 0 0 0 1px var(--border);
  }
  .meter span {
    display: block;
    height: 100%;
    border-radius: inherit;
    background: var(--accent);
    transition: width 0.3s ease;
  }
  .meter.warn span {
    background: var(--warning);
  }
  .meter.bad span {
    background: var(--danger);
  }
  .loading {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px 0;
    color: var(--text-3);
  }
  @media (max-width: 860px) {
    .tiles {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
  @media (max-width: 380px) {
    .value {
      font-size: 20px;
    }
  }
</style>
