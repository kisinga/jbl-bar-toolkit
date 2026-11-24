// Section navigation: one list of links, shown as a side menu on wide screens
// and as a row of chips stuck under the header on narrow ones (style.css).
// Each link follows its section: hidden with it, a count or state beside it
// (running timers, messages, an applied EQ), the section opened when it is a
// folded <details>, and marked current while it is on screen.
export function createNav(nav, {doc = document, win = window} = {}) {
  const links = [...nav.querySelectorAll('a[href^="#"]')];
  const target = link => doc.getElementById(link.getAttribute('href').slice(1));
  const reduced = () => win.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  let current = null;
  // A link clicked stays current until the reader scrolls by hand, even when
  // the page cannot bring its section up to the band (the last ones).
  let pinned = null;

  function setCurrent(link) {
    if (link === current) return;
    current?.removeAttribute('aria-current');
    current = link;
    if (!link) return;
    link.setAttribute('aria-current', 'location');
    // Chips: bring the current one to the middle of the row when it is cut
    // or near an edge, without scrolling the page.
    if (nav.scrollWidth > nav.clientWidth) {
      const row = nav.getBoundingClientRect(), chip = link.getBoundingClientRect();
      const left = chip.left - row.left + nav.scrollLeft, right = left + chip.width, gutter = 24;
      if (left < nav.scrollLeft + gutter || right > nav.scrollLeft + nav.clientWidth - gutter) {
        nav.scrollTo({left: Math.max(0, left - (nav.clientWidth - chip.width) / 2), behavior: reduced() ? 'auto' : 'smooth'});
      }
    }
  }

  for (const link of links) {
    link.addEventListener('click', event => {
      const section = target(link);
      if (!section || section.hidden) return;
      event.preventDefault();
      if (section.tagName === 'DETAILS' && !section.open) section.open = true;
      if (!section.hasAttribute('tabindex')) section.setAttribute('tabindex', '-1');
      section.focus({preventScroll: true});
      section.scrollIntoView({behavior: reduced() ? 'auto' : 'smooth', block: 'start'});
      setCurrent(link);
      pinned = link;
    });
  }

  // The current section: the first one crossing a band near the top of the
  // window (below the sticky chips), in page order.
  const visible = new Set();
  function follow() {
    if (pinned) return;
    const first = links.find(link => !link.hidden && visible.has(target(link)));
    if (first) setCurrent(first);
  }
  if (win.IntersectionObserver) {
    const observer = new win.IntersectionObserver(entries => {
      for (const entry of entries) entry.isIntersecting ? visible.add(entry.target) : visible.delete(entry.target);
      follow();
    }, {rootMargin: '-25% 0px -65% 0px'});
    for (const link of links) if (target(link)) observer.observe(target(link));
    const release = () => { if (pinned) { pinned = null; follow(); } };
    for (const type of ['wheel', 'touchmove', 'keydown']) win.addEventListener(type, release, {passive: true});
  }

  // Refresh at the foot of the page on phones mirrors the header's button.
  const refresh = doc.getElementById('refresh'), footer = doc.getElementById('refresh-footer');
  if (refresh && footer) {
    const mirror = () => { footer.disabled = refresh.disabled; };
    mirror();
    new win.MutationObserver(mirror).observe(refresh, {attributes: true, attributeFilter: ['disabled']});
    footer.addEventListener('click', () => refresh.click());
  }

  function badge(name, state) {
    if (name === 'timers') {
      const n = (state?.timers || []).length;
      return n ? [String(n), n === 1 ? '1 running' : n + ' running'] : null;
    }
    if (name === 'messages') {
      const n = (state?.messages || []).length;
      return n ? [String(n), n === 1 ? '1 message' : n + ' messages'] : null;
    }
    if (name === 'eq') return state?.eq?.enabled === true ? ['On', 'on'] : null;
    return null;
  }

  // Before the first state: the sections the page starts with hidden.
  for (const link of links) link.hidden = !target(link) || target(link).hidden;

  return {
    render(context) {
      for (const link of links) {
        const section = target(link);
        link.hidden = !section || section.hidden;
        const mark = link.querySelector('[data-badge]');
        if (!mark) continue;
        const value = badge(mark.dataset.badge, context.state);
        mark.hidden = !value;
        mark.textContent = value ? value[0] : '';
        // Read as "Timers, 2 running" rather than "Timers 2".
        if (value) link.setAttribute('aria-label', link.firstChild.textContent + ', ' + value[1]);
        else link.removeAttribute('aria-label');
      }
      if (current?.hidden) { pinned = null; setCurrent(null); follow(); }
    },
  };
}
