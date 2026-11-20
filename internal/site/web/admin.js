"use strict";

const roleNames = { admin: "管理员", member: "可云下载", reader: "只读" };
const stateNames = {
  account: "账号接入",
  token: "现有令牌",
  unconfigured: "待配置",
};

function capacityLabel(capacity) {
  if (!capacity) return "—";
  return size(capacity.available) + " 可用 / " + size(capacity.total);
}

function durationLabel(seconds) {
  if (!Number.isFinite(seconds) || seconds < 0) return "—";
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);
  return (
    (days ? days + " 天 " : "") +
    (hours % 24) +
    " 小时 " +
    (minutes % 60) +
    " 分钟"
  );
}

function renderStatus(snapshot) {
  const lastRead = new Date(snapshot.drive.time);
  const native =
    lastRead.getFullYear() > 2000
      ? (snapshot.drive.ok ? "读取成功 · " : "读取失败 · ") +
        lastRead.toLocaleTimeString()
      : "尚无读取记录";
  const values = [
    ["网站", "在线"],
    ["PostgreSQL", snapshot.database ? "在线" : "不可用"],
    ["115 目录", native],
    ["OpenList", snapshot.openlist ? "服务在线" : "服务不可用"],
    ["CPU 负载 (1/5/15 分钟)", snapshot.server.load || "—"],
    ["可见 CPU 核数", String(snapshot.server.cpus)],
    ["服务器内存", capacityLabel(snapshot.server.memory)],
    ["容器存储盘", capacityLabel(snapshot.server.disk)],
    ["服务器运行", durationLabel(snapshot.server.online_seconds)],
    ["应用运行", durationLabel(snapshot.server.app_seconds)],
  ];
  el("status-grid").replaceChildren(
    ...values.map(([name, value]) => {
      const card = document.createElement("div");
      const label = document.createElement("dt");
      const detail = document.createElement("dd");
      label.textContent = name;
      detail.textContent = value;
      card.append(label, detail);
      return card;
    }),
  );
  const connection = snapshot.connection;
  el("connection-state").textContent =
    stateNames[connection.mode] +
    (connection.username ? " · " + connection.username : "") +
    " · " +
    connection.root;
  const root = el("openlist-root");
  if (!root.value) root.value = connection.root;
  el("openlist-verify").disabled = !connection.editable;
  if (!connection.editable)
    el("openlist-message").textContent = "服务器未配置加密密钥。";
}

const editUser = el("edit-user");
let editingLogin = "";

function openUserEditor(user) {
  editingLogin = user.login;
  el("edit-user-login").value = user.login;
  el("edit-user-role").value = user.role;
  el("edit-user-enabled").checked = user.enabled;
  el("edit-user-password").value = "";
  el("edit-user-error").textContent = "";
  editUser.showModal();
}

function userEndpoint(login) {
  return "/api/admin/users/" + encodeURIComponent(login);
}

async function removeUser(user) {
  if (!window.confirm("删除 " + user.login + " 的网站账号？网盘文件会保留。"))
    return;
  try {
    await request(userEndpoint(user.login), { method: "DELETE" });
    el("manage-message").textContent = "已删除网站账号 " + user.login;
    await loadAdmin();
  } catch (error) {
    el("manage-message").textContent = error.message;
  }
}

function renderUsers(users) {
  const rows = users.map((user) => {
    const row = document.createElement("tr");
    const controls = document.createElement("div");
    controls.className = "row-ops";
    if (user.role === "admin") {
      controls.textContent = "管理员受保护";
    } else {
      controls.append(
        action("编辑", () => openUserEditor(user)),
        action("删除", () => removeUser(user)),
      );
    }
    row.append(
      cell(user.login),
      cell(roleNames[user.role]),
      cell(user.enabled ? "启用" : "停用"),
      cell(controls),
    );
    return row;
  });
  if (!rows.length) {
    const row = document.createElement("tr");
    const empty = cell("没有匹配的账号");
    empty.colSpan = 4;
    row.append(empty);
    rows.push(row);
  }
  el("user-rows").replaceChildren(...rows);
}

el("edit-user-cancel").addEventListener("click", () => editUser.close());
editUser.addEventListener("close", () => {
  el("edit-user-password").value = "";
});
el("edit-user-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = event.submitter;
  button.disabled = true;
  try {
    await request(userEndpoint(editingLogin), {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        login: el("edit-user-login").value.trim(),
        password: el("edit-user-password").value,
        role: el("edit-user-role").value,
        enabled: el("edit-user-enabled").checked,
      }),
    });
    editUser.close();
    el("manage-message").textContent = "账号已更新";
    await loadAdmin();
  } catch (error) {
    if (error.status === 401) {
      editUser.close();
      showLogin();
    } else {
      el("edit-user-error").textContent = error.message;
    }
  } finally {
    button.disabled = false;
  }
});

el("user-filter").addEventListener("submit", (event) => {
  event.preventDefault();
  loadAdmin();
});

async function loadAdmin() {
  if (state.view !== "admin") return;
  const ticket = ++state.request;
  const refresh = el("refresh-admin");
  refresh.disabled = true;
  try {
    const [status, users] = await Promise.all([
      request("/api/admin/status"),
      request(
        "/api/admin/users?" +
          new URLSearchParams({ q: el("user-query").value.trim() }),
      ),
    ]);
    if (ticket !== state.request) return;
    renderStatus(status);
    renderUsers(users);
  } catch (error) {
    if (ticket === state.request)
      el("manage-message").textContent = error.message;
  } finally {
    refresh.disabled = false;
  }
}

el("refresh-admin").addEventListener("click", loadAdmin);
el("new-user-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = el("create-user");
  const message = el("manage-message");
  button.disabled = true;
  try {
    await post("/api/admin/users", {
      login: el("new-user-login").value.trim(),
      password: el("new-user-password").value,
      role: el("new-user-role").value,
    });
    event.target.reset();
    message.textContent = "账号已创建";
    await loadAdmin();
  } catch (error) {
    message.textContent = error.message;
  } finally {
    button.disabled = false;
  }
});
el("openlist-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = el("openlist-verify");
  const message = el("openlist-message");
  button.disabled = true;
  message.textContent = "正在验证接入…";
  try {
    await request("/api/admin/openlist", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        username: el("openlist-user").value.trim(),
        password: el("openlist-password").value,
        root: el("openlist-root").value.trim(),
      }),
    });
    message.textContent = "验证通过，新接入已生效";
    await loadAdmin();
  } catch (error) {
    message.textContent = error.message;
  } finally {
    el("openlist-password").value = "";
    button.disabled = false;
  }
});

function selectAdminPanel(name) {
  for (const pane of document.querySelectorAll("[data-admin-panel]"))
    pane.hidden = pane.dataset.adminPanel !== name;
  for (const button of el("admin-tabs").querySelectorAll("button"))
    button.setAttribute("aria-pressed", String(button.dataset.panel === name));
  if (name === "shares") loadAdminShares();
}
el("admin-tabs").addEventListener("click", (event) => {
  if (event.target.dataset.panel) selectAdminPanel(event.target.dataset.panel);
});
