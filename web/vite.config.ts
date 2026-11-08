import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

// Vite only builds here (vite build, vite build --watch): Go renders every
// page and serves dist/app under /static, using .vite/manifest.json to link
// each entry's hashed file.
export default defineConfig({
  root: fileURLToPath(new URL(".", import.meta.url)),
  base: "/static/",
  // Copied as-is, keeping their names: files that need a fixed URL, like the
  // install icons manifest.webmanifest names.
  publicDir: "app/public",
  build: {
    outDir: "dist/app",
    emptyOutDir: true,
    manifest: true,
    // Every supported browser has modulepreload.
    modulePreload: { polyfill: false },
    rolldownOptions: {
      input: ["app/app.js", "app/styles/main.css"],
    },
  },
});
