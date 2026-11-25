// Shared shell behaviour: health dot, rail counts, rescan, topbar search.
// Ported from `Layout.astro` — Astro ClientRouter / view-transition hooks dropped,
// runtime behaviour identical. Runs on every page.

import { api } from './api.js';
import { artistAvatar, setBusy, toast } from './ui.js';

function setStat(key, value) {
  const node = document.querySelector(`[data-stat="${key}"]`);
  if (node) node.textContent = value.toLocaleString();
}

function initLayout() {
  const health = document.getElementById('health');
  const healthLabel = health?.querySelector('.health__label');
  const rescan = document.getElementById('rescan');

  if (health && healthLabel) {
    fetch('/api/v1/health')
      .then((res) => {
        if (!res.ok) throw new Error('unhealthy');
        health.classList.add('is-ok');
        healthLabel.textContent = 'Library online';
      })
      .catch(() => {
        health.classList.add('is-down');
        healthLabel.textContent = 'Server unreachable';
      });
  }

  api
    .getStats()
    .then((stats) => {
      if (!stats) return;

      setStat('artists', stats.artists);
      setStat('release_groups', stats.release_groups);
      setStat('unlinked_files', stats.files.unlinked);
      setStat('total_files', stats.files.total);
    })
    .catch(() => {
      /* counts are optional */
    });

  rescan?.addEventListener('click', async () => {
    const button = rescan;
    setBusy(button, true, 'Scanning…');
    try {
      const response = await api.scan();
      if (!response.ok) {
        let message = response.statusText;
        try {
          message = (await response.json()).error ?? message;
        } catch {
          /* keep status text */
        }
        toast(`Could not queue scan: ${message}`, 'error');
        return;
      }
      toast('Scan queued. New files will appear shortly.', 'ok');
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      toast(`Could not queue scan: ${message}`, 'error');
    } finally {
      setBusy(button, false);
    }
  });

  initTopbarSearch();
}

function initTopbarSearch() {
  const container = document.getElementById('topbar-search');
  if (!container || container.dataset.initialized) return;
  container.dataset.initialized = 'true';

  const input = document.getElementById('artist-search');
  const results = document.getElementById('artist-search-results');
  if (!input || !results) return;

  let items = [];
  let active = -1;
  let token = 0;
  let debounceTimer;

  function close() {
    results.hidden = true;
    input.setAttribute('aria-expanded', 'false');
    active = -1;
  }

  function go(hit) {
    window.location.assign(`/artist?id=${encodeURIComponent(hit.id)}`);
  }

  function render() {
    results.replaceChildren();
    if (items.length === 0) {
      const empty = document.createElement('div');
      empty.className = 'topbar__empty';
      empty.textContent = 'No artists found in your library.';
      results.append(empty);
    } else {
      items.forEach((hit, index) => {
        const button = document.createElement('button');
        button.type = 'button';
        button.className =
          'topbar__result' + (index === active ? ' is-active' : '');
        button.setAttribute('role', 'option');
        button.setAttribute(
          'aria-selected',
          index === active ? 'true' : 'false',
        );
        const art = document.createElement('span');
        art.className = 'pick__art';
        art.append(
          artistAvatar(hit.id, hit.name, '', {
            variant: 'initials',
          }),
        );
        const label = document.createElement('span');
        label.textContent = hit.name;
        button.append(art, label);
        button.addEventListener('click', () => go(hit));
        results.append(button);
      });
    }
    results.hidden = false;
    input.setAttribute('aria-expanded', 'true');
  }

  async function search(query) {
    const current = ++token;
    if (query.length < 2) {
      close();
      return;
    }
    try {
      // Local library only (GET /api/v1/artists?q=) — never MusicBrainz.
      const page = await api
        .listArtists(8, 0, { query })
        .catch(() => null);
      if (current !== token) return;
      items = (page?.items ?? []).map((artist) => ({
        id: artist.id,
        name: artist.name,
      }));
      active = -1;
      render();
    } catch {
      if (current !== token) return;
      close();
    }
  }

  input.addEventListener('input', () => {
    window.clearTimeout(debounceTimer);
    const query = input.value.trim();
    // Debounced: only fires after the user pauses typing.
    debounceTimer = window.setTimeout(() => void search(query), 200);
  });

  input.addEventListener('focus', () => {
    if (items.length > 0 && input.value.trim().length >= 2) render();
  });

  const kbd = container.querySelector('.topbar__kbd');
  if (kbd) {
    kbd.textContent = /mac/i.test(navigator.platform ?? '')
      ? '⌘\u00A0K'
      : 'Ctrl\u00A0K';
  }

  document.addEventListener('keydown', (event) => {
    if (
      (event.metaKey || event.ctrlKey) &&
      event.key.toLowerCase() === 'k'
    ) {
      // Don't steal focus from an open dialog.
      if (document.querySelector('dialog[open]')) return;
      event.preventDefault();
      input.focus();
      input.select();
    }
  });

  input.addEventListener('keydown', (event) => {
    if (results.hidden) return;
    if (event.key === 'ArrowDown' && items.length > 0) {
      event.preventDefault();
      active = (active + 1) % items.length;
      render();
    } else if (event.key === 'ArrowUp' && items.length > 0) {
      event.preventDefault();
      active = (active - 1 + items.length) % items.length;
      render();
    } else if (event.key === 'Enter') {
      event.preventDefault();
      const pick = items[active] ?? items[0];
      if (pick) go(pick);
    } else if (event.key === 'Escape') {
      close();
      input.blur();
    }
  });

  document.addEventListener('click', (event) => {
    if (!container.contains(event.target)) close();
  });
}

initLayout();
