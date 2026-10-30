<script lang="ts">
  import ExternalLink from '@lucide/svelte/icons/external-link';
  import Send from '@lucide/svelte/icons/send';
  import Webhook from '@lucide/svelte/icons/webhook';
  import { duration } from '../lib/format';
  import { sample, SAMPLES, type SampleKind } from '../lib/snippets';
  import { app } from '../lib/state.svelte';
  import CopyButton from './CopyButton.svelte';
  import OpenSource from './OpenSource.svelte';

  let kind = $state<SampleKind>('curl');
  let sending = $state(false);

  async function sendTest() {
    sending = true;
    try {
      await fetch(`${app.webhookUrl}/test?source=browser`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Test-Request': 'true' },
        body: JSON.stringify({
          event: 'test.ping',
          message: 'Hello from your browser 👋',
          sent_at: new Date().toISOString(),
          data: { id: 9007199254740993, tags: ['demo', 'webhook'], nested: { ok: true, value: null } },
        }),
      });
    } catch {
      app.toast('Could not send the test request', 'error');
    } finally {
      sending = false;
    }
  }
</script>

<div class="empty">
  <div class="radar" aria-hidden="true">
    <span></span><span></span><span></span>
    <div class="core"><Webhook size={26} /></div>
  </div>
  <h1>Waiting for the first request…</h1>
  <p class="lead">Send any HTTP request to your unique URL — it shows up here instantly, no refresh needed.</p>

  <div class="url">
    <code>{app.webhookUrl}</code>
    <CopyButton text={app.webhookUrl} class="btn primary sm" label="Copy" title="Copy URL" toast="URL copied" />
    <a class="btn sm" href={app.webhookUrl} target="_blank" rel="noopener" title="Open in a new tab"><ExternalLink /> <span>Open</span></a>
  </div>

  <div class="card samples">
    <div class="tabs">
      <div class="seg" role="tablist">
        {#each Object.entries(SAMPLES) as [k, label] (k)}
          <button type="button" role="tab" aria-selected={kind === k} class:on={kind === k} onclick={() => (kind = k as SampleKind)}>{label}</button>
        {/each}
      </div>
      <CopyButton text={() => sample(kind, app.webhookUrl)} class="btn ghost icon sm" title="Copy command" />
    </div>
    <pre class="code wrap">{sample(kind, app.webhookUrl)}</pre>
  </div>

  <button type="button" class="btn primary" disabled={sending} onclick={sendTest}>
    <Send /> Send a test request
  </button>

  <ul class="tips">
    <li><strong>Any method, any path:</strong> <code>/{app.token?.uuid.slice(0, 8)}…/orders/42</code> works too.</li>
    <li><strong>Force a status code</strong> by starting the path with it: <code>…/404</code>, <code>…/503/retry</code>.</li>
    <li><strong>Customize the response</strong> (status, body, headers, delay, CORS) with the <em>Response</em> button.</li>
    <li><strong>Forward</strong> requests to <code>localhost</code> right from your browser.</li>
    {#if app.token?.expires_at === null}
      <li>Keeps the last {app.config.max_requests} requests, for {duration(app.config.token_ttl)} without new ones; this private URL doesn't expire.</li>
    {:else}
      <li>Keeps the last {app.config.max_requests} requests; the URL expires after {duration(app.config.token_ttl)} without use.</li>
    {/if}
  </ul>

  <OpenSource />
</div>

<style>
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 18px;
    width: 100%;
    max-width: 760px;
    margin: 0 auto;
    padding: 48px 24px 40px;
    text-align: center;
  }
  .radar {
    position: relative;
    display: grid;
    place-items: center;
    width: 112px;
    height: 112px;
    margin-bottom: 4px;
  }
  .radar span {
    position: absolute;
    inset: 0;
    border-radius: 50%;
    border: 1.5px solid var(--accent);
    opacity: 0;
    animation: radar 3s cubic-bezier(0.2, 0.6, 0.3, 1) infinite;
  }
  .radar span:nth-child(2) {
    animation-delay: 1s;
  }
  .radar span:nth-child(3) {
    animation-delay: 2s;
  }
  @keyframes radar {
    0% {
      transform: scale(0.45);
      opacity: 0.8;
    }
    100% {
      transform: scale(1.15);
      opacity: 0;
    }
  }
  .core {
    display: grid;
    place-items: center;
    width: 58px;
    height: 58px;
    border-radius: 18px;
    background: linear-gradient(140deg, var(--accent), color-mix(in srgb, var(--accent) 60%, #6d5dfc));
    color: #fff;
    box-shadow: 0 10px 30px color-mix(in srgb, var(--accent) 35%, transparent);
  }
  h1 {
    font-size: 22px;
    font-weight: 700;
    letter-spacing: -0.02em;
  }
  .lead {
    max-width: 480px;
    margin-top: -8px;
    color: var(--text-2);
  }
  .url {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: center;
    gap: 8px;
    width: 100%;
    padding: 8px 8px 8px 14px;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--bg-elev);
    box-shadow: var(--shadow-sm);
  }
  .url code {
    flex: 1;
    min-width: 0;
    font-size: 13.5px;
    text-align: left;
    overflow-wrap: anywhere;
    user-select: all;
  }
  .samples {
    width: 100%;
    overflow: hidden;
    text-align: left;
  }
  .tabs {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 8px;
    border-bottom: 1px solid var(--border);
  }
  .tabs .seg {
    overflow-x: auto;
    scrollbar-width: none;
  }
  .samples pre {
    background: var(--bg-sunken);
  }
  .tips {
    display: grid;
    gap: 8px;
    margin: 6px 0 0;
    padding: 0;
    list-style: none;
    color: var(--text-2);
    font-size: 13px;
    text-align: left;
  }
  .tips li {
    padding-left: 18px;
    position: relative;
  }
  .tips li::before {
    content: '';
    position: absolute;
    left: 4px;
    top: 8px;
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: var(--accent);
  }
  .tips code {
    padding: 1px 5px;
    border-radius: 4px;
    background: var(--bg-sunken);
    font-size: 12px;
  }
  @media (max-width: 640px) {
    .empty {
      padding: 28px 14px 32px;
    }
    h1 {
      font-size: 19px;
    }
    .url {
      padding: 10px;
    }
    .url code {
      flex-basis: 100%;
      text-align: center;
    }
  }
</style>
