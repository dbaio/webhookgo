<script lang="ts">
  // The admin panel (/admin): numbers, users, URLs and the log, one tab each,
  // with the tab in the address (#users, #urls/<id>…).
  import type { LucideIcon } from '@lucide/svelte';
  import LayoutDashboard from '@lucide/svelte/icons/layout-dashboard';
  import Link from '@lucide/svelte/icons/link';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import ScrollText from '@lucide/svelte/icons/scroll-text';
  import ShieldAlert from '@lucide/svelte/icons/shield-alert';
  import ShieldCheck from '@lucide/svelte/icons/shield-check';
  import Users from '@lucide/svelte/icons/users';
  import { onMount } from 'svelte';
  import { findUUID, LOGIN_FOR_ADMIN, type Owner } from './lib/admin';
  import { app } from './lib/state.svelte';
  import AccountShell from './components/AccountShell.svelte';
  import Notice from './components/Notice.svelte';
  import Log from './components/admin/Log.svelte';
  import Overview from './components/admin/Overview.svelte';
  import UrlsTab from './components/admin/Urls.svelte';
  import UsersTab from './components/admin/Users.svelte';

  type Tab = 'overview' | 'users' | 'urls' | 'log';
  const TABS: { id: Tab; label: string; icon: LucideIcon }[] = [
    { id: 'overview', label: 'Overview', icon: LayoutDashboard },
    { id: 'users', label: 'Users', icon: Users },
    { id: 'urls', label: 'URLs', icon: Link },
    { id: 'log', label: 'Log', icon: ScrollText },
  ];

  let tab = $state<Tab>('overview');
  let lookup = $state(''); // #urls/<id>
  let owner = $state<Owner | null>(null); // the URL list's owner filter

  function route() {
    const [t, rest = ''] = location.hash.replace(/^#\/?/, '').split('/');
    tab = TABS.some((x) => x.id === t) ? (t as Tab) : 'overview';
    lookup = tab === 'urls' ? findUUID(rest) : '';
  }
  route();

  onMount(() => {
    const ticker = setInterval(() => (app.now = Date.now()), 30_000);
    addEventListener('hashchange', route);
    return () => {
      clearInterval(ticker);
      removeEventListener('hashchange', route);
    };
  });

  // Signed out: sign in, then come back here.
  const signedOut = $derived(app.config.accounts && app.userLoaded && !app.user);
  $effect(() => {
    if (signedOut) location.replace(LOGIN_FOR_ADMIN);
  });

  function go(id: Tab) {
    if (id !== tab || lookup) location.hash = `#${id}`;
  }

  // Arrow keys move between tabs (and show them), as in any tab list.
  function onkeydown(e: KeyboardEvent) {
    const i = TABS.findIndex((x) => x.id === tab);
    const next = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: TABS.length - 1 }[e.key];
    if (next === undefined) return;
    e.preventDefault();
    const t = TABS[(next + TABS.length) % TABS.length];
    go(t.id);
    document.getElementById(`admin-tab-${t.id}`)?.focus();
  }

  function showUrlsOf(o: Owner) {
    owner = o;
    location.hash = '#urls';
  }

  const label = $derived(TABS.find((x) => x.id === tab)?.label ?? '');
</script>

<AccountShell title={`${label} · Admin`}>
  {#if app.user?.role === 'admin'}
    <div class="admin">
      <header class="head">
        <div class="grow">
          <h1><ShieldCheck size={22} /> Admin</h1>
          <p>Every action here is logged, including opening someone's private URL.</p>
        </div>
        <p class="me">
          <span class="muted">Signed in as</span> <a href="/account">{app.user.email}</a>
          <span class="muted">·</span> <a href="/">Back to {app.config.app_name}</a>
        </p>
      </header>

      <div class="tabs" role="tablist" aria-label="Admin sections" tabindex="-1" {onkeydown}>
        {#each TABS as t (t.id)}
          {@const Icon = t.icon}
          <button
            type="button"
            role="tab"
            id={`admin-tab-${t.id}`}
            class="tab"
            class:on={tab === t.id}
            aria-selected={tab === t.id}
            aria-controls="admin-panel"
            tabindex={tab === t.id ? 0 : -1}
            onclick={() => go(t.id)}
          >
            <Icon /> {t.label}
          </button>
        {/each}
      </div>

      <div id="admin-panel" class="tabpanel" role="tabpanel" aria-labelledby={`admin-tab-${tab}`}>
        {#if tab === 'overview'}
          <Overview />
        {:else if tab === 'users'}
          <UsersTab onurls={showUrlsOf} />
        {:else if tab === 'urls'}
          <UrlsTab {lookup} bind:owner />
        {:else}
          <Log />
        {/if}
      </div>
    </div>
  {:else if app.user}
    <Notice icon={ShieldAlert} title="Admins only">
      You're signed in as <strong>{app.user.email}</strong>, which isn't an admin account.
      {#snippet actions()}
        <a class="btn primary" href="/">Go to {app.config.app_name}</a>
        <a class="btn" href="/account">Your account</a>
      {/snippet}
    </Notice>
  {:else}
    <p class="loading"><LoaderCircle class="spin" size={20} /> Taking you to sign in…</p>
  {/if}
</AccountShell>

<style>
  .admin {
    display: grid;
    gap: 16px;
    width: 100%;
    max-width: 1180px;
    min-width: 0;
  }
  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: 8px 16px;
  }
  .grow {
    flex: 1;
    min-width: 0;
  }
  h1 {
    display: flex;
    align-items: center;
    gap: 9px;
    font-size: 22px;
    letter-spacing: -0.015em;
  }
  h1 :global(svg) {
    color: var(--accent);
  }
  .head p {
    margin-top: 2px;
    color: var(--text-3);
    font-size: 13px;
  }
  .head .me {
    overflow-wrap: anywhere;
  }
  .tabs {
    display: flex;
    gap: 2px;
    overflow-x: auto;
    border-bottom: 1px solid var(--border);
  }
  .tabs:focus {
    outline: none;
  }
  .tab {
    display: inline-flex;
    flex: none;
    align-items: center;
    gap: 7px;
    height: 38px;
    margin-bottom: -1px;
    padding: 0 12px;
    border: 0;
    border-bottom: 2px solid transparent;
    background: transparent;
    color: var(--text-2);
    font-size: 13.5px;
    font-weight: 500;
    cursor: pointer;
  }
  .tab:hover {
    color: var(--text);
  }
  .tab:focus-visible {
    outline-offset: -2px;
    border-radius: var(--radius-xs) var(--radius-xs) 0 0;
  }
  .tab.on {
    border-bottom-color: var(--accent);
    color: var(--text);
  }
  .tab :global(svg) {
    width: 16px;
    height: 16px;
    color: var(--text-3);
  }
  .tab.on :global(svg) {
    color: var(--accent);
  }
  .tabpanel {
    min-width: 0;
  }
  .loading {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 24px;
    color: var(--text-3);
  }

  /* Shared by the tabs: a toolbar, then a card holding a table that scrolls
     sideways on its own when it is wider than the screen. */
  .admin :global(.panel) {
    display: grid;
    gap: 12px;
    min-width: 0;
  }
  .admin :global(.toolbar) {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }
  .admin :global(.search) {
    position: relative;
    display: block;
    flex: 0 1 320px;
    min-width: 0;
  }
  .admin :global(.search svg) {
    position: absolute;
    top: 8px;
    left: 9px;
    width: 14px;
    height: 14px;
    color: var(--text-3);
    pointer-events: none;
  }
  .admin :global(.search .input) {
    height: 30px;
    padding-left: 29px;
    font-size: 13px;
  }
  .admin :global(.table-card) {
    min-width: 0;
    overflow: hidden;
  }
  .admin :global(.table-wrap) {
    position: relative; /* holds the .sr-only headers, which would widen the page */
    overflow-x: auto;
  }
  .admin :global(table) {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
  }
  .admin :global(th),
  .admin :global(td) {
    padding: 9px 12px;
    border-bottom: 1px solid var(--border);
    text-align: left;
    vertical-align: middle;
  }
  .admin :global(th) {
    background: var(--bg-sunken);
    color: var(--text-3);
    font-size: 12px;
    font-weight: 600;
    white-space: nowrap;
  }
  .admin :global(tbody tr:last-child td) {
    border-bottom: 0;
  }
  .admin :global(tbody tr:hover td) {
    background: var(--bg-hover);
  }
  .admin :global(td small) {
    display: block;
    color: var(--text-3);
    font-size: 11.5px;
  }
  .admin :global(.num) {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .admin :global(td.actions) {
    width: 1%;
    text-align: right;
    white-space: nowrap;
  }
  .admin :global(td.none) {
    padding: 28px 12px;
    color: var(--text-3);
    text-align: center;
  }
  .admin :global(td.none svg) {
    vertical-align: -2px;
  }
  .admin :global(.toolbar > svg.spin) {
    color: var(--text-3);
  }
  @media (max-width: 640px) {
    .head .me {
      width: 100%;
    }
    .admin :global(.search) {
      flex: 1 1 100%;
    }
  }
</style>
