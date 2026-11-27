// Artists home page: grid + add-artist dialog.
// Ported from `src/pages/index.astro` — Astro guards dropped, runtime identical.

import {
  api,
  extractMbid,
  MBID_RE,
} from './api.js';
import { artistCard } from './cards.js';
import { plural } from './format.js';
import {
  byId,
  clear,
  el,
  emptyState,
  setBusy,
  setFormStatus,
  skeletonGrid,
  artistAvatar,
  toast,
} from './ui.js';

function initArtistsPage() {
  const grid = byId('grid');
  const empty = byId('empty');
  const status = byId('status');
  const filter = byId('filter');
  const refresh = byId('refresh');
  const dialog = byId('add-dialog');
  const form = byId('add-form');
  const mbidInput = byId('mbid');
  const artistSearchInput = byId('add-artist-search');
  const artistSearchButton = byId('artist-search-submit');
  const artistResults = byId('artist-results');
  const addStatus = byId('add-status');
  const addSubmit = byId('add-submit');

  let artists = [];
  let loadToken = 0;
  let filterTimer;
  let artistSearchController = null;
  let artistSearchToken = 0;
  let addInFlight = false;

  function render() {
    const query = filter.value.trim();

    clear(grid);
    const fragment = document.createDocumentFragment();
    for (const artist of artists) fragment.append(artistCard(artist));
    grid.append(fragment);

    if (artists.length === 0) {
      empty.hidden = false;
      const state = query
        ? emptyState(
            'No matches',
            'Try a different name or clear the filter.',
          )
        : emptyState(
            'No artists yet',
            'Add an artist by MusicBrainz ID and Coda will import the discography in the background.',
          );
      if (!query) {
        const button = el('button', 'btn btn--primary', 'Add artist');
        button.type = 'button';
        button.addEventListener('click', openAdd);
        state.append(button);
      }
      empty.replaceChildren(state);
    } else {
      empty.hidden = true;
      empty.replaceChildren();
    }

    status.textContent = query
      ? `${artists.length} ${artists.length === 1 ? 'match' : 'matches'}`
      : plural(artists.length, 'artist');
  }

  async function load(quiet = false) {
    const token = ++loadToken;
    const query = filter.value.trim();

    if (!quiet) {
      skeletonGrid(grid, 10, 'artist');
      status.textContent = 'Loading artists…';
    } else {
      status.textContent = 'Searching…';
    }

    try {
      const result = await api.listAllArtists({
        query,
        includeTrackStats: true,
      });
      if (token !== loadToken) return;
      artists = result.items;
      render();
    } catch (error) {
      if (token !== loadToken) return;
      clear(grid);
      empty.hidden = false;
      const message =
        error instanceof Error ? error.message : String(error);
      empty.replaceChildren(emptyState('Could not load artists', message));
      status.textContent = 'Failed to load';
    }
  }

  function setResultButtonsDisabled(disabled) {
    for (const button of artistResults.querySelectorAll('button')) {
      button.disabled = disabled;
    }
  }

  function renderArtistResults(items) {
    clear(artistResults);
    if (items.length === 0) return;

    const fragment = document.createDocumentFragment();
    for (const item of items) {
      const row = el('div', 'pick artist-result');

      const art = el('span', 'pick__art');
      art.append(
        artistAvatar(item.id, item.name, '', { variant: 'initials' }),
      );

      const main = el('span', 'pick__main');
      main.append(el('span', 'pick__title', item.name));
      const meta = [item.disambiguation, item.type, item.country]
        .filter(Boolean)
        .join(' · ');
      if (meta) main.append(el('span', 'pick__meta', meta));

      const add = el('button', 'btn btn--sm btn--primary', 'Add');
      add.type = 'button';
      add.addEventListener('click', () => {
        void addArtistById(item.id, item.name, add);
      });

      row.append(art, main, add);
      fragment.append(row);
    }
    artistResults.append(fragment);
  }

  async function runArtistSearch(query) {
    const token = ++artistSearchToken;
    artistSearchController?.abort();
    const controller = new AbortController();
    artistSearchController = controller;

    clear(artistResults);
    artistSearchButton.disabled = true;
    artistSearchButton.textContent = 'Searching…';
    setFormStatus(addStatus, 'Searching MusicBrainz…');

    try {
      const page = await api.searchMusicBrainzArtists(
        query,
        controller.signal,
      );
      if (token !== artistSearchToken) return;

      const items = page?.items ?? [];
      renderArtistResults(items);
      setFormStatus(addStatus, items.length ? '' : 'No artists found.');
    } catch (error) {
      if (controller.signal.aborted || token !== artistSearchToken) return;
      clear(artistResults);
      const message =
        error instanceof Error ? error.message : String(error);
      setFormStatus(addStatus, `Search failed: ${message}`, 'error');
    } finally {
      if (token === artistSearchToken) {
        artistSearchButton.disabled = false;
        artistSearchButton.textContent = 'Search';
      }
    }
  }

  function triggerArtistSearch() {
    if (artistSearchButton.disabled) return;

    const query = artistSearchInput.value.trim();
    if (query.length < 2) {
      clear(artistResults);
      setFormStatus(addStatus, 'Type at least 2 characters.', 'info');
      return;
    }

    void runArtistSearch(query);
  }

  async function addArtistById(id, displayName, button) {
    if (addInFlight) return;
    addInFlight = true;
    setResultButtonsDisabled(true);
    setBusy(button, true, 'Adding…');
    setFormStatus(addStatus, `Adding ${displayName ?? 'artist'}…`);

    try {
      const response = await api.addArtist(id);

      if (!response.ok) {
        if (response.status === 403) {
          setFormStatus(
            addStatus,
            'This artist was permanently deleted. Allow it again from Permanently deleted first.',
            'error',
          );
          return;
        }
        let message = response.statusText;
        try {
          const body = await response.json();
          if (typeof body?.error === 'string' && body.error) {
            message = body.error;
          }
        } catch {
          /* keep status text */
        }
        setFormStatus(addStatus, message, 'error');
        return;
      }

      let name = displayName ?? 'Artist';
      try {
        const body = await response.json();
        if (typeof body?.name === 'string') name = body.name;
      } catch {
        /* the import still started */
      }

      toast(`${name} added. Discography import started.`, 'ok');
      closeAdd();
      await load(true);
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      setFormStatus(addStatus, `Could not add artist: ${message}`, 'error');
    } finally {
      addInFlight = false;
      setResultButtonsDisabled(false);
      setBusy(button, false);
    }
  }

  function openAdd() {
    form.reset();
    artistSearchInput.value = '';
    artistSearchButton.disabled = false;
    artistSearchButton.textContent = 'Search';
    clear(artistResults);
    artistSearchController?.abort();
    artistSearchToken += 1;
    setFormStatus(addStatus, '');
    dialog.showModal();
    if (window.matchMedia('(pointer: fine)').matches) {
      requestAnimationFrame(() => artistSearchInput.focus());
    }
  }

  function closeAdd() {
    artistSearchController?.abort();
    artistSearchToken += 1;
    dialog.close();
  }

  byId('add-open').addEventListener('click', openAdd);
  byId('add-close').addEventListener('click', closeAdd);
  byId('add-cancel').addEventListener('click', closeAdd);
  dialog.addEventListener('click', (event) => {
    if (event.target === dialog) closeAdd();
  });

  filter.addEventListener('input', () => {
    window.clearTimeout(filterTimer);
    filterTimer = window.setTimeout(() => void load(true), 250);
  });
  artistSearchButton.addEventListener('click', triggerArtistSearch);
  artistSearchInput.addEventListener('keydown', (event) => {
    if (event.key !== 'Enter') return;
    event.preventDefault();
    triggerArtistSearch();
  });
  refresh.addEventListener('click', () => load());

  form.addEventListener('submit', (event) => {
    event.preventDefault();
    const id = extractMbid(mbidInput.value);

    if (!MBID_RE.test(id)) {
      setFormStatus(
        addStatus,
        'That doesn’t look like a MusicBrainz artist MBID or URL.',
        'error',
      );
      mbidInput.focus();
      return;
    }

    void addArtistById(id, undefined, addSubmit);
  });

  load();
}

initArtistsPage();
