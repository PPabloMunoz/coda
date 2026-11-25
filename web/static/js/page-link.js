// Unlinked-files page + link wizard (artist → group → track).
// Ported from `src/pages/link.astro` — Astro guards dropped, runtime identical.

import {
  api,
  creditName,
  fmtDuration,
} from './api.js';
import {
  baseName,
  dirName,
  extension,
  formatBytes,
  formatDate,
  typeLabel,
} from './format.js';
import {
  byId,
  clear,
  el,
  emptyState,
  link,
  normalize,
  setFormStatus,
  artistAvatar,
  coverArt,
  trackArt,
  toast,
} from './ui.js';

function formatKey(file) {
  return (file.format_hint || extension(file.path)).toUpperCase();
}

function loading(text) {
  return el('p', 'notice', text);
}

function initLinkPage() {
  const list = byId('list');
  const empty = byId('empty');
  const status = byId('status');
  const filter = byId('filter');
  const formatFilter = byId('format-filter');
  const refresh = byId('refresh');
  const statUnlinked = byId('stat-unlinked');
  const statLinked = byId('stat-linked');
  const statFormats = byId('stat-formats');

  const dialog = byId('wizard');
  const wizardFileFormat = byId('wizard-file-format');
  const wizardFileName = byId('wizard-file-name');
  const wizardFilePath = byId('wizard-file-path');
  const wizardSteps = byId('wizard-steps');
  const wizardTrail = byId('wizard-trail');
  const wizardSuggest = byId('wizard-suggest');
  const wizardBody = byId('wizard-body');
  const wizardStatus = byId('wizard-status');
  const wizardBack = byId('wizard-back');
  const wizardNext = byId('wizard-next');

  let unlinkedFiles = [];
  let fileStats = null;
  let artistsCache = null;
  const groupsCache = new Map();
  let wizardState = null;
  let stepToken = 0;
  let suggestToken = 0;
  let pendingSuggestedTrackID = null;
  // Artist id for which scoped suggestions were loaded (or attempted).
  // Reset whenever the artist pick changes so re-entering the group step
  // refetches instead of reusing a stale scope.
  let scopedArtistID = null;

  function fileRow(file) {
    const row = el('article', 'file-row is-unlinked');

    const format = el('div', 'file-row__format', formatKey(file));

    const main = el('div', 'file-row__main');
    main.append(
      el('h3', 'file-row__name', baseName(file.path)),
      el('p', 'file-row__path', dirName(file.path)),
    );

    const meta = el('div', 'file-row__meta');
    meta.append(
      el('span', '', formatBytes(file.size_bytes)),
      el('span', '', `Scanned ${formatDate(file.scanned_at)}`),
    );
    main.append(meta);

    const actions = el('div', 'file-row__actions');
    const linkButton = el(
      'button',
      'btn btn--sm btn--primary btn--icon btn--icon-link',
      'Link to track',
    );
    linkButton.type = 'button';
    linkButton.addEventListener('click', () => openWizard(file));
    actions.append(linkButton);
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

  function updateStats() {
    const unlinked = unlinkedFiles.filter((file) => !file.track_id);
    statUnlinked.textContent = String(
      fileStats?.unlinked ?? unlinked.length,
    );
    statLinked.textContent = fileStats ? String(fileStats.linked) : '—';
    statFormats.textContent = String(
      new Set(unlinked.map((file) => formatKey(file))).size,
    );
  }

  function populateFormats() {
    const current = formatFilter.value;
    const formats = Array.from(
      new Set(unlinkedFiles.map(formatKey)),
    ).toSorted();

    clear(formatFilter);
    const all = new Option('All formats', '');
    formatFilter.append(all);
    for (const format of formats) {
      formatFilter.append(new Option(format, format));
    }
    formatFilter.value = formats.includes(current) ? current : '';
  }

  function renderFiles() {
    const query = normalize(filter.value);
    const selectedFormat = formatFilter.value;
    const unlinked = unlinkedFiles.filter((file) => !file.track_id);
    const visible = unlinked.filter((file) => {
      if (selectedFormat && formatKey(file) !== selectedFormat)
        return false;
      if (!query) return true;
      return normalize(`${file.path} ${formatKey(file)}`).includes(query);
    });

    clear(list);
    const fragment = document.createDocumentFragment();
    for (const file of visible) fragment.append(fileRow(file));
    list.append(fragment);

    if (visible.length === 0) {
      empty.hidden = false;
      let state;
      if (unlinked.length === 0) {
        state = emptyState(
          'Everything is linked',
          'Every scanned file is tied to a MusicBrainz track. New files appear here after a scan.',
          { href: '/files', label: 'Browse all files' },
        );
      } else if (query || selectedFormat) {
        state = emptyState(
          'No matching files',
          'Try a different name or format filter.',
        );
      } else {
        state = emptyState(
          'No unlinked files',
          'The scanner may still be running. Refresh in a moment.',
        );
      }
      empty.replaceChildren(state);
    } else {
      empty.hidden = true;
      empty.replaceChildren();
    }

    status.textContent =
      unlinked.length === 0
        ? 'All files are linked'
        : `${visible.length} of ${unlinked.length} unlinked files`;
  }

  async function loadFiles() {
    status.textContent = 'Loading files…';
    try {
      const [filesResult, statsResult] = await Promise.allSettled([
        api.listAllUnlinkedFiles(),
        api.getFileStats(),
      ]);

      if (filesResult.status === 'rejected') throw filesResult.reason;

      unlinkedFiles = filesResult.value.items;
      fileStats =
        statsResult.status === 'fulfilled' ? statsResult.value : null;

      populateFormats();
      updateStats();
      renderFiles();

      const requested = Number(
        new URLSearchParams(location.search).get('file'),
      );
      if (Number.isInteger(requested) && requested > 0) {
        const file = unlinkedFiles.find(
          (candidate) => candidate.id === requested,
        );
        if (file && !file.track_id) {
          history.replaceState(history.state, '', '/link');
          openWizard(file);
        } else {
          const existing = await api.getFile(requested).catch(() => null);
          if (existing) {
            history.replaceState(history.state, '', '/link');
            toast('This file is already linked.', 'info');
          }
        }
      }
    } catch (error) {
      clear(list);
      empty.hidden = false;
      const message =
        error instanceof Error ? error.message : String(error);
      empty.replaceChildren(emptyState('Could not load files', message));
      status.textContent = 'Failed to load';
    }
  }

  function picker(items, options) {
    const wrap = el('div', 'picker');
    const searchWrap = el('label', 'picker__search');
    const input = el('input');
    input.type = 'search';
    input.name = 'q';
    input.placeholder = options.placeholder;
    input.setAttribute('aria-label', options.placeholder.replace(/…$/, ''));
    input.autocomplete = 'off';
    input.spellcheck = false;
    searchWrap.append(input);
    wrap.append(searchWrap);

    const listNode = el('div', 'picker__list');
    const entries = [];
    const noMatch = el('div', 'picker__empty', 'No matches.');
    noMatch.hidden = true;

    for (const item of items) {
      const button = el('button', 'pick');
      button.type = 'button';
      button.setAttribute(
        'aria-pressed',
        options.isSelected(item.data) ? 'true' : 'false',
      );

      const art = el('span', 'pick__art');
      if (item.artSeed) {
        art.append(
          item.artKind === 'artist'
            ? artistAvatar(item.artSeed, item.title, '', {
                variant: 'initials',
              })
            : item.artKind === 'track'
              ? trackArt(item.artSeed)
              : coverArt('group', item.artSeed, item.title),
        );
      } else {
        art.append(el('span', 'pick__num', item.number ?? ''));
      }

      const main = el('span', 'pick__main');
      main.append(el('span', 'pick__title', item.title));
      if (item.meta) main.append(el('span', 'pick__meta', item.meta));

      button.append(art, main, el('span', 'pick__mark', '✓'));

      button.addEventListener('click', () => {
        options.onSelect(item.data);
        for (const entry of entries) {
          entry.button.setAttribute(
            'aria-pressed',
            entry.button === button ? 'true' : 'false',
          );
        }
      });

      listNode.append(button);
      entries.push({
        button,
        text: normalize(
          `${item.title} ${item.meta ?? ''} ${item.search ?? ''}`,
        ),
      });
    }

    if (items.length === 0) {
      listNode.append(el('div', 'picker__empty', options.emptyText));
    } else {
      listNode.append(noMatch);
    }

    input.addEventListener('input', () => {
      const query = normalize(input.value);
      let visible = 0;
      for (const entry of entries) {
        const show = !query || entry.text.includes(query);
        entry.button.hidden = !show;
        if (show) visible += 1;
      }
      noMatch.hidden = visible > 0;
    });

    wrap.append(listNode);

    if (options.autoFocus !== false) {
      requestAnimationFrame(() => input.focus());
    }

    return wrap;
  }

  function canContinue() {
    const state = wizardState;
    if (!state) return false;
    if (state.step === 0) return Boolean(state.artist);
    if (state.step === 1) {
      return Boolean(state.release && state.release.tracks.length > 0);
    }
    return Boolean(state.track);
  }

  function updateFooter() {
    if (!wizardState) return;
    wizardNext.textContent =
      wizardState.step === 2 ? 'Link file' : 'Continue';
    wizardNext.disabled = !canContinue();
    wizardBack.disabled = wizardState.step === 0;
  }

  function updateSteps() {
    if (!wizardState) return;
    const steps = Array.from(wizardSteps.querySelectorAll('li'));
    steps.forEach((step, index) => {
      step.classList.toggle('is-active', index === wizardState?.step);
      step.classList.toggle('is-done', index < (wizardState?.step ?? 0));
    });
  }

  function updateTrail() {
    if (!wizardState) return;
    clear(wizardTrail);

    if (wizardState.artist) {
      wizardTrail.append(
        el('span', 'chip', `Artist: ${wizardState.artist.name}`),
      );
    }
    if (wizardState.group) {
      wizardTrail.append(
        el('span', 'chip', `Group: ${wizardState.group.title}`),
      );
    }
    if (wizardState.release) {
      wizardTrail.append(
        el('span', 'chip', `Release: ${wizardState.release.title}`),
      );
    }
    if (wizardState.track) {
      wizardTrail.append(
        el(
          'span',
          'chip',
          `Track: ${wizardState.track.number || wizardState.track.position} · ${wizardState.track.title}`,
        ),
      );
    }
  }

  function markChanged() {
    updateTrail();
    updateFooter();
  }

  function advance() {
    if (!wizardState || wizardState.step === 2) return;
    wizardState.step = wizardState.step + 1;
    renderWizard();
  }

  async function renderArtistStep(token) {
    wizardBody.append(loading('Loading artists…'));
    try {
      if (!artistsCache) {
        const result = await api.listAllArtists();
        artistsCache = result.items;
      }
      if (token !== stepToken || !wizardState) return;

      clear(wizardBody);
      const state = wizardState;

      if (artistsCache.length === 0) {
        wizardBody.append(
          el(
            'div',
            'picker__empty',
            'No artists in the library yet. Add one from the Artists page first.',
          ),
        );
        return;
      }

      wizardBody.append(
        picker(
          artistsCache.map((artist) => ({
            data: artist,
            title: artist.name,
            meta:
              artist.disambiguation ||
              (artist['sort-name'] && artist['sort-name'] !== artist.name
                ? `Sort name: ${artist['sort-name']}`
                : 'MusicBrainz artist'),
            search: artist['sort-name'] ?? '',
            artSeed: artist.id,
            artKind: 'artist',
          })),
          {
            placeholder: 'Find an artist…',
            emptyText: 'No artists match.',
            isSelected: (artist) => state.artist?.id === artist.id,
            onSelect: (artist) => {
              state.artist = artist;
              state.group = null;
              state.release = null;
              state.track = null;
              // A new pick invalidates any scoped suggestion: the next
              // group step refetches for this artist.
              scopedArtistID = null;
              clear(wizardSuggest);
              wizardSuggest.hidden = true;
              markChanged();
              // Selecting an artist advances immediately; Back/Continue
              // stay available for manual navigation.
              if (wizardState === state && state.step === 0) advance();
            },
          },
        ),
      );
    } catch (error) {
      if (token !== stepToken) return;
      clear(wizardBody);
      const message =
        error instanceof Error ? error.message : String(error);
      wizardBody.append(
        el('div', 'picker__empty', `Could not load artists: ${message}`),
      );
    }
  }

  async function renderGroupStep(token) {
    const state = wizardState;
    if (!state?.artist) return;

    // Second-chance suggestions scoped to the picked artist. Skipped
    // while an accepted suggestion is being applied (group already set).
    if (
      !state.group &&
      scopedArtistID !== state.artist.id &&
      !pendingSuggestedTrackID
    ) {
      void loadScopedSuggestions(state);
    }

    wizardBody.append(loading('Loading release groups…'));
    try {
      let groups = groupsCache.get(state.artist.id);
      if (!groups) {
        const result = await api.listAllReleaseGroups({
          artist: state.artist.id,
        });
        groups = result.items;
        groupsCache.set(state.artist.id, groups);
      }
      if (token !== stepToken || !wizardState) return;

      clear(wizardBody);

      if (groups.length === 0) {
        wizardBody.append(
          el(
            'div',
            'picker__empty',
            'No release groups for this artist yet. The discography import may still be running.',
          ),
        );
        return;
      }

      wizardBody.append(
        picker(
          groups.map((group) => ({
            data: group,
            title: group.title,
            meta: typeLabel(group.type),
            artSeed: group.id,
            artKind: 'group',
            search: creditName(group.credit),
          })),
          {
            placeholder: 'Find a release group…',
            emptyText: 'No release groups match.',
            isSelected: (group) => state.group?.id === group.id,
            onSelect: (group) => {
              state.group = group;
              state.release = null;
              state.track = null;
              markChanged();
              // One saved release per group: skip the release step and
              // jump straight to its tracks.
              void loadReleaseAndAdvance();
            },
          },
        ),
      );
    } catch (error) {
      if (token !== stepToken) return;
      clear(wizardBody);
      const message =
        error instanceof Error ? error.message : String(error);
      wizardBody.append(
        el(
          'div',
          'picker__empty',
          `Could not load release groups: ${message}`,
        ),
      );
    }
  }

  async function loadReleaseAndAdvance() {
    const state = wizardState;
    if (!state?.group || state.step !== 1) return;

    // A group is now fixed; any scoped banner for the artist is stale.
    wizardSuggest.hidden = true;

    const token = ++stepToken;
    clear(wizardBody);
    wizardBody.append(loading('Loading tracks…'));
    updateFooter();

    try {
      const release = await api.getReleaseByGroup(state.group.id);
      if (token !== stepToken || wizardState !== state) return;

      if (!release) {
        clear(wizardBody);
        wizardBody.append(
          el(
            'div',
            'picker__empty',
            'No release is saved for this group yet. Choose another group or wait for the discography import.',
          ),
        );
        updateFooter();
        pendingSuggestedTrackID = null;
        return;
      }

      if (release.tracks.length === 0) {
        state.release = release;
        updateTrail();
        clear(wizardBody);
        wizardBody.append(
          el(
            'div',
            'picker__empty',
            'This release has no tracks saved, so there is nothing to link.',
          ),
        );
        updateFooter();
        pendingSuggestedTrackID = null;
        return;
      }

      state.release = release;
      state.track = null;
      state.step = 2;
      renderWizard();
      if (pendingSuggestedTrackID && wizardState === state) {
        const match = state.release.tracks.find(
          (candidate) => candidate.id === pendingSuggestedTrackID,
        );
        if (match) {
          state.track = match;
          markChanged();
        }
        pendingSuggestedTrackID = null;
      }
    } catch (error) {
      if (token !== stepToken || wizardState !== state) return;
      clear(wizardBody);
      const message =
        error instanceof Error ? error.message : String(error);
      wizardBody.append(
        el('div', 'picker__empty', `Could not load tracks: ${message}`),
      );
      updateFooter();
    }
  }

  function renderTrackStep(token) {
    const state = wizardState;
    if (!state?.release) return;

    clear(wizardBody);
    const tracks = state.release.tracks;

    if (tracks.length === 0) {
      wizardBody.append(
        el(
          'div',
          'picker__empty',
          'This release has no tracks saved, so there is nothing to link.',
        ),
      );
      return;
    }

    wizardBody.append(
      picker(
        tracks.map((track) => ({
          data: track,
          title: track.title,
          meta: [
            `${track.disc}.${track.number || track.position}`,
            fmtDuration(track.dur_ms),
            creditName(track.credit),
          ]
            .filter(Boolean)
            .join(' · '),
          artSeed: track.id,
          artKind: 'track',
          search: creditName(track.credit),
        })),
        {
          placeholder: 'Find a track…',
          emptyText: 'No tracks match.',
          isSelected: (track) => state.track?.id === track.id,
          onSelect: (track) => {
            state.track = track;
            // Last step: select only, wait for the Link button.
            markChanged();
          },
        },
      ),
    );
    updateFooter();
    if (token !== stepToken) return;
  }

  async function renderStep() {
    if (!wizardState) return;
    const token = ++stepToken;
    clear(wizardBody);
    setFormStatus(wizardStatus, '');

    if (wizardState.step === 0) await renderArtistStep(token);
    else if (wizardState.step === 1) await renderGroupStep(token);
    else renderTrackStep(token);
  }

  function renderWizard() {
    if (!wizardState) return;
    updateSteps();
    updateTrail();
    updateFooter();
    void renderStep();
  }

  function openWizard(file) {
    wizardState = {
      file,
      step: 0,
      artist: null,
      group: null,
      release: null,
      track: null,
    };
    scopedArtistID = null;

    wizardFileFormat.textContent = formatKey(file);
    wizardFileName.textContent = baseName(file.path);
    wizardFilePath.textContent = dirName(file.path);

    if (!dialog.open) dialog.showModal();
    renderWizard();
    void loadSuggestions(file);
  }

  function renderSuggestBanner(top, preserveArtist = false) {
    clear(wizardSuggest);

    const banner = el('div', 'suggest-banner');
    const art = el('span', 'pick__art');
    art.append(coverArt('group', top.group.id, top.group.title));
    banner.append(art);

    const main = el('span', 'pick__main');
    main.append(el('span', 'eyebrow', 'Suggested match'));
    const pct = Math.round(top.score * 100);
    // Confidence tiers mirror the design-system chips: green ≥ 90
    // (exact/tag-strong), blue 70–89 (good, incl. filename-capped),
    // amber < 70 (weak — confirm carefully).
    const tier =
      top.score >= 0.9 ? 'high' : top.score >= 0.7 ? 'med' : 'low';
    banner.classList.add(`suggest-banner--${tier}`);
    main.append(
      el(
        'span',
        'pick__title',
        `${top.track.title} — ${top.artist.name} · ${top.release.title}`,
      ),
    );
    const details = [
      top.track.credit?.length ? creditName(top.track.credit) : '',
      top.reasons.join(' · '),
      top.has_file ? 'already has a file' : '',
    ]
      .filter(Boolean)
      .join(' · ');
    if (details) main.append(el('span', 'pick__meta', details));
    banner.append(main);

    const actions = el('span', 'suggest-banner__actions');
    const tone =
      tier === 'high'
        ? 'chip--ok'
        : tier === 'low'
          ? 'chip--warn'
          : 'chip--type';
    const score = el('span', `chip ${tone}`, `${pct}% match`);
    score.title = `Match confidence: ${pct}%`;
    actions.append(score);
    const accept = el(
      'button',
      'btn btn--sm btn--primary',
      'Use suggestion',
    );
    accept.type = 'button';
    accept.addEventListener('click', () => {
      if (!wizardState) return;
      // Scoped accept: the artist is already fixed by the user's pick.
      if (!preserveArtist) {
        wizardState.artist = top.artist;
      } else if (!wizardState.artist) {
        return;
      }
      wizardState.group = top.group;
      wizardState.release = null;
      wizardState.track = null;
      wizardState.step = 1;
      pendingSuggestedTrackID = top.track.id;
      wizardSuggest.hidden = true;
      renderWizard();
      void loadReleaseAndAdvance();
    });
    const dismiss = el('button', 'btn btn--sm btn--ghost', 'Dismiss');
    dismiss.type = 'button';
    dismiss.addEventListener('click', () => {
      wizardSuggest.hidden = true;
      clear(wizardSuggest);
    });
    actions.append(accept, dismiss);
    banner.append(actions);

    wizardSuggest.append(banner);
    wizardSuggest.hidden = false;
  }

  async function loadScopedSuggestions(state) {
    const artist = state.artist;
    if (!artist) return;
    scopedArtistID = artist.id;

    const token = ++suggestToken;
    clear(wizardSuggest);
    wizardSuggest.hidden = false;
    wizardSuggest.append(el('p', 'muted', 'Finding suggestions…'));

    try {
      const res = await api.getSuggestions(state.file.id, 3, artist.id);
      if (token !== suggestToken || wizardState !== state) {
        return;
      }
      clear(wizardSuggest);
      if (!res || res.suggestions.length === 0) {
        wizardSuggest.append(
          el(
            'p',
            'muted',
            'No suggestions for this artist — pick a group below.',
          ),
        );
        return;
      }
      renderSuggestBanner(res.suggestions[0], true);
    } catch {
      if (token !== suggestToken || wizardState !== state) return;
      clear(wizardSuggest);
      wizardSuggest.append(
        el(
          'p',
          'muted',
          'No suggestions for this artist — pick a group below.',
        ),
      );
    }
  }

  // Global suggestions: ranked by the server's (deliberately simple,
  // heuristic — see internal/store/suggest.go) matcher. The banner only
  // preselects; the user always confirms via the Link button.
  async function loadSuggestions(file) {
    const token = ++suggestToken;
    clear(wizardSuggest);
    wizardSuggest.hidden = false;
    wizardSuggest.append(el('p', 'muted', 'Finding suggestions…'));

    try {
      const res = await api.getSuggestions(file.id, 3);
      if (
        token !== suggestToken ||
        !wizardState ||
        wizardState.file.id !== file.id
      ) {
        return;
      }
      clear(wizardSuggest);
      if (!res || res.suggestions.length === 0) {
        wizardSuggest.append(
          el('p', 'muted', 'No suggestions found — pick manually below.'),
        );
        return;
      }
      renderSuggestBanner(res.suggestions[0]);
    } catch {
      if (token !== suggestToken || !wizardState) return;
      clear(wizardSuggest);
      wizardSuggest.append(
        el('p', 'muted', 'No suggestions found — pick manually below.'),
      );
    }
  }

  async function linkSelected() {
    const state = wizardState;
    if (!state?.file || !state.track) return;

    const file = state.file;
    const track = state.track;

    wizardNext.disabled = true;
    wizardBack.disabled = true;
    wizardNext.textContent = 'Moving…';
    setFormStatus(wizardStatus, 'Moving file and writing tags…');

    try {
      const response = await api.link(file.id, track.id);

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
        setFormStatus(wizardStatus, message, 'error');
        updateFooter();
        return;
      }

      toast(`${baseName(file.path)} linked to “${track.title}”.`, 'ok');
      // The path may have changed, so reload from the server.
      await loadFiles();
      dialog.close();
    } catch (error) {
      const message =
        error instanceof Error ? error.message : String(error);
      setFormStatus(
        wizardStatus,
        `Could not link file: ${message}`,
        'error',
      );
      updateFooter();
    }
  }

  byId('wizard-close').addEventListener('click', () => dialog.close());
  dialog.addEventListener('click', (event) => {
    if (event.target === dialog) dialog.close();
  });
  dialog.addEventListener('close', () => {
    wizardState = null;
    stepToken += 1;
    suggestToken += 1;
    pendingSuggestedTrackID = null;
    scopedArtistID = null;
    clear(wizardSuggest);
    wizardSuggest.hidden = true;
  });

  wizardBack.addEventListener('click', () => {
    if (!wizardState || wizardState.step === 0) return;
    wizardState.step = wizardState.step - 1;
    if (wizardState.step === 0) {
      // Reconsidering the artist invalidates its scoped suggestion;
      // re-advancing refetches instead of showing a stale artist.
      scopedArtistID = null;
      clear(wizardSuggest);
      wizardSuggest.hidden = true;
    }
    renderWizard();
  });

  wizardNext.addEventListener('click', () => {
    if (!wizardState) return;
    if (wizardState.step === 2) {
      void linkSelected();
      return;
    }
    advance();
  });

  filter.addEventListener('input', renderFiles);
  formatFilter.addEventListener('change', renderFiles);
  refresh.addEventListener('click', loadFiles);

  loadFiles();
}

initLinkPage();
