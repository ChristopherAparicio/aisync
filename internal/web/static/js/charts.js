/**
 * aisync charts.js — Shared chart utilities built on uPlot + Tippy.js.
 *
 * Design philosophy:
 *   - Dark theme matching aisync CSS variables
 *   - Grafana/Datadog-style area sparklines with gradient fills
 *   - Rich tooltips via Tippy.js (replaces native title attributes)
 *   - Smooth accordion animations (replaces instant <details> toggle)
 *   - All charts are progressive enhancement: CSS fallback works without JS
 */

var AI5 = (function() {
  'use strict';

  // ---------------------------------------------------------------------------
  // Theme constants (match style.css :root vars)
  // ---------------------------------------------------------------------------
  var COLORS = {
    bg:         '#0f1117',
    bgCard:     '#1a1d27',
    bgHover:    '#22252f',
    border:     '#2a2d3a',
    text:       '#e1e4ec',
    textMuted:  '#8b8fa3',
    accent:     '#6c7ee1',
    accentLight:'#8b9cf0',
    green:      '#4ade80',
    red:        '#f87171',
    yellow:     '#fbbf24',
    purple:     '#a855f7',
  };

  // Semi-transparent versions for area fills
  function alpha(hex, a) {
    var r = parseInt(hex.slice(1,3), 16);
    var g = parseInt(hex.slice(3,5), 16);
    var b = parseInt(hex.slice(5,7), 16);
    return 'rgba(' + r + ',' + g + ',' + b + ',' + a + ')';
  }

  // ---------------------------------------------------------------------------
  // Sparkline — mini area chart for KPI strip
  // ---------------------------------------------------------------------------
  // el: DOM element to render into
  // opts: { values: [int,...], labels: [str,...], color: '#hex', label: 'Sessions' }
  function sparkline(el, opts) {
    if (!window.uPlot || !opts.values || opts.values.length === 0) return null;

    var vals = opts.values;
    var labels = opts.labels || [];
    var color = opts.color || COLORS.accent;
    var n = vals.length;

    // uPlot data: [x-array, y-array]
    // x = sequential indices (0..n-1), displayed as labels
    var xs = new Array(n);
    for (var i = 0; i < n; i++) xs[i] = i;

    var data = [xs, vals];

    // Gradient fill factory
    function gradientFill(u, seriesIdx) {
      var s = u.series[seriesIdx];
      var can = u.ctx.canvas;
      var grad = u.ctx.createLinearGradient(0, 0, 0, can.height);
      grad.addColorStop(0, alpha(color, 0.4));
      grad.addColorStop(1, alpha(color, 0.02));
      return grad;
    }

    var uOpts = {
      width:  el.offsetWidth || 120,
      height: el.offsetHeight || 32,
      cursor: {
        show: true,
        x: false,   // no vertical crosshair
        y: false,   // no horizontal crosshair
        points: { show: false }
      },
      select: { show: false },
      legend: { show: false },
      padding: [2, 0, 0, 0],
      scales: {
        x: { time: false },
        y: {
          range: function(u, dMin, dMax) {
            return [0, dMax * 1.1 || 1];
          }
        }
      },
      axes: [
        { show: false },
        { show: false }
      ],
      series: [
        {},
        {
          stroke: color,
          width:  1.5 / devicePixelRatio,
          fill:   gradientFill,
          points: { show: false },
          spanGaps: true,
        }
      ],
      hooks: {
        setCursor: [
          function(u) {
            var idx = u.cursor.idx;
            if (idx == null) {
              hideSparkTip(el);
              return;
            }
            var val = vals[idx];
            var lbl = labels[idx] || '';
            var label = opts.label || '';
            showSparkTip(el, u, idx, lbl, val, label, color);
          }
        ],
        ready: [
          function(u) {
            // Remove cursor hover bg
            u.over.style.background = 'transparent';
          }
        ]
      }
    };

    // Clear CSS fallback bars
    el.innerHTML = '';
    el.classList.add('ai5-sparkline');

    return new uPlot(uOpts, data, el);
  }

  // Sparkline tooltip using Tippy.js
  var sparkTipInstances = new WeakMap();

  function showSparkTip(el, u, idx, label, value, seriesName, color) {
    if (!window.tippy) return;

    var html = '<div class="ai5-tip">' +
      '<span class="ai5-tip-label">' + label + '</span>' +
      '<span class="ai5-tip-value" style="color:' + color + '">' +
        formatNum(value) + (seriesName ? ' ' + seriesName : '') +
      '</span></div>';

    var inst = sparkTipInstances.get(el);
    if (!inst) {
      inst = tippy(el, {
        content: html,
        allowHTML: true,
        placement: 'top',
        arrow: true,
        animation: 'shift-away',
        duration: [150, 100],
        theme: 'ai5',
        trigger: 'manual',
        hideOnClick: false,
        offset: [0, 8],
      });
      sparkTipInstances.set(el, inst);
    } else {
      inst.setContent(html);
    }
    inst.show();
  }

  function hideSparkTip(el) {
    var inst = sparkTipInstances.get(el);
    if (inst) inst.hide();
  }

  // ---------------------------------------------------------------------------
  // Number formatting
  // ---------------------------------------------------------------------------
  function formatNum(n) {
    if (n == null) return '—';
    if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
    if (n >= 1000) return (n / 1000).toFixed(1) + 'K';
    return String(n);
  }

  // ---------------------------------------------------------------------------
  // Smooth accordion animation for <details> elements
  // ---------------------------------------------------------------------------
  function initAccordions() {
    document.querySelectorAll('details.section-accordion').forEach(function(details) {
      // Skip if already initialized
      if (details._ai5Accordion) return;
      details._ai5Accordion = true;

      var summary = details.querySelector('summary');
      var content = details.querySelector('.section-accordion-body, .accordion-body');
      if (!summary || !content) {
        // Wrap non-summary children in a div for animation
        var children = [];
        for (var i = 0; i < details.children.length; i++) {
          if (details.children[i].tagName !== 'SUMMARY') {
            children.push(details.children[i]);
          }
        }
        if (children.length === 0) return;

        content = document.createElement('div');
        content.className = 'ai5-accordion-body';
        children.forEach(function(c) { content.appendChild(c); });
        details.appendChild(content);
      }

      summary.addEventListener('click', function(e) {
        e.preventDefault();

        if (details.open) {
          // Closing: animate height to 0, then remove open
          content.style.maxHeight = content.scrollHeight + 'px';
          content.offsetHeight; // force reflow
          content.style.maxHeight = '0';
          content.style.overflow = 'hidden';

          content.addEventListener('transitionend', function handler() {
            content.removeEventListener('transitionend', handler);
            details.open = false;
            content.style.maxHeight = '';
            content.style.overflow = '';
          });
        } else {
          // Opening: set open, then animate from 0 to scrollHeight
          details.open = true;
          content.style.maxHeight = '0';
          content.style.overflow = 'hidden';
          content.offsetHeight; // force reflow
          content.style.maxHeight = content.scrollHeight + 'px';

          content.addEventListener('transitionend', function handler() {
            content.removeEventListener('transitionend', handler);
            content.style.maxHeight = '';
            content.style.overflow = '';
          });
        }
      });
    });
  }

  // ---------------------------------------------------------------------------
  // Tippy.js tooltip upgrade — replace native title="" with rich tooltips
  // ---------------------------------------------------------------------------
  function initTooltips() {
    if (!window.tippy) return;

    // Convert all elements with title= to Tippy instances
    document.querySelectorAll('[data-tippy-content]').forEach(function(el) {
      if (el._tippy) return; // already initialized
      tippy(el, {
        theme: 'ai5',
        placement: 'top',
        arrow: true,
        animation: 'shift-away',
        duration: [200, 150],
      });
    });

    // Auto-tooltip for truncated sidebar items (overflow detection)
    document.querySelectorAll('.sidebar-item-name').forEach(function(el) {
      if (el._tippy) return;
      if (el.scrollWidth > el.clientWidth) {
        tippy(el, {
          content: el.textContent.trim(),
          theme: 'ai5',
          placement: 'right',
          arrow: true,
          animation: 'shift-away',
          duration: [200, 150],
          delay: [300, 0],
        });
      }
    });

    // Upgrade native title attributes on KPI values and badges
    document.querySelectorAll('.kpi-strip-value[title], .badge[title], .sparkline-bar[title], .activity-card-when[title]').forEach(function(el) {
      if (el._tippy) return;
      var title = el.getAttribute('title');
      if (!title) return;
      el.removeAttribute('title');
      tippy(el, {
        content: title,
        theme: 'ai5',
        placement: 'top',
        arrow: true,
        animation: 'shift-away',
        duration: [200, 150],
      });
    });
  }

  // ---------------------------------------------------------------------------
  // Auto-init on DOMContentLoaded + HTMX content swap
  // ---------------------------------------------------------------------------
  function init() {
    initAccordions();
    initTooltips();
    initAllSparklines();
  }

  function initAllSparklines() {
    // Find all sparkline containers with embedded JSON data
    document.querySelectorAll('[data-ai5-sparkline]').forEach(function(el) {
      if (el._ai5Chart) return; // already initialized
      try {
        var opts = JSON.parse(el.getAttribute('data-ai5-sparkline'));
        el._ai5Chart = sparkline(el, opts);
      } catch(e) {
        // Fallback: keep CSS bars
      }
    });

    // KPI sparklines with data-values attribute (cost/session KPI cards)
    document.querySelectorAll('.kpi-sparkline[data-values]').forEach(function(el) {
      if (el._ai5Chart) return;
      try {
        var vals = JSON.parse(el.getAttribute('data-values'));
        if (!vals || vals.length === 0) return;
        var color = el.getAttribute('data-color') || COLORS.accent;
        // Resolve CSS variables
        if (color.indexOf('var(') === 0) {
          var varName = color.replace('var(', '').replace(')', '').trim();
          color = getComputedStyle(document.documentElement).getPropertyValue(varName).trim() || COLORS.accent;
        }
        el._ai5Chart = sparkline(el, { values: vals, color: color, label: '$' });
      } catch(e) {
        // Fallback: leave empty
      }
    });
  }

  // Init on page load
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

  // Re-init after HTMX swaps new content
  document.addEventListener('htmx:afterSwap', init);
  document.addEventListener('htmx:afterSettle', init);

  // ---------------------------------------------------------------------------
  // Public API
  // ---------------------------------------------------------------------------
  return {
    sparkline:   sparkline,
    initTooltips: initTooltips,
    initAccordions: initAccordions,
    COLORS:      COLORS,
    formatNum:   formatNum,
    alpha:       alpha,
  };
})();
