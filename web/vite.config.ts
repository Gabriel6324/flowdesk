import { defineConfig } from 'vitest/config';
export default defineConfig({
  build:{outDir:'dist',emptyOutDir:true},
  test:{environment:'jsdom',setupFiles:['./src/test-setup.ts'],include:['src/**/*.test.ts','src/**/*.test.tsx'],restoreMocks:true},
  server:{host:'127.0.0.1',proxy:{'/api':{target:'http://127.0.0.1:8787',changeOrigin:true}}}
});

