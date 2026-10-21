<script lang="ts">
  import X from '@lucide/svelte/icons/x';
  import { fly } from 'svelte/transition';
  import { bytes, clock, dateTime, shortAgo, statusClass } from '../lib/format';
  import { SIGNATURE_STATUS } from '../lib/signature';
  import { app } from '../lib/state.svelte';
  import type { Summary } from '../lib/types';
  import MethodBadge from './MethodBadge.svelte';
  import SignatureIcon from './SignatureIcon.svelte';

  let { r, selected, unread }: { r: Summary; selected: boolean; unread: boolean } = $props();
  let el = $state<HTMLDivElement>();

  $effect(() => {
    if (selected && el) el.scrollIntoView({ block: 'nearest' });
  });
</script>

<div bind:this={el} class="item" class:selected class:unread in:fly={{ y: -10, duration: 220 }}>
  <button type="button" class="main" onclick={() => app.select(r.uuid)} aria-current={selected}>
    <MethodBadge method={r.method} />
    <span class="text">
      <span class="path mono">{r.path}{#if r.query_string}<span class="qs">?{r.query_string}</span>{/if}</span>
      <span class="sub">
        <span class="status {statusClass(r.response_status)}">{r.response_status}</span>
        {#if r.signature}
          <span class="sig {SIGNATURE_STATUS[r.signature].tone}" title={SIGNATURE_STATUS[r.signature].label}>
            <SignatureIcon status={r.signature} />
          </span>
        {/if}
        <span>{bytes(r.size)}</span>
        <span class="at">{clock(r.created_at, app.prefs.utc)}</span>
        <span class="ip">{r.ip}</span>
      </span>
    </span>
    <span class="when" title={dateTime(r.created_at, app.prefs.utc)}>
      {shortAgo(r.created_at, app.now)}
      {#if unread}<span class="dot" aria-label="unread"></span>{/if}
    </span>
  </button>
  <button type="button" class="del btn ghost icon xs" title="Delete request" aria-label="Delete request" onclick={() => app.deleteRequest(r.uuid)}>
    <X />
  </button>
</div>

<style>
  .item {
    position: relative;
    margin: 0 8px 2px;
    border-radius: 9px;
  }
  .main {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    align-items: start;
    gap: 10px;
    width: 100%;
    padding: 9px 10px;
    border: 1px solid transparent;
    border-radius: 9px;
    background: transparent;
    text-align: left;
    cursor: pointer;
  }
  .main:hover {
    background: var(--bg-hover);
  }
  .selected .main {
    background: var(--bg-active);
    border-color: color-mix(in srgb, var(--accent) 30%, transparent);
  }
  .main :global(.method) {
    margin-top: 1px;
  }
  .text {
    display: grid;
    gap: 3px;
    min-width: 0;
  }
  .path {
    overflow: hidden;
    color: var(--text);
    font-size: 12.5px;
    font-weight: 500;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .qs {
    color: var(--text-3);
  }
  .sub {
    display: flex;
    gap: 8px;
    overflow: hidden;
    color: var(--text-3);
    font-size: 11.5px;
    white-space: nowrap;
  }
  .sub .status {
    font-size: 11px;
  }
  .sig {
    display: inline-flex;
    align-items: center;
  }
  .sig.ok {
    color: var(--success);
  }
  .sig.bad {
    color: var(--danger);
  }
  .sig.warn {
    color: var(--warning);
  }
  .at {
    font-variant-numeric: tabular-nums;
  }
  .ip {
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .when {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--text-3);
    font-size: 11.5px;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
  .dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-soft);
  }
  .unread .path {
    font-weight: 650;
  }
  .del {
    position: absolute;
    right: 6px;
    bottom: 6px;
    opacity: 0;
    background: var(--bg-elev);
  }
  .item:hover .del,
  .del:focus-visible {
    opacity: 1;
  }
  .del:hover {
    color: var(--danger);
  }
  @media (hover: none) {
    .del {
      display: none;
    }
  }
</style>
