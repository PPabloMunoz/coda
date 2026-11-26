// Legacy /artists URL: redirect to the library home.

if (location.pathname.replace(/\/+$/, '') === '/artists') {
  window.location.replace('/');
}
