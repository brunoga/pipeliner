// ── Starlark syntax highlighting ──────────────────────────────────────────────
// Line-by-line tokeniser for Starlark (.star) config files.
// Handles: comments, string literals (single + triple-quoted), keywords,
// built-in helpers (plugin/task/env), True/False/None, numbers, and def names.
// Multi-line triple-quoted strings are tracked with a state flag so subsequent
// lines inside them are rendered as string spans.

const STARLARK_KEYWORDS = new Set(
  ['def','if','elif','else','for','in','return','not','and','or',
   'load','pass','break','continue','lambda','import']);
const STARLARK_BUILTINS  = new Set(['plugin','task','env']);
const STARLARK_BOOLEANS  = new Set(['True','False','None']);

// Persistent state across lines for multi-line triple-quoted strings.
let _inTriple = false;
let _tripleQ  = '';

function highlightStarlark(text) {
  _inTriple = false; _tripleQ = '';
  return text.split('\n').map(hlStarlarkLine).join('\n') + '\n';
}

function hlStarlarkLine(line) {
  const e = s => s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
  let out = '';
  let i   = 0;

  // If we're inside a triple-quoted string from a previous line, scan for end.
  if (_inTriple) {
    const end = line.indexOf(_tripleQ);
    if (end === -1) {
      return `<span class="y-str">${e(line)}</span>`;
    }
    _inTriple = false;
    out += `<span class="y-str">${e(line.slice(0, end + 3))}</span>`;
    i = end + 3;
  }

  while (i < line.length) {
    // Comment
    if (line[i] === '#') {
      out += `<span class="y-comment">${e(line.slice(i))}</span>`;
      break;
    }
    // Triple-quoted string
    const tq = line.slice(i, i+3);
    if (tq === '"""' || tq === "'''") {
      const close = line.indexOf(tq, i+3);
      if (close !== -1) {
        out += `<span class="y-str">${e(line.slice(i, close+3))}</span>`;
        i = close + 3;
      } else {
        _inTriple = true; _tripleQ = tq;
        out += `<span class="y-str">${e(line.slice(i))}</span>`;
        break;
      }
      continue;
    }
    // Single-quoted string (simple: no escape handling needed for highlighting)
    if (line[i] === '"' || line[i] === "'") {
      const q = line[i];
      let j = i + 1;
      while (j < line.length && line[j] !== q) {
        if (line[j] === '\\') j++; // skip escaped char
        j++;
      }
      out += `<span class="y-str">${e(line.slice(i, j+1))}</span>`;
      i = j + 1;
      continue;
    }
    // Identifier or keyword
    const idM = line.slice(i).match(/^[A-Za-z_]\w*/);
    if (idM) {
      const word = idM[0];
      const rest = line.slice(i + word.length);
      // def <funcname>
      if (word === 'def') {
        const nm = rest.match(/^\s+([A-Za-z_]\w*)/);
        if (nm) {
          out += `<span class="y-key">def</span>`;
          out += e(rest.slice(0, rest.indexOf(nm[1])));
          out += `<span class="y-func">${e(nm[1])}</span>`;
          i += word.length + rest.indexOf(nm[1]) + nm[1].length;
          continue;
        }
      }
      if (STARLARK_KEYWORDS.has(word)) {
        out += `<span class="y-key">${e(word)}</span>`;
      } else if (STARLARK_BUILTINS.has(word)) {
        out += `<span class="y-builtin">${e(word)}</span>`;
      } else if (STARLARK_BOOLEANS.has(word)) {
        out += `<span class="y-bool">${e(word)}</span>`;
      } else {
        out += e(word);
      }
      i += word.length;
      continue;
    }
    // Number
    const numM = line.slice(i).match(/^-?\d+(\.\d+)?/);
    if (numM) {
      out += `<span class="y-num">${e(numM[0])}</span>`;
      i += numM[0].length;
      continue;
    }
    // Everything else: emit verbatim
    out += e(line[i++]);
  }
  return out;
}

function syncHighlight() {
  const ta = document.getElementById('config-editor');
  document.getElementById('editor-hl').innerHTML = highlightStarlark(ta.value);
  syncScroll(); // re-sync height and scroll position after content changes
}

function syncScroll() {
  const ta = document.getElementById('config-editor');
  const hl = document.getElementById('editor-hl');
  hl.style.height = ta.clientHeight + 'px';
  hl.style.width  = ta.clientWidth  + 'px';  // match exactly, incl. scrollbar gutter
  hl.scrollTop  = ta.scrollTop;
  hl.scrollLeft = ta.scrollLeft;
}

function editorTab(e) {
  if (e.key !== 'Tab') return;
  e.preventDefault();
  const ta = document.getElementById('config-editor');
  const s = ta.selectionStart, end = ta.selectionEnd;
  ta.value = ta.value.slice(0, s) + '  ' + ta.value.slice(end);
  ta.selectionStart = ta.selectionEnd = s + 2;
  syncHighlight();
}

