import * as vscode from 'vscode';
import { spawn, ChildProcess } from 'child_process';
import * as readline from 'readline';

class AgentGoClient {
  private process: ChildProcess | null = null;
  private reqId = 0;
  private pending = new Map<number, { resolve: (val: any) => void; reject: (err: any) => void }>();
  private eventHandlers = new Map<string, (data: any) => void>();

  constructor(private binaryPath: string, private workspaceRoot: string) {}

  public start() {
    this.process = spawn(this.binaryPath, ['acp'], {
      cwd: this.workspaceRoot,
      stdio: ['pipe', 'pipe', 'inherit'],
    });

    const rl = readline.createInterface({
      input: this.process.stdout!,
      terminal: false,
    });

    rl.on('line', (line) => {
      try {
        const msg = JSON.parse(line);
        if (msg.method === 'session/event') {
          const handler = this.eventHandlers.get(msg.params.session_id);
          if (handler) {
            handler(msg.params);
          }
        } else if (msg.id !== undefined) {
          const promise = this.pending.get(msg.id);
          if (promise) {
            this.pending.delete(msg.id);
            if (msg.error) {
              promise.reject(new Error(msg.error.message));
            } else {
              promise.resolve(msg.result);
            }
          }
        }
      } catch (e) {
        console.error('Failed to parse ACP message:', line, e);
      }
    });

    this.send('initialize', {
      client_name: 'VSCode-AgentGo',
      client_version: '0.10.0',
    });
  }

  public send(method: string, params: any): Promise<any> {
    const id = ++this.reqId;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      const payload = JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n';
      this.process?.stdin?.write(payload);
    });
  }

  public registerSessionEvents(sessionId: string, callback: (data: any) => void) {
    this.eventHandlers.set(sessionId, callback);
  }

  public stop() {
    if (this.process) {
      this.process.kill();
      this.process = null;
    }
  }
}

export function activate(context: vscode.ExtensionContext) {
  const workspaceRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath || process.cwd();
  const config = vscode.workspace.getConfiguration('agentgo');
  const binaryPath = config.get<string>('binaryPath', 'agentgo');

  const client = new AgentGoClient(binaryPath, workspaceRoot);
  try {
    client.start();
  } catch (err) {
    vscode.window.showErrorMessage(`Failed to start AgentGo process: ${err}`);
  }

  const participant = vscode.chat.createChatParticipant('agentgo.chat', async (request, chatContext, stream, token) => {
    const sessionId = `vscode_${Date.now()}`;

    client.registerSessionEvents(sessionId, (event) => {
      if (event.type === 'chunk' && event.data?.delta) {
        stream.markdown(event.data.delta);
      } else if (event.type === 'tool_call') {
        stream.progress(`Executing tool: ${event.data.name}...`);
      }
    });

    token.onCancellationRequested(() => {
      client.send('session/cancel', { session_id: sessionId });
    });

    try {
      let prompt = request.prompt;
      if (request.command === 'plan') {
        prompt = `[PlanTask Mode] ${prompt}`;
      } else if (request.command === 'code') {
        prompt = `[Code Mode] ${prompt}`;
      }

      await client.send('session/prompt', {
        session_id: sessionId,
        prompt: prompt,
      });
    } catch (error: any) {
      stream.markdown(`\n**Error:** ${error.message}`);
    }

    return { metadata: { command: request.command } };
  });

  context.subscriptions.push(participant);
  context.subscriptions.push({
    dispose: () => client.stop(),
  });
}

export function deactivate() {}
