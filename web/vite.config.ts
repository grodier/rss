/// <reference types="vitest/config" />
import { readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { defineConfig, type UserConfig } from "vite";

const root = fileURLToPath(new URL(".", import.meta.url));

// Every page group's entry (<dir>/entries/<group>.ts, not its tests) and
// every sheet (<dir>/styles/<name>.css, not its partials/), so a new group
// needs no change here. Vite splits the modules entries share into chunks.
const inputs = (dir: string, ext: string) =>
  readdirSync(root + dir)
    .filter((f) => f.endsWith(ext) && !f.endsWith(".test" + ext))
    .map((f) => `${dir}/${f}`);

// The build of one site's frontend, dir: "app", the reader's, written to
// dist/app, or "admin", the admin's (vite.admin.config.ts), written to
// dist/admin. They're two builds, as each server reads its own manifest
// (package internal/assets), and a build writes one. Both import shared/.
//
// Vite only builds (vite build, vite build --watch): Go renders every page
// and serves the build under /static, using .vite/manifest.json to link each
// page group's files (internal/assets).
export function frontend(dir: "app" | "admin"): UserConfig {
  return {
    root,
    base: "/static/",
    // Copied as-is, keeping their names: files that need a fixed URL, like
    // the install icons manifest.webmanifest names. The admin shows the
    // reader's logo and favicons, so it copies the same ones.
    publicDir: "app/public",
    build: {
      outDir: `dist/${dir}`,
      emptyOutDir: true,
      manifest: true,
      // Every supported browser has modulepreload.
      modulePreload: { polyfill: false },
      rolldownOptions: {
        input: [...inputs(`${dir}/entries`, ".ts"), ...inputs(`${dir}/styles`, ".css")],
        output: {
          // Chunks get their own folder, so an outdated chunk's name can
          // never be taken for an outdated entry's (current in
          // internal/assets).
          chunkFileNames: "assets/chunks/[name]-[hash].js",
        },
      },
    },
  };
}

// The reader's build. Vitest reads this config too, for every test (npm
// test), the admin's included.
export default defineConfig({
  ...frontend("app"),
  test: {
    // The tests sit next to their modules (share.ts, share.test.ts).
    environment: "happy-dom",
  },
});
