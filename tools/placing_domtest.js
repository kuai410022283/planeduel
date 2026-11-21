// 用极简 DOM 桩跑 app.js 里的布阵交互逻辑（无法在此环境开浏览器）
const fs = require('fs');
const path = process.argv[2];

class El {
  constructor(tag = 'div') {
    this.tagName = tag;
    this.children = [];
    this.dataset = {};
    this._cls = new Set();
    this.style = {};
    this.hidden = false;
    this.disabled = false;
    this.textContent = '';
    this._listeners = {};
  }
  get classList() {
    const s = this._cls;
    return {
      add: (...c) => c.forEach((x) => s.add(x)),
      remove: (...c) => c.forEach((x) => s.delete(x)),
      contains: (c) => s.has(c),
      toggle: (c, on) => { if (on === undefined) { s.has(c) ? s.delete(c) : s.add(c); } else { on ? s.add(c) : s.delete(c); } },
    };
  }
  set className(v) { this._cls = new Set(String(v || '').split(/\s+/).filter(Boolean)); }
  get className() { return [...this._cls].join(' '); }
  appendChild(c) { this.children.push(c); return c; }
  addEventListener(t, fn) { (this._listeners[t] = this._listeners[t] || []).push(fn); }
  fire(t, ev) { (this._listeners[t] || []).forEach((fn) => fn(ev)); }
  querySelector(sel) {
    const m = /data-cell="([^"]+)"/.exec(sel);
    if (m) return this.children.find((c) => c.dataset && c.dataset.cell === m[1]) || null;
    return null;
  }
  setPointerCapture() {}
  releasePointerCapture() {}
}

const els = new Map();
function el(id) {
  if (!els.has(id)) els.set(id, new El());
  return els.get(id);
}

// 棋盘：1 个角标 + 10 个列标 + 10 行 × (1 行标 + 10 格)
const board = el('board-mine');
const ROWS = ['A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J'];
board.appendChild(new El()); // corner
for (let c = 1; c <= 10; c++) board.appendChild(new El());
for (let r = 0; r < 10; r++) {
  board.appendChild(new El());
  for (let c = 0; c < 10; c++) {
    const d = new El();
    d.className = 'cell';
    d.dataset.cell = ROWS[r] + (c + 1);
    board.appendChild(d);
  }
}

let pointerAt = null;
global.document = {
  elementFromPoint: () => pointerAt,
  getElementById: (id) => el(id),
};
let SENT = [];
global.send = (o) => SENT.push(o);
global.$ = (id) => el(id);
global.state = { room: null };
global.render = () => {};
global.paintBoard = () => {};
global.setTitles = () => {};
global.applyDuelStatus = () => {};
global.fmtLeft = (s) => String(s);

// 抽取 app.js 里的布阵模块（从形状常量到「启动」段之前）
const src = fs.readFileSync(path, 'utf8');
const start = src.indexOf('const SHAPE_BASE');
const end = src.indexOf('// ---------------------------------------------------------------- 启动');
if (start < 0 || end < 0) { console.error('模块定位失败'); process.exit(1); }
const mod = src.slice(start, end);

const modCode = mod +
  '\nmodule.exports = { PLACE, shipCells, headCellOf, shipFromHead, placementValid, ' +
  'adoptPlacing, renderPlacing, shapeOutline, setHint, shipIndexAt };\n';
const m = { exports: {} };
new Function('module', 'exports', 'ROWS', 'document', '$', 'send', 'state', 'render',
  'paintBoard', 'setTitles', 'applyDuelStatus', 'fmtLeft', modCode)(
  m, m.exports, ROWS, global.document, global.$, global.send, global.state, global.render,
  global.paintBoard, global.setTitles, global.applyDuelStatus, global.fmtLeft);
const A = m.exports;

// ---------------------------------------------------------------- 场景
let fails = 0;
function check(name, cond, extra) {
  if (cond) { console.log('  ✓ ' + name); }
  else { console.log('  ✗ ' + name + (extra ? '  → ' + extra : '')); fails++; }
}
function click(cellName) {
  const target = board.querySelector('[data-cell="' + cellName + '"]');
  pointerAt = target;
  board.fire('pointerdown', { target, clientX: 1, clientY: 1, pointerId: 1, preventDefault() {} });
  board.fire('pointerup', { clientX: 1, clientY: 1, pointerId: 1, preventDefault() {} });
}
function drag(fromCell, toCell, x, y) {
  const a = board.querySelector('[data-cell="' + fromCell + '"]');
  pointerAt = a;
  board.fire('pointerdown', { target: a, clientX: 1, clientY: 1, pointerId: 1, preventDefault() {} });
  pointerAt = board.querySelector('[data-cell="' + toCell + '"]');
  board.fire('pointermove', { clientX: x, clientY: y, pointerId: 1, preventDefault() {} });
  board.fire('pointerup', { clientX: x, clientY: y, pointerId: 1, preventDefault() {} });
}
function reset() {
  A.PLACE.ships = [];
  A.PLACE.selected = -1;
  A.PLACE.grab = null;
  A.PLACE.drag = false;
  A.PLACE.orient = 0;
  A.PLACE.ready = false;
}

const room = { state: 'placing', me: 0, my_ready: false, opp_ready: false, placed: [], code: 'ABC123', names: ['甲', '乙'], turn_sec: 300 };
state.room = room;
A.adoptPlacing(room);
A.renderPlacing(room);

// ---- 阶段 1：摆放 / 校验
click('C5');
check('机头锚点：点 C5 → 机头在 C5', (() => { const h = A.headCellOf(A.PLACE.ships[0]); return h.row === 2 && h.col === 4; })(),
  JSON.stringify(A.PLACE.ships));
check('已同步给服务端', SENT.some((o) => o.t === 'placing.set' && o.placements.length === 1));

click('A1');
check('边缘格（机翼放不下）被拒绝', A.PLACE.ships.length === 1, JSON.stringify(A.PLACE.ships));

reset();
click('A3'); click('A8'); click('F3');
check('三架放满', A.PLACE.ships.length === 3, JSON.stringify(A.PLACE.ships));
click('B5');
check('重叠位置放不下', A.PLACE.ships.length === 3, JSON.stringify(A.PLACE.ships));

// ---- 阶段 2：拖动
const before = JSON.stringify(A.PLACE.ships);
pointerAt = board.querySelector('[data-cell="F3"]');
board.fire('pointerdown', { target: pointerAt, clientX: 1, clientY: 6, pointerId: 1, preventDefault() {} });
check('抓起时先从棋盘移除', A.PLACE.ships.length === 2, JSON.stringify(A.PLACE.ships));
pointerAt = board.querySelector('[data-cell="J9"]');
board.fire('pointermove', { clientX: 9, clientY: 10, pointerId: 1, preventDefault() {} });
board.fire('pointerup', { clientX: 9, clientY: 10, pointerId: 1, preventDefault() {} });
check('越界拖动回到原位', JSON.stringify(A.PLACE.ships) === before, JSON.stringify(A.PLACE.ships));

// 合法拖动：右下角有空位吗？把 F3 那架往右挪一格（F4）
drag('F3', 'F4', 4, 6);
const moved = A.PLACE.ships.find((s) => s.row === 5 && s.col === 1);
check('合法拖动生效（F3 → F4）', !!moved, JSON.stringify(A.PLACE.ships));

// ---- 阶段 3：旋转
reset();
click('C5'); // 中央位置，四个朝向都放得下
A.PLACE.selected = 0;
const headBefore = A.headCellOf(A.PLACE.ships[0]);
const oBefore = A.PLACE.ships[0].orient;
els.get('place-rotate').fire('click', {});
check('中央飞机旋转后朝向改变', A.PLACE.ships[0].orient !== oBefore);
const headAfter = A.headCellOf(A.PLACE.ships[0]);
check('旋转后机头位置不变', headAfter.row === headBefore.row && headAfter.col === headBefore.col);

// 角落上的飞机原地转不开 → 自动挪一点位置后仍能转过去
reset();
click('A3');
A.PLACE.selected = 0;
const cornerOrient = A.PLACE.ships[0].orient;
els.get('place-rotate').fire('click', {});
check('角落飞机能转（自动挪位）', A.PLACE.ships[0].orient !== cornerOrient, JSON.stringify(A.PLACE.ships));
check('自动挪位后仍在棋盘内', A.shipCells(A.PLACE.ships[0]).every((c) => c.row >= 0 && c.row < 10 && c.col >= 0 && c.col < 10));

// ---- 阶段 4：删除 / 锁定 / 清除
reset();
click('A3'); click('A8'); click('F3');
check('三架就位（待删除）', A.PLACE.ships.length === 3, JSON.stringify(A.PLACE.ships));
board.fire('dblclick', { target: board.querySelector('[data-cell="A3"]'), preventDefault() {} });
check('双击删除一架', A.PLACE.ships.length === 2, JSON.stringify(A.PLACE.ships));

A.PLACE.ready = true;
const locked = JSON.stringify(A.PLACE.ships);
click('C5');
els.get('place-rotate').fire('click', {});
els.get('place-clear').fire('click', {});
els.get('place-random').fire('click', {});
check('锁定后布阵不变（不可再挪动修改）', JSON.stringify(A.PLACE.ships) === locked, JSON.stringify(A.PLACE.ships));

A.PLACE.ready = false;
els.get('place-clear').fire('click', {});
check('取消锁定后清除生效', A.PLACE.ships.length === 0);
const last = SENT[SENT.length - 1];
check('清除同步给服务端', last.t === 'placing.set' && last.placements.length === 0, JSON.stringify(last));

console.log(fails === 0 ? '\n全部通过' : '\n' + fails + ' 项失败');
process.exit(fails === 0 ? 0 : 1);