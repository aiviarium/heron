import solid from "@solidjs/vite-plugin"
import { defineConfig } from "vite"

// One bundle for every island, plus the theme. scripts/embed.mjs copies the
// result into internal/tools/docs/assets, which the Go binary embeds.
export default defineConfig({
  plugins: [solid()],
  optimizeDeps: { exclude: ["@ui-sdk/core"] },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    cssCodeSplit: false,
    target: "es2022",
    minify: true,
    lib: {
      entry: "src/main.tsx",
      formats: ["iife"],
      name: "heronDocs",
      fileName: () => "docs.js",
      cssFileName: "docs",
    },
    rollupOptions: { output: { inlineDynamicImports: true } },
  },
})
