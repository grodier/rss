/// <reference types="vitest/config" />
import { readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

const root = fileURLToPath(new URL(".", import.meta.url));

// Every page group's entry (app/entries/<group>.ts, not its tests) and
// every sheet (app/styles/<name>.css, not its partials/), so a new group
// needs no change here. Vite splits the modules entries share into chunks.
const inputs = (dir: string, ext: string) =>
  readdirSync(root + dir)
    .filter((f) => f.endsWith(ext) && !f.endsWith(".test" + ext))
    .map((f) => `${dir}/${f}`);

// Vite only builds here (vite build, vite build --watch): Go renders every
// page and serves dist/app under /static, using .vite/manifest.json to link
// each page group's files (assetTags in internal/server/static.go). Vitest
// reads it too, for the tests (npm test).
export default defineConfig({
  root,
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
      input: [...inputs("app/entries", ".ts"), ...inputs("app/styles", ".css")],
      output: {
        // Chunks get their own folder, so an outdated chunk's name can never
        // be taken for an outdated entry's (current in static.go).
        chunkFileNames: "assets/chunks/[name]-[hash].js",
      },
    },
  },
  test: {
    // The tests sit next to their modules (share.ts, share.test.ts).
    environment: "happy-dom",
  },
});
