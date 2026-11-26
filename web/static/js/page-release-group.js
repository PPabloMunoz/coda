// Release-group detail page: saved release panel + tracklist + switch dialog.
// Ported from `src/pages/release-group.astro` — Astro guards dropped, runtime identical.

import {
  api,
  extractMbid,
  fmtDuration,
  MBID_RE,
  mbUrl,
} from './api.js';
import { baseName, plural, typeLabel, yearOf } from './format.js';
import {
  byId,
  clear,
  creditFragment,
  el,
  emptyState,
  externalLink,
  imageUploadButton,
  link,
  mbLogoLink,
  setBusy,
  setFormStatus,
  setSharedArtName,
  sharedArtName,
  coverArt,
  discArt,
  toast,
} from './ui.js';

function renderFileCell(files) {
  const cell = el('td');
  if (!files || files.length === 0) {
    cell.append(el('span', 'chip', 'No file'));
    return cell;
  }

  const first = files[0];
  const anchor = link(
    `/file?id=${first.id}`,
    baseName(first.path),
    'track-file',
  );
  anchor.title = files.map((file) => file.path).join('\n');
  cell.append(anchor);
  if (files.length > 1) {
    cell.append(el('span', 'chip', `+${files.length - 1}`));
  }
  return cell;
}

function renderTrack(track, options = {}) {
  const row = el('tr');

  const disc = el('td', 'num', String(track.disc));
  const number = el('td', 'num', track.number || String(track.position));

  const titleCell = el('td');
  const trackTitle = externalLink(
    mbUrl.recording(track.recording_id),
    track.title,
    'track-title',
  );
  trackTitle.title = 'Open recording on MusicBrainz';
  titleCell.append(trackTitle);

  const creditCell = el('td', 'credit-line');
  creditCell.append(creditFragment(track.credit));

  const length = el('td', 'num', fmtDuration(track.dur_ms));

  const mbCell = el('td');
  mbCell.append(
    mbLogoLink(
      mbUrl.track(track.id),
      'track-link',
      `Open ${track.title} on MusicBrainz`,
    ),
  );

  row.append(
    ...(options.hideDisc ? [] : [disc]),
    number,
    titleCell,
    creditCell,
    length,
    mbCell,
    renderFileCell(track.files),
  );
  return row;
}

function renderTrackTable(tracks, showDisc) {
  const tableWrap = el('div', 'table-wrap');
  const table = el('table', 'data-table');
  const thead = el('thead');
  const headRow = el('tr');
  const labels = showDisc
    ? ['Disc', '#', 'Title', 'Artist', 'Length', 'MusicBrainz', 'Local file']
    : ['#', 'Title', 'Artist', 'Length', 'MusicBrainz', 'Local file'];
  for (const label of labels) {
    headRow.append(el('th', '', label));
  }
  thead.append(headRow);
  table.append(thead);
  const tbody = el('tbody');
  if (tracks.length === 0) {
    const row = el('tr');
    const cell = el('td', 'muted', 'No tracks stored for this release.');
    cell.colSpan = labels.length;
    row.append(cell);
    tbody.append(row);
  } else {
    const fragment = document.createDocumentFragment();
    for (const track of tracks) {
      fragment.append(
        showDisc ? renderTrack(track) : renderTrack(track, { hideDisc: true }),
      );
    }
    tbody.append(fragment);
  }
  table.append(tbody);
  tableWrap.append(table);
  return tableWrap;
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

function initReleaseGroupPage() {
  const status = byId('status');
  const content = byId('content');
  const crumb = byId('crumb');
  const crumbArtist = byId('crumb-artist');
  const heroArt = byId('hero-art');
  const title = byId('title');
  const credit = byId('credit');
  const groupMb = byId('group-mb');
  const groupMbRow = byId('group-mb-row');
  const actions = byId('actions');
  const typeNode = byId('type');
  const savedStatus = byId('saved-status');
  const releasePanel = byId('release-panel');
  const tracksSection = byId('tracks-section');
  const tracksMeta = byId('tracks-meta');
  const tracksWrap = byId('tracks-wrap');
  const trackTotal = byId('track-total');

  const groupID = new URLSearchParams(location.search).get('id') ?? '';
  // Same-hue tile shown instantly; replaced with the named release on load.
  {
    const heroName = groupID ? sharedArtName('group', groupID) : null;
    if (heroName) {
      const placeholder = discArt(groupID);
      setSharedArtName(placeholder, heroName);
      byId('hero-art').replaceChildren(placeholder);
    }
  }

  const dialog = byId('switch-dialog');
  const form = byId('switch-form');
  const mbidInput = byId('switch-mbid');
  const switchCurrent = byId('switch-current');
  const switchStatus = byId('switch-status');
  const switchSubmit = byId('switch-submit');

  let currentGroup = null;
  let currentRelease = null;
  let heroRefreshButton = null;
  let heroUploadButton = null;
  let heroUploadInput = null;
  let heroSwitchButton = null;
  let switchInFlight = false;
  let coverInFlight = false;

  function openSwitch(release) {
    if (!currentGroup || !release) return;
    form.reset();
    setFormStatus(switchStatus, '');
    switchCurrent.textContent = `Current: ${release.title} · ${release.id}`;
    dialog.showModal();
    if (window.matchMedia('(pointer: fine)').matches) {
      requestAnimationFrame(() => mbidInput.focus());
    }
  }

  function closeSwitch() {
    dialog.close();
  }

  async function switchRelease(newReleaseId) {
    if (!currentGroup || switchInFlight) return;
    switchInFlight = true;
    setBusy(switchSubmit, true, 'Switching…');
    setFormStatus(switchStatus, 'Fetching the release from MusicBrainz…');
    try {
      const response = await api.updateReleaseGroup(
        currentGroup.id,
        newReleaseId,
      );
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
        setFormStatus(switchStatus, message, 'error');
        return;
      }
      toast('Saved release switched.', 'ok');
      closeSwitch();
      await load();
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      setFormStatus(
        switchStatus,
        `Could not switch release: ${message}`,
        'error',
      );
    } finally {
      switchInFlight = false;
      setBusy(switchSubmit, false);
    }
  }

  async function refreshCover(release, button) {
    await runCoverRefresh(button, () =>
      api.refreshReleaseCover(release.id),
    );
  }

  async function runCoverRefresh(button, refresh) {
    if (coverInFlight) return;
    coverInFlight = true;
    setBusy(button, true, 'Refreshing…');
    try {
      const row = await refresh();
      if (!row) {
        toast('No cover art found.', 'info');
        return;
      }
      bustCoverImgs();
      toast(`Cover refreshed (${row.source}).`, 'ok');
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      toast(`Could not refresh cover: ${message}`, 'error');
    } finally {
      coverInFlight = false;
      setBusy(button, false);
    }
  }

  function rebuildHeroCover() {
    if (!currentGroup) return;
    const heroCover = coverArt('group', currentGroup.id, '', { eager: true });
    setSharedArtName(heroCover, sharedArtName('group', currentGroup.id));
    heroArt.replaceChildren(heroCover);
    bustCoverImgs();
  }

  async function uploadCover(file, button) {
    if (!currentRelease || coverInFlight) return;
    coverInFlight = true;
    setBusy(button, true, 'Uploading…');
    try {
      const row = await api.uploadReleaseCover(currentRelease.id, file);
      if (!row) {
        toast('Upload finished with no cover.', 'info');
        return;
      }
      rebuildHeroCover();
      toast(
        'Cover uploaded. Linked files are updating in the background.',
        'ok',
      );
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      toast(`Could not upload cover: ${message}`, 'error');
    } finally {
      coverInFlight = false;
      setBusy(button, false);
    }
  }

  async function deleteGroup(button) {
    if (!currentGroup) return;
    const groupTitle = currentGroup.title;
    const artistId = currentGroup.credit?.[0]?.artist_id;
    if (
      !window.confirm(
        `Remove "${groupTitle}" from the library? Its saved release and tracks are deleted and linked files become unlinked. Audio files are kept.`,
      )
    ) {
      return;
    }
    setBusy(button, true, 'Removing…');
    try {
      const response = await api.deleteReleaseGroup(currentGroup.id);
      if (response.status === 404) {
        toast('Release group not found.', 'error');
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
        toast(`Could not remove release group: ${message}`, 'error');
        return;
      }
      toast(`"${groupTitle}" removed.`, 'ok');
      window.location.assign(
        artistId
          ? `/artist?id=${encodeURIComponent(artistId)}`
          : '/',
      );
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      toast(`Could not remove release group: ${message}`, 'error');
    } finally {
      setBusy(button, false);
    }
  }

  function showError(message) {
    status.hidden = false;
    status.classList.add('is-error');
    status.textContent = message;
    content.hidden = true;
  }

  function renderRelease(release, group) {
    currentRelease = release;
    const hasRelease = Boolean(release);
    if (heroRefreshButton) heroRefreshButton.hidden = !hasRelease;
    if (heroUploadButton) heroUploadButton.hidden = !hasRelease;
    if (heroSwitchButton) heroSwitchButton.hidden = !hasRelease;

    if (!release) {
      savedStatus.textContent = 'None';
      trackTotal.textContent = '—';
      tracksSection.hidden = true;
      releasePanel.replaceChildren(
        emptyState(
          'No release saved for this group',
          'The discography import may still be running. Refresh in a moment.',
        ),
      );
      return;
    }

    const year = yearOf(release.date);
    savedStatus.textContent = year || 'Saved';

    const panel = el('div', 'release-panel');
    const body = el('div', 'release-panel__body');
    body.append(
      el(
        'p',
        'eyebrow',
        release.status ? typeLabel(release.status) : 'Release',
      ),
    );
    if (release.date) {
      body.append(el('p', 'muted', `Released ${release.date}`));
    }
    const extRow = el('p', 'ext-links');
    extRow.append(
      mbLogoLink(
        mbUrl.release(release.id),
        'ext-link',
        `Open ${release.title} on MusicBrainz`,
        'MusicBrainz',
      ),
    );
    body.append(extRow);

    panel.append(body);
    releasePanel.replaceChildren(panel);

    const tracks = release.tracks ?? [];
    const linked = tracks.filter(
      (track) => (track.files?.length ?? 0) > 0,
    ).length;
    trackTotal.textContent =
      tracks.length > 0
        ? `${linked} / ${plural(tracks.length, 'track')}`
        : '—';
    tracksMeta.textContent =
      tracks.length > 0 ? plural(tracks.length, 'track') : '';
    tracksSection.hidden = false;

    clear(tracksWrap);
    const discs = [...new Set(tracks.map((track) => track.disc))].sort(
      (a, b) => a - b,
    );
    if (discs.length > 1) {
      const fragment = document.createDocumentFragment();
      for (const disc of discs) {
        const section = el('section', 'disc-section');
        const head = el('div', 'disc-section__head');
        head.append(
          el('h3', 'disc-section__title', `Disc ${disc}`),
          el(
            'span',
            'disc-section__meta',
            plural(
              tracks.filter((track) => track.disc === disc).length,
              'track',
            ),
          ),
        );
        section.append(head);
        section.append(
          renderTrackTable(
            tracks.filter((track) => track.disc === disc),
            false,
          ),
        );
        fragment.append(section);
      }
      tracksWrap.append(fragment);
    } else {
      tracksWrap.append(renderTrackTable(tracks, false));
    }
  }

  async function load() {
    if (!groupID) {
      showError('Missing release group id. Open one from the library.');
      return;
    }

    status.hidden = false;
    status.classList.remove('is-error');
    status.textContent = 'Loading release group…';
    content.hidden = true;

    try {
      const group = await api.getReleaseGroup(groupID);
      if (!group) {
        showError('Release group not found.');
        return;
      }
      currentGroup = group;

      const release = await api.getReleaseByGroup(group.id, true);

      document.title = `${group.title} · Coda`;
      crumb.textContent = group.title;
      const firstCredit = group.credit?.[0];
      if (firstCredit?.artist_id) {
        crumbArtist.textContent =
          firstCredit.credited_name || firstCredit.name || 'Artist';
        crumbArtist.href = `/artist?id=${encodeURIComponent(firstCredit.artist_id)}`;
      } else {
        crumbArtist.textContent = 'Artists';
        crumbArtist.href = '/';
      }
      const heroCover = coverArt('group', group.id, '', { eager: true });
      setSharedArtName(heroCover, sharedArtName('group', group.id));
      heroArt.replaceChildren(heroCover);
      title.textContent = group.title;
      credit.replaceChildren(creditFragment(group.credit));
      groupMb.href = mbUrl.releaseGroup(group.id);
      groupMb.setAttribute(
        'aria-label',
        `Open ${group.title} on MusicBrainz`,
      );
      groupMbRow.hidden = false;
      typeNode.textContent = typeLabel(group.type);
      const switchButton = el(
        'button',
        'btn btn--secondary btn--icon btn--icon-repeat',
        'Switch release…',
      );
      switchButton.type = 'button';
      switchButton.addEventListener(
        'click',
        () => void openSwitch(currentRelease),
      );
      heroSwitchButton = switchButton;
      const refreshButton = el(
        'button',
        'btn btn--secondary btn--icon btn--icon-refresh',
        'Refresh cover',
      );
      refreshButton.type = 'button';
      refreshButton.title =
        'Fetch the cover art again (Cover Art Archive, then Deezer)';
      refreshButton.addEventListener('click', () => {
        if (currentRelease) void refreshCover(currentRelease, refreshButton);
      });
      heroRefreshButton = refreshButton;
      const { button: uploadButton, input: uploadInput } =
        imageUploadButton(
          'Upload cover',
          'btn btn--secondary btn--icon btn--icon-upload',
          {
            title: 'Upload a JPEG, PNG, or WebP cover (max 15MB)',
            onFile: (file) => void uploadCover(file, uploadButton),
          },
        );
      heroUploadButton = uploadButton;
      heroUploadInput = uploadInput;
      const deleteButton = el(
        'button',
        'btn btn--danger btn--icon btn--icon-trash',
        'Remove group',
      );
      deleteButton.type = 'button';
      deleteButton.addEventListener('click', () =>
        deleteGroup(deleteButton),
      );
      actions.replaceChildren(
        switchButton,
        refreshButton,
        uploadButton,
        uploadInput,
        deleteButton,
      );

      renderRelease(release, group);
      status.hidden = true;
      content.hidden = false;
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      showError(`Could not load release group: ${message}`);
    }
  }

  byId('switch-close').addEventListener('click', closeSwitch);
  byId('switch-cancel').addEventListener('click', closeSwitch);
  dialog.addEventListener('click', (event) => {
    if (event.target === dialog) closeSwitch();
  });
  form.addEventListener('submit', (event) => {
    event.preventDefault();
    const id = extractMbid(mbidInput.value);
    if (!MBID_RE.test(id)) {
      setFormStatus(
        switchStatus,
        'That doesn’t look like a release MBID or URL.',
        'error',
      );
      mbidInput.focus();
      return;
    }
    void switchRelease(id);
  });

  load();
}

initReleaseGroupPage();
