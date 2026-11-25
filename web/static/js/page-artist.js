// Artist detail page: hero, import status, primary + guest release groups.
// Ported from `src/pages/artist.astro` — Astro page-swap hooks dropped, runtime identical.

import {
  api,
  creditName,
  mbUrl,
} from './api.js';
import { hydrateReleaseLine, releaseGroupCard } from './cards.js';
import { formatDateTime, plural } from './format.js';
import {
  byId,
  clear,
  el,
  emptyState,
  mapLimit,
  normalize,
  setBusy,
  setSharedArtName,
  sharedArtName,
  artistAvatar,
  coverArt,
  toast,
} from './ui.js';

const GROUP_TYPE_ORDER = ['album', 'ep', 'single', 'other'];

function normalizeGroupType(value) {
  return value === 'album' || value === 'ep' || value === 'single'
    ? value
    : 'other';
}

function matchesQuery(group, query) {
  if (!query) return true;
  return normalize(`${group.title} ${creditName(group.credit)}`).includes(
    query,
  );
}

function sortTypeGroups(typeGroups) {
  typeGroups.sort((a, b) => {
    const dateA = a.release?.date ?? '';
    const dateB = b.release?.date ?? '';
    if (dateA && dateB && dateA !== dateB) {
      return dateB.localeCompare(dateA);
    }
    if (dateA && !dateB) return -1;
    if (!dateA && dateB) return 1;
    return a.title.localeCompare(b.title, undefined, {
      sensitivity: 'base',
    });
  });
}

function bustCoverImgs() {
  // The endpoint sends a 24h max-age, so bust the <img> URLs or the
  // browser would keep showing the previous bytes.
  const stamp = String(Date.now());
  for (const img of document.querySelectorAll('img.cover-img')) {
    try {
      const url = new URL(img.currentSrc || img.src, location.origin);
      url.searchParams.set('t', stamp);
      img.src = url.toString();
    } catch {
      /* keep the previous art on weird URLs */
    }
  }
}

function initArtistPage() {
  const status = byId('status');
  const content = byId('content');
  const crumb = byId('crumb');
  const heroArt = byId('hero-art');
  const artistName = byId('artist-name');
  const artistMb = byId('artist-mb');
  const artistMbRow = byId('artist-mb-row');
  const artistMeta = byId('artist-meta');
  const artistActions = byId('artist-actions');
  const importStatusNode = byId('import-status');
  const trackTotal = byId('track-total');
  const linkedBar = byId('linked-bar');
  const linkedFill = byId('linked-fill');
  const groupStatus = byId('group-status');
  const legendComplete = byId('legend-complete');
  const legendPartial = byId('legend-partial');
  const legendMissing = byId('legend-missing');
  const filter = byId('filter');
  const groupsNode = byId('groups');
  const groupsEmpty = byId('groups-empty');
  const appearsSection = byId('appears-section');
  const appearsGroupsNode = byId('appears-groups');
  const appearsEmpty = byId('appears-empty');
  const appearsStatus = byId('appears-status');

  const artistID = new URLSearchParams(location.search).get('id') ?? '';
  const releaseCache = new Map();
  // Same-hue tile shown instantly; replaced with the named artist on load.
  {
    const heroName = artistID ? sharedArtName('artist', artistID) : null;
    if (heroName) {
      const placeholder = artistAvatar(artistID, '', {
        variant: 'initials',
      });
      for (const label of placeholder.querySelectorAll('span')) {
        label.remove();
      }
      setSharedArtName(placeholder, heroName);
      heroArt.replaceChildren(placeholder);
    }
  }
  let groups = [];
  let appearsGroups = [];
  let artist = null;
  let coverInFlight = false;
  let importPollTimer;
  let importPollToken = 0;
  let lastImportState = 'idle';

  const stopImportPolling = () => {
    window.clearTimeout(importPollTimer);
    importPollToken += 1;
  };
  // MPA: timers die with the page, but stop explicitly on hide.
  window.addEventListener('pagehide', stopImportPolling);

  function showError(message) {
    status.hidden = false;
    status.classList.add('is-error');
    status.textContent = message;
    content.hidden = true;
  }

  function showNotInLibrary(collab) {
    document.title = `${collab.name} · Coda`;
    crumb.textContent = collab.name;

    heroArt.replaceChildren(
      artistAvatar(collab.id, collab.name, '', { variant: 'initials' }),
    );
    setSharedArtName(
      heroArt.firstElementChild,
      sharedArtName('artist', collab.id),
    );
    artistName.textContent = collab.name;
    artistMb.href = mbUrl.artist(collab.id);
    artistMb.setAttribute(
      'aria-label',
      `Open ${collab.name} on MusicBrainz`,
    );
    artistMbRow.hidden = false;

    const meta = ['Not in your library yet'];
    if (collab.disambiguation) meta.push(collab.disambiguation);
    if (collab['sort-name'] && collab['sort-name'] !== collab.name) {
      meta.push(`Sort name: ${collab['sort-name']}`);
    }
    artistMeta.textContent = meta.join(' · ');

    const addButton = el(
      'button',
      'btn btn--primary btn--icon btn--icon-plus',
      'Add to library',
    );
    addButton.type = 'button';
    addButton.addEventListener('click', () => addToLibrary(addButton));
    artistActions.replaceChildren(addButton);

    importStatusNode.hidden = true;
    trackTotal.textContent = '—';
    setLinkedBar(0, 0);
    groupStatus.textContent = '';
    appearsSection.hidden = true;
    groupsEmpty.hidden = true;
    groupsEmpty.replaceChildren();
    groupsNode.replaceChildren(
      emptyState(
        'Not in your library yet',
        'Add this artist to import their discography.',
      ),
    );

    status.hidden = true;
    content.hidden = false;
  }

  function setImportStatus(importStatus) {
    if (!importStatus || importStatus.state === 'idle') {
      importStatusNode.hidden = true;
      importStatusNode.textContent = '';
      importStatusNode.dataset.state = 'idle';
      return;
    }

    let message = '';
    switch (importStatus.state) {
      case 'queued':
        message = 'Discography import queued…';
        break;
      case 'running':
        message = 'Importing discography…';
        break;
      case 'done':
        message = 'Discography updated';
        if (importStatus.finished_at) {
          message += ` · ${formatDateTime(importStatus.finished_at)}`;
        }
        break;
      case 'failed':
        message = importStatus.error
          ? `Import failed: ${importStatus.error}`
          : 'Import failed';
        break;
    }

    importStatusNode.hidden = false;
    importStatusNode.textContent = message;
    importStatusNode.dataset.state = importStatus.state;
  }

  async function pollImportStatus(token, announce) {
    if (!artist) return;

    try {
      const importStatus = await api.getArtistImportStatus(artist.id);
      if (token !== importPollToken || !artist) return;

      setImportStatus(importStatus);

      const state = importStatus?.state ?? 'idle';
      const wasPending =
        lastImportState === 'queued' || lastImportState === 'running';
      lastImportState = state;

      if (state === 'queued' || state === 'running') {
        importPollTimer = window.setTimeout(
          () => void pollImportStatus(token, announce),
          2000,
        );
        return;
      }

      if (wasPending && state === 'done') {
        if (announce) toast('Discography updated.', 'ok');
        await reloadGroups();
      }
    } catch {
      if (token !== importPollToken || !artist) return;
      setImportStatus({
        artist_id: artist.id,
        state: 'failed',
        error: 'Could not check import status.',
      });
    }
  }

  function startImportPolling(announce) {
    if (!artist) return;
    importPollToken += 1;
    window.clearTimeout(importPollTimer);
    void pollImportStatus(importPollToken, announce);
  }

  async function refreshDiscography(button) {
    if (!artist) return;
    setBusy(button, true, 'Queueing…');
    try {
      const importStatus = await api.refreshArtist(artist.id);
      if (!importStatus) {
        toast('Artist not found.', 'error');
        return;
      }
      setImportStatus(importStatus);
      lastImportState = importStatus.state;
      toast('Discography refresh queued.', 'ok');
      startImportPolling(true);
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      toast(`Could not queue refresh: ${message}`, 'error');
    } finally {
      setBusy(button, false);
    }
  }

  async function refreshArtistCover(button) {
    if (!artist || coverInFlight) return;
    coverInFlight = true;
    setBusy(button, true, 'Refreshing…');
    try {
      const row = await api.refreshArtistCover(artist.id);
      if (!row) {
        toast('No photo found for this artist.', 'info');
        return;
      }
      bustCoverImgs();
      toast(`Artist photo refreshed (${row.source}).`, 'ok');
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      toast(`Could not refresh photo: ${message}`, 'error');
    } finally {
      coverInFlight = false;
      setBusy(button, false);
    }
  }

  async function addToLibrary(button) {
    setBusy(button, true, 'Adding…');
    try {
      const response = await api.addArtist(artistID);
      if (
        response.status === 200 ||
        response.status === 201 ||
        response.status === 304
      ) {
        toast('Artist added. Importing discography…', 'ok');
        await load();
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
      toast(`Could not add artist: ${message}`, 'error');
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      toast(`Could not add artist: ${message}`, 'error');
    } finally {
      setBusy(button, false);
    }
  }

  async function deleteArtist(button) {
    if (!artist) return;
    const name = artist.name;
    if (
      !window.confirm(
        `Remove ${name} from the library? The saved discography is deleted and linked files become unlinked. Audio files are kept.`,
      )
    ) {
      return;
    }
    setBusy(button, true, 'Removing…');
    try {
      const response = await api.deleteArtist(artist.id);
      if (response.status === 404) {
        toast('Artist not found.', 'error');
        return;
      }
      if (!response.ok) {
        let message = response.statusText;
        try {
          const body = await response.json();
          if (typeof body?.error === 'string' && body.error) {
            message = body.error;
          }
        } catch {
          /* keep status text */
        }
        toast(`Could not remove artist: ${message}`, 'error');
        return;
      }
      stopImportPolling();
      let message = `${name} removed.`;
      try {
        const body = await response.json();
        const pruned = typeof body?.pruned === 'number' ? body.pruned : 0;
        if (pruned > 0) {
          message += ` ${pruned === 1 ? '1 collaborator' : `${pruned} collaborators`} cleaned up.`;
        }
      } catch {
        /* count is optional */
      }
      toast(message, 'ok');
      window.location.assign('/');
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      toast(`Could not remove artist: ${message}`, 'error');
    } finally {
      setBusy(button, false);
    }
  }

  function setLinkedBar(linkedCount, total) {
    const percent =
      total > 0 ? Math.round((linkedCount / total) * 100) : 0;
    linkedFill.style.width = `${percent}%`;
    linkedBar.dataset.state =
      percent >= 100 ? 'complete' : percent > 0 ? 'partial' : 'empty';
    linkedBar.setAttribute('aria-valuenow', String(percent));
    linkedBar.title =
      total > 0 ? `${linkedCount} of ${plural(total, 'track')} linked` : '';
  }

  function setTrackStats(current) {
    if (!current.stats) {
      trackTotal.textContent = '—';
      setLinkedBar(0, 0);
      return;
    }
    const total = current.stats.track_count;
    const linkedCount = current.stats.linked_track_count;
    trackTotal.textContent = `${linkedCount} / ${total}`;
    setLinkedBar(linkedCount, total);
  }

  async function hydrateCards(entries, updateTotals = true) {
    await mapLimit(entries, 4, ({ group, line }) =>
      hydrateReleaseLine(line, group, releaseCache),
    );
    if (!updateTotals) return;

    let complete = 0;
    let partial = 0;
    let missing = 0;
    for (const { line } of entries) {
      if (line.classList.contains('is-complete')) complete += 1;
      else if (line.classList.contains('is-partial')) partial += 1;
      else if (line.classList.contains('is-missing')) missing += 1;
    }
    legendComplete.textContent = String(complete);
    legendPartial.textContent = String(partial);
    legendMissing.textContent = String(missing);
  }

  const GROUP_TYPE_LABELS = {
    album: 'Albums',
    ep: 'EPs',
    single: 'Singles',
    other: 'Other',
  };

  function renderInto(node, visibleList, showCredit) {
    clear(node);
    const buckets = {
      album: [],
      ep: [],
      single: [],
      other: [],
    };
    for (const group of visibleList) {
      buckets[normalizeGroupType(group.type)].push(group);
    }

    const fragment = document.createDocumentFragment();
    const entries = [];

    for (const type of GROUP_TYPE_ORDER) {
      const typeGroups = buckets[type];
      if (typeGroups.length === 0) continue;
      sortTypeGroups(typeGroups);

      const section = el('section', 'rg-section');
      const heading = el(
        'h3',
        'rg-section__title',
        GROUP_TYPE_LABELS[type],
      );
      heading.append(
        el('span', 'rg-section__count', String(typeGroups.length)),
      );

      const head = el('div', 'rg-section__head');
      head.append(heading);

      const grid = el('div', 'rg-grid');
      for (const group of typeGroups) {
        const { card, releaseLine } = releaseGroupCard(group, {
          showCredit,
          showType: false,
        });
        grid.append(card);
        entries.push({ group, line: releaseLine });
      }

      section.append(head, grid);
      fragment.append(section);
    }
    node.append(fragment);
    return entries;
  }

  function renderGroups() {
    const query = normalize(filter.value);
    const visible = groups.filter((group) => matchesQuery(group, query));
    const visibleAppears = appearsGroups.filter((group) =>
      matchesQuery(group, query),
    );

    const entries = renderInto(groupsNode, visible, false);
    const appearsEntries = renderInto(
      appearsGroupsNode,
      visibleAppears,
      true,
    );

    if (visible.length === 0) {
      groupsEmpty.hidden = false;
      const state = groups.length
        ? emptyState(
            'No matching release groups',
            'Try a different filter.',
          )
        : emptyState(
            'No release groups yet',
            'The discography import may still be running. Refresh in a moment.',
          );
      if (!groups.length) {
        const button = el(
          'button',
          'btn btn--primary btn--icon btn--icon-refresh',
          'Refresh discography',
        );
        button.type = 'button';
        button.addEventListener('click', () => refreshDiscography(button));
        state.append(button);
      }
      groupsEmpty.replaceChildren(state);
    } else {
      groupsEmpty.hidden = true;
      groupsEmpty.replaceChildren();
    }

    groupStatus.textContent = query
      ? `${visible.length} of ${plural(groups.length, 'release group')}`
      : plural(groups.length, 'release group');

    if (appearsGroups.length === 0) {
      appearsSection.hidden = true;
      appearsEmpty.hidden = true;
      appearsEmpty.replaceChildren();
      appearsStatus.textContent = '';
    } else {
      appearsSection.hidden = false;
      if (visibleAppears.length === 0) {
        appearsEmpty.hidden = false;
        appearsEmpty.replaceChildren(
          emptyState(
            'No matching guest appearances',
            'Try a different filter.',
          ),
        );
      } else {
        appearsEmpty.hidden = true;
        appearsEmpty.replaceChildren();
      }
      appearsStatus.textContent = query
        ? `${visibleAppears.length} of ${plural(appearsGroups.length, 'release group')}`
        : plural(appearsGroups.length, 'release group');
    }

    void hydrateCards(entries);
    void hydrateCards(appearsEntries, false);
  }

  async function reloadGroups() {
    if (!artist) return;

    groupStatus.textContent = 'Refreshing…';
    appearsStatus.textContent = '';
    try {
      const [groupResult, appearsResult, artistResult] = await Promise.all([
        api.listAllReleaseGroups({
          artist: artist.id,
          includeRelease: true,
          primary: true,
        }),
        api.listAllReleaseGroups({
          artist: artist.id,
          includeRelease: true,
          primary: false,
        }),
        api.getArtist(artist.id, true),
      ]);
      groups = groupResult.items;
      appearsGroups = appearsResult.items;
      if (artistResult) {
        artist = artistResult;
        setTrackStats(artist);
      }
      releaseCache.clear();
      renderGroups();
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      toast(`Could not reload discography: ${message}`, 'error');
      renderGroups();
    }
  }

  async function load() {
    if (!artistID) {
      showError('Missing artist id. Open an artist from the library.');
      return;
    }

    status.hidden = false;
    status.classList.remove('is-error');
    status.textContent = 'Loading artist…';
    content.hidden = true;

    try {
      const [initialArtist, groupResult, appearsResult] = await Promise.all(
        [
          api.getArtist(artistID, true),
          api.listAllReleaseGroups({
            artist: artistID,
            includeRelease: true,
            primary: true,
          }),
          api.listAllReleaseGroups({
            artist: artistID,
            includeRelease: true,
            primary: false,
          }),
        ],
      );

      let artistResult = initialArtist;
      if (!artistResult) {
        // Hidden by the library-only default: probe the full view to see
        // if this is a collaboration-only artist we can add.
        let collab = null;
        try {
          collab = await api.getArtist(artistID, false, true);
        } catch {
          collab = null;
        }
        if (!collab) {
          showError('Artist not found.');
          return;
        }
        if (collab.in_library !== 1) {
          groups = [];
          appearsGroups = [];
          releaseCache.clear();
          showNotInLibrary(collab);
          return;
        }
        // Joined the library between the two reads.
        artistResult = collab;
      }

      artist = artistResult;
      groups = groupResult.items;
      appearsGroups = appearsResult.items;
      releaseCache.clear();

      document.title = `${artist.name} · Coda`;
      crumb.textContent = artist.name;

      heroArt.replaceChildren(
        coverArt('artist', artist.id, '', {
          eager: true,
          fallback: () =>
            artistAvatar(artist.id, artist.name, '', {
              variant: 'initials',
            }),
        }),
      );
      setSharedArtName(
        heroArt.firstElementChild,
        sharedArtName('artist', artist.id),
      );
      artistName.textContent = artist.name;

      const meta = [];
      if (artist.disambiguation) meta.push(artist.disambiguation);
      if (artist['sort-name'] && artist['sort-name'] !== artist.name) {
        meta.push(`Sort name: ${artist['sort-name']}`);
      }
      artistMeta.textContent = meta.join(' · ') || 'MusicBrainz artist';

      artistMb.href = mbUrl.artist(artist.id);
      artistMb.setAttribute(
        'aria-label',
        `Open ${artist.name} on MusicBrainz`,
      );
      artistMbRow.hidden = false;
      const photoButton = el(
        'button',
        'btn btn--secondary btn--icon btn--icon-photo',
        'Refresh photo',
      );
      photoButton.type = 'button';
      photoButton.title =
        'Fetch the artist photo again (Deezer, then Wikimedia Commons)';
      photoButton.addEventListener(
        'click',
        () => void refreshArtistCover(photoButton),
      );
      artistActions.append(photoButton);
      const refreshButton = el(
        'button',
        'btn btn--secondary btn--icon btn--icon-refresh',
        'Refresh discography',
      );
      refreshButton.type = 'button';
      refreshButton.addEventListener('click', () =>
        refreshDiscography(refreshButton),
      );
      const deleteButton = el(
        'button',
        'btn btn--danger btn--icon btn--icon-trash',
        'Remove artist',
      );
      deleteButton.type = 'button';
      deleteButton.addEventListener('click', () =>
        deleteArtist(deleteButton),
      );
      artistActions.replaceChildren(photoButton, refreshButton, deleteButton);

      setTrackStats(artist);

      status.hidden = true;
      content.hidden = false;
      renderGroups();
      startImportPolling(false);
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      showError(`Could not load artist: ${message}`);
    }
  }

  filter.addEventListener('input', renderGroups);
  load();
}

initArtistPage();
