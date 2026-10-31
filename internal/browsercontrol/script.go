package browsercontrol

import (
	"encoding/json"
	"fmt"
)

// The scripts run inside the page. They never add attributes or elements to the
// page: the elements a snapshot lists are remembered in window.__agentgoRefs
// and addressed by index, so a later action can tell whether the element is
// still there.

// jsHelpers is shared by every script: how an element is named and described.
const jsHelpers = `
function squash(s, n) {
  s = String(s == null ? '' : s).replace(/\s+/g, ' ').trim();
  return s.length > n ? s.slice(0, n - 1) + '…' : s;
}
function roleOf(el) {
  var r = (el.getAttribute('role') || '').toLowerCase();
  var tag = el.tagName.toLowerCase();
  if (tag === 'a' && el.hasAttribute('href')) return 'link';
  if (tag === 'button' || tag === 'summary') return 'button';
  if (tag === 'select') return 'combobox';
  if (tag === 'textarea') return 'textbox';
  if (tag === 'input') {
    var t = (el.getAttribute('type') || 'text').toLowerCase();
    if (t === 'button' || t === 'submit' || t === 'reset' || t === 'image') return 'button';
    if (t === 'checkbox') return 'checkbox';
    if (t === 'radio') return 'radio';
    if (t === 'file') return 'file';
    if (t === 'range') return 'slider';
    return 'textbox';
  }
  if (r) return r;
  if (el.isContentEditable) return 'textbox';
  return 'clickable';
}
function nameOf(el) {
  var n = el.getAttribute('aria-label');
  if (n && n.trim()) return squash(n, 80);
  var lb = el.getAttribute('aria-labelledby');
  if (lb) {
    var d = el.ownerDocument;
    n = lb.split(/\s+/).map(function (i) { var e = d.getElementById(i); return e ? e.textContent : ''; }).join(' ');
    if (n.trim()) return squash(n, 80);
  }
  var tag = el.tagName.toLowerCase();
  if (el.labels && el.labels.length) {
    n = el.labels[0].textContent;
    if (n && n.trim()) return squash(n, 80);
  }
  if (tag === 'input') {
    var t = (el.getAttribute('type') || 'text').toLowerCase();
    if (t === 'button' || t === 'submit' || t === 'reset') return squash(el.value || el.getAttribute('title') || t, 80);
    if (t === 'image') return squash(el.getAttribute('alt') || el.getAttribute('title') || 'image button', 80);
    return squash(el.getAttribute('placeholder') || el.getAttribute('title') || el.getAttribute('name') || '', 80);
  }
  if (tag === 'textarea' || tag === 'select') {
    return squash(el.getAttribute('placeholder') || el.getAttribute('title') || el.getAttribute('name') || '', 80);
  }
  n = el.innerText || el.textContent || '';
  if (n.trim()) return squash(n, 80);
  var img = el.querySelector && el.querySelector('img[alt]');
  if (img && img.getAttribute('alt').trim()) return squash(img.getAttribute('alt'), 80);
  return squash(el.getAttribute('title') || el.getAttribute('alt') || el.getAttribute('name') || '', 80);
}
function visible(el) {
  var win = el.ownerDocument.defaultView;
  if (!win) return false;
  var r = el.getBoundingClientRect();
  if (r.width <= 0 || r.height <= 0) return false;
  var cs = win.getComputedStyle(el);
  return cs.visibility !== 'hidden' && cs.display !== 'none' && parseFloat(cs.opacity) !== 0;
}
function topRect(el) {
  var r = el.getBoundingClientRect(), x = r.left, y = r.top;
  var w = el.ownerDocument.defaultView;
  try {
    while (w && w !== w.top && w.frameElement) {
      var fr = w.frameElement.getBoundingClientRect();
      x += fr.left; y += fr.top; w = w.parent;
    }
  } catch (e) {}
  return { x: x, y: y, w: r.width, h: r.height };
}
function deepHit(x, y) {
  var el = document.elementFromPoint(x, y), ox = 0, oy = 0;
  for (var i = 0; i < 6 && el && /^(IFRAME|FRAME)$/.test(el.tagName); i++) {
    var d = null;
    try { d = el.contentDocument; } catch (e) {}
    if (!d) break;
    var fr = el.getBoundingClientRect();
    ox += fr.left; oy += fr.top;
    var inner = d.elementFromPoint(x - ox, y - oy);
    if (!inner) break;
    el = inner;
  }
  return el;
}
function refEl(ref) {
  var list = window.__agentgoRefs || [];
  var el = list[ref];
  if (!el) return { error: 'ref ' + ref + ' is not in the latest snapshot; take a new snapshot' };
  if (!el.isConnected) return { error: 'the element for ref ' + ref + ' is no longer on the page; take a new snapshot' };
  return { el: el };
}
`

// snapshotScript lists what can be operated and the text a person would read.
// Arguments: max elements, max text characters.
func snapshotScript(maxElements, maxText int) string {
	return fmt.Sprintf(`(function(MAX, TEXT){`+jsHelpers+`
var SEL = 'a[href],button,input:not([type=hidden]),select,textarea,summary,[role=button],[role=link],[role=checkbox],[role=radio],[role=tab],[role=menuitem],[role=option],[role=switch],[role=combobox],[role=textbox],[role=searchbox],[contenteditable=""],[contenteditable="true"],[onclick],[tabindex]:not([tabindex="-1"])';
var found = [], frames = 0, order = 0;
function collect(root, doc) {
  var list;
  try { list = root.querySelectorAll(SEL); } catch (e) { return; }
  for (var i = 0; i < list.length; i++) {
    var el = list[i];
    if (!visible(el)) continue;
    var rc = topRect(el);
    var inView = rc.y + rc.h > 0 && rc.y < window.innerHeight && rc.x + rc.w > 0 && rc.x < window.innerWidth;
    found.push({ el: el, order: order++, inView: inView });
  }
  var all;
  try { all = root.querySelectorAll('*'); } catch (e) { return; }
  for (var j = 0; j < all.length; j++) {
    var host = all[j];
    if (host.shadowRoot) collect(host.shadowRoot, doc);
    var tag = host.tagName;
    if (tag === 'IFRAME' || tag === 'FRAME') {
      frames++;
      var sub = null;
      try { sub = host.contentDocument; } catch (e) {}
      if (sub && sub.documentElement) collect(sub, sub);
    }
  }
}
collect(document, document);
var total = found.length;
if (found.length > MAX) {
  var vis = found.filter(function (f) { return f.inView; });
  var rest = found.filter(function (f) { return !f.inView; });
  found = vis.concat(rest).slice(0, MAX).sort(function (a, b) { return a.order - b.order; });
}
window.__agentgoRefs = found.map(function (f) { return f.el; });
var items = found.map(function (f, i) {
  var el = f.el, tag = el.tagName.toLowerCase();
  var it = { ref: i, role: roleOf(el), name: nameOf(el), inView: f.inView };
  var type = (el.getAttribute('type') || '').toLowerCase();
  if (tag === 'input') it.type = type || 'text';
  if (tag === 'a') it.href = squash(el.href || el.getAttribute('href') || '', 160);
  if (tag === 'input' || tag === 'textarea') {
    if (type === 'password') it.secret = true;
    else if (type !== 'checkbox' && type !== 'radio' && el.value) it.value = squash(el.value, 80);
  }
  if (tag === 'select') {
    var opts = [];
    for (var k = 0; k < el.options.length && k < 12; k++) opts.push(squash(el.options[k].text, 40));
    it.options = opts;
    it.optionCount = el.options.length;
    if (el.selectedIndex >= 0) it.value = squash(el.options[el.selectedIndex].text, 60);
  }
  if (type === 'checkbox' || type === 'radio' || el.getAttribute('aria-checked') != null) {
    it.checked = el.checked === true || el.getAttribute('aria-checked') === 'true';
  }
  if (el.disabled || el.getAttribute('aria-disabled') === 'true') it.disabled = true;
  if (el.readOnly) it.readonly = true;
  if (el.ownerDocument.activeElement === el) it.focused = true;
  if (el.getAttribute('aria-expanded') != null) it.expanded = el.getAttribute('aria-expanded') === 'true';
  return it;
});
var body = '';
try { body = document.body ? document.body.innerText : ''; } catch (e) {}
var fr = document.querySelectorAll('iframe,frame');
for (var q = 0; q < fr.length && body.length < TEXT; q++) {
  try {
    var fb = fr[q].contentDocument && fr[q].contentDocument.body;
    if (fb && fb.innerText && fb.innerText.trim()) body += '\n' + fb.innerText;
  } catch (e) {}
}
body = body.replace(/[ \t]+\n/g, '\n').replace(/\n{3,}/g, '\n\n').trim();
var textTotal = body.length;
if (body.length > TEXT) body = body.slice(0, TEXT);
return JSON.stringify({
  title: document.title || '', url: location.href,
  scrollY: Math.round(window.scrollY), scrollHeight: Math.round(document.documentElement.scrollHeight), viewHeight: window.innerHeight,
  elements: items, totalElements: total, frames: frames, text: body, textTotal: textTotal
});
})(%d, %d)`, maxElements, maxText)
}

// refTarget scrolls an element into view and reports where to click it.
func refTargetScript(ref int) string {
	return fmt.Sprintf(`(function(ref){`+jsHelpers+`
var f = refEl(ref); if (f.error) return JSON.stringify({ error: f.error });
var el = f.el;
var role = roleOf(el), name = nameOf(el);
if (el.disabled || el.getAttribute('aria-disabled') === 'true') return JSON.stringify({ error: 'the element is disabled', name: name, role: role });
try { el.scrollIntoView({ block: 'center', inline: 'center' }); } catch (e) {}
var rc = topRect(el);
var cx = Math.min(Math.max(rc.x + rc.w / 2, 1), window.innerWidth - 1);
var cy = Math.min(Math.max(rc.y + rc.h / 2, 1), window.innerHeight - 1);
if (rc.w <= 0 || rc.h <= 0) return JSON.stringify({ error: 'the element has no size on screen', name: name, role: role });
var hit = deepHit(cx, cy);
var ok = !!hit && (hit === el || el.contains(hit) || hit.contains(el));
var cover = '';
if (!ok && hit) cover = hit.tagName.toLowerCase() + (hit.id ? '#' + hit.id : '') + ' "' + squash(hit.innerText || hit.getAttribute('aria-label') || '', 40) + '"';
return JSON.stringify({ ok: ok, x: cx, y: cy, name: name, role: role, tag: el.tagName.toLowerCase(), coveredBy: cover });
})(%d)`, ref)
}

// refInfoScript names the element behind a ref without touching it, so the
// caller can check it is the element the agent meant before acting on it.
func refInfoScript(ref int) string {
	return fmt.Sprintf(`(function(ref){`+jsHelpers+`
var f = refEl(ref); if (f.error) return JSON.stringify({ error: f.error });
var el = f.el;
return JSON.stringify({ name: nameOf(el), role: roleOf(el), tag: el.tagName.toLowerCase() });
})(%d)`, ref)
}

// refFocusScript prepares a text field for typing, or sets a select.
// mode is "text" to focus and select the content, "value" to set a select.
func refFocusScript(ref int, value string) string {
	v, _ := json.Marshal(value)
	return fmt.Sprintf(`(function(ref, want){`+jsHelpers+`
var f = refEl(ref); if (f.error) return JSON.stringify({ error: f.error });
var el = f.el, tag = el.tagName.toLowerCase(), name = nameOf(el);
var type = (el.getAttribute('type') || '').toLowerCase();
if (el.disabled || el.getAttribute('aria-disabled') === 'true') return JSON.stringify({ error: 'the field is disabled', name: name });
if (el.readOnly) return JSON.stringify({ error: 'the field is read-only', name: name });
if (tag === 'select') {
  var idx = -1, lw = want.toLowerCase();
  for (var i = 0; i < el.options.length && idx < 0; i++) if (el.options[i].value === want || el.options[i].text.trim() === want) idx = i;
  for (var j = 0; j < el.options.length && idx < 0; j++) if (el.options[j].text.trim().toLowerCase().indexOf(lw) >= 0) idx = j;
  if (idx < 0) {
    var names = []; for (var k = 0; k < el.options.length && k < 20; k++) names.push(squash(el.options[k].text, 40));
    return JSON.stringify({ error: 'no option matches "' + want + '"; options: ' + names.join(' | '), name: name });
  }
  el.focus(); el.selectedIndex = idx;
  el.dispatchEvent(new Event('input', { bubbles: true })); el.dispatchEvent(new Event('change', { bubbles: true }));
  return JSON.stringify({ kind: 'select', name: name, value: squash(el.options[idx].text, 80) });
}
if (type === 'password') return JSON.stringify({ error: 'password fields are never filled by the agent; ask the person to type it in the browser window', name: name, secret: true });
if (type === 'checkbox' || type === 'radio' || type === 'button' || type === 'submit' || type === 'reset' || type === 'file' || type === 'image' || type === 'range') {
  return JSON.stringify({ error: 'this is a ' + type + ' input; use browser_click for it', name: name });
}
try { el.scrollIntoView({ block: 'center', inline: 'center' }); } catch (e) {}
if (tag === 'input' || tag === 'textarea') { el.focus(); try { el.select(); } catch (e) {} return JSON.stringify({ kind: 'text', name: name }); }
if (el.isContentEditable) {
  el.focus();
  var d = el.ownerDocument, rg = d.createRange(); rg.selectNodeContents(el);
  var sel = d.defaultView.getSelection(); sel.removeAllRanges(); sel.addRange(rg);
  return JSON.stringify({ kind: 'text', name: name });
}
return JSON.stringify({ error: 'this element does not take text; use browser_click', name: name });
})(%d, %s)`, ref, v)
}

// refValueScript reads back what a field now holds.
func refValueScript(ref int) string {
	return fmt.Sprintf(`(function(ref){`+jsHelpers+`
var f = refEl(ref); if (f.error) return '';
var el = f.el;
if (el.isContentEditable && el.tagName.toLowerCase() !== 'input') return squash(el.innerText, 120);
return squash(el.value, 120);
})(%d)`, ref)
}

const pageInfoScript = `JSON.stringify({ url: location.href, title: document.title || '', ready: document.readyState })`

const viewportScript = `JSON.stringify({ w: window.innerWidth, h: window.innerHeight, y: Math.round(window.scrollY), max: Math.round(document.documentElement.scrollHeight - window.innerHeight) })`

func scrollToScript(bottom bool) string {
	if bottom {
		return `window.scrollTo(0, Math.max(document.documentElement.scrollHeight, document.body ? document.body.scrollHeight : 0)); 0`
	}
	return `window.scrollTo(0, 0); 0`
}
