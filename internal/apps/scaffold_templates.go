package apps

import (
	"fmt"
	"html"
	"strings"
)

func buildChatHTML(name, displayName, description string) string {
	title := html.EscapeString(firstNonEmpty(displayName, name))
	desc := html.EscapeString(description)
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>%s</title>
  <link rel="stylesheet" href="static/style.css" />
</head>
<body>
  <div class="chat-header">
    <div>
      <h1>%s</h1>
      <div class="subtitle">%s</div>
    </div>
  </div>
  <div id="messages" class="chat-messages"></div>
  <div class="chat-input-area">
    <input id="input" type="text" placeholder="输入消息…" autocomplete="off" />
    <button id="send">发送</button>
  </div>
  <script src="/agentgo-app-helpers.js" data-app="%s"></script>
  <script src="static/app.js"></script>
</body>
</html>`, title, title, desc, html.EscapeString(name))
}

func buildStaticHTML(name, displayName, description string) string {
	title := html.EscapeString(firstNonEmpty(displayName, name))
	desc := html.EscapeString(description)
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>%s</title>
  <link rel="stylesheet" href="static/style.css" />
</head>
<body>
  <h1>%s</h1>
  <p>%s</p>
  <button id="btn">Ping</button>
  <pre id="out"></pre>
  <script src="/agentgo-app-helpers.js" data-app="%s"></script>
  <script src="static/app.js"></script>
</body>
</html>`, title, title, desc, html.EscapeString(name))
}

func buildWorkflowHTML(name, displayName, description, workflowID string) string {
	title := html.EscapeString(firstNonEmpty(displayName, name))
	desc := html.EscapeString(description)
	wf := html.EscapeString(workflowID)
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>%s</title>
  <link rel="stylesheet" href="static/style.css" />
</head>
<body data-workflow-id="%s">
  <div class="wf-container">
    <div class="wf-header">
      <h1>%s</h1>
      <p>%s</p>
    </div>
    <div class="wf-form">
      <label>输入</label>
      <textarea class="wf-input" name="input" rows="4" placeholder="传给工作流的文本或 JSON"></textarea>
      <button id="run">运行工作流</button>
    </div>
    <div id="result" class="wf-result" style="display:none;">
      <span id="status" class="wf-status"></span>
      <h3>输出</h3>
      <pre id="output"></pre>
    </div>
  </div>
  <script src="/agentgo-app-helpers.js" data-app="%s"></script>
  <script src="static/app.js"></script>
</body>
</html>`, title, wf, title, desc, html.EscapeString(name))
}

const chatCSS = `* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: system-ui, sans-serif; background: #0f172a; color: #e2e8f0; height: 100vh; display: flex; flex-direction: column; }
.chat-header { padding: 16px 24px; background: #1e293b; border-bottom: 1px solid #334155; }
.chat-header h1 { font-size: 18px; }
.subtitle { font-size: 12px; color: #94a3b8; margin-top: 4px; }
.chat-messages { flex: 1; overflow-y: auto; padding: 16px; display: flex; flex-direction: column; gap: 12px; }
.msg { max-width: 85%%; padding: 10px 14px; border-radius: 12px; font-size: 14px; line-height: 1.5; white-space: pre-wrap; }
.msg.user { align-self: flex-end; background: #6366f1; color: #fff; }
.msg.assistant { align-self: flex-start; background: #1e293b; border: 1px solid #334155; }
.chat-input-area { padding: 12px 16px; background: #1e293b; display: flex; gap: 8px; }
.chat-input-area input { flex: 1; padding: 10px 12px; border-radius: 8px; border: 1px solid #334155; background: #0f172a; color: #e2e8f0; }
.chat-input-area button { padding: 10px 20px; background: #6366f1; color: #fff; border: none; border-radius: 8px; cursor: pointer; }
`

const staticCSS = `body { font-family: system-ui; padding: 1rem; max-width: 640px; background: #0f172a; color: #e2e8f0; }
button { padding: 0.5rem 1rem; cursor: pointer; background: #6366f1; color: #fff; border: none; border-radius: 8px; }
pre { background: #1e293b; padding: 1rem; border-radius: 8px; margin-top: 1rem; }
`

const workflowCSS = `* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: system-ui, sans-serif; background: #0f172a; color: #e2e8f0; min-height: 100vh; }
.wf-container { max-width: 720px; margin: 0 auto; padding: 24px; }
.wf-header { margin-bottom: 20px; }
.wf-form { background: #1e293b; border: 1px solid #334155; border-radius: 12px; padding: 16px; }
.wf-form label { font-size: 12px; color: #94a3b8; }
.wf-form textarea { width: 100%%; margin: 8px 0 12px; padding: 10px; background: #0f172a; border: 1px solid #334155; border-radius: 8px; color: #e2e8f0; }
.wf-form button { width: 100%%; padding: 12px; background: #6366f1; color: #fff; border: none; border-radius: 8px; cursor: pointer; }
.wf-result { margin-top: 16px; background: #1e293b; border-radius: 12px; padding: 16px; }
.wf-status { font-size: 12px; font-weight: 600; }
.wf-status.ok { color: #10b981; }
.wf-status.err { color: #ef4444; }
pre { white-space: pre-wrap; font-size: 13px; }
`

const chatJS = `const messagesEl = document.getElementById('messages');
const inputEl = document.getElementById('input');
const sendBtn = document.getElementById('send');

function addMessage(role, content) {
  const div = document.createElement('div');
  div.className = 'msg ' + role;
  div.textContent = content;
  messagesEl.appendChild(div);
  messagesEl.scrollTop = messagesEl.scrollHeight;
  return div;
}

async function sendMessage() {
  const text = inputEl.value.trim();
  if (!text) return;
  inputEl.value = '';
  sendBtn.disabled = true;
  addMessage('user', text);
  const assistantDiv = addMessage('assistant', '…');
  try {
    const r = await agentGo.chat(text);
    const out = (r && (r.content || r.output || r.message)) || JSON.stringify(r);
    assistantDiv.textContent = typeof out === 'string' ? out : String(out);
  } catch (e) {
    assistantDiv.textContent = 'Error: ' + e.message;
  }
  sendBtn.disabled = false;
  inputEl.focus();
}

sendBtn.addEventListener('click', sendMessage);
inputEl.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); sendMessage(); } });
inputEl.focus();
`

const staticJS = `document.getElementById('btn').onclick = async () => {
  const out = document.getElementById('out');
  out.textContent = 'calling...';
  try {
    const r = await agentGo.apiCall('ping', { t: Date.now() });
    out.textContent = JSON.stringify(r, null, 2);
  } catch (e) { out.textContent = e.message; }
};
`

func buildWorkflowJS(workflowID string) string {
	wf := strings.ReplaceAll(workflowID, `\`, `\\`)
	wf = strings.ReplaceAll(wf, `'`, `\'`)
	return fmt.Sprintf(`const runBtn = document.getElementById('run');
const resultEl = document.getElementById('result');
const statusEl = document.getElementById('status');
const outputEl = document.getElementById('output');
const WF_ID = '%s';

runBtn.addEventListener('click', async () => {
  const input = (document.querySelector('.wf-input') || {}).value || '';
  runBtn.disabled = true;
  resultEl.style.display = 'none';
  try {
    const r = await agentGo.apiCall('workflow_run', { input });
    resultEl.style.display = 'block';
    statusEl.className = 'wf-status ok';
    statusEl.textContent = '完成';
    outputEl.textContent = typeof r === 'string' ? r : JSON.stringify(r, null, 2);
  } catch (e) {
    resultEl.style.display = 'block';
    statusEl.className = 'wf-status err';
    statusEl.textContent = '失败';
    outputEl.textContent = e.message;
  }
  runBtn.disabled = false;
});
`, wf)
}

type templateBundle struct {
	html, css, js string
}

func templateForMode(mode, name, displayName, description, workflowID string) (templateBundle, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "chat":
		return templateBundle{buildChatHTML(name, displayName, description), chatCSS, chatJS}, nil
	case "static":
		return templateBundle{buildStaticHTML(name, displayName, description), staticCSS, staticJS}, nil
	case "workflow":
		if strings.TrimSpace(workflowID) == "" {
			return templateBundle{}, fmt.Errorf("workflow mode requires workflow_id")
		}
		return templateBundle{
			buildWorkflowHTML(name, displayName, description, workflowID),
			workflowCSS,
			buildWorkflowJS(workflowID),
		}, nil
	default:
		return templateBundle{}, fmt.Errorf("mode must be chat, static, or workflow")
	}
}
