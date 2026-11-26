// markdown.js：极简 Markdown → DOM 渲染器，零第三方库、无构建步骤。
//
// 整条链路只用 createElement / createTextNode 拼节点，不碰 innerHTML，
// 所以 HTML 注入不可能发生（CSP 也仍是 default-src 'self'）。
// 支持：标题、段落（保留换行）、粗体/斜体/删除线、行内代码、围栏代码块、
// 引用、有序/无序列表（含嵌套与任务清单）、分隔线、表格、链接、同源图片。
import { iconEl } from './ui.js';

// renderMarkdown 把 src 渲染进 host，替换原有子节点。
export function renderMarkdown(host, src) {
  if (!host) return;
  host.classList.add('md');
  host.replaceChildren(...parseBlocks(toLines(src)));
}

function toLines(src) {
  return String(src ?? '').replace(/\r\n?/g, '\n').split('\n');
}

// ---------- 块级语法 ----------

function parseBlocks(lines) {
  const out = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];

    if (!line.trim()) {
      i++;
      continue;
    }

    // 围栏代码块 ``` / ~~~
    if (isFence(line)) {
      const fence = parseFence(lines, i);
      out.push(fence.node);
      i = fence.next;
      continue;
    }

    // ATX 标题
    const head = /^ {0,3}(#{1,6})[ \t]+(.*)$/.exec(line);
    if (head) {
      const h = document.createElement('h' + head[1].length);
      inlineInto(h, head[2].replace(/(^|[ \t])#+[ \t]*$/, ''));
      out.push(h);
      i++;
      continue;
    }

    // 分隔线
    if (isHr(line)) {
      out.push(document.createElement('hr'));
      i++;
      continue;
    }

    // 引用：连续 > 行，空行后仍是引用则继续
    if (isQuote(line)) {
      const buf = [];
      while (i < lines.length) {
        const m = /^ {0,3}>[ \t]?(.*)$/.exec(lines[i]);
        if (m) {
          buf.push(m[1]);
          i++;
          continue;
        }
        if (!lines[i].trim() && isQuote(lines[i + 1] || '')) {
          buf.push('');
          i++;
          continue;
        }
        break;
      }
      const bq = document.createElement('blockquote');
      bq.append(...parseBlocks(buf));
      out.push(bq);
      continue;
    }

    // 列表
    if (isListItem(line)) {
      const list = parseList(lines, i);
      out.push(list.node);
      i = list.next;
      continue;
    }

    // 表格：表头行 + 分隔行
    if (line.includes('|') && isTableSep(lines[i + 1] || '')) {
      const table = parseTable(lines, i);
      out.push(table.node);
      i = table.next;
      continue;
    }

    // 段落：收集到空行或下一个块级标记；保留原有换行（渲染成 <br>）
    const para = [line];
    let j = i + 1;
    while (j < lines.length && lines[j].trim() && !isBlockStart(lines, j)) {
      para.push(lines[j]);
      j++;
    }
    const p = document.createElement('p');
    inlineInto(p, para.join('\n'));
    out.push(p);
    i = j;
  }
  return out;
}

function isHr(line) {
  return /^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/.test(line);
}

function isFence(line) {
  return /^ {0,3}(`{3,}|~{3,})/.test(line);
}

function isQuote(line) {
  return /^ {0,3}>/.test(line);
}

function isListItem(line) {
  return /^ {0,3}([-*+]|\d{1,9}[.)])[ \t]+\S/.test(line);
}

function isBlockStart(lines, i) {
  const line = lines[i];
  if (!line) return false;
  return (
    isFence(line) ||
    isHr(line) ||
    isQuote(line) ||
    isListItem(line) ||
    /^ {0,3}#{1,6}[ \t]+/.test(line) ||
    (line.includes('|') && isTableSep(lines[i + 1] || ''))
  );
}

function parseFence(lines, start) {
  const m = /^ {0,3}(`{3,}|~{3,})[ \t]*([^`~\s]*)[ \t]*$/.exec(lines[start]);
  const marker = m[1][0];
  const size = m[1].length;
  const lang = (m[2] || '').toLowerCase().replace(/[^\w+-]/g, '');
  const close = new RegExp('^ {0,3}' + (marker === '`' ? '`' : '~') + '{' + size + ',}[ \\t]*$');

  const body = [];
  let i = start + 1;
  while (i < lines.length) {
    if (close.test(lines[i])) {
      i++;
      break;
    }
    body.push(lines[i]);
    i++;
  }

  const pre = document.createElement('pre');
  const code = document.createElement('code');
  if (lang) code.className = 'language-' + lang;
  code.textContent = body.join('\n');
  pre.append(code);
  return { node: pre, next: i };
}

function parseList(lines, start) {
  const first = /^( *)([-*+]|\d{1,9}[.)])[ \t]+/.exec(lines[start]);
  const baseIndent = first[1].length;
  const ordered = /\d/.test(first[2]);
  const list = document.createElement(ordered ? 'ol' : 'ul');

  let li = null;
  let linesOfItem = null;

  const flush = () => {
    if (!li) return;
    const head = linesOfItem[0];
    const rest = linesOfItem.slice(1);
    const task = /^\[([ xX])\][ \t]+/.exec(head);
    if (task) {
      const box = document.createElement('input');
      box.type = 'checkbox';
      box.disabled = true;
      box.checked = task[1].toLowerCase() === 'x';
      li.append(box);
      inlineInto(li, head.slice(task[0].length));
    } else {
      inlineInto(li, head);
    }
    if (rest.some((l) => l.trim())) {
      const sub = document.createElement('div');
      sub.className = 'md-sub';
      sub.append(...parseBlocks(rest));
      li.append(sub);
    }
    li = null;
    linesOfItem = null;
  };

  let i = start;
  while (i < lines.length) {
    const line = lines[i];
    const m = /^( *)([-*+]|\d{1,9}[.)])[ \t]+(.*)$/.exec(line);

    // 同级新条目：缩进不超过基准一层，且有序/无序类型一致
    if (m && m[1].length <= baseIndent + 1 && /\d/.test(m[2]) === ordered) {
      flush();
      li = document.createElement('li');
      list.append(li);
      linesOfItem = [m[3]];
      i++;
      continue;
    }

    if (!line.trim()) {
      // 空行：后面仍是本列表的缩进内容才继续（宽松处理松散列表）
      const next = lines[i + 1] || '';
      if (!/^ {2,}\S/.test(next)) break;
      if (linesOfItem) linesOfItem.push('');
      i++;
      continue;
    }

    // 缩进的续行/子列表：去掉最多 4 个空格后交给块级解析递归
    if (/^ {2,}\S/.test(line) && linesOfItem) {
      linesOfItem.push(line.replace(/^ {1,4}/, ''));
      i++;
      continue;
    }

    break;
  }
  flush();
  return { node: list, next: i };
}

function splitRow(line) {
  let s = line.trim();
  if (s.startsWith('|')) s = s.slice(1);
  if (s.endsWith('|')) s = s.slice(0, -1);
  return s.split('|').map((c) => c.trim());
}

function isTableSep(line) {
  if (!line.trim().includes('-') || !line.includes('|')) return false;
  const cells = splitRow(line);
  return cells.length > 0 && cells.every((c) => /^:?-{1,}:?$/.test(c));
}

function parseTable(lines, start) {
  const header = splitRow(lines[start]);
  const align = splitRow(lines[start + 1]).map((c) => {
    if (/^:-+:$/.test(c)) return 'center';
    if (/^-+:$/.test(c)) return 'right';
    if (/^:-+$/.test(c)) return 'left';
    return '';
  });

  const table = document.createElement('table');
  const thead = document.createElement('thead');
  const headRow = document.createElement('tr');
  header.forEach((cell, idx) => {
    const th = document.createElement('th');
    if (align[idx]) th.style.textAlign = align[idx];
    inlineInto(th, cell);
    headRow.append(th);
  });
  thead.append(headRow);
  table.append(thead);

  const tbody = document.createElement('tbody');
  let i = start + 2;
  while (i < lines.length && lines[i].trim() && lines[i].includes('|')) {
    const cells = splitRow(lines[i]);
    const tr = document.createElement('tr');
    for (let c = 0; c < header.length; c++) {
      const td = document.createElement('td');
      if (align[c]) td.style.textAlign = align[c];
      inlineInto(td, cells[c] || '');
      tr.append(td);
    }
    tbody.append(tr);
    i++;
  }
  if (tbody.children.length) table.append(tbody);

  const wrap = document.createElement('div');
  wrap.className = 'md-table';
  wrap.append(table);
  return { node: wrap, next: i };
}

// ---------- 行内语法 ----------

function inlineInto(el, text) {
  el.append(...parseInline(text));
}

function parseInline(text) {
  const out = [];
  let buf = '';

  const flush = () => {
    if (!buf) return;
    out.push(document.createTextNode(buf));
    buf = '';
  };

  let i = 0;
  while (i < text.length) {
    const ch = text[i];

    // 反斜杠转义
    if (ch === '\\' && i + 1 < text.length && '\\`*_{}[]()#+-.!~>|'.includes(text[i + 1])) {
      buf += text[i + 1];
      i += 2;
      continue;
    }

    // 行内代码（反引号数量需成对）
    if (ch === '`') {
      let n = 0;
      while (text[i + n] === '`') n++;
      const ticks = '`'.repeat(n);
      const end = text.indexOf(ticks, i + n);
      if (end > i + n) {
        flush();
        let body = text.slice(i + n, end);
        if (/^ .* $/.test(body)) body = body.slice(1, -1);
        const code = document.createElement('code');
        code.textContent = body;
        out.push(code);
        i = end + n;
        continue;
      }
      buf += ticks;
      i += n;
      continue;
    }

    // 图片 ![alt](src)
    if (ch === '!' && text[i + 1] === '[') {
      const link = matchLink(text, i + 1);
      if (link) {
        flush();
        out.push(imageNode(link.text, link.href));
        i = link.end;
        continue;
      }
    }

    // 链接 [label](href)
    if (ch === '[') {
      const link = matchLink(text, i);
      if (link) {
        flush();
        out.push(linkNode(link.text, link.href));
        i = link.end;
        continue;
      }
    }

    // 自动链接 <https://…> / <mailto:…>
    if (ch === '<') {
      const m = /^<((?:https?:\/\/|mailto:)[^\s<>]+)>/i.exec(text.slice(i));
      if (m) {
        flush();
        out.push(plainLink(m[1]));
        i += m[0].length;
        continue;
      }
    }

    // 删除线 ~~
    if (ch === '~' && text[i + 1] === '~' && !/\s/.test(text[i + 2] || ' ')) {
      const end = findClosing(text, i + 2, '~~', false);
      if (end > i + 2) {
        flush();
        const del = document.createElement('del');
        inlineInto(del, text.slice(i + 2, end));
        out.push(del);
        i = end + 2;
        continue;
      }
    }

    // 粗体 ** / 斜体 * / 粗斜体 ***
    if (ch === '*' || ch === '_') {
      const triple = text[i + 1] === ch && text[i + 2] === ch;
      const dbl = !triple && text[i + 1] === ch;
      const marker = triple ? ch + ch + ch : dbl ? ch + ch : ch;
      // 开标记后不能是空白（否则 `2 * 3 * 4` 会被当成强调），
      // 下划线也不做词内强调（避免 snake_case 被吃掉）。
      const canOpen = !/\s/.test(text[i + marker.length] || ' ');
      const word = ch === '_' && /[\p{L}\p{N}]/u.test(text[i - 1] || ' ');
      const end = canOpen && !word ? findClosing(text, i + marker.length, marker, ch === '_') : -1;
      if (end > i + marker.length) {
        flush();
        const el = document.createElement(triple || dbl ? 'strong' : 'em');
        if (triple) {
          const em = document.createElement('em');
          inlineInto(em, text.slice(i + marker.length, end));
          el.append(em);
        } else {
          inlineInto(el, text.slice(i + marker.length, end));
        }
        out.push(el);
        i = end + marker.length;
        continue;
      }
      buf += marker;
      i += marker.length;
      continue;
    }

    // 裸链接
    if ((ch === 'h' || ch === 'H') && /^https?:\/\//i.test(text.slice(i, i + 8))) {
      const m = /^https?:\/\/[^\s<>"'()（）[\]【】]+/i.exec(text.slice(i));
      if (m) {
        // 去掉句尾标点，但保留 URL 中间的 ?query 与 #anchor
        const url = m[0].replace(/[.,;:!?。，；：！？]+$/, '');
        flush();
        out.push(plainLink(url));
        i += url.length;
        continue;
      }
    }

    // 换行：聊天场景保留用户的分行
    if (ch === '\n') {
      flush();
      out.push(document.createElement('br'));
      i++;
      continue;
    }

    buf += ch;
    i++;
  }
  flush();
  return out;
}

// findClosing 找强调结束标记：前面必须紧跟非空白字符，下划线还要求后面不是字母数字。
function findClosing(text, from, marker, wordBoundary) {
  if (from >= text.length) return -1;
  let idx = text.indexOf(marker, from);
  while (idx !== -1) {
    const before = text[idx - 1];
    const after = text[idx + marker.length];
    const beforeOK = before !== undefined && before !== ' ' && before !== '\n' && before !== '\t';
    const afterOK = !wordBoundary || after === undefined || !/[\p{L}\p{N}]/u.test(after);
    if (beforeOK && afterOK) return idx;
    idx = text.indexOf(marker, idx + marker.length);
  }
  return -1;
}

// matchLink 解析 [label](href "title")，返回 label / href / 结束位置。
function matchLink(text, open) {
  if (text[open] !== '[') return null;

  let depth = 0;
  let close = -1;
  for (let i = open; i < text.length; i++) {
    const c = text[i];
    if (c === '\\') {
      i++;
      continue;
    }
    if (c === '[') depth++;
    else if (c === ']') {
      depth--;
      if (depth === 0) {
        close = i;
        break;
      }
    }
  }
  if (close < 0 || text[close + 1] !== '(') return null;

  let depthP = 0;
  let endP = -1;
  for (let j = close + 1; j < text.length; j++) {
    const c = text[j];
    if (c === '\\') {
      j++;
      continue;
    }
    if (c === '(') depthP++;
    else if (c === ')') {
      depthP--;
      if (depthP === 0) {
        endP = j;
        break;
      }
    }
  }
  if (endP < 0) return null;

  const inner = text.slice(close + 2, endP).trim();
  // href 与可选 title 之间用空白分隔；href 可写成 <...>
  const m = /^(<[^>\n]*>|\S+?)(?:[ \t]+(?:"([^"]*)"|'([^']*)'|\(([^)]*)\)))?$/.exec(inner);
  const href = (m && m[1] ? m[1] : inner).replace(/^<|>$/g, '');
  return { text: text.slice(open + 1, close), href, end: endP + 1 };
}

// safeUrl 只放行 http(s) 与 mailto；相对路径按当前页面解析。
function safeUrl(raw) {
  const v = (raw || '').trim();
  if (!v) return '';
  try {
    const u = new URL(v, document.baseURI);
    if (u.protocol === 'http:' || u.protocol === 'https:' || u.protocol === 'mailto:') return u.href;
  } catch {
    /* 非法 URL */
  }
  return '';
}

function anchor(href) {
  const a = document.createElement('a');
  a.href = href;
  a.target = '_blank';
  a.rel = 'noopener noreferrer nofollow';
  return a;
}

function linkNode(label, href) {
  const safe = safeUrl(href);
  if (!safe) {
    const span = document.createElement('span');
    inlineInto(span, label);
    return span;
  }
  const a = anchor(safe);
  inlineInto(a, label);
  return a;
}

function plainLink(href) {
  const safe = safeUrl(href);
  if (!safe) return document.createTextNode(href);
  const a = anchor(safe);
  a.textContent = href;
  return a;
}

// imageNode：只渲染同源图片（外链图会泄露访问者 IP），其余退化成链接。
function imageNode(alt, href) {
  const safe = safeUrl(href);
  let sameOrigin = false;
  try {
    sameOrigin = new URL(safe).origin === window.location.origin;
  } catch {
    /* 解析失败按外链处理 */
  }
  if (!safe || !sameOrigin) return linkNode(alt || href, href);

  const img = document.createElement('img');
  img.src = safe;
  img.alt = alt || '';
  img.loading = 'lazy';
  img.decoding = 'async';

  const a = anchor(safe);
  a.className = 'md-img-link';
  a.append(img, iconEl('expand'));
  return a;
}
