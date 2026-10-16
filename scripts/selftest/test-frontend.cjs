// test-frontend.cjs —— 在 Node 中执行 index.html 里的前端脚本，
// 用最小 DOM 打桩捕捉运行时错误（语法错误、未定义函数、逻辑异常）。
//
// 为什么需要：静态检查只能确认函数「存在」，无法发现
// 语法错误、拼写错误的调用、或渲染时的类型异常。
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const ROOT = process.argv[2] || '.';
const html = fs.readFileSync(path.join(ROOT, 'internal/web/static/index.html'), 'utf8');

// 抽取 <script> 内容（最后一个 script 块是应用逻辑）
const scripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map((m) => m[1]);
if (scripts.length === 0) {
  console.error('[FAIL] 未找到 <script> 块');
  process.exit(1);
}
const code = scripts[scripts.length - 1];
console.log(`提取脚本: ${code.length} 字符`);

let fail = 0;

// ---- 1. 语法检查 ----------------------------------------------------------
try {
  new vm.Script(code, { filename: 'index.html:script' });
  console.log('  [PASS] 语法检查通过');
} catch (e) {
  console.log(`  [FAIL] 语法错误: ${e.message}`);
  process.exit(1);
}

// ---- 2. 构造最小 DOM 打桩 --------------------------------------------------
const elements = new Map();

function makeEl(id) {
  const el = {
    id,
    _value: '',
    _checked: false,
    textContent: '',
    innerHTML: '',
    className: '',
    style: {},
    type: 'text',
    disabled: false,
    placeholder: '',
    children: [],
    get value() { return this._value; },
    set value(v) { this._value = v; },
    get checked() { return this._checked; },
    set checked(v) { this._checked = v; },
    addEventListener() {},
    removeEventListener() {},
    querySelectorAll() { return []; },
    querySelector() { return null; },
    appendChild(c) { this.children.push(c); return c; },
    remove() {},
    setAttribute(k, v) { this[k] = v; },
    getAttribute(k) { return this[k] !== undefined ? this[k] : null; },
    classList: { add() {}, remove() {}, contains() { return false; } },
    getContext() {
      // canvas 2d context 打桩
      const noop = () => {};
      return new Proxy({}, {
        get: (t, k) => {
          if (k === 'canvas') return { width: 600, height: 170 };
          if (k === 'createLinearGradient') return () => ({ addColorStop: noop });
          if (k === 'measureText') return () => ({ width: 10 });
          return noop;
        },
        set: () => true,
      });
    },
  };
  return el;
}

function getEl(id) {
  if (!elements.has(id)) elements.set(id, makeEl(id));
  return elements.get(id);
}

const documentStub = {
  getElementById: getEl,
  querySelector: () => null,
  querySelectorAll: () => [],
  createElement: (tag) => makeEl('created-' + tag),
  addEventListener() {},
  body: makeEl('body'),
};

// 预建常用元素，避免 getElementById 返回 undefined
[
  'toast', 'badge-conn', 'badge-mode', 'badge-uptime', 'btn-refresh',
  'm-speed', 'm-speed-fam', 'm-window', 'm-window-bar', 'm-window-sub',
  'm-limit', 'm-limit-sub', 'm-clients', 'm-clients-sub',
  'chart-speed', 'chart-cum', 'clients-table', 'policies', 'events',
  'client-forms', 'policy-forms', 'cfg-text',
  'f-listen', 'f-poll', 'f-loglevel', 'f-timezone', 'f-history',
  'f-auth-enabled', 'f-auth-user', 'f-auth-pass',
  'f-notify-enabled', 'f-ntfy-enabled', 'f-ntfy-url', 'f-ntfy-topic',
  'f-ntfy-token', 'f-ntfy-priority',
  'f-webhook-enabled', 'f-webhook-url', 'f-webhook-format', 'f-webhook-method',
].forEach(getEl);

const fetchCalls = [];
const sandbox = {
  document: documentStub,
  window: { addEventListener() {}, devicePixelRatio: 1 },
  console,
  fetch: (url, opts) => {
    fetchCalls.push({ url, opts });
    // 返回一个空但结构正确的响应，让渲染逻辑能跑完
    return Promise.resolve({
      ok: true,
      status: 200,
      text: () => Promise.resolve('{}'),
      json: () => Promise.resolve({}),
    });
  },
  setInterval: () => 0,
  setTimeout: (fn) => 0,
  clearTimeout: () => {},
  Date, Math, JSON, Number, String, Array, Object, Boolean, Error,
  parseInt, parseFloat, isNaN, encodeURIComponent, decodeURIComponent,
};
sandbox.globalThis = sandbox;

// ---- 3. 执行脚本 ----------------------------------------------------------
let ctx;
try {
  ctx = vm.createContext(sandbox);
  vm.runInContext(code, ctx, { filename: 'index.html:script', timeout: 10000 });
  console.log('  [PASS] 脚本执行无异常');
} catch (e) {
  console.log(`  [FAIL] 执行时报错: ${e.message}`);
  if (e.stack) console.log('        ' + String(e.stack).split('\n').slice(0, 4).join('\n        '));
  fail++;
}

// ---- 4. 检查关键常量与函数已定义 ------------------------------------------
console.log('\n=== 关键符号检查 ===');
const wantFn = [
  'renderClientForms', 'renderPolicyForms', 'renderGeneralForm',
  'collectClientInputs', 'collectPolicyInputs', 'collectGeneralForm',
  'loadFormTab', 'saveForm', 'clientTemplate', 'policyTemplate',
  'fmtBytes', 'fmtSpeed', 'esc', 'drawAreaChart',
];
for (const f of wantFn) {
  const ok = typeof ctx[f] === 'function';
  console.log(`  ${ok ? '[PASS]' : '[FAIL]'} 函数 ${f}`);
  if (!ok) fail++;
}

console.log('\n=== 关键常量检查 ===');
// 注意：顶层 const 声明不会挂到 vm 的 context 对象上（词法作用域），
// 因此不能直接读 ctx.PASSWORD_MASK。改为从源码文本解析声明值，
// 并与后端 internal/web/form.go 中的定义比对 —— 这才是真正要保证的性质：
// 前后端的哨兵值必须逐字节一致，否则密码会被写坏。
const maskDecl = /const PASSWORD_MASK = "(.*?)";/.exec(code);
const clearDecl = /const CLEAR_TOKEN = "(.*?)";/.exec(code);
const goSrc = fs.readFileSync(path.join(ROOT, 'internal/web/form.go'), 'utf8');
const goMask = /passwordMask = "(.*?)"/.exec(goSrc);
const goClear = /clearToken = "(.*?)"/.exec(goSrc);

function cmpSentinel(name, fe, be) {
  if (!fe) { console.log(`  [FAIL] 前端未声明 ${name}`); fail++; return; }
  if (!be) { console.log(`  [FAIL] 后端未声明 ${name}`); fail++; return; }
  const ok = fe[1] === be[1];
  console.log(`  ${ok ? '[PASS]' : '[FAIL]'} ${name} 前后端一致: ${JSON.stringify(fe[1])}`);
  if (!ok) {
    console.log(`         前端: ${JSON.stringify(fe[1])}`);
    console.log(`         后端: ${JSON.stringify(be[1])}`);
    fail++;
  }
}
cmpSentinel('PASSWORD_MASK', maskDecl, goMask);
cmpSentinel('CLEAR_TOKEN', clearDecl, goClear);

// ---- 5. 调用纯函数验证行为 -------------------------------------------------
console.log('\n=== 函数行为抽查 ===');
try {
  const cases = [
    ['fmtBytes(1024)', () => ctx.fmtBytes(1024), '1.00 KiB'],
    ['fmtBytes(0)', () => ctx.fmtBytes(0), '0 B'],
    ['fmtSpeed(0)', () => ctx.fmtSpeed(0), '不限速'],
    ['fmtSpeed(1048576)', () => ctx.fmtSpeed(1048576), '1.00 MiB/s'],
    ['esc("<b>")', () => ctx.esc('<b>'), '&lt;b&gt;'],
  ];
  for (const [name, fn, want] of cases) {
    let got;
    try { got = fn(); } catch (e) { got = 'ERR:' + e.message; }
    const ok = String(got) === String(want);
    console.log(`  ${ok ? '[PASS]' : '[FAIL]'} ${name.padEnd(22)} = ${JSON.stringify(got)}`);
    if (!ok) { console.log(`         期望: ${JSON.stringify(want)}`); fail++; }
  }
} catch (e) {
  console.log(`  [FAIL] 行为抽查异常: ${e.message}`);
  fail++;
}

// ---- 6. 模板函数不应抛错 ---------------------------------------------------
console.log('\n=== 模板函数 ===');
for (const f of ['clientTemplate', 'policyTemplate']) {
  try {
    const v = ctx[f]();
    const ok = v && typeof v === 'object';
    console.log(`  ${ok ? '[PASS]' : '[FAIL]'} ${f}() 返回对象: ${JSON.stringify(v).slice(0, 70)}...`);
    if (!ok) fail++;
  } catch (e) {
    console.log(`  [FAIL] ${f}() 抛错: ${e.message}`);
    fail++;
  }
}

console.log('');
if (fail === 0) console.log('*** 前端脚本校验全部通过 ***');
else { console.log(`*** 有 ${fail} 项失败 ***`); process.exit(1); }
