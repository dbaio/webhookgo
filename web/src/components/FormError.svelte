<script lang="ts">
  // An error shown under a form. Clear it before each submit so a repeated
  // message is announced again.
  import CircleAlert from '@lucide/svelte/icons/circle-alert';

  let { message }: { message: string } = $props();
</script>

{#if message}
  <p class="error" role="alert"><CircleAlert size={15} /><span>{message}</span></p>
{/if}

<style>
  .error {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 9px 12px;
    border-radius: var(--radius-sm);
    background: var(--danger-soft);
    color: var(--danger);
    font-size: 13px;
  }
  .error :global(svg) {
    flex: none;
    margin-top: 2px;
  }
  span {
    overflow-wrap: anywhere;
  }
</style>
