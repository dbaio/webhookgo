<script lang="ts">
  import CircleCheck from '@lucide/svelte/icons/circle-check';
  import LogIn from '@lucide/svelte/icons/log-in';
  import LogOut from '@lucide/svelte/icons/log-out';
  import { emailProblem } from './lib/account';
  import { api, isUUID } from './lib/api';
  import { app } from './lib/state.svelte';
  import AccountShell from './components/AccountShell.svelte';
  import AuthCard from './components/AuthCard.svelte';
  import Notice from './components/Notice.svelte';

  // Back to the private URL that asked for a sign-in, else home.
  const next = new URLSearchParams(location.search).get('next') ?? undefined;
  const home = isUUID(next) ? `/#/${next.toLowerCase()}` : '/';

  let email = $state('');
  let password = $state('');
  let error = $state('');
  let busy = $state(false);

  const focus = (id: string) => document.getElementById(id)?.focus();
  const autofocus = (node: HTMLElement) => node.focus();

  async function submit() {
    error = emailProblem(email.trim());
    if (error) return focus('login-email');
    if (!password) {
      error = 'Enter your password.';
      return focus('login-password');
    }
    busy = true;
    try {
      await api.login(email.trim(), password);
      location.assign(home);
    } catch (e) {
      error = (e as Error).message;
      busy = false;
      (document.getElementById('login-password') as HTMLInputElement | null)?.select();
    }
  }
</script>

<AccountShell title="Sign in">
  {#if app.user}
    <Notice icon={CircleCheck} tone="accent" title="You're signed in">
      As <strong>{app.user.email}</strong>.
      {#snippet actions()}
        <a class="btn primary" href={home}>Go to {app.config.app_name}</a>
        <button type="button" class="btn" onclick={() => app.signOut()}><LogOut /> Sign out</button>
      {/snippet}
    </Notice>
  {:else}
    <AuthCard title="Sign in" submit="Sign in" icon={LogIn} {error} {busy} onsubmit={submit}>
      {#snippet lead()}to {app.config.app_name}{/snippet}
      <div class="field">
        <label for="login-email">Email</label>
        <input
          id="login-email"
          class="input"
          type="email"
          autocomplete="username"
          spellcheck="false"
          required
          use:autofocus
          bind:value={email}
        />
      </div>
      <div class="field">
        <label for="login-password">Password</label>
        <input id="login-password" class="input" type="password" autocomplete="current-password" required bind:value={password} />
      </div>
      {#snippet footer()}Forgot your password? Ask an admin for a reset link.{/snippet}
    </AuthCard>
  {/if}
</AccountShell>
