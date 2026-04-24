// taler-explorer — small vanilla JS bindings on top of HTMX / Alpine.
// No framework, no build step.

(function () {
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
  document.body.addEventListener('htmx:afterSwap', function (ev) {
    if (ev.detail.target && ev.detail.target.id === 'header-stats') {
      if (lastSeries) {
        renderInto(document.getElementById('sparkline-mount'), lastSeries);
      }
      if (lastPriceSeries) {
        renderPriceInto(document.getElementById('price-spark'), lastPriceSeries);
      }
    }
  });

  window.addEventListener('resize', function () {
    if (!lastSeries) return;
    const mount = document.getElementById('sparkline-mount');
    renderInto(mount, lastSeries);
  });
})();
