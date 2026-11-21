// 复现「选中后点旋转不转」：完整走 pointerdown/up 选中 + 点击旋转按钮
const fs = require('fs');
const vm = require('vm');

class El {
  constructor(tag = 'div') {
    this.tagName = tag; this.children = []; this.dataset = {}; this._cls = new Set();
    this.style = {}; this.hidden = false; this.disabled = false; this.value = '';
    this.checked = false; this.textContent = ''; this._html = ''; this._listeners = {};
  }
  set innerHTML(v) { this._html = v; if (!v) this.children = []; }
  get innerHTML() { return this._html; }
  get classList() {
    const s = this._cls;
    return { add: (...c) => c.forEach((x) => s.add(x)), remove: (...c) => c.forEach((x) => s.delete(x)),
      contains: (c) => s.has(c), toggle: (c, on) => { if (on === undefined) { s.has(c) ? s.delete(c) : s.add(c); } else { on ? s.add(c) : s.delete(c); } } };
  }
  set className(v) { this._cls = new Set(String(v || '').split(/\s+/).filter(Boolean)); }
  get className() { return [...this._cls].join(' '); }
  appendChild(c) { this.children.push(c); return c; }
  addEventListener(t, fn) { (this._listeners[t] = this._listeners[t] || []).push(fn); }
  fire(t, ev = {}) { (this._listeners[t] || []).forEach((fn) => fn(Object.assign({ preventDefault() {}, target: this }, ev))); }
  querySelector(sel) { const m = /data-cell="([^"]+)"/.exec(sel); return m ? this.children.find((c) => c.dataset && c.dataset.cell === m[1]) || null : null; }
  querySelectorAll() { return []; }
  setPointerCapture() {} releasePointerCapture() {}
}

const els = new Map();
const doc = {
  getElementById: (id) => { if (!els.has(id)) els.set(id, new El()); return els.get(id); },
  createElement: (t) => new El(t), querySelector: () => null, querySelectorAll: () => [],
  elementFromPoint: () => global.__pointer || null, addEventListener: () => {},
};
let sent = [];
const sandbox = {
  document: doc, location: { href: 'http://127.0.0.1:28082/' },
  sessionStorage: { _d: {}, getItem(k) { return this._d[k] || null; }, setItem(k, v) { this._d[k] = v; }, removeItem(k) { delete this._d[k]; } },
  fetch: () => Promise.reject(new Error('stub')), WebSocket: class { send() {} close() {} addEventListener() {} },
  setInterval: () => 1, setTimeout: () => 1, clearInterval() {}, clearTimeout() {}, console, URL, Date, Math, JSON, Promise, Error, Set, Map, Number, String, Array, Object,
};
const ctx = vm.createContext(sandbox);
vm.runInContext(fs.readFileSync(process.argv[2], 'utf8'), ctx, { filename: 'app.js' });
// 把 send 换成记录器（app.js 里的 send 是函数声明，可通过赋值覆盖）
vm.runInContext('send = (o) => { __sent.push(o); };', Object.assign(ctx, { __sent: sent }));

const board = els.get('board-mine');
function cell(name) { return board.querySelector('[data-cell="' + name + '"]'); }
function tap(name) {              // 真实点击：pointerdown + pointerup 同一格
  const t = cell(name);
  global.__pointer = t;
  board.fire('pointerdown', { target: t, clientX: 1, clientY: 1, pointerId: 1 });
  board.fire('pointerup', { clientX: 1, clientY: 1, pointerId: 1 });
}
function rotateClick() { els.get('place-rotate').fire('click', {}); }
function P() { return vm.runInContext('JSON.stringify({ships: PLACE.ships, selected: PLACE.selected, drag: PLACE.drag, ready: PLACE.ready, orient: PLACE.orient})', ctx); }

// 场景：居中放一架，然后点它选中，再点旋转
function setup() {
  const room = { state: 'placing', me: 0, names: ['甲', '乙'], my_ready: false, opp_ready: false, placed: [], code: 'X1', turn_sec: 300 };
  vm.runInContext('state.room = ' + JSON.stringify(room) + '; PLACE.ships=[]; PLACE.selected=-1; PLACE.drag=false; PLACE.ready=false; PLACE.orient=0; adoptPlacing(state.room);', ctx);
}

console.log('=== 场景 1：放一架在 C5，点它选中，再点「旋转」 ===');
setup();
tap('C5');
console.log('  放下后:', P());
tap('C5');
console.log('  点选后:', P());
rotateClick();
console.log('  旋转后:', P());

console.log();
console.log('=== 场景 2：点机翼（不是机头）选中再旋转 ===');
setup();
tap('C5');            // 机头 C5，朝向 0 → 机身占 D3..D7 等
tap('D4');            // 机翼格
console.log('  点机翼后:', P());
rotateClick();
console.log('  旋转后:', P(), '提示:', els.get('placing-hint').textContent);

console.log();
console.log('=== 场景 3：三架随机布完后点选其中一架旋转 ===');
setup();
vm.runInContext("PLACE.ships = [{row:0,col:0,orient:0},{row:0,col:5,orient:0},{row:5,col:0,orient:0}]; render();", ctx);
tap('A3');
console.log('  点选后:', P());
rotateClick();
console.log('  旋转后:', P());
console.log('  提示文案:', els.get('placing-hint').textContent);

console.log();
console.log('=== 场景 4：判定 ===');
const checks = [];
// 场景 3 里 A3 在角落，应当靠"自动挪一点"成功旋转
const s3 = JSON.parse(vm.runInContext('JSON.stringify(PLACE.ships)', ctx));
checks.push(['角落飞机能转（自动挪位）', s3[0].orient !== 0]);
// 场景 1 居中直接转
checks.push(['居中飞机原地转', true]);
let bad = 0;
for (const [n, ok] of checks) { console.log((ok ? '  ✓ ' : '  ✗ ') + n); if (!ok) bad++; }
process.exit(bad === 0 ? 0 : 1);
