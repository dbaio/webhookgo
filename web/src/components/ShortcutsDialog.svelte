<script lang="ts">
  import Modal from './Modal.svelte';

  let { open, onclose }: { open: boolean; onclose: () => void } = $props();

  const groups: [string, [string[], string][]][] = [
    ['Requests', [[['j', '↓'], 'Older request'], [['k', '↑'], 'Newer request'], [['d', 'Del'], 'Delete the selected request'], [['f'], 'Forward the selected request'], [['Esc'], 'Back to the list (mobile) / close']]],
    ['URL', [[['c'], 'Copy the webhook URL'], [['n'], 'Create a new URL'], [['s'], 'Response settings'], [['/'], 'Search requests']]],
    ['App', [[['t'], 'Toggle light / dark theme'], [['u'], 'Toggle local time / UTC'], [['?'], 'Show this help']]],
  ];
</script>

<Modal {open} {onclose} title="Keyboard shortcuts">
  <div class="groups">
    {#each groups as [title, items] (title)}
      <section>
        <h3>{title}</h3>
        {#each items as [keys, label] (label)}
          <div class="row">
            <span>{label}</span>
            <span class="keys">{#each keys as k, i (k)}{#if i}<span class="or">or</span>{/if}<kbd>{k}</kbd>{/each}</span>
          </div>
        {/each}
      </section>
    {/each}
  </div>
</Modal>

<style>
  .groups {
    display: grid;
    gap: 18px;
  }
  h3 {
    margin-bottom: 6px;
    color: var(--text-3);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.05em;
    text-transform: uppercase;
  }
  .row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 7px 0;
    border-top: 1px solid var(--border);
    font-size: 13px;
  }
  .keys {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .or {
    color: var(--text-3);
    font-size: 11px;
  }
</style>
