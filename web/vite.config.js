import { defineConfig } from 'vite';
const target = process.env.LANBRIDGE_DEV_API || 'https://localhost:8766';
const proxy = () => ({
  target,
  secure: false, // Only the local development certificate.
  changeOrigin: true,
  configure(server) {
    server.on('proxyReq', request => request.setHeader('Origin', new URL(target).origin));
  },
});
export default defineConfig({
  build: { target: 'es2022', assetsInlineLimit: 0 },
  server: { host: 'localhost', proxy: { '/api': proxy(), '/healthz': proxy() } },
});
