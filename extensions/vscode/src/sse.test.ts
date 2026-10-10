// Plain node test (no vscode import): `npm test`.
import * as assert from 'assert';
import { SseParser } from './sse';

const p = new SseParser();
// event split across chunks, CRLF tolerated
assert.deepStrictEqual(p.push('event: chunk\ndata: {"delta":"he'), []);
const a = p.push('llo"}\n\nevent: done\r\ndata: {"content":"x"}\r\n\n');
assert.deepStrictEqual(a, [
  { event: 'chunk', data: { delta: 'hello' } },
  { event: 'done', data: { content: 'x' } },
]);
// non-JSON payload stays raw; comment-only blocks are ignored
assert.deepStrictEqual(p.push(': ping\n\ndata: plain\n\n'), [{ event: 'message', data: 'plain' }]);
console.log('sse parser ok');
