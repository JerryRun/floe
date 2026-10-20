"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const data = require("./workbench-data.js");

test("JSON formatting preserves numeric tokens, escaped strings and special object keys", () => {
  const raw = '{"z":900719925474099312345,"__proto__":{"safe":true},"a":1.2300,"big":1e999,"x":"\\n中文😀"}';
  const value = data.parseJSON(raw);
  assert.equal(data.stringifyJSON(value, 0), raw);
  assert.equal(data.stringifyJSON(data.parseJSON('"{\\"b\\":2,\\"a\\":1}"', true), 2, true), '{\n  "a": 1,\n  "b": 2\n}');
  assert.equal(data.stringifyJSON(data.parseJSON('"ordinary string"', true), 0), '"ordinary string"');
  assert.equal(data.stringifyJSON(data.parseJSON('[false,null,0,""]'), 0), '[false,null,0,""]');
  assert.equal(Object.prototype.safe, undefined);
});

test("JSON diagnostics reject trailing commas, malformed values and excessive nesting", () => {
  for (const input of ['{"a":}', '{a:1}', '[1,]', '{"a":1,}', '01', '1.', 'truefalse', 'NaN', '"unterminated', '"\\x20"', '"a\nb"', '', ' '.repeat(4)]) {
    assert.throws(() => data.parseJSON(input), /第 .*行、第 .*列/);
  }
  assert.throws(() => data.parseJSON('['.repeat(130) + '0' + ']'.repeat(130)), /128/);
  assert.throws(() => data.stringifyJSON({}, -1), /缩进/);
});

test("JSON diff matches screenshot and distinguishes missing, null, arrays and types", () => {
  const diff = (a, b) => data.diffJSON(data.parseJSON(a), data.parseJSON(b));
  assert.deepEqual(diff('{"a":1,"c":false}', '{"a":2,"b":1}').map(({path,kind}) => [path,kind]), [["$.a","changed"],["$.b","added"],["$.c","removed"]]);
  assert.deepEqual(diff('{"a":null}', '{}')[0], {path:"$.a",kind:"removed",before:"null",after:undefined});
  assert.equal(diff('[]','{}')[0].kind, "changed");
  assert.equal(diff('[1,2]','[1,2,3]')[2].path, "$[2]");
  assert.equal(diff('[1,2]','[1,2,3]')[2].kind, "added");
  assert.equal(diff('{"x.y":1}', '{"x.y":"1"}')[0].path, '$["x.y"]');
  assert.equal(diff('{"a":1,"b":2}', '{"b":2,"a":1}').every((row) => row.kind === 'same'), true);
  assert.equal(diff('9007199254740992','9007199254740993')[0].kind,'changed');
  for (const [a,b] of [['1','1.00e0'],['0','-0'],['123e999','1230e998']]) assert.equal(diff(a,b)[0].kind,'same');
});

test("Date conversion interprets seconds, milliseconds, ISO offsets and ObjectIDs", () => {
  const iso = '2026-10-20T05:53:28.219Z';
  assert.equal(data.parseDate('1792475608219','milliseconds',true).toISOString(),iso);
  assert.equal(data.parseDate('1792475608.219','seconds',true).toISOString(),iso);
  assert.equal(data.parseDate('2026-10-20T13:53:28.219+08:00','date',true).toISOString(),iso);
  assert.equal(data.parseDate('2026-10-20 05:53:28.219','date',true).toISOString(),iso);
  assert.equal(data.parseDate('1792475608','auto',true).toISOString(),'2026-10-20T05:53:28.000Z');
  assert.equal(data.parseDate('6ad701d80000000000000000','objectid',true).toISOString(),'2026-10-20T05:53:28.000Z');
  assert.equal(data.parseDate('', 'auto', true, 0).getTime(),0);
  assert.equal(data.parseDate('0001-01-01', 'date',true).getUTCFullYear(),1);
});

test("Date conversion rejects calendar normalization and invalid offsets", () => {
  for (const value of ['2026-02-29','2026-04-31','2026-13-01','2026-01-01T24:00:00','2026-01-01T00:00:00+25:00','2026-01-01T00:00:00+08:60','tomorrow']) assert.throws(()=>data.parseDate(value,'date',true));
  assert.throws(()=>data.parseDate('abc','seconds'));
  assert.throws(()=>data.parseDate('ffffffff','objectid'));
  assert.throws(()=>data.parseDate('999999999999999999','milliseconds'));
  assert.equal(data.parseDate('2024-02-29', 'date',true).toISOString(),'2024-02-29T00:00:00.000Z');
});

test("Date output covers all reference formats and Excel 1900 leap-year anomaly", () => {
  const date = data.parseDate('2026-10-20T05:53:28.219Z','date',true);
  const rows = Object.fromEntries(data.dateFormats(date,true));
  assert.equal(rows['ISO 8601'],'2026-10-20T05:53:28.219Z');
  assert.equal(rows['Unix timestamp · 秒'],'1792475608');
  assert.equal(rows['Timestamp · 毫秒'],'1792475608219');
  assert.equal(rows['Mongo ObjectID · 时间前缀'],'6ad701d80000000000000000');
  assert.equal(data.parseDate(rows['Excel date/time · 1900'],'excel',true).getTime(),date.getTime());
  for (const [serial,iso] of [['1','1900-01-01T00:00:00.000Z'],['59','1900-02-28T00:00:00.000Z'],['61','1900-03-01T00:00:00.000Z']]) {
    const parsed = data.parseDate(serial,'excel',true);
    assert.equal(parsed.toISOString(),iso);
    assert.equal(Object.fromEntries(data.dateFormats(parsed,true))['Excel date/time · 1900'],serial);
  }
  assert.throws(()=>data.parseDate('60','excel',true),/不存在/);
  const negative=Object.fromEntries(data.dateFormats(new Date(-1),true));
  assert.equal(negative['Unix timestamp · 秒'],'-1');
  assert.equal(negative['Mongo ObjectID · 时间前缀'],'');
});
