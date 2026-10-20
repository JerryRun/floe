"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const { createHmac } = require("node:crypto");
const codecs = require("./codecs.js");

test("Base64 preserves UTF-8, whitespace and both alphabets", () => {
  assert.equal(codecs.encodeBase64("中文😀"), "5Lit5paH8J+YgA==");
  assert.equal(codecs.decodeBase64("5Lit\n5paH8J+YgA=="), "中文😀");
  assert.equal(codecs.encodeBase64("中文😀", true), "5Lit5paH8J-YgA");
  assert.equal(codecs.decodeBase64("5Lit5paH8J-YgA", true), "中文😀");
  assert.equal(codecs.decodeBase64("YQ"), "a");
  assert.equal(codecs.decodeBase64(""), "");
  assert.equal(codecs.decodeBase64(codecs.encodeBase64("a\r\nb\t\0")), "a\r\nb\t\0");
});

test("Base64 rejects malformed padding, noncanonical bits and binary UTF-8", () => {
  for (const value of ["A", "YQ=", "Y=Q=", "YQ===", "YR==", "YQ!!", "8J-YgA"]) assert.throws(() => codecs.decodeBase64(value));
  assert.throws(() => codecs.decodeBase64("/w=="), /UTF-8/);
  assert.throws(() => codecs.decodeBase64("8J+YgA", true));
});

test("URL parameter and form encoding distinguish plus from space", () => {
  assert.equal(codecs.encodeURL("a +/中文"), "a%20%2B%2F%E4%B8%AD%E6%96%87");
  assert.equal(codecs.encodeURL("a +/中文", true), "a+%2B%2F%E4%B8%AD%E6%96%87");
  assert.equal(codecs.decodeURL("a+b%2Bc"), "a+b+c");
  assert.equal(codecs.decodeURL("a+b%2Bc", true), "a b+c");
  assert.equal(codecs.encodeURL("~!'()*", true), "%7E%21%27%28%29*");
  for (const invalid of ["%", "%GG", "%FF", "%E4%B8"]) assert.throws(() => codecs.decodeURL(invalid));
  assert.equal(codecs.decodeURL(""), "");
});

test("JWT parses and verifies the published jwt.io HS256 example", async () => {
  const token = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c";
  assert.equal(codecs.parseJWT(token).payload.name, "John Doe");
  assert.equal((await codecs.verifyJWT(token, "your-256-bit-secret", "HS256")).valid, true);
  assert.equal((await codecs.verifyJWT(token, "wrong", "HS256")).valid, false);
  await assert.rejects(codecs.verifyJWT(token, "your-256-bit-secret", "HS512"), /一致/);
});

test("HMAC JWT matches an independent implementation for all supported algorithms", async () => {
  for (const algorithm of ["HS256", "HS384", "HS512"]) {
    const token = await codecs.generateJWT(JSON.stringify({ alg: algorithm, typ: "JWT" }), '{"sub":"中文😀","exp":2000000000}', "测试密钥", algorithm);
    const [header, payload, signature] = token.split(".");
    const expected = createHmac(`sha${algorithm.slice(2)}`, "测试密钥").update(`${header}.${payload}`).digest("base64url");
    assert.equal(signature, expected);
    assert.equal((await codecs.verifyJWT(token, "测试密钥", algorithm)).valid, true);
    assert.equal(codecs.parseJWT(token).payload.sub, "中文😀");
    const tampered = `${header}.${Buffer.from('{"sub":"changed"}').toString("base64url")}.${signature}`;
    assert.equal((await codecs.verifyJWT(tampered, "测试密钥", algorithm)).valid, false);
  }
});

test("JWT rejects malformed JSON, unsafe algorithm changes and unsupported extensions", async () => {
  for (const token of ["", "a.b", "a.b.c.d.e", "a.b.c", "a=.b.c", "a. b.c"]) assert.throws(() => codecs.parseJWT(token));
  for (const [header, payload, secret, algorithm] of [
    ['{"alg":"none"}', "{}", "secret", "none"],
    ['{"alg":"HS256"}', "[]", "secret", "HS256"],
    ['{"alg":"HS256"}', "{", "secret", "HS256"],
    ['{"alg":"HS256"}', "{}", "", "HS256"],
    ['{"alg":"HS256","crit":["custom"]}', "{}", "secret", "HS256"],
    ['{"alg":"HS256","b64":false}', "{}", "secret", "HS256"],
  ]) await assert.rejects(codecs.generateJWT(header, payload, secret, algorithm));
  const unsigned = `${Buffer.from('{"alg":"none"}').toString("base64url")}.e30.`;
  assert.equal(codecs.parseJWT(unsigned).header.alg, "none");
  await assert.rejects(codecs.verifyJWT(unsigned, "secret", "none"));
});

test("JWT time declarations use seconds and flag expired, future and invalid values", () => {
  const claims = codecs.claims({ iat: 10, nbf: 101, exp: 100 }, 100);
  assert.equal(claims[0].warning, false);
  assert.equal(claims[1].warning, true);
  assert.equal(claims[2].warning, true);
  assert.equal(codecs.claims({ exp: "100" }, 100)[0].warning, true);
  assert.equal(codecs.claims({ exp: 1e300 })[0].warning, true);
});
