// Persistent user settings and transcript, kept in the local SQLite database
// (via SettingsService and TranscriptService).
import {Events} from '@wailsio/runtime';
import {SettingsService, TranscriptService} from '../../bindings/changeme/backend';

// Both lived in localStorage before SQLite; they are moved over once.
const LEGACY_SETTINGS_KEY = 'stt-settings';
const LEGACY_TRANSCRIPT_KEY = 'stt-transcript';

/** Default voice command phrases (users can edit these). */
export const defaultCommands = {
  wake: ['hey ada', 'ada', 'hayda', 'heyda'],
  sleep: ['tamam ada', 'dur ada', 'durdur ada', 'teşekkürler ada', 'teşekkür ederim ada', 'stop ada'],
  stop: ['dinlemeyi kapat', 'ada kapan'],
};

const defaults = {
  modelId: '',
  device: 'cpu', // 'cpu' | 'gpu'
  language: 'tr',
  languages: ['tr', 'en'], // languages shown in the language menu
  translate: false,
  threads: 0, // 0 = auto
  microphone: '', // '' = system default
  silenceMs: 800,
  vad: true, // Silero VAD speech detection
  vadModel: 'silero-vad', // which VAD model to use
  prompt: '', // names/terms Whisper should prefer
  vadSens: 50, // speech detection sensitivity 0..100
  minSpeechMs: 96, // shortest sound counted as speech
  wakeWord: false, // wait for "Hey Ada" / "Ada"
  wakeSec: 8, // silence before going back to standby
  wakeHoldForAI: true, // stay awake while the AI is replying
  wakeSound: true,
  commands: structuredClone(defaultCommands),
  showTimestamps: true,
  autoScroll: true,
  fontSize: 20,
  theme: 'system', // 'system' | 'light' | 'dark'
};

function read(key, fallback) {
  try {
    const raw = localStorage.getItem(key);
    return raw ? JSON.parse(raw) : fallback;
  } catch {
    return fallback;
  }
}

/** True in the separate settings window (opened at /#/settings/<page>). */
export const isSettingsWindow = location.hash.startsWith('#/settings');

export const settings = $state(structuredClone(defaults));

/** Save status: idle | saving | saved | error. */
export const saveState = $state({status: 'idle', at: 0, error: ''});

/**
 * Unsaved changes per part of the settings window. The main window saves its
 * quick toggles (theme, font size…) automatically; the settings window only
 * saves when the user presses "Kaydet".
 */
export const unsaved = $state({settings: false, ai: false});
export const hasUnsaved = () => Object.values(unsaved).some(Boolean);

const autoSave = !isSettingsWindow;

// Identifies this window, so it can ignore its own change notifications.
const windowId = Math.random().toString(36).slice(2);
// What the database holds, as far as this window knows (key → JSON string).
let saved = {};
let savedVersion = $state(0); // bumped when `saved` changes, so effects re-check
let saveTimer = 0;

const encodeAll = (obj) => Object.fromEntries(Object.entries(obj).map(([k, v]) => [k, JSON.stringify(v)]));
const decode = (json) => (json === undefined ? undefined : JSON.parse(json));

function markSaved(values) {
  Object.assign(saved, encodeAll(values));
  savedVersion++;
}

/** Settings keys whose value differs from what is saved. */
function pending() {
  const now = $state.snapshot(settings);
  const out = {};
  for (const [k, v] of Object.entries(now)) if (saved[k] !== JSON.stringify(v)) out[k] = v;
  return out;
}

async function flush() {
  clearTimeout(saveTimer);
  saveTimer = 0;
  const changes = pending();
  if (!Object.keys(changes).length) return true;
  saveState.status = 'saving';
  try {
    await SettingsService.Save(windowId, changes);
    markSaved(changes);
    saveState.status = 'saved';
    saveState.at = Date.now();
    saveState.error = '';
    return true;
  } catch (e) {
    saveState.status = 'error';
    saveState.error = String(e?.message ?? e);
    return false;
  }
}

// Other parts of the settings window with their own data (the AI page)
// register how to save and discard it.
const savers = new Map();

/** @param {keyof typeof unsaved} name */
export function registerSaver(name, saver) {
  savers.set(name, saver);
  return () => savers.delete(name);
}

/** Saves every unsaved change. Returns false if something failed. */
export async function saveAll() {
  let ok = await flush();
  for (const [name, saver] of savers) {
    if (!unsaved[name]) continue;
    try {
      await saver.save();
    } catch (e) {
      ok = false;
      saveState.status = 'error';
      saveState.error = String(e?.message ?? e);
    }
  }
  if (ok) {
    saveState.status = 'saved';
    saveState.at = Date.now();
    saveState.error = '';
  }
  return ok;
}

/** Throws away unsaved changes, going back to what is saved. */
export function discardAll() {
  const d = structuredClone(defaults);
  for (const k of Object.keys(pending())) settings[k] = decode(saved[k]) ?? d[k];
  for (const [name, saver] of savers) if (unsaved[name]) saver.discard();
  if (saveState.status === 'error') saveState.status = 'idle';
}

/** Put every setting back to its default (also resets the AI settings). */
export async function resetSettings() {
  clearTimeout(saveTimer);
  saveTimer = 0;
  await SettingsService.Reset(); // the settings:changed event applies the defaults
}

function applyDefaults() {
  clearTimeout(saveTimer);
  saveTimer = 0;
  const d = structuredClone(defaults);
  saved = {};
  markSaved(d);
  Object.assign(settings, d);
  saveState.status = 'saved';
  saveState.at = Date.now();
}

/**
 * Loads the saved settings from the database and starts tracking changes.
 * Must finish before the UI mounts.
 */
export async function loadSettings() {
  let stored = {};
  try {
    stored = (await SettingsService.Load()) ?? {};
  } catch (e) {
    console.error('Ayarlar yüklenemedi:', e);
    saveState.status = 'error';
    saveState.error = String(e?.message ?? e);
  }
  const legacy = read(LEGACY_SETTINGS_KEY, null);
  // One-time move of the old localStorage settings into the database.
  if (!Object.keys(stored).length && legacy && typeof legacy === 'object') {
    try {
      await SettingsService.Save(windowId, legacy);
      stored = legacy;
    } catch (e) {
      console.error('Eski ayarlar taşınamadı:', e);
    }
  }
  if (legacy) try { localStorage.removeItem(LEGACY_SETTINGS_KEY); } catch { /* ignore */ }

  Object.assign(settings, stored);
  // Keys never saved hold their default value; that doesn't count as a change.
  markSaved({...structuredClone(defaults), ...stored});

  // Keep windows in sync: apply changes saved by other windows, except where
  // this window has its own unsaved edit of the same setting.
  Events.On('settings:changed', (e) => {
    const ch = e.data;
    if (ch.reset) return applyDefaults();
    if (ch.source === windowId) return;
    const local = pending();
    for (const [k, v] of Object.entries(ch.values)) if (!(k in local)) settings[k] = v;
    markSaved(ch.values);
  });

  $effect.root(() => {
    $effect(() => {
      savedVersion; // re-check after saves
      const dirty = Object.keys(pending()).length > 0;
      if (!autoSave) {
        unsaved.settings = dirty;
        return;
      }
      // Main window: write quick toggles shortly after they change.
      if (!dirty) return;
      clearTimeout(saveTimer);
      saveTimer = setTimeout(flush, 300);
    });
  });
  if (autoSave) window.addEventListener('beforeunload', () => { if (saveTimer) flush(); });
}

/**
 * Transcript rows. `ai` is set once the row has been sent to the AI:
 * {status: 'pending'|'streaming'|'done'|'error'|'canceled', text, error, model}.
 * `source` is 'typed' for messages written in the chat box ('' = spoken);
 * `attachments` lists files sent with it ({id, name, mime, size}).
 * @type {{id: number, text: string, at: number, ai?: any, source?: string, attachments?: any[]}[]}
 */
export const transcript = $state([]);

// What the database holds (row ID → JSON string).
let savedRows = new Map();
let rowTimer = 0;

const rowKey = (r) => String(r.id);
const rowData = (r) => ({id: r.id, text: r.text, at: r.at, ai: r.ai ?? null, source: r.source ?? '', attachments: r.attachments ?? []});
const encodeRow = (r) => JSON.stringify(rowData(r));

async function flushTranscript() {
  clearTimeout(rowTimer);
  rowTimer = 0;
  const now = new Map($state.snapshot(transcript).map((r) => [rowKey(r), r]));
  const upserts = [];
  const deletes = [];
  for (const [k, r] of now) {
    const enc = encodeRow(r);
    if (savedRows.get(k) !== enc) upserts.push(rowData(r));
  }
  for (const k of savedRows.keys()) if (!now.has(k)) deletes.push(Number(k));
  if (!upserts.length && !deletes.length) return;
  try {
    await TranscriptService.Sync(upserts, deletes);
    for (const r of upserts) savedRows.set(rowKey(r), encodeRow(r));
    for (const id of deletes) savedRows.delete(String(id));
  } catch (e) {
    console.error('Transkript kaydedilemedi:', e);
    // Try again later; changes stay pending until a save succeeds.
    rowTimer = setTimeout(flushTranscript, 3000);
  }
}

/**
 * Loads the transcript from the database (main window only) and starts
 * saving changes to it.
 */
export async function loadTranscript() {
  let rows = [];
  try {
    rows = (await TranscriptService.Load()) ?? [];
  } catch (e) {
    console.error('Transkript yüklenemedi:', e);
  }
  const legacy = read(LEGACY_TRANSCRIPT_KEY, null);
  if (!rows.length && Array.isArray(legacy) && legacy.length) rows = legacy; // saved by the first flush below
  else savedRows = new Map(rows.map((r) => [rowKey(r), encodeRow(r)]));

  // An AI reply cut off by closing the app can't resume.
  for (const row of rows) {
    if (row.ai && (row.ai.status === 'pending' || row.ai.status === 'streaming')) {
      row.ai.status = row.ai.text ? 'done' : 'error';
      row.ai.error ||= row.ai.text ? '' : 'Uygulama kapandığı için yanıt yarıda kaldı';
    }
  }
  transcript.push(...rows);

  if (legacy) {
    await flushTranscript();
    if (!rowTimer) try { localStorage.removeItem(LEGACY_TRANSCRIPT_KEY); } catch { /* ignore */ }
  }

  $effect.root(() => {
    $effect(() => {
      $state.snapshot(transcript); // track every row
      clearTimeout(rowTimer);
      // AI replies stream in many small pieces; batch them.
      rowTimer = setTimeout(flushTranscript, 500);
    });
  });
  window.addEventListener('beforeunload', () => { if (rowTimer) flushTranscript(); });
}

$effect.root(() => {
  // Theme: follow the setting, or the OS when set to "system" — in every window.
  $effect(() => {
    // index.html reads this before the app loads, so the window opens in the right theme.
    try { localStorage.setItem('stt-theme', settings.theme); } catch { /* ignore */ }
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const apply = () => document.documentElement.classList.toggle(
      'dark', settings.theme === 'dark' || (settings.theme === 'system' && mq.matches));
    apply();
    mq.addEventListener('change', apply);
    return () => mq.removeEventListener('change', apply);
  });
});

// Whisper uses a few non-ISO codes; map them for Intl.DisplayNames.
const isoCode = {jw: 'jv'};
let trNames;
try {
  trNames = new Intl.DisplayNames(['tr'], {type: 'language'});
} catch {
  trNames = null;
}

const capitalize = (s, locale) => (s ? s.charAt(0).toLocaleUpperCase(locale) + s.slice(1) : s);

/** Turkish display name for a Whisper language code, e.g. "de" → "Almanca". */
export function langName(code, fallback = code) {
  const name = trNames?.of(isoCode[code] ?? code);
  return capitalize(name && name !== (isoCode[code] ?? code) ? name : fallback, 'tr');
}

/** The language's name in itself, e.g. "de" → "Deutsch". */
export function nativeName(code) {
  try {
    const iso = isoCode[code] ?? code;
    const name = new Intl.DisplayNames([iso], {type: 'language'}).of(iso);
    return name && name !== iso ? capitalize(name, iso) : '';
  } catch {
    return '';
  }
}

export function formatTime(ms) {
  return new Date(ms).toLocaleTimeString('tr-TR', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export function formatDuration(sec) {
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
}

export function formatBytes(bytes) {
  if (bytes >= 1 << 30) return (bytes / (1 << 30)).toFixed(2) + ' GB';
  return Math.round(bytes / (1 << 20)) + ' MB';
}
