<script lang="ts">
  import LockOpen from '@lucide/svelte/icons/lock-open';
  import Plus from '@lucide/svelte/icons/plus';
  import Sparkles from '@lucide/svelte/icons/sparkles';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import X from '@lucide/svelte/icons/x';
  import { untrack } from 'svelte';
  import { jsonText, parseJson } from '../lib/body';
  import { ago, COMMON_STATUSES, dateTime, duration, statusText } from '../lib/format';
  import { SCHEMES } from '../lib/signature';
  import { app } from '../lib/state.svelte';
  import type { HmacAlgorithm, HmacEncoding, KV, SignatureInput, SignatureScheme, TokenInput } from '../lib/types';
  import Modal from './Modal.svelte';

  let { open, onclose }: { open: boolean; onclose: () => void } = $props();

  const CONTENT_TYPES = ['text/plain', 'application/json', 'text/html', 'application/xml'];

  let status = $state(200);
  let contentType = $state('text/plain');
  let content = $state('');
  let delay = $state(0);
  let cors = $state(false);
  let description = $state('');
  let headers = $state<KV[]>([]);
  let saving = $state(false);
  let confirmDelete = $state(false);
  let confirmRelease = $state(false);

  let sigScheme = $state<SignatureScheme | ''>('');
  let sigSecret = $state('');
  let sigHeader = $state('');
  let sigAlgorithm = $state<HmacAlgorithm>('sha256');
  let sigEncoding = $state<HmacEncoding>('hex');
  let sigPrefix = $state('');
  let sigTolerance = $state(300);
  let sigReject = $state(false);
  let sigSaved = $state<SignatureScheme | ''>(''); // scheme whose secret is stored

  // Load the current values each time the dialog opens.
  $effect(() => {
    if (!open) return;
    const t = untrack(() => app.token);
    if (!t) return;
    status = t.default_status;
    contentType = t.default_content_type;
    content = t.default_content;
    delay = t.timeout;
    cors = t.cors;
    description = t.description;
    headers = t.response_headers.map((h) => ({ ...h }));
    confirmDelete = false;
    confirmRelease = false;

    const sig = t.signature;
    sigScheme = sig?.scheme ?? '';
    sigSecret = '';
    sigHeader = sig?.header ?? '';
    sigAlgorithm = sig?.algorithm ?? 'sha256';
    sigEncoding = sig?.encoding ?? 'hex';
    sigPrefix = sig?.prefix ?? '';
    sigTolerance = sig && SCHEMES[sig.scheme].timestamped ? sig.tolerance : 300;
    sigReject = sig?.reject ?? false;
    sigSaved = sig?.has_secret ? sig.scheme : '';
  });

  const scheme = $derived(sigScheme ? SCHEMES[sigScheme] : null);
  const secretSaved = $derived(!!sigScheme && sigScheme === sigSaved);

  function signatureInput(): SignatureInput | null {
    if (!sigScheme) return null;
    const out: SignatureInput = { scheme: sigScheme, secret: sigSecret.trim(), reject: sigReject };
    if (sigScheme === 'hmac') {
      Object.assign(out, { header: sigHeader, algorithm: sigAlgorithm, encoding: sigEncoding, prefix: sigPrefix });
    }
    if (SCHEMES[sigScheme].timestamped) out.tolerance = Number(sigTolerance) || 0;
    return out;
  }

  const input = (): TokenInput => ({
    default_status: Number(status) || 200,
    default_content_type: contentType,
    default_content: content,
    timeout: Number(delay) || 0,
    cors,
    description,
    response_headers: headers.filter((h) => h.name.trim()),
    signature: signatureInput(),
  });

  // A private URL: its owner and the admins may make it anonymous again.
  const mine = $derived(!!app.token?.owner_id && app.token.owner_id === app.user?.id);
  const canRelease = $derived(!!app.token?.owner_id && (mine || app.user?.role === 'admin'));

  const jsonInvalid = $derived(contentType.includes('json') && content.trim() !== '' && !parseJson(content));

  async function save() {
    saving = true;
    const ok = await app.saveToken(input());
    saving = false;
    if (ok) onclose();
  }

  async function saveAsNew() {
    saving = true;
    const t = await app.createToken(input());
    saving = false;
    if (t) {
      app.toast(t.owner_id ? 'New private URL created with this response' : 'New URL created with this response', 'success');
      onclose();
    }
  }

  async function remove() {
    if (!confirmDelete) {
      confirmDelete = true;
      return;
    }
    onclose();
    await app.deleteToken();
  }

  async function release() {
    if (!confirmRelease) {
      confirmRelease = true;
      return;
    }
    saving = true;
    await app.releaseToken();
    saving = false;
    confirmRelease = false;
  }

  function formatBody() {
    const n = parseJson(content);
    if (n) content = jsonText(n);
  }
</script>

<Modal {open} {onclose} wide title="Response settings" subtitle="How your URL answers the requests it receives">
  {#if app.token}
    <form id="token-form" class="form" onsubmit={(e) => { e.preventDefault(); save(); }}>
      <div class="info">
        <span>Created {ago(app.token.created_at, app.now)}</span>
        {#if app.token.expires_at}
          <span title={dateTime(app.token.expires_at, app.prefs.utc)}>Expires after {duration(app.config.token_ttl)} without use</span>
        {:else}
          <span title={`Its requests are still dropped after ${duration(app.config.token_ttl)} without new ones`}>Doesn't expire</span>
        {/if}
        <span>{app.total} / {app.config.max_requests} requests kept</span>
      </div>

      <div class="field">
        <label for="desc">Name <span class="muted">(optional)</span></label>
        <input id="desc" class="input" maxlength="200" placeholder="e.g. Stripe staging" bind:value={description} />
      </div>

      <div class="row3">
        <div class="field">
          <label for="status">Status code</label>
          <input id="status" class="input mono" type="number" min="200" max="599" list="statuses" required bind:value={status} />
          <datalist id="statuses">
            {#each COMMON_STATUSES.filter((c) => c >= 200) as c (c)}<option value={c}>{statusText(c)}</option>{/each}
          </datalist>
          <span class="hint">{statusText(Number(status)) || ' '}</span>
        </div>
        <div class="field">
          <label for="delay">Delay</label>
          <div class="suffix">
            <input id="delay" class="input mono" type="number" min="0" max={app.config.max_delay} bind:value={delay} />
            <span>seconds</span>
          </div>
          <span class="hint">0 – {app.config.max_delay}s before answering</span>
        </div>
        <div class="field">
          <span class="label">CORS</span>
          <label class="toggle">
            <input type="checkbox" class="switch" bind:checked={cors} />
            <span>{cors ? 'Enabled' : 'Disabled'}</span>
          </label>
          <span class="hint">Allow browsers on any origin</span>
        </div>
      </div>

      <div class="field">
        <label for="ctype">Content-Type</label>
        <input id="ctype" class="input mono" bind:value={contentType} placeholder="text/plain" />
        <div class="picks">
          {#each CONTENT_TYPES as ct (ct)}
            <button type="button" class="btn xs" class:on={contentType === ct} onclick={() => (contentType = ct)}>{ct}</button>
          {/each}
        </div>
      </div>

      <div class="field">
        <div class="label-row">
          <label for="content">Response body</label>
          {#if contentType.includes('json')}
            <button type="button" class="btn ghost xs" disabled={jsonInvalid || !content.trim()} onclick={formatBody}><Sparkles /> Format JSON</button>
          {/if}
        </div>
        <textarea id="content" class="input" rows="7" spellcheck="false" placeholder="Empty body" bind:value={content}></textarea>
        {#if jsonInvalid}<span class="hint bad">This is not valid JSON (it will still be sent as is).</span>{/if}
      </div>

      <div class="field">
        <div class="label-row">
          <span class="label">Response headers</span>
          <button type="button" class="btn ghost xs" onclick={() => headers.push({ name: '', value: '' })}><Plus /> Add header</button>
        </div>
        {#each headers as h, i (i)}
          <div class="header-row">
            <input class="input mono" placeholder="X-Header" aria-label="Header name" bind:value={h.name} />
            <input class="input mono" placeholder="value" aria-label="Header value" bind:value={h.value} />
            <button type="button" class="btn ghost icon sm" aria-label="Remove header" onclick={() => headers.splice(i, 1)}><X /></button>
          </div>
        {:else}
          <p class="hint">No custom headers. <code>X-Request-Id</code> and <code>X-Token-Id</code> are always added.</p>
        {/each}
      </div>

      <div class="group">
        <div class="field">
          <label for="sig-scheme">Signature verification</label>
          <select id="sig-scheme" class="input" bind:value={sigScheme}>
            <option value="">Off</option>
            {#each Object.entries(SCHEMES) as [id, s] (id)}<option value={id}>{s.name}</option>{/each}
          </select>
          <span class="hint">{scheme?.hint ?? 'Check that requests really come from your provider. Each request is marked as valid, invalid, expired or missing a signature.'}</span>
        </div>

        {#if scheme}
          <div class="field">
            <label for="sig-secret">{scheme.publicKey ? 'Public key' : 'Secret'}</label>
            {#if scheme.publicKey}
              <textarea id="sig-secret" class="input mono" rows="3" spellcheck="false" placeholder={secretSaved ? 'Saved. Leave empty to keep it.' : '-----BEGIN PUBLIC KEY-----'} bind:value={sigSecret}></textarea>
            {:else}
              <input id="sig-secret" class="input mono" type="password" autocomplete="new-password" spellcheck="false" placeholder={secretSaved ? 'Saved. Leave empty to keep it.' : 'Paste the secret'} bind:value={sigSecret} />
            {/if}
            <span class="hint">{scheme.publicKey ? 'PEM, with or without line breaks, or the raw key in hex or base64.' : 'Kept on the server and never shown again.'}</span>
          </div>

          {#if sigScheme === 'hmac'}
            <div class="row4">
              <div class="field">
                <label for="sig-header">Header</label>
                <input id="sig-header" class="input mono" placeholder="X-Signature" required bind:value={sigHeader} />
              </div>
              <div class="field">
                <label for="sig-alg">Algorithm</label>
                <select id="sig-alg" class="input" bind:value={sigAlgorithm}>
                  <option value="sha256">SHA-256</option>
                  <option value="sha512">SHA-512</option>
                  <option value="sha1">SHA-1</option>
                </select>
              </div>
              <div class="field">
                <label for="sig-enc">Encoding</label>
                <select id="sig-enc" class="input" bind:value={sigEncoding}>
                  <option value="hex">hex</option>
                  <option value="base64">base64</option>
                </select>
              </div>
              <div class="field">
                <label for="sig-prefix">Prefix <span class="muted">(optional)</span></label>
                <input id="sig-prefix" class="input mono" placeholder="sha256=" bind:value={sigPrefix} />
              </div>
            </div>
          {/if}

          <div class="row3">
            {#if scheme.timestamped}
              <div class="field">
                <label for="sig-tolerance">Tolerance</label>
                <div class="suffix">
                  <input id="sig-tolerance" class="input mono" type="number" min="0" max="604800" bind:value={sigTolerance} />
                  <span>seconds</span>
                </div>
                <span class="hint">Older signed timestamps are marked expired. 0 turns this off.</span>
              </div>
            {/if}
            <div class="field">
              <span class="label">Unverified requests</span>
              <label class="toggle">
                <input type="checkbox" class="switch" bind:checked={sigReject} />
                <span>{sigReject ? 'Answer 401' : 'Answer as usual'}</span>
              </label>
              <span class="hint">They are recorded either way.</span>
            </div>
          </div>
        {/if}
      </div>

      {#if canRelease}
        <div class="group">
          <div class="field">
            <span class="label">Private URL</span>
            <span class="hint">
              Only {mine ? 'you' : 'its owner'} and the admins can see it, and it doesn't expire. Made anonymous, it leaves
              {mine ? 'your' : "its owner's"} account: anyone with the URL can see it, and it expires after
              {duration(app.config.token_ttl)} without use. This browser can save it back to {mine ? 'your' : 'an'} account from the URL menu.
            </span>
          </div>
          <div>
            <button type="button" class="btn sm" class:confirm={confirmRelease} disabled={saving} onclick={release}>
              <LockOpen /> {confirmRelease ? 'Click again to make it anonymous' : 'Make anonymous'}
            </button>
          </div>
        </div>
      {/if}
    </form>
  {/if}

  {#snippet footer()}
    <button type="button" class="btn sm" class:danger={!confirmDelete} class:confirm={confirmDelete} onclick={remove}>
      <Trash2 /> {confirmDelete ? 'Click again to delete this URL' : 'Delete URL'}
    </button>
    <span class="grow"></span>
    {#if app.canCreate}
      <button type="button" class="btn sm" disabled={saving} onclick={saveAsNew} title="Keep this URL and create another one with these settings">Save as new URL</button>
    {/if}
    <button type="submit" form="token-form" class="btn primary sm" disabled={saving}>Save</button>
  {/snippet}
</Modal>

<style>
  .form {
    display: grid;
    gap: 18px;
  }
  .info {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 16px;
    padding: 10px 12px;
    border-radius: 10px;
    background: var(--bg-sunken);
    color: var(--text-2);
    font-size: 12.5px;
  }
  .row3 {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 14px;
  }
  .group {
    display: grid;
    gap: 14px;
    padding-top: 16px;
    border-top: 1px solid var(--border);
  }
  .row4 {
    display: grid;
    grid-template-columns: minmax(0, 1.6fr) repeat(2, minmax(0, 1fr)) minmax(0, 1.2fr);
    gap: 10px;
  }
  @media (max-width: 640px) {
    .row3,
    .row4 {
      grid-template-columns: 1fr 1fr;
    }
  }
  .suffix {
    position: relative;
  }
  .suffix span {
    position: absolute;
    top: 0;
    right: 10px;
    bottom: 0;
    display: flex;
    align-items: center;
    color: var(--text-3);
    font-size: 12px;
    pointer-events: none;
  }
  .toggle {
    display: flex;
    align-items: center;
    gap: 10px;
    height: 34px;
    cursor: pointer;
  }
  .picks {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .picks .btn {
    font-family: var(--mono);
    font-size: 11.5px;
  }
  .label-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .label-row label {
    font-size: 12.5px;
    font-weight: 600;
    color: var(--text-2);
  }
  .header-row {
    display: grid;
    grid-template-columns: minmax(0, 2fr) minmax(0, 3fr) auto;
    gap: 6px;
  }
  .bad {
    color: var(--warning);
  }
  .grow {
    flex: 1;
  }
  code {
    font-size: 11.5px;
  }
</style>
