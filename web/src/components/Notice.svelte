<script lang="ts">
  // A centred card for a page that has nothing else to show: an invalid link,
  // a signed-out visitor, an unreachable server.
  import type { LucideIcon } from '@lucide/svelte';
  import type { Snippet } from 'svelte';

  interface Props {
    icon: LucideIcon;
    title: string;
    tone?: 'warning' | 'accent';
    children?: Snippet;
    actions?: Snippet;
  }
  let { icon: Icon, title, tone = 'warning', children, actions }: Props = $props();
</script>

<section class="notice card {tone}">
  <Icon size={28} />
  <h1>{title}</h1>
  {#if children}<p>{@render children()}</p>{/if}
  {#if actions}<div class="actions">{@render actions()}</div>{/if}
</section>

<style>
  .notice {
    display: grid;
    justify-items: center;
    gap: 12px;
    width: 100%;
    max-width: 440px;
    padding: 32px 28px;
    color: var(--text-2);
    text-align: center;
  }
  .notice > :global(svg) {
    color: var(--warning);
  }
  .accent > :global(svg) {
    color: var(--accent);
  }
  h1 {
    color: var(--text);
    font-size: 18px;
    letter-spacing: -0.01em;
  }
  p {
    overflow-wrap: anywhere;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 8px;
    margin-top: 4px;
  }
</style>
