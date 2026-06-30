#!/usr/bin/env node

import { existsSync, promises as fs, readFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { request as httpsRequest } from "node:https";
import { randomUUID } from "node:crypto";
import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
import * as tar from "tar";

const API_BASE_URL = "https://simple-host.app";
const CONFIG_DIR = join(homedir(), ".website-deploy");
const CONFIG_PATH = join(CONFIG_DIR, "config.json");

// SKILL_VERSION is read from .claude-plugin/plugin.json at module load.
// The plugin.json sits two dirs above dist/index.js when the plugin is
// installed via setup.sh. Falls back to "0.0.0" if not readable — the
// server treats anything mismatched as stale, so a fallback still
// triggers the upgrade notice.
const SKILL_VERSION: string = (() => {
  try {
    const here = dirname(fileURLToPath(import.meta.url));
    const raw = readFileSync(join(here, "..", "..", ".claude-plugin", "plugin.json"), "utf8");
    const v = JSON.parse(raw)?.version;
    return typeof v === "string" && v ? v : "0.0.0";
  } catch {
    return "0.0.0";
  }
})();

// pendingNotice holds the most recent server-issued `_notice` string,
// surfaced in the next tool result and then cleared. Re-fetched on
// every subsequent request that the server still considers stale.
let pendingNotice: string | undefined;

type AuthResponse = {
  id: string | number;
  username: string;
  api_key: string;
  is_admin: boolean;
  created: string;
};

type SiteInfo = {
  name: string;
  [key: string]: unknown;
};

type Config = {
  api_key: string;
};

function assertString(value: unknown, field: string): string {
  if (typeof value !== "string" || value.trim() === "") {
    throw new Error(`${field} must be a non-empty string`);
  }

  return value.trim();
}

async function ensureConfigDir(): Promise<void> {
  await fs.mkdir(CONFIG_DIR, { recursive: true, mode: 0o700 });
}

async function writeConfig(config: Config): Promise<void> {
  await ensureConfigDir();
  await fs.writeFile(CONFIG_PATH, `${JSON.stringify(config, null, 2)}\n`, {
    mode: 0o600,
  });
  await fs.chmod(CONFIG_PATH, 0o600);
}

async function readConfig(): Promise<Config> {
  let raw: string;

  try {
    raw = await fs.readFile(CONFIG_PATH, "utf8");
  } catch (error) {
    throw new Error(`Missing API config at ${CONFIG_PATH}. Run register first.`);
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new Error(`Invalid JSON in ${CONFIG_PATH}`);
  }

  if (
    typeof parsed !== "object" ||
    parsed === null ||
    typeof (parsed as Partial<Config>).api_key !== "string" ||
    (parsed as Partial<Config>).api_key?.trim() === ""
  ) {
    throw new Error(`Missing api_key in ${CONFIG_PATH}`);
  }

  return { api_key: (parsed as Config).api_key };
}

function httpRequest(
  method: string,
  path: string,
  headers: Record<string, string> = {},
  body?: Uint8Array | string,
): Promise<{ statusCode: number; headers: Record<string, string | string[] | undefined>; body: Buffer }> {
  return new Promise((resolvePromise, reject) => {
    const req = httpsRequest(
      `${API_BASE_URL}${path}`,
      {
        method,
        headers: {
          "X-Skill-Version": SKILL_VERSION,
          ...headers,
          ...(body ? { "Content-Length": Buffer.byteLength(body).toString() } : {}),
        },
      },
      (res) => {
        const chunks: Uint8Array[] = [];
        res.on("data", (chunk) => {
          const nextChunk = Buffer.isBuffer(chunk)
            ? new Uint8Array(chunk)
            : Uint8Array.from(Buffer.from(chunk));
          chunks.push(nextChunk);
        });
        res.on("end", () => {
          const totalLength = chunks.reduce((sum, chunk) => sum + chunk.byteLength, 0);
          const combined = new Uint8Array(totalLength);
          let offset = 0;

          for (const chunk of chunks) {
            combined.set(chunk, offset);
            offset += chunk.byteLength;
          }

          resolvePromise({
            statusCode: res.statusCode ?? 0,
            headers: res.headers,
            body: Buffer.from(combined),
          });
        });
      },
    );

    req.on("error", reject);

    if (body) {
      req.write(body);
    }

    req.end();
  });
}

function parseJsonResponse(buffer: Buffer): unknown {
  if (buffer.length === 0) {
    return null;
  }

  const text = buffer.toString("utf8");
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

async function requestJson(
  method: string,
  path: string,
  headers: Record<string, string> = {},
  body?: Uint8Array | string,
): Promise<unknown> {
  const response = await httpRequest(method, path, headers, body);
  const parsed = parseJsonResponse(response.body);

  if (response.statusCode < 200 || response.statusCode >= 300) {
    throw new Error(`Request failed (${response.statusCode}): ${JSON.stringify(parsed)}`);
  }

  return extractNotice(parsed);
}

// extractNotice strips the server-injected `_notice` field from a
// response body and stashes it for the next formatResult call. Returns
// the response unwrapped to its pre-notice shape so downstream callers
// can keep treating arrays as arrays.
function extractNotice(parsed: unknown): unknown {
  if (typeof parsed !== "object" || parsed === null) {
    return parsed;
  }

  const obj = parsed as Record<string, unknown>;
  if (typeof obj._notice === "string") {
    pendingNotice = obj._notice;
  } else {
    return parsed;
  }

  // Array-wrapped shape: { data: [...], _notice }
  const keys = Object.keys(obj);
  if (Array.isArray(obj.data) && keys.length === 2 && keys.includes("data") && keys.includes("_notice")) {
    return obj.data;
  }

  // Object shape: drop _notice, return the rest.
  const { _notice, ...rest } = obj;
  return rest;
}

async function listSites(apiKey: string): Promise<SiteInfo[]> {
  const result = await requestJson("GET", "/v1/sites", { "X-API-Key": apiKey });

  if (!Array.isArray(result)) {
    throw new Error("Expected /v1/sites to return an array");
  }

  return result as SiteInfo[];
}

async function createArchive(directory: string): Promise<Buffer> {
  const sourceDir = resolve(directory);
  const stats = await fs.stat(sourceDir).catch(() => null);

  if (!stats || !stats.isDirectory()) {
    throw new Error(`Directory not found: ${sourceDir}`);
  }

  const archivePath = join(tmpdir(), `website-deploy-${randomUUID()}.tar.gz`);

  try {
    await tar.c(
      {
        gzip: true,
        cwd: sourceDir,
        file: archivePath,
      },
      ["."],
    );

    return await fs.readFile(archivePath);
  } finally {
    if (existsSync(archivePath)) {
      await fs.unlink(archivePath).catch(() => undefined);
    }
  }
}

async function register(email: string): Promise<AuthResponse> {
  const payload = JSON.stringify({ email });
  const response = (await requestJson(
    "POST",
    "/v1/auth",
    {
      "Content-Type": "application/json",
    },
    payload,
  )) as AuthResponse;

  if (typeof response !== "object" || response === null || typeof response.api_key !== "string") {
    throw new Error("Auth response did not include api_key");
  }

  await writeConfig({ api_key: response.api_key });
  return response;
}

async function deploy(directory: string, siteName: string): Promise<unknown> {
  const { api_key: apiKey } = await readConfig();
  const sites = await listSites(apiKey);
  const archive = await createArchive(directory);
  const existingSite = sites.find((site) => site.name === siteName);
  const method = existingSite ? "PUT" : "POST";

  return await requestJson(
    method,
    `/v1/sites/${encodeURIComponent(siteName)}`,
    {
      "Content-Type": "application/gzip",
      "X-API-Key": apiKey,
    },
    new Uint8Array(archive),
  );
}

async function status(siteName: string): Promise<unknown> {
  const { api_key: apiKey } = await readConfig();
  const sites = await listSites(apiKey);
  const site = sites.find((entry) => entry.name === siteName);
  return site ?? "site not found";
}

async function list(): Promise<SiteInfo[]> {
  const { api_key: apiKey } = await readConfig();
  return await listSites(apiKey);
}

function formatResult(result: unknown) {
  const content: { type: "text"; text: string }[] = [];
  if (pendingNotice) {
    content.push({ type: "text", text: `NOTICE: ${pendingNotice}` });
    pendingNotice = undefined;
  }
  content.push({ type: "text", text: JSON.stringify(result, null, 2) });
  return { content };
}

const server = new Server(
  {
    name: "website-deploy-mcp",
    version: "1.0.0",
  },
  {
    capabilities: {
      tools: {},
    },
  },
);

server.setRequestHandler(ListToolsRequestSchema, async () => ({
  tools: [
    {
      name: "register",
      description: "Register a Website Deploy account and persist the API key locally.",
      inputSchema: {
        type: "object",
        properties: {
          email: {
            type: "string",
            description: "Email address to register.",
          },
        },
        required: ["email"],
        additionalProperties: false,
      },
    },
    {
      name: "deploy",
      description: "Create or update a site by uploading a tar.gz archive of a directory.",
      inputSchema: {
        type: "object",
        properties: {
          directory: {
            type: "string",
            description: "Path to the directory to archive and deploy.",
          },
          site_name: {
            type: "string",
            description: "Site name in Website Deploy.",
          },
        },
        required: ["directory", "site_name"],
        additionalProperties: false,
      },
    },
    {
      name: "status",
      description: "Get information for a specific site.",
      inputSchema: {
        type: "object",
        properties: {
          site_name: {
            type: "string",
            description: "Site name in Website Deploy.",
          },
        },
        required: ["site_name"],
        additionalProperties: false,
      },
    },
    {
      name: "list",
      description: "List all sites visible to the configured API key.",
      inputSchema: {
        type: "object",
        properties: {},
        additionalProperties: false,
      },
    },
  ],
}));

server.setRequestHandler(CallToolRequestSchema, async (request) => {
  try {
    switch (request.params.name) {
      case "register": {
        const email = assertString(request.params.arguments?.email, "email");
        return formatResult(await register(email));
      }
      case "deploy": {
        const directory = assertString(request.params.arguments?.directory, "directory");
        const siteName = assertString(request.params.arguments?.site_name, "site_name");
        return formatResult(await deploy(directory, siteName));
      }
      case "status": {
        const siteName = assertString(request.params.arguments?.site_name, "site_name");
        return formatResult(await status(siteName));
      }
      case "list":
        return formatResult(await list());
      default:
        throw new Error(`Unknown tool: ${request.params.name}`);
    }
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return {
      isError: true,
      content: [
        {
          type: "text",
          text: message,
        },
      ],
    };
  }
});

async function main(): Promise<void> {
  const transport = new StdioServerTransport();
  await server.connect(transport);
}

main().catch((error) => {
  const message = error instanceof Error ? error.stack ?? error.message : String(error);
  process.stderr.write(`${message}\n`);
  process.exit(1);
});
