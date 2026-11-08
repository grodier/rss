import { defineConfig } from "vite";
import { frontend } from "./vite.config";

// The admin's build, into dist/admin (see frontend in vite.config.ts).
export default defineConfig(frontend("admin"));
