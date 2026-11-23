// Single-file detail page: stats, tags, MusicBrainz link panel.
// Ported from `src/pages/file.astro` — Astro guards dropped, runtime identical.

import { api, mbUrl } from './api.js';
import {
  baseName,
  formatBytes,
  formatDate,
  formatDateTime,
  typeLabel,
} from './format.js';
import {
  byId,
  creditFragment,
  el,
  externalLink,
  link,
  setBusy,
  toast,
} from './ui.js';

function setText(id, value) {
  byId(id).textContent = value;
}

function initFilePage() {
  const status = byId('status');
  const content = byId('content');
  const crumb = byId('crumb');
  const fileName = byId('file-name');
  const filePath = byId('file-path');
  const actions = byId('actions');
  const linkPanel = byId('link-panel');

  const fileID = new URLSearchParams(location.search).get('id') ?? '';
  let current = null;

  function showError(message) {
    status.hidden = false;
    status.classList.add('is-error');
    status.textContent = message;
    content.hidden = true;
  }

  function renderLinkPanel(file) {
    const panel = el('div', 'link-panel');
    const body = el('div', 'link-panel__body');

    if (file.track) {
      const track = file.track;
      body.append(
        el('p', 'eyebrow', 'MusicBrainz link'),
        el('h3', '', track.title),
      );

      const creditLine = el('p', 'credit-line');
      creditLine.append(creditFragment(track.credit));
      body.append(creditLine);

      const releaseMeta = el('div', 'link-panel__meta');
      releaseMeta.append(
        el('span', '', 'Release'),
        el('span', '', track.release.title),
      );
      if (track.release.date) {
        releaseMeta.append(el('span', 'mono', track.release.date));
      }
      body.append(releaseMeta);

      const groupMeta = el('div', 'link-panel__meta');
      groupMeta.append(
        el('span', '', 'Release group'),
        link(
          `/release-group?id=${encodeURIComponent(track.group.id)}`,
          track.group.title,
        ),
        el('span', 'chip chip--type', typeLabel(track.group.type)),
      );
      body.append(groupMeta);

      const panelActions = el('div', 'link-panel__actions');
      panelActions.append(
        externalLink(
          mbUrl.recording(track.recording_id),
          'Recording ↗',
          'btn btn--secondary',
        ),
        externalLink(
          mbUrl.release(track.release.id),
          'Release ↗',
          'btn btn--secondary',
        ),
      );
      const refresh = el('button', 'btn btn--secondary', 'Refresh tags');
      refresh.type = 'button';
      refresh.addEventListener('click', () => refreshFile(refresh));
      panelActions.append(refresh);
      const unlink = el(
        'button',
        'btn btn--danger',
        'Unlink and clear tags',
      );
      unlink.type = 'button';
      unlink.addEventListener('click', () => unlinkFile(unlink));
      panelActions.append(unlink);
      body.append(panelActions);
    } else if (file.track_id) {
      body.append(
        el('p', 'eyebrow', 'MusicBrainz link'),
        el('h3', '', 'Linked to a track'),
        el('p', 'mono link-panel__id', file.track_id),
      );
      const panelActions = el('div', 'link-panel__actions');
      panelActions.append(
        externalLink(
          mbUrl.track(file.track_id),
          'Open track on MusicBrainz ↗',
          'btn btn--secondary',
        ),
      );
      const refresh = el('button', 'btn btn--secondary', 'Refresh tags');
      refresh.type = 'button';
      refresh.addEventListener('click', () => refreshFile(refresh));
      panelActions.append(refresh);
      const unlink = el(
        'button',
        'btn btn--danger',
        'Unlink and clear tags',
      );
      unlink.type = 'button';
      unlink.addEventListener('click', () => unlinkFile(unlink));
      panelActions.append(unlink);
      body.append(panelActions);
    } else {
      body.append(
        el('p', 'eyebrow', 'MusicBrainz link'),
        el('h3', '', 'This file isn’t linked yet'),
        el(
          'p',
          'muted',
          'Choose an artist, release group, release, and track to write MusicBrainz tags.',
        ),
      );
      const panelActions = el('div', 'link-panel__actions');
      panelActions.append(
        link(`/link?file=${file.id}`, 'Link this file', 'btn btn--primary'),
      );
      body.append(panelActions);
    }

    panel.append(body);
    linkPanel.replaceChildren(panel);
  }

  async function unlinkFile(button) {
    if (!current) return;
    const name = baseName(current.path);
    const confirmed = window.confirm(
      `Remove the MusicBrainz link and clear tags from “${name}”?`,
    );
    if (!confirmed) return;

    setBusy(button, true, 'Unlinking…');
    try {
      const response = await api.unlink(current.id);
      if (!response.ok) {
        let message = response.statusText;
        try {
          message = (await response.json()).error ?? message;
        } catch {
          /* keep status text */
        }
        toast(`Could not unlink: ${message}`, 'error');
        return;
      }
      toast(`${name} unlinked.`, 'ok');
      await load();
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      toast(`Could not unlink: ${message}`, 'error');
    } finally {
      setBusy(button, false);
    }
  }

  async function refreshFile(button) {
    if (!current) return;
    const name = baseName(current.path);

    setBusy(button, true, 'Refreshing…');
    try {
      const response = await api.refreshFile(current.id);
      if (!response.ok) {
        let message = response.statusText;
        try {
          message = (await response.json()).error ?? message;
        } catch {
          /* keep status text */
        }
        toast(`Could not refresh tags: ${message}`, 'error');
        return;
      }
      toast(`Tags refreshed for ${name}.`, 'ok');
      await load();
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      toast(`Could not refresh tags: ${message}`, 'error');
    } finally {
      setBusy(button, false);
    }
  }

  async function load() {
    if (!fileID) {
      showError('Missing file id. Open one from all files.');
      return;
    }

    status.hidden = false;
    status.classList.remove('is-error');
    status.textContent = 'Loading file…';
    content.hidden = true;

    try {
      const file = await api.getFile(fileID, true);
      if (!file) {
        showError('File not found.');
        return;
      }

      current = file;
      const name = baseName(file.path);
      document.title = `${name} · Coda`;
      crumb.textContent = name;
      fileName.textContent = name;
      filePath.textContent = file.path;

      setText('stat-format', (file.format_hint || 'audio').toUpperCase());
      setText('stat-size', formatBytes(file.size_bytes));
      setText('stat-scanned', formatDate(file.scanned_at));
      setText('stat-link', file.track_id ? 'Linked' : 'Unlinked');

      setText('detail-path', file.path);
      setText('detail-format', file.format_hint || '—');
      setText('detail-size', formatBytes(file.size_bytes));
      setText(
        'detail-mtime',
        file.mtime_ns > 0
          ? formatDateTime(
              new Date(file.mtime_ns / 1_000_000).toISOString(),
            )
          : '—',
      );
      setText('detail-scanned', formatDateTime(file.scanned_at));
      setText('detail-track', file.track_id || '—');

      actions.replaceChildren(
        link('/files', 'All files', 'btn btn--ghost'),
      );
      if (file.track) {
        actions.append(
          externalLink(
            mbUrl.recording(file.track.recording_id),
            'Recording ↗',
            'btn btn--secondary',
          ),
        );
        const headerRefresh = el(
          'button',
          'btn btn--secondary',
          'Refresh tags',
        );
        headerRefresh.type = 'button';
        headerRefresh.addEventListener('click', () =>
          refreshFile(headerRefresh),
        );
        actions.append(headerRefresh);
      } else if (file.track_id) {
        actions.append(
          externalLink(
            mbUrl.track(file.track_id),
            'MusicBrainz ↗',
            'btn btn--secondary',
          ),
        );
        const headerRefresh = el(
          'button',
          'btn btn--secondary',
          'Refresh tags',
        );
        headerRefresh.type = 'button';
        headerRefresh.addEventListener('click', () =>
          refreshFile(headerRefresh),
        );
        actions.append(headerRefresh);
      } else {
        actions.append(
          link(
            `/link?file=${file.id}`,
            'Link this file',
            'btn btn--primary',
          ),
        );
      }

      renderLinkPanel(file);
      status.hidden = true;
      content.hidden = false;
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      showError(`Could not load file: ${message}`);
    }
  }

  load();
}

initFilePage();
