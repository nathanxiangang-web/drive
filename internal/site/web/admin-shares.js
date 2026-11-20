"use strict";

const adminSharePageSize = 50;
let adminShareOffset = 0;
let adminShareQuery = "";
let adminShareRequest = 0;
let editingShare = null;
const adminShareEditor = el("admin-share-editor");

function resetAdminShares() {
  adminShareRequest++;
  adminShareOffset = 0;
  adminShareQuery = "";
  editingShare = null;
  el("admin-share-filter").reset();
  el("admin-share-rows").replaceChildren();
  el("admin-share-status").textContent = "";
}

async function loadAdminShares() {
  const ticket = ++adminShareRequest;
  const status = el("admin-share-status");
  const prev = el("admin-share-prev");
  const next = el("admin-share-next");
  status.textContent = "正在读取分享记录…";
  prev.disabled = next.disabled = true;
  try {
    const page = await request(
      "/api/admin/shares?" +
        new URLSearchParams({
          q: adminShareQuery,
          offset: String(adminShareOffset),
        }),
    );
    if (ticket !== adminShareRequest) return;
    el("admin-share-rows").replaceChildren(...page.items.map(adminShareRow));
    el("admin-share-page").textContent =
      "第 " + (adminShareOffset / adminSharePageSize + 1) + " 页";
    prev.disabled = adminShareOffset === 0;
    next.disabled = !page.more;
    status.textContent = page.items.length ? "" : "没有符合条件的分享";
  } catch (error) {
    if (ticket === adminShareRequest) status.textContent = error.message;
  }
}

function adminShareRow(item) {
  const row = document.createElement("tr");
  const code = cell(item.code || "无");
  code.className = "share-code-value";
  const status = item.revoked ? "已取消" : item.active ? "有效" : "已失效";
  const controls = document.createElement("div");
  controls.className = "row-ops";
  if (item.active) {
    const link = document.createElement("a");
    link.href = shareLink(item.token);
    link.rel = "noreferrer";
    link.target = "_blank";
    link.textContent = "查看链接";
    controls.append(
      link,
      action(item.code ? "修改提取码" : "设置提取码", () =>
        editAdminShare(item),
      ),
    );
  }
  if (!item.revoked && new Date(item.expires) > new Date())
    controls.append(action("取消分享", () => cancelAdminShare(item)));
  row.append(
    cell(item.name),
    cell(item.login),
    code,
    cell(timestamp(item.expires)),
    cell(status),
    cell(controls),
  );
  return row;
}

function editAdminShare(item) {
  editingShare = item;
  el("admin-share-file").textContent = item.name;
  el("admin-share-code").value = item.code || "";
  el("admin-share-edit-status").textContent = "";
  el("admin-share-hint").textContent =
    "保存后，原链接仍可使用。请把新提取码发给接收者，他们需要重新验证。";
  adminShareEditor.showModal();
}

async function cancelAdminShare(item) {
  if (!window.confirm("取消这个分享？链接将失效。")) return;
  try {
    await request("/api/admin/shares/" + item.token, { method: "DELETE" });
    if (!state.admin) return;
    await loadAdminShares();
    if (state.admin) el("admin-share-status").textContent = "已取消分享";
  } catch (error) {
    if (state.admin) el("admin-share-status").textContent = error.message;
  }
}

el("admin-share-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const item = editingShare;
  if (!item) return;
  event.submitter.disabled = true;
  try {
    await request("/api/admin/shares/" + item.token, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ code: el("admin-share-code").value }),
    });
    if (!state.admin) return;
    adminShareEditor.close();
    await loadAdminShares();
    if (state.admin)
      el("admin-share-status").textContent = "提取码已保存，原链接仍可使用";
  } catch (error) {
    if (editingShare === item)
      el("admin-share-edit-status").textContent = error.message;
  } finally {
    event.submitter.disabled = false;
  }
});
el("admin-share-close").addEventListener("click", () =>
  adminShareEditor.close(),
);
adminShareEditor.addEventListener("close", () => {
  editingShare = null;
  el("admin-share-code").value = "";
});
el("admin-share-filter").addEventListener("submit", (event) => {
  event.preventDefault();
  adminShareOffset = 0;
  adminShareQuery = el("admin-share-query").value.trim();
  loadAdminShares();
});
el("admin-share-prev").addEventListener("click", () => {
  adminShareOffset -= adminSharePageSize;
  loadAdminShares();
});
el("admin-share-next").addEventListener("click", () => {
  adminShareOffset += adminSharePageSize;
  loadAdminShares();
});
