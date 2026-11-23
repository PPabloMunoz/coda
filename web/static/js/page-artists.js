// Legacy /artists URL: redirect to the library home.
// The Astro build prerendered /artists as a redirect page; keep the same behaviour.

if (location.pathname.replace(/\/+$/, '') === '/artists') {
  window.location.replace('/');
}
