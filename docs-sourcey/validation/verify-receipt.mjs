import { spawn } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const receiptDir = join(here, "receipts");
const kid = "sourcey-validation-20260714125016";
const publicKey = "SCa6Q9u9f5o6LJL8pVzXXves5NqF+qiToVf70IUfpnc=";
const npxCli = process.env.npm_execpath
  ? join(dirname(process.env.npm_execpath), "npx-cli.js")
  : null;
const command = npxCli ? process.execPath : "npx";
const commandArgs = [
  "-y",
  "@runxhq/cli@0.7.0",
  "verify",
  "--receipt-dir",
  receiptDir,
  "--json",
];

const child = spawn(
  command,
  npxCli ? [npxCli, ...commandArgs] : commandArgs,
  {
    env: {
      ...process.env,
      RUNX_RECEIPT_VERIFY_KID: kid,
      RUNX_RECEIPT_VERIFY_ED25519_PUBLIC_KEY_BASE64: publicKey,
    },
    stdio: ["ignore", "pipe", "inherit"],
  },
);

let stdout = "";
child.stdout.setEncoding("utf8");
child.stdout.on("data", (chunk) => {
  stdout += chunk;
});

const exitCode = await new Promise((resolve, reject) => {
  child.once("error", reject);
  child.once("close", resolve);
});

if (exitCode !== 0) {
  throw new Error(`runx verify exited with code ${exitCode}`);
}

const result = JSON.parse(stdout);
if (result.signature_mode !== "production" || result.valid !== true) {
  throw new Error("expected a valid production-signature receipt tree");
}

process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
