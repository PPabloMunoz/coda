export interface LocalFile {
  id: number;
  root_id: number;
  path: string;
  size_bytes: number;
  mtime_ns: number;
  format_hint: string;
  scanned_at: string;
  track_id: string | null;
  track?: LinkedTrack | null;
}

export interface ArtistTrackStats {
  track_count: number;
  linked_track_count: number;
}

export interface Artist {
  id: string;
  name: string;
  'sort-name': string | null;
  disambiguation: string | null;
  active: number;
  in_library: number;
  stats?: ArtistTrackStats | null;
}

export interface MusicBrainzArtist {
  id: string;
  name: string;
  'sort-name': string;
  disambiguation: string;
  type: string;
  country: string;
  score: number;
}

export type ArtistImportState =
  | 'idle'
  | 'queued'
  | 'running'
  | 'done'
  | 'failed';

export interface ArtistImportStatus {
  artist_id: string;
  state: ArtistImportState;
  queued_at?: string;
  started_at?: string;
  finished_at?: string;
  error?: string;
}

export interface CreditEntry {
  artist_id: string;
  name: string;
  sort_name: string | null;
  disambiguation: string | null;
  credited_name: string;
  join_phrase: string;
}

export interface ReleaseGroupRef {
  id: string;
  title: string;
  type: string;
}

export interface ReleaseRef {
  id: string;
  title: string;
  date: string | null;
  status: string | null;
}

export interface LinkedTrack {
  id: string;
  title: string;
  disc: number;
  position: number;
  number: string;
  recording_id: string;
  credit: CreditEntry[];
  release: ReleaseRef;
  group: ReleaseGroupRef;
}

export interface ReleaseSummary {
  id: string;
  title: string;
  date: string | null;
  status: string | null;
  track_count: number;
  linked_file_count: number;
}

export interface ReleaseGroup {
  id: string;
  title: string;
  type: string;
  credit: CreditEntry[];
  release?: ReleaseSummary | null;
}

export interface LinkedFile {
  id: number;
  path: string;
  format_hint: string;
  size_bytes: number;
}

export interface Track {
  id: string;
  release_id: string;
  disc: number;
  position: number;
  number: string;
  title: string;
  credit: CreditEntry[];
  dur_ms: number | null;
  recording_id: string;
  files?: LinkedFile[];
}

export interface Release {
  id: string;
  title: string;
  date: string | null;
  status: string | null;
  credit: CreditEntry[];
  group: ReleaseGroup;
  tracks: Track[];
}

export type Page<T> = { total: number; items: T[] };
export type AllPages<T> = { total: number; items: T[] };

export interface LocalFilesStats {
  total: number;
  linked: number;
  unlinked: number;
  formats: Record<string, number>;
}

export interface LibraryStats {
  artists: number;
  release_groups: number;
  releases: number;
  tracks: number;
  files: LocalFilesStats;
}

export const MBID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

const MBID_ANYWHERE =
  /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;

export function normMbid(value: string): string {
  return value.trim().toLowerCase();
}

/** Accepts a bare MBID or a MusicBrainz URL and returns the first MBID found. */
export function extractMbid(value: string): string {
  const match = value.match(MBID_ANYWHERE);
  return match ? match[0].toLowerCase() : normMbid(value);
}

export function creditName(credit: CreditEntry[] | null | undefined): string {
  if (!credit || credit.length === 0) return '';
  return credit
    .map(
      (entry) =>
        (entry.credited_name || entry.name) + (entry.join_phrase || ''),
    )
    .join('');
}

export function fmtDuration(ms: number | null | undefined): string {
  if (ms == null) return '';
  const total = Math.round(ms / 1000);
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`;
}

export const mbUrl = {
  artist: (id: string) => `https://musicbrainz.org/artist/${id}`,
  releaseGroup: (id: string) => `https://musicbrainz.org/release-group/${id}`,
  release: (id: string) => `https://musicbrainz.org/release/${id}`,
  recording: (id: string) => `https://musicbrainz.org/recording/${id}`,
  track: (id: string) => `https://musicbrainz.org/track/${id}`,
};

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

export async function request<T>(
  path: string,
  init?: RequestInit,
): Promise<T | null> {
  const res = await fetch(path, init);

  if (res.status === 304 || res.status === 404) return null;

  if (!res.ok) {
    let message: string = res.statusText;
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
  return JSON.parse(text) as T;
}

export interface ArtistListOptions {
  query?: string;
  sort?: string;
  order?: string;
  includeTrackStats?: boolean;
}

async function listArtistsPage(
  limit: number,
  offset: number,
  options: ArtistListOptions = {},
) {
  const params = new URLSearchParams({
    limit: String(limit),
    offset: String(offset),
  });
  if (options.query) params.set('q', options.query);
  if (options.sort) params.set('sort', options.sort);
  if (options.order) params.set('order', options.order);
  if (options.includeTrackStats) params.set('include', 'track_stats');
  return request<Page<Artist>>(`/api/artists?${params.toString()}`);
}

export interface FileListOptions {
  unlinked?: boolean;
  includeTrack?: boolean;
  query?: string;
  format?: string;
  sort?: string;
  order?: string;
}

async function listFilesPage(
  limit: number,
  offset: number,
  options: FileListOptions = {},
) {
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
  return request<Page<LocalFile>>(`/api/files?${params.toString()}`);
}

export interface ReleaseGroupListOptions {
  artist?: string;
  includeRelease?: boolean;
  query?: string;
  type?: string;
  hasRelease?: boolean;
  primary?: boolean;
  sort?: string;
  order?: string;
}

async function listReleaseGroupsPage(
  limit: number,
  offset: number,
  options: ReleaseGroupListOptions = {},
) {
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
  return request<Page<ReleaseGroup>>(
    `/api/release-groups?${params.toString()}`,
  );
}

/**
 * The API caps page size at 100. Most screens want the whole library, so this
 * walks the pages and returns the lot. `maxItems` is a guard against a runaway
 * loop, not a product limit.
 */
export async function collectAll<T>(
  fetchPage: (limit: number, offset: number) => Promise<Page<T> | null>,
  pageSize = 100,
  maxItems = 5000,
): Promise<AllPages<T>> {
  const items: T[] = [];
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

  return { items, total };
}

export function pageParams(defaultLimit = 10): {
  limit: number;
  offset: number;
} {
  const query = new URLSearchParams(location.search);
  const limit = Math.min(
    100,
    Math.max(1, Number(query.get('limit')) || defaultLimit),
  );
  const offset = Math.max(0, Number(query.get('offset')) || 0);
  return { limit, offset };
}

export const api = {
  health: () => fetch('/api/health').then((res) => res.text()),

  listFiles: listFilesPage,
  getFile: (id: string | number, includeTrack = false) =>
    request<LocalFile>(
      `/api/files/${encodeURIComponent(String(id))}` +
        (includeTrack ? '?include=track' : ''),
    ),
  getFileStats: () => request<LocalFilesStats>('/api/files/stats'),
  getStats: () => request<LibraryStats>('/api/stats'),
  listUnlinkedFiles: (limit: number, offset: number) =>
    listFilesPage(limit, offset, { unlinked: true }),
  listAllUnlinkedFiles: async (): Promise<AllPages<LocalFile>> => {
    const items: LocalFile[] = [];
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
  listAllArtists: (options: ArtistListOptions = {}) =>
    collectAll<Artist>((limit, offset) =>
      listArtistsPage(limit, offset, options),
    ),
  getArtist: (id: string, includeTrackStats = false, all = false) => {
    const params = new URLSearchParams();
    if (includeTrackStats) params.set('include', 'track_stats');
    if (all) params.set('all', 'true');
    const query = params.toString();
    return request<Artist>(
      `/api/artists/${encodeURIComponent(id)}${query ? `?${query}` : ''}`,
    );
  },
  getArtistImportStatus: (id: string) =>
    request<ArtistImportStatus>(
      `/api/artists/${encodeURIComponent(id)}/import`,
    ),
  refreshArtist: (id: string) =>
    request<ArtistImportStatus>(
      `/api/artists/${encodeURIComponent(id)}/refresh`,
      { method: 'POST' },
    ),
  searchMusicBrainzArtists: (query: string, signal?: AbortSignal) =>
    request<Page<MusicBrainzArtist>>(
      `/api/musicbrainz/artists?q=${encodeURIComponent(query)}`,
      { signal },
    ),
  addArtist: (id: string) =>
    fetch('/api/artists', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id }),
    }),
  deleteArtist: (id: string) =>
    fetch(`/api/artists/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    }),

  listReleaseGroups: listReleaseGroupsPage,
  listAllReleaseGroups: (options: ReleaseGroupListOptions = {}) =>
    collectAll<ReleaseGroup>((limit, offset) =>
      listReleaseGroupsPage(limit, offset, options),
    ),
  getReleaseGroup: (id: string) =>
    request<ReleaseGroup>(`/api/release-groups/${encodeURIComponent(id)}`),
  deleteReleaseGroup: (id: string) =>
    fetch(`/api/release-groups/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    }),
  updateReleaseGroup: (groupId: string, newReleaseId: string) =>
    fetch(`/api/release-groups/${encodeURIComponent(groupId)}/update`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ new_release_id: newReleaseId }),
    }),

  getRelease: (id: string, includeFiles = false) =>
    request<Release>(
      `/api/releases/${encodeURIComponent(id)}` +
        (includeFiles ? '?include=files' : ''),
    ),
  getReleaseByGroup: (groupId: string, includeFiles = false) =>
    request<Release>(
      `/api/releases?group=${encodeURIComponent(groupId)}` +
        (includeFiles ? '&include=files' : ''),
    ),

  link: (fileID: number, trackID: string, move = false) =>
    fetch(`/api/files/${fileID}/link`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ track_id: trackID, move }),
    }),
  scan: () => fetch('/api/scan', { method: 'POST' }),
  unlink: (fileID: number) =>
    fetch(`/api/files/${fileID}/link`, { method: 'DELETE' }),
  refreshFile: (fileID: number) =>
    fetch(`/api/files/${fileID}/refresh`, { method: 'POST' }),
};
