export const startupLimits = {
  aws: 16384,
  azure: 65535,
  digitalocean: 65536,
  linode: 49149,
};
export const startupLabels = {
  none: "不使用初始化脚本",
  shell: "Shell 脚本",
  "cloud-config": "Cloud-init / cloud-config",
  powershell: "PowerShell 初始化脚本",
  stackscript: "Linode StackScript",
};
export const utf8Bytes = (value) => new TextEncoder().encode(value).length;
export const startupPayload = (mode, text) =>
  mode === "powershell" ? `<powershell>\n${text}\n</powershell>` : text;
// SHA-256 works on ordinary HTTP too; subtle.digest requires a secure context.
// This is a display fingerprint only. The server independently hashes the intent.
export function startupHash(text) {
  const data = new TextEncoder().encode(text),
    length = data.length,
    padded = new Uint8Array(((length + 9 + 63) >> 6) << 6);
  padded.set(data);
  padded[length] = 128;
  const view = new DataView(padded.buffer);
  view.setUint32(padded.length - 4, length * 8);
  const constants = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
    0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
    0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
    0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
    0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
    0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
    0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
    0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
    0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
  ];
  const h = [
      0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c,
      0x1f83d9ab, 0x5be0cd19,
    ],
    w = new Int32Array(64),
    rotr = (x, n) => (x >>> n) | (x << (32 - n));
  for (let offset = 0; offset < padded.length; offset += 64) {
    for (let t = 0; t < 16; t++) w[t] = view.getInt32(offset + t * 4);
    for (let t = 16; t < 64; t++) {
      const x = w[t - 15],
        y = w[t - 2];
      w[t] =
        (w[t - 16] +
          (rotr(x, 7) ^ rotr(x, 18) ^ (x >>> 3)) +
          w[t - 7] +
          (rotr(y, 17) ^ rotr(y, 19) ^ (y >>> 10))) |
        0;
    }
    let [a, b, c, d, e, f, g, j] = h;
    for (let t = 0; t < 64; t++) {
      const s1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25),
        ch = (e & f) ^ (~e & g),
        v = (j + s1 + ch + constants[t] + w[t]) | 0,
        s0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22),
        maj = (a & b) ^ (a & c) ^ (b & c);
      j = g;
      g = f;
      f = e;
      e = (d + v) | 0;
      d = c;
      c = b;
      b = a;
      a = (v + s0 + maj) | 0;
    }
    [a, b, c, d, e, f, g, j].forEach((v, i) => (h[i] = (h[i] + v) | 0));
  }
  return h.map((v) => (v >>> 0).toString(16).padStart(8, "0")).join("");
}
export function startupCompatibility(provider, mode, image, region) {
  if (mode === "none") return { allowed: true, note: "不提交初始化数据" };
  if (!image)
    return {
      allowed: false,
      note: "先选择系统镜像，已填写脚本会在当前弹窗中保留",
    };
  if (image.family === "windows")
    return provider === "aws" && mode === "powershell"
      ? {
          allowed: true,
          ack: true,
          note: "需要镜像内受支持的EC2Launch/EC2Config代理。首次启动以系统权限执行，不能仅凭开机判断结果。",
        }
      : {
          allowed: false,
          note: "当前方式不适用Windows；Azure customData仅保存文件，不会自动执行。",
        };
  if (mode === "powershell")
    return { allowed: false, note: "PowerShell仅适用于AWS Windows镜像" };
  if (provider === "linode") {
    if (mode === "stackscript")
      return {
        allowed: region?.extra?.siteType !== "distributed",
        note: "StackScript在首次启动以root权限执行；只选择可信来源，创建前核实当前脚本及参数。",
      };
    return {
      allowed:
        image.startupSupport === "cloud-init" &&
        region?.extra?.metadata === "true",
      note:
        image.startupSupport === "cloud-init" &&
        region?.extra?.metadata === "true"
          ? "官方API确认镜像Cloud-init与地区Metadata能力；仍需登录服务器核实执行结果。"
          : "该镜像或地区未确认Cloud-init / Metadata能力，不能使用脚本。",
    };
  }
  return {
    allowed: true,
    ack: true,
    note:
      provider === "azure"
        ? "镜像Cloud-init能力未通过API核实；Linux Agent默认不执行customData。须先核实镜像代理。"
        : "镜像需包含配置正确的cloud-init；目录不能证明具体代理可执行，须先核实镜像。",
  };
}

export function stackParameterJSON(values) {
  return JSON.stringify(
    Object.fromEntries(
      Object.entries(values).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)),
    ),
  ).replace(
    /[<>&\u2028\u2029]/g,
    (c) => "\\u" + c.charCodeAt(0).toString(16).padStart(4, "0"),
  );
}
