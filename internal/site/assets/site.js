// Lowlight documentation: theme toggle, navigation, copy buttons, table-of-contents
// highlighting, and client-side search. No dependencies, works from file://.
(function () {
  "use strict";
  var root = document.body.getAttribute("data-root") || "";

  // Theme.
  var toggle = document.querySelector("[data-theme-toggle]");
  if (toggle) {
    toggle.addEventListener("click", function () {
      var cur = document.documentElement.dataset.theme;
      if (!cur) cur = matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
      var next = cur === "dark" ? "light" : "dark";
      document.documentElement.dataset.theme = next;
      try { localStorage.setItem("ll-theme", next); } catch (e) {}
    });
  }

  // Mobile sidebar.
  var menu = document.querySelector("[data-toggle=sidebar]");
  if (menu) menu.addEventListener("click", function () { document.body.classList.toggle("sidebar-open"); });
  var active = document.querySelector(".sidebar a.active");
  if (active && active.scrollIntoView) active.scrollIntoView({ block: "center" });

  // Copy buttons.
  document.querySelectorAll(".doc pre, .home pre").forEach(function (pre) {
    if (pre.closest(".api-sig") || pre.closest(".api-method")) return;
    var btn = document.createElement("button");
    btn.className = "copy-btn";
    btn.type = "button";
    btn.textContent = "Copy";
    btn.addEventListener("click", function () {
      var text = pre.querySelector("code") ? pre.querySelector("code").innerText : pre.innerText;
      var done = function () { btn.textContent = "Copied"; btn.classList.add("done"); setTimeout(function () { btn.textContent = "Copy"; btn.classList.remove("done"); }, 1400); };
      if (navigator.clipboard) navigator.clipboard.writeText(text).then(done, function () {});
      else { var ta = document.createElement("textarea"); ta.value = text; document.body.appendChild(ta); ta.select(); document.execCommand("copy"); ta.remove(); done(); }
    });
    pre.appendChild(btn);
  });

  // Table of contents: highlight the section in view.
  var tocLinks = Array.prototype.slice.call(document.querySelectorAll(".toc a"));
  if (tocLinks.length && "IntersectionObserver" in window) {
    var map = {};
    tocLinks.forEach(function (a) { map[decodeURIComponent(a.getAttribute("href").slice(1))] = a; });
    var visible = {};
    var obs = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) { visible[e.target.id] = e.isIntersecting; });
      var first = null;
      tocLinks.forEach(function (a) {
        var id = decodeURIComponent(a.getAttribute("href").slice(1));
        if (!first && visible[id]) first = a;
      });
      if (first) { tocLinks.forEach(function (a) { a.classList.remove("current"); }); first.classList.add("current"); }
    }, { rootMargin: "-70px 0px -60% 0px" });
    Object.keys(map).forEach(function (id) { var el = document.getElementById(id); if (el) obs.observe(el); });
  }

  // Search.
  var overlay = document.querySelector(".search-overlay");
  var input = overlay && overlay.querySelector("input");
  var list = overlay && overlay.querySelector(".search-results");
  var index = window.LL_SEARCH || [];
  var results = [], sel = 0;

  function openSearch() {
    if (!overlay) return;
    overlay.hidden = false;
    input.value = "";
    render([]);
    setTimeout(function () { input.focus(); }, 0);
  }
  function closeSearch() { if (overlay) overlay.hidden = true; }
  function esc(s) { return String(s).replace(/[&<>"]/g, function (c) { return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]; }); }

  function score(entry, terms, q) {
    var t = entry.t.toLowerCase();
    var hay = (t + " " + (entry.s || "") + " " + (entry.d || "")).toLowerCase();
    var total = 0;
    for (var i = 0; i < terms.length; i++) {
      var term = terms[i];
      var at = t.indexOf(term);
      if (at === 0) total += 12; else if (at > 0) total += (/[.\s_:/-]/.test(t[at - 1]) ? 8 : 5);
      else if (hay.indexOf(term) >= 0) total += 2;
      else return 0;
    }
    if (t === q) total += 30;
    if (entry.k === "page") total += 4;
    else if (entry.k !== "heading") total += 2;
    return total - t.length / 40;
  }

  function search(q) {
    q = q.trim().toLowerCase();
    if (!q) return [];
    var terms = q.split(/\s+/);
    var out = [];
    for (var i = 0; i < index.length; i++) {
      var sc = score(index[i], terms, q);
      if (sc > 0) out.push([sc, index[i]]);
    }
    out.sort(function (a, b) { return b[0] - a[0]; });
    return out.slice(0, 40).map(function (x) { return x[1]; });
  }

  function render(rs) {
    results = rs; sel = 0;
    if (!input.value.trim()) { list.innerHTML = '<li class="empty">Type to search the guides, specification, examples, and package APIs.</li>'; return; }
    if (!rs.length) { list.innerHTML = '<li class="empty">No results.</li>'; return; }
    list.innerHTML = rs.map(function (r, i) {
      var kind = r.k && r.k !== "page" && r.k !== "heading" ? '<span class="r-kind">' + esc(r.k) + "</span>" : "";
      return '<li' + (i === 0 ? ' class="sel"' : "") + '><a href="' + esc(root + r.u) + '"><span class="r-title">' + esc(r.t) + "</span>" + kind +
        '<span class="r-sec">' + esc(r.s || "") + "</span>" + (r.d ? '<span class="r-desc">' + esc(r.d) + "</span>" : "") + "</a></li>";
    }).join("");
  }

  function move(d) {
    var items = list.querySelectorAll("li");
    if (!items.length || !results.length) return;
    items[sel].classList.remove("sel");
    sel = (sel + d + items.length) % items.length;
    items[sel].classList.add("sel");
    items[sel].scrollIntoView({ block: "nearest" });
  }

  document.querySelectorAll("[data-open-search]").forEach(function (b) { b.addEventListener("click", openSearch); });
  if (overlay) {
    overlay.addEventListener("click", function (e) { if (e.target === overlay) closeSearch(); });
    input.addEventListener("input", function () { render(search(input.value)); });
    input.addEventListener("keydown", function (e) {
      if (e.key === "ArrowDown") { e.preventDefault(); move(1); }
      else if (e.key === "ArrowUp") { e.preventDefault(); move(-1); }
      else if (e.key === "Enter") {
        var a = list.querySelectorAll("li a")[sel];
        if (a) { e.preventDefault(); location.href = a.getAttribute("href"); closeSearch(); }
      } else if (e.key === "Escape") closeSearch();
    });
  }
  document.addEventListener("keydown", function (e) {
    var tag = (e.target.tagName || "").toLowerCase();
    var typing = tag === "input" || tag === "textarea" || e.target.isContentEditable;
    if ((e.key === "/" && !typing) || ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k")) { e.preventDefault(); openSearch(); }
    else if (e.key === "Escape") { closeSearch(); document.body.classList.remove("sidebar-open"); }
  });
})();
