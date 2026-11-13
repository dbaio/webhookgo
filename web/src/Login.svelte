<script lang="ts">
  import CircleCheck from '@lucide/svelte/icons/circle-check';
  import LogIn from '@lucide/svelte/icons/log-in';
  import LogOut from '@lucide/svelte/icons/log-out';
  import { emailProblem } from './lib/account';
  import { api, isUUID } from './lib/api';
  import { app } from './lib/state.svelte';
  import AccountShell from './components/AccountShell.svelte';
  import AuthCard from './components/AuthCard.svelte';
  import FormError from './components/FormError.svelte';
  import GoogleMark from './components/GoogleMark.svelte';
  import Notice from './components/Notice.svelte';

  const params = new URLSearchParams(location.search);
  // Back to the private URL or the admin panel that asked for a sign-in, else home.
  const next = params.get('next') ?? undefined;
  const home = isUUID(next) ? `/#/${next.toLowerCase()}` : next === '/admin' ? next : '/';
  // The server goes to the same place after Google, from the same ?next=.
  const google = `/api/auth/google${next ? `?next=${encodeURIComponent(next)}` : ''}`;

  // Why a sign-in with Google came back here (?error= from the server).
  const GOOGLE_ERRORS: Record<string, string> = {
    google_cancelled: 'Signing in with Google was cancelled.',
    google_expired: 'That sign-in with Google took too long, or was started in another tab. Try again.',
    google_failed: "Signing in with Google didn't work. Try again; if it keeps failing, tell the admins.",
    google_unverified: "Google hasn't verified that account's email address, so it can't sign in here.",
    google_address: "Use a Gmail or Google Workspace account: Google can't vouch for that one's address. Or sign in with your password.",
    google_domain: "That Google account can't sign in here. Try another account, or ask an admin for an invitation.",
    disabled: 'This account is disabled.',
    rate_limited: 'Too many sign-in attempts. Wait a minute and try again.',
  };
  const returned = params.get('error');
  if (returned !== null) {
    // Once: a reload or a bookmark shouldn't show it again.
    params.delete('error');
    history.replaceState(null, '', location.pathname + (params.size ? `?${params}` : ''));
  }

  let email = $state('');
  let password = $state('');
  let error = $state('');
  // Shown under the Google button, until the password form is used.
  let googleError = $state(returned === null ? '' : (GOOGLE_ERRORS[returned] ?? GOOGLE_ERRORS.google_failed));
  let busy = $state(false);

  const focus = (id: string) => document.getElementById(id)?.focus();
  // With Google on, nothing is focused: the button may be all most people need.
  const autofocus = (node: HTMLElement, on = true) => {
    if (on) node.focus();
  };

  async function submit() {
    googleError = '';
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
    <!-- Should Google be off by now, its error shows with the form's. -->
    <AuthCard title="Sign in" submit="Sign in" icon={LogIn} error={error || (app.config.google ? '' : googleError)} {busy} onsubmit={submit}>
      {#snippet lead()}to {app.config.app_name}{/snippet}
      {#if app.config.google}
        <a class="btn google" href={google}><GoogleMark size={18} /> Continue with Google</a>
        <FormError message={googleError} />
        <div class="or" role="separator"><span>or with your password</span></div>
      {/if}
      <div class="field">
        <label for="login-email">Email</label>
        <input
          id="login-email"
          class="input"
          type="email"
          autocomplete="username"
          spellcheck="false"
          required
          use:autofocus={!app.config.google}
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

<style>
  .google {
    gap: 10px;
    height: 40px;
    font-size: 14px;
  }
  .or {
    display: flex;
    align-items: center;
    gap: 10px;
    color: var(--text-3);
    font-size: 12px;
  }
  .or::before,
  .or::after {
    flex: 1;
    border-top: 1px solid var(--border);
    content: '';
  }
</style>
