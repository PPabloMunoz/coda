// Card builders shared by the library pages.
// Ported verbatim from the Astro `src/lib/cards.ts` — types stripped, runtime identical.

import { api, creditName } from './api.js';
import { plural, yearOf } from './format.js';
import {
  artistAvatar,
  coverArt,
  el,
  link,
  setSharedArtName,
  sharedArtName,
} from './ui.js';

export function artistCard(artist) {
  const card = link(
    `/artist?id=${encodeURIComponent(artist.id)}`,
    '',
    'artist-card',
  );
  card.setAttribute('aria-label', artist.name);

  const cover = el('div', 'artist-card__cover');
  const photo = coverArt('artist', artist.id, '', {
    fallback: () => artistAvatar(artist.id, artist.name),
  });
  setSharedArtName(photo, sharedArtName('artist', artist.id));
  cover.append(photo);

  const body = el('div', 'artist-card__body');
  body.append(
    el('h2', 'artist-card__name', artist.name),
    el(
      'p',
      'artist-card__meta',
      artist.disambiguation || artist['sort-name'] || 'Artist',
    ),
  );

  if (artist.stats) {
    const total = artist.stats.track_count;
    const linked = artist.stats.linked_track_count;
    const percent = total > 0 ? Math.round((linked / total) * 100) : 0;

    const stats = el('div', 'artist-card__stats');
    stats.title = `${linked} linked of ${plural(total, 'song')}`;

    const bar = el('span', 'artist-card__bar');
    const fill = el('span', 'artist-card__bar-fill');
    fill.style.width = `${percent}%`;
    bar.append(fill);
    bar.setAttribute('aria-hidden', 'true');

    stats.append(
      el(
        'span',
        'artist-card__stats-text',
        `${linked} / ${plural(total, 'song')}`,
      ),
      bar,
    );
    body.append(stats);
  }

  card.append(cover, body);
  return card;
}

export function releaseGroupCard(group, options = {}) {
  const { showCredit = true, showType = true } = options;
  const card = link(
    `/release-group?id=${encodeURIComponent(group.id)}`,
    '',
    'rg-card',
  );
  card.setAttribute('aria-label', group.title);

  const art = el('div', 'rg-card__art');
  const cover = coverArt('group', group.id);
  setSharedArtName(cover, sharedArtName('group', group.id));
  art.append(cover);

  const body = el('div', 'rg-card__body');
  if (showType) {
    body.append(
      el(
        'span',
        'chip chip--type',
        group.type ? group.type.toUpperCase() : 'OTHER',
      ),
    );
  }
  const title = el('h3', 'rg-card__title', group.title);
  const releaseLine = el(
    'p',
    'rg-card__release is-loading',
    'Checking saved release…',
  );

  body.append(title, releaseLine);

  if (showCredit) {
    const credit = el('p', 'rg-card__credit');
    credit.textContent = creditName(group.credit);
    if (!credit.textContent) credit.hidden = true;
    body.append(credit);
  }

  card.append(art, body);
  return { card, releaseLine };
}

export function releaseCompleteness(trackCount, linkedCount) {
  if (trackCount > 0 && linkedCount >= trackCount) return 'complete';
  if (linkedCount > 0) return 'partial';
  return 'missing';
}

function setCompletenessLine(line, opts) {
  const status = releaseCompleteness(opts.trackCount, opts.linkedCount);
  line.classList.remove(
    'is-loading',
    'is-saved',
    'is-complete',
    'is-partial',
    'is-missing',
  );
  line.classList.add(`is-${status}`);
  const card = line.closest('.rg-card');
  if (card) {
    card.classList.remove('is-complete', 'is-partial', 'is-missing');
    card.classList.add(`is-${status}`);
  }
  const counts = `${opts.linkedCount}/${opts.trackCount} linked`;
  line.textContent = [opts.year, counts].filter(Boolean).join(' · ');
  line.title = `${opts.title} — ${opts.linkedCount} of ${opts.trackCount} tracks linked`;
  line.setAttribute('aria-label', line.title);
}

function markCardMissing(line) {
  const card = line.closest('.rg-card');
  if (card) {
    card.classList.remove('is-complete', 'is-partial');
    card.classList.add('is-missing');
  }
}

export async function hydrateReleaseLine(line, group, cache) {
  try {
    if (group.release) {
      if (!line.isConnected) return true;
      setCompletenessLine(line, {
        year: yearOf(group.release.date),
        title: group.release.title,
        trackCount: group.release.track_count,
        linkedCount: group.release.linked_file_count,
      });
      return true;
    }

    let release = cache?.get(group.id);
    if (release === undefined) {
      release = await api.getReleaseByGroup(group.id, true);
      cache?.set(group.id, release);
    }

    if (!line.isConnected) return Boolean(release);

    line.classList.remove(
      'is-loading',
      'is-saved',
      'is-complete',
      'is-partial',
    );

    if (!release) {
      line.classList.add('is-missing');
      markCardMissing(line);
      line.textContent = 'No release saved yet';
      line.title = 'No release saved yet — no tracks linked';
      line.setAttribute('aria-label', line.title);
      return false;
    }

    const tracks = release.tracks ?? [];
    const linked = tracks.filter(
      (track) => (track.files?.length ?? 0) > 0,
    ).length;
    setCompletenessLine(line, {
      year: yearOf(release.date),
      title: release.title,
      trackCount: tracks.length,
      linkedCount: linked,
    });
    return true;
  } catch {
    if (!line.isConnected) return false;
    line.classList.remove(
      'is-loading',
      'is-saved',
      'is-complete',
      'is-partial',
    );
    line.classList.add('is-missing');
    markCardMissing(line);
    line.textContent = 'Could not read saved release';
    line.removeAttribute('aria-label');
    return false;
  }
}
