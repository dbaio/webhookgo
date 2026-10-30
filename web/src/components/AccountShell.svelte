<script lang="ts">
  // The frame of the account pages (sign-in, one-time links, account): the
  // abuse page's header bar, then the page itself once the config says
  // accounts are on and the server has said who is signed in.
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import Webhook from '@lucide/svelte/icons/webhook';
  import { onMount, type Snippet } from 'svelte';
  import { api } from '../lib/api';
  import { app } from '../lib/state.svelte';
  import Notice from './Notice.svelte';
  import Toasts from './Toasts.svelte';

  let { title, children }: { title: string; children: Snippet } = $props();

  let phase = $state<'loading' | 'ready' | 'off' | 'error'>('loading');
  let error = $state('');

  async function load() {
    phase = 'loading';
    try {
      app.config = await api.config();
      if (app.config.accounts) await app.loadUser();
      phase = app.config.accounts ? 'ready' : 'off';
    } catch (e) {
      error = (e as Error).message;
      phase = 'error';
    }
  }

  onMount(() => {
    load();
  });

  $effect(() => {
    document.title = `${title} · ${app.config.app_name}`;
  });
</script>

<div class="page">
  <header class="bar">
    <a class="brand" href="/" title={`Open ${app.config.app_name}`}>
      <span class="logo"><Webhook size={18} /></span>
      <span class="name">{app.config.app_name}</span>
    </a>
  </header>

  <main>
    {#if phase === 'loading'}
      <p class="loading"><LoaderCircle class="spin" size={20} /> Loading…</p>
    {:else if phase === 'off'}
      <Notice icon={TriangleAlert} title="Accounts are off">
        This {app.config.app_name} instance doesn't use accounts: anyone can create URLs without signing in.
        {#snippet actions()}<a class="btn primary" href="/">Go to {app.config.app_name}</a>{/snippet}
      </Notice>
    {:else if phase === 'error'}
      <Notice icon={TriangleAlert} title="Something went wrong">
        {error}
        {#snippet actions()}<button type="button" class="btn primary" onclick={load}>Try again</button>{/snippet}
      </Notice>
    {:else}
      {@render children()}
    {/if}
  </main>
</div>

<Toasts />

<style>
  .page {
    height: 100%;
    overflow-y: auto;
  }
  .bar {
    display: flex;
    align-items: center;
    height: var(--topbar-h);
    padding: 0 14px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-elev);
  }
  .brand {
    display: inline-flex;
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
  }
  .name {
    font-size: 15px;
    font-weight: 700;
    letter-spacing: -0.015em;
  }
  main {
    display: grid;
    justify-items: center;
    align-content: start;
    gap: 18px;
    padding: 48px 16px max(48px, env(safe-area-inset-bottom));
  }
  .loading {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 24px;
    color: var(--text-3);
  }
  @media (max-width: 640px) {
    main {
      padding-top: 24px;
    }
  }
</style>
