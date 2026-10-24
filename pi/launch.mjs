#!/usr/bin/env node
// launch.mjs — the ONLY supported daily entry point for the restricted
// timesheet Pi profile (R5).
//
// It starts the pinned local Pi CLI with `spawn` (no shell, absolute trusted
// paths) and a fixed isolation set: isolated working directory and dedicated
// profile directory, no ambient extensions/skills/prompt templates/MCP/context
// files, no built-in tools, no session, offline automatic refresh/telemetry,
// and exactly one trusted adapter extension. Provisioning arguments name the
// fixed compiled binary, the non-secret config path, the dotenv path, the
// dedicated profile and an optional model choice. Those are operator inputs:
// they are never model arguments, never request fields, and no free-form Pi
// arguments pass through.
//
// The launcher never reads the configuration or dotenv contents; the compiled
// Go child loads them. It never prints their contents, credential values or
// derived paths into a transcript.

import { spawn } from "node:child_process";
import { dirname, isAbsolute, join, resolve } from "node:path";
import { mkdirSync, statSync, existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));

const USAGE = `usage: node launch.mjs --binary <abs-go-binary> --config <abs-config.json> \\
  --env-file <abs-dotenv> --profile-dir <abs-dedicated-pi-profile> \\
  [--workdir <abs-isolated-workdir>] [--model <name>] [--provider <name>] \\
  [--forward-env NAME,NAME,...]

All paths must be absolute. Only the trusted adapter next to this launcher is
loaded; no test provisioning flags exist in production.`;

const DEFAULT_FORWARD_ENV = ["GITHUB_TOKEN", "GCAL_ICS_URL", "GRADION_MCP_TOKEN"];
const ENV_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const DATE_PATH_RE = /\.(json|env|local)$/i;

function fail(message) {
  process.stderr.write(`timesheet-launcher: ${message}\n`);
  process.exit(2);
}

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i];
    switch (token) {
      case "--binary":
      case "--config":
      case "--env-file":
      case "--profile-dir":
      case "--workdir":
      case "--model":
      case "--provider":
      case "--forward-env": {
        const value = argv[i + 1];
        if (!value || value.startsWith("--")) fail(`missing value for ${token}`);
        out[token.slice(2)] = value;
        i += 1;
        break;
      }
      case "--help":
      case "-h":
        process.stdout.write(USAGE + "\n");
        process.exit(0);
        break;
      default:
        fail(`unknown argument ${token}\n${USAGE}`);
    }
  }
  return out;
}

function requireAbsolute(label, value) {
  if (!value) fail(`${label} is required and must be an absolute path`);
  if (!isAbsolute(value)) fail(`${label} must be an absolute path`);
  return value;
}

function requireFile(label, value) {
  requireAbsolute(label, value);
  let stat;
  try {
    stat = statSync(value);
  } catch {
    fail(`${label} does not exist`);
  }
  if (!stat.isFile()) fail(`${label} must be a regular file`);
  return value;
}

const args = parseArgs(process.argv.slice(2));

const binary = requireFile("--binary", args.binary);
const configPath = requireFile("--config", args.config);
const envFile = requireFile("--env-file", args["env-file"]);
const profileDir = requireAbsolute("--profile-dir", args["profile-dir"]);
if (DATE_PATH_RE.test(binary)) fail("--binary must name a compiled executable, not a config/env file");

const workdir = args.workdir ? requireAbsolute("--workdir", args.workdir) : join(profileDir, "workdir");
mkdirSync(profileDir, { recursive: true, mode: 0o700 });
mkdirSync(workdir, { recursive: true, mode: 0o700 });

const forwardNames = (args["forward-env"] ?? DEFAULT_FORWARD_ENV.join(","))
  .split(",")
  .map((n) => n.trim())
  .filter(Boolean);
for (const name of forwardNames) {
  if (!ENV_NAME_RE.test(name)) fail(`--forward-env contains an invalid variable name: ${name}`);
}

const adapterPath = join(HERE, "timesheet.ts");
if (!existsSync(adapterPath)) fail("trusted adapter extension is missing next to the launcher");

// Resolve the pinned Pi CLI from THIS package's own dependency tree: an
// absolute trusted path, never a PATH lookup. The package publishes no
// ".". exports entry, so the launcher locates its own installed copy directly.
const piRoot = join(HERE, "node_modules", "@earendil-works", "pi-coding-agent");
if (!existsSync(join(piRoot, "package.json"))) {
  fail("pinned Pi package is not installed next to the launcher (run npm ci in pi/)");
}
const piPackage = JSON.parse(readFileSync(join(piRoot, "package.json"), "utf8"));
const piBin = piPackage.bin && piPackage.bin.pi;
if (!piBin) fail("pinned Pi package does not declare its CLI entry point");
const piCli = resolve(piRoot, piBin);
if (!existsSync(piCli)) fail("pinned Pi CLI entry point is missing");

// Environment for the Pi process: required OS variables plus the explicitly
// provisioned timesheet credential NAMES (values preserved verbatim, including
// empty strings). Chat-provider secrets are never copied around wholesale.
const childEnv = {};
for (const name of ["PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "TZ", "SHELL", "USER", "LOGNAME", "TERM"]) {
  if (process.env[name] !== undefined) childEnv[name] = process.env[name];
}
for (const name of forwardNames) {
  if (process.env[name] !== undefined) childEnv[name] = process.env[name];
}
Object.assign(childEnv, {
  // Dedicated daily profile: never the owner's normal Pi profile/settings/auth.
  PI_CODING_AGENT_DIR: profileDir,
  // Offline automatic network activity; this is not an OS sandbox.
  PI_OFFLINE: "1",
  PI_SKIP_VERSION_CHECK: "1",
  PI_TELEMETRY: "0",
  // Fixed, non-secret launcher inputs consumed by the trusted adapter. They are
  // file PATHS only; the adapter never reads the files themselves.
  TIMESHEET_CHILD_BINARY: binary,
  TIMESHEET_CHILD_CONFIG: configPath,
  TIMESHEET_CHILD_ENVFILE: envFile,
  TIMESHEET_CHILD_WORKDIR: workdir,
  TIMESHEET_FORWARD_ENV: forwardNames.join(","),
});

// Fixed isolation set. `--no-extensions` still loads explicit trusted paths;
// `--no-builtin-tools` plus `--tools timesheet_prepare` makes exactly one tool
// declared. `--no-session` keeps the session in memory (no transcript writes).
const piArgs = [
  "--no-extensions",
  "--extension", adapterPath,
  "--no-context-files",
  "--no-skills",
  "--no-prompt-templates",
  "--no-mcp",
  "--no-builtin-tools",
  "--tools", "timesheet_prepare",
  "--no-session",
];
if (args.provider) piArgs.push("--provider", args.provider);
if (args.model) piArgs.push("--model", args.model);

const child = spawn(process.execPath, [piCli, ...piArgs], {
  cwd: workdir, // isolated working directory: never the repository
  env: childEnv,
  shell: false,
  stdio: "inherit",
});

child.on("error", (error) => {
  process.stderr.write(`timesheet-launcher: failed to start Pi: ${error.code ?? "unknown"}\n`);
  process.exit(1);
});
child.on("exit", (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 1);
});
for (const sig of ["SIGINT", "SIGTERM", "SIGHUP"]) {
  process.on(sig, () => {
    if (!child.killed) child.kill(sig);
  });
}
