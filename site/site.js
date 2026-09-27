// Small touches. The page works without any of this.
(function () {
  var reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  // Hero terminal types itself out once. Commands are typed, output appears
  // whole, the way a real session reads.
  var pre = document.querySelector('.term pre');
  if (pre && !reduced) {
    var lines = pre.innerHTML.split('\n');
    var prompt = '<span class="p">$ </span>';
    // Continuation lines of a command are indented; output never is.
    var inCmd = false;
    var isInput = function (l) {
      inCmd = l.indexOf(prompt) === 0 || (inCmd && /^\s{4}/.test(l));
      return inCmd;
    };
    pre.style.minHeight = pre.offsetHeight + 'px';
    pre.innerHTML = '';
    var i = 0;
    var next = function () {
      if (i >= lines.length) return;
      var line = lines[i++];
      var input = isInput(line);
      if (!input || line.indexOf('cursor') !== -1) {
        pre.insertAdjacentHTML('beforeend', line + (i < lines.length ? '\n' : ''));
        return setTimeout(next, input ? 0 : 140);
      }
      var head = line.indexOf(prompt) === 0 ? prompt : '';
      var text = line.slice(head.length).replace(/&amp;/g, '&').replace(/&lt;/g, '<').replace(/&gt;/g, '>');
      pre.insertAdjacentHTML('beforeend', head);
      var node = document.createTextNode('');
      pre.appendChild(node);
      var c = 0;
      (function type() {
        node.data = text.slice(0, ++c);
        if (c < text.length) return setTimeout(type, 9 + Math.random() * 18);
        pre.appendChild(document.createTextNode('\n'));
        setTimeout(next, /^\s{4}/.test(lines[i] || '') ? 30 : 380);
      })();
    };
    setTimeout(next, 450);
  }

  // Chart readout on hover.
  var chart = document.querySelector('.chart');
  if (chart) {
    var svg = chart.querySelector('svg');
    var pts = svg.querySelector('polyline').getAttribute('points').trim().split(/\s+/).map(function (p) {
      var xy = p.split(','); return [+xy[0], +xy[1]];
    });
    var line = document.createElementNS('http://www.w3.org/2000/svg', 'line');
    line.setAttribute('y1', 0); line.setAttribute('y2', 72);
    line.setAttribute('stroke', '#8a877e'); line.setAttribute('vector-effect', 'non-scaling-stroke');
    line.style.display = 'none';
    svg.appendChild(line);
    var label = chart.querySelector('.chart-label span:last-child');
    var idle = label.textContent;
    svg.addEventListener('mousemove', function (e) {
      var r = svg.getBoundingClientRect();
      var k = Math.round((e.clientX - r.left) / r.width * (pts.length - 1));
      k = Math.max(0, Math.min(pts.length - 1, k));
      var x = pts[k][0], rps = (72 - pts[k][1]) * 1.2;
      var mins = Math.round(k / (pts.length - 1) * 60);
      line.setAttribute('x1', x); line.setAttribute('x2', x); line.style.display = '';
      label.textContent = (mins < 60 ? '14:' + String(mins).padStart(2, '0') : '15:00') + '  ' + rps.toFixed(1) + ' req/s';
      label.classList.add('amber');
    });
    svg.addEventListener('mouseleave', function () {
      line.style.display = 'none'; label.textContent = idle; label.classList.remove('amber');
    });
  }

  // Size bars fill in when the table scrolls into view.
  var table = document.querySelector('.sizes');
  if (table && !reduced && 'IntersectionObserver' in window) {
    table.classList.add('pending');
    new IntersectionObserver(function (es, o) {
      if (es[0].isIntersecting) { table.classList.remove('pending'); o.disconnect(); }
    }, { threshold: 0.4 }).observe(table);
  }

  // Keyboard: d for docs, h for home, g for GitHub.
  document.addEventListener('keydown', function (e) {
    if (e.metaKey || e.ctrlKey || e.altKey || /input|textarea/i.test(e.target.tagName)) return;
    var root = document.body.dataset.root || './';
    if (e.key === 'd') location.href = root + 'docs/';
    else if (e.key === 'h') location.href = root;
    else if (e.key === 'g') location.href = 'https://github.com/nicktill/tinyobs';
  });

  console.log('%c▮ tinyobs', 'color:#f5a524;font:500 14px monospace',
    '\nyou read source. you might like this one:\nhttps://github.com/nicktill/tinyobs/tree/main/pkg/promql');
})();
