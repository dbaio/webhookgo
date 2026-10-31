import { mount, type Component } from 'svelte';
import './app.css';
import Abuse from './Abuse.svelte';
import Account from './Account.svelte';
import Admin from './Admin.svelte';
import App from './App.svelte';
import Login from './Login.svelte';
import Privacy from './Privacy.svelte';
import Redeem from './Redeem.svelte';

// Standalone pages. The server serves the account ones only with ACCOUNTS=on.
const pages: Record<string, Component> = {
  '/abuse': Abuse,
  '/privacy': Privacy,
  '/login': Login,
  '/setup': Redeem,
  '/invite': Redeem,
  '/reset': Redeem,
  '/account': Account,
  '/admin': Admin,
};
const page = pages[location.pathname] ?? App;

export default mount(page, { target: document.getElementById('app')! });
