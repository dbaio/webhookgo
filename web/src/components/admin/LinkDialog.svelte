<script lang="ts">
  // A one-time link just created (an invitation or a password reset): shown
  // once, with a copy button and when it expires.
  import type { Snippet } from 'svelte';
  import type { AdminLink } from '../../lib/admin';
  import { ago, dateTime } from '../../lib/format';
  import { app } from '../../lib/state.svelte';
  import CopyButton from '../CopyButton.svelte';
  import Modal from '../Modal.svelte';

  interface Props {
    link: AdminLink | null;
    title: string;
    onclose: () => void;
    children: Snippet;
  }
  let { link, title, onclose, children }: Props = $props();
  const id = `link-${Math.random().toString(36).slice(2, 9)}`;
</script>

<Modal open={!!link} {title} {onclose}>
  {#if link}
    <div class="body">
      <p class="text">{@render children()}</p>
      <div class="field">
        <label for={id}>Link</label>
        <div class="copy">
          <input
            {id}
            class="input mono"
            readonly
            value={link.link}
            onfocus={(e) => e.currentTarget.select()}
            onclick={(e) => e.currentTarget.select()}
          />
          <CopyButton text={link.link} class="btn primary sm" label="Copy" title="Copy the link" toast="Link copied to clipboard" />
        </div>
        <span class="hint">
          It works once and expires on <strong>{dateTime(link.expires_at, app.prefs.utc)}</strong> ({ago(link.expires_at, Date.now())}).
          Anyone who has it can use it: send it privately. It isn't shown again.
        </span>
      </div>
    </div>
  {/if}
  {#snippet footer()}
    <span class="grow"></span>
    <button type="button" class="btn primary sm" onclick={onclose}>Done</button>
  {/snippet}
</Modal>

<style>
  .body {
    display: grid;
    gap: 14px;
  }
  .text {
    color: var(--text-2);
    overflow-wrap: anywhere;
  }
  .text :global(strong) {
    color: var(--text);
  }
  .copy {
    display: flex;
    gap: 6px;
  }
  .copy .input {
    flex: 1;
    min-width: 0;
    height: 30px;
    font-size: 12px;
  }
  .copy :global(.btn) {
    height: 30px;
  }
  @media (pointer: coarse) {
    .copy .input,
    .copy :global(.btn) {
      height: 44px;
    }
  }
  .hint strong {
    color: var(--text-2);
    font-weight: 600;
  }
  .grow {
    flex: 1;
  }
</style>
