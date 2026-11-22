import { coverUrl, type CreditEntry } from './api';

export function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className?: string,
  text?: string,
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text != null) node.textContent = text;
  return node;
}

export function clear(node: Element): void {
  node.replaceChildren();
}

export function byId<T extends HTMLElement = HTMLElement>(id: string): T {
  const node = document.getElementById(id);
  if (!node) throw new Error(`Missing #${id}`);
  return node as T;
}

export function pathIs(...paths: string[]): boolean {
  const path = location.pathname.replace(/\/+$/, '') || '/';
  return paths.includes(path);
}

export function link(
  href: string,
  text: string,
  className?: string,
): HTMLAnchorElement {
  const anchor = el('a', className, text);
  anchor.href = href;
  return anchor;
}

export function externalLink(
  href: string,
  text: string,
  className?: string,
): HTMLAnchorElement {
  const anchor = link(href, text, className);
  anchor.target = '_blank';
  anchor.rel = 'noreferrer noopener';
  return anchor;
}

export function normalize(value: string | null | undefined): string {
  return (value ?? '')
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase();
}

export function creditFragment(
  credit: CreditEntry[] | null | undefined,
): DocumentFragment {
  const fragment = document.createDocumentFragment();
  if (!credit || credit.length === 0) {
    fragment.append(document.createTextNode('Unknown artist'));
    return fragment;
  }

  for (const entry of credit) {
    const name = entry.credited_name || entry.name;
    const anchor = link(
      `/artist?id=${encodeURIComponent(entry.artist_id)}`,
      name,
    );
    if (entry.disambiguation) anchor.title = entry.disambiguation;
    fragment.append(anchor);
    if (entry.join_phrase)
      fragment.append(document.createTextNode(entry.join_phrase));
  }

  return fragment;
}

const ART_HUES = [36, 22, 152, 168, 210, 8, 42, 145];

function hashString(value: string): number {
  let hash = 2166136261;
  for (let i = 0; i < value.length; i += 1) {
    hash ^= value.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
}

export function initials(value: string): string {
  const words = value
    .trim()
    .split(/\s+/)
    .filter((word) => word && !/^[()[\]{}]+$/.test(word));

  if (words.length === 0) return '?';
  if (words.length === 1) {
    return Array.from(words[0]).slice(0, 2).join('').toUpperCase();
  }

  const first = Array.from(words[0])[0] ?? '';
  const last = Array.from(words[words.length - 1])[0] ?? '';
  return (first + last).toUpperCase();
}

/**
 * The deterministic hue for an art tile seed (artist, group, release or
 * track id). Exposed so pages can paint a same-hue placeholder before the
 * data arrives — used for the shared-element art transition.
 */
export function artHue(seed: string): number {
  return ART_HUES[hashString(seed || '?') % ART_HUES.length];
}

/**
 * Shared-element name pairing a card tile with its detail hero. Astro's
 * ClientRouter drives the snapshot; matching `view-transition-name`s morph
 * one into the other. Returns null under reduced motion so the navigation
 * falls back to the plain crossfade.
 */
export function sharedArtName(
  kind: 'artist' | 'group',
  id: string,
): string | null {
  if (
    typeof matchMedia !== 'undefined' &&
    matchMedia('(prefers-reduced-motion: reduce)').matches
  ) {
    return null;
  }
  return `coda-${kind}-${id}`;
}

/** Applies (or clears) a shared-element name on an art tile. */
export function setSharedArtName(
  node: HTMLElement | null | undefined,
  name: string | null,
): void {
  if (!node) return;
  if (name) node.style.setProperty('view-transition-name', name);
  else node.style.removeProperty('view-transition-name');
}

/**
 * A deterministic artist avatar: tinted tile with a person glyph and the
 * artist name (truncated for very long names). Pass `variant: "initials"`
 * for tiny contexts like pickers, where the name would be unreadable.
 * No disc, no images, no network.
 */
export function artistAvatar(
  seed: string,
  label = seed,
  className = '',
  options: { maxChars?: number; variant?: 'name' | 'initials' } = {},
): HTMLDivElement {
  const { maxChars = 20, variant = 'name' } = options;
  const root = el('div', `avatar${className ? ` ${className}` : ''}`);
  const hash = hashString(seed || label);
  const hue = ART_HUES[hash % ART_HUES.length];

  root.style.setProperty('--avatar-hue', String(hue));
  root.setAttribute('aria-hidden', 'true');

  const svgNS = 'http://www.w3.org/2000/svg';
  const icon = document.createElementNS(svgNS, 'svg');
  icon.setAttribute('viewBox', '0 0 24 24');
  icon.setAttribute('class', 'avatar__icon');
  icon.setAttribute('aria-hidden', 'true');
  const head = document.createElementNS(svgNS, 'circle');
  head.setAttribute('cx', '12');
  head.setAttribute('cy', '8');
  head.setAttribute('r', '3.6');
  const shoulders = document.createElementNS(svgNS, 'path');
  shoulders.setAttribute('d', 'M5 19.5c1.4-3.4 4-5 7-5s5.6 1.6 7 5');
  icon.append(head, shoulders);

  root.append(icon);

  if (variant === 'initials') {
    root.append(el('span', 'avatar__initials', initials(label)));
  } else {
    const name = (label || '?').trim().replace(/\s+/g, ' ');
    const chars = Array.from(name);
    const text =
      chars.length > maxChars
        ? `${chars.slice(0, Math.max(1, maxChars - 1)).join('')}…`
        : name;
    const nameNode = el('span', 'avatar__name', text || '?');
    nameNode.title = name;
    root.append(nameNode);
  }

  return root;
}

const TRACK_SVG =
  '<path d="M9.5 17.5V6l9-2v11"/><circle cx="7" cy="17.5" r="2.5"/><circle cx="16" cy="15" r="2.5"/>';

/**
 * A cover photo served by the API (`GET …/cover`), with the same footprint
 * as the deterministic tiles. When the item has no cached art (404) or the
 * image fails to load, it swaps itself for the fallback tile (disc by
 * default) — call sites never handle the fallback. The shared-element name
 * (for Astro's ClientRouter morph) carries over to the fallback tile.
 */
export function coverArt(
  kind: 'release' | 'group' | 'artist',
  id: string,
  alt = '',
  options: {
    eager?: boolean;
    fallback?: () => HTMLElement;
  } = {},
): HTMLImageElement {
  const img = document.createElement('img');
  img.className = 'cover-img';
  img.src =
    kind === 'release'
      ? coverUrl.release(id)
      : kind === 'group'
        ? coverUrl.releaseGroup(id)
        : coverUrl.artist(id);
  img.alt = alt;
  img.decoding = 'async';
  img.loading = options.eager ? 'eager' : 'lazy';
  img.addEventListener(
    'error',
    () => {
      const fallback = options.fallback ? options.fallback() : discArt(id);
      const name = img.style.getPropertyValue('view-transition-name');
      if (name) setSharedArtName(fallback, name);
      img.replaceWith(fallback);
    },
    { once: true },
  );
  return img;
}

/**
 * A deterministic disc tile for release groups and releases: coloured card
 * stock with a vinyl record and a blank centre label. No letters, no images,
 * no network.
 */
export function discArt(seed: string, className = ''): HTMLDivElement {
  const root = el('div', `disc${className ? ` ${className}` : ''}`);
  const hash = hashString(seed || 'disc');
  const hue = ART_HUES[hash % ART_HUES.length];

  root.style.setProperty('--disc-hue', String(hue));
  root.style.setProperty('--disc-angle', `${(hash % 24) - 12}deg`);
  root.setAttribute('aria-hidden', 'true');

  const record = el('span', 'disc__record');
  const ring = el('span', 'disc__ring');
  const label = el('span', 'disc__label');
  root.append(record, ring, label);

  return root;
}

/**
 * A deterministic icon tile for tracks: tinted card stock with a music-note
 * glyph. No letters, no images, no network.
 */
export function trackArt(seed: string, className = ''): HTMLDivElement {
  const root = el('div', `art art--track${className ? ` ${className}` : ''}`);
  const hash = hashString(seed || 'track');
  const hue = ART_HUES[hash % ART_HUES.length];

  root.style.setProperty('--art-hue', String(hue));
  root.setAttribute('aria-hidden', 'true');

  const svgNS = 'http://www.w3.org/2000/svg';
  const icon = document.createElementNS(svgNS, 'svg');
  icon.setAttribute('viewBox', '0 0 24 24');
  icon.setAttribute('class', 'art__icon');
  icon.setAttribute('aria-hidden', 'true');
  icon.innerHTML = TRACK_SVG;

  root.append(icon);

  return root;
}

let toastTimer: number | undefined;

export function toast(
  message: string,
  tone: 'ok' | 'error' | 'info' = 'ok',
): void {
  const node = document.getElementById('toast');
  if (!node) return;

  node.textContent = message;
  node.dataset.tone = tone;
  node.classList.add('is-visible');

  window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => {
    node.classList.remove('is-visible');
  }, 4200);
}

export function setFormStatus(
  node: HTMLElement | null,
  message: string,
  tone: 'ok' | 'error' | 'info' = 'info',
): void {
  if (!node) return;
  node.textContent = message;
  node.dataset.tone = tone;
}

export function setBusy(
  button: HTMLButtonElement | null,
  busy: boolean,
  busyLabel = 'Working…',
): void {
  if (!button) return;
  if (busy) {
    if (!button.dataset.label) button.dataset.label = button.textContent ?? '';
    button.textContent = busyLabel;
    button.disabled = true;
  } else {
    button.textContent = button.dataset.label ?? button.textContent;
    button.disabled = false;
  }
}

export async function mapLimit<T, R>(
  items: T[],
  limit: number,
  fn: (item: T, index: number) => Promise<R>,
): Promise<R[]> {
  const results = Array.from({ length: items.length }) as R[];
  let next = 0;

  const workers = Array.from(
    { length: Math.min(Math.max(1, limit), items.length) },
    async () => {
      for (;;) {
        const index = next;
        next += 1;
        if (index >= items.length) break;
        results[index] = await fn(items[index], index);
      }
    },
  );

  await Promise.all(workers);
  return results;
}

export function skeletonGrid(
  container: HTMLElement,
  count = 8,
  variant: 'artist' | 'group' = 'artist',
): void {
  clear(container);
  for (let i = 0; i < count; i += 1) {
    const card = el('div', variant === 'artist' ? 'artist-card' : 'rg-card');
    card.append(el('div', 'skeleton skeleton--cover'));
    const body = el('div', 'skeleton-card__body');
    body.append(
      el('div', 'skeleton skeleton--line skeleton--wide'),
      el('div', 'skeleton skeleton--line skeleton--short'),
    );
    card.append(body);
    container.append(card);
  }
}

export function emptyState(
  title: string,
  body: string,
  action?: { href: string; label: string },
): HTMLDivElement {
  const wrap = el('div', 'empty');
  wrap.append(
    el('p', 'eyebrow', 'Nothing here'),
    el('h3', '', title),
    el('p', '', body),
  );
  if (action)
    wrap.append(link(action.href, action.label, 'btn btn--secondary'));
  return wrap;
}
