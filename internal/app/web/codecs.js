(function (root) {
  "use strict";
  const encoder = new TextEncoder();
  const decoder = new TextDecoder("utf-8", { fatal: true });
  const algorithms = Object.freeze({ HS256: "SHA-256", HS384: "SHA-384", HS512: "SHA-512" });

  function bytesToBase64(bytes, url = false) {
    let binary = "";
    for (let offset = 0; offset < bytes.length; offset += 8192) {
      binary += String.fromCharCode(...bytes.subarray(offset, offset + 8192));
    }
    const result = root.btoa(binary);
    return url ? result.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "") : result;
  }

  function base64ToBytes(text, url = false) {
    const input = text.replace(/[\t\n\r ]/g, "");
    const alphabet = url ? /^[A-Za-z0-9_-]*={0,2}$/ : /^[A-Za-z0-9+/]*={0,2}$/;
    const raw = input.replace(/=+$/, "");
    if (!alphabet.test(input) || raw.length % 4 === 1 || (input.includes("=") && input.length % 4 !== 0)) {
      throw new Error(`无效的 ${url ? "Base64URL" : "Base64"} 编码，请检查字符和末尾填充`);
    }
    let binary;
    try { binary = root.atob(raw.replace(/-/g, "+").replace(/_/g, "/")); }
    catch (_) { throw new Error("无效的 Base64 编码"); }
    const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
    if (bytesToBase64(bytes, url).replace(/=+$/, "") !== raw) throw new Error("Base64 末尾包含无效的填充位");
    return bytes;
  }

  function decodeUTF8(bytes) {
    try { return decoder.decode(bytes); }
    catch (_) { throw new Error("解码结果不是有效的 UTF-8 文本，此工具用于文本转换"); }
  }

  function encodeBase64(text, url = false) { return bytesToBase64(encoder.encode(text), url); }
  function decodeBase64(text, url = false) { return decodeUTF8(base64ToBytes(text, url)); }

  function encodeURL(text, form = false) {
    try {
      const result = encodeURIComponent(text);
      return form ? result.replace(/[!'()~]/g, (char) => `%${char.charCodeAt(0).toString(16).toUpperCase()}`).replace(/%20/g, "+") : result;
    } catch (_) { throw new Error("输入包含无效的 Unicode 字符"); }
  }

  function decodeURL(text, form = false) {
    try { return decodeURIComponent(form ? text.replace(/\+/g, " ") : text); }
    catch (_) { throw new Error("URL 编码无效，请检查 % 后的十六进制字符及 UTF-8 字节"); }
  }

  function jsonObject(text, label) {
    let value;
    try { value = JSON.parse(text); }
    catch (_) { throw new Error(`${label} 不是有效的 JSON`); }
    if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(`${label} 必须是 JSON 对象`);
    return value;
  }

  function parseJWT(text) {
    const token = text.trim();
    const parts = token.split(".");
    if (parts.length === 5) throw new Error("这是加密的 JWE，此工具支持三段式 JWT（JWS）");
    if (parts.length !== 3 || !parts[0] || !parts[1] || /\s/.test(token)) throw new Error("JWT 应为 Header.Payload.Signature 三段式文本");
    if (parts.some((part) => !/^[A-Za-z0-9_-]*$/.test(part))) throw new Error("JWT 分段必须使用无填充的 Base64URL 编码");
    const header = jsonObject(decodeBase64(parts[0], true), "Header");
    const payload = jsonObject(decodeBase64(parts[1], true), "Payload");
    if (typeof header.alg !== "string" || !header.alg) throw new Error("Header 缺少有效的 alg 算法字段");
    const signature = base64ToBytes(parts[2], true);
    return { header, payload, signature, signingInput: `${parts[0]}.${parts[1]}`, token };
  }

  function checkAlgorithm(algorithm) {
    if (!Object.prototype.hasOwnProperty.call(algorithms, algorithm)) throw new Error("生成和验签仅支持 HS256、HS384、HS512");
  }

  function checkHeader(header, algorithm) {
    checkAlgorithm(algorithm);
    if (header.alg !== algorithm) throw new Error(`Header 的 alg 必须与选择的算法 ${algorithm} 一致`);
    if (header.crit !== undefined || header.b64 !== undefined) throw new Error("暂不支持 crit 或 b64 扩展 Header");
  }

  async function hmacKey(secret, algorithm, usage) {
    if (!secret.length) throw new Error("请填写签名密钥");
    if (!root.crypto?.subtle) throw new Error("当前浏览器不支持 Web Crypto，请使用 localhost 地址和新版浏览器");
    return root.crypto.subtle.importKey("raw", encoder.encode(secret), { name: "HMAC", hash: algorithms[algorithm] }, false, [usage]);
  }

  async function generateJWT(headerText, payloadText, secret, algorithm) {
    const header = jsonObject(headerText, "Header");
    const payload = jsonObject(payloadText, "Payload");
    checkHeader(header, algorithm);
    const key = await hmacKey(secret, algorithm, "sign");
    const input = `${encodeBase64(JSON.stringify(header), true)}.${encodeBase64(JSON.stringify(payload), true)}`;
    const signature = await root.crypto.subtle.sign("HMAC", key, encoder.encode(input));
    return `${input}.${bytesToBase64(new Uint8Array(signature), true)}`;
  }

  async function verifyJWT(text, secret, algorithm) {
    const parsed = parseJWT(text);
    checkHeader(parsed.header, algorithm);
    const key = await hmacKey(secret, algorithm, "verify");
    const valid = await root.crypto.subtle.verify("HMAC", key, parsed.signature, encoder.encode(parsed.signingInput));
    return { ...parsed, valid };
  }

  function claims(payload, now = Date.now() / 1000) {
    const labels = { iat: "签发时间", nbf: "生效时间", exp: "过期时间" };
    return Object.entries(labels).filter(([key]) => Object.prototype.hasOwnProperty.call(payload, key)).map(([key, label]) => {
      const value = payload[key];
      const date = new Date(typeof value === "number" ? value * 1000 : NaN);
      if (!Number.isFinite(value) || Number.isNaN(date.getTime())) return { key, label, value: "无效的 Unix 秒时间戳", warning: true };
      const warning = key === "exp" ? value <= now : (key === "nbf" ? value > now : false);
      return { key, label, value: `${date.toLocaleString()}${warning ? (key === "exp" ? " · 已过期" : " · 尚未生效") : ""}`, warning };
    });
  }

  const codecs = Object.freeze({ encodeBase64, decodeBase64, encodeURL, decodeURL, parseJWT, generateJWT, verifyJWT, claims });
  root.FloeCodecs = codecs;
  if (typeof module !== "undefined" && module.exports) module.exports = codecs;
})(globalThis);
