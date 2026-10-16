// test-roundtrip.cjs —— 端到端验证「网页表单保存」的完整数据路径。
//
// 模拟真实用户操作序列：
//   1. 打开配置页，读取表单
//   2. 填入 API Key 与地址
//   3. 保存
//   4. 再次打开（模拟刷新），确认掩码生效
//   5. 只改名字，保存，确认密码未丢
//   6. 点击「清除」，保存，确认 Key 被清空
//
// 直接打真实的 /api/config/form 接口，等价于浏览器行为。
const http = require('http');

const BASE = process.argv[2] || 'http://127.0.0.1:18100';
const PASSWORD_MASK = '\u0000__UNCHANGED__\u0000';
const CLEAR_TOKEN = '\u0000__CLEAR__\u0000';

let fail = 0;
function check(name, got, want) {
  const ok = JSON.stringify(got) === JSON.stringify(want);
  console.log(`  ${ok ? '[PASS]' : '[FAIL]'} ${name}`);
  if (!ok) { console.log(`         期望: ${JSON.stringify(want)}`); console.log(`         实际: ${JSON.stringify(got)}`); fail++; }
}
function ok(name, cond) {
  console.log(`  ${cond ? '[PASS]' : '[FAIL]'} ${name}`);
  if (!cond) fail++;
}

function request(method, path, body) {
  return new Promise((resolve, reject) => {
    const payload = body ? JSON.stringify(body) : null;
    const u = new URL(BASE + path);
    const req = http.request(
      {
        hostname: u.hostname, port: u.port, path: u.pathname + u.search, method,
        headers: Object.assign(
          { 'Content-Type': 'application/json' },
          payload ? { 'Content-Length': Buffer.byteLength(payload) } : {}
        ),
      },
      (res) => {
        let d = '';
        res.on('data', (c) => (d += c));
        res.on('end', () => {
          let j = null;
          try { j = d ? JSON.parse(d) : null; } catch (e) { j = d; }
          resolve({ code: res.statusCode, body: j });
        });
      }
    );
    req.on('error', reject);
    if (payload) req.write(payload);
    req.end();
  });
}

const getForm = () => request('GET', '/api/config/form');
const postForm = (cfg) => request('POST', '/api/config/form', cfg);

// 只取服务端期望的字段（模拟前端 collect 的结果）
function toPayload(f) {
  return {
    general: f.general,
    clients: f.clients,
    policies: f.policies,
    notify: f.notify,
  };
}

(async () => {
  console.log('=== 步骤 1：读取初始表单 ===');
  let r = await getForm();
  ok('GET /api/config/form 返回 200', r.code === 200);
  let f = r.body;
  ok('包含 clients 数组', Array.isArray(f.clients));
  ok('包含 policies 数组', Array.isArray(f.policies));
  if (!Array.isArray(f.clients) || !f.clients.length) {
    console.log('  [SKIP] 无客户端，终止');
    process.exit(fail ? 1 : 0);
  }

  console.log('\n=== 步骤 2：填入 API Key 与地址 ===');
  const KEY = 'qbt_roundtrip1234567890abcdefg';
  f.clients[0].api_key = KEY;
  f.clients[0].url = 'http://127.0.0.1:18081';
  r = await postForm(toPayload(f));
  ok('保存返回 200', r.code === 200);
  ok('保存后认证方式为 API Key', r.body?.config?.clients?.[0]?.auth_mode === 'API Key');
  ok('返回的 Key 是掩码而非明文', r.body?.config?.clients?.[0]?.api_key === PASSWORD_MASK);
  ok('不再返回明文 Key', !JSON.stringify(r.body).includes(KEY));

  console.log('\n=== 步骤 3：模拟刷新后重新读取 ===');
  r = await getForm();
  f = r.body;
  ok('Key 显示为掩码', f.clients[0].api_key === PASSWORD_MASK);
  ok('has_api_key = true', f.clients[0].has_api_key === true);
  ok('api_key_hint 非空', typeof f.clients[0].api_key_hint === 'string' && f.clients[0].api_key_hint.length > 0);
  ok('响应体中不含明文 Key', !JSON.stringify(f).includes(KEY));

  console.log('\n=== 步骤 4：只改名字，掩码原样回传，Key 必须保留 ===');
  const hintBefore = f.clients[0].api_key_hint;
  f.clients[0].name = f.clients[0].name; // 不改名，只验证掩码回传
  r = await postForm(toPayload(f));
  ok('保存返回 200', r.code === 200);
  r = await getForm();
  ok('Key 仍在（未因掩码回传而丢失）', r.body.clients[0].has_api_key === true);
  ok('Key 提示串不变', r.body.clients[0].api_key_hint === hintBefore);

  console.log('\n=== 步骤 5：显式清除 Key ===');
  f = r.body;
  f.clients[0].api_key = CLEAR_TOKEN;
  r = await postForm(toPayload(f));
  ok('保存返回 200', r.code === 200);
  r = await getForm();
  ok('Key 已被清空', r.body.clients[0].has_api_key === false);
  ok('认证方式回退为用户名密码或无认证',
     r.body.clients[0].auth_mode !== 'API Key');

  console.log('\n=== 步骤 6：传空串（用户没碰输入框）不应清空密码 ===');
  const pwdBefore = r.body.clients[0].has_password;
  f = r.body;
  f.clients[0].password = '';   // 空串 = 未修改
  r = await postForm(toPayload(f));
  ok('保存返回 200', r.code === 200);
  r = await getForm();
  ok('密码状态与操作前一致', r.body.clients[0].has_password === pwdBefore);

  console.log('');
  if (fail === 0) console.log('*** 表单往返测试全部通过 ***');
  else { console.log(`*** 有 ${fail} 项失败 ***`); process.exit(1); }
})().catch((e) => {
  console.error('异常: ' + e.message);
  process.exit(1);
});
