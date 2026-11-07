<script lang="ts">
  import JsonNode from './JsonNode.svelte';
  import { scalarClass, scalarText, type JNode } from '../lib/body';

  interface Props {
    node: JNode;
    name?: string | number;
    depth?: number;
    last?: boolean;
    openDepth: number;
  }
  let { node, name, depth = 0, last = true, openDepth }: Props = $props();

  const PAGE = 200;
  // svelte-ignore state_referenced_locally
  let open = $state(depth < openDepth);
  let limit = $state(PAGE);

  const container = $derived(node.t === 'o' || node.t === 'a');
  const size = $derived(node.t === 'o' ? node.entries.length : node.t === 'a' ? node.items.length : 0);
  const brackets = $derived(node.t === 'o' ? ['{', '}'] : ['[', ']']);
</script>

{#snippet label()}
  {#if typeof name === 'string'}<span class="k">{JSON.stringify(name)}</span><span class="p">{': '}</span>{:else if typeof name === 'number'}<span class="i">{name}</span><span class="p">{': '}</span>{/if}
{/snippet}

{#if container && size > 0}
  <div class="line">
    <button type="button" class="tog" class:open aria-label={open ? 'Collapse' : 'Expand'} onclick={() => (open = !open)}></button>
    {@render label()}<span class="p">{brackets[0]}</span>
    {#if !open}
      <button type="button" class="summary" onclick={() => (open = true)}>{size} {node.t === 'o' ? (size === 1 ? 'key' : 'keys') : size === 1 ? 'item' : 'items'}</button><span class="p">{brackets[1]}{last ? '' : ','}</span>
    {/if}
  </div>
  {#if open}
    <div class="children">
      {#if node.t === 'o'}
        {#each node.entries.slice(0, limit) as [k, v], i (i)}
          <JsonNode node={v} name={k} depth={depth + 1} last={i === size - 1} {openDepth} />
        {/each}
      {:else if node.t === 'a'}
        {#each node.items.slice(0, limit) as v, i (i)}
          <JsonNode node={v} name={i} depth={depth + 1} last={i === size - 1} {openDepth} />
        {/each}
      {/if}
      {#if size > limit}
        <button type="button" class="more" onclick={() => (limit += PAGE)}>Show {Math.min(PAGE, size - limit)} more of {size - limit} remaining…</button>
      {/if}
    </div>
    <div class="line"><span class="p">{brackets[1]}{last ? '' : ','}</span></div>
  {/if}
{:else}
  <div class="line leaf">
    {@render label()}{#if container}<span class="p">{brackets[0]}{brackets[1]}</span>{:else}<span class={scalarClass(node)}>{scalarText(node)}</span>{/if}<span class="p">{last ? '' : ','}</span>
  </div>
{/if}

<style>
  .line {
    position: relative;
    padding-left: 18px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .children {
    margin-left: 7px;
    padding-left: 11px;
    border-left: 1px solid var(--border);
  }
  .children:hover {
    border-left-color: var(--border-strong);
  }
  .tog {
    position: absolute;
    left: 0;
    top: 3px;
    width: 15px;
    height: 15px;
    padding: 0;
    border: 0;
    border-radius: 4px;
    background: transparent;
    cursor: pointer;
  }
  .tog::before {
    content: '';
    position: absolute;
    inset: 0;
    margin: auto;
    width: 0;
    height: 0;
    border-left: 5px solid var(--text-3);
    border-top: 4px solid transparent;
    border-bottom: 4px solid transparent;
    transition: transform 0.12s;
  }
  .tog.open::before {
    transform: rotate(90deg);
  }
  @media (pointer: coarse) {
    .tog::after {
      content: '';
      position: absolute;
      inset: -8px;
    }
  }
  .tog:hover {
    background: var(--bg-hover);
  }
  .summary {
    margin: 0 3px;
    padding: 0 6px;
    border: 1px solid var(--border);
    border-radius: 4px;
    background: var(--bg-sunken);
    color: var(--text-3);
    font: 11px/16px var(--font);
    cursor: pointer;
  }
  .summary:hover {
    color: var(--text);
  }
  .i {
    color: var(--text-3);
  }
  .more {
    margin: 4px 0 4px 18px;
    padding: 2px 8px;
    border: 1px dashed var(--border-strong);
    border-radius: 5px;
    background: transparent;
    color: var(--accent-strong);
    font: 12px var(--font);
    cursor: pointer;
  }
</style>
