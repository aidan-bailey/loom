import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import license from "rollup-plugin-license";
import type { Plugin } from "vite";
import { defineConfig } from "vitest/config";

// The two hooks of rollup-plugin-license this config runs itself. The plugin's own file output
// writes to a fixed path on disk, which would put a file in the tree from a `dist:check` build;
// here the licences are collected as the chunks render and emitted into the bundle instead.
interface LicenseHooks {
  renderChunk(
    code: string,
    chunk: { modules: Record<string, { renderedLength: number }> },
    options: { sourcemap: boolean },
  ): unknown;
  generateBundle(): void;
}

// thirdPartyLicenses writes the licence of every package the bundle holds into the bundle as
// THIRD_PARTY.txt (NOTICE.md points there). The packages come sorted by name by code point, never
// by locale, so the file is the same on every machine.
function thirdPartyLicenses(): Plugin {
  let text = "";
  const scanner = license({
    thirdParty: {
      output: (dependencies) => {
        text = [...dependencies]
          .sort((a, b) => {
            const x = a.name ?? "";
            const y = b.name ?? "";
            return x < y ? -1 : x > y ? 1 : 0;
          })
          .map((dependency) => dependency.text().replaceAll("\r\n", "\n"))
          .join("\n\n---\n\n");
      },
    },
  }) as unknown as LicenseHooks;
  return {
    name: "loom:third-party-licenses",
    apply: "build",
    renderChunk(code, chunk) {
      scanner.renderChunk(code, chunk, { sourcemap: false });
      return null;
    },
    generateBundle() {
      scanner.generateBundle();
      this.emitFile({ type: "asset", fileName: "THIRD_PARTY.txt", source: `${text}\n` });
    },
  };
}

export default defineConfig({
  plugins: [react(), tailwindcss(), thirdPartyLicenses()],
  resolve: {
    alias: { "@": decodeURIComponent(new URL("./src", import.meta.url).pathname) },
  },
  build: {
    outDir: "../dist",
    emptyOutDir: true,
    manifest: false,
  },
  server: {
    proxy: { "/api/v1": "http://127.0.0.1:5670" },
  },
  test: {
    environment: "happy-dom",
    globals: true,
    include: ["src/**/*.test.{ts,tsx}"],
    setupFiles: ["./src/test-setup.ts"],
  },
});
