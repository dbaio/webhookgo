<script lang="ts">
  import ArrowLeft from '@lucide/svelte/icons/arrow-left';
  import ChevronDown from '@lucide/svelte/icons/chevron-down';
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import Code from '@lucide/svelte/icons/code';
  import ExternalLink from '@lucide/svelte/icons/external-link';
  import Forward from '@lucide/svelte/icons/forward';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Paperclip from '@lucide/svelte/icons/paperclip';
  import Settings2 from '@lucide/svelte/icons/settings-2';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import { api } from '../lib/api';
  import { copyText } from '../lib/clipboard';
  import { ago, bytes, dateTime, headerCase, statusClass, statusText } from '../lib/format';
  import { SCHEMES, SIGNATURE_STATUS } from '../lib/signature';
  import { snippet, SNIPPETS, type SnippetKind } from '../lib/snippets';
  import { app } from '../lib/state.svelte';
  import type { KV } from '../lib/types';
  import BodyViewer from './BodyViewer.svelte';
  import CopyButton from './CopyButton.svelte';
  import Dropdown from './Dropdown.svelte';
  import KVTable from './KVTable.svelte';
  import MethodBadge from './MethodBadge.svelte';
  import Section from './Section.svelte';
  import SignatureIcon from './SignatureIcon.svelte';

  const summary = $derived(app.requests.find((r) => r.uuid === app.selectedId) ?? null);
  const req = $derived(app.selected);
  const head = $derived(req ?? summary);
  const query = $derived(req ? (req.url.split('?')[1] ?? '') : (summary?.query_string ?? ''));
  const index = $derived(app.requests.findIndex((r) => r.uuid === app.selectedId));
  const result = $derived(app.selectedId ? app.forwardResults[app.selectedId] : undefined);
  const busy = $derived(!!app.selectedId && app.forwarding.has(app.selectedId));
  const sigStatus = $derived(req?.signature?.status ?? summary?.signature);

  const headers = $derived<KV[]>(
    req
      ? Object.entries(req.headers)
          .sort(([a], [b]) => a.localeCompare(b))
          .flatMap(([k, vs]) => vs.map((value) => ({ name: headerCase(k), value })))
      : [],
  );

  async function copyAs(kind: SnippetKind) {
    if (!req) return;
    const ok = await copyText(snippet(kind, req));
    app.toast(ok ? `Copied as ${SNIPPETS[kind]}` : 'Copy failed', ok ? 'success' : 'error', 1800);
  }

  const permalink = () => `${location.origin}${location.pathname}#/${app.token?.uuid}/${app.selectedId}`;
</script>

<div class="view">
  {#if head}
    <header class="head">
      {#if app.mobile}
        <button type="button" class="btn ghost sm back" onclick={() => app.select(null)}><ArrowLeft /> Requests</button>
      {/if}
      <div class="title">
        <MethodBadge method={head.method} size="lg" />
        <h1 class="mono">{head.path}{#if query}<span class="qs">?{query}</span>{/if}</h1>
      </div>
      <div class="meta">
        <span class="chip" title="Status this URL answered with">
          <span class="status {statusClass(head.response_status)}">{head.response_status}</span>
          {statusText(head.response_status)}
        </span>
        {#if sigStatus}
          <span class="chip {SIGNATURE_STATUS[sigStatus].tone}" title={req?.signature?.detail ?? ''}>
            <SignatureIcon status={sigStatus} />
            {SIGNATURE_STATUS[sigStatus].label}
          </span>
        {/if}
        <span class="chip">{bytes(head.size)}</span>
        <span class="chip" title={dateTime(head.created_at, app.prefs.utc)}>{ago(head.created_at, app.now)}</span>
        <span class="chip mono">{head.ip}</span>
        {#if result}
          <span class="chip" class:ok={result.ok} class:bad={!result.ok} title={result.error ?? result.url}>
            <Forward />
            {#if result.status}{result.status} · {result.ms} ms{:else}failed{/if}
          </span>
        {/if}
      </div>
      <div class="actions">
        <Dropdown class="btn sm" title="Copy as code" align="start">
          {#snippet button()}<Code /> Copy as <ChevronDown />{/snippet}
          {#snippet children(close)}
            <div class="menu-label">Copy request as</div>
            {#each Object.entries(SNIPPETS) as [kind, label] (kind)}
              <button type="button" class="menu-item" disabled={!req} onclick={() => { copyAs(kind as SnippetKind); close(); }}>{label}</button>
            {/each}
          {/snippet}
        </Dropdown>
        <div class="split">
          <button type="button" class="btn sm" disabled={busy} onclick={() => app.selectedId && app.forward(app.selectedId)} title="Replay this request to your forward target (f)">
            {#if busy}<LoaderCircle class="spin" />{:else}<Forward />{/if}
            Forward
          </button>
          <button type="button" class="btn sm icon" title="Forwarding settings" aria-label="Forwarding settings" onclick={() => (app.dialog = 'forward')}><Settings2 /></button>
        </div>
        <CopyButton text={permalink} class="btn sm" label="Permalink" title="Copy a link to this request" />
        <a class="btn sm" href={app.token && app.selectedId ? api.rawUrl(app.token.uuid, app.selectedId) : undefined} target="_blank" rel="noopener" title="Open the raw body">
          <ExternalLink /> Raw
        </a>
        <span class="grow"></span>
        {#if !app.mobile}
          <button type="button" class="btn ghost icon sm" title="Newer (k)" aria-label="Newer request" disabled={index <= 0} onclick={() => app.step(-1)}><ChevronLeft /></button>
          <button type="button" class="btn ghost icon sm" title="Older (j)" aria-label="Older request" disabled={index < 0 || index >= app.requests.length - 1} onclick={() => app.step(1)}><ChevronRight /></button>
        {/if}
        <button type="button" class="btn sm danger" title="Delete request (d)" onclick={() => app.selectedId && app.deleteRequest(app.selectedId)}>
          <Trash2 /> <span class="hide-sm">Delete</span>
        </button>
      </div>
    </header>
  {/if}

  {#if req}
    <div class="grid">
      <div class="col">
        {#if req.query.length}
          <Section id="query" title="Query parameters" count={req.query.length}>
            <KVTable rows={req.query} />
          </Section>
        {/if}
        {#if req.form.length || req.files.length}
          <Section id="form" title="Form fields" count={req.form.length + req.files.length}>
            {#if req.form.length}<KVTable rows={req.form} />{/if}
            {#if req.files.length}
              <ul class="files">
                {#each req.files as f, i (i)}
                  <li>
                    <Paperclip size={14} />
                    <span class="mono">{f.field}</span>
                    <strong>{f.filename}</strong>
                    <span class="muted">{f.content_type || 'unknown type'} · {bytes(f.size)}</span>
                  </li>
                {/each}
              </ul>
            {/if}
          </Section>
        {/if}
        <Section id="body" title="Body" flush>
          <BodyViewer {req} />
        </Section>
      </div>
      <div class="col">
        <Section id="headers" title="Headers" count={headers.length}>
          {#snippet actions()}
            <CopyButton text={() => headers.map((h) => `${h.name}: ${h.value}`).join('\n')} class="btn ghost xs" label="Copy all" title="Copy all headers" />
          {/snippet}
          <KVTable rows={headers} />
        </Section>
        <Section id="details" title="Details">
          <dl class="details">
            <dt>URL</dt>
            <dd class="mono url"><span>{req.url}</span><CopyButton text={req.url} title="Copy URL" /></dd>
            <dt>Method</dt>
            <dd class="mono">{req.method}</dd>
            <dt>Client IP</dt>
            <dd class="mono">
              {req.ip}
              <a href={`https://ipinfo.io/${encodeURIComponent(req.ip)}`} target="_blank" rel="noopener noreferrer" class="ext">lookup <ExternalLink size={12} /></a>
            </dd>
            <dt>Host</dt>
            <dd class="mono">{req.hostname}</dd>
            <dt>Date</dt>
            <dd>{dateTime(req.created_at, app.prefs.utc)} <span class="muted">({ago(req.created_at, app.now)})</span></dd>
            <dt>Size</dt>
            <dd>{bytes(req.size)}{#if req.truncated} <span class="chip warn">truncated</span>{/if}</dd>
            <dt>Protocol</dt>
            <dd class="mono">{req.protocol}</dd>
            <dt>User agent</dt>
            <dd class="mono">{req.user_agent || '—'}</dd>
            <dt>Request ID</dt>
            <dd class="mono"><span>{req.uuid}</span><CopyButton text={req.uuid} title="Copy ID" /></dd>
            <dt>Response</dt>
            <dd><span class="status {statusClass(req.response_status)}">{req.response_status}</span> {statusText(req.response_status)}</dd>
            {#if req.signature}
              <dt>Signature</dt>
              <dd class="sig">
                <span>
                  {SCHEMES[req.signature.scheme]?.name ?? req.signature.scheme}:
                  <strong class={SIGNATURE_STATUS[req.signature.status].tone}>{req.signature.status}</strong>
                  {#if req.signature.detail}<span class="muted"> · {req.signature.detail}</span>{/if}
                </span>
              </dd>
            {/if}
          </dl>
        </Section>
      </div>
    </div>
  {:else}
    <div class="loading"><LoaderCircle class="spin" size={20} /> Loading request…</div>
  {/if}
</div>

<style>
  .view {
    container-type: inline-size;
    display: flex;
    flex-direction: column;
    gap: 14px;
    min-width: 0;
    padding: 18px 22px 28px;
  }
  .head {
    display: grid;
    gap: 10px;
  }
  .back {
    justify-self: start;
    margin-left: -8px;
  }
  .title {
    display: flex;
    align-items: center;
    gap: 12px;
    min-width: 0;
  }
  h1 {
    min-width: 0;
    font-size: 17px;
    font-weight: 600;
    line-height: 1.35;
    overflow-wrap: anywhere;
  }
  .qs {
    color: var(--text-3);
    font-weight: 400;
  }
  .meta,
  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
  }
  .actions {
    padding-top: 2px;
  }
  .grow {
    flex: 1;
  }
  .split {
    display: inline-flex;
  }
  .split > :first-child {
    border-top-right-radius: 0;
    border-bottom-right-radius: 0;
  }
  .split > :last-child {
    margin-left: -1px;
    border-top-left-radius: 0;
    border-bottom-left-radius: 0;
  }
  .grid {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: 14px;
    align-items: start;
  }
  .col {
    display: grid;
    gap: 14px;
    min-width: 0;
  }
  @container (min-width: 980px) {
    .grid {
      grid-template-columns: minmax(0, 1.5fr) minmax(320px, 1fr);
    }
  }
  .details {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: 8px 16px;
    margin: 0;
    font-size: 13px;
  }
  dt {
    color: var(--text-3);
  }
  dd {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    margin: 0;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  dd.mono {
    font-size: 12.5px;
  }
  dd {
    flex-wrap: nowrap;
  }
  dd > span {
    min-width: 0;
  }
  .url span {
    word-break: break-all;
  }
  .sig .ok {
    color: var(--success);
  }
  .sig .bad {
    color: var(--danger);
  }
  .sig .warn {
    color: var(--warning);
  }
  .ext {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    font-family: var(--font);
    font-size: 12px;
  }
  .files {
    display: grid;
    gap: 6px;
    margin: 8px 0 0;
    padding: 0;
    list-style: none;
    font-size: 13px;
  }
  .files li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
    padding: 8px 10px;
    border: 1px solid var(--border);
    border-radius: 8px;
  }
  .files :global(svg) {
    color: var(--text-3);
  }
  .loading {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 40px;
    color: var(--text-3);
  }
  @media (max-width: 899px) {
    .view {
      padding: 12px 14px 24px;
    }
    h1 {
      font-size: 15px;
    }
  }
  @media (max-width: 520px) {
    .hide-sm {
      display: none;
    }
  }
</style>
