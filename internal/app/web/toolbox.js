const FloeToolbox = (() => {
  const titles = { jwt: "JWT 解析与生成", base64: "Base64 编码与解码", url: "URL 编码与解码", datetime: "日期时间转换", json: "JSON 解析与 jq 过滤", diff: "JSON 差异比较" };
  const drafts = new Map();
  const fields = ["toolInput", "toolVariant", "jwtMode", "jwtAlgorithm", "jwtSecret", "jwtHeader", "jwtPayload", "toolOutput"];
  let tool = "jwt";
  let operation = 0;

  function status(message = "", kind = "") {
    $("#toolStatus").textContent = message;
    $("#toolStatus").dataset.kind = kind;
  }

  function update() {
    const dataTool = ["datetime", "json", "diff"].includes(tool);
    const jwt = tool === "jwt";
    const generate = jwt && $("#jwtMode").value === "generate";
    $("#toolTitle").textContent = titles[tool];
    $("#jwtMode").hidden = !jwt;
    $("#toolVariant").hidden = jwt || dataTool;
    $("#codecColumns").hidden = dataTool;
    $("#dataTools").hidden = !dataTool;
    $("#toolHelp").hidden = dataTool;
    FloeDataTools.show(tool);
    $("#toolInputField").hidden = generate;
    $("#jwtGenerateFields").hidden = !generate;
    $("#jwtSecretRow").hidden = !jwt;
    $("#toolDecode").hidden = jwt;
    $("#jwtVerify").hidden = !jwt || generate;
    $("#toolRun").textContent = jwt ? (generate ? "生成 JWT" : "解析") : "编码";
    $("#toolOutputLabel").textContent = jwt ? (generate ? "JWT" : "解析结果") : "转换结果";
    $("#toolUseOutput").hidden = jwt && !generate;
    $("#toolInput").setAttribute("aria-label", jwt ? "JWT Token" : "待转换文本");
    $("#toolInput").placeholder = jwt ? "粘贴 JWT：eyJ… . eyJ… . …" : "输入待转换的文本";
    $("#toolHelp").textContent = jwt
      ? (generate ? "Header 和 Payload 使用 JSON 对象；密钥按 UTF-8 文本处理。" : "解析可查看任意 JWT 的内容；选择预期算法并填写密钥后可验证 HMAC 签名。")
      : tool === "base64" ? "按 UTF-8 转换文本，支持中文和 Emoji；Base64URL 使用 -、_，编码结果省略末尾填充。"
      : $("#toolVariant").value === "form" ? "表单参数值模式：空格编码为 +，字面量 + 编码为 %2B；解码时 + 表示空格。"
      : "URL 参数值模式：空格编码为 %20，解码时保留字面量 +。请输入单个参数值。";
    $$('[data-tool]').forEach((button) => {
      const selected = button.dataset.tool === tool;
      button.classList.toggle("active", selected);
      button.setAttribute("aria-pressed", String(selected));
    });
  }

  function invalidate() {
    operation++;
    $("#toolOutput").value = "";
    $("#jwtClaims").replaceChildren();
    $("#jwtClaims").hidden = true;
    status();
    setBusy(false);
  }

  function select(next) {
    if (next === tool) return;
    const draft = Object.fromEntries(fields.map((id) => [id, $(`#${id}`).value]));
    draft.status = $("#toolStatus").textContent;
    draft.kind = $("#toolStatus").dataset.kind;
    draft.claims = [...$("#jwtClaims").children].map((row) => row.textContent).join("\n") || $("#jwtClaims").textContent;
    drafts.set(tool, draft);
    operation++;
    tool = next;
    const options = tool === "base64" ? [["standard", "标准 Base64"], ["url", "Base64URL"]] : [["component", "URL 参数值（%20）"], ["form", "表单参数值（+）"]];
    $("#toolVariant").replaceChildren(...options.map(([value, label]) => {
      const option = document.createElement("option");
      option.value = value; option.textContent = label;
      return option;
    }));
    const saved = drafts.get(tool);
    fields.forEach((id) => {
      const fallback = id === "jwtMode" ? "parse" : id === "jwtAlgorithm" ? "HS256" : id === "jwtHeader" ? '{"alg":"HS256","typ":"JWT"}' : id === "toolVariant" ? options[0][0] : "";
      $(`#${id}`).value = saved?.[id] ?? fallback;
    });
    status(saved?.status || "", saved?.kind || "");
    $("#jwtClaims").textContent = saved?.claims || "";
    $("#jwtClaims").hidden = tool !== "jwt" || !saved?.claims;
    setBusy(false);
    update();
  }

  function setBusy(busy) {
    for (const id of ["toolRun", "toolDecode", "jwtVerify"]) $(`#${id}`).disabled = busy;
    $("#toolboxPanel").setAttribute("aria-busy", String(busy));
  }

  function showClaims(payload) {
    const claims = FloeCodecs.claims(payload);
    $("#jwtClaims").replaceChildren(...claims.map((claim) => {
      const row = document.createElement("div");
      row.className = claim.warning ? "claim-warning" : "";
      row.textContent = `${claim.key} · ${claim.label}：${claim.value}`;
      return row;
    }));
    $("#jwtClaims").hidden = !claims.length;
  }

  async function run(action) {
    const sequence = ++operation;
    setBusy(true);
    $("#toolOutput").value = "";
    $("#jwtClaims").hidden = true;
    status("正在处理…");
    try {
      const input = $("#toolInput").value;
      let output, message = "转换完成", kind = "success", parsed;
      if (tool === "base64") {
        output = FloeCodecs[action === "decode" ? "decodeBase64" : "encodeBase64"](input, $("#toolVariant").value === "url");
      } else if (tool === "url") {
        output = FloeCodecs[action === "decode" ? "decodeURL" : "encodeURL"](input, $("#toolVariant").value === "form");
      } else if ($("#jwtMode").value === "generate") {
        output = await FloeCodecs.generateJWT($("#jwtHeader").value, $("#jwtPayload").value, $("#jwtSecret").value, $("#jwtAlgorithm").value);
        message = "JWT 已生成";
      } else {
        parsed = action === "verify" ? await FloeCodecs.verifyJWT(input, $("#jwtSecret").value, $("#jwtAlgorithm").value) : FloeCodecs.parseJWT(input);
        output = JSON.stringify({ header: parsed.header, payload: parsed.payload }, null, 2);
        message = action === "verify" ? (parsed.valid ? "签名验证通过" : "签名验证失败，请检查 Token、算法和密钥") : "解析成功 · 签名尚未验证";
        kind = action === "verify" ? (parsed.valid ? "success" : "error") : "info";
        if (FloeCodecs.claims(parsed.payload).some((claim) => claim.warning)) { message += " · 时间声明异常，见上方"; if (kind !== "error") kind = "warning"; }
      }
      if (sequence !== operation) return;
      $("#toolOutput").value = output;
      if (parsed) showClaims(parsed.payload);
      status(message, kind);
    } catch (error) {
      if (sequence === operation) status(error.message, "error");
    } finally { if (sequence === operation) setBusy(false); }
  }

  function fullscreen(force) {
    const queue = $("#transferQueue");
    const enabled = force ?? !queue.classList.contains("toolbox-fullscreen");
    queue.classList.toggle("toolbox-fullscreen", enabled);
    $("#toolboxFullscreen .material-symbols-rounded").textContent = enabled ? "close" : "fullscreen";
    $("#toolboxFullscreen").title = enabled ? "退出全屏" : "展开工具箱";
    $("#toolboxFullscreen").setAttribute("aria-label", enabled ? "退出全屏" : "展开工具箱");
  }

  function activate() {
    toggleMemoryFullscreen(false);
    state.taskFilter = "toolbox";
    $("#transferQueue").classList.remove("collapsed");
    const mainArea = $(".main-area");
    const height = parseInt(getComputedStyle(mainArea).getPropertyValue("--queue-height"), 10) || 0;
    if (height < 320) mainArea.style.setProperty("--queue-height", "360px");
    renderTaskList();
  }

  $$('[data-tool]').forEach((button) => button.addEventListener("click", () => select(button.dataset.tool)));
  $("#toolRun").addEventListener("click", () => run("encode"));
  $("#toolDecode").addEventListener("click", () => run("decode"));
  $("#jwtVerify").addEventListener("click", () => run("verify"));
  $("#toolCopy").addEventListener("click", () => { if ($("#toolOutput").value) copyText($("#toolOutput").value, "结果已复制"); });
  $("#toolUseOutput").addEventListener("click", () => {
    const output = $("#toolOutput").value;
    if (!output) return;
    if (tool === "jwt") $("#jwtMode").value = "parse";
    $("#toolInput").value = output;
    invalidate(); update();
  });
  $("#toolClear").addEventListener("click", () => {
    if (["datetime", "json", "diff"].includes(tool)) { FloeDataTools.clear(); return; }
    for (const id of ["toolInput", "jwtPayload", "jwtSecret"]) $(`#${id}`).value = "";
    $("#jwtHeader").value = JSON.stringify({ alg: $("#jwtAlgorithm").value, typ: "JWT" });
    drafts.delete(tool);
    invalidate();
  });
  fields.filter((id) => id !== "toolOutput").forEach((id) => $(`#${id}`).addEventListener(id === "toolVariant" || id === "jwtMode" || id === "jwtAlgorithm" ? "change" : "input", () => {
    if (id === "jwtAlgorithm") {
      try { const header = JSON.parse($("#jwtHeader").value); if (header && typeof header === "object" && !Array.isArray(header)) { header.alg = $("#jwtAlgorithm").value; $("#jwtHeader").value = JSON.stringify(header, null, 2); } } catch (_) { /* Preserve an unfinished draft. */ }
    }
    invalidate(); update();
  }));
  $("#toolboxFullscreen").addEventListener("click", () => fullscreen());
  update();
  return { activate, fullscreen };
})();
