// API client for the Coda Go backend (`/api/v1/*`).
// Ported verbatim from the Astro `src/lib/api.ts` — types stripped, runtime identical.

export const MBID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

const MBID_ANYWHERE =
  /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;

export function normMbid(value) {
  return value.trim().toLowerCase();
}

/** Accepts a bare MBID or a MusicBrainz URL and returns the first MBID found. */
export function extractMbid(value) {
  const match = value.match(MBID_ANYWHERE);
  return match ? match[0].toLowerCase() : normMbid(value);
}

export function creditName(credit) {
  if (!credit || credit.length === 0) return '';
  return credit
    .map(
      (entry) =>
        (entry.credited_name || entry.name) + (entry.join_phrase || ''),
    )
    .join('');
}

export function fmtDuration(ms) {
  if (ms == null) return '';
  const total = Math.round(ms / 1000);
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`;
}

export const mbUrl = {
  artist: (id) => `https://musicbrainz.org/artist/${id}`,
  releaseGroup: (id) => `https://musicbrainz.org/release-group/${id}`,
  release: (id) => `https://musicbrainz.org/release/${id}`,
  recording: (id) => `https://musicbrainz.org/recording/${id}`,
  track: (id) => `https://musicbrainz.org/track/${id}`,
};

/** Cover photo URLs served by the API (404 when there is no art). */
const COVER_URL_VERSION = '2';

export const coverUrl = {
  release: (id) =>
    `/api/v1/releases/${encodeURIComponent(id)}/cover?v=${COVER_URL_VERSION}`,
  releaseGroup: (id) =>
    `/api/v1/release-groups/${encodeURIComponent(id)}/cover?v=${COVER_URL_VERSION}`,
  artist: (id) =>
    `/api/v1/artists/${encodeURIComponent(id)}/cover?v=${COVER_URL_VERSION}`,
};

export class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

export async function request(path, init) {
  const res = await fetch(path, init);

  if (res.status === 304 || res.status === 404) return null;

  if (!res.ok) {
    let message = res.statusText;
    try {
      const body = await res.json();
      if (typeof body?.error === 'string' && body.error) message = body.error;
    } catch {
      /* keep statusText */
    }
    throw new ApiError(res.status, message);
  }

  if (res.status === 204) return null;
  const text = await res.text();
  if (!text.trim()) return null;
  return JSON.parse(text);
}

async function listArtistsPage(limit, offset, options = {}) {
  const params = new URLSearchParams({
    limit: String(limit),
    offset: String(offset),
  });
  if (options.query) params.set('q', options.query);
  if (options.sort) params.set('sort', options.sort);
  if (options.order) params.set('order', options.order);
  if (options.includeTrackStats) params.set('include', 'track_stats');
  return request(`/api/v1/artists?${params.toString()}`);
}

async function listFilesPage(limit, offset, options = {}) {
  const params = new URLSearchParams({
    limit: String(limit),
    offset: String(offset),
  });
  if (options.unlinked) params.set('unlink', '1');
  if (options.includeTrack) params.set('include', 'track');
  if (options.query) params.set('q', options.query);
  if (options.format) params.set('format', options.format);
  if (options.sort) params.set('sort', options.sort);
  if (options.order) params.set('order', options.order);
  return request(`/api/v1/files?${params.toString()}`);
}

async function listReleaseGroupsPage(limit, offset, options = {}) {
  const params = new URLSearchParams({
    limit: String(limit),
    offset: String(offset),
  });
  if (options.artist) params.set('artist', options.artist);
  if (options.includeRelease) params.set('include', 'release');
  if (options.query) params.set('q', options.query);
  if (options.type) params.set('type', options.type);
  if (options.hasRelease !== undefined) {
    params.set('has_release', options.hasRelease ? '1' : '0');
  }
  if (options.primary !== undefined) {
    params.set('primary', options.primary ? '1' : '0');
  }
  if (options.sort) params.set('sort', options.sort);
  if (options.order) params.set('order', options.order);
  return request(
    `/api/v1/release-groups?${params.toString()}`,
  );
}

/**
 * The API caps page size at 100. Most screens want the whole library, so this
 * walks the pages and returns the lot. `maxItems` is a guard against a runaway
 * loop, not a product limit.
 */
export async function collectAll(fetchPage, pageSize = 100, maxItems = 5000) {
  const items = [];
  let total = 0;

  for (;;) {
    const page = await fetchPage(pageSize, items.length);
    if (!page) break;

    total = page.total;
    items.push(...page.items);

    if (
      page.items.length === 0 ||
      items.length >= total ||
      items.length >= maxItems
    ) {
      break;
    }
  }

  if (items.length < total) {
    console.warn(
      `collectAll: truncated at ${items.length} of ${total} items — ` +
        'raising maxItems is safer than trusting an incomplete list'
    );
  }

  return { items, total };
}

export function pageParams(defaultLimit = 10) {
  const query = new URLSearchParams(location.search);
  const limit = Math.min(
    100,
    Math.max(1, Number(query.get('limit')) || defaultLimit),
  );
  const offset = Math.max(0, Number(query.get('offset')) || 0);
  return { limit, offset };
}

export const api = {
  health: () => fetch('/api/v1/health').then((res) => res.text()),

  listFiles: listFilesPage,
  getFile: (id, includeTrack = false) =>
    request(
      `/api/v1/files/${encodeURIComponent(String(id))}` +
        (includeTrack ? '?include=track' : ''),
    ),
  getFileStats: () => request('/api/v1/files/stats'),
  getStats: () => request('/api/v1/stats'),
  listUnlinkedFiles: (limit, offset) =>
    listFilesPage(limit, offset, { unlinked: true }),
  listAllUnlinkedFiles: async () => {
    const items = [];
    const pageSize = 100;

    // The filtered endpoint still reports the library-wide file count, so walk
    // until a short page instead of trusting `total`.
    for (;;) {
      const page = await listFilesPage(pageSize, items.length, {
        unlinked: true,
      });
      if (!page) break;
      items.push(...page.items);
      if (page.items.length < pageSize) break;
    }

    return { total: items.length, items };
  },

  listArtists: listArtistsPage,
  listAllArtists: (options = {}) =>
    collectAll((limit, offset) =>
      listArtistsPage(limit, offset, options),
    ),
  getArtist: (id, includeTrackStats = false, all = false) => {
    const params = new URLSearchParams();
    if (includeTrackStats) params.set('include', 'track_stats');
    if (all) params.set('all', 'true');
    const query = params.toString();
    return request(
      `/api/v1/artists/${encodeURIComponent(id)}${query ? `?${query}` : ''}`,
    );
  },
  getArtistImportStatus: (id) =>
    request(
      `/api/v1/artists/${encodeURIComponent(id)}/import`,
    ),
  refreshArtist: (id) =>
    request(
      `/api/v1/artists/${encodeURIComponent(id)}/refresh`,
      { method: 'POST' },
    ),
  searchMusicBrainzArtists: (query, signal) =>
    request(
      `/api/v1/musicbrainz/artists?q=${encodeURIComponent(query)}`,
      { signal },
    ),
  addArtist: (id) =>
    fetch('/api/v1/artists', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id }),
    }),
  deleteArtist: (id, permanent = false) =>
    fetch(
      `/api/v1/artists/${encodeURIComponent(id)}${permanent ? '?permanent=true' : ''}`,
      {
        method: 'DELETE',
      },
    ),

  listReleaseGroups: listReleaseGroupsPage,
  listAllReleaseGroups: (options = {}) =>
    collectAll((limit, offset) =>
      listReleaseGroupsPage(limit, offset, options),
    ),
  getReleaseGroup: (id) =>
    request(`/api/v1/release-groups/${encodeURIComponent(id)}`),
  deleteReleaseGroup: (id, permanent = false) =>
    fetch(
      `/api/v1/release-groups/${encodeURIComponent(id)}${permanent ? '?permanent=true' : ''}`,
      {
        method: 'DELETE',
      },
    ),
  updateReleaseGroup: (groupId, newReleaseId) =>
    fetch(`/api/v1/release-groups/${encodeURIComponent(groupId)}/update`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ new_release_id: newReleaseId }),
    }),

  getRelease: (id, includeFiles = false) =>
    request(
      `/api/v1/releases/${encodeURIComponent(id)}` +
        (includeFiles ? '?include=files' : ''),
    ),
  getReleaseByGroup: (groupId, includeFiles = false) =>
    request(
      `/api/v1/releases?group=${encodeURIComponent(groupId)}` +
        (includeFiles ? '&include=files' : ''),
    ),
  refreshReleaseCover: (id) =>
    request(
      `/api/v1/releases/${encodeURIComponent(id)}/cover/refresh`,
      { method: 'POST' },
    ),
  refreshArtistCover: (id) =>
    request(
      `/api/v1/artists/${encodeURIComponent(id)}/cover/refresh`,
      { method: 'POST' },
    ),
  getReleaseCoverMeta: (id) =>
    request(`/api/v1/releases/${encodeURIComponent(id)}/cover/meta`),
  getArtistCoverMeta: (id) =>
    request(`/api/v1/artists/${encodeURIComponent(id)}/cover/meta`),
  deleteReleaseCover: (id) =>
    fetch(`/api/v1/releases/${encodeURIComponent(id)}/cover`, {
      method: 'DELETE',
    }),
  deleteArtistCover: (id) =>
    fetch(`/api/v1/artists/${encodeURIComponent(id)}/cover`, {
      method: 'DELETE',
    }),
  uploadReleaseCover: (id, file) =>
    request(
      `/api/v1/releases/${encodeURIComponent(id)}/cover`,
      {
        method: 'PUT',
        headers: { 'Content-Type': file?.type || 'application/octet-stream' },
        body: file,
      },
    ),
  uploadArtistCover: (id, file) =>
    request(
      `/api/v1/artists/${encodeURIComponent(id)}/cover`,
      {
        method: 'PUT',
        headers: { 'Content-Type': file?.type || 'application/octet-stream' },
        body: file,
      },
    ),

  link: (fileID, trackID) =>
    fetch(`/api/v1/files/${fileID}/link`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ track_id: trackID }),
    }),
  getSuggestions: (fileID, limit = 5, artistID) =>
    request(
      `/api/v1/files/${fileID}/suggestions?limit=${limit}${artistID ? `&artist=${encodeURIComponent(artistID)}` : ''}`,
    ),
  scan: () => fetch('/api/v1/scan', { method: 'POST' }),
  unlink: (fileID) =>
    fetch(`/api/v1/files/${fileID}/link`, { method: 'DELETE' }),
  refreshFile: (fileID) =>
    fetch(`/api/v1/files/${fileID}/refresh`, { method: 'POST' }),

  listIgnored: (limit, offset, options = {}) => {
    const params = new URLSearchParams({
      limit: String(limit),
      offset: String(offset),
    });
    if (options.query) params.set('q', options.query);
    if (options.type) params.set('type', options.type);
    if (options.sort) params.set('sort', options.sort);
    if (options.order) params.set('order', options.order);
    return request(`/api/v1/ignored?${params.toString()}`);
  },
  listAllIgnored: (options = {}) =>
    collectAll((limit, offset) =>
      request(
        `/api/v1/ignored?${new URLSearchParams({
          limit: String(limit),
          offset: String(offset),
          ...(options.query ? { q: options.query } : {}),
          ...(options.type ? { type: options.type } : {}),
        }).toString()}`,
      ),
    ),
  removeIgnored: (id) =>
    fetch(`/api/v1/ignored/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    }),
};
