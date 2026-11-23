// All release groups page.
// Ported from `src/pages/release-groups.astro` — Astro guards dropped, runtime identical.

import { api } from './api.js';
import { hydrateReleaseLine, releaseGroupCard } from './cards.js';
import { plural } from './format.js';
import {
  byId,
  clear,
  emptyState,
  mapLimit,
  skeletonGrid,
} from './ui.js';

function initReleaseGroupsPage() {
  const grid = byId('grid');
  const empty = byId('empty');
  const status = byId('status');
  const filter = byId('filter');
  const refresh = byId('refresh');

  const releaseCache = new Map();
  let groups = [];
  let loadToken = 0;
  let searchTimer;

  async function hydrateCards(entries) {
    await mapLimit(entries, 6, ({ group, line }) =>
      hydrateReleaseLine(line, group, releaseCache),
    );
  }

  function render() {
    const query = filter.value.trim();

    clear(grid);
    const fragment = document.createDocumentFragment();
    const entries = [];

    for (const group of groups) {
      const { card, releaseLine } = releaseGroupCard(group);
      fragment.append(card);
      entries.push({ group, line: releaseLine });
    }
    grid.append(fragment);

    if (groups.length === 0) {
      empty.hidden = false;
      empty.replaceChildren(
        query
          ? emptyState(
              'No matching release groups',
              'Try a different title or artist.',
            )
          : emptyState(
              'No release groups yet',
              'Add an artist and Coda will import the discography in the background.',
              { href: '/', label: 'Browse artists' },
            ),
      );
    } else {
      empty.hidden = true;
      empty.replaceChildren();
    }

    status.textContent = query
      ? `${groups.length} ${groups.length === 1 ? 'match' : 'matches'}`
      : plural(groups.length, 'release group');

    void hydrateCards(entries);
  }

  async function load(quiet = false) {
    const token = ++loadToken;
    const query = filter.value.trim();

    if (!quiet) {
      skeletonGrid(grid, 12, 'group');
      status.textContent = 'Loading release groups…';
    } else {
      status.textContent = 'Searching…';
    }

    try {
      const result = await api.listAllReleaseGroups({
        includeRelease: true,
        query,
      });
      if (token !== loadToken) return;
      groups = result.items;
      render();
    } catch (error) {
      if (token !== loadToken) return;
      clear(grid);
      empty.hidden = false;
      const message =
        error instanceof Error ? error.message : String(error);
      empty.replaceChildren(
        emptyState('Could not load release groups', message),
      );
      status.textContent = 'Failed to load';
    }
  }

  filter.addEventListener('input', () => {
    window.clearTimeout(searchTimer);
    searchTimer = window.setTimeout(() => void load(true), 250);
  });
  refresh.addEventListener('click', () => load());
  load();
}

initReleaseGroupsPage();
