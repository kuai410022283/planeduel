// 纸上打飞机 · 前端（含真人邀请对战）
// 视角约定：左 = 布阵图（自己的飞机），右 = 敌情图（空白，只记我打出来的结果）

const API = new URL('./api/', location.href.replace(/[^/]*$/, ''));
const ROWS = 'ABCDEFGHIJ';

const state = {
  nick: '',
  token: '',
  channel: '',
  room: null,   // 真人房（优先显示）
  solo: null,   // 练习局
  players: [],
  invite: null,
  spectate: null,
  noReconnect: false,
};

const $ = (id) => document.getElementById(id);

// 一个标签页一个身份：token 存 sessionStorage，同一台电脑开两个窗口也能各用各的昵称
let SID = sessionStorage.getItem('pd_sid') || '';
function setSID(t) {
  SID = t || '';
  if (SID) sessionStorage.setItem('pd_sid', SID);
  else sessionStorage.removeItem('pd_sid');
}

async function call(path, opts = {}) {
  const res = await fetch(new URL(path, API), {
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      ...(SID ? { 'X-Session': SID } : {}),
      ...(opts.headers || {}),
    },
    ...opts,
  });
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = { error: text }; }
  if (!res.ok) {
    const err = new Error((data && data.error) || `请求失败（${res.status}）`);
    err.status = res.status;
    throw err;
  }
  return data;
}

function setMsg(node, text, kind) {
  node.textContent = text || '';
  node.className = 'msg' + (kind ? ' ' + kind : '');
}

// ---------------------------------------------------------------- WebSocket
let ws = null;
let heartbeat = null;

function wsURL() {
  const base = API.href.replace(/api\/$/, '');
  return base.replace(/^http/, 'ws') + 'ws' + (SID ? '?sid=' + encodeURIComponent(SID) : '');
}

function setTurnbar(main, sub, cls, timer) {
  $('turnbar-main').textContent = main;
  $('turnbar-sub').textContent = sub || '';
  const bar = document.getElementById('turnbar');
  if (bar) bar.className = 'turnbar' + (cls ? ' ' + cls : '');
  const t = $('duel-timer');
  if (t && timer !== undefined) { t.textContent = timer || ''; }
}

function setConn(text, on) {
  const el = $('conn');
  if (!el) return;
  el.textContent = text;
  el.className = 'chip' + (on ? ' on' : ' warn');
}

function send(obj) {
  if (ws && ws.readyState === 1) {
    ws.send(JSON.stringify(obj));
    return true;
  }
  $('log').textContent = '连接已断开，正在重连…（稍等一秒再点）';
  return false;
}

// ---------------------------------------------------------------- 连接与重连
let reconnectTimer = null;
let reconnectTick = null;
let reconnectAttempt = 0;
const RECONNECT_WAIT = 10; // 秒

function stopReconnect() {
  if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
  if (reconnectTick) { clearInterval(reconnectTick); reconnectTick = null; }
}

// scheduleReconnect 显示真实的倒计时，并在倒计时结束时真的去重连
function scheduleReconnect() {
  if (state.noReconnect) {
    setConn('这个身份已在别的窗口', false);
    const b = $('reconnect');
    if (b) b.hidden = true;
    return;
  }
  reconnectAttempt += 1;
  const btn = $('reconnect');
  if (btn) btn.hidden = false;

  let left = RECONNECT_WAIT;
  const label = () => `连接中断，${left}s 后重连（第 ${reconnectAttempt} 次）`;
  stopReconnect();
  setConn(label(), false);

  reconnectTick = setInterval(() => {
    left -= 1;
    if (left > 0) {
      setConn(label(), false);
    } else {
      clearInterval(reconnectTick);
      reconnectTick = null;
      setConn(`正在重连…（第 ${reconnectAttempt} 次）`, false);
    }
  }, 1000);

  reconnectTimer = setTimeout(() => {
    stopReconnect();
    connectWS();
  }, RECONNECT_WAIT * 1000);
}

function connectWS() {
  if (!state.nick && !SID) {
    setConn('未登录，无法连接', false);
    return;
  }
  stopReconnect();
  setConn('正在连接…', false);
  try {
    ws = new WebSocket(wsURL());
  } catch (e) {
    scheduleReconnect();
    return;
  }
  ws.onopen = () => {
    reconnectAttempt = 0;
    stopReconnect();
    const btn = $('reconnect');
    if (btn) btn.hidden = true;
    setConn('已连接', true);
    send({ t: 'players' });
  };
  ws.onmessage = (e) => {
    let msg;
    try { msg = JSON.parse(e.data); } catch { return; }
    handleWS(msg);
  };
  ws.onclose = () => {
    clearInterval(heartbeat);
    scheduleReconnect();
  };
  clearInterval(heartbeat);
  heartbeat = setInterval(() => send({ t: 'ping' }), 25000);
}

function handleWS(msg) {
  switch (msg.t) {
    case 'players':
      state.players = msg.list || [];
      renderPlayers();
      break;
    case 'invite':
      state.invite = msg;
      $('invite-from').textContent = `${msg.from} 想和你打一局`;
      $('invite-modal').hidden = false;
      break;
    case 'invite.sent':
      $('log').textContent = `已向 ${msg.to} 发出邀请…`;
      break;
    case 'invite.declined':
      $('log').textContent = `${msg.by} 拒绝了邀请`;
      break;
    case 'invite.expired':
      $('log').textContent = `邀请 ${msg.to} 已超时`;
      break;
    case 'room':
      state.room = msg.room;
      state.solo = null;
      $('invite-modal').hidden = true;
      render();
      break;
    case 'spectate':
      state.spectate = msg.spectate;
      state.room = null;
      $('invite-modal').hidden = true;
      render();
      break;
    case 'room.left':
      state.room = null;
      state.spectate = null;
      render();
      break;
    case 'timeout':
      $('log').textContent = msg.winner === state.room?.me ? '对手超时，你赢了' : '你超时了，判负';
      break;
    case 'dup':
      // 同一身份开在两个窗口：停止重连并提示改名
      state.noReconnect = true;
      setConn('这个身份已在别的窗口', false);
      $('log').textContent = msg.msg;
      break;
    case 'info':
      $('log').className = 'log';
      $('log').textContent = msg.msg;
      break;
    case 'error':
      $('log').className = 'log err';
      $('log').textContent = msg.msg;
      break;
    default:
      break;
  }
}

// ---------------------------------------------------------------- 棋盘
function buildBoard(node, clickable) {
  node.innerHTML = '';
  const corner = document.createElement('div');
  corner.className = 'lbl';
  node.appendChild(corner);

  for (let c = 1; c <= 10; c++) {
    const d = document.createElement('div');
    d.className = 'lbl';
    d.textContent = c;
    node.appendChild(d);
  }

  for (let r = 0; r < 10; r++) {
    const lab = document.createElement('div');
    lab.className = 'lbl';
    lab.textContent = ROWS[r];
    node.appendChild(lab);

    for (let c = 0; c < 10; c++) {
      const d = document.createElement('div');
      d.className = 'cell';
      const name = ROWS[r] + (c + 1);
      d.dataset.cell = name;
      if (clickable) d.addEventListener('click', () => fire(name));
      node.appendChild(d);
    }
  }
}

function paintBoard(node, ownCells, marks, live) {
  node.classList.toggle('live', !!live);
  for (const cell of node.children) {
    if (!cell.classList.contains('cell')) continue;
    const name = cell.dataset.cell;
    cell.className = 'cell';
    if (ownCells && ownCells.has(name)) cell.classList.add('own');
    const fb = marks.get(name);
    if (fb === '空') cell.classList.add('miss', 'shot');
    else if (fb === '中') cell.classList.add('hit', 'shot');
    else if (fb === '死') cell.classList.add('kill', 'shot');
  }
}

// ---------------------------------------------------------------- 渲染
function render() {
  const r = state.room;
  const sp = state.spectate;
  $('duel-box').hidden = !r;
  $('spec-box').hidden = !sp;
  $('idle-box').hidden = !!r || !!sp;

  if (r) renderDuel(r);
  else if (sp) renderSpectate(sp);
  else renderSolo();

  renderPlayers();
}

function setTitles(mine, enemy) {
  const a = $('title-mine');
  const b = $('title-enemy');
  if (!a || !b) return;
  a.innerHTML = mine + '<span>左</span>';
  b.innerHTML = enemy + '<span>右</span>';
}

function resetTitles() {
  setTitles('布 阵 图', '敌 情 图');
}

// 观战：只能看到已经打出来的格子；房主结束后才亮出双方布阵
function renderSpectate(s) {
  const shots = s.shots || [];
  const marksP0 = new Map(shots.filter((m) => m.by === 1).map((m) => [m.cell, m.fb]));
  const marksP1 = new Map(shots.filter((m) => m.by === 0).map((m) => [m.cell, m.fb]));
  const rev0 = new Set(s.finished ? (s.reveal[0] || []) : []);
  const rev1 = new Set(s.finished ? (s.reveal[1] || []) : []);

  paintBoard($('board-mine'), rev0, marksP0, false);
  paintBoard($('board-enemy'), rev1, marksP1, false);

  setTitles(`${s.names[0] || '甲'} 的棋盘`, `${s.names[1] || '乙'} 的棋盘`);

  const st = $('spec-status');
  if (s.finished) {
    const why = s.winner === 0 ? s.names[0] : s.names[1];
    st.textContent = `🏆 ${why || '胜者'} 获胜（已亮牌）`;
    st.className = 'status done';
    setTurnbar('观战 · 已结束', `${s.names[0]} vs ${s.names[1]}`, 'done', '');
  } else {
    const turnName = s.turn === 0 ? s.names[0] : s.names[1];
    st.textContent = `观战中：${turnName || ''} 出手`;
    st.className = 'status wait';
    setTurnbar('观战模式', `${s.names[0]} vs ${s.names[1]}　${s.viewers} 人旁观`, 'theirs', '');
  }

  $('stats').hidden = false;
  $('st-alive').textContent = s.alive ? s.alive[0] : '-';
  $('st-enemy').textContent = s.alive ? s.alive[1] : '-';
  $('st-shots').textContent = (s.shots || []).length;
  $('st-hits').textContent = (s.shots || []).filter((m) => m.fb !== '空').length;
  $('log').textContent = '你正在观战：看不到未被打中的布阵，结束后自动亮牌。';
}

function renderDuel(r) {
  const mine = new Set(r.my_cells || []);
  const myMarks = new Map((r.my_marks || []).map((m) => [m.cell, m.fb]));
  const enemyMarks = new Map((r.enemy_marks || []).map((m) => [m.cell, m.fb]));
  const live = r.state === 'playing' && r.turn === r.me;

  paintBoard($('board-mine'), mine, myMarks, false);
  paintBoard($('board-enemy'), null, enemyMarks, live);

  setTitles(`${r.names[r.me] || '我'} 的布阵`, `${r.names[1 - r.me] || '对手'} 的敌情`);
  applyDuelStatus(r);

  $('duel-code').textContent = r.code || '—';
  if (r.turn_sec) {
    $('duel-timer').dataset.limit = String(r.turn_sec);
  }

  const stats = $('stats');
  stats.hidden = false;
  $('st-alive').textContent = r.alive ? r.alive[r.me] : '-';
  $('st-enemy').textContent = r.alive ? r.alive[1 - r.me] : '-';
  $('st-shots').textContent = (r.enemy_marks || []).length;
  $('st-hits').textContent = (r.enemy_marks || []).filter((m) => m.fb !== '空').length;

  $('log').textContent = r.last_shot
    ? `${r.names[r.last_shot.by] || ''} 打 ${r.last_shot.cell} → ${r.last_shot.fb}`
    : '';
}

// 剩余秒数格式：不足一分钟显示 45s，超过显示 4:32
function fmtLeft(sec) {
  if (sec === null || sec === undefined) return '';
  if (sec >= 60) return `${Math.floor(sec / 60)}:${String(sec % 60).padStart(2, '0')}`;
  return `${sec}s`;
}

function leftSeconds(r) {
  if (!r || !r.deadline_ms || r.state !== 'playing') return null;
  return Math.max(0, Math.round((r.deadline_ms - Date.now()) / 1000));
}

// applyDuelStatus 只刷新与时间相关的文字（每秒调用一次），不重建棋盘
function applyDuelStatus(r) {
  if (!r) return;
  const st = $('duel-status');
  const live = r.state === 'playing' && r.turn === r.me;
  const opp = r.names[1 - r.me] || '对手';
  const me = r.names[r.me] || '我';
  const left = leftSeconds(r);
  const leftTxt = left === null ? '' : `（剩余 ${fmtLeft(left)}）`;
  const REASON = {
    win: '三架飞机全部被击落',
    timeout: '回合超时',
    offline: '掉线超过宽限',
    leave: '对手主动离开',
  };

  if (r.state === 'waiting') {
    $('duel-rematch').hidden = true;
    st.textContent = '等待对手加入…';
    st.className = 'status';
    setTurnbar('等待对手加入', `把房间码 ${r.code} 发给朋友`, '');
  } else if (r.finished || r.state === 'done') {
    $('duel-rematch').hidden = false;
    const why = REASON[r.reason] || '对局结束';
    if (r.winner === r.me) {
      st.textContent = `🏆 你赢了（${why}）`;
      st.className = 'status done';
      setTurnbar('🏆 你赢了', why, 'done');
    } else {
      st.textContent = `你输了（${why}）`;
      st.className = 'status lose';
      setTurnbar('这局输了', why, 'lose');
    }
  } else if (live) {
    $('duel-rematch').hidden = true;
    const chain = r.last_shot && r.last_shot.by === r.me ? '（连击，继续打）' : '';
    st.textContent = `轮到你出手${leftTxt}${chain}`;
    st.className = 'status';
    setTurnbar(`轮到你（${me}）`, chain ? '连击，继续打' : '点右侧敌情图喊号', 'mine');
  } else {
    $('duel-rematch').hidden = true;
    st.textContent = `等待 ${opp} 出手${leftTxt}`;
    st.className = 'status wait';
    setTurnbar(`等待 ${opp} 出手`, '对手正在选格子', 'theirs');
  }

  // 右上角倒计时胶囊：每秒跟着走
  const chip = $('duel-timer');
  if (chip) {
    chip.textContent = left === null ? '' : `剩余 ${fmtLeft(left)}`;
    chip.className = 'tb-timer' + (left !== null && left <= 20 ? ' warn' : '');
  }

  $('duel-code').textContent = r.code || '—';
  if (r.turn_sec) $('duel-timer').dataset.limit = String(r.turn_sec);
}




function renderSolo() {
  resetTitles();
  const v = state.solo;
  const mine = new Set(v ? v.my_planes : []);
  const myMarks = new Map((v ? v.my_marks : []).map((m) => [m.cell, m.fb]));
  const enemyMarks = new Map((v ? v.enemy_marks : []).map((m) => [m.cell, m.fb]));
  const live = !!v && !v.finished;

  paintBoard($('board-mine'), mine, myMarks, false);
  paintBoard($('board-enemy'), null, enemyMarks, live);

  const stats = $('stats');
  if (!v) {
    stats.hidden = true;
    $('log').textContent = '';
    setTurnbar('准备开始', '点「练习：随机陪练」或邀请在线玩家', '', '');
    return;
  }
  stats.hidden = false;
  $('st-alive').textContent = v.alive;
  $('st-enemy').textContent = v.enemy_alive;
  $('st-shots').textContent = v.shots;
  $('st-hits').textContent = v.hits;
  $('log').textContent = v.finished
    ? (v.winner === 0 ? `练习局：你赢了（${v.shots} 发）` : '练习局：陪练赢了')
    : ((v.last || []).map((m) => (m.by === 0 ? `你 ${m.cell}→${m.fb}` : `陪练 ${m.cell}→${m.fb}`)).join('　'));

  if (v.finished) {
    setTurnbar(v.winner === 0 ? '🏆 练习局你赢了' : '练习局输了',
      `共 ${v.shots} 发，命中 ${v.hits}`, v.winner === 0 ? 'done' : 'lose', '');
  } else {
    setTurnbar('练习模式', `对手：随机陪练　已打 ${v.shots} 发`, 'mine', '');
  }
}

function renderPlayers() {
  const list = $('players');
  const all = state.players || [];
  const me = all.find((p) => p.me);
  const idle = all.filter((p) => !p.busy).length;
  $('online-count').textContent = all.length
    ? `${all.length} 在线 · ${idle} 空闲`
    : '';

  // 人多了才显示搜索框
  const tools = $('player-tools');
  if (tools) tools.hidden = all.length <= 6;
  const q = (tools && !tools.hidden ? ($('player-search').value || '') : '').trim().toLowerCase();

  // 排序：空闲的排前面，自己排最前，其余按昵称
  const shown = all
    .filter((p) => !q || p.nick.toLowerCase().includes(q))
    .sort((a, b) => {
      if (a.me !== b.me) return a.me ? -1 : 1;
      if (!!a.busy !== !!b.busy) return a.busy ? 1 : -1;
      return a.nick.localeCompare(b.nick, 'zh');
    });

  list.innerHTML = '';

  if (!shown.length) {
    const li = document.createElement('li');
    li.className = 'empty';
    li.textContent = all.length
      ? `没有匹配「${q}」的玩家`
      : '还没有人进来 —— 把房间码发给朋友，或让对方打开同一个地址';
    list.appendChild(li);
    return;
  }

  for (const p of shown) {
    const li = document.createElement('li');
    if (p.me) li.classList.add('me');
    if (p.busy) li.classList.add('busy');

    const av = document.createElement('span');
    av.className = 'avatar';
    av.textContent = (p.nick || '?').slice(0, 1).toUpperCase();

    const info = document.createElement('div');
    info.className = 'pinfo';
    const name = document.createElement('span');
    name.className = 'pname';
    name.textContent = p.nick;
    const st = document.createElement('span');
    st.className = 'pstate';
    st.textContent = p.me ? '我' : p.busy ? '对局中' : '空闲';
    info.append(name, st);
    li.append(av, info);

    if (p.me) {
      // 自己：状态里已经写了「我」，不再加按钮
    } else if (p.busy || state.room) {
      // 对局中/自己正在打：不放按钮，避免点了没反应
    } else {
      const btn = document.createElement('button');
      btn.className = 'btn-sm invite-btn';
      btn.textContent = '邀请';
      btn.addEventListener('click', () => invite(p.nick, btn));
      li.appendChild(btn);
    }
    list.appendChild(li);
  }

  if (!me && !state.room) {
    const li = document.createElement('li');
    li.className = 'empty';
    li.textContent = '设置昵称后才会出现在列表里';
    list.appendChild(li);
  }
}
// ---------------------------------------------------------------- 交互
function fire(cell) {
  if (state.room) {
    const r = state.room;
    if (r.state !== 'playing') {
      $('log').textContent = '对局已经结束了';
      return;
    }
    if (r.turn !== r.me) {
      $('log').textContent = `还没轮到你 —— 现在轮到 ${r.names[r.turn] || '对手'}`;
      return;
    }
    send({ t: 'shot', cell });
    return;
  }
  if (!state.solo) {
    $('log').textContent = '你还没进入房间：点上面「建房 / 加入」或邀请别人';
    return;
  }
  if (!state.solo || state.solo.finished) return;
  call('solo/fire', {
    method: 'POST',
    body: JSON.stringify({ id: state.solo.id, cell }),
  }).then((v) => { state.solo = v; render(); })
    .catch((e) => { $('log').textContent = e.message; });
}

function segValue(id) {
  const on = document.querySelector('#' + id + ' button.on');
  return on ? on.dataset.v : '';
}

function turnSec() {
  return Number(segValue('turn-limit')) || 300;
}

function ruleContinue() {
  return segValue('rule-continue') === '1';
}

function initSegs() {
  document.querySelectorAll('.seg').forEach((seg) => {
    seg.addEventListener('click', (e) => {
      const btn = e.target.closest('button');
      if (!btn || !seg.contains(btn)) return;
      seg.querySelectorAll('button').forEach((b) => b.classList.remove('on'));
      btn.classList.add('on');
    });
  });
}

function invite(nick, btn) {
  btn.disabled = true;
  btn.textContent = '已邀请…';
  send({ t: 'invite', to: nick, turn_sec: turnSec(), continue_on_hit: ruleContinue() });
  setTimeout(() => { btn.disabled = false; btn.textContent = '邀请游戏'; }, 3000);
}

$('invite-accept').addEventListener('click', () => {
  if (state.invite) send({ t: 'invite.reply', id: state.invite.id, accept: true });
  $('invite-modal').hidden = true;
  state.invite = null;
});

$('invite-decline').addEventListener('click', () => {
  if (state.invite) send({ t: 'invite.reply', id: state.invite.id, accept: false });
  $('invite-modal').hidden = true;
  state.invite = null;
});

$('new-game').addEventListener('click', async () => {
  try {
    state.solo = await call('solo', { method: 'POST', body: '{}' });
    state.room = null;
    render();
  } catch (e) {
    $('log').textContent = e.message;
  }
});

$('create-btn').addEventListener('click', () => send({ t: 'room.create' }));
$('join-btn').addEventListener('click', () => {
  const code = $('join-code').value.trim().toUpperCase();
  if (code.length < 4) { $('log').textContent = '请填房间码'; return; }
  send({ t: 'room.join', code });
});
$('duel-leave').addEventListener('click', () => send({ t: 'room.leave' }));
$('reconnect').addEventListener('click', () => {
  reconnectAttempt = 0;
  stopReconnect();
  connectWS();
});

$('spec-leave').addEventListener('click', () => send({ t: 'room.leave' }));

$('duel-copy').addEventListener('click', async () => {
  const code = ($('duel-code').textContent || '').trim();
  if (!code || code === '—') return;
  let ok = false;
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(code);
      ok = true;
    }
  } catch (e) { ok = false; }
  if (!ok) {
    // http 明文页面没有 clipboard API，退回到 execCommand
    try {
      const ta = document.createElement('textarea');
      ta.value = code;
      ta.setAttribute('readonly', '');
      ta.style.cssText = 'position:fixed;top:-999px;opacity:0';
      document.body.appendChild(ta);
      ta.select();
      ok = document.execCommand('copy');
      document.body.removeChild(ta);
    } catch (e) { ok = false; }
  }
  const btn = $('duel-copy');
  if (ok) {
    btn.textContent = '已复制';
    setTimeout(() => { btn.textContent = '复制'; }, 1500);
  } else {
    const el = $('duel-code');
    const range = document.createRange();
    range.selectNodeContents(el);
    const sel = window.getSelection();
    sel.removeAllRanges(); sel.addRange(range);
    $('log').className = 'log';
    $('log').textContent = '这个浏览器不允许自动复制，已帮你选中，按 Ctrl+C 即可';
  }
});

$('duel-rematch').addEventListener('click', () => {
  const r = state.room;
  if (!r) return;
  const opp = r.names[1 - r.me];
  if (!opp) {
    $('log').className = 'log err';
    $('log').textContent = '找不到对手信息，请用房间码重新邀请';
    return;
  }
  send({ t: 'invite', to: opp, turn_sec: r.turn_sec || 300, continue_on_hit: !!r.continue_on_hit });
  $('log').className = 'log';
  $('log').textContent = `已向 ${opp} 发出「再来一局」邀请，等对方接受`;
});

$('duel-copy').addEventListener('click', async () => {
  try {
    await navigator.clipboard.writeText(state.room.code);
    $('log').textContent = '房间码已复制';
  } catch { $('log').textContent = `房间码：${state.room.code}`; }
});

$('nick-save').addEventListener('click', async () => {
  const nick = $('nick').value.trim();
  if (nick.length < 2) { setMsg($('nick-msg'), '至少 2 个字', 'err'); return; }
  try {
    const s = await call('session', { method: 'POST', body: JSON.stringify({ nick }) });
    state.nick = s.nick;
    if (s.token) setSID(s.token); // 改名后这个标签页用新身份（另一个窗口不受影响）
    setMsg($('nick-msg'), `你好，${s.nick}`, 'ok');
    state.noReconnect = false;
    if (ws) ws.close();
    connectWS();
    renderPlayers();
  } catch (e) {
    setMsg($('nick-msg'), e.message, 'err');
  }
});

// ---------------------------------------------------------------- 会话与设置
async function loadSession() {
  try {
    const s = await call('session');
    state.channel = s.channel || '';
    if (s.authenticated) {
      state.nick = s.nick;
      $('nick').value = s.nick;
      if (!SID && s.token) setSID(s.token); // 首次进来把这个标签页的身份钉住
      setMsg($('nick-msg'), s.channel === 'gateway' ? '已按飞牛账号自动登录' : `你好，${s.nick}`, 'ok');
      connectWS();
    } else if (s.channel !== 'gateway') {
      // 外网访问：自动分配「打飞机xxxx」昵称，可随时改名
      try {
        const g = await call('session', { method: 'POST', body: JSON.stringify({ nick: '' }) });
        state.nick = g.nick;
        $('nick').value = g.nick;
        if (g.token) setSID(g.token);
        setMsg($('nick-msg'), `已自动分配昵称「${g.nick}」，可点「改名」修改`, 'ok');
        connectWS();
      } catch (e) {
        setMsg($('nick-msg'), '自动分配昵称失败，请手动输入', 'err');
      }
    } else {
      setSID('');
    }
    // 网关通道：身份固定「管理员」，不显示改名表单与登录提示
    const gateway = s.channel === 'gateway';
    const nameChip = $('me-name');
    if (gateway) {
      nameChip.hidden = false;
      nameChip.textContent = s.nick || '管理员';
      $('nick-edit').hidden = true;
      $('nick-msg').hidden = true;
      $('nick').value = '';
    } else {
      nameChip.hidden = true;
      $('nick-edit').hidden = false;
      $('nick-msg').hidden = false;
    }
    await initSettings(s);
  } catch { /* ignore */ }
}

// 管理权限只由入口决定：飞牛网关 / 本机回环 = 管理员；独立端口永远不是。
// 没有口令，所以这里只看服务端返回的 can_admin。
async function initSettings(ctx) {
  const card = $('settings-card');
  if (!ctx.can_admin) { card.hidden = true; return; }
  card.hidden = false;
  try {
    const s = await call('admin/settings');
    $('settings-box').hidden = false;
    $('port-enabled').checked = s.public_port_enabled;
    $('port').value = s.public_port;
    renderPort(s);
  } catch (e) {
    const n = $('port-status');
    n.textContent = e.message;
    n.className = 'hint err';
  }
}

$('port-apply').addEventListener('click', async () => {
  try {
    const s = await call('admin/settings', {
      method: 'PUT',
      body: JSON.stringify({
        public_port_enabled: $('port-enabled').checked,
        public_port: Number($('port').value),
      }),
    });
    renderPort(s);
  } catch (e) {
    const n = $('port-status');
    n.textContent = e.message;
    n.className = 'hint err';
  }
});

function renderPort(s) {
  const n = $('port-status');
  n.className = 'hint';
  // 服务端状态是准的：端口可能被自动顺延，开关可能被回滚
  $('port-enabled').checked = !!s.public_port_enabled;
  if (s.public_port) $('port').value = s.public_port;

  const live = !!(s.status && s.status.listening);
  const addr = s.status && s.status.addr ? s.status.addr.replace(/^:/, '') : '';

  if (s.public_port_enabled && live) {
    if (s.notice) {
      n.textContent = `${s.notice}，实际监听 ${addr}。`;
      n.className = 'hint warn';
    } else {
      n.textContent = `独立端口监听中：${addr}（局域网/外网可直连）。`;
    }
    return;
  }
  if (!s.public_port_enabled) {
    n.textContent = s.notice
      ? `${s.notice} 请换一个端口再试。`
      : '独立端口已关闭：只能通过飞牛网关（/app/planeduel）访问。';
    n.className = s.notice ? 'hint err' : 'hint';
    return;
  }
  n.textContent = `监听失败：${s.status.error}`;
  n.className = 'hint err';
}

// ---------------------------------------------------------------- 启动
buildBoard($('board-mine'), false);
buildBoard($('board-enemy'), true);
render();
initSegs();
loadSession();
setInterval(() => { if (state.room) applyDuelStatus(state.room); }, 1000);
