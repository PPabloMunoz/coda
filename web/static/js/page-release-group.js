// Release-group detail page: saved release panel + tracklist + switch dialog.
// Ported from `src/pages/release-group.astro` — Astro guards dropped, runtime identical.

import {
  api,
  extractMbid,
  fmtDuration,
  MBID_RE,
  mbUrl,
} from './api.js';
import {
  baseName,
  coverSourceBadge,
  coverSourceText,
  dirName,
  extension,
  formatBytes,
  plural,
  typeLabel,
  yearOf,
} from './format.js';
import {
  byId,
  clear,
  coverTools,
  creditFragment,
  el,
  emptyState,
  externalLink,
  link,
  mbLogoLink,
  normalize,
  openDeleteDialog,
  setBusy,
  setFormStatus,
  setSharedArtName,
  sharedArtName,
  coverArt,
  discArt,
  toast,
} from './ui.js';

function formatKey(file) {
  return (file.format_hint || extension(file.path)).toUpperCase();
}

function renderFileCell(track, onLink) {
  const cell = el('td');
  const files = track.files || [];
  if (files.length === 0) {
    const button = el(
      'button',
      'btn btn--sm btn--secondary btn--icon btn--icon-link',
      'Link file',
    );
    button.type = 'button';
    button.setAttribute('aria-label', `Link a file to ${track.title}`);
    button.addEventListener('click', () => onLink(track));
    cell.append(button);
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

function renderTrack(track, onLink, options = {}) {
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
    renderFileCell(track, onLink),
  );
  return row;
}

function renderTrackTable(tracks, showDisc, onLink) {
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
        showDisc
          ? renderTrack(track, onLink)
          : renderTrack(track, onLink, { hideDisc: true }),
      );
    }
    tbody.append(fragment);
  }
  table.append(tbody);
  tableWrap.append(table);
  return tableWrap;
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

  const linkDialog = byId('link-dialog');
  const linkTrackFormat = byId('link-track-format');
  const linkTrackTitle = byId('link-track-title');
  const linkTrackMeta = byId('link-track-meta');
  const linkSearch = byId('link-search');
  const linkList = byId('link-list');
  const linkStatus = byId('link-status');
  const linkSubmit = byId('link-submit');

  let currentGroup = null;
  let currentRelease = null;
  let heroSwitchButton = null;
  let heroTools = null;
  let coverMetaToken = 0;
  let switchInFlight = false;
  let coverInFlight = false;
  let linkTrack = null;
  let linkFiles = [];
  let linkSelectedID = null;
  let linkToken = 0;
  let linkInFlight = false;

  function closeLinkDialog() {
    linkDialog.close();
  }

  function openLinkDialog(track) {
    linkTrack = track;
    linkSelectedID = null;
    linkSubmit.disabled = true;
    linkSubmit.textContent = 'Link file';
    linkTrackFormat.textContent = String(
      track.number || track.position || 'TRK',
    );
    linkTrackTitle.textContent = track.title;
    linkTrackMeta.textContent = `Disc ${track.disc} · Track ${track.number || track.position}`;
    linkSearch.value = '';
    setFormStatus(linkStatus, '');
    clear(linkList);
    if (!linkDialog.open) linkDialog.showModal();
    if (window.matchMedia('(pointer: fine)').matches) {
      requestAnimationFrame(() => linkSearch.focus());
    }
    void loadLinkFiles();
  }

  function renderLinkList() {
    clear(linkList);
    const query = normalize(linkSearch.value);
    const visible = linkFiles.filter(
      (file) =>
        !query ||
        normalize(`${file.path} ${formatKey(file)}`).includes(query),
    );
    if (visible.length === 0) {
      linkList.append(
        el(
          'div',
          'picker__empty',
          linkFiles.length === 0
            ? 'No unlinked files. New files appear here after a scan.'
            : 'No matching files.',
        ),
      );
      return;
    }
    const fragment = document.createDocumentFragment();
    for (const file of visible) {
      const button = el('button', 'pick');
      button.type = 'button';
      button.setAttribute(
        'aria-pressed',
        linkSelectedID === file.id ? 'true' : 'false',
      );
      const art = el('span', 'pick__art');
      art.append(el('span', 'pick__num', formatKey(file)));
      const main = el('span', 'pick__main');
      main.append(el('span', 'pick__title', baseName(file.path)));
      main.append(
        el(
          'span',
          'pick__meta',
          `${dirName(file.path) || '—'} · ${formatBytes(file.size_bytes)}`,
        ),
      );
      button.append(art, main, el('span', 'pick__mark', '✓'));
      button.addEventListener('click', () => {
        linkSelectedID = file.id;
        for (const node of linkList.querySelectorAll('.pick')) {
          node.setAttribute(
            'aria-pressed',
            node === button ? 'true' : 'false',
          );
        }
        linkSubmit.disabled = false;
        setFormStatus(linkStatus, '');
      });
      fragment.append(button);
    }
    linkList.append(fragment);
  }

  async function loadLinkFiles() {
    const token = ++linkToken;
    linkSubmit.disabled = true;
    setFormStatus(linkStatus, 'Loading unlinked files…');
    clear(linkList);
    linkList.append(el('div', 'picker__empty', 'Loading unlinked files…'));
    try {
      const result = await api.listAllUnlinkedFiles();
      if (token !== linkToken || !linkTrack) return;
      linkFiles = result.items;
      linkSelectedID = null;
      renderLinkList();
      if (linkFiles.length === 0) {
        setFormStatus(linkStatus, '');
      } else {
        setFormStatus(
          linkStatus,
          `${linkFiles.length} unlinked file${linkFiles.length === 1 ? '' : 's'}`,
        );
      }
    } catch (error) {
      if (token !== linkToken) return;
      clear(linkList);
      const message =
        error instanceof Error ? error.message : String(error);
      linkList.append(
        el('div', 'picker__empty', `Could not load files: ${message}`),
      );
      setFormStatus(linkStatus, `Could not load files: ${message}`, 'error');
    }
  }

  async function submitLink() {
    if (!linkTrack || !linkSelectedID || linkInFlight) return;
    const track = linkTrack;
    const fileID = linkSelectedID;
    const file = linkFiles.find((candidate) => candidate.id === fileID);
    linkInFlight = true;
    setBusy(linkSubmit, true, 'Linking…');
    setFormStatus(linkStatus, 'Moving file and writing tags…');
    try {
      const response = await api.link(fileID, track.id);
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
        setFormStatus(linkStatus, message, 'error');
        return;
      }
      toast(
        `${file ? baseName(file.path) : 'File'} linked to “${track.title}”.`,
        'ok',
      );
      closeLinkDialog();
      await load();
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      setFormStatus(linkStatus, `Could not link file: ${message}`, 'error');
    } finally {
      linkInFlight = false;
      setBusy(linkSubmit, false);
      linkSubmit.disabled = !linkSelectedID;
    }
  }

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

  async function refreshCover() {
    if (!currentRelease) return;
    await runCoverRefresh(() =>
      api.refreshReleaseCover(currentRelease.id),
    );
  }

  async function runCoverRefresh(refresh) {
    if (coverInFlight) return;
    coverInFlight = true;
    heroTools.setBusy(true);
    try {
      const row = await refresh();
      // Rebuild the hero either way: the page may be showing the disc
      // fallback (when there was no cached cover before) or stale art
      // (when the refresh found nothing and dropped the cache).
      renderHeroCover();
      if (!row) {
        toast('No cover art found.', 'info');
        return;
      }
      toast(`Cover refreshed (${coverSourceText(row.source)}).`, 'ok');
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      toast(`Could not refresh cover: ${message}`, 'error');
    } finally {
      coverInFlight = false;
      heroTools.setBusy(false);
    }
  }

  // Renders the hero art with the overlay tools and source badge. Remove
  // and the badge only show once the <img> proves itself; the disc
  // fallback keeps them hidden.
  function renderHeroCover() {
    if (!currentGroup || !heroTools) return;
    const art = coverArt('group', currentGroup.id, '', { eager: true });
    setSharedArtName(art, sharedArtName('group', currentGroup.id));
    heroArt.replaceChildren(art, heroTools.tools, heroTools.source);
    syncCoverSource(art);
    if (heroTools) {
      heroTools.tools.hidden = !currentRelease;
      heroTools.source.hidden = heroTools.source.hidden || !currentRelease;
    }
  }

  function syncCoverSource(art) {
    const token = ++coverMetaToken;
    heroTools.setHasArt(false);
    if (art.tagName !== 'IMG' || !currentRelease) return;
    const releaseID = currentRelease.id;
    const onLoad = () => {
      heroTools.setHasArt(true);
      void loadCoverSource(token, releaseID);
    };
    if (art.complete && art.naturalWidth > 0) {
      onLoad();
      return;
    }
    art.addEventListener('load', onLoad);
    art.addEventListener('error', () => heroTools.setHasArt(false));
  }

  async function loadCoverSource(token, releaseID) {
    if (token !== coverMetaToken) return;
    try {
      const row = await api.getReleaseCoverMeta(releaseID);
      if (token !== coverMetaToken) return;
      heroTools.setSource(row ? coverSourceBadge(row.source) : '');
    } catch {
      if (token === coverMetaToken) heroTools.setSource('');
    }
  }

  async function uploadCover(file) {
    if (!currentRelease || coverInFlight) return;
    coverInFlight = true;
    heroTools.setBusy(true);
    try {
      const row = await api.uploadReleaseCover(currentRelease.id, file);
      if (!row) {
        toast('Upload finished with no cover.', 'info');
        return;
      }
      renderHeroCover();
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
      heroTools.setBusy(false);
    }
  }

  async function removeCover() {
    if (!currentGroup || !currentRelease || coverInFlight) return;
    if (
      !window.confirm(
        `Remove the cached cover for "${currentGroup.title}"? The cached file is deleted and linked files lose their embedded cover in the background. Upload a new cover or refresh to fetch one again.`,
      )
    ) {
      return;
    }
    coverInFlight = true;
    heroTools.setBusy(true);
    try {
      const response = await api.deleteReleaseCover(currentRelease.id);
      if (response.status === 404) {
        toast('Release not found.', 'error');
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
        toast(`Could not remove cover: ${message}`, 'error');
        return;
      }
      renderHeroCover();
      toast(
        'Cover removed. Linked files are updating in the background.',
        'ok',
      );
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      toast(`Could not remove cover: ${message}`, 'error');
    } finally {
      coverInFlight = false;
      heroTools.setBusy(false);
    }
  }

  async function deleteGroup(button) {
    if (!currentGroup) return;
    const groupTitle = currentGroup.title;
    const artistId = currentGroup.credit?.[0]?.artist_id;
    const choice = await openDeleteDialog({
      title: `Remove "${groupTitle}"?`,
      copy: 'Its saved release and tracks are deleted and linked files become unlinked. Audio files are kept.',
      permanentHint:
        'Permanently delete: this release group will not return on refresh until allowed from Permanently deleted.',
    });
    if (!choice.confirmed) return;
    setBusy(button, true, 'Removing…');
    try {
      const response = await api.deleteReleaseGroup(
        currentGroup.id,
        choice.permanent,
      );
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
      toast(
        choice.permanent
          ? `"${groupTitle}" permanently removed. It will not return on refresh.`
          : `"${groupTitle}" removed.`,
        'ok',
      );
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
    if (heroTools) heroTools.tools.hidden = !hasRelease;
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
            openLinkDialog,
          ),
        );
        fragment.append(section);
      }
      tracksWrap.append(fragment);
    } else {
      tracksWrap.append(renderTrackTable(tracks, false, openLinkDialog));
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
      // Set before the hero renders so the source badge can load; the
      // release panel re-affirms it below.
      currentRelease = release;

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
      heroTools = coverTools({
        refreshLabel: 'Refresh cover',
        refreshTitle:
          'Fetch the cover art again (Cover Art Archive, then Deezer)',
        uploadLabel: 'Upload cover',
        uploadTitle: 'Upload a JPEG, PNG, or WebP cover (max 15MB)',
        removeLabel: 'Remove cover',
        removeTitle: 'Remove the cached cover',
        onRefresh: () => void refreshCover(),
        onUpload: (file) => void uploadCover(file),
        onRemove: () => void removeCover(),
      });
      renderHeroCover();
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
      const deleteButton = el(
        'button',
        'btn btn--danger btn--icon btn--icon-trash',
        'Remove group',
      );
      deleteButton.type = 'button';
      deleteButton.addEventListener('click', () =>
        deleteGroup(deleteButton),
      );
      actions.replaceChildren(switchButton, deleteButton);

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
  byId('link-close').addEventListener('click', closeLinkDialog);
  byId('link-cancel').addEventListener('click', closeLinkDialog);
  linkDialog.addEventListener('click', (event) => {
    if (event.target === linkDialog) closeLinkDialog();
  });
  linkDialog.addEventListener('close', () => {
    linkTrack = null;
    linkSelectedID = null;
    linkToken += 1;
    setFormStatus(linkStatus, '');
    clear(linkList);
  });
  linkSearch.addEventListener('input', renderLinkList);
  linkSubmit.addEventListener('click', () => void submitLink());
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
