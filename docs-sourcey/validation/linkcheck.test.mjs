import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { checkSite } from "./linkcheck.mjs";

async function fixture(files) {
  const root = await mkdtemp(join(tmpdir(), "sourcey-linkcheck-"));
  for (const [path, contents] of Object.entries(files)) {
    const target = join(root, path);
    await mkdir(join(target, ".."), { recursive: true });
    await writeFile(target, contents);
  }
  return root;
}

test("accepts local files, encoded fragments, queries, and skipped URLs", async (t) => {
  const root = await fixture({
    "index.html": `<a id="top" href="docs/page%20one.html?mode=test#hello%20world">Page</a>
      <meta property="og:image" content="assets/preview.png">
      <meta content="https://cdn.example.test/preview.png" name="twitter:image">
      <a href="//cdn.example.test/library.js">CDN</a>
      <a href="mailto:docs@example.test">Mail</a>
      <script src="assets/app.js"></script>`,
    "docs/page one.html": `<h2 id="hello world">Hello</h2><a href="/index.html#top">Home</a>`,
    "assets/app.js": "export {};",
    "assets/preview.png": "preview",
  });
  t.after(() => rm(root, { recursive: true, force: true }));

  const result = await checkSite(root);
  assert.equal(result.status, "passed");
  assert.equal(result.html_files, 2);
  assert.equal(result.references_checked, 4);
  assert.equal(result.references_skipped, 3);
  assert.deepEqual(result.errors, []);
});

test("reports a missing local target", async (t) => {
  const root = await fixture({ "index.html": `<a href="missing.html">Missing</a>` });
  t.after(() => rm(root, { recursive: true, force: true }));

  const result = await checkSite(root);
  assert.equal(result.status, "failed");
  assert.equal(result.errors[0].type, "missing_target");
});

test("reports a missing local social-preview asset", async (t) => {
  const root = await fixture({
    "index.html": `<meta content="preview/missing.png" property="og:image">`,
  });
  t.after(() => rm(root, { recursive: true, force: true }));

  const result = await checkSite(root);
  assert.equal(result.status, "failed");
  assert.equal(result.errors[0].type, "missing_target");
  assert.equal(result.errors[0].reference, "preview/missing.png");
});

test("reports a missing Sourcey search index", async (t) => {
  const root = await fixture({
    "index.html": `<meta name="sourcey-search" content="missing-search-index.json">`,
  });
  t.after(() => rm(root, { recursive: true, force: true }));

  const result = await checkSite(root);
  assert.equal(result.status, "failed");
  assert.equal(result.errors[0].type, "missing_target");
  assert.equal(result.errors[0].reference, "missing-search-index.json");
});

test("reports a missing HTML fragment", async (t) => {
  const root = await fixture({
    "index.html": `<a href="page.html#absent">Missing fragment</a>`,
    "page.html": `<h1 id="present">Present</h1>`,
  });
  t.after(() => rm(root, { recursive: true, force: true }));

  const result = await checkSite(root);
  assert.equal(result.status, "failed");
  assert.equal(result.errors[0].type, "missing_fragment");
});

test("rejects encoded attempts to escape the site root", async (t) => {
  const root = await fixture({
    "nested/index.html": `<a href="../../%2e%2e/outside.txt">Escape</a>`,
  });
  t.after(() => rm(root, { recursive: true, force: true }));

  const result = await checkSite(root);
  assert.equal(result.status, "failed");
  assert.equal(result.errors[0].type, "path_escape");
});

test("marks errors as truncated only when a record is omitted", async (t) => {
  const links = (count) =>
    Array.from({ length: count }, (_, index) => `<a href="missing-${index}.html">x</a>`).join("");
  const exactRoot = await fixture({ "index.html": links(20) });
  const overflowRoot = await fixture({ "index.html": links(21) });
  t.after(() => rm(exactRoot, { recursive: true, force: true }));
  t.after(() => rm(overflowRoot, { recursive: true, force: true }));

  const exact = await checkSite(exactRoot);
  assert.equal(exact.error_count, 20);
  assert.equal(exact.errors.length, 20);
  assert.equal(exact.errors_truncated, false);

  const overflow = await checkSite(overflowRoot);
  assert.equal(overflow.error_count, 21);
  assert.equal(overflow.errors.length, 20);
  assert.equal(overflow.errors_truncated, true);
});
