import { readdir, readFile, realpath, stat } from "node:fs/promises";
import { dirname, isAbsolute, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const MAX_REPORTED_ERRORS = 20;
const ATTRIBUTE_PATTERN = /\b(?:href|src)\s*=\s*(["'])(.*?)\1/giu;
const ANCHOR_PATTERN = /\b(?:id|name)\s*=\s*(["'])(.*?)\1/giu;
const META_PATTERN = /<meta\b[^>]*>/giu;
const META_ATTRIBUTE_PATTERN = /\b([a-z][\w:-]*)\s*=\s*(["'])(.*?)\2/giu;
const META_ASSET_KEYS = new Set([
  "og:image",
  "og:image:url",
  "og:image:secure_url",
  "sourcey-search",
  "twitter:image",
  "twitter:image:src",
]);
const EXTERNAL_PATTERN = /^[a-z][a-z\d+.-]*:/iu;

function decodeHtml(value) {
  return value.replace(
    /&(?:amp|quot|apos|lt|gt|#\d+|#x[\da-f]+);/giu,
    (entity) => {
      const normalized = entity.toLowerCase();
      const named = {
        "&amp;": "&",
        "&quot;": '"',
        "&apos;": "'",
        "&lt;": "<",
        "&gt;": ">",
      };
      if (named[normalized]) return named[normalized];
      const radix = normalized.startsWith("&#x") ? 16 : 10;
      const digits = normalized.slice(radix === 16 ? 3 : 2, -1);
      return String.fromCodePoint(Number.parseInt(digits, radix));
    },
  );
}

function decodeUrlComponent(value, kind) {
  try {
    return decodeURIComponent(value);
  } catch {
    throw new Error(`invalid percent encoding in ${kind}`);
  }
}

function isOutside(root, candidate) {
  const pathFromRoot = relative(root, candidate);
  return (
    pathFromRoot === ".." ||
    pathFromRoot.startsWith(`..${sep}`) ||
    isAbsolute(pathFromRoot)
  );
}

async function htmlFiles(root) {
  const found = [];
  async function visit(directory) {
    const entries = await readdir(directory, { withFileTypes: true });
    entries.sort((left, right) => left.name.localeCompare(right.name));
    for (const entry of entries) {
      const path = resolve(directory, entry.name);
      if (entry.isDirectory()) await visit(path);
      else if (entry.isFile() && entry.name.endsWith(".html")) found.push(path);
    }
  }
  await visit(root);
  return found;
}

function references(html) {
  const found = [...html.matchAll(ATTRIBUTE_PATTERN)].map((match) =>
    decodeHtml(match[2].trim()),
  );
  for (const meta of html.matchAll(META_PATTERN)) {
    const attributes = new Map(
      [...meta[0].matchAll(META_ATTRIBUTE_PATTERN)].map((match) => [
        match[1].toLowerCase(),
        decodeHtml(match[3].trim()),
      ]),
    );
    const assetKey = (attributes.get("property") ?? attributes.get("name") ?? "")
      .toLowerCase();
    if (META_ASSET_KEYS.has(assetKey) && attributes.has("content")) {
      found.push(attributes.get("content"));
    }
  }
  return found;
}

function anchors(html) {
  return new Set(
    [...html.matchAll(ANCHOR_PATTERN)].map((match) => decodeHtml(match[2])),
  );
}

function errorRecord(type, source, reference, detail) {
  return { type, source, reference, detail };
}

export async function checkSite(siteRoot) {
  const root = await realpath(resolve(siteRoot));
  const pages = await htmlFiles(root);
  const anchorCache = new Map();
  const errors = [];
  let errorCount = 0;
  let referencesTotal = 0;
  let referencesChecked = 0;
  let referencesSkipped = 0;

  function addError(record) {
    errorCount += 1;
    if (errors.length < MAX_REPORTED_ERRORS) errors.push(record);
  }

  for (const sourcePath of pages) {
    const source = relative(root, sourcePath).replaceAll(sep, "/");
    const html = await readFile(sourcePath, "utf8");
    for (const reference of references(html)) {
      referencesTotal += 1;
      if (
        reference === "" ||
        reference.startsWith("//") ||
        EXTERNAL_PATTERN.test(reference)
      ) {
        referencesSkipped += 1;
        continue;
      }

      referencesChecked += 1;
      const hashAt = reference.indexOf("#");
      const beforeHash = hashAt === -1 ? reference : reference.slice(0, hashAt);
      const rawFragment = hashAt === -1 ? "" : reference.slice(hashAt + 1);
      const queryAt = beforeHash.indexOf("?");
      const rawPath = queryAt === -1 ? beforeHash : beforeHash.slice(0, queryAt);
      let decodedPath;
      let fragment;
      try {
        decodedPath = decodeUrlComponent(rawPath, "path");
        fragment = decodeUrlComponent(rawFragment, "fragment");
      } catch (error) {
        addError(errorRecord("invalid_url", source, reference, error.message));
        continue;
      }

      if (decodedPath.includes("\0") || decodedPath.includes("\\")) {
        addError(
          errorRecord(
            "invalid_path",
            source,
            reference,
            "local paths cannot contain NUL or backslash characters",
          ),
        );
        continue;
      }

      const base = decodedPath.startsWith("/") ? root : dirname(sourcePath);
      const localPath = decodedPath.startsWith("/")
        ? decodedPath.slice(1)
        : decodedPath;
      let target = decodedPath === "" ? sourcePath : resolve(base, localPath);
      if (isOutside(root, target)) {
        addError(
          errorRecord(
            "path_escape",
            source,
            reference,
            "resolved target is outside the generated site root",
          ),
        );
        continue;
      }

      let targetStat;
      try {
        targetStat = await stat(target);
        if (targetStat.isDirectory()) {
          target = resolve(target, "index.html");
          targetStat = await stat(target);
        }
        const canonicalTarget = await realpath(target);
        if (isOutside(root, canonicalTarget)) {
          addError(
            errorRecord(
              "path_escape",
              source,
              reference,
              "target resolves through a link outside the generated site root",
            ),
          );
          continue;
        }
      } catch {
        addError(
          errorRecord("missing_target", source, reference, "target does not exist"),
        );
        continue;
      }

      if (!targetStat.isFile()) {
        addError(
          errorRecord("missing_target", source, reference, "target is not a file"),
        );
        continue;
      }

      if (fragment !== "") {
        let targetAnchors = anchorCache.get(target);
        if (!targetAnchors) {
          if (!target.toLowerCase().endsWith(".html")) {
            addError(
              errorRecord(
                "missing_fragment",
                source,
                reference,
                "fragment target is not an HTML file",
              ),
            );
            continue;
          }
          targetAnchors = anchors(await readFile(target, "utf8"));
          anchorCache.set(target, targetAnchors);
        }
        if (!targetAnchors.has(fragment)) {
          addError(
            errorRecord(
              "missing_fragment",
              source,
              reference,
              `fragment #${fragment} does not exist`,
            ),
          );
        }
      }
    }
  }

  return {
    status: errors.length === 0 ? "passed" : "failed",
    root,
    html_files: pages.length,
    references_total: referencesTotal,
    references_checked: referencesChecked,
    references_skipped: referencesSkipped,
    error_count: errorCount,
    errors,
    errors_truncated: errorCount > errors.length,
  };
}

const invokedPath = process.argv[1] ? resolve(process.argv[1]) : "";
if (invokedPath === fileURLToPath(import.meta.url)) {
  const siteRoot = process.argv[2] ?? resolve(dirname(invokedPath), "..", "dist");
  try {
    const result = await checkSite(siteRoot);
    process.stdout.write(`${JSON.stringify({ linkcheck: result })}\n`);
    if (result.status !== "passed") process.exitCode = 1;
  } catch (error) {
    process.stderr.write(`${JSON.stringify({ linkcheck: { status: "error", message: error.message } })}\n`);
    process.exitCode = 1;
  }
}
