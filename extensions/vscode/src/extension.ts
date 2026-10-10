import * as vscode from 'vscode';
import { Conn, ensureSidecar } from './sidecar';
import { cancelChat, streamChat } from './sse';

const SESSION_KEY = 'agentgo.sessionId';

function newSessionId(): string {
  return `vscode_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
}

export function activate(context: vscode.ExtensionContext) {
  const out = vscode.window.createOutputChannel('AgentGo');
  const status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 50);
  status.command = 'agentgo.reconnect';
  status.text = '$(circle-slash) AgentGo';
  status.show();
  context.subscriptions.push(out, status);

  const workspaceRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  let conn: Conn | undefined;
  let connecting: Promise<Conn> | undefined;

  const connect = (): Promise<Conn> => {
    if (conn) {
      return Promise.resolve(conn);
    }
    if (!connecting) {
      const cfg = vscode.workspace.getConfiguration('agentgo');
      status.text = '$(sync~spin) AgentGo';
      connecting = ensureSidecar({
        binaryPath: cfg.get<string>('binaryPath', 'agentgo'),
        serveFile: cfg.get<string>('serveFile', ''),
        workspaceRoot,
        log: (l) => out.appendLine(l),
      })
        .then((c) => {
          conn = c;
          status.text = '$(check) AgentGo';
          status.tooltip = `Sidecar: ${c.baseUrl}`;
          out.appendLine(`connected to ${c.baseUrl}`);
          return c;
        })
        .catch((e) => {
          status.text = '$(error) AgentGo';
          status.tooltip = String(e?.message ?? e);
          out.appendLine(`connect failed: ${e?.message ?? e}`);
          throw e;
        })
        .finally(() => {
          connecting = undefined;
        });
    }
    return connecting;
  };

  const sessionId = (): string => {
    let id = context.workspaceState.get<string>(SESSION_KEY);
    if (!id) {
      id = newSessionId();
      void context.workspaceState.update(SESSION_KEY, id);
    }
    return id;
  };

  const participant = vscode.chat.createChatParticipant('agentgo.chat', async (request, _ctx, stream, token) => {
    let c: Conn;
    try {
      c = await connect();
    } catch (e: any) {
      stream.markdown(`**无法连接 AgentGo Sidecar:** ${e?.message ?? e}\n\n请检查设置 \`agentgo.binaryPath\`。`);
      return {};
    }

    let prompt = request.prompt;
    if (request.command === 'plan') {
      prompt = `[PlanTask Mode] ${prompt}`;
    } else if (request.command === 'code') {
      prompt = `[Code Mode] ${prompt}`;
    }

    const sid = sessionId();
    const ac = new AbortController();
    token.onCancellationRequested(() => {
      ac.abort();
      void cancelChat(c, sid);
    });

    try {
      await streamChat(c, sid, prompt, ac.signal, (e) => {
        if (e.event === 'chunk' && e.data?.delta) {
          stream.markdown(e.data.delta);
        } else if (e.event === 'interrupt') {
          stream.markdown(
            `\n\n> ⚠️ 工具 \`${e.data?.tool_name ?? '?'}\` 需要审批。VS Code 内审批尚未接入,请在 AgentGo 桌面端处理。\n`,
          );
        } else if (e.event === 'error') {
          stream.markdown(`\n\n**错误:** ${e.data?.error ?? JSON.stringify(e.data)}`);
        }
      });
    } catch (e: any) {
      if (!ac.signal.aborted) {
        // Sidecar may have restarted: drop the cached connection so the next turn rediscovers it.
        conn = undefined;
        status.text = '$(error) AgentGo';
        stream.markdown(`\n\n**请求失败:** ${e?.message ?? e}`);
      }
    }
    return { metadata: { command: request.command } };
  });
  participant.iconPath = new vscode.ThemeIcon('hubot');

  context.subscriptions.push(
    participant,
    vscode.commands.registerCommand('agentgo.reconnect', async () => {
      conn = undefined;
      try {
        await connect();
        vscode.window.showInformationMessage('AgentGo: 已连接');
      } catch (e: any) {
        vscode.window.showErrorMessage(`AgentGo: ${e?.message ?? e}`);
      }
    }),
    vscode.commands.registerCommand('agentgo.newSession', async () => {
      await context.workspaceState.update(SESSION_KEY, newSessionId());
      vscode.window.showInformationMessage('AgentGo: 已开启新会话');
    }),
    vscode.commands.registerCommand('agentgo.cancelRun', async () => {
      if (conn) {
        await cancelChat(conn, sessionId());
      }
    }),
  );

  // Connect eagerly so the status bar is truthful, but never block activation or nag on failure.
  void connect().catch(() => undefined);
}

export function deactivate() {
  // The sidecar is deliberately left running.
}
