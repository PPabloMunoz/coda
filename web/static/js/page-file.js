// Single-file detail page: stats, tags, MusicBrainz link panel.
// Ported from `src/pages/file.astro` — Astro guards dropped, runtime identical.

import { api, mbUrl } from './api.js';
import {
  baseName,
  formatBytes,
  formatDate,
  formatDateTime,
} from './format.js';
import {
  byId,
  el,
  link,
  mbLogoLink,
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
        link(
          `/link?file=${file.id}`,
          'Link this file',
          'btn btn--primary btn--icon btn--icon-link',
        ),
      );
      body.append(panelActions);

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
      byId('stat-link').replaceChildren(
        el(
          'span',
          file.track_id ? 'chip chip--ok' : 'chip chip--warn',
          file.track_id ? 'Linked' : 'Unlinked',
        ),
      );

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
          mbLogoLink(
            mbUrl.recording(file.track.recording_id),
            'btn btn--secondary',
            `Open ${file.track.title} on MusicBrainz`,
          ),
        );
        const headerRefresh = el(
          'button',
          'btn btn--secondary btn--icon btn--icon-refresh',
          'Refresh tags',
        );
        headerRefresh.type = 'button';
        headerRefresh.addEventListener('click', () =>
          refreshFile(headerRefresh),
        );
        const headerUnlink = el(
          'button',
          'btn btn--danger btn--icon btn--icon-trash',
          'Unlink',
        );
        headerUnlink.type = 'button';
        headerUnlink.addEventListener('click', () =>
          unlinkFile(headerUnlink),
        );
        actions.append(headerRefresh, headerUnlink);
      } else if (file.track_id) {
        actions.append(
          mbLogoLink(
            mbUrl.track(file.track_id),
            'btn btn--secondary',
            'Open track on MusicBrainz',
          ),
        );
        const headerRefresh = el(
          'button',
          'btn btn--secondary btn--icon btn--icon-refresh',
          'Refresh tags',
        );
        headerRefresh.type = 'button';
        headerRefresh.addEventListener('click', () =>
          refreshFile(headerRefresh),
        );
        const headerUnlink = el(
          'button',
          'btn btn--danger btn--icon btn--icon-trash',
          'Unlink',
        );
        headerUnlink.type = 'button';
        headerUnlink.addEventListener('click', () =>
          unlinkFile(headerUnlink),
        );
        actions.append(headerRefresh, headerUnlink);
      } else {
        actions.append(
          link(
            `/link?file=${file.id}`,
            'Link this file',
            'btn btn--primary btn--icon btn--icon-link',
          ),
        );
      }

      // Linked files show stats only; the detail table and link panel below
      // repeat the same data, so they stay for unlinked files.
      const linked = Boolean(file.track_id);
      byId('detail-wrap').hidden = linked;
      linkPanel.hidden = linked;
      if (!linked) renderLinkPanel(file);
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
