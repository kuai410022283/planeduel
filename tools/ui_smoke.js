// 整个 app.js 的渲染冒烟测试：用极简 DOM 桩把四条渲染路径都跑一遍，
// 专门用来抓「变量未定义 / 拼写错误」这类线上才会炸的问题（例如 practice 模式白屏）。
const fs = require('fs');
const appPath = process.argv[2];

class El {
  constructor(tag = 'div') {
    this.tagName = tag;
    this.children = [];
    this.dataset = {};
    this._cls = new Set();
    this.style = {};
    this.hidden = false;
    this.disabled = false;
    this.value = '';
    this.checked = false;
    this.textContent = '';
    this.innerHTML = '';
    this._listeners = {};
  }
  set innerHTML(v) { this._html = v; if (!v) this.children = []; }
  get innerHTML() { return this._html || ''; }
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
  removeEventListener() {}
  querySelector(sel) {
    const m = /data-cell="([^"]+)"/.exec(sel);
    if (m) return this.children.find((c) => c.dataset && c.dataset.cell === m[1]) || null;
    return null;
  }
  querySelectorAll() { return []; }
  setPointerCapture() {}
}

const els = new Map();
const doc = {
  getElementById: (id) => { if (!els.has(id)) els.set(id, new El()); return els.get(id); },
  createElement: (tag) => new El(tag),
  querySelector: () => null,
  querySelectorAll: () => [],
  elementFromPoint: () => null,
  addEventListener: () => {},
};

const sandbox = {
  document: doc,
  location: { href: 'http://127.0.0.1:28082/' },
  sessionStorage: { _d: {}, getItem(k) { return this._d[k] || null; }, setItem(k, v) { this._d[k] = v; }, removeItem(k) { delete this._d[k]; } },
  localStorage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
  fetch: () => Promise.reject(new Error('offline (stub)')),
  WebSocket: class { constructor() { this.readyState = 3; } send() {} close() {} addEventListener() {} },
  setInterval: () => 1,
  setTimeout: () => 1,
  clearInterval: () => {},
  clearTimeout: () => {},
  console,
  URL,
  Date,
  Math,
  JSON,
  Promise,
  Error,
  Set,
  Map,
  Number,
  String,
  Array,
  Object,
  Intl,
};

const src = fs.readFileSync(appPath, 'utf8');
const ctx = require('vm').createContext(sandbox);
try {
  require('vm').runInContext(src, ctx, { filename: 'app.js' });
} catch (e) {
  console.log('  ✗ 加载 app.js 失败: ' + e.message);
  process.exit(1);
}

const vm = require('vm');
// const/let 不会挂到 sandbox 上，统一在上下文里求值
function setState(patch) {
  vm.runInContext('Object.assign(state, ' + JSON.stringify(patch) + ');', ctx);
}
function callRender() {
  vm.runInContext('render();', ctx);
}

let fails = 0;
function run(name, fn) {
  try {
    fn();
    console.log('  ✓ ' + name);
  } catch (e) {
    console.log('  ✗ ' + name + ' → ' + e.message);
    fails++;
  }
}

// 1) 空闲态（没有对局）
run('空闲态渲染', () => {
  setState({ room: null, solo: null, spectate: null });
  callRender();
});

// 2) 练习/陪练：这次线上报错的路径
run('练习局渲染（含按机分组）', () => {
  setState({ room: null, spectate: null, solo: {
    id: 's1', me: 0, finished: false, winner: -1, alive: 3, enemy_alive: 3,
    shots: 0, hits: 0, bot_shots: 0, bot_hits: 0, last: [],
    my_planes: ['A3', 'B1', 'C3'],
    my_plane_groups: [
      { cells: ['A3', 'B1', 'B2', 'B3'], head: 'A3', alive: true },
      { cells: ['D1', 'D2'], head: 'D1', alive: true },
    ],
    my_marks: [], enemy_marks: [],
  } });
  callRender();
});

// 2b) 老服务端（没有 my_plane_groups）也不能炸
run('练习局渲染（无分组字段的兼容）', () => {
  setState({ solo: {
    id: 's2', me: 0, finished: true, winner: 0, alive: 1, enemy_alive: 0,
    shots: 9, hits: 4, last: [{ cell: 'A3', fb: '中', by: 0 }],
    my_planes: ['A3', 'B1'], my_marks: [], enemy_marks: [],
  } });
  callRender();
});

// 3) 对局：playing
run('对局渲染（playing）', () => {
  setState({ solo: null, room: {
    id: 'r1', code: 'ABC123', state: 'playing', me: 0, names: ['甲', '乙'],
    turn: 0, finished: false, winner: -1, alive: [3, 3],
    my_cells: ['A3', 'B1'], my_planes: [{ cells: ['A3', 'B1'], head: 'A3', alive: true }],
    my_marks: [], enemy_marks: [{ cell: 'C5', fb: '中', by: 0 }],
    last_shot: { cell: 'C5', fb: '中', by: 0 }, deadline_ms: Date.now() + 60000, turn_sec: 300,
  } });
  callRender();
});

// 4) 对局：布阵阶段
run('对局渲染（placing）', () => {
  setState({ room: {
    id: 'r2', code: 'XYZ789', state: 'placing', me: 1, names: ['甲', '乙'],
    my_ready: false, opp_ready: true, placed: [], my_cells: [], my_planes: [],
    my_marks: [], enemy_marks: [], place_deadline_ms: Date.now() + 90000, turn_sec: 300,
  } });
  callRender();
});

// 5) 观战
run('观战渲染', () => {
  setState({ room: null, spectate: {
    code: 'ABC123', names: ['甲', '乙'], shots: [{ cell: 'A1', fb: '空', by: 0 }], finished: false,
  } });
  callRender();
});

// 6) 对局结束
run('对局渲染（done）', () => {
  setState({ spectate: null, room: {
    id: 'r3', code: 'ABC123', state: 'done', me: 0, names: ['甲', '乙'], finished: true,
    winner: 0, reason: 'win', alive: [1, 0], my_cells: [], my_planes: [], my_marks: [], enemy_marks: [],
  } });
  callRender();
});

console.log(fails === 0 ? '\n全部渲染路径通过' : '\n' + fails + ' 条路径失败');
process.exit(fails === 0 ? 0 : 1);
