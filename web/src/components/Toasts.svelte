<script lang="ts">
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import CircleCheck from '@lucide/svelte/icons/circle-check';
  import Info from '@lucide/svelte/icons/info';
  import X from '@lucide/svelte/icons/x';
  import { fly } from 'svelte/transition';
  import { flip } from 'svelte/animate';
  import { app } from '../lib/state.svelte';

  let el = $state<HTMLDivElement>();

  // Re-show the manual popover whenever toasts change so it stays above any
  // dialog opened in the meantime (top layer is ordered by show time).
  $effect(() => {
    if (!el) return;
    if (app.toasts.length) {
      if (el.matches(':popover-open')) el.hidePopover();
      el.showPopover();
    }
  });
</script>

<div bind:this={el} popover="manual" class="toasts" aria-live="polite">
  {#each app.toasts as t (t.id)}
    <div class="toast {t.kind}" animate:flip={{ duration: 180 }} in:fly={{ y: 16, duration: 200 }} out:fly={{ y: 8, duration: 150 }}>
      {#if t.kind === 'success'}<CircleCheck size={16} />{:else if t.kind === 'error'}<CircleAlert size={16} />{:else}<Info size={16} />{/if}
      <span>{t.message}</span>
      {#if t.action}
        {@const action = t.action}
        <button
          type="button"
          class="btn xs action"
          onclick={() => {
            app.dismiss(t.id);
            action.run();
          }}>{action.label}</button
        >
      {/if}
      <button type="button" class="btn ghost icon xs" aria-label="Dismiss" onclick={() => app.dismiss(t.id)}><X /></button>
    </div>
  {/each}
</div>

<style>
  .toasts {
    position: fixed;
    inset: auto 16px 16px auto;
    margin: 0;
    padding: 0;
    border: 0;
    background: transparent;
    overflow: visible;
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 8px;
    width: min(420px, calc(100vw - 32px));
    pointer-events: none;
  }
  .toast {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 10px 8px 10px 12px;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--bg-elev);
    box-shadow: var(--shadow-lg);
    font-size: 13px;
    pointer-events: auto;
    max-width: 100%;
  }
  .toast > :global(svg) {
    flex: none;
    margin-top: 1px;
  }
  .toast span {
    flex: 1;
    overflow-wrap: anywhere;
  }
  .action {
    flex: none;
    margin: -2px 0;
  }
  .success > :global(svg) {
    color: var(--success);
  }
  .error > :global(svg) {
    color: var(--danger);
  }
  .info > :global(svg) {
    color: var(--accent);
  }
  @media (max-width: 640px) {
    .toasts {
      inset: auto 12px max(12px, env(safe-area-inset-bottom)) 12px;
      width: auto;
      align-items: stretch;
    }
  }
</style>
