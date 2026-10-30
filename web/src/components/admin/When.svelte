<script lang="ts">
  // A compact date and time, with the full one and how long ago on hover.
  // It follows the viewer's UTC preference.
  import { ago, dateTime } from '../../lib/format';
  import { app } from '../../lib/state.svelte';

  let { iso, empty = '–' }: { iso: string | null | undefined; empty?: string } = $props();

  const short = new Intl.DateTimeFormat(undefined, { dateStyle: 'short', timeStyle: 'short' });
  const shortUTC = new Intl.DateTimeFormat(undefined, { dateStyle: 'short', timeStyle: 'short', timeZone: 'UTC' });
</script>

{#if iso}
  <time class="when" datetime={iso} title={`${dateTime(iso, app.prefs.utc)} (${ago(iso, app.now)})`}>
    {app.prefs.utc ? `${shortUTC.format(new Date(iso))} UTC` : short.format(new Date(iso))}
  </time>
{:else}
  <span class="muted">{empty}</span>
{/if}

<style>
  .when {
    white-space: nowrap;
  }
</style>
