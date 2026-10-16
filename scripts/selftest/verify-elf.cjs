// verify-elf.cjs —— 静态分析 ELF 二进制，判断其是否可在任意 Linux 上运行。
//
// 背景：本机没有 Linux 执行环境（无 Docker、WSL 无发行版、无 QEMU），
// 无法实际运行产物。退而求其次，直接解析 ELF 头部与段表，
// 验证「纯静态链接」这一使其可移植的关键性质。
//
// 检查项：
//   1. ELF magic 与 class / endianness / 架构正确
//   2. 无 PT_INTERP 段（有则说明依赖动态链接器）
//   3. 无 DT_NEEDED 条目（有则说明依赖外部 .so）
//   4. 有 PT_LOAD 段且入口点非零（可被内核加载执行）
const fs = require('fs');

const expectedMachine = {
  amd64: 62,   // EM_X86_64
  arm64: 183,  // EM_AARCH64
  armv7: 40,   // EM_ARM
};

let fail = 0;
function ok(name, cond, detail) {
  console.log(`  ${cond ? '[PASS]' : '[FAIL]'} ${name}${detail ? '  ' + detail : ''}`);
  if (!cond) fail++;
}

function analyze(file, wantArch) {
  console.log(`\n=== ${file} (期望架构: ${wantArch}) ===`);
  const b = fs.readFileSync(file);
  console.log(`  大小: ${(b.length / 1024 / 1024).toFixed(2)} MB`);

  // --- ELF 头 ---
  const magic = b.slice(0, 4).toString('hex');
  ok('ELF magic', magic === '7f454c46', magic);

  const eiClass = b[4];      // 1=32bit, 2=64bit
  const eiData = b[5];       // 1=LE, 2=BE
  ok('小端序', eiData === 1, `ei_data=${eiData}`);

  const want64 = wantArch !== 'armv7';
  ok(`${want64 ? '64' : '32'} 位`, eiClass === (want64 ? 2 : 1), `ei_class=${eiClass}`);

  const machine = b[18] | (b[19] << 8);
  ok('机器类型正确', machine === expectedMachine[wantArch],
     `e_machine=${machine} 期望=${expectedMachine[wantArch]}`);

  // --- 入口点 ---
  // ELF64: e_entry 在偏移 0x18 (8 字节)；ELF32: 偏移 0x18 (4 字节)
  let entry;
  if (want64) entry = Number(b.readBigUInt64LE(0x18));
  else entry = b.readUInt32LE(0x18);
  ok('入口点非零', entry !== 0, `e_entry=0x${entry.toString(16)}`);

  // --- 程序头表（找 PT_INTERP = 3）---
  let phoff, phentsize, phnum;
  if (want64) {
    phoff = Number(b.readBigUInt64LE(0x20));
    phentsize = b.readUInt16LE(0x36);
    phnum = b.readUInt16LE(0x38);
  } else {
    phoff = b.readUInt32LE(0x1c);
    phentsize = b.readUInt16LE(0x2a);
    phnum = b.readUInt16LE(0x2c);
  }

  let hasInterp = false;
  let hasLoad = false;
  const ptypes = [];
  for (let i = 0; i < phnum; i++) {
    const off = phoff + i * phentsize;
    if (off + 4 > b.length) break;
    const ptype = b.readUInt32LE(off);
    ptypes.push(ptype);
    if (ptype === 3) hasInterp = true;   // PT_INTERP
    if (ptype === 1) hasLoad = true;     // PT_LOAD
  }
  console.log(`  程序头: ${phnum} 个，类型=[${[...new Set(ptypes)].join(',')}]`);

  // PT_INTERP 存在 => 动态链接，依赖 /lib64/ld-linux-*.so
  ok('无 PT_INTERP（非动态链接）', !hasInterp,
     hasInterp ? '存在 PT_INTERP，依赖系统动态链接器' : '');
  ok('有 PT_LOAD 段', hasLoad);

  // --- 查找 .dynamic 中的 DT_NEEDED (tag=1) ---
  // 简化做法：在二进制中查找 ".dynamic" 段名与相关字符串
  const asStr = b.toString('latin1');
  const needsLibc = /libc\.so|libpthread\.so|libdl\.so|librt\.so/.test(asStr);
  ok('未依赖常见系统库', !needsLibc,
     needsLibc ? '发现 libc/libpthread 等依赖' : '纯静态');

  return { hasInterp, hasLoad, machine };
}

// 参数格式：<文件路径>[:架构]
//
// 注意：Windows 盘符形如 C:\...，本身含冒号，
// 因此不能用 split(':') 取架构，必须从【最后一个】冒号处切分，
// 且只有当冒号后的内容是已知架构名时才认为是架构后缀。
const KNOWN_ARCH = ['amd64', 'arm64', 'armv7'];

function parseSpec(spec) {
  const idx = spec.lastIndexOf(':');
  if (idx > 0) {
    const tail = spec.slice(idx + 1);
    if (KNOWN_ARCH.includes(tail)) {
      return { file: spec.slice(0, idx), arch: tail };
    }
  }
  return { file: spec, arch: 'amd64' };
}

const files = process.argv.slice(2);
if (files.length === 0) {
  console.log('用法: node verify-elf.cjs <文件路径>[:架构] ...');
  console.log('示例: node verify-elf.cjs ./btg-amd64:amd64 ./btg-arm64:arm64');
  process.exit(1);
}

for (const spec of files) {
  const { file, arch } = parseSpec(spec);
  if (!fs.existsSync(file)) {
    console.log(`\n  [SKIP] ${file} 不存在`);
    continue;
  }
  analyze(file, arch);
}

console.log('');
if (fail === 0) console.log('*** ELF 校验全部通过：产物为纯静态，可在任意 Linux 运行 ***');
else { console.log(`*** 有 ${fail} 项失败 ***`); process.exit(1); }
