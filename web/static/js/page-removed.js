// Permanently deleted page: tombstoned artists and release groups.
// Removing a row only deletes the tombstone; a group returns on the next
// artist refresh and an artist can be re-added from Add artist.

import { api, mbUrl, pageParams } from './api.js';
import { formatDate, plural } from './format.js';
import { byId, clear, el, emptyState, externalLink, toast } from './ui.js';

function typeLabel(value) {
  return value === 'artist' ? 'Artist' : 'Release group';
}

function mbHref(entry) {
  return entry.type === 'artist'
    ? mbUrl.artist(entry.id)
    : mbUrl.releaseGroup(entry.id);
}

function initRemovedPage() {
  const list = byId('list');
  const empty = byId('empty');
  const status = byId('status');
  const pageInfo = byId('page-info');
  const filter = byId('filter');
  const typeSelect = byId('type');
  const limitSelect = byId('limit');
  const prev = byId('prev');
  const next = byId('next');

  const initial = pageParams(50);
  let limit = initial.limit;
  let offset = initial.offset;
  let total = 0;
  let items = [];
  let loadToken = 0;
  let searchTimer;

  limitSelect.value = String(limit);
  const initialParams = new URLSearchParams(location.search);
  filter.value = initialParams.get('q') ?? '';
  const initialType = initialParams.get('type') ?? '';
  if ([...typeSelect.options].some((option) => option.value === initialType)) {
    typeSelect.value = initialType;
  }

  function entryRow(entry) {
    const row = el('article', 'file-row is-unlinked');

    const format = el(
      'div',
      'file-row__format',
      entry.type === 'artist' ? 'AR' : 'RG',
    );

    const main = el('div', 'file-row__main');
    main.append(el('h3', 'file-row__name', entry.name));
    const meta = el('p', 'file-row__path');
    meta.textContent = `${typeLabel(entry.type)} · Deleted ${formatDate(entry.deleted_at)}`;
    main.append(meta);
    const mb = externalLink(
      mbHref(entry),
      'Open on MusicBrainz',
      'track-link',
    );
    mb.title = 'Open on MusicBrainz';
    main.append(mb);

    const actions = el('div', 'file-row__actions');
    const allow = el(
      'button',
      'btn btn--sm btn--secondary',
      'Allow again',
    );
    allow.type = 'button';
    allow.title =
      entry.type === 'artist'
        ? 'Remove the tombstone so this artist can be added again'
        : 'Remove the tombstone so this group returns on the next refresh';
    allow.addEventListener('click', () => allowAgain(entry, allow));
    actions.append(allow);

    row.append(format, main, actions);
    return row;
  }

  function render() {
    const query = filter.value.trim();

    clear(list);
    const fragment = document.createDocumentFragment();
    for (const entry of items) fragment.append(entryRow(entry));
    list.append(fragment);

    if (items.length === 0) {
      empty.hidden = false;
      empty.replaceChildren(
        emptyState(
          query || typeSelect.value
            ? 'No matches'
            : 'Nothing permanently deleted',
          query || typeSelect.value
            ? 'Try a different name or type.'
            : 'Permanently deleted artists and release groups will appear here.',
        ),
      );
    } else {
      empty.hidden = true;
      empty.replaceChildren();
    }

    status.textContent = query
      ? `${items.length} of ${plural(total, 'entry', 'entries')}`
      : plural(total, 'entry', 'entries');
    const page = Math.floor(offset / limit) + 1;
    const pages = Math.max(1, Math.ceil(total / limit));
    pageInfo.textContent = `Page ${page} of ${pages}`;
    prev.disabled = offset <= 0;
    next.disabled = offset + items.length >= total;
  }

  async function load() {
    const token = ++loadToken;
    const query = filter.value.trim();
    const type = typeSelect.value;

    status.textContent = 'Loading…';

    const url = new URL(location.href);
    if (query) url.searchParams.set('q', query);
    else url.searchParams.delete('q');
    if (type) url.searchParams.set('type', type);
    else url.searchParams.delete('type');
    url.searchParams.set('limit', String(limit));
    url.searchParams.set('offset', String(offset));
    history.replaceState(null, '', url);

    try {
      const result = await api.listIgnored(limit, offset, { query, type });
      if (token !== loadToken || !result) return;
      items = result.items;
      total = result.total;
      render();
    } catch (error) {
      if (token !== loadToken) return;
      clear(list);
      empty.hidden = false;
      const message =
        error instanceof Error ? error.message : String(error);
      empty.replaceChildren(
        emptyState('Could not load permanently deleted', message),
      );
      status.textContent = 'Failed to load';
    }
  }

  async function allowAgain(entry, button) {
    button.disabled = true;
    try {
      const response = await api.removeIgnored(entry.id);
      if (response.status === 404) {
        toast('Entry already removed.', 'error');
        await load();
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
        toast(`Could not allow again: ${message}`, 'error');
        button.disabled = false;
        return;
      }
      toast(
        entry.type === 'artist'
          ? `"${entry.name}" allowed again. Add the artist from Artists to import it.`
          : `"${entry.name}" allowed again. Refresh its artist to import it.`,
        'ok',
      );
      await load();
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      toast(`Could not allow again: ${message}`, 'error');
      button.disabled = false;
    }
  }

  filter.addEventListener('input', () => {
    window.clearTimeout(searchTimer);
    searchTimer = window.setTimeout(() => {
      offset = 0;
      void load();
    }, 250);
  });
  typeSelect.addEventListener('change', () => {
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

  void load();
}

initRemovedPage();
