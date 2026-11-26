export function baseName(path) {
  const parts = path.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] ?? path;
}

export function dirName(path) {
  const normalized = path.replace(/[\\/]+$/, '');
  const index = Math.max(
    normalized.lastIndexOf('/'),
    normalized.lastIndexOf('\\'),
  );
  return index > 0 ? normalized.slice(0, index) : '';
}

export function extension(path) {
  const name = baseName(path);
  const dot = name.lastIndexOf('.');
  return dot > 0 ? name.slice(dot + 1).toUpperCase() : 'FILE';
}

export function formatBytes(bytes) {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const exponent = Math.min(
    units.length - 1,
    Math.floor(Math.log(bytes) / Math.log(1024)),
  );
  const value = bytes / 1024 ** exponent;
  return `${value.toFixed(value >= 10 || exponent === 0 ? 0 : 1)} ${units[exponent]}`;
}

export function formatDate(value) {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  }).format(date);
}

export function formatDateTime(value) {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date);
}

export function yearOf(value) {
  if (!value) return '';
  const match = value.match(/\d{4}/);
  return match ? match[0] : '';
}

export function typeLabel(value) {
  if (!value) return 'Other';
  return value.charAt(0).toUpperCase() + value.slice(1);
}

/**
 * Friendly name for a cover/photo source key from the cover metadata row.
 * Unknown keys pass through unchanged so new providers degrade gracefully.
 */
export function coverSourceText(source) {
  switch (source) {
    case 'coverartarchive':
      return 'Cover Art Archive';
    case 'deezer':
      return 'Deezer';
    case 'itunes':
      return 'iTunes';
    case 'wikimedia':
      return 'Wikimedia Commons';
    case 'upload':
      return 'Uploaded';
    default:
      return source || '';
  }
}

/**
 * Hero badge text for a cover source: "Uploaded" for manual uploads,
 * "From <provider>" otherwise. Empty source means no badge.
 */
export function coverSourceBadge(source) {
  if (!source) return '';
  if (source === 'upload') return 'Uploaded';
  return `From ${coverSourceText(source)}`;
}

export function plural(count, singular, pluralForm) {
  return `${count} ${count === 1 ? singular : (pluralForm ?? `${singular}s`)}`;
}
