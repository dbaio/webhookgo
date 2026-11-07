<script lang="ts">
  import X from '@lucide/svelte/icons/x';
  import type { Snippet } from 'svelte';

  interface Props {
    open: boolean;
    title: string;
    subtitle?: string;
    wide?: boolean;
    onclose: () => void;
    children: Snippet;
    footer?: Snippet;
  }
  let { open, title, subtitle, wide = false, onclose, children, footer }: Props = $props();
  let el = $state<HTMLDialogElement>();
  let downOnBackdrop = false;

  $effect(() => {
    if (!el) return;
    if (open && !el.open) el.showModal();
    else if (!open && el.open) el.close();
  });
</script>

<dialog
  bind:this={el}
  class="modal"
  class:wide
  onclose={() => open && onclose()}
  onpointerdown={(e) => (downOnBackdrop = e.target === el)}
  onclick={(e) => {
    if (e.target === el && downOnBackdrop) onclose();
  }}
>
  {#if open}
    <div class="frame">
      <header>
        <div>
          <h2>{title}</h2>
          {#if subtitle}<p>{subtitle}</p>{/if}
        </div>
        <button type="button" class="btn ghost icon sm" aria-label="Close" onclick={onclose}><X /></button>
      </header>
      <div class="content">{@render children()}</div>
      {#if footer}<footer>{@render footer()}</footer>{/if}
    </div>
  {/if}
</dialog>

<style>
  .frame {
    display: flex;
    flex-direction: column;
    max-height: inherit;
  }
  header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    padding: 18px 20px 14px;
    border-bottom: 1px solid var(--border);
  }
  header .btn {
    flex: none; /* a long subtitle mustn't squeeze it */
  }
  h2 {
    font-size: 16px;
    font-weight: 650;
    letter-spacing: -0.01em;
  }
  header p {
    margin-top: 2px;
    color: var(--text-3);
    font-size: 13px;
  }
  .content {
    flex: 1;
    min-height: 0;
    padding: 18px 20px;
    overflow: auto;
  }
  footer {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
    padding: 14px 20px;
    border-top: 1px solid var(--border);
    background: var(--bg-sunken);
    padding-bottom: max(14px, env(safe-area-inset-bottom));
  }
</style>
