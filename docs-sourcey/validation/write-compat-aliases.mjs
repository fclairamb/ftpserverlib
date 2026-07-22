import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const docs = join(dirname(fileURLToPath(import.meta.url)), "..");
const aliasPath = join(docs, "dist", "api", "index.html");
const aliasHtml = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta http-equiv="refresh" content="0; url=../api.html">
    <link rel="canonical" href="../api.html">
    <title>Go API</title>
  </head>
  <body><a href="../api.html">Continue to the Go API overview</a></body>
</html>
`;

await mkdir(dirname(aliasPath), { recursive: true });
await writeFile(aliasPath, aliasHtml, "utf8");
process.stdout.write(
  `${JSON.stringify({ compatibility_alias: "dist/api/index.html", target: "dist/api.html" })}\n`,
);
