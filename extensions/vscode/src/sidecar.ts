import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { spawn } from 'child_process';

export interface ServeInfo {
  addr: string;
  token: string;
  pid: number;
}

export interface Conn {
  baseUrl: string;
  token: string;
}

// Mirrors Go's xdg.DataFile("agentgo") parent directory, which is where agentgo-serve.json lives.
export function defaultDataDir(): string {
  const home = os.homedir();
  switch (process.platform) {
    case 'win32':
      return process.env.LOCALAPPDATA || path.join(home, 'AppData', 'Local');
    case 'darwin':
      return path.join(home, 'Library', 'Application Support');
    default:
      return process.env.XDG_DATA_HOME || path.join(home, '.local', 'share');
  }
}

export function serveFilePath(override?: string): string {
  if (override && override.trim()) {
    return override.trim();
  }
  return path.join(defaultDataDir(), 'agentgo-serve.json');
}

export function readServeInfo(file: string): ServeInfo | undefined {
  try {
    const info = JSON.parse(fs.readFileSync(file, 'utf8')) as ServeInfo;
    return info && info.addr && info.token ? info : undefined;
  } catch {
    return undefined;
  }
}

async function healthy(addr: string): Promise<boolean> {
  try {
    const res = await fetch(`http://${addr}/health`, { signal: AbortSignal.timeout(1500) });
    return res.ok;
  } catch {
    return false;
  }
}

export interface EnsureOptions {
  serveFile?: string;
  binaryPath: string;
  workspaceRoot?: string;
  log: (line: string) => void;
}

/**
 * Returns a connection to a running sidecar, launching `agentgo --serve` detached when none answers.
 * The sidecar outlives VS Code on purpose: triggers must keep firing when the editor is closed.
 */
export async function ensureSidecar(opts: EnsureOptions): Promise<Conn> {
  const file = serveFilePath(opts.serveFile);
  let info = readServeInfo(file);
  if (info && (await healthy(info.addr))) {
    return { baseUrl: `http://${info.addr}`, token: info.token };
  }

  opts.log(`sidecar not running, launching: ${opts.binaryPath} --serve`);
  const env = { ...process.env };
  if (opts.workspaceRoot) {
    env.AGENTGO_WORKSPACE_ROOT = opts.workspaceRoot;
  }
  const child = spawn(opts.binaryPath, ['--serve'], {
    cwd: opts.workspaceRoot || os.homedir(),
    env,
    detached: true,
    stdio: 'ignore',
    windowsHide: true,
  });
  const spawnError = new Promise<never>((_, reject) => child.once('error', reject));
  child.unref();

  const deadline = Date.now() + 20000;
  const poll = (async (): Promise<Conn> => {
    while (Date.now() < deadline) {
      info = readServeInfo(file);
      if (info && (await healthy(info.addr))) {
        return { baseUrl: `http://${info.addr}`, token: info.token };
      }
      await new Promise((r) => setTimeout(r, 400));
    }
    throw new Error(`AgentGo sidecar did not become ready within 20s (looked for ${file})`);
  })();
  return Promise.race([poll, spawnError]);
}
