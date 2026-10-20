const FloeDataTools = (() => {
  let active = "", timer = 0, sequence = 0, request, diffRows = [], diffValid = false;
  let dateTimer = 0, dateFields = [];
  const labels = { added: "新增", removed: "删除", changed: "修改", same: "相同" };
  function status(text = "", kind = "") {
    $("#dataStatus").textContent = text;
    $("#dataStatus").dataset.kind = kind;
  }
  function cancel() { clearTimeout(timer); sequence++; request?.abort(); request = null; }
  function show(tool) {
    if (active === tool) return;
    cancel(); active = tool; status();
    syncDateRefresh();
    for (const [name, id] of [["datetime", "datetimeTool"], ["json", "jsonTool"], ["diff", "diffTool"]]) $(`#${id}`).hidden = tool !== name;
    if (tool === "datetime") convertDate();
    if (tool === "json" && $("#jsonInput").value.trim()) formatJSON();
    if (tool === "diff" && $("#diffLeft").value.trim() && $("#diffRight").value.trim()) compare();
  }
  function syncDateRefresh() {
    cancelAnimationFrame(dateTimer); dateTimer = 0;
    if (active === "datetime" && !$("#dateInput").value.trim() && !document.hidden) dateTimer = requestAnimationFrame(refreshDate);
  }
  function refreshDate() {
    if (!$("#toolboxPanel").hidden && !$("#transferQueue").classList.contains("collapsed")) convertDate();
    dateTimer = requestAnimationFrame(refreshDate);
  }
  function copyButton(field, label = "复制") {
    const button = document.createElement("button");
    button.type = "button"; button.textContent = label;
    button.addEventListener("click", () => copyText(field.value, "结果已复制"));
    return button;
  }
  function convertDate() {
    status();
    try {
      const utc = $("#dateZone").value === "utc";
      const date = FloeData.parseDate($("#dateInput").value, $("#dateMode").value, utc);
      const context = `${utc ? "UTC" : Intl.DateTimeFormat().resolvedOptions().timeZone} · ${$("#dateInput").value.trim() ? "转换结果" : "当前时间 · 实时刷新"}`;
      if ($("#dateContext").textContent !== context) $("#dateContext").textContent = context;
      const formats = FloeData.dateFormats(date, utc);
      if (!dateFields.length) {
        $("#dateResults").replaceChildren(...formats.map(([label]) => {
          const row = document.createElement("div"); row.className = "date-result";
          const name = document.createElement("span"); name.textContent = label;
          const field = document.createElement("input"); field.readOnly = true; field.placeholder = "超出此格式的有效范围"; field.setAttribute("aria-label", label);
          const button = copyButton(field);
          dateFields.push({ field, button });
          row.append(name, field, button); return row;
        }));
      }
      formats.forEach(([, value], index) => {
        const { field, button } = dateFields[index];
        if (field.value !== value) field.value = value;
        button.disabled = !value;
      });
    } catch (error) {
      dateFields = []; $("#dateResults").replaceChildren();
      $("#dateContext").textContent = ""; status(error.message, "error");
    }
  }
  async function formatJSON(compact = false) {
    cancel(); const current = sequence;
    $("#jsonOutput").value = ""; status();
    if (!$("#jsonInput").value.trim()) return;
    try {
      const input = FloeData.parseJSON($("#jsonInput").value, $("#jsonUnwrap").checked);
      const indent = compact ? 0 : Number($("#jsonIndent").value);
      if (!Number.isInteger(indent) || indent < 0 || indent > 8) throw new Error("缩进应为 0 到 8 的整数");
      const query = $("#jsonQuery").value.trim();
      let results = [input];
      if (query && query !== ".") {
        status("正在执行 jq…");
        request = new AbortController();
        const response = await api("/api/v1/tools/jq", { method: "POST", body: `{"input":${FloeData.stringifyJSON(input, 0)},"query":${JSON.stringify(query)}}`, signal: request.signal });
        if (current !== sequence) return;
        // Preserve numeric tokens by parsing the result text ourselves, rather
        // than allowing Response.json to round large integers.
        results = response.results.map((text) => FloeData.parseJSON(text));
      }
      if (current !== sequence) return;
      $("#jsonOutput").value = results.map((value) => FloeData.stringifyJSON(value, indent, $("#jsonSort").checked)).join("\n");
      status(results.length ? `${query && query !== "." ? "过滤" : "格式化"}完成 · ${results.length} 项结果` : "过滤完成 · 无匹配结果", "success");
    } catch (error) {
      if (current === sequence && error.name !== "AbortError") status(`${error.message}${error.payload?.detail ? "：" + error.payload.detail : ""}`, "error");
    } finally { if (current === sequence) request = null; }
  }
  function renderDiff() {
    const visible = diffRows.filter((row) => !$("#diffOnly").checked || row.kind !== "same");
    const counts = { added: 0, removed: 0, changed: 0 };
    diffRows.forEach((row) => { if (row.kind !== "same") counts[row.kind]++; });
    $("#diffSummary").textContent = diffValid ? `新增 ${counts.added} · 删除 ${counts.removed} · 修改 ${counts.changed}` : "";
    const body = $("#diffResults"); body.replaceChildren();
    if (!diffValid) return;
    for (const row of visible) {
      const tr = document.createElement("tr"); tr.className = `diff-${row.kind}`;
      const path = document.createElement("td");
      const badge = document.createElement("span"); badge.className = "diff-kind"; badge.textContent = labels[row.kind];
      const code = document.createElement("code"); code.textContent = row.path; path.append(badge, code);
      tr.append(path);
      for (const value of [row.before, row.after]) {
        const td = document.createElement("td"), pre = document.createElement("pre");
        pre.textContent = value === undefined ? "（不存在）" : value;
        td.append(pre); tr.append(td);
      }
      body.append(tr);
    }
    if (!visible.length) {
      const tr = document.createElement("tr"), td = document.createElement("td"); td.colSpan = 3;
      td.className = "diff-empty"; td.textContent = "两个 JSON 的内容相同"; tr.append(td); body.append(tr);
    }
  }
  function compare() {
    diffRows = []; diffValid = false; status(); renderDiff();
    if (!$("#diffLeft").value.trim() || !$("#diffRight").value.trim()) return;
    try {
      const left = FloeData.parseJSON($("#diffLeft").value, false, "原始 JSON");
      const right = FloeData.parseJSON($("#diffRight").value, false, "比较 JSON");
      diffRows = FloeData.diffJSON(left, right); diffValid = true; renderDiff();
      status("比较完成", "success");
    } catch (error) { status(error.message, "error"); }
  }
  function schedule(action) {
    cancel(); status();
    if (active === "json") $("#jsonOutput").value = "";
    if (active === "diff") { diffValid = false; diffRows = []; renderDiff(); }
    timer = setTimeout(action, 300);
  }
  function clear() {
    cancel(); status();
    if (active === "datetime") { $("#dateInput").value = ""; convertDate(); syncDateRefresh(); }
    if (active === "json") { $("#jsonInput").value = $("#jsonOutput").value = $("#jsonQuery").value = ""; }
    if (active === "diff") { $("#diffLeft").value = $("#diffRight").value = ""; diffRows = []; diffValid = false; renderDiff(); }
  }
  $("#dateInput").addEventListener("input", () => { cancel(); convertDate(); syncDateRefresh(); });
  for (const id of ["dateMode", "dateZone"]) $(`#${id}`).addEventListener("change", convertDate);
  $("#dateNow").addEventListener("click", () => { cancel(); $("#dateInput").value = ""; convertDate(); syncDateRefresh(); });
  document.addEventListener("visibilitychange", () => {
    if (active === "datetime" && !document.hidden && !$("#dateInput").value.trim()) convertDate();
    syncDateRefresh();
  });
  $("#jsonFormat").addEventListener("click", () => formatJSON());
  $("#jsonCompact").addEventListener("click", () => formatJSON(true));
  for (const id of ["jsonInput", "jsonQuery", "jsonIndent"]) $(`#${id}`).addEventListener("input", () => schedule(formatJSON));
  for (const id of ["jsonSort", "jsonUnwrap"]) $(`#${id}`).addEventListener("change", () => formatJSON());
  $("#jsonCopy").addEventListener("click", () => { if ($("#jsonOutput").value) copyText($("#jsonOutput").value, "JSON 结果已复制"); });
  $("#jsonQueryExample").addEventListener("click", () => {
    $("#jsonInput").value = '{"items":[{"name":"Floe","active":true},{"name":"Other","active":false}]}';
    $("#jsonQuery").value = ".items[] | select(.active == true) | .name"; formatJSON();
  });
  $("#diffCompare").addEventListener("click", () => { cancel(); compare(); });
  for (const id of ["diffLeft", "diffRight"]) $(`#${id}`).addEventListener("input", () => schedule(compare));
  $("#diffOnly").addEventListener("change", renderDiff);
  $("#diffSwap").addEventListener("click", () => { cancel(); const left = $("#diffLeft").value; $("#diffLeft").value = $("#diffRight").value; $("#diffRight").value = left; compare(); });
  $("#diffCopy").addEventListener("click", () => {
    if (!diffValid) return;
    const rows = diffRows.filter((row) => !$("#diffOnly").checked || row.kind !== "same");
    copyText(JSON.stringify(rows, null, 2), "比较结果已复制");
  });
  return { show, clear };
})();
