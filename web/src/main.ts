import { mount } from 'svelte';
import './app.css';
import Abuse from './Abuse.svelte';
import App from './App.svelte';

const page = location.pathname === '/abuse' ? Abuse : App;

export default mount(page, { target: document.getElementById('app')! });
