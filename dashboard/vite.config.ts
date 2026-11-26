import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// The build output goes where the Go binary embeds it (internal/webui/dist).
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: false,
    // The hub's CSP is default-src 'self': it blocks a font inlined as a data:
    // URI, which Vite does for any file under 4 KiB (the small subsets of
    // Inter and JetBrains Mono). Nothing is inlined, so every font is a file.
    assetsInlineLimit: 0,
  },
  server: {
    proxy: { "/api": "http://127.0.0.1:8080" },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test-setup.ts"],
  },
});
