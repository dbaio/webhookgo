import { writeFileSync } from 'node:fs';
import { defineConfig, type Plugin } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// The Go server embeds dist/ — `npm run dev` proxies the API to it.
const backend = process.env.BACKEND_URL ?? 'http://localhost:8080';

// Go's //go:embed needs dist/ to exist even before the first build.
const keepDist = (): Plugin => ({
  name: 'keep-dist',
  closeBundle: () => writeFileSync('dist/.gitkeep', ''),
});

export default defineConfig({
  plugins: [svelte(), keepDist()],
  server: {
    proxy: {
      '^/(token|api|healthz)([/?]|$)': backend,
      '^/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-': backend,
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2022',
    chunkSizeWarningLimit: 300,
  },
});
