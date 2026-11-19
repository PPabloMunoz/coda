// @ts-check
import { defineConfig } from 'astro/config';

// https://astro.build/config
export default defineConfig({
  outDir: 'dist',
  vite: { server: { proxy: { '/api': 'http://localhost:8687' } } },
});
