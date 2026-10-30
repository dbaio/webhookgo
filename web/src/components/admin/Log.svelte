<script lang="ts">
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import { onMount } from 'svelte';
  import { adminApi, LOG_ACTIONS, type LogEntry } from '../../lib/admin';
  import { isUUID } from '../../lib/api';
  import type { Page } from '../../lib/types';
  import FormError from '../FormError.svelte';
  import Pager from './Pager.svelte';
  import When from './When.svelte';

  const PER_PAGE = 50;
  const LABELS: Record<string, string> = {
    'user.invite': 'Invite a user',
    'user.role': 'Change a role',
    'user.disable': 'Disable a user',
    'user.enable': 'Enable a user',
    'user.reset_link': 'Password reset link',
    'user.delete': 'Delete a user',
    'url.view': "Open someone's private URL",
    'url.delete': 'Delete a URL',
    setup: 'First admin created',
    'cli.invite': 'Invite from the CLI',
    'cli.reset': 'Reset link from the CLI',
  };

  let action = $state('');
  let res = $state.raw<Page<LogEntry> | null>(null);
  let loading = $state(false);
  let error = $state('');
  let seq = 0;

  async function load(page = res?.current_page ?? 1) {
    const n = ++seq;
    loading = true;
    error = '';
    try {
      const r = await adminApi.log({ page, perPage: PER_PAGE, action });
      if (n === seq) res = r;
    } catch (e) {
      if (n === seq) error = `Could not load the log: ${(e as Error).message}`;
    } finally {
      if (n === seq) loading = false;
    }
  }

  onMount(() => {
    load(1);
  });

  const details = (d: LogEntry['details']) => (d && Object.keys(d).length ? JSON.stringify(d) : '');
  const email = (e: LogEntry) => (typeof e.details?.email === 'string' ? e.details.email : '');
</script>

<div class="panel">
  <div class="toolbar">
    <label class="filter">
      <span class="sr-only">Action</span>
      <select class="input" bind:value={action} onchange={() => load(1)}>
        <option value="">Every action</option>
        {#each LOG_ACTIONS as a (a)}<option value={a}>{a} · {LABELS[a]}</option>{/each}
      </select>
    </label>
    {#if loading}<LoaderCircle class="spin muted" size={16} />{/if}
    <span class="grow"></span>
    <button type="button" class="btn sm" disabled={loading} onclick={() => load(1)}><RefreshCw /> Refresh</button>
  </div>

  <FormError message={error} />

  <div class="card table-card">
    <div class="table-wrap">
      <table>
        <caption class="sr-only">Admin log, newest first</caption>
        <thead>
          <tr>
            <th>Time</th>
            <th>Actor</th>
            <th>Action</th>
            <th>Target</th>
            <th>Details</th>
          </tr>
        </thead>
        <tbody>
          {#each res?.data ?? [] as e (e.id)}
            {@const d = details(e.details)}
            <tr>
              <td><When iso={e.created_at} /></td>
              <td class="actor">
                {#if e.actor_email === 'cli' || e.actor_email === 'setup'}
                  <span class="chip">{e.actor_email === 'cli' ? 'Command line' : 'Setup link'}</span>
                {:else}
                  <span class="email">{e.actor_email}</span>
                {/if}
                {#if e.ip}<small class="mono">{e.ip}</small>{/if}
              </td>
              <td><code class="action" title={LABELS[e.action] ?? ''}>{e.action}</code></td>
              <td class="target">
                <span class="type">{e.target_type}</span>
                {#if e.target_type === 'url' && isUUID(e.target_id)}
                  <a class="mono" href={`#urls/${e.target_id.toLowerCase()}`} title="Look up this URL">{e.target_id}</a>
                {:else if e.target_type === 'user' && email(e)}
                  <span title={e.target_id}>{email(e)}</span>
                {:else}
                  <span class="mono">{e.target_id}</span>
                {/if}
              </td>
              <td class="details">
                {#if d}<code title={d}>{d}</code>{:else}<span class="muted">–</span>{/if}
              </td>
            </tr>
          {:else}
            <tr>
              <td colspan="5" class="none">
                {#if loading}<LoaderCircle class="spin" size={14} /> Loading…{:else if !error}{action ? 'Nothing logged for this action.' : 'Nothing logged yet.'}{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <Pager page={res} {loading} label="Pages of the log" onpage={load} />
  </div>
</div>

<style>
  .filter {
    flex: 1 1 200px;
    max-width: 320px;
    min-width: 0;
  }
  .filter .input {
    height: 30px;
    font-size: 13px;
  }
  .actor .email {
    white-space: nowrap;
  }
  .actor .chip {
    height: 20px;
    font-size: 11.5px;
  }
  .action {
    white-space: nowrap;
  }
  .target {
    white-space: nowrap;
  }
  .target .type {
    display: block;
    color: var(--text-3);
    font-size: 11.5px;
  }
  .target .mono {
    font-size: 12px;
  }
  .details code {
    display: block;
    min-width: 200px;
    max-width: 420px;
    color: var(--text-2);
    font-size: 11.5px;
    overflow-wrap: anywhere;
    white-space: normal;
  }
  .grow {
    flex: 1;
  }
</style>
