<script lang="ts">
  // The form of the sign-in and one-time link pages: a heading, the fields,
  // an inline error and one submit button. Enter submits; the page validates.
  import type { LucideIcon } from '@lucide/svelte';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import type { Snippet } from 'svelte';
  import FormError from './FormError.svelte';

  interface Props {
    title: string;
    submit: string;
    icon: LucideIcon;
    error: string;
    busy: boolean;
    onsubmit: () => void;
    lead?: Snippet;
    children: Snippet;
    footer?: Snippet;
  }
  let { title, submit, icon: Icon, error, busy, onsubmit, lead, children, footer }: Props = $props();
</script>

<form
  class="auth card"
  novalidate
  aria-busy={busy}
  onsubmit={(e) => {
    e.preventDefault();
    if (!busy) onsubmit();
  }}
>
  <header>
    <h1>{title}</h1>
    {#if lead}<p>{@render lead()}</p>{/if}
  </header>
  {@render children()}
  <FormError message={error} />
  <button type="submit" class="btn primary submit">
    {#if busy}<LoaderCircle class="spin" />{:else}<Icon />{/if}
    {submit}
  </button>
  {#if footer}<p class="foot">{@render footer()}</p>{/if}
</form>

<style>
  .auth {
    display: grid;
    gap: 14px;
    width: 100%;
    max-width: 400px;
    padding: 28px 24px 24px;
    box-shadow: var(--shadow-md);
  }
  header {
    display: grid;
    gap: 4px;
    margin-bottom: 4px;
  }
  h1 {
    font-size: 20px;
    letter-spacing: -0.015em;
  }
  header p {
    color: var(--text-2);
    overflow-wrap: anywhere;
  }
  .submit {
    height: 36px;
    margin-top: 4px;
  }
  .foot {
    color: var(--text-3);
    font-size: 12.5px;
    text-align: center;
  }
  /* Read-only fields (the invited email) look settled, not editable. */
  .auth :global(.input[readonly]) {
    background: var(--bg-sunken);
    color: var(--text-2);
  }
  @media (max-width: 480px) {
    .auth {
      padding: 22px 16px 18px;
    }
  }
</style>
