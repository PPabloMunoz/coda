// All-files page: searchable, sortable, paginated file list.
// Ported from `src/pages/files.astro` — Astro guards dropped, runtime identical.

import {
  api,
  creditName,
  pageParams,
} from './api.js';
import {
  baseName,
  dirName,
  extension,
  formatBytes,
  formatDate,
  plural,
} from './format.js';
import {
  byId,
  clear,
  el,
  emptyState,
  link,
  toast,
} from './ui.js';

function initFilesPage() {
  const list = byId('list');
  const empty = byId('empty');
  const status = byId('status');
  const pageInfo = byId('page-info');
  const filter = byId('filter');
  const sortSelect = byId('sort');
  const limitSelect = byId('limit');
  const prev = byId('prev');
  const next = byId('next');

  const initial = pageParams(50);
  let limit = initial.limit;
  let offset = initial.offset;
  let total = 0;
  let files = [];
  let loadToken = 0;
  let searchTimer;
  limitSelect.value = String(limit);

  function fileRow(file) {
    const row = el(
      'article',
      `file-row ${file.track_id ? 'is-linked' : 'is-unlinked'}`,
    );

    const format = el(
      'div',
      'file-row__format',
      (file.format_hint || extension(file.path)).toUpperCase(),
    );

    const main = el('div', 'file-row__main');
    main.append(
      el('h3', 'file-row__name', baseName(file.path)),
      el('p', 'file-row__path', dirName(file.path)),
    );

    if (file.track) {
      const trackLine = el('p', 'file-row__track');
      trackLine.append(
        link(
          `/release-group?id=${encodeURIComponent(file.track.group.id)}`,
          file.track.title,
        ),
      );
      const artist = creditName(file.track.credit);
      if (artist) trackLine.append(document.createTextNode(` — ${artist}`));
      main.append(trackLine);
    }

    const meta = el('div', 'file-row__meta');
    meta.append(
      el('span', '', formatBytes(file.size_bytes)),
      el('span', '', `Scanned ${formatDate(file.scanned_at)}`),
    );
    main.append(meta);

    const actions = el('div', 'file-row__actions');
    if (file.track_id) {
      const groupTitle =
        (file.track.group && file.track.group.title) || 'Release group';
      const groupButton = link(
        `/release-group?id=${encodeURIComponent(file.track.group.id)}`,
        groupTitle,
        'btn btn--sm btn--secondary btn--icon btn--icon-disc',
      );
      groupButton.title = groupTitle;
      actions.append(groupButton);
      const refresh = el(
        'button',
        'btn btn--sm btn--secondary btn--icon btn--icon-refresh',
        'Refresh',
      );
      refresh.type = 'button';
      refresh.title = 'Rewrite tags from the linked track';
      refresh.addEventListener('click', () => refreshFile(file, refresh));
      actions.append(refresh);
      const unlink = el(
        'button',
        'btn btn--sm btn--danger btn--icon btn--icon-trash',
        'Unlink',
      );
      unlink.type = 'button';
      unlink.addEventListener('click', () => unlinkFile(file, unlink));
      actions.append(unlink);
    } else {
      actions.append(
        link(
          `/link?file=${file.id}`,
          'Link to track',
          'btn btn--sm btn--primary btn--icon btn--icon-link',
        ),
      );
    }
    actions.append(
      link(
        `/file?id=${file.id}`,
        'Details',
        'btn btn--sm btn--icon btn--icon-info',
      ),
    );

    row.append(format, main, actions);
    return row;
  }

  function render() {
    const query = filter.value.trim();

    clear(list);
    const fragment = document.createDocumentFragment();
    for (const file of files) fragment.append(fileRow(file));
    list.append(fragment);

    if (files.length === 0) {
      empty.hidden = false;
      let state;
      if (query) {
        state = emptyState(
          'No matching files',
          'Try a different path or clear the search.',
        );
      } else if (total === 0 && offset === 0) {
        state = emptyState(
          'No files scanned yet',
          'Add a root folder in config.yaml and restart Coda. Files appear here after a scan.',
        );
      } else {
        state = emptyState(
          'No files on this page',
          'The library changed since this page was loaded.',
          { href: '/files', label: 'Start over' },
        );
      }
      empty.replaceChildren(state);
    } else {
      empty.hidden = true;
      empty.replaceChildren();
    }

    if (total > 0) {
      const end = offset + files.length;
      status.textContent = `Showing ${offset + 1}–${end} of ${plural(total, 'file')}`;
      pageInfo.textContent = `Page ${Math.floor(offset / limit) + 1}`;
    } else {
      status.textContent = query ? '0 matches' : '0 files';
      pageInfo.textContent = '';
    }

    prev.disabled = offset <= 0;
    next.disabled = offset + limit >= total;
  }

  async function unlinkFile(file, button) {
    const name = baseName(file.path);
    const confirmed = window.confirm(
      `Remove the MusicBrainz link and clear tags from “${name}”?`,
    );
    if (!confirmed) return;

    button.disabled = true;
    button.textContent = 'Unlinking…';
    try {
      const response = await api.unlink(file.id);
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
      button.disabled = false;
      button.textContent = 'Unlink';
    }
  }

  async function refreshFile(file, button) {
    const name = baseName(file.path);

    button.disabled = true;
    button.textContent = 'Refreshing…';
    try {
      const response = await api.refreshFile(file.id);
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
      button.disabled = false;
      button.textContent = 'Refresh';
    }
  }

  async function load() {
    const token = ++loadToken;
    const [sortField, sortOrder] = sortSelect.value.split(':');

    prev.disabled = true;
    next.disabled = true;
    status.textContent = 'Loading files…';

    try {
      const page = await api.listFiles(limit, offset, {
        includeTrack: true,
        query: filter.value.trim(),
        sort: sortField,
        order: sortOrder,
      });
      if (token !== loadToken) return;

      files = page?.items ?? [];
      total = page?.total ?? 0;

      if (files.length === 0 && offset > 0) {
        offset = Math.max(0, offset - limit);
        await load();
        return;
      }

      render();
      history.replaceState(
        history.state,
        '',
        `?limit=${limit}&offset=${offset}`,
      );
    } catch (error) {
      if (token !== loadToken) return;
      clear(list);
      empty.hidden = false;
      const message =
        error instanceof Error ? error.message : String(error);
      empty.replaceChildren(emptyState('Could not load files', message));
      status.textContent = 'Failed to load';
    }
  }

  filter.addEventListener('input', () => {
    window.clearTimeout(searchTimer);
    searchTimer = window.setTimeout(() => {
      offset = 0;
      void load();
    }, 250);
  });
  sortSelect.addEventListener('change', () => {
    offset = 0;
    void load();
  });
  limitSelect.addEventListener('change', () => {
    limit = Number(limitSelect.value) || 50;
    offset = 0;
    void load();
  });
  prev.addEventListener('click', () => {
    offset = Math.max(0, offset - limit);
    void load();
  });
  next.addEventListener('click', () => {
    offset += limit;
    void load();
  });

  load();
}

initFilesPage();
