// Larry dashboard client. Vanilla JS, no framework, no build step.
// Connects to the server's /ws, parses Snapshot messages, and repaints.
(function () {
  "use strict";

  var $ = function (id) { return document.getElementById(id); };
  var serverBody = $("serverBody");
  var meshBody = $("meshBody");
  var onlinePill = $("onlinePill");
  var updated = $("updated");
  var meshHint = $("meshHint");

  var addModal = $("addModal");
  var manageModal = $("manageModal");
  var nodeNameInput = $("nodeNameInput");
  var addResult = $("addResult");
  var cmdResult = $("cmdResult");
  var tokenTableBody = $("tokenTableBody");

  window.closeModals = function () {
    if (addModal) addModal.classList.remove("open");
    if (manageModal) manageModal.classList.remove("open");
  };

  if ($("btnAddNode")) {
    $("btnAddNode").onclick = function () {
      nodeNameInput.value = "";
      addResult.style.display = "none";
      addModal.classList.add("open");
    };
  }

  if ($("btnManage")) {
    $("btnManage").onclick = function () {
      loadTokenList();
      manageModal.classList.add("open");
    };
  }

  if ($("btnSubmitAdd")) {
    $("btnSubmitAdd").onclick = function () {
      var name = (nodeNameInput.value || "").trim();
      if (!name) { alert("请输入节点名称！"); return; }
      fetch("/api/tokens", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: name })
      })
      .then(function (r) { return r.json(); })
      .then(function (res) {
        if (res.error) { alert("失败: " + res.error); return; }
        var host = location.host;
        var proto = location.protocol === "https:" ? "wss:" : "ws:";
        var cmd = "./larry-agent --server " + proto + "//" + host + " --token " + res.token + " --name " + res.name;
        cmdResult.textContent = cmd;
        addResult.style.display = "block";
      })
      .catch(function (err) { alert("请求失败: " + err); });
    };
  }

  if ($("btnCopyCmd")) {
    $("btnCopyCmd").onclick = function () {
      var text = cmdResult.textContent;
      navigator.clipboard.writeText(text).then(function () {
        alert("命令已复制到剪贴板！");
      }).catch(function () {
        alert("复制失败，请手动选择复制代码。");
      });
    };
  }

  function loadTokenList() {
    tokenTableBody.innerHTML = '<tr><td colspan="4" class="empty">加载中…</td></tr>';
    fetch("/api/tokens")
      .then(function (r) { return r.json(); })
      .then(function (list) {
        if (!list || list.length === 0) {
          tokenTableBody.innerHTML = '<tr><td colspan="4" class="empty">暂无注册节点</td></tr>';
          return;
        }
        var html = list.map(function (item) {
          var shortTok = item.token ? item.token.substring(0, 10) + "…" : "—";
          return '<tr>' +
            '<td>#' + item.id + '</td>' +
            '<td><strong>' + escapeHtml(item.name) + '</strong></td>' +
            '<td><code style="color:var(--muted)">' + shortTok + '</code></td>' +
            '<td><button class="btn danger" onclick="deleteToken(' + item.id + ')">删除</button></td>' +
            '</tr>';
        }).join("");
        tokenTableBody.innerHTML = html;
      })
      .catch(function () {
        tokenTableBody.innerHTML = '<tr><td colspan="4" class="empty">加载失败</td></tr>';
      });
  }

  window.deleteToken = function (id) {
    if (!confirm("确定要删除该节点 Token 吗？节点断开后将无法自动重连。")) return;
    fetch("/api/tokens?id=" + id, { method: "DELETE" })
      .then(function (r) { return r.json(); })
      .then(function () {
        loadTokenList();
      })
      .catch(function (err) { alert("删除失败: " + err); });
  };

  function fmtBytes(n) {
    if (!n || n < 0) return "0 B";
    var u = ["B", "KB", "MB", "GB", "TB", "PB"];
    var i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (n >= 100 ? n.toFixed(0) : n.toFixed(1)) + " " + u[i];
  }

  function fmtRate(n) {
    return fmtBytes(n) + "/s";
  }

  function fmtUptime(sec) {
    if (!sec) return "—";
    var d = Math.floor(sec / 86400);
    var h = Math.floor((sec % 86400) / 3600);
    var m = Math.floor((sec % 3600) / 60);
    if (d > 0) return d + "d " + h + "h";
    if (h > 0) return h + "h " + m + "m";
    return m + "m";
  }

  function bar(pct, warn, crit) {
    pct = Math.max(0, Math.min(100, pct || 0));
    var cls = "bar";
    if (pct >= crit) cls += " crit";
    else if (pct >= warn) cls += " warn";
    return '<span class="' + cls + '"><i style="width:' + pct + '%"></i></span>';
  }

  function pct(used, total) {
    if (!total) return 0;
    return (used / total) * 100;
  }

  function escapeHtml(s) {
    if (!s) return "";
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  function renderServers(agents) {
    if (!agents || agents.length === 0) {
      serverBody.innerHTML = '<tr><td colspan="11" class="empty">还没有 Agent 连接</td></tr>';
      return;
    }
    agents.sort(function (a, b) { return a.id - b.id; });
    var rows = agents.map(function (a) {
      var os = (a.os || "") + (a.arch ? "/" + a.arch : "");
      var memP = pct(a.mem_used, a.mem_total);
      var disks = (a.disks || []).map(function (d) {
        var p = pct(d.used, d.total);
        return p.toFixed(0) + "% " + escapeHtml(d.mount);
      }).join("<br>");
      var cls = a.online ? "" : "offline";
      var dot = '<span class="dot' + (a.online ? "" : " off") + '"></span>';
      return '<tr class="' + cls + '">' +
        "<td>" + dot + (a.online ? "在线" : "离线") + "</td>" +
        "<td><strong>" + escapeHtml(a.name || a.hostname || ("#" + a.id)) + "</strong><br><span style='color:var(--muted);font-size:11px'>" + escapeHtml(a.remote_addr || "") + "</span></td>" +
        "<td>" + escapeHtml(os) + "</td>" +
        "<td>" + (a.cpu || 0).toFixed(1) + "% " + bar(a.cpu, 70, 90) + "</td>" +
        "<td>" + memP.toFixed(0) + "% " + bar(memP, 70, 90) + "<br><span style='color:var(--muted);font-size:11px'>" + fmtBytes(a.mem_used) + " / " + fmtBytes(a.mem_total) + "</span></td>" +
        "<td><span style='font-size:12px'>" + (disks || "—") + "</span></td>" +
        "<td style='font-size:12px'>" + fmtRate(a.net_in) + "<br>" + fmtRate(a.net_out) + "</td>" +
        "<td>" + (a.load1 || 0).toFixed(2) + "</td>" +
        "<td>" + (a.conns || 0) + "</td>" +
        "<td>" + (a.procs || 0) + "</td>" +
        "<td>" + fmtUptime(a.uptime) + "</td>" +
        "</tr>";
    });
    serverBody.innerHTML = rows.join("");
  }

  function heatColor(ms) {
    if (ms < 0) return "#3d2a2a";
    if (ms === 0) return "#1c2230";
    if (ms < 20) return "#1a3a2a";
    if (ms < 50) return "#2a4a1a";
    if (ms < 100) return "#4a3a1a";
    if (ms < 200) return "#4a2a1a";
    return "#4a1a1a";
  }

  function renderMesh(agents, mesh) {
    var online = (agents || []).filter(function (a) { return a.online; });
    if (online.length < 2) {
      meshBody.innerHTML = '<tr><td class="empty">至少需要 2 个在线 Agent 才能生成延迟网格</td></tr>';
      meshHint.textContent = "— 至少需要 2 个在线 Agent";
      return;
    }
    online.sort(function (a, b) { return a.id - b.id; });
    meshHint.textContent = "— 两两 TCP 探测延迟 (ms)";

    var html = '<tr><td class="cell na" style="background:transparent"></td>';
    online.forEach(function (a) {
      html += '<th><div class="lbl" title="' + escapeHtml(a.name) + '">' + escapeHtml(a.name) + '</div></th>';
    });
    html += "</tr>";

    online.forEach(function (from) {
      var row = mesh[from.id] || {};
      html += '<tr><th><div class="lbl" title="' + escapeHtml(from.name) + '">' + escapeHtml(from.name) + '</div></th>';
      online.forEach(function (to) {
        if (from.id === to.id) {
          html += '<td><div class="cell self">—</div></td>';
          return;
        }
        var v = row[to.id];
        if (v === undefined) {
          html += '<td><div class="cell na">·</div></td>';
        } else if (v < 0) {
          html += '<td><div class="cell" style="background:' + heatColor(-1) + ';color:#f85149">×</div></td>';
        } else {
          html += '<td><div class="cell" style="background:' + heatColor(v) + '">' + v.toFixed(1) + '</div></td>';
        }
      });
      html += "</tr>";
    });
    meshBody.innerHTML = html;
  }

  function render(snap) {
    var agents = snap.agents || [];
    var online = agents.filter(function (a) { return a.online; }).length;
    onlinePill.textContent = "在线 " + online + " / " + agents.length;
    renderServers(agents);
    renderMesh(agents, snap.mesh || {});
    updated.textContent = "更新于 " + new Date(snap.ts * 1000).toLocaleTimeString();
  }

  function connect() {
    var proto = location.protocol === "https:" ? "wss:" : "ws:";
    var ws = new WebSocket(proto + "//" + location.host + "/ws");
    ws.onmessage = function (e) {
      try { render(JSON.parse(e.data)); } catch (err) { /* ignore bad frame */ }
    };
    ws.onclose = function () {
      updated.textContent = "连接断开,3 秒后重连…";
      setTimeout(connect, 3000);
    };
    ws.onerror = function () { ws.close(); };
  }
  connect();
})();
