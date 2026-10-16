// verify-locate-root.cjs —— 用 JS 复刻 build-image.sh 的 locate_root 逻辑，
// 覆盖各种调用方式，确认都能定位到正确的仓库根目录。
//
// 为什么需要：bash 在本环境无法执行（沙箱禁止子进程命名管道），
// 而「定位根目录」是最容易出错、又最难察觉的一环 ——
// 算错的后果是脚本报「找不到 Dockerfile.release」，
// 用户完全不知道该怎么办。
const path = require('path');
const fs = require('fs');
const os = require('os');

let fail = 0;
function check(name, got, want) {
  const ok = path.resolve(String(got)) === path.resolve(String(want));
  console.log(`  ${ok ? '[PASS]' : '[FAIL]'} ${name}`);
  if (!ok) { console.log(`         期望: ${want}`); console.log(`         实际: ${got}`); fail++; }
}

// ---- 构造三种真实的目录布局 ------------------------------------------------

const base = path.join(os.tmpdir(), 'locate-root-sim');
fs.rmSync(base, { recursive: true, force: true });

// 布局 A：git clone 得到的仓库
const repoA = path.join(base, 'clone', 'bt-traffic-guard');
fs.mkdirSync(path.join(repoA, 'scripts'), { recursive: true });
fs.writeFileSync(path.join(repoA, 'Dockerfile.release'), 'x');
fs.writeFileSync(path.join(repoA, 'scripts', 'build-image.sh'), 'x');
fs.mkdirSync(path.join(repoA, '.git'), { recursive: true });

// 布局 B：解压 GitHub tarball 得到的目录（无 .git）
const repoB = path.join(base, 'tarball', 'bt-traffic-guard-main');
fs.mkdirSync(path.join(repoB, 'scripts'), { recursive: true });
fs.writeFileSync(path.join(repoB, 'Dockerfile.release'), 'x');
fs.writeFileSync(path.join(repoB, 'scripts', 'build-image.sh'), 'x');

// 布局 C：另一个仓库里恰好也有一份同名脚本副本（用于验证不会误判）
const repoC = path.join(base, 'other-repo');
fs.mkdirSync(path.join(repoC, 'vendor', 'bt-traffic-guard', 'scripts'), { recursive: true });
fs.writeFileSync(path.join(repoC, '.gitkeep'), '');
fs.writeFileSync(path.join(repoC, 'vendor', 'bt-traffic-guard', 'Dockerfile.release'), 'x');
fs.writeFileSync(path.join(repoC, 'vendor', 'bt-traffic-guard', 'scripts', 'build-image.sh'), 'x');

console.log('构造的布局:');
console.log('  A (git clone) :', repoA);
console.log('  B (tarball)   :', repoB);
console.log('  C (其它仓库)  :', path.join(repoC, 'vendor', 'bt-traffic-guard'));

// ---- 复刻 locate_root 的逻辑 ----------------------------------------------
//
// bash 版本：
//   1. git rev-parse --show-toplevel；若成功【且该目录含 Dockerfile.release】则采用
//   2. 否则解析软链接得到脚本真实路径，取其上一级
//
// 这里用等价的 JS 实现：
//   - gitToplevel: 模拟 git rev-parse 的返回（null 表示失败）
//   - scriptArg:   模拟 $0（可能是绝对路径、相对路径、或软链接）
//   - cwd:         模拟调用时的工作目录
function locateRoot({ gitToplevel, scriptArg, cwd }) {
  const hasDockerfile = (d) => fs.existsSync(path.join(d, 'Dockerfile.release'));

  // 1) git 分支 —— 必须校验该目录确实是本项目
  if (gitToplevel && hasDockerfile(gitToplevel)) {
    return gitToplevel;
  }

  // 2) 回退：解析软链接（此处 scriptArg 已是解析后的真实路径）
  const abs = path.isAbsolute(scriptArg) ? scriptArg : path.resolve(cwd, scriptArg);
  return path.resolve(path.dirname(abs), '..');
}

// ---- 场景验证 --------------------------------------------------------------

console.log('\n=== 场景 1：git clone 后在仓库根执行 ===');
check('bash scripts/build-image.sh（相对路径）',
  locateRoot({
    gitToplevel: repoA,
    scriptArg: 'scripts/build-image.sh',
    cwd: repoA,
  }), repoA);

console.log('\n=== 场景 2：从子目录执行 ===');
check('在 scripts/ 里执行 bash build-image.sh',
  locateRoot({
    gitToplevel: repoA,
    scriptArg: 'build-image.sh',
    cwd: path.join(repoA, 'scripts'),
  }), repoA);

console.log('\n=== 场景 3：用绝对路径从别处执行（git 可用）===');
check('cd /tmp && bash /path/scripts/build-image.sh',
  locateRoot({
    gitToplevel: repoA,
    scriptArg: path.join(repoA, 'scripts', 'build-image.sh'),
    cwd: os.tmpdir(),
  }), repoA);

console.log('\n=== 场景 4：tarball 解压，无 git（关键回退路径）===');
check('bash scripts/build-image.sh（无 git）',
  locateRoot({
    gitToplevel: null,
    scriptArg: 'scripts/build-image.sh',
    cwd: repoB,
  }), repoB);

check('绝对路径调用（无 git）',
  locateRoot({
    gitToplevel: null,
    scriptArg: path.join(repoB, 'scripts', 'build-image.sh'),
    cwd: os.tmpdir(),
  }), repoB);

console.log('\n=== 场景 5：在其它仓库里执行副本（不应误判）===');
// git 返回的是 other-repo 根，那里没有 Dockerfile.release，
// 因此必须回退到脚本自身位置，定位到 vendor/bt-traffic-guard
const vendor = path.join(repoC, 'vendor', 'bt-traffic-guard');
check('git 指向无 Dockerfile 的仓库 -> 回退到脚本位置',
  locateRoot({
    gitToplevel: repoC,
    scriptArg: path.join(vendor, 'scripts', 'build-image.sh'),
    cwd: repoC,
  }), vendor);

console.log('\n=== 场景 6：脚本所在目录直接执行 ===');
check('cd scripts && bash ./build-image.sh',
  locateRoot({
    gitToplevel: null,
    scriptArg: './build-image.sh',
    cwd: path.join(repoB, 'scripts'),
  }), repoB);

// ---- 验证 install-workflow.sh 的变体 ---------------------------------------
//
// 它的判断标志物是 scripts/build.yml.example（随仓库分发），
// 逻辑与 build-image.sh 一致，只是标志文件不同。
function locateRootWorkflow({ gitToplevel, scriptArg, cwd }) {
  const marker = (d) =>
    fs.existsSync(path.join(d, 'scripts', 'build.yml.example')) ||
    fs.existsSync(path.join(d, 'Dockerfile.release'));

  if (gitToplevel && marker(gitToplevel)) return gitToplevel;

  const abs = path.isAbsolute(scriptArg) ? scriptArg : path.resolve(cwd, scriptArg);
  return path.resolve(path.dirname(abs), '..');
}

console.log('\n=== 场景 7：install-workflow.sh 的定位 ===');
// 给仓库 A 补上模板标志物
fs.mkdirSync(path.join(repoA, 'scripts'), { recursive: true });
fs.writeFileSync(path.join(repoA, 'scripts', 'build.yml.example'), 'x');
check('git clone 场景',
  locateRootWorkflow({ gitToplevel: repoA, scriptArg: 'scripts/install-workflow.sh', cwd: repoA }),
  repoA);
check('tarball 场景（无 git）',
  locateRootWorkflow({ gitToplevel: null, scriptArg: 'scripts/install-workflow.sh', cwd: repoB }),
  repoB);
check('其它仓库不误判',
  locateRootWorkflow({
    gitToplevel: repoC,
    scriptArg: path.join(vendor, 'scripts', 'install-workflow.sh'),
    cwd: repoC,
  }), vendor);

// 清理
fs.rmSync(base, { recursive: true, force: true });

console.log('');
if (fail === 0) console.log('*** 根目录定位逻辑全部场景通过 ***');
else { console.log(`*** 有 ${fail} 项失败 ***`); process.exit(1); }
