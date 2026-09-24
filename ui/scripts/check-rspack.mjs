#!/usr/bin/env node
// Local-only packaging smoke test; does not modify the FleetShift checkout.
import { createRequire } from "node:module";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join, resolve } from "node:path";

const ui = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const fleetshift = resolve(ui, "../../fleetshift");
const hostRequire = createRequire(join(fleetshift, "extensions/core/client/package.json"));
const { rspack } = hostRequire("@rspack/core");
const temp = await mkdtemp(join(ui, ".rspack-smoke-"));
try {
  const entry = join(temp, "entry.js");
  await writeFile(entry, 'import { InfraPadDocuments } from "@infrapad/ui";\nimport "@infrapad/ui/styles.css";\nconsole.log(InfraPadDocuments);\n');
  await mkdir(join(temp, "out"));
  const peers = [
    "react", "react-dom", "react-router-dom", "victory",
    "@patternfly/react-core", "@patternfly/react-icons",
    "@patternfly/react-charts", "@patternfly/react-data-view",
  ];
  const aliases = Object.fromEntries(peers.map((name) => [name, dirname(hostRequire.resolve(`${name}/package.json`))]));
  const stats = await new Promise((done, fail) => rspack({
    mode: "production",
    context: ui,
    entry,
    output: { path: join(temp, "out"), filename: "consumer.js" },
    resolve: { alias: aliases },
    module: { rules: [{ test: /\.css$/, use: [hostRequire.resolve("style-loader"), hostRequire.resolve("css-loader")] }] },
  }, (err, result) => err ? fail(err) : done(result)));
  if (stats.hasErrors()) throw new Error(stats.toString({ all: false, errors: true, errorDetails: true }));
  console.log("Rspack resolved built @infrapad/ui and styles.css with FleetShift peers.");
} finally {
  await rm(temp, { recursive: true, force: true });
}
