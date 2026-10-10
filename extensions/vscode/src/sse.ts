import { Conn } from './sidecar';

export interface SseEvent {
  event: string;
  data: any;
}

/** Incremental SSE parser: feed text chunks, get complete events. Exported for tests. */
export class SseParser {
  private buf = '';

  push(chunk: string): SseEvent[] {
    this.buf += chunk.replace(/\r\n/g, '\n');
    const out: SseEvent[] = [];
    let idx: number;
    while ((idx = this.buf.indexOf('\n\n')) >= 0) {
      const block = this.buf.slice(0, idx);
      this.buf = this.buf.slice(idx + 2);
      let event = 'message';
      const dataLines: string[] = [];
      for (const line of block.split('\n')) {
        if (line.startsWith('event:')) {
          event = line.slice(6).trim();
        } else if (line.startsWith('data:')) {
          dataLines.push(line.slice(5).replace(/^ /, ''));
        }
      }
      if (dataLines.length === 0) {
        continue;
      }
      const raw = dataLines.join('\n');
      let data: any = raw;
      try {
        data = JSON.parse(raw);
      } catch {
        // keep raw text
      }
      out.push({ event, data });
    }
    return out;
  }
}

function authHeaders(conn: Conn): Record<string, string> {
  return { Authorization: `Bearer ${conn.token}`, 'Content-Type': 'application/json' };
}

export async function streamChat(
  conn: Conn,
  sessionId: string,
  message: string,
  signal: AbortSignal,
  onEvent: (e: SseEvent) => void,
): Promise<void> {
  const res = await fetch(`${conn.baseUrl}/api/v1/chat/stream`, {
    method: 'POST',
    headers: authHeaders(conn),
    body: JSON.stringify({ session_id: sessionId, message }),
    signal,
  });
  if (!res.ok || !res.body) {
    throw new Error(`chat stream failed: HTTP ${res.status} ${await res.text().catch(() => '')}`);
  }
  const parser = new SseParser();
  const decoder = new TextDecoder();
  const reader = res.body.getReader();
  for (;;) {
    const { done, value } = await reader.read();
    if (done) {
      break;
    }
    for (const e of parser.push(decoder.decode(value, { stream: true }))) {
      onEvent(e);
    }
  }
}

export async function cancelChat(conn: Conn, sessionId: string): Promise<void> {
  await fetch(`${conn.baseUrl}/api/v1/chat/cancel`, {
    method: 'POST',
    headers: authHeaders(conn),
    body: JSON.stringify({ session_id: sessionId }),
  }).catch(() => undefined);
}
