<script lang="ts">
  // /setup, /invite and /reset: one-time links that create the first admin,
  // accept an invitation or set a new password, then sign in.
  import KeyRound from '@lucide/svelte/icons/key-round';
  import Link2Off from '@lucide/svelte/icons/link-2-off';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import ShieldCheck from '@lucide/svelte/icons/shield-check';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import UserRoundPlus from '@lucide/svelte/icons/user-round-plus';
  import { onMount } from 'svelte';
  import { emailProblem, NAME_MAX, PASSWORD_MIN, passwordProblem } from './lib/account';
  import { api, ApiError } from './lib/api';
  import { app } from './lib/state.svelte';
  import type { LinkInfo, RedeemInput } from './lib/types';
  import AccountShell from './components/AccountShell.svelte';
  import AuthCard from './components/AuthCard.svelte';
  import Notice from './components/Notice.svelte';

  const titles: Record<string, string> = { '/setup': 'Set up', '/invite': 'Invitation', '/reset': 'Reset password' };

  let token = '';
  let link = $state<LinkInfo | null>(null);
  let phase = $state<'checking' | 'ready' | 'invalid' | 'error'>('checking');
  let loadError = $state('');

  let email = $state('');
  let name = $state('');
  let password = $state('');
  let confirm = $state('');
  let error = $state('');
  let busy = $state(false);

  const card = $derived(
    link &&
      {
        setup: { title: 'Create the first admin', submit: 'Create admin account', icon: ShieldCheck },
        invite: { title: `Join ${app.config.app_name}`, submit: 'Create account', icon: UserRoundPlus },
        reset: { title: 'Choose a new password', submit: 'Set password', icon: KeyRound },
      }[link.kind],
  );

  // The secret travels in the fragment, which never reaches the server or its
  // logs. Take it, then drop it from the address bar and the history.
  onMount(() => {
    token = location.hash.slice(1).trim();
    if (location.hash) history.replaceState(history.state, '', location.pathname + location.search);
    inspect();
  });

  async function inspect() {
    if (!token) {
      phase = 'invalid';
      return;
    }
    phase = 'checking';
    try {
      link = await api.inspectLink(token);
      email = link.email;
      phase = 'ready';
    } catch (e) {
      if (e instanceof ApiError && (e.status === 404 || e.status === 400)) phase = 'invalid';
      else {
        loadError = (e as Error).message;
        phase = 'error';
      }
    }
  }

  // Focuses the first field to fill in when the form shows.
  function autofocus(node: HTMLElement, on: boolean) {
    if (on) node.focus();
  }

  const focus = (id: string) => document.getElementById(id)?.focus();

  async function submit() {
    if (!link) return;
    error = '';
    const input: RedeemInput = { token, password };
    if (link.kind === 'setup') {
      error = emailProblem(email.trim());
      if (error) return focus('redeem-email');
      input.email = email.trim();
    }
    if (link.kind !== 'reset') {
      if (!name.trim()) {
        error = 'Enter your name.';
        return focus('redeem-name');
      }
      input.name = name.trim();
    }
    error = passwordProblem(password, confirm);
    if (error) return focus(password === confirm || !confirm ? 'redeem-password' : 'redeem-confirm');
    busy = true;
    try {
      await api.redeemLink(input);
      location.assign('/');
    } catch (e) {
      busy = false;
      // Used or expired since it was opened: nothing on this form can fix that.
      if (e instanceof ApiError && e.status === 404) phase = 'invalid';
      else error = (e as Error).message;
    }
  }
</script>

<AccountShell title={titles[location.pathname] ?? 'Account'}>
  {#if phase === 'checking'}
    <p class="checking"><LoaderCircle class="spin" size={20} /> Checking the link…</p>
  {:else if phase === 'invalid'}
    <Notice icon={Link2Off} title="This link is invalid, used or expired">
      Each link works once, and only for a while. If you reloaded this page, open the link again from where you got it;
      otherwise, ask for a new one.
      {#snippet actions()}<a class="btn primary" href="/">Go to {app.config.app_name}</a>{/snippet}
    </Notice>
  {:else if phase === 'error'}
    <Notice icon={TriangleAlert} title="Something went wrong">
      {loadError}
      {#snippet actions()}<button type="button" class="btn primary" onclick={inspect}>Try again</button>{/snippet}
    </Notice>
  {:else if link && card}
    <AuthCard title={card.title} submit={card.submit} icon={card.icon} {error} {busy} onsubmit={submit}>
      {#snippet lead()}
        {#if link?.kind === 'setup'}
          This instance has no admin yet. The account you create here manages it.
        {:else if link?.kind === 'invite'}
          You were invited{link.role === 'admin' ? ' as an admin' : ''}. Pick a name and a password to sign in.
        {:else}
          For <strong>{email}</strong>. You will be signed out everywhere else.
        {/if}
      {/snippet}

      {#if link.kind === 'reset'}
        <!-- Lets password managers file the new password under the right account. -->
        <input type="email" autocomplete="username" value={email} readonly hidden />
      {:else}
        <div class="field">
          <label for="redeem-email">Email</label>
          <input
            id="redeem-email"
            class="input"
            type="email"
            autocomplete="username"
            spellcheck="false"
            required
            readonly={link.kind === 'invite'}
            use:autofocus={link.kind === 'setup'}
            bind:value={email}
          />
        </div>
        <div class="field">
          <label for="redeem-name">Name</label>
          <input
            id="redeem-name"
            class="input"
            autocomplete="name"
            maxlength={NAME_MAX}
            required
            use:autofocus={link.kind === 'invite'}
            bind:value={name}
          />
        </div>
      {/if}
      <div class="field">
        <label for="redeem-password">{link.kind === 'reset' ? 'New password' : 'Password'}</label>
        <input
          id="redeem-password"
          class="input"
          type="password"
          autocomplete="new-password"
          aria-describedby="redeem-password-hint"
          required
          use:autofocus={link.kind === 'reset'}
          bind:value={password}
        />
        <span id="redeem-password-hint" class="hint">At least {PASSWORD_MIN} characters.</span>
      </div>
      <div class="field">
        <label for="redeem-confirm">Confirm password</label>
        <input id="redeem-confirm" class="input" type="password" autocomplete="new-password" required bind:value={confirm} />
      </div>
    </AuthCard>
  {/if}
</AccountShell>

<style>
  .checking {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 24px;
    color: var(--text-3);
  }
</style>
