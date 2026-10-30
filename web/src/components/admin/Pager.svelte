<script lang="ts">
  // "21–40 of 135" with previous and next buttons, under a paged table.
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import type { Page } from '../../lib/types';

  interface Props {
    page: Page<unknown> | null;
    loading?: boolean;
    label: string;
    onpage: (n: number) => void;
  }
  let { page, loading = false, label, onpage }: Props = $props();
  const count = new Intl.NumberFormat();
</script>

{#if page && page.total > 0}
  <nav class="pager" aria-label={label}>
    <span class="range">
      {page.from === page.to ? count.format(page.from) : `${count.format(page.from)}–${count.format(page.to)}`} of {count.format(page.total)}
    </span>
    {#if page.current_page > 1 || !page.is_last_page}
      <button
        type="button"
        class="btn sm icon"
        aria-label="Previous page"
        title="Previous page"
        disabled={loading || page.current_page <= 1}
        onclick={() => onpage(page.current_page - 1)}><ChevronLeft /></button
      >
      <button
        type="button"
        class="btn sm icon"
        aria-label="Next page"
        title="Next page"
        disabled={loading || page.is_last_page}
        onclick={() => onpage(page.current_page + 1)}><ChevronRight /></button
      >
    {/if}
  </nav>
{/if}

<style>
  .pager {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 6px;
    padding: 10px 12px;
    border-top: 1px solid var(--border);
  }
  .range {
    margin-right: 6px;
    color: var(--text-3);
    font-size: 12.5px;
    font-variant-numeric: tabular-nums;
  }
</style>
