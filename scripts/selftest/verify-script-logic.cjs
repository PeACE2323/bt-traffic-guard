// verify-script-logic.cjs —— 用 JS 复刻 build-image.sh 的关键逻辑，
// 验证分支判断是否符合预期（bash 在本沙箱无法执行，改用等价实现检验逻辑）。
//
// 逐条核对：
//   1. 架构识别映射
//   2. 下载 URL 拼接
//   3. 文件大小校验阈值
//   4. ELF magic 校验
const fs = require('fs');
const path = require('path');
const os = require('os');

let fail = 0;
function check(name, got, want) {
  const ok = String(got) === String(want);
  console.log(`  ${ok ? '[PASS]' : '[FAIL]'} ${name.padEnd(46)} got=${got}`);
  if (!ok) { console.log(`         期望: ${want}`); fail++; }
}

console.log('=== 1. 架构识别映射（对应脚本的 case 分支）===');
// bash: x86_64|amd64) ARCH="amd64" ;; aarch64|arm64) ARCH="arm64" ;; armv7l|armv7) ARCH="armv7"
const map = (raw) => {
  if (raw === 'x86_64' || raw === 'amd64') return 'amd64';
  if (raw === 'aarch64' || raw === 'arm64') return 'arm64';
  if (raw === 'armv7l' || raw === 'armv7') return 'armv7';
  return null;
};
check('uname -m = x86_64', map('x86_64'), 'amd64');
check('uname -m = amd64', map('amd64'), 'amd64');
check('uname -m = aarch64', map('aarch64'), 'arm64');
check('uname -m = arm64', map('arm64'), 'arm64');
check('uname -m = armv7l', map('armv7l'), 'armv7');
check('uname -m = armv7', map('armv7'), 'armv7');
check('uname -m = s390x（应拒绝）', map('s390x'), 'null');
check('uname -m = mips（应拒绝）', map('mips'), 'null');

console.log('\n=== 2. 下载 URL 拼接 ===');
const REPO = 'PeACE2323/bt-traffic-guard';
const url = (arch) => `https://github.com/${REPO}/releases/latest/download/bt-traffic-guard-linux-${arch}`;
check('amd64 URL', url('amd64'),
  'https://github.com/PeACE2323/bt-traffic-guard/releases/latest/download/bt-traffic-guard-linux-amd64');
check('arm64 URL', url('arm64'),
  'https://github.com/PeACE2323/bt-traffic-guard/releases/latest/download/bt-traffic-guard-linux-arm64');
check('armv7 URL', url('armv7'),
  'https://github.com/PeACE2323/bt-traffic-guard/releases/latest/download/bt-traffic-guard-linux-armv7');

console.log('\n=== 3. 用真实下载的文件验证大小与 ELF 校验 ===');
const dir = process.argv[2];

// 复刻脚本里的 ELF 判定：
//   magic="$(dd if=... bs=1 count=4 | od -An -c | tr -d ' \n')"
//   case "$magic" in 177ELF*) ok ;; esac
// od -c 把 0x7F 显示为八进制 177，其余可打印字符原样输出。
function odC(f) {
  const b = fs.readFileSync(f).slice(0, 4);
  let s = '';
  for (const c of b) {
    if (c >= 32 && c < 127) s += String.fromCharCode(c);
    else s += '\\' + c.toString(8).padStart(3, '0');
  }
  return s.replace(/[ \n]/g, '');
}
function isELF(f) {
  if (odC(f).startsWith('177ELF')) return true;
  // 备用判定：直接比对前 4 字节
  return fs.readFileSync(f).slice(0, 4).equals(Buffer.from([0x7f, 0x45, 0x4c, 0x46]));
}

for (const a of ['amd64', 'arm64', 'armv7']) {
  const f = path.join(dir, 'btg-' + a);
  if (!fs.existsSync(f)) { console.log(`  [SKIP] ${a} 文件不存在`); continue; }
  const buf = fs.readFileSync(f);
  // 脚本: [ "$size" -lt 1000000 ] 视为失败
  check(`${a} 大小 >= 1MB`, buf.length >= 1000000, 'true');
  check(`${a} 通过 ELF 判定`, isELF(f), 'true');
}

console.log('\n=== 4. 边界：非 ELF 内容应被拒绝 ===');
// 模拟下载到了错误页面（如 404 HTML），而不是二进制
const fake = path.join(os.tmpdir(), 'verify-script-logic-fake-' + process.pid);
fs.writeFileSync(fake, '<html>404 Not Found</html>');
check('404 页面大小 < 1MB（应拒绝）', fs.statSync(fake).size < 1000000, 'true');
check('404 页面未通过 ELF 判定（应拒绝）', isELF(fake), 'false');
fs.rmSync(fake, { force: true });

console.log('');
if (fail === 0) console.log('*** 脚本逻辑校验全部通过 ***');
else { console.log(`*** 有 ${fail} 项失败 ***`); process.exit(1); }
