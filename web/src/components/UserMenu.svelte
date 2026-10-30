<script lang="ts">
  // The top bar's account control, with accounts on: "Sign in", or an avatar
  // with a menu. Nothing shows until the server has said who is signed in.
  import LogIn from '@lucide/svelte/icons/log-in';
  import LogOut from '@lucide/svelte/icons/log-out';
  import UserRound from '@lucide/svelte/icons/user-round';
  import { initials, roleLabel } from '../lib/account';
  import { app } from '../lib/state.svelte';
  import Dropdown from './Dropdown.svelte';
</script>

<div class="account">
  {#if app.user}
    {@const u = app.user}
    <Dropdown class="btn ghost icon sm" title={`Signed in as ${u.email}`} width={280}>
      {#snippet button()}<span class="avatar" aria-hidden="true">{initials(u)}</span>{/snippet}
      {#snippet children(close)}
        <div class="who">
          <span class="avatar lg" aria-hidden="true">{initials(u)}</span>
          <span class="grow">
            <span class="title">{u.name || u.email}</span>
            {#if u.name}<span class="sub">{u.email}</span>{/if}
          </span>
          <span class="chip" class:admin={u.role === 'admin'}>{roleLabel(u.role)}</span>
        </div>
        <div class="menu-sep"></div>
        <a class="menu-item" href="/account" onclick={close}><UserRound /> <span class="grow">Account</span></a>
        <button
          type="button"
          class="menu-item"
          onclick={() => {
            close();
            app.signOut();
          }}><LogOut /> <span class="grow">Sign out</span></button
        >
      {/snippet}
    </Dropdown>
  {:else if app.userLoaded}
    <a class="btn ghost sm" href="/login" title="Sign in" aria-label="Sign in"><LogIn /> <span class="label">Sign in</span></a>
  {/if}
</div>

<style>
  .account {
    display: flex;
    flex: none;
    margin-left: 4px;
  }
  .avatar {
    display: grid;
    flex: none;
    place-items: center;
    width: 24px;
    height: 24px;
    border-radius: 50%;
    background: var(--accent-soft);
    color: var(--accent-strong);
    font-size: 10.5px;
    font-weight: 700;
    letter-spacing: 0.02em;
  }
  .avatar.lg {
    width: 34px;
    height: 34px;
    font-size: 13px;
  }
  .who {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 10px 6px;
  }
  .who .grow {
    display: grid;
    flex: 1;
    min-width: 0;
    gap: 1px;
  }
  .title {
    overflow: hidden;
    font-size: 13px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .sub {
    overflow: hidden;
    color: var(--text-3);
    font-size: 11.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .chip {
    height: 20px;
    font-size: 11px;
  }
  .chip.admin {
    border-color: transparent;
    background: var(--accent-soft);
    color: var(--accent-strong);
  }
  /* The top bar turns its small buttons into icons below this width. */
  @media (max-width: 1180px) {
    .label {
      display: none;
    }
  }
</style>
