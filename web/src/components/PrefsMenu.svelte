<script lang="ts">
  import Bug from '@lucide/svelte/icons/bug';
  import Ellipsis from '@lucide/svelte/icons/ellipsis';
  import Forward from '@lucide/svelte/icons/forward';
  import GitFork from '@lucide/svelte/icons/git-fork';
  import Keyboard from '@lucide/svelte/icons/keyboard';
  import Monitor from '@lucide/svelte/icons/monitor';
  import Moon from '@lucide/svelte/icons/moon';
  import SlidersHorizontal from '@lucide/svelte/icons/sliders-horizontal';
  import Star from '@lucide/svelte/icons/star';
  import Sun from '@lucide/svelte/icons/sun';
  import { timeZone } from '../lib/format';
  import { FORK_URL, ISSUES_URL, REPO_URL } from '../lib/project';
  import { app, type Prefs } from '../lib/state.svelte';
  import Dropdown from './Dropdown.svelte';

  const themes = [
    { v: 'system', label: 'System', icon: Monitor },
    { v: 'light', label: 'Light', icon: Sun },
    { v: 'dark', label: 'Dark', icon: Moon },
  ] as const;

  function set<K extends keyof Prefs>(key: K, value: Prefs[K]) {
    app.prefs[key] = value;
    app.savePrefs();
  }
</script>

<Dropdown class="btn ghost icon sm" title="Preferences" width={290}>
  {#snippet button()}<Ellipsis />{/snippet}
  {#snippet children(close)}
    <div class="menu-label">Theme</div>
    <div class="theme">
      <div class="seg">
        {#each themes as t (t.v)}
          <button type="button" class:on={app.prefs.theme === t.v} onclick={() => set('theme', t.v)}><t.icon /> {t.label}</button>
        {/each}
      </div>
    </div>
    <div class="menu-label">Behaviour</div>
    <label class="menu-item">
      <span class="grow">Auto-select new requests<small>Jump to each request as it arrives</small></span>
      <input type="checkbox" class="switch" checked={app.prefs.autoSelect} onchange={(e) => set('autoSelect', e.currentTarget.checked)} />
    </label>
    <label class="menu-item">
      <span class="grow">Format bodies<small>Pretty-print JSON and XML</small></span>
      <input type="checkbox" class="switch" checked={app.prefs.pretty} onchange={(e) => set('pretty', e.currentTarget.checked)} />
    </label>
    <label class="menu-item">
      <span class="grow">Wrap long lines</span>
      <input type="checkbox" class="switch" checked={app.prefs.wrap} onchange={(e) => set('wrap', e.currentTarget.checked)} />
    </label>
    <label class="menu-item">
      <span class="grow">Times in UTC<small>Instead of {timeZone}</small></span>
      <input type="checkbox" class="switch" checked={app.prefs.utc} onchange={(e) => set('utc', e.currentTarget.checked)} />
    </label>
    <label class="menu-item">
      <span class="grow">Desktop notifications<small>When this tab is in the background</small></span>
      <input type="checkbox" class="switch" checked={app.prefs.notify} onchange={(e) => app.setNotify(e.currentTarget.checked)} />
    </label>
    <div class="menu-sep"></div>
    {#if app.token}
      <button type="button" class="menu-item" onclick={() => { close(); app.dialog = 'settings'; }}><SlidersHorizontal /> <span class="grow">Response settings</span><kbd>s</kbd></button>
      <button type="button" class="menu-item" onclick={() => { close(); app.dialog = 'forward'; }}><Forward /> <span class="grow">Forwarding</span><kbd>f</kbd></button>
    {/if}
    <button type="button" class="menu-item" onclick={() => { close(); app.dialog = 'shortcuts'; }}><Keyboard /> <span class="grow">Keyboard shortcuts</span><kbd>?</kbd></button>
    <div class="menu-sep"></div>
    <a class="menu-item" href={REPO_URL} target="_blank" rel="noopener" onclick={close}><Star /> <span class="grow">Star on GitHub</span></a>
    <a class="menu-item" href={FORK_URL} target="_blank" rel="noopener" onclick={close}><GitFork /> <span class="grow">Fork on GitHub</span></a>
    <a class="menu-item" href={ISSUES_URL} target="_blank" rel="noopener" onclick={close}><Bug /> <span class="grow">Report an issue</span></a>
    {#if app.config.version}<p class="version">{app.config.app_name} {app.config.version}</p>{/if}
  {/snippet}
</Dropdown>

<style>
  .theme {
    padding: 2px 6px 8px;
  }
  .theme .seg {
    display: flex;
  }
  .theme .seg button {
    flex: 1;
    justify-content: center;
  }
  label.menu-item {
    cursor: pointer;
  }
  small {
    display: block;
    color: var(--text-3);
    font-size: 11.5px;
  }
  .version {
    padding: 6px 10px 4px;
    color: var(--text-3);
    font: 11px var(--mono);
  }
</style>
