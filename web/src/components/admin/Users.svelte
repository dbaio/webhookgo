<script lang="ts">
  import Ban from '@lucide/svelte/icons/ban';
  import Ellipsis from '@lucide/svelte/icons/ellipsis';
  import KeyRound from '@lucide/svelte/icons/key-round';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Search from '@lucide/svelte/icons/search';
  import ShieldCheck from '@lucide/svelte/icons/shield-check';
  import ShieldMinus from '@lucide/svelte/icons/shield-minus';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import UserCheck from '@lucide/svelte/icons/user-check';
  import UserPlus from '@lucide/svelte/icons/user-plus';
  import UserRound from '@lucide/svelte/icons/user-round';
  import { onMount } from 'svelte';
  import { EMAIL, initials, roleLabel } from '../../lib/account';
  import { adminApi, type AdminLink, type AdminUser, type Owner } from '../../lib/admin';
  import { app } from '../../lib/state.svelte';
  import type { Page, Role } from '../../lib/types';
  import Dropdown from '../Dropdown.svelte';
  import FormError from '../FormError.svelte';
  import Modal from '../Modal.svelte';
  import ConfirmDialog from './ConfirmDialog.svelte';
  import LinkDialog from './LinkDialog.svelte';
  import Pager from './Pager.svelte';
  import When from './When.svelte';

  /** Shows a user's URLs in the URLs tab. */
  let { onurls }: { onurls: (owner: Owner) => void } = $props();

  const PER_PAGE = 25;
  const count = new Intl.NumberFormat();

  // --- List ----------------------------------------------------------------------
  let q = $state('');
  let res = $state.raw<Page<AdminUser> | null>(null);
  let loading = $state(false);
  let error = $state('');
  let seq = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;

  async function load(page = res?.current_page ?? 1) {
    const n = ++seq;
    loading = true;
    error = '';
    try {
      const r = await adminApi.listUsers({ page, perPage: PER_PAGE, q: q.trim() });
      if (n !== seq) return;
      // The last row of the last page went: show the page before.
      if (!r.data.length && page > 1 && r.total > 0) return load(Math.ceil(r.total / PER_PAGE));
      res = r;
    } catch (e) {
      if (n === seq) error = `Could not load users: ${(e as Error).message}`;
    } finally {
      if (n === seq) loading = false;
    }
  }

  onMount(() => {
    load(1);
    return () => clearTimeout(timer);
  });

  function search() {
    clearTimeout(timer);
    timer = setTimeout(() => load(1), 250);
  }

  function replace(u: AdminUser) {
    if (!res) return;
    // Keep the list's own fields if the answer leaves them out.
    res = { ...res, data: res.data.map((x) => (x.id === u.id ? { ...x, ...u } : x)) };
  }

  // --- Invite --------------------------------------------------------------------
  // Focuses the first field once the dialog it is in has opened.
  const autofocus = (node: HTMLElement) => void requestAnimationFrame(() => node.focus());

  let inviting = $state(false);
  let inviteEmail = $state('');
  let inviteRole = $state<Role>('user');
  let inviteError = $state('');
  let inviteBusy = $state(false);

  function openInvite() {
    inviteEmail = '';
    inviteRole = 'user';
    inviteError = '';
    inviting = true;
  }

  async function invite() {
    if (inviteBusy) return;
    inviteError = '';
    const email = inviteEmail.trim();
    if (!email || !EMAIL.test(email) || email.length > 254) {
      inviteError = email ? "That email address doesn't look right." : 'Enter the email address to invite.';
      return document.getElementById('invite-email')?.focus();
    }
    inviteBusy = true;
    try {
      const l = await adminApi.invite(email, inviteRole);
      inviting = false;
      shown = { kind: 'invite', email, role: inviteRole, link: l };
    } catch (e) {
      inviteError = (e as Error).message;
    } finally {
      inviteBusy = false;
    }
  }

  // --- Links ---------------------------------------------------------------------
  let shown = $state<{ kind: 'invite' | 'reset'; email: string; role: Role; link: AdminLink } | null>(null);
  let resetting = $state('');

  async function resetLink(u: AdminUser) {
    resetting = u.id;
    try {
      shown = { kind: 'reset', email: u.email, role: u.role, link: await adminApi.resetLink(u.id) };
    } catch (e) {
      app.toast(`Could not create a reset link: ${(e as Error).message}`, 'error');
    } finally {
      resetting = '';
    }
  }

  // --- Changes that ask first ----------------------------------------------------
  type Kind = 'promote' | 'demote' | 'disable' | 'delete';
  const TITLES: Record<Kind, string> = { promote: 'Make admin?', demote: 'Remove admin rights?', disable: 'Disable user?', delete: 'Delete user?' };
  const ACTIONS: Record<Kind, string> = { promote: 'Make admin', demote: 'Make user', disable: 'Disable', delete: 'Delete user' };
  let pending = $state<{ kind: Kind; user: AdminUser } | null>(null);
  let busy = $state(false);
  let actionError = $state('');

  function ask(kind: Kind, user: AdminUser) {
    actionError = '';
    pending = { kind, user };
  }

  async function confirm() {
    if (!pending || busy) return;
    const { kind, user } = pending;
    busy = true;
    actionError = '';
    try {
      if (kind === 'delete') {
        await adminApi.deleteUser(user.id);
        app.toast(`${user.email} deleted`, 'success');
        load();
      } else {
        const change = kind === 'disable' ? { disabled: true } : { role: (kind === 'promote' ? 'admin' : 'user') as Role };
        replace(await adminApi.updateUser(user.id, change));
        app.toast(
          kind === 'disable' ? `${user.email} disabled and signed out` : `${user.email} is ${kind === 'promote' ? 'an admin' : 'a user'} now`,
          'success',
        );
      }
      pending = null;
    } catch (e) {
      actionError = (e as Error).message;
    } finally {
      busy = false;
    }
  }

  let enabling = $state('');
  async function enable(u: AdminUser) {
    enabling = u.id;
    try {
      replace(await adminApi.updateUser(u.id, { disabled: false }));
      app.toast(`${u.email} can sign in again`, 'success');
    } catch (e) {
      app.toast(`Could not enable ${u.email}: ${(e as Error).message}`, 'error');
    } finally {
      enabling = '';
    }
  }

  /** A menu item's click: close the menu, then act. */
  const pick = (close: () => void, run: () => void) => () => {
    close();
    run();
  };

  const urlsOf = (n: number) => (n === 1 ? '1 private URL' : `${count.format(n)} private URLs`);
</script>

<div class="panel">
  <div class="toolbar">
    <label class="search">
      <Search />
      <input class="input" type="search" placeholder="Search by email or name" aria-label="Search users" bind:value={q} oninput={search} />
    </label>
    {#if loading}<LoaderCircle class="spin muted" size={16} />{/if}
    <span class="grow"></span>
    <button type="button" class="btn primary sm" onclick={openInvite}><UserPlus /> Invite user</button>
  </div>

  <FormError message={error} />

  <div class="card table-card">
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>User</th>
            <th>Role</th>
            <th>Status</th>
            <th class="num">URLs</th>
            <th>Created</th>
            <th>Last sign-in</th>
            <th><span class="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          {#each res?.data ?? [] as u (u.id)}
            {@const self = u.id === app.user?.id}
            <tr class:off={!!u.disabled_at}>
              <td>
                <div class="who">
                  <span class="avatar" aria-hidden="true">{initials(u)}</span>
                  <span class="names">
                    <span class="email">{u.email}{#if self}<span class="chip you">You</span>{/if}</span>
                    {#if u.name}<small>{u.name}</small>{/if}
                  </span>
                </div>
              </td>
              <td><span class="chip" class:admin={u.role === 'admin'}>{roleLabel(u.role)}</span></td>
              <td>
                {#if u.disabled_at}
                  <span class="chip bad">Disabled</span>
                  <small><When iso={u.disabled_at} /></small>
                {:else}
                  <span class="chip ok">Active</span>
                {/if}
              </td>
              <td class="num">
                {#if u.url_count > 0}
                  <button
                    type="button"
                    class="btn ghost xs count"
                    title={`Show ${u.email}'s URLs`}
                    onclick={() => onurls({ id: u.id, email: u.email })}>{count.format(u.url_count)}</button
                  >
                {:else}
                  <span class="muted">0</span>
                {/if}
              </td>
              <td><When iso={u.created_at} /></td>
              <td><When iso={u.last_login_at} empty="Never" /></td>
              <td class="actions">
                {#if self}
                  <a class="btn ghost xs" href="/account" title="Change your name or password on your account page"><UserRound /> Account</a>
                {:else}
                  <Dropdown class="btn ghost icon xs" title={`Actions for ${u.email}`} width={230}>
                    {#snippet button()}
                      {#if resetting === u.id || enabling === u.id}<LoaderCircle class="spin" />{:else}<Ellipsis />{/if}
                    {/snippet}
                    {#snippet children(close)}
                      <div class="menu-label">{u.email}</div>
                      {#if u.role === 'admin'}
                        <button type="button" class="menu-item" onclick={pick(close, () => ask('demote', u))}>
                          <ShieldMinus /> <span class="grow">Make user</span>
                        </button>
                      {:else}
                        <button type="button" class="menu-item" onclick={pick(close, () => ask('promote', u))}>
                          <ShieldCheck /> <span class="grow">Make admin</span>
                        </button>
                      {/if}
                      {#if u.disabled_at}
                        <button type="button" class="menu-item" onclick={pick(close, () => enable(u))}>
                          <UserCheck /> <span class="grow">Enable</span>
                        </button>
                      {:else}
                        <button type="button" class="menu-item" onclick={pick(close, () => ask('disable', u))}>
                          <Ban /> <span class="grow">Disable</span>
                        </button>
                      {/if}
                      <button type="button" class="menu-item" onclick={pick(close, () => resetLink(u))}>
                        <KeyRound /> <span class="grow">Reset password link</span>
                      </button>
                      <div class="menu-sep"></div>
                      <button type="button" class="menu-item danger" onclick={pick(close, () => ask('delete', u))}>
                        <Trash2 /> <span class="grow">Delete…</span>
                      </button>
                    {/snippet}
                  </Dropdown>
                {/if}
              </td>
            </tr>
          {:else}
            <tr>
              <td colspan="7" class="none">
                {#if loading}<LoaderCircle class="spin" size={14} /> Loading…{:else if !error}{q.trim() ? 'No users match.' : 'No users yet.'}{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <Pager page={res} {loading} label="Pages of users" onpage={load} />
  </div>
</div>

<Modal open={inviting} title="Invite user" subtitle="Creates a sign-up link to send them" onclose={() => !inviteBusy && (inviting = false)}>
  <form
    id="invite-form"
    class="form"
    novalidate
    onsubmit={(e) => {
      e.preventDefault();
      invite();
    }}
  >
    <div class="field">
      <label for="invite-email">Email</label>
      <input
        id="invite-email"
        class="input"
        type="email"
        autocomplete="off"
        spellcheck="false"
        required
        bind:value={inviteEmail}
        use:autofocus
      />
      <span class="hint">They choose a name and password when they open the link, and sign in with this address.</span>
    </div>
    <div class="field">
      <label for="invite-role">Role</label>
      <select id="invite-role" class="input" bind:value={inviteRole}>
        <option value="user">User</option>
        <option value="admin">Admin</option>
      </select>
      <span class="hint">
        {inviteRole === 'admin'
          ? 'Admins manage users and can see and delete every URL, private ones included.'
          : 'Users create private URLs that only they and the admins can see.'}
      </span>
    </div>
    <FormError message={inviteError} />
  </form>
  {#snippet footer()}
    <span class="grow"></span>
    <button type="button" class="btn sm" disabled={inviteBusy} onclick={() => (inviting = false)}>Cancel</button>
    <button type="submit" form="invite-form" class="btn primary sm" disabled={inviteBusy}>
      {#if inviteBusy}<LoaderCircle class="spin" />{:else}<UserPlus />{/if} Create invite link
    </button>
  {/snippet}
</Modal>

<LinkDialog link={shown?.link ?? null} title={shown?.kind === 'reset' ? 'Password reset link' : 'Invite link'} onclose={() => (shown = null)}>
  {#if shown?.kind === 'reset'}
    Send this link to <strong>{shown.email}</strong> to choose a new password. Their current password works until then.
  {:else if shown}
    Send this link to <strong>{shown.email}</strong> to create their account, as {shown.role === 'admin' ? 'an admin' : 'a user'}.
  {/if}
</LinkDialog>

<ConfirmDialog
  open={!!pending}
  title={pending ? TITLES[pending.kind] : ''}
  action={pending ? ACTIONS[pending.kind] : ''}
  danger={pending?.kind === 'disable' || pending?.kind === 'delete'}
  typed={pending?.kind === 'delete' ? pending.user.email : undefined}
  {busy}
  error={actionError}
  onconfirm={confirm}
  onclose={() => (pending = null)}
>
  {#if pending}
    {@const u = pending.user}
    {#if pending.kind === 'promote'}
      <p><strong>{u.email}</strong> will manage users and can see and delete every URL, private ones included.</p>
    {:else if pending.kind === 'demote'}
      <p><strong>{u.email}</strong> keeps the account and its URLs, but can no longer use the admin panel.</p>
    {:else if pending.kind === 'disable'}
      <p>
        <strong>{u.email}</strong> is signed out everywhere and can't sign in until you enable the account again. Their
        account and URLs are kept.
      </p>
    {:else}
      {#if u.url_count > 0}
        <p>This deletes <strong>{u.email}</strong> and <strong>{urlsOf(u.url_count)}</strong>, with every request they captured.</p>
      {:else}
        <p>This deletes <strong>{u.email}</strong>. They have no private URLs.</p>
      {/if}
      <p>It can't be undone.</p>
    {/if}
  {/if}
</ConfirmDialog>

<style>
  .who {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 200px;
  }
  .avatar {
    display: grid;
    flex: none;
    place-items: center;
    width: 28px;
    height: 28px;
    border-radius: 50%;
    background: var(--accent-soft);
    color: var(--accent-strong);
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.02em;
  }
  .off .avatar {
    background: var(--bg-sunken);
    color: var(--text-3);
  }
  .names {
    display: grid;
    min-width: 0;
  }
  .email {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-weight: 500;
    white-space: nowrap;
  }
  .off .email {
    color: var(--text-3);
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
  .chip.you {
    height: 18px;
    padding: 0 6px;
    font-size: 10.5px;
    font-weight: 600;
  }
  .count {
    min-width: 28px;
    color: var(--accent-strong);
    font-variant-numeric: tabular-nums;
  }
  .menu-item.danger,
  .menu-item.danger :global(svg) {
    color: var(--danger);
  }
  .menu-label {
    overflow: hidden;
    text-align: left;
    text-overflow: ellipsis;
    text-transform: none;
    letter-spacing: 0;
    white-space: nowrap;
  }
  .form {
    display: grid;
    gap: 14px;
  }
  .grow {
    flex: 1;
  }
</style>
