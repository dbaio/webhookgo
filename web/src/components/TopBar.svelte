<script lang="ts">
  import Forward from '@lucide/svelte/icons/forward';
  import Lock from '@lucide/svelte/icons/lock';
  import Plus from '@lucide/svelte/icons/plus';
  import SlidersHorizontal from '@lucide/svelte/icons/sliders-horizontal';
  import Star from '@lucide/svelte/icons/star';
  import Webhook from '@lucide/svelte/icons/webhook';
  import { count, REPO_URL, repoStats, type RepoStats } from '../lib/project';
  import { app } from '../lib/state.svelte';
  import CopyButton from './CopyButton.svelte';
  import GitHubMark from './GitHubMark.svelte';
  import HistoryMenu from './HistoryMenu.svelte';
  import PrefsMenu from './PrefsMenu.svelte';
  import UserMenu from './UserMenu.svelte';

  let creating = $state(false);
  let stats = $state<RepoStats | null>(null);
  repoStats().then((s) => (stats = s));

  async function newUrl() {
    creating = true;
    const t = await app.createToken();
    creating = false;
    if (t) app.toast(t.owner_id ? 'New private URL created — only you and the admins can see it' : 'New URL created — the previous one is in your history', 'success');
  }

  const privateTitle = $derived(
    `Only ${app.token?.owner_id === app.user?.id ? 'you' : 'its owner'} and the admins can see it. It doesn't expire.`,
  );

  const liveTitle = $derived(
    {
      live: 'Live: new requests appear instantly',
      polling: 'Live, by polling: this connection holds back streamed updates, so new requests are checked every few seconds',
      connecting: 'Connecting to live updates…',
      offline: 'Live updates offline, retrying…',
    }[app.live],
  );
</script>

<header class="topbar">
  <a class="brand" href="#/" title={app.config.app_name} onclick={(e) => { e.preventDefault(); if (app.token) app.select(null); }}>
    <span class="logo"><Webhook size={18} /></span>
    <span class="name">{app.config.app_name}</span>
  </a>

  {#if app.token}
    <div class="url" title={liveTitle}>
      <span class="live-dot {app.live}"></span>
      {#if app.token.owner_id}<span class="private" title={privateTitle}><Lock /><span>Private</span></span>{/if}
      {#if app.token.description}<span class="desc">{app.token.description}</span>{/if}
      <input
        class="mono"
        readonly
        value={app.webhookUrl}
        aria-label="Your webhook URL"
        onfocus={(e) => e.currentTarget.select()}
        onclick={(e) => e.currentTarget.select()}
      />
      <CopyButton text={app.webhookUrl} class="btn primary xs" label="Copy" title="Copy URL (c)" toast="URL copied to clipboard" />
    </div>
  {/if}

  <nav class="tools">
    {#if app.token}
      <button type="button" class="btn ghost sm desk" class:on={app.forwardSettings.auto} title="Forwarding (f)" onclick={() => (app.dialog = 'forward')}>
        <Forward /> <span class="label">Forward{app.forwardSettings.auto ? ' · auto' : ''}</span>
      </button>
      <button type="button" class="btn ghost sm desk" title="Response settings (s)" onclick={() => (app.dialog = 'settings')}>
        <SlidersHorizontal /> <span class="label">Response</span>
      </button>
    {/if}
    <a class="btn ghost sm desk gh" href={REPO_URL} target="_blank" rel="noopener" title="webhookgo on GitHub: star it, fork it, report issues">
      <GitHubMark />
      {#if stats}<span class="stars"><Star />{count(stats.stars)}</span>{:else}<span class="label">GitHub</span>{/if}
    </a>
    <HistoryMenu />
    <PrefsMenu />
    {#if app.canCreate}
      <button type="button" class="btn sm new" disabled={creating} title="Create a new URL (n)" onclick={newUrl}>
        <Plus /> <span class="label">New URL</span>
      </button>
    {/if}
    {#if app.config.accounts}<UserMenu />{/if}
  </nav>
</header>

<style>
  .topbar {
    display: flex;
    align-items: center;
    gap: 12px;
    height: var(--topbar-h);
    padding: 0 12px 0 14px;
    border-bottom: 1px solid var(--border);
    background: color-mix(in srgb, var(--bg-elev) 88%, transparent);
    backdrop-filter: saturate(1.4) blur(10px);
    position: relative;
    z-index: 5;
  }
  .brand {
    display: inline-flex;
    flex: none;
    align-items: center;
    gap: 9px;
    color: var(--text);
    text-decoration: none !important;
  }
  .logo {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    border-radius: 9px;
    background: linear-gradient(140deg, var(--accent), color-mix(in srgb, var(--accent) 60%, #6d5dfc));
    color: #fff;
    box-shadow: 0 2px 8px color-mix(in srgb, var(--accent) 35%, transparent);
  }
  .name {
    font-size: 15px;
    font-weight: 700;
    letter-spacing: -0.015em;
  }
  .url {
    display: flex;
    flex: 1;
    align-items: center;
    gap: 8px;
    min-width: 0;
    max-width: 640px;
    height: 36px;
    margin-left: 8px;
    padding: 0 4px 0 12px;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--bg-sunken);
  }
  .url:focus-within {
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-soft);
  }
  .desc {
    flex: none;
    max-width: 160px;
    overflow: hidden;
    padding: 1px 7px;
    border-radius: 5px;
    background: var(--accent-soft);
    color: var(--accent-strong);
    font-size: 12px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .private {
    display: inline-flex;
    flex: none;
    align-items: center;
    gap: 4px;
    height: 22px;
    padding: 0 7px;
    border: 1px solid var(--border);
    border-radius: 5px;
    background: var(--bg-elev);
    color: var(--text-2);
    font-size: 12px;
    font-weight: 600;
    cursor: default;
  }
  .private :global(svg) {
    width: 12px;
    height: 12px;
  }
  .url input {
    flex: 1;
    min-width: 0;
    height: 100%;
    border: 0;
    outline: 0;
    background: transparent;
    font-size: 12.5px;
    text-overflow: ellipsis;
  }
  .tools {
    display: flex;
    flex: none;
    align-items: center;
    gap: 2px;
    margin-left: auto;
  }
  .new {
    margin-left: 6px;
  }
  .stars {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    font-variant-numeric: tabular-nums;
  }
  .stars :global(svg) {
    width: 13px;
    height: 13px;
  }
  @media (max-width: 1180px) {
    .tools .label {
      display: none;
    }
    .tools :global(.btn.sm) {
      width: 30px;
      padding: 0;
    }
    .tools .gh {
      width: auto;
      padding: 0 8px;
    }
  }
  @media (max-width: 899px) {
    .name,
    .desc,
    .private span {
      display: none;
    }
    .private {
      padding: 0 5px;
    }
    .topbar {
      gap: 8px;
      padding: 0 8px 0 10px;
    }
    .url {
      margin-left: 0;
      padding-left: 10px;
    }
    .new {
      margin-left: 2px;
    }
  }
  @media (max-width: 640px) {
    .desk {
      display: none;
    }
    .url input {
      font-size: 12px;
      direction: rtl; /* keep the token, not the host, visible when truncated */
      text-align: left;
    }
    .url :global(.btn span) {
      display: none;
    }
  }
</style>
