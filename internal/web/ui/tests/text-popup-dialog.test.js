/**
 * openTextPopup accessibility: the popup must present as a modal dialog
 * (role="dialog", aria-modal="true") labelled by its title, so screen
 * readers announce it instead of reading a bare div soup.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'visual-editor.js'), 'utf8');

// Minimal element stub: records attributes, hands out child stubs by selector.
function mkEl() {
  const el = {
    attrs: {}, style: {}, children: {},
    setAttribute(k, v) { this.attrs[k] = v; },
    getAttribute(k) { return this.attrs[k]; },
    appendChild() {}, remove() {},
    classList: { add() {}, remove() {}, toggle() {} },
    addEventListener() {},
    focus() {}, select() {}, setSelectionRange() {},
    querySelector(sel) {
      if (!this.children[sel]) this.children[sel] = mkEl();
      return this.children[sel];
    },
    querySelectorAll() { return []; },
  };
  return el;
}

let openTextPopup, created;

beforeAll(() => {
  created = [];
  const doc = {
    getElementById: () => null, // popup does not exist yet → created fresh
    createElement: () => { const el = mkEl(); created.push(el); return el; },
    body: { appendChild() {} },
    querySelector: () => null,
    querySelectorAll: () => [],
    addEventListener() {}, removeEventListener() {},
  };
  const helperStubs = `
    function esc(s) { return String(s ?? ''); }
    function syncHighlight() {}
    const CSS = { escape: s => String(s) };
  `;
  const mod = new Function('exports', 'document', 'fetch', 'confirm',
    helperStubs + src + '\nexports.openTextPopup = openTextPopup;');
  const exports = {};
  mod(exports, doc, () => Promise.reject(new Error('no fetch in tests')), () => true);
  ({ openTextPopup } = exports);
});

describe('openTextPopup dialog semantics', () => {
  it('creates the popup with role=dialog, aria-modal, and aria-label from the title', () => {
    openTextPopup('Edit notify body', 'placeholder…', 'hello', () => {});
    const modal = created.find(el => el.attrs.role === 'dialog');
    expect(modal, 'a created element carries role=dialog').toBeTruthy();
    expect(modal.attrs['aria-modal']).toBe('true');
    expect(modal.attrs['aria-label']).toBe('Edit notify body');
  });
});
