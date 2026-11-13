<script lang="ts">
  // The /privacy page: what this server keeps, who can see it and for how
  // long, written from its own settings (/api/config).
  import ShieldCheck from '@lucide/svelte/icons/shield-check';
  import Webhook from '@lucide/svelte/icons/webhook';
  import { onMount } from 'svelte';
  import { api } from './lib/api';
  import { duration } from './lib/format';
  import { REPO_URL } from './lib/project';
  import { app } from './lib/state.svelte';

  onMount(() => {
    api.config().then((c) => (app.config = c), () => {});
  });

  const c = $derived(app.config);
  $effect(() => {
    document.title = `Privacy · ${c.app_name}`;
  });
  // Without accounts, nobody signs in to administer the server.
  const staff = $derived(c.accounts ? 'the administrators' : 'the people who run this server');
  const contact = $derived(c.abuse_contact.trim());
  const contactHref = $derived(
    /^(https?|mailto):/i.test(contact) ? contact : /^[^\s@/:]+@[^\s@/:]+$/.test(contact) ? `mailto:${contact}` : '',
  );
</script>

<div class="page">
  <header class="bar">
    <a class="brand" href="/" title={`Open ${c.app_name}`}>
      <span class="logo"><Webhook size={18} /></span>
      <span class="name">{c.app_name}</span>
    </a>
  </header>

  <main>
    <h1><ShieldCheck size={22} /> Privacy</h1>
    <p class="lead">
      What this server keeps, who can see it and for how long, from its own settings. {c.app_name} is
      <a href={REPO_URL} target="_blank" rel="noopener">open source</a>: the code shows exactly what it does.
    </p>

    <section>
      <h2>Webhook URLs and their requests</h2>
      <p>
        A URL keeps the requests sent to it, up to the newest {c.max_requests.toLocaleString()}: whatever the sender sent (method, path, headers,
        query, body) and the sender's IP address.
      </p>
      <ul>
        {#if c.anonymous_urls}
          <li>
            <strong>Anonymous URLs</strong> are public: anyone who has the link can see their requests, change their settings and delete them. A
            URL and its requests are deleted {duration(c.token_ttl)} after it was last used.
          </li>
        {/if}
        {#if c.accounts}
          <li>
            <strong>Private URLs</strong>, created while signed in, can be opened only by their owner and the administrators. They don't expire;
            their requests do, as on any URL.
          </li>
        {/if}
        <li>Deleting a URL erases its requests at once.</li>
      </ul>
      <p>
        When a URL is created, the server notes the IP address and browser (user agent) it came from. For an anonymous URL, the
        <a href="/abuse">abuse page</a> shows them to anyone who looks the URL up there, so that abuse can be reported to the network it came
        from. {c.accounts ? "Otherwise only the URL's owner and the administrators see them." : 'The rest of the app leaves them out.'}
      </p>
    </section>

    {#if c.retention > 0}
      <section>
        <h2>Records for abuse investigations</h2>
        <p>
          A short record of each URL is kept for {duration(c.retention)} after it expires or is deleted: when and from which IP address and
          browser it was created and deleted, and how many requests it got, not what they held. Only {staff} read it.
        </p>
      </section>
    {/if}

    {#if c.accounts}
      <section>
        <h2>Administrators</h2>
        <p>
          To look for abuse, the administrators can list every URL, anonymous or private, open it and delete it. When one of them opens a URL
          that isn't theirs, that goes into a log that cannot be edited and that the other administrators see.
        </p>
      </section>

      <section>
        <h2>Accounts</h2>
        <ul>
          <li>Your email address, your name, and your password as an argon2id hash: the password itself is never stored.</li>
          {#if c.google}
            <li>
              If you sign in with Google, this server asks Google for your email address only, and keeps nothing else from it: the address is
              what ties your Google account to your account here. Google, in turn, knows that you signed in here.
            </li>
          {/if}
          <li>
            For each session, the IP address and browser it was opened from, until you sign out or it expires. The session cookie only
            identifies the session, and it is removed from the requests your URLs capture.
          </li>
          <li>For each API key, its name, its first characters, a hash of it and when it was last used.</li>
          <li>An account deleted by an administrator takes its URLs with it.</li>
        </ul>
      </section>

      <section>
        <h2>Abuse reports</h2>
        <p>A report sent from the <a href="/abuse">abuse page</a> keeps its message, the contact you give, if any, and your IP address, for the administrators.</p>
      </section>
    {/if}

    <section>
      <h2>In your browser</h2>
      <p>
        The app keeps the URLs you opened and your preferences in your browser's local storage{c.accounts
          ? ', and, for an hour, which URLs each tab created, so that signing in from that tab saves them to your account. While you are signed in, there is also a session cookie' +
            (c.google ? ', and signing in with Google sets one for ten minutes, to carry the sign-in there and back' : '')
          : ''}. There are no analytics, ads or third-party scripts: the app loads only from this server.
      </p>
    </section>

    {#if contact}
      <section>
        <h2>Contact</h2>
        <p>
          Questions about your data, or a URL to report:
          {#if contactHref}<a href={contactHref}>{contact}</a>{:else}{contact}{/if}.
        </p>
      </section>
    {/if}
  </main>
</div>

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
    gap: 14px;
    max-width: 720px;
    margin: 0 auto;
    padding: 28px 16px 48px;
    color: var(--text-2);
    line-height: 1.55;
  }
  h1 {
    display: flex;
    align-items: center;
    gap: 9px;
    color: var(--text);
    font-size: 22px;
    letter-spacing: -0.015em;
  }
  h1 :global(svg) {
    color: var(--accent);
  }
  .lead {
    color: var(--text);
    font-size: 15px;
  }
  section {
    display: grid;
    gap: 8px;
    padding-top: 14px;
    border-top: 1px solid var(--border);
  }
  h2 {
    color: var(--text);
    font-size: 15px;
    font-weight: 650;
    letter-spacing: -0.01em;
  }
  ul {
    display: grid;
    gap: 6px;
    margin: 0;
    padding-left: 20px;
  }
  strong {
    color: var(--text);
  }
</style>
