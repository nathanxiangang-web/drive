"use strict";

const shareDialog = el("share-dialog");
let sharing = null;

function shareLink(token) {
  return location.origin + "/s/" + token;
}

async function presetShareCode() {
  const current = sharing;
  const input = el("share-code");
  const before = input.value;
  const button = el("share-randomize");
  button.disabled = true;
  try {
    const { code } = await request("/api/shares/code");
    if (sharing === current && input.value === before) input.value = code;
  } catch {
    if (sharing === current)
      el("share-status").textContent = "无法随机生成，可手动填写提取码。";
  } finally {
    button.disabled = false;
  }
}

function showShare(item, records = false) {
  const query = Object.fromEntries(
    new URL(fileURL(item), location.origin).searchParams,
  );
  sharing = { item, query };
  el("share-name").textContent = item.name;
  const parent = visibleParts(item.dir ?? state.directory).join(" / ");
  el("share-location").textContent = [titles[state.view], parent]
    .filter(Boolean)
    .join(" / ");
  el("share-status").textContent = "";
  el("share-result").hidden = true;
  el("share-setup").hidden = records;
  el("share-code").value = "";
  el("share-records").hidden = !records;
  el("share-record-list").replaceChildren();
  shareDialog.showModal();
  if (records) loadShareRecords();
  else {
    el("share-days").value = "7";
    presetShareCode();
  }
}

async function copyShare(url, code, button) {
  try {
    await navigator.clipboard.writeText(code ? url + "\n提取码：" + code : url);
    button.textContent = "已复制";
  } catch {
    if (!el("share-result").hidden && el("share-url").value === url) {
      el("share-url").focus();
      el("share-url").select();
    }
    el("share-status").textContent = "无法自动复制，可手动复制链接和提取码。";
  }
}

async function loadShareRecords() {
  const current = sharing;
  if (!current) return;
  el("share-records").hidden = false;
  try {
    const records = await request(
      "/api/shares?" + new URLSearchParams({ file: current.item.id }),
    );
    if (sharing !== current) return;
    const rows = records.map((record) => {
      const row = document.createElement("li");
      const text = document.createElement("span");
      const link = document.createElement("a");
      const live = !record.revoked && new Date(record.expires) > new Date();
      text.textContent = record.revoked
        ? "已撤销"
        : live
          ? "有效至 " + timestamp(record.expires)
          : "已到期";
      row.append(text);
      if (live) {
        if (record.code) text.textContent += " · 提取码 " + record.code;
        link.href = shareLink(record.token);
        link.target = "_blank";
        link.rel = "noreferrer";
        link.textContent = "打开";
        const copy = action("复制", () =>
          copyShare(link.href, record.code, copy),
        );
        row.append(
          link,
          copy,
          action("撤销分享", async () => {
            if (
              !window.confirm(
                "撤销这个分享链接？持有链接的人将失去通过该链接访问文件的权限。",
              )
            )
              return;
            try {
              await request("/api/shares/" + record.token, {
                method: "DELETE",
              });
              if (sharing !== current) return;
              el("share-status").textContent = "分享已撤销";
              el("share-result").hidden = true;
              await loadShareRecords();
            } catch (error) {
              el("share-status").textContent = error.message;
            }
          }),
        );
      }
      return row;
    });
    if (rows.length === 0) {
      const empty = document.createElement("li");
      empty.textContent = "暂无分享记录";
      rows.push(empty);
    }
    el("share-record-list").replaceChildren(...rows);
  } catch (error) {
    if (sharing === current) el("share-status").textContent = error.message;
  }
}

el("share-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const current = sharing;
  if (!current) return;
  const submit = event.submitter;
  submit.disabled = true;
  el("share-status").textContent = "正在创建链接…";
  try {
    const share = await post("/api/shares", {
      ...current.query,
      days: Number(el("share-days").value),
      code: el("share-code").value,
    });
    if (sharing !== current) return;
    const url = shareLink(share.token);
    el("share-url").value = url;
    el("share-code-value").textContent = share.code;
    el("share-open").href = url;
    el("share-expiration").textContent = "有效期至 " + timestamp(share.expires);
    el("share-copy").textContent = "复制链接和提取码";
    el("share-result").hidden = false;
    el("share-setup").hidden = true;
    el("share-status").textContent = "链接已创建";
    el("share-copy").focus();
    await loadShareRecords();
  } catch (error) {
    if (sharing === current) el("share-status").textContent = error.message;
  } finally {
    submit.disabled = false;
  }
});

el("share-copy").addEventListener("click", () =>
  copyShare(
    el("share-url").value,
    el("share-code-value").textContent,
    el("share-copy"),
  ),
);
el("share-randomize").addEventListener("click", presetShareCode);
el("share-close").addEventListener("click", () => shareDialog.close());
el("share-cancel").addEventListener("click", () => shareDialog.close());
shareDialog.addEventListener("close", () => {
  sharing = null;
  el("share-url").value = "";
  el("share-code").value = "";
  el("share-code-value").textContent = "";
  el("share-open").removeAttribute("href");
  el("share-record-list").replaceChildren();
});
