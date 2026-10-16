// validate-yaml.cjs —— 用极简 YAML 子集校验关键结构，
// 避免依赖外部库。重点检查缩进、键值格式与明显的语法错误。
//
// 说明：这不是一个完整的 YAML 解析器，而是针对本项目文件的
// 结构性检查：确认能按行解析出顶层键、没有 tab 缩进、
// 没有明显未闭合的引号，并且关键字段存在。
const fs = require('fs');
const path = require('path');

const ROOT = process.argv[2];

const files = [
  'docker-compose.yml',
  'config.example.yaml',
];

let fail = 0;

function check(file) {
  const p = path.join(ROOT, file);
  if (!fs.existsSync(p)) {
    console.log(`  [FAIL] ${file} 不存在`);
    fail++;
    return;
  }
  const raw = fs.readFileSync(p, 'utf8');
  const lines = raw.split(/\r?\n/);
  const problems = [];

  lines.forEach((line, i) => {
    const n = i + 1;
    // tab 缩进在 YAML 里非法
    if (/^\t/.test(line)) problems.push(`第 ${n} 行使用了 tab 缩进`);
    // 未闭合的引号（奇数个双引号且非注释）
    const noComment = line.replace(/\s+#.*$/, '');
    const quotes = (noComment.match(/"/g) || []).length;
    if (quotes % 2 !== 0 && !line.includes('#')) {
      problems.push(`第 ${n} 行引号可能未闭合: ${line.trim().slice(0, 60)}`);
    }
    // 行尾多余空格（非致命，仅提示）
  });

  // 顶层键数量
  const topKeys = lines
    .filter((l) => /^[A-Za-z_][\w.-]*:/.test(l))
    .map((l) => l.split(':')[0]);

  if (problems.length) {
    console.log(`  [FAIL] ${file}`);
    problems.slice(0, 5).forEach((x) => console.log(`         ${x}`));
    fail++;
  } else {
    const kb = (raw.length / 1024).toFixed(1);
    console.log(`  [OK]   ${file.padEnd(32)} ${lines.length} 行 / ${kb} KB / 顶层键: ${topKeys.join(', ')}`);
  }
  return { raw, topKeys };
}

console.log('=== YAML 结构校验 ===');
const results = {};
for (const f of files) results[f] = check(f);

// 针对性的语义断言
console.log('\n=== 关键字段断言 ===');

function assert(cond, msg) {
  if (cond) console.log(`  [OK]   ${msg}`);
  else { console.log(`  [FAIL] ${msg}`); fail++; }
}

const compose = results['docker-compose.yml']?.raw || '';
// GHCR 镜像已发布，compose 默认应直接拉取它（用户无需本地构建）。
assert(/image:\s*ghcr\.io\/peace2323\/bt-traffic-guard:latest/.test(compose),
  'compose 默认使用已发布的 GHCR 镜像');
// 镜像名必须全小写，否则 GHCR 拉取会失败
const composeImage = (compose.match(/image:\s*(\S+)/) || [])[1] || '';
assert(composeImage === composeImage.toLowerCase(),
  'compose 的镜像名全小写（GHCR 要求）');
assert(/TZ:/.test(compose), 'compose 设置了时区');
assert(/\/config/.test(compose) && /\/data/.test(compose), 'compose 挂载了 /config 与 /data');
assert(!/^\s{4}BTG_QBITTORRENT_PASSWORD:/m.test(compose),
  'compose 默认不强制设置连接信息（应为注释状态）');

// workflow 文件按设计不在工作树中（受权限门禁未推送），
// 因此只在它存在时才校验内容。
const wfPath = path.join(ROOT, '.github/workflows/build.yml');
if (fs.existsSync(wfPath)) {
  const wf = fs.readFileSync(wfPath, 'utf8');
  assert(/packages:\s*write/.test(wf), 'workflow 声明 packages: write 权限');
  assert(/ghcr\.io/.test(wf), 'workflow 推送到 ghcr.io');
  assert(/linux\/amd64,linux\/arm64/.test(wf), 'workflow 构建 amd64 + arm64 双架构');
  assert(/needs:\s*test/.test(wf), 'workflow 中 build 依赖 test');
  assert(/\$\{GITHUB_REPOSITORY,,\}/.test(wf), 'workflow 对镜像名做小写转换');
  assert(/secrets\.GITHUB_TOKEN/.test(wf), 'workflow 使用 GITHUB_TOKEN 登录 GHCR');
} else {
  console.log('  [SKIP] workflow 文件不在工作树中（按设计保留在 git 历史里）');
}

const cfg = results['config.example.yaml']?.raw || '';
assert(/api_key:/.test(cfg), 'config 示例包含 api_key 字段');
assert(/threshold_total:/.test(cfg), 'config 示例包含阈值字段');
assert(/limit_total:/.test(cfg), 'config 示例包含限速字段');

// Unraid 模板：GHCR 镜像现已发布，模板应指向它，
// 这样用户安装时无需任何本地构建。
const tplPath = path.join(ROOT, 'unraid-template.xml');
if (fs.existsSync(tplPath)) {
  const tpl = fs.readFileSync(tplPath, 'utf8');
  assert(/<Repository>ghcr\.io\/peace2323\/bt-traffic-guard:latest<\/Repository>/.test(tpl),
    'Unraid 模板指向已发布的 GHCR 镜像');
  // GHCR 要求仓库路径全小写，大小写错误会导致拉取失败
  const repoTag = (tpl.match(/<Repository>([^<]+)<\/Repository>/) || [])[1] || '';
  assert(repoTag === repoTag.toLowerCase(),
    'Unraid 模板的镜像名全小写（GHCR 要求）');
}

console.log('');
if (fail === 0) {
  console.log('*** 全部校验通过 ***');
} else {
  console.log(`*** 有 ${fail} 项失败 ***`);
  process.exit(1);
}
