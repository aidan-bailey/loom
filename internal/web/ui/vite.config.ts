import { readFileSync } from "node:fs";
import { resolve } from "node:path";
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
// by locale, so the file is the same on every machine. The bundled fonts are no JS modules, so the
// scanner never sees them: each @fontsource-variable package whose .woff2 the build emits has its
// own LICENSE file (OFL) added in the same entry format.
function thirdPartyLicenses(): Plugin {
  let dependencies: { name?: string | null; text(): string }[] = [];
  const scanner = license({
    thirdParty: {
      output: (found) => {
        dependencies = [...found];
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
    generateBundle(_options, bundle) {
      scanner.generateBundle();
      const entries = dependencies.map((dependency) => ({
        name: dependency.name ?? "",
        text: dependency.text().replaceAll("\r\n", "\n"),
      }));
      const fonts = new Map<string, string>();
      for (const file of Object.values(bundle)) {
        if (file.type !== "asset" || !file.fileName.endsWith(".woff2")) continue;
        for (const original of file.originalFileNames) {
          const match = /^(.*[\\/]@fontsource-variable[\\/][^\\/]+)[\\/]/.exec(original);
          if (match) fonts.set(match[1], match[1]);
        }
      }
      if (fonts.size === 0) throw new Error("no @fontsource-variable font reached the bundle");
      for (const dir of fonts.keys()) {
        const root = resolve(dir);
        const pkg = JSON.parse(readFileSync(resolve(root, "package.json"), "utf8")) as {
          name: string;
          version: string;
          license: string;
          description?: string;
        };
        const licence = readFileSync(resolve(root, "LICENSE"), "utf8").replaceAll("\r\n", "\n");
        entries.push({
          name: pkg.name,
          text: [
            `Name: ${pkg.name}`,
            `Version: ${pkg.version}`,
            `License: ${pkg.license}`,
            "Private: false",
            `Description: ${pkg.description ?? ""}`,
            "License Text:",
            "===",
            "",
            licence.trimEnd(),
          ].join("\n"),
        });
      }
      const text = entries
        .sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0))
        .map((entry) => entry.text)
        .join("\n\n---\n\n");
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
