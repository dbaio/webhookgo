<script lang="ts">
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Lock from '@lucide/svelte/icons/lock';
  import LogIn from '@lucide/svelte/icons/log-in';
  import Plus from '@lucide/svelte/icons/plus';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import MousePointerClick from '@lucide/svelte/icons/mouse-pointer-click';
  import Webhook from '@lucide/svelte/icons/webhook';
  import { onMount } from 'svelte';
  import { copyText } from './lib/clipboard';
  import { timeZone } from './lib/format';
  import { app } from './lib/state.svelte';
  import EmptyState from './components/EmptyState.svelte';
  import ForwardDialog from './components/ForwardDialog.svelte';
  import Notice from './components/Notice.svelte';
  import RequestView from './components/RequestView.svelte';
  import ShortcutsDialog from './components/ShortcutsDialog.svelte';
  import Sidebar from './components/Sidebar.svelte';
  import Toasts from './components/Toasts.svelte';
  import TokenSettings from './components/TokenSettings.svelte';
  import TopBar from './components/TopBar.svelte';

  onMount(() => {
    app.init();
  });

  // Theme: "system" follows prefers-color-scheme through CSS light-dark().
  $effect(() => {
    const t = app.prefs.theme;
    if (t === 'system') delete document.documentElement.dataset.theme;
    else document.documentElement.dataset.theme = t;
  });

  $effect(() => {
    const n = app.unread.size;
    document.title = `${n ? `(${n}) ` : ''}${app.config.app_name}`;
  });

  const close = () => (app.dialog = null);
  const showDetail = $derived(!!app.selectedId);

  // --- Resizable sidebar -------------------------------------------------------
  let dragging = $state(false);
  function startResize(e: PointerEvent) {
    const startX = e.clientX;
    const startW = app.prefs.sidebarWidth;
    dragging = true;
    const move = (ev: PointerEvent) => {
      app.prefs.sidebarWidth = Math.round(Math.min(560, Math.max(260, startW + ev.clientX - startX)));
    };
    const up = () => {
      dragging = false;
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
      app.savePrefs();
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
  }

  // --- Keyboard shortcuts ------------------------------------------------------
  function onkeydown(e: KeyboardEvent) {
    if (e.metaKey || e.ctrlKey || e.altKey || e.defaultPrevented) return;
    const t = e.target as HTMLElement;
    if (t.closest('input, textarea, select, [contenteditable]') || document.querySelector('dialog[open]')) return;
    if (app.status !== 'ready') return;
    const actions: Record<string, () => void> = {
      j: () => app.step(1),
      ArrowDown: () => app.step(1),
      k: () => app.step(-1),
      ArrowUp: () => app.step(-1),
      d: () => app.selectedId && app.deleteRequest(app.selectedId),
      Delete: () => app.selectedId && app.deleteRequest(app.selectedId),
      f: () => app.selectedId && app.forward(app.selectedId),
      c: async () => app.toast((await copyText(app.webhookUrl)) ? 'URL copied to clipboard' : 'Copy failed', 'success', 1800),
      n: () => app.createToken(),
      s: () => (app.dialog = 'settings'),
      '/': () => document.getElementById('search')?.focus(),
      t: () => {
        const dark = matchMedia('(prefers-color-scheme: dark)').matches;
        const current = app.prefs.theme === 'system' ? (dark ? 'dark' : 'light') : app.prefs.theme;
        app.prefs.theme = current === 'dark' ? 'light' : 'dark';
        app.savePrefs();
      },
      u: () => {
        app.prefs.utc = !app.prefs.utc;
        app.savePrefs();
        app.toast(app.prefs.utc ? 'Times in UTC' : `Times in your time zone (${timeZone})`, 'info', 1800);
      },
      '?': () => (app.dialog = 'shortcuts'),
      Escape: () => app.mobile && app.select(null),
    };
    const fn = actions[e.key];
    if (fn) {
      e.preventDefault();
      fn();
    }
  }
</script>

<svelte:window {onkeydown} />

<div class="shell" class:dragging style:--sidebar-w={`${app.prefs.sidebarWidth}px`}>
  <TopBar />

  {#if app.status === 'loading'}
    <div class="center"><LoaderCircle class="spin" size={22} /><span>Loading…</span></div>
  {:else if app.status === 'missing'}
    <div class="center">
      <div class="notice card">
        <TriangleAlert size={28} />
        <h1>This URL doesn't exist anymore</h1>
        <p>It may have been deleted, or it expired after {Math.round(app.config.token_ttl / 86400)} days without requests.</p>
        {#if app.canCreate}
          <button type="button" class="btn primary" onclick={() => app.createToken()}><Plus /> Create a new URL</button>
        {:else}
          <a class="btn primary" href="/login"><LogIn /> Sign in to create URLs</a>
        {/if}
      </div>
    </div>
  {:else if app.status === 'private'}
    <div class="center">
      <Notice icon={Lock} tone="accent" title="This URL is private">
        Only its owner and the admins can see it.
        {#if app.user}You're signed in as <strong>{app.user.email}</strong>.{/if}
        {#snippet actions()}
          {#if app.userLoaded && !app.user}
            <a class="btn primary" href={`/login?next=${app.privateId}`}><LogIn /> Sign in</a>
          {/if}
          {#if app.canCreate}
            <button type="button" class="btn" class:primary={!!app.user} onclick={() => app.createToken()}><Plus /> Create a new URL</button>
          {/if}
        {/snippet}
      </Notice>
    </div>
  {:else if app.status === 'welcome'}
    <div class="center">
      <Notice icon={Webhook} tone="accent" title={`Welcome to ${app.config.app_name}`}>
        Capture webhooks and other HTTP requests, and inspect them as they arrive. Here, only signed-in users create URLs.
        {#snippet actions()}<a class="btn primary" href="/login"><LogIn /> Sign in to create URLs</a>{/snippet}
      </Notice>
    </div>
  {:else if app.status === 'error'}
    <div class="center">
      <div class="notice card">
        <TriangleAlert size={28} />
        <h1>Something went wrong</h1>
        <p>{app.error}</p>
        <button type="button" class="btn primary" onclick={() => location.reload()}>Try again</button>
      </div>
    </div>
  {:else if app.empty}
    <main class="scroll"><EmptyState /></main>
  {:else if app.mobile}
    {#if showDetail}
      <main class="scroll"><RequestView /></main>
    {:else}
      <div class="mobile-list"><Sidebar /></div>
    {/if}
  {:else}
    <div class="split">
      <Sidebar />
      <div class="resizer" role="separator" aria-orientation="vertical" aria-label="Resize sidebar" onpointerdown={startResize}></div>
      <main class="scroll">
        {#if showDetail}
          <RequestView />
        {:else}
          <div class="center pick"><MousePointerClick size={22} /><span>Select a request to inspect it</span></div>
        {/if}
      </main>
    </div>
  {/if}
</div>

<TokenSettings open={app.dialog === 'settings'} onclose={close} />
<ForwardDialog open={app.dialog === 'forward'} onclose={close} />
<ShortcutsDialog open={app.dialog === 'shortcuts'} onclose={close} />
<Toasts />

<style>
  .shell {
    display: flex;
    flex-direction: column;
    height: 100dvh;
    overflow: hidden;
  }
  .shell.dragging {
    cursor: col-resize;
    user-select: none;
  }
  .split {
    display: grid;
    flex: 1;
    grid-template-columns: var(--sidebar-w) 1px minmax(0, 1fr);
    min-height: 0;
  }
  .resizer {
    position: relative;
    background: var(--border);
    cursor: col-resize;
    touch-action: none;
  }
  .resizer::after {
    content: '';
    position: absolute;
    inset: 0 -4px;
    z-index: 2;
  }
  .resizer:hover,
  .dragging .resizer {
    background: var(--accent);
  }
  .scroll {
    flex: 1;
    min-width: 0;
    min-height: 0;
    overflow-y: auto;
  }
  .mobile-list {
    flex: 1;
    min-height: 0;
  }
  .center {
    display: flex;
    flex: 1;
    align-items: center;
    justify-content: center;
    gap: 10px;
    padding: 24px;
    color: var(--text-3);
  }
  .pick {
    height: 100%;
    flex-direction: column;
  }
  .notice {
    display: grid;
    justify-items: center;
    gap: 12px;
    max-width: 440px;
    padding: 32px 28px;
    text-align: center;
    color: var(--text-2);
  }
  .notice :global(svg:first-child) {
    color: var(--warning);
  }
  .notice h1 {
    color: var(--text);
    font-size: 18px;
  }
</style>
