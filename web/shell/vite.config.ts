import { defineConfig } from "vite";
import solid from "@solidjs/vite-plugin";

// The build goes where the server embeds it, so `heron serve` ships it.
export default defineConfig({
  plugins: [solid()],
  build: {
    outDir: "../../internal/tools/server/frontend/dist",
    emptyOutDir: true,
    target: "es2022",
    chunkSizeWarningLimit: 6000,
    sourcemap: false,
  },
  worker: { format: "es" },
  server: {
    proxy: {
      "/api": { target: "http://127.0.0.1:7340", ws: true },
    },
  },
});
