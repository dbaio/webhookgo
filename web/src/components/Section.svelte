<script lang="ts">
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import type { Snippet } from 'svelte';
  import { app } from '../lib/state.svelte';

  interface Props {
    id: string;
    title: string;
    count?: number;
    actions?: Snippet;
    children: Snippet;
    flush?: boolean;
  }
  let { id, title, count, actions, children, flush = false }: Props = $props();
  const collapsed = $derived(!!app.prefs.collapsed[id]);

  function toggle() {
    app.prefs.collapsed[id] = !collapsed;
    app.savePrefs();
  }
</script>

<section class="card" class:collapsed>
  <header>
    <button type="button" class="toggle" onclick={toggle} aria-expanded={!collapsed}>
      <ChevronRight size={15} />
      <h3>{title}</h3>
      {#if count !== undefined}<span class="count">{count}</span>{/if}
    </button>
    {#if actions && !collapsed}<div class="actions">{@render actions()}</div>{/if}
  </header>
  {#if !collapsed}
    <div class="body" class:flush>{@render children()}</div>
  {/if}
</section>

<style>
  section {
    min-width: 0;
    overflow: hidden;
  }
  header {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    min-height: 44px;
    padding: 6px 10px 6px 6px;
  }
  .collapsed header {
    border-bottom: 0;
  }
  .toggle {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 6px;
    border: 0;
    border-radius: 6px;
    background: transparent;
    cursor: pointer;
  }
  .toggle:hover {
    background: var(--bg-hover);
  }
  @media (pointer: coarse) {
    .toggle {
      flex: 1;
      min-height: 36px;
    }
  }
  .toggle :global(svg) {
    color: var(--text-3);
    transform: rotate(90deg);
    transition: transform 0.15s;
  }
  .collapsed .toggle :global(svg) {
    transform: none;
  }
  h3 {
    font-size: 13px;
    font-weight: 650;
  }
  .count {
    padding: 0 6px;
    border-radius: 999px;
    background: var(--bg-sunken);
    color: var(--text-3);
    font-size: 11.5px;
    font-weight: 600;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
  }
  .body {
    padding: 0 14px 14px;
  }
  .body.flush {
    padding: 0;
    border-top: 1px solid var(--border);
  }
</style>
