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
      setStat('ignored', stats.ignored ?? 0);
      setStat('unlinked_files', stats.files.unlinked);
      setStat('total_files', stats.files.total);
    })
    .catch(() => {
      /* counts are optional */
    });

  rescan?.addEventListener('click', () => void startRescan(rescan));

  // If a scan is already queued/running (another tab, the scheduler, or
  // the startup scan), reflect it instead of looking idle.
  api
    .scanStatus()
    .then((status) => {
      if (
        status &&
        (status.state === 'queued' || status.state === 'running') &&
        rescan
      ) {
        watchScan(rescan);
      }
    })
    .catch(() => {
      /* status is optional */
    });

  initTopbarSearch();
}

// startRescan enqueues a scan, then polls /scan/status until it reaches a
// terminal state. The button stays busy while queued/running so duplicate
// triggers are disabled; the backend still dedups queued scans.
function startRescan(button) {
  setBusy(button, true, 'Scan queued…');
  api
    .scan()
    .then(async (response) => {
      if (!response.ok) {
        let message = response.statusText;
        try {
          message = (await response.json()).error ?? message;
        } catch {
          /* keep status text */
        }
        toast(`Could not queue scan: ${message}`, 'error');
        setBusy(button, false);
        return;
      }
      watchScan(button);
    })
    .catch((error) => {
      const message =
        error instanceof Error ? error.message : String(error);
      toast(`Could not queue scan: ${message}`, 'error');
      setBusy(button, false);
    });
}

// watchScan polls the scan status and reports the terminal outcome. It
// stops on done/failed, on repeated status failures, or after ~3 minutes.
function watchScan(button) {
  let polls = 0;
  let failures = 0;
  const timer = window.setInterval(async () => {
    polls += 1;
    let status = null;
    try {
      status = await api.scanStatus();
      failures = 0;
    } catch {
      failures += 1;
      if (failures >= 3 || polls >= 120) {
        window.clearInterval(timer);
        setBusy(button, false);
        toast('Scan status unavailable. Check the logs.', 'error');
      }
      return;
    }
    if (!status) {
      if (polls >= 120) {
        window.clearInterval(timer);
        setBusy(button, false);
      }
      return;
    }
    if (status.state === 'queued') {
      setBusy(button, true, 'Scan queued…');
    } else if (status.state === 'running') {
      setBusy(button, true, 'Scanning…');
    } else if (status.state === 'done' || status.state === 'failed') {
      window.clearInterval(timer);
      setBusy(button, false);
      if (status.state === 'done') {
        const found = status.found ?? 0;
        const linked = status.autolinked ?? 0;
        const pruned = status.pruned ?? 0;
        toast(
          `Scan finished: ${found} found, ${linked} linked, ${pruned} removed.`,
          'ok',
        );
      } else {
        toast(`Scan failed: ${status.error || 'unknown error'}`, 'error');
      }
      api
        .getStats()
        .then((stats) => {
          if (!stats) return;
          setStat('artists', stats.artists);
          setStat('release_groups', stats.release_groups);
          setStat('ignored', stats.ignored ?? 0);
          setStat('unlinked_files', stats.files.unlinked);
          setStat('total_files', stats.files.total);
        })
        .catch(() => {
          /* counts are optional */
        });
      return;
    }
    if (polls >= 120) {
      window.clearInterval(timer);
      setBusy(button, false);
    }
  }, 1500);
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
