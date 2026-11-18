// Builds the app into a fresh temporary directory and compares the result with the committed
// bundle, internal/web/dist, byte for byte. It writes nothing under the tree: a stale bundle is
// a failure here, and `pnpm run build` is the fix.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "vite";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const committed = path.resolve(root, "../dist");

// files lists every file under dir as a slash-separated path relative to dir, sorted.
function files(dir) {
  if (!fs.existsSync(dir)) return [];
  return fs
    .readdirSync(dir, { recursive: true, withFileTypes: true })
    .filter((entry) => entry.isFile())
    .map((entry) =>
      path.relative(dir, path.join(entry.parentPath, entry.name)).split(path.sep).join("/"),
    )
    .sort();
}

process.chdir(root);
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "loom-dist-"));
const problems = [];
try {
  await build({ root, logLevel: "warn", build: { outDir: tmp, emptyOutDir: true } });
  const built = files(tmp);
  const have = files(committed);
  for (const name of built) {
    if (!have.includes(name)) {
      problems.push(`missing from internal/web/dist: ${name}`);
    } else if (
      !fs.readFileSync(path.join(tmp, name)).equals(fs.readFileSync(path.join(committed, name)))
    ) {
      problems.push(`differs from the build: internal/web/dist/${name}`);
    }
  }
  for (const name of have) {
    if (!built.includes(name)) problems.push(`extra in internal/web/dist: ${name}`);
  }
} catch (err) {
  problems.push(`the build failed: ${err instanceof Error ? err.message : String(err)}`);
} finally {
  fs.rmSync(tmp, { recursive: true, force: true });
}

if (problems.length > 0) {
  console.error(problems.join("\n"));
  console.error(
    "internal/web/dist is stale: run `pnpm run build` in internal/web/ui and commit it.",
  );
  process.exit(1);
}
console.log("internal/web/dist matches the build.");
