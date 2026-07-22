import { readFile, stat } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const commit = "b4c3694ee73399d8a55293d568e5100c25e4d2d4";
const here = dirname(fileURLToPath(import.meta.url));
const docs = join(here, "..");

const artifacts = [
  "dist/index.html",
  "dist/guides/overview.html",
  "dist/guides/quickstart.html",
  "dist/guides/driver-contract.html",
  "dist/guides/server-settings.html",
  "dist/guides/extensions.html",
  "dist/api.html",
  "dist/api/index.html",
  "dist/api/package-root.html",
  "dist/llms.txt",
  "dist/llms-full.txt",
];

const artifactSizes = {};
for (const relative of artifacts) {
  artifactSizes[relative] = (await stat(join(docs, relative))).size;
}

const snapshot = JSON.parse(await readFile(join(docs, "godoc.json"), "utf8"));
const packageSpec = snapshot.packages[0];
const publicApiCount =
  (packageSpec.types?.length ?? 0) +
  (packageSpec.funcs?.length ?? 0) +
  (packageSpec.consts?.length ?? 0) +
  (packageSpec.vars?.length ?? 0);

const apiHtml = await readFile(join(docs, "dist/api/package-root.html"), "utf8");
const sourcePattern = new RegExp(
  `https://github\\.com/fclairamb/ftpserverlib/blob/${commit}/[^"'\\s<]+#L\\d+`,
  "g",
);
const sourceLinks = [...new Set(apiHtml.match(sourcePattern) ?? [])];

const requiredSymbols = [
  "FtpServer",
  "Settings",
  "MainDriver",
  "ClientContext",
  "ClientDriverExtensionHasher",
];
const missingSymbols = requiredSymbols.filter((symbol) => !apiHtml.includes(symbol));

if (snapshot.module_path !== "github.com/fclairamb/ftpserverlib") {
  throw new Error(`unexpected module path: ${snapshot.module_path}`);
}
if (publicApiCount < 15) {
  throw new Error(`expected at least 15 public API concepts, found ${publicApiCount}`);
}
if (sourceLinks.length < 15) {
  throw new Error(`expected at least 15 pinned source links, found ${sourceLinks.length}`);
}
if (missingSymbols.length > 0) {
  throw new Error(`missing generated symbols: ${missingSymbols.join(", ")}`);
}

process.stdout.write(
  `${JSON.stringify({
    validation: {
      status: "passed",
      repo: "https://github.com/fclairamb/ftpserverlib",
      commit,
      ecosystem: "Go",
      adapter: "sourcey godoc snapshot",
      sourcey_version: "3.6.5",
      module_path: snapshot.module_path,
      generated_at: snapshot.generated_at,
      public_api_count: publicApiCount,
      generated_html_pages: 9,
      pinned_source_links: sourceLinks.length,
      required_symbols: requiredSymbols,
      artifacts: artifactSizes,
    },
  })}\n`,
);
