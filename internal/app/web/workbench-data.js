(function (root) {
  "use strict";
  const MAX_INPUT = 1024 * 1024;
  class JSONNumber {
    constructor(raw) { this.raw = raw; }
  }

  // Retain the original decimal tokens: formatting and diff must not round IDs
  // larger than Number.MAX_SAFE_INTEGER or overflow large exponents.
  function parseJSON(text, unwrap = false, label = "JSON") {
    if (text.length > MAX_INPUT) throw new Error(`${label} 不能超过 1 MiB`);
    let position = 0;
    const fail = (message) => {
      const prefix = text.slice(0, position);
      const line = prefix.split("\n").length;
      const column = position - prefix.lastIndexOf("\n");
      throw new Error(`${label} 第 ${line} 行、第 ${column} 列：${message}`);
    };
    const space = () => { while (/[\t\n\r ]/.test(text[position] || "x")) position++; };
    function string() {
      const start = position++;
      while (position < text.length) {
        const char = text[position++];
        if (char === "\\") position++;
        else if (char === '"') {
          try { return JSON.parse(text.slice(start, position)); }
          catch (_) { fail("字符串转义或内容无效"); }
        }
      }
      fail("字符串缺少结束引号");
    }
    function value(depth = 0) {
      if (depth > 128) fail("嵌套层数超过 128");
      space();
      const char = text[position];
      if (char === '"') return string();
      if (char === "[" || char === "{") {
        const array = char === "[", end = array ? "]" : "}";
        const result = array ? [] : Object.create(null);
        position++; space();
        if (text[position] === end) { position++; return result; }
        while (true) {
          space();
          if (array) result.push(value(depth + 1));
          else {
            if (text[position] !== '"') fail("对象键必须使用双引号");
            const key = string(); space();
            if (text[position++] !== ":") fail("键后缺少冒号");
            result[key] = value(depth + 1);
          }
          space();
          if (text[position] === end) { position++; return result; }
          if (text[position++] !== ",") fail(`缺少逗号或 ${end}`);
        }
      }
      for (const [token, literal] of [["true", true], ["false", false], ["null", null]]) {
        if (text.startsWith(token, position)) { position += token.length; return literal; }
      }
      const number = /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/.exec(text.slice(position));
      if (number) { position += number[0].length; return new JSONNumber(number[0]); }
      fail("需要有效的 JSON 值");
    }
    const result = value(); space();
    if (position !== text.length) fail("JSON 值后存在多余内容");
    if (unwrap && typeof result === "string" && /^[\[{]/.test(result.trim())) return parseJSON(result, false, label);
    return result;
  }

  function stringifyJSON(value, indent = 2, sort = false) {
    const width = Number(indent);
    if (!Number.isInteger(width) || width < 0 || width > 8) throw new Error("缩进应为 0 到 8 的整数");
    function encode(node, depth) {
      if (node instanceof JSONNumber) return node.raw;
      if (!node || typeof node !== "object") return JSON.stringify(node);
      const array = Array.isArray(node);
      let keys = Object.keys(node);
      if (!array && sort) keys = keys.sort();
      const parts = keys.map((key) => `${array ? "" : JSON.stringify(key) + (width ? ": " : ":")}${encode(node[key], depth + 1)}`);
      const [open, close] = array ? ["[", "]"] : ["{", "}"];
      if (!parts.length) return open + close;
      if (!width) return open + parts.join(",") + close;
      const pad = " ".repeat(width * (depth + 1));
      return `${open}\n${pad}${parts.join(",\n" + pad)}\n${" ".repeat(width * depth)}${close}`;
    }
    return encode(value, 0);
  }

  function numberIdentity(raw) {
    const match = /^(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(raw);
    let digits = (match[2] + (match[3] || "")).replace(/^0+/, "");
    if (!digits) return "0";
    let exponent = BigInt(match[4] || 0) - BigInt((match[3] || "").length);
    const zeros = /0+$/.exec(digits)?.[0].length || 0;
    if (zeros) { digits = digits.slice(0, -zeros); exponent += BigInt(zeros); }
    return `${match[1]}${digits}e${exponent}`;
  }

  function diffJSON(left, right) {
    const rows = [];
    const add = (path, kind, a, b) => {
      if (rows.length >= 20000) throw new Error("比较结果超过 20000 项，请缩小输入范围");
      rows.push({ path, kind, before: a === undefined ? undefined : stringifyJSON(a, 2, true), after: b === undefined ? undefined : stringifyJSON(b, 2, true) });
    };
    const type = (x) => x instanceof JSONNumber ? "number" : x === null ? "null" : Array.isArray(x) ? "array" : typeof x;
    function walk(a, b, path, hasA = true, hasB = true) {
      if (!hasA) { add(path, "added", undefined, b); return; }
      if (!hasB) { add(path, "removed", a, undefined); return; }
      const ta = type(a), tb = type(b);
      if (ta !== tb) { add(path, "changed", a, b); return; }
      if (ta === "object" || ta === "array") {
        const array = ta === "array";
        const keys = array ? Array.from({ length: Math.max(a.length, b.length) }, (_, i) => String(i)) : [...new Set([...Object.keys(a), ...Object.keys(b)])].sort();
        if (!keys.length) add(path, "same", a, b);
        for (const key of keys) {
          const next = array ? `${path}[${key}]` : /^[A-Za-z_$][\w$]*$/.test(key) ? `${path}.${key}` : `${path}[${JSON.stringify(key)}]`;
          walk(a[key], b[key], next, Object.hasOwn(a, key), Object.hasOwn(b, key));
        }
      } else {
        const same = ta === "number" ? numberIdentity(a.raw) === numberIdentity(b.raw) : a === b;
        add(path, same ? "same" : "changed", a, b);
      }
    }
    walk(left, right, "$");
    return rows;
  }

  const dayMS = 86400000;
  const excelEpoch = Date.UTC(1899, 11, 30);
  function pad(value, width = 2) { return String(value).padStart(width, "0"); }
  function parts(date, utc) {
    const get = (name) => date[`get${utc ? "UTC" : ""}${name}`]();
    return { y: get("FullYear"), m: get("Month") + 1, d: get("Date"), h: get("Hours"), n: get("Minutes"), s: get("Seconds"), ms: get("Milliseconds") };
  }
  function wallDate(p, utc) {
    const date = new Date(0);
    if (utc) { date.setUTCFullYear(p.y, p.m - 1, p.d); date.setUTCHours(p.h, p.n, p.s, p.ms); }
    else { date.setFullYear(p.y, p.m - 1, p.d); date.setHours(p.h, p.n, p.s, p.ms); }
    return date;
  }
  function validateParts(p) {
    const check = wallDate(p, true);
    if (Object.entries(parts(check, true)).some(([key, value]) => p[key] !== value)) throw new Error("日期或时间无效，请检查月份、天数、时分秒");
  }
  function parseDate(text, mode = "auto", utc = false, now = Date.now()) {
    const input = text.trim();
    if (!input) return new Date(now);
    let date;
    if (mode === "objectid" || (mode === "auto" && /^[\da-f]{24}$/i.test(input))) {
      if (!/^[\da-f]{24}$/i.test(input)) throw new Error("Mongo ObjectID 应为 24 位十六进制字符串");
      date = new Date(parseInt(input.slice(0, 8), 16) * 1000);
    } else if (mode === "excel") {
      const serial = Number(input);
      if (!Number.isFinite(serial) || serial < 1 || serial >= 2958466) throw new Error("Excel 日期序列值应在 1 到 2958466 之间（1900 日期系统）");
      if (Math.floor(serial) === 60) throw new Error("Excel 序列 60 对应不存在的 1900-02-29");
      const wall = new Date(excelEpoch + Math.round((serial < 60 ? serial + 1 : serial) * dayMS));
      date = wallDate(parts(wall, true), utc);
    } else if (["seconds", "milliseconds"].includes(mode) || (mode === "auto" && /^[+-]?\d+(?:\.\d+)?$/.test(input))) {
      if (!/^[+-]?\d+(?:\.\d+)?$/.test(input)) throw new Error("时间戳必须是数字");
      const multiplier = mode === "seconds" ? 1000 : mode === "milliseconds" ? 1 : Math.abs(Number(input)) < 1e11 ? 1000 : 1;
      date = new Date(Number(input) * multiplier);
    } else {
      const match = /^(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{2}):(\d{2})(?::(\d{2})(?:\.(\d{1,3}))?)?(Z|[+-]\d{2}:?\d{2})?)?$/i.exec(input);
      if (match) {
        const p = { y: +match[1], m: +match[2], d: +match[3], h: +(match[4] || 0), n: +(match[5] || 0), s: +(match[6] || 0), ms: +(match[7] || "0").padEnd(3, "0") };
        validateParts(p);
        if (match[8]) {
          const zone = match[8];
          const hours = zone.toUpperCase() === "Z" ? 0 : +zone.slice(1, 3);
          const minutes = zone.toUpperCase() === "Z" ? 0 : +zone.slice(-2);
          if (hours > 23 || minutes > 59) throw new Error("时区偏移无效");
          const offset = (hours * 60 + minutes) * (zone[0] === "-" ? -1 : 1);
          date = new Date(wallDate(p, true).getTime() - offset * 60000);
        } else {
          date = wallDate(p, utc);
          if (Object.entries(parts(date, utc)).some(([key, value]) => p[key] !== value)) throw new Error("此本地时间受夏令时影响而不存在，请使用明确的时区偏移");
        }
      } else if (/^(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun),?\s/.test(input)) date = new Date(input);
      else throw new Error("请输入 ISO 日期、RFC 日期或数字时间戳，例如 2026-10-20 13:53:28");
    }
    if (!Number.isFinite(date.getTime())) throw new Error("日期超出支持的范围");
    return date;
  }

  function dateFormats(date, utc = false) {
    const p = parts(date, utc);
    const y = p.y >= 0 && p.y <= 9999 ? pad(p.y, 4) : `${p.y < 0 ? "-" : "+"}${pad(Math.abs(p.y), 6)}`;
    const calendar = `${y}-${pad(p.m)}-${pad(p.d)}`, time = `${pad(p.h)}:${pad(p.n)}:${pad(p.s)}`;
    const offsetMinutes = utc ? 0 : -date.getTimezoneOffset();
    const offset = `${offsetMinutes >= 0 ? "+" : "-"}${pad(Math.floor(Math.abs(offsetMinutes) / 60))}:${pad(Math.abs(offsetMinutes) % 60)}`;
    const iso = `${calendar}T${time}${p.ms ? "." + pad(p.ms, 3) : ""}${utc ? "Z" : offset}`;
    const seconds = Math.floor(date.getTime() / 1000);
    let excel = (wallDate(p, true).getTime() - excelEpoch) / dayMS;
    if (excel < 61) excel--;
    const objectid = seconds >= 0 && seconds <= 0xffffffff ? pad(seconds.toString(16), 8) + "0000000000000000" : "";
    return [
      ["JS locale date string", utc ? date.toLocaleString(undefined, { timeZone: "UTC", timeZoneName: "short" }) : date.toString()],
      ["ISO 8601", iso], ["ISO 9075", `${calendar} ${time}`], ["RFC 3339", iso],
      ["RFC 7231", date.toUTCString()], ["Unix timestamp · 秒", String(seconds)],
      ["Timestamp · 毫秒", String(date.getTime())], ["UTC format", date.toUTCString()],
      ["Mongo ObjectID · 时间前缀", objectid], ["Excel date/time · 1900", excel >= 1 && excel < 2958466 ? String(excel) : ""],
    ];
  }

  const api = { parseJSON, stringifyJSON, diffJSON, parseDate, dateFormats };
  root.FloeData = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})(globalThis);
