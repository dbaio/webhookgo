<script lang="ts">
  import KeyRound from '@lucide/svelte/icons/key-round';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import LogIn from '@lucide/svelte/icons/log-in';
  import LogOut from '@lucide/svelte/icons/log-out';
  import MonitorSmartphone from '@lucide/svelte/icons/monitor-smartphone';
  import Save from '@lucide/svelte/icons/save';
  import { initials, NAME_MAX, PASSWORD_MIN, passwordProblem, roleLabel } from './lib/account';
  import { api, ApiError } from './lib/api';
  import { app } from './lib/state.svelte';
  import AccountShell from './components/AccountShell.svelte';
  import ApiKeys from './components/ApiKeys.svelte';
  import FormError from './components/FormError.svelte';
  import Notice from './components/Notice.svelte';

  const day = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' });
  const focus = (id: string) => document.getElementById(id)?.focus();

  // --- Name --------------------------------------------------------------------
  let name = $state('');
  let nameError = $state('');
  let savingName = $state(false);

  // Fill in the saved name when the user loads, and again after each save.
  $effect(() => {
    name = app.user?.name ?? '';
  });

  async function saveName() {
    if (savingName) return;
    nameError = '';
    savingName = true;
    try {
      app.user = await api.updateAccount({ name: name.trim() });
      app.toast('Name saved', 'success');
    } catch (e) {
      nameError = (e as Error).message;
    } finally {
      savingName = false;
    }
  }

  // --- Password ----------------------------------------------------------------
  let current = $state('');
  let next = $state('');
  let confirm = $state('');
  let passwordError = $state('');
  let changing = $state(false);

  async function changePassword() {
    if (changing) return;
    passwordError = '';
    if (!current) {
      passwordError = 'Enter your current password.';
      return focus('account-current');
    }
    passwordError = passwordProblem(next, confirm);
    if (passwordError) return focus(next === confirm || !confirm ? 'account-new' : 'account-confirm');
    changing = true;
    try {
      await api.changePassword(current, next);
      current = next = confirm = '';
      app.toast('Password changed. Your other devices were signed out.', 'success');
    } catch (e) {
      passwordError = (e as Error).message;
      if (e instanceof ApiError && e.status === 403) (document.getElementById('account-current') as HTMLInputElement | null)?.select();
    } finally {
      changing = false;
    }
  }

  // --- Sessions ----------------------------------------------------------------
  let sessionError = $state('');
  let endingOthers = $state(false);

  async function logoutOthers() {
    sessionError = '';
    endingOthers = true;
    try {
      await api.logoutOthers();
      app.toast('Signed out of your other devices', 'success');
    } catch (e) {
      sessionError = (e as Error).message;
    } finally {
      endingOthers = false;
    }
  }
</script>

<AccountShell title="Account">
  {#if app.user}
    {@const u = app.user}
    <div class="account">
      <header class="head">
        <span class="avatar" aria-hidden="true">{initials(u)}</span>
        <div class="who">
          <h1>{u.name || u.email}</h1>
          <p>
            {#if u.name}<span class="email">{u.email}</span> ·{/if}
            <span class="chip" class:admin={u.role === 'admin'}>{roleLabel(u.role)}</span>
            · joined {day.format(new Date(u.created_at))}
          </p>
        </div>
      </header>

      <section class="card" aria-labelledby="account-profile">
        <h2 id="account-profile">Profile</h2>
        <form
          class="form"
          novalidate
          onsubmit={(e) => {
            e.preventDefault();
            saveName();
          }}
        >
          <div class="field">
            <label for="account-name">Name</label>
            <input id="account-name" class="input" autocomplete="name" maxlength={NAME_MAX} bind:value={name} />
            <span class="hint">Shown instead of your email address. Your email can't be changed here.</span>
          </div>
          <FormError message={nameError} />
          <div class="actions">
            <button type="submit" class="btn primary sm" disabled={savingName || name.trim() === u.name}>
              {#if savingName}<LoaderCircle class="spin" />{:else}<Save />{/if} Save
            </button>
          </div>
        </form>
      </section>

      <section class="card" aria-labelledby="account-password">
        <h2 id="account-password">Password</h2>
        <form
          class="form"
          novalidate
          aria-busy={changing}
          onsubmit={(e) => {
            e.preventDefault();
            changePassword();
          }}
        >
          <!-- Lets password managers file the new password under the right account. -->
          <input type="email" autocomplete="username" value={u.email} readonly hidden />
          <div class="field">
            <label for="account-current">Current password</label>
            <input id="account-current" class="input" type="password" autocomplete="current-password" required bind:value={current} />
          </div>
          <div class="field">
            <label for="account-new">New password</label>
            <input
              id="account-new"
              class="input"
              type="password"
              autocomplete="new-password"
              aria-describedby="account-new-hint"
              required
              bind:value={next}
            />
            <span id="account-new-hint" class="hint">At least {PASSWORD_MIN} characters. Your other devices will be signed out.</span>
          </div>
          <div class="field">
            <label for="account-confirm">Confirm new password</label>
            <input id="account-confirm" class="input" type="password" autocomplete="new-password" required bind:value={confirm} />
          </div>
          <FormError message={passwordError} />
          <div class="actions">
            <button type="submit" class="btn primary sm">
              {#if changing}<LoaderCircle class="spin" />{:else}<KeyRound />{/if} Change password
            </button>
          </div>
        </form>
      </section>

      <section class="card" aria-labelledby="account-sessions">
        <h2 id="account-sessions">Sessions</h2>
        <div class="row">
          <p class="grow">
            <strong>Other devices</strong>
            <small>Signs out every other browser where you are signed in. This one stays signed in.</small>
          </p>
          <button type="button" class="btn sm" disabled={endingOthers} onclick={logoutOthers}>
            {#if endingOthers}<LoaderCircle class="spin" />{:else}<MonitorSmartphone />{/if} Sign out of other devices
          </button>
        </div>
        <div class="row">
          <p class="grow">
            <strong>This browser</strong>
            <small>Ends your session here. You can sign in again at any time.</small>
          </p>
          <button type="button" class="btn sm" onclick={() => app.signOut()}><LogOut /> Sign out</button>
        </div>
        <FormError message={sessionError} />
      </section>

      <ApiKeys />
    </div>
  {:else}
    <Notice icon={LogIn} tone="accent" title="You're signed out">
      Sign in to see your account.
      {#snippet actions()}<a class="btn primary" href="/login"><LogIn /> Sign in</a>{/snippet}
    </Notice>
  {/if}
</AccountShell>

<style>
  .account {
    display: grid;
    gap: 16px;
    width: 100%;
    max-width: 560px;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 14px;
    margin-bottom: 4px;
  }
  .avatar {
    display: grid;
    flex: none;
    place-items: center;
    width: 52px;
    height: 52px;
    border-radius: 50%;
    background: var(--accent-soft);
    color: var(--accent-strong);
    font-size: 19px;
    font-weight: 700;
    letter-spacing: 0.02em;
  }
  .who {
    min-width: 0;
  }
  h1 {
    overflow: hidden;
    font-size: 22px;
    letter-spacing: -0.015em;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .who p {
    color: var(--text-3);
    font-size: 13px;
    overflow-wrap: anywhere;
  }
  .email {
    color: var(--text-2);
  }
  .chip {
    height: 20px;
    font-size: 11.5px;
  }
  .chip.admin {
    border-color: transparent;
    background: var(--accent-soft);
    color: var(--accent-strong);
  }
  section {
    display: grid;
    gap: 14px;
    padding: 18px 20px 20px;
  }
  h2 {
    font-size: 15px;
    font-weight: 650;
    letter-spacing: -0.01em;
  }
  .form {
    display: grid;
    gap: 14px;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px 16px;
  }
  .row + .row {
    padding-top: 14px;
    border-top: 1px solid var(--border);
  }
  .grow {
    flex: 1;
    min-width: 220px;
  }
  small {
    display: block;
    color: var(--text-3);
    font-size: 12px;
  }
  @media (max-width: 480px) {
    section {
      padding: 16px;
    }
    .row .btn {
      width: 100%;
    }
  }
</style>
