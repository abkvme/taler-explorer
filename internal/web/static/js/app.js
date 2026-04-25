// taler-explorer — small vanilla JS bindings on top of HTMX / Alpine.
// No framework, no build step.

(function () {
  // ==========================================================================
  // i18n — client-only. Server stays monolingual English; JS swaps `data-t`
  // nodes based on a per-language JSON catalog. Catalogs cached in
  // localStorage so warm loads apply before first paint (see early-paint
  // snippet in layout.html <head>).
  // ==========================================================================
  const I18N_SUPPORTED = ['en', 'be', 'ru', 'uk'];
  const I18N_TTL_MS = 365 * 24 * 60 * 60 * 1000; // 1 year
  const I18N_LANG_KEY = 'taler_lang';
  const I18N_SET_AT_KEY = 'taler_lang_set_at';
  const I18N_CACHE_PREFIX = 'taler_lang_cache_';

  function resolveLang() {
    try {
      const stored = localStorage.getItem(I18N_LANG_KEY);
      const setAt = parseInt(localStorage.getItem(I18N_SET_AT_KEY) || '0', 10);
      if (stored && I18N_SUPPORTED.indexOf(stored) >= 0 && Date.now() - setAt <= I18N_TTL_MS) {
        return stored;
      }
    } catch (_) {}
    const prefs = (navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language || 'en']);
    for (let i = 0; i < prefs.length; i++) {
      const primary = String(prefs[i] || '').toLowerCase().split('-')[0];
      if (I18N_SUPPORTED.indexOf(primary) >= 0) return primary;
    }
    return 'en';
  }

  let currentLang = resolveLang();
  let currentCatalog = null;

  function loadCachedCatalog(lang) {
    try {
      const raw = localStorage.getItem(I18N_CACHE_PREFIX + lang);
      return raw ? JSON.parse(raw) : null;
    } catch (_) { return null; }
  }

  async function fetchCatalog(lang) {
    if (lang === 'en') return null;
    const cached = loadCachedCatalog(lang);
    if (cached) return cached;
    try {
      const res = await fetch('/static/i18n/' + lang + '.json', { cache: 'default' });
      if (!res.ok) return null;
      const j = await res.json();
      try { localStorage.setItem(I18N_CACHE_PREFIX + lang, JSON.stringify(j)); } catch (_) {}
      return j;
    } catch (_) { return null; }
  }

  function translate(catalog, key) {
    if (!catalog || !key) return key;
    return (Object.prototype.hasOwnProperty.call(catalog, key) && catalog[key]) || key;
  }

  function applyLang(root) {
    if (!root) return;
    const cat = currentCatalog;
    if (cat === null && currentLang === 'en') return; // nothing to do
    const q = (sel) => (root.querySelectorAll ? root.querySelectorAll(sel) : []);
    q('[data-t]').forEach((el) => {
      const k = el.getAttribute('data-t');
      const v = translate(cat, k);
      if (el.textContent !== v) el.textContent = v;
    });
    q('[data-t-placeholder]').forEach((el) => {
      el.setAttribute('placeholder', translate(cat, el.getAttribute('data-t-placeholder')));
    });
    q('[data-t-title]').forEach((el) => {
      el.setAttribute('title', translate(cat, el.getAttribute('data-t-title')));
    });
    q('[data-t-aria-label]').forEach((el) => {
      el.setAttribute('aria-label', translate(cat, el.getAttribute('data-t-aria-label')));
    });
  }

  async function setLang(code) {
    if (I18N_SUPPORTED.indexOf(code) < 0) return;
    try {
      localStorage.setItem(I18N_LANG_KEY, code);
      localStorage.setItem(I18N_SET_AT_KEY, String(Date.now()));
    } catch (_) {}
    currentLang = code;
    currentCatalog = code === 'en' ? null : await fetchCatalog(code);
    document.documentElement.setAttribute('lang', code);
    applyLang(document);
  }
  window.TalerI18n = { setLang, resolveLang, currentLang: () => currentLang };

  // Kick off initial catalog load (no-op for EN; cached path is synchronous).
  (async function initI18n() {
    if (currentLang !== 'en') {
      currentCatalog = loadCachedCatalog(currentLang) || await fetchCatalog(currentLang);
      document.documentElement.setAttribute('lang', currentLang);
      applyLang(document);
    }
  })();


  // Copy-to-clipboard for txids/addresses.
  document.addEventListener('click', function (ev) {
    const btn = ev.target.closest('.copy-btn');
    if (!btn) return;
    ev.stopPropagation();
    ev.preventDefault();
    const v = btn.dataset.copy;
    if (!v) return;
    navigator.clipboard && navigator.clipboard.writeText(v).then(
      () => flash(btn, '✓'),
      () => flash(btn, '!')
    );
  });

  // Inner <a> clicks inside clickable rows should not also trigger the row
  // onclick (which would double-navigate).
  document.addEventListener('click', function (ev) {
    if (ev.target.closest('a.cell-link') && ev.target.closest('tr[role="link"]')) {
      ev.stopPropagation();
    }
  });

  // ----- Pause auto-refresh while the tab is hidden -----------------------
  // When the user switches to another tab / minimises the browser, every
  // [hx-trigger="every Xs"] element has its trigger stashed in a data-attr
  // and removed. On visibility=visible we restore them and HTMX re-arms the
  // intervals. Net effect: zero background fetches in inactive tabs.
  function pauseHxPolling() {
    document.querySelectorAll('[hx-trigger]').forEach((el) => {
      const trig = el.getAttribute('hx-trigger') || '';
      if (!/(^|\s)every\s/.test(trig)) return;
      if (el.dataset.hxTriggerStashed) return; // already paused
      el.dataset.hxTriggerStashed = trig;
      el.removeAttribute('hx-trigger');
    });
    if (window.htmx && htmx.process) htmx.process(document.body);
  }
  function resumeHxPolling() {
    let any = false;
    document.querySelectorAll('[data-hx-trigger-stashed]').forEach((el) => {
      el.setAttribute('hx-trigger', el.dataset.hxTriggerStashed);
      delete el.dataset.hxTriggerStashed;
      any = true;
    });
    if (any && window.htmx && htmx.process) htmx.process(document.body);
  }
  document.addEventListener('visibilitychange', function () {
    if (document.hidden) pauseHxPolling();
    else resumeHxPolling();
  });
  // Initial state — if the page loads in a backgrounded tab, pause immediately.
  if (document.hidden) {
    document.addEventListener('DOMContentLoaded', pauseHxPolling);
  }

  // Brand coin: spin on hover (CSS), and spin two turns on tap (JS). We
  // listen to both `pointerdown` (so touch gets immediate feedback before any
  // click-nav delay) and fall back to `click` for keyboard activation.
  function triggerBrandSpin(brand) {
    const mark = brand && brand.querySelector('.brand-mark');
    if (!mark) return;
    mark.classList.remove('spin-once');
    // Force layout so re-adding the class restarts the animation.
    void mark.offsetWidth;
    mark.classList.add('spin-once');
    mark.addEventListener('animationend', function handler() {
      mark.classList.remove('spin-once');
      mark.removeEventListener('animationend', handler);
    }, { once: true });
  }
  document.addEventListener('pointerdown', function (ev) {
    // Only touch/pen — mouse already gets the continuous hover animation.
    if (ev.pointerType === 'mouse') return;
    const brand = ev.target.closest('.brand');
    if (brand) triggerBrandSpin(brand);
  });
  document.addEventListener('keydown', function (ev) {
    if (ev.key !== 'Enter' && ev.key !== ' ') return;
    const brand = document.activeElement && document.activeElement.closest('.brand');
    if (brand) triggerBrandSpin(brand);
  });

  function flash(el, glyph) {
    const prev = el.textContent;
    el.textContent = glyph;
    setTimeout(() => { el.textContent = prev; }, 900);
  }

  // ----- Local-time rendering for <time data-local="auto"> elements -----
  const pad = (n) => String(n).padStart(2, '0');
  function toLocalString(d) {
    return (
      d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) +
      ' ' +
      pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds())
    );
  }
  function applyLocalTimes(root) {
    if (!root || !root.querySelectorAll) return;
    root.querySelectorAll('time[data-local="auto"]').forEach((el) => {
      const iso = el.getAttribute('datetime');
      if (!iso) return;
      const d = new Date(iso);
      if (isNaN(d.getTime())) return;
      const local = toLocalString(d);
      if (el.textContent !== local) el.textContent = local;
    });
  }
  document.addEventListener('DOMContentLoaded', () => applyLocalTimes(document));
  document.body.addEventListener('htmx:afterSwap', (ev) => applyLocalTimes(ev.detail.target));

  // ----- Sparkline (24h hashrate) embedded inside the Network tile.
  //
  // The Network tile sits inside the #header-stats partial, which is swapped
  // every 10s by HTMX. That throws away our mount div, so we:
  //   1) cache the last series payload,
  //   2) re-render the plot whenever #header-stats is re-inserted,
  //   3) drop the stale uPlot instance if its root is no longer in the DOM.
  let plot = null;
  let lastSeries = null;

  function renderInto(mount, data) {
    if (!mount || !window.uPlot) return;
    if (!data || !data[0] || data[0].length < 2) return;

    // If our last plot was attached to a detached mount (swap happened),
    // tear it down so we can rebuild against the fresh node.
    if (plot && (!plot.root || !document.body.contains(plot.root))) {
      try { plot.destroy(); } catch (_) {}
      plot = null;
    }
    // If the plot is on a different mount, tear down too.
    if (plot && plot.root && plot.root.parentNode !== mount) {
      try { plot.destroy(); } catch (_) {}
      plot = null;
    }

    const accent = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#c9a24b';
    const w = Math.max(40, mount.clientWidth);
    const h = Math.max(20, mount.clientHeight);

    if (plot) {
      plot.setData(data);
      plot.setSize({ width: w, height: h });
      return;
    }

    const opts = {
      width: w,
      height: h,
      padding: [2, 2, 2, 2],
      legend: { show: false },
      cursor: { show: false },
      scales: { x: { time: true } },
      axes: [{ show: false }, { show: false }],
      series: [
        {},
        { stroke: accent, width: 1.5, fill: accent + '33', points: { show: false } },
      ],
    };
    plot = new uPlot(opts, data, mount);
  }

  window.renderSparkline = function (json) {
    try { lastSeries = JSON.parse(json); } catch (_) { return; }
    const mount = document.getElementById('sparkline-mount');
    renderInto(mount, lastSeries);
  };

  // Price sparkline — green for uptrend, red for downtrend, computed from the
  // first/last values of the series.
  let pricePlot = null;
  let lastPriceSeries = null;

  function priceStrokeColor(data) {
    if (!data || !data[1] || data[1].length < 2) return '#7ea8ff';
    const first = data[1][0];
    const last = data[1][data[1].length - 1];
    if (last > first) return getComputedStyle(document.documentElement).getPropertyValue('--good').trim() || '#3cc98a';
    if (last < first) return getComputedStyle(document.documentElement).getPropertyValue('--danger').trim() || '#ea5a74';
    return getComputedStyle(document.documentElement).getPropertyValue('--fg-muted').trim() || '#8a93a6';
  }

  function renderPriceInto(mount, data) {
    if (!mount || !window.uPlot) return;
    if (!data || !data[0] || data[0].length < 2) return;
    if (pricePlot && (!pricePlot.root || !document.body.contains(pricePlot.root))) {
      try { pricePlot.destroy(); } catch (_) {}
      pricePlot = null;
    }
    if (pricePlot && pricePlot.root && pricePlot.root.parentNode !== mount) {
      try { pricePlot.destroy(); } catch (_) {}
      pricePlot = null;
    }
    const stroke = priceStrokeColor(data);
    const w = Math.max(40, mount.clientWidth);
    const h = Math.max(20, mount.clientHeight);
    if (pricePlot) {
      pricePlot.setData(data);
      pricePlot.setSize({ width: w, height: h });
      return;
    }
    const opts = {
      width: w,
      height: h,
      padding: [2, 2, 2, 2],
      legend: { show: false },
      cursor: { show: false },
      scales: { x: { time: true } },
      axes: [{ show: false }, { show: false }],
      series: [{}, { stroke: stroke, width: 1.5, fill: stroke + '33', points: { show: false } }],
    };
    pricePlot = new uPlot(opts, data, mount);
  }

  window.renderPriceSparkline = function (json) {
    try { lastPriceSeries = JSON.parse(json); } catch (_) { return; }
    const mount = document.getElementById('price-spark');
    renderPriceInto(mount, lastPriceSeries);
  };

  // Re-render the cached series whenever the header-stats partial is swapped
  // (every 10s) — the mount nodes are brand new each time.
  // Also re-apply i18n over the swapped subtree so newly-inserted rows pick
  // up the currently-selected language.
  document.body.addEventListener('htmx:afterSwap', function (ev) {
    const target = ev.detail && ev.detail.target;
    if (target && target.id === 'header-stats') {
      if (lastSeries) {
        renderInto(document.getElementById('sparkline-mount'), lastSeries);
      }
      if (lastPriceSeries) {
        renderPriceInto(document.getElementById('price-spark'), lastPriceSeries);
      }
    }
    if (target) applyLang(target);
  });

  window.addEventListener('resize', function () {
    if (!lastSeries) return;
    const mount = document.getElementById('sparkline-mount');
    renderInto(mount, lastSeries);
  });
})();
