<script lang="ts">
  // Asks before an action that can't be taken back. With `typed`, the button
  // only works once that text (an email address, say) has been typed in.
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import type { Snippet } from 'svelte';
  import FormError from '../FormError.svelte';
  import Modal from '../Modal.svelte';

  interface Props {
    open: boolean;
    title: string;
    action: string;
    danger?: boolean;
    typed?: string;
    busy?: boolean;
    error?: string;
    onconfirm: () => void;
    onclose: () => void;
    children: Snippet;
  }
  let { open, title, action, danger = false, typed, busy = false, error = '', onconfirm, onclose, children }: Props = $props();

  let text = $state('');
  $effect(() => {
    if (open) text = '';
  });
  const matches = $derived(!typed || text.trim().toLowerCase() === typed.trim().toLowerCase());
  const id = `confirm-${Math.random().toString(36).slice(2, 9)}`;

  // The dialog focuses its first control when it opens; the field goes first.
  function autofocus(node: HTMLElement) {
    requestAnimationFrame(() => node.focus());
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    if (matches && !busy) onconfirm();
  }
</script>

<Modal {open} {title} onclose={() => !busy && onclose()}>
  <form id={id} class="body" novalidate onsubmit={submit}>
    <div class="text">{@render children()}</div>
    {#if typed}
      <div class="field">
        <label for={`${id}-text`}>Type <strong class="typed">{typed}</strong> to confirm</label>
        <input
          id={`${id}-text`}
          class="input"
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
          bind:value={text}
          use:autofocus
        />
      </div>
    {/if}
    <FormError message={error} />
  </form>
  {#snippet footer()}
    <span class="grow"></span>
    <button type="button" class="btn sm" disabled={busy} onclick={onclose}>Cancel</button>
    <button type="submit" form={id} class="btn sm" class:primary={!danger} class:danger={danger} class:solid={danger} disabled={busy || !matches}>
      {#if busy}<LoaderCircle class="spin" />{/if}
      {action}
    </button>
  {/snippet}
</Modal>

<style>
  .body {
    display: grid;
    gap: 14px;
  }
  .text {
    display: grid;
    gap: 8px;
    color: var(--text-2);
    overflow-wrap: anywhere;
  }
  .text :global(strong) {
    color: var(--text);
  }
  .typed {
    font-family: var(--mono);
    font-size: 12.5px;
    overflow-wrap: anywhere;
  }
  .grow {
    flex: 1;
  }
</style>
