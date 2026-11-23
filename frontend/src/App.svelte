<script>
  import {onMount, tick, untrack} from 'svelte';
  import {Browser, Events} from '@wailsio/runtime';
  import {STTService, ModelService, AIService, WindowService} from '../bindings/changeme/backend';
  import AArrowDown from '@lucide/svelte/icons/a-arrow-down';
  import AArrowUp from '@lucide/svelte/icons/a-arrow-up';
  import ChevronDown from '@lucide/svelte/icons/chevron-down';
  import ChevronUp from '@lucide/svelte/icons/chevron-up';
  import CircleStop from '@lucide/svelte/icons/circle-stop';
  import Clock from '@lucide/svelte/icons/clock';
  import Copy from '@lucide/svelte/icons/copy';
  import Ear from '@lucide/svelte/icons/ear';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Keyboard from '@lucide/svelte/icons/keyboard';
  import Mic from '@lucide/svelte/icons/mic';
  import Monitor from '@lucide/svelte/icons/monitor';
  import Moon from '@lucide/svelte/icons/moon';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import Save from '@lucide/svelte/icons/save';
  import Search from '@lucide/svelte/icons/search';
  import SearchX from '@lucide/svelte/icons/search-x';
  import Settings from '@lucide/svelte/icons/settings';
  import Sparkles from '@lucide/svelte/icons/sparkles';
  import Square from '@lucide/svelte/icons/square';
  import Sun from '@lucide/svelte/icons/sun';
  import Trash from '@lucide/svelte/icons/trash';
  import X from '@lucide/svelte/icons/x';
  import Toasts from './lib/ui/Toasts.svelte';
  import {notify, errMsg} from './lib/ui/toast.svelte.js';
  import {settings, transcript, formatTime, formatDuration} from './lib/settings.svelte.js';
  import {renderMarkdown} from './lib/markdown.js';
  import ChatComposer from './lib/chat/ChatComposer.svelte';
  import AttachmentTile from './lib/chat/AttachmentTile.svelte';
  import PreviewModal from './lib/chat/PreviewModal.svelte';
  import ToolCalls from './lib/chat/ToolCalls.svelte';
  import {preview, openPreview} from './lib/chat/attachments.svelte.js';
  import {fmtTps, statsTitle} from './lib/speed.js';
  import {fold, matches, findRanges, paintHighlights, clearHighlights} from './lib/search.js';

  // ---- state ------------------------------------------------------------
  let models = $state([]);
  let listening = $state(false);
  let starting = $state(false);
  let partial = $state('');
  let level = $state(0);
  let awake = $state(false); // wake-word mode: true after a wake phrase
  let heard = $state(''); // standby: last thing heard that wasn't a wake phrase
  let speaking = $state(false); // VAD currently hears speech
  let startedAt = $state(0);
  let now = $state(Date.now());
  let ai = $state(null); // AI config view from the backend
  let llama = $state({state: 'stopped', message: ''}); // local model server (llama.cpp)
  let scrollEl;

  // Search
  let searchOpen = $state(false);
  let query = $state('');
  let matchCount = $state(0);
  let current = $state(0); // index of the focused match
  let searchInput = $state();
  let listEl = $state();
  let ranges = [];

  const themes = [['system', 'Sistem teması', Monitor], ['light', 'Açık tema', Sun], ['dark', 'Koyu tema', Moon]];
  const nextTheme = () => {
    const i = themes.findIndex(([id]) => id === settings.theme);
    settings.theme = themes[(i + 1) % themes.length][0];
  };

  // ---- derived ----------------------------------------------------------
  const installed = $derived(models.filter((m) => m.installed && m.engine !== 'vad'));
  const model = $derived(models.find((m) => m.id === settings.modelId));
  const standby = $derived(listening && settings.wakeWord && !awake);
  // An AI reply is on its way.
  const aiBusy = $derived(transcript.some((r) => r.ai?.status === 'pending' || r.ai?.status === 'streaming'));
  const holding = $derived(listening && settings.wakeWord && settings.wakeHoldForAI && aiBusy);
  const wakeHint = $derived(settings.commands.wake[0] ? `“${settings.commands.wake[0]}”` : 'başlatma komutu');
  const elapsed = $derived(listening && startedAt ? (now - startedAt) / 1000 : 0);
  const fullText = $derived(transcript.map((s) => s.text).join(' '));
  const wordCount = $derived(fullText.trim() ? fullText.trim().split(/\s+/).length : 0);
  const aiOn = $derived(!!ai?.config.enabled);
  // Chat messages are a little smaller than plain transcript lines.
  const chatSize = $derived(Math.max(14, Math.round(settings.fontSize * 0.8)));
  const foldedQuery = $derived(searchOpen ? fold(query.trim()) : '');
  // Rows whose text or AI reply contains the query (all rows when not searching).
  const visible = $derived(foldedQuery
    ? transcript.filter((s) => matches(s.text, foldedQuery) || matches(s.ai?.text, foldedQuery))
    : transcript);
  // A local model is a file path: show just its name.
  const aiModel = $derived((ai ? ai.config.models?.[ai.config.provider] || '' : '').replace(/^.*\//, '').replace(/\.gguf$/i, ''));

  // ---- helpers ----------------------------------------------------------
  const openSettings = (page = '') => WindowService.OpenSettings(page);

  async function refreshModels() {
    models = await ModelService.List();
    if (!models.some((m) => m.id === settings.modelId && m.installed && m.engine !== 'vad')) {
      // Prefer the most accurate installed model as a default.
      const best = models.filter((m) => m.installed && m.engine !== 'vad')
        .sort((a, b) => b.accuracy - a.accuracy || b.speed - a.speed)[0];
      settings.modelId = best?.id ?? '';
    }
  }

  async function scrollDown() {
    if (!settings.autoScroll || foldedQuery) return;
    await tick();
    scrollEl?.scrollTo({top: scrollEl.scrollHeight, behavior: 'smooth'});
  }

  const rowById = (id) => transcript.find((r) => String(r.id) === id);

  // ---- lifecycle --------------------------------------------------------
  onMount(() => {
    // In wake-word mode, start listening as soon as the app opens.
    Promise.all([refreshModels(), STTService.IsListening()])
      .then(([, l]) => { listening = l; if (settings.wakeWord && !l && model?.installed) toggle(); })
      .catch((e) => notify(errMsg(e), 'error'));
    AIService.Config().then((v) => (ai = v));
    // Older settings may have a language that isn't in the list yet.
    if (settings.language !== 'auto' && !settings.languages.includes(settings.language)) settings.languages.push(settings.language);

    const offs = [
      Events.On('stt:partial', (e) => { partial = e.data; scrollDown(); }),
      Events.On('stt:final', (e) => {
        transcript.push({id: e.data.at + Math.random(), text: e.data.text, at: e.data.at});
        const row = transcript[transcript.length - 1];
        if (aiOn && ai.config.auto) ask(row);
        scrollDown();
      }),
      Events.On('stt:level', (e) => (level = e.data)),
      Events.On('stt:heard', (e) => (heard = e.data)),
      Events.On('stt:speech', (e) => (speaking = e.data)),
      Events.On('stt:command', (e) => {
        const {kind, phrase} = e.data;
        const what = {wake: 'yazma başladı', sleep: 'beklemeye geçildi', stop: 'dinleme kapatıldı'}[kind];
        if (what && kind !== 'wake') notify(`“${phrase}” → ${what}`, 'info');
      }),
      Events.On('stt:wake', (e) => {
        awake = e.data;
        heard = '';
        if (settings.wakeSound) chime(e.data);
      }),
      Events.On('stt:started', () => {
        if (!listening) { listening = true; startedAt = Date.now(); }
      }),
      Events.On('stt:stopped', (e) => {
        listening = false;
        awake = false;
        partial = '';
        level = 0;
        speaking = false;
        if (e.data) notify(e.data, 'error');
      }),
      Events.On('model:done', () => refreshModels()),
      Events.On('ai:config', (e) => (ai = e.data)),
      Events.On('llama:status', (e) => (llama = e.data)),
      Events.On('ai:delta', (e) => {
        const row = rowById(e.data.id);
        if (!row?.ai) return;
        row.ai.status = 'streaming';
        row.ai.text += e.data.text;
        scrollDown();
      }),
      // Tool calls show up above the reply as they run and finish.
      Events.On('ai:tool', (e) => {
        const row = rowById(e.data.id);
        if (!row?.ai) return;
        const {call, name, input, output, error, state} = e.data;
        row.ai.tools ??= [];
        const t = row.ai.tools.find((x) => x.call === call);
        if (t) Object.assign(t, {output, error, state});
        else row.ai.tools.push({call, name, input, output, error, state});
        scrollDown();
      }),
      Events.On('ai:done', (e) => {
        const row = rowById(e.data.id);
        if (!row?.ai) return;
        row.ai.model = e.data.model || row.ai.model;
        if (e.data.stats) row.ai.stats = e.data.stats;
        if (e.data.canceled) row.ai.status = 'canceled';
        else if (e.data.error) { row.ai.status = 'error'; row.ai.error = e.data.error; }
        else row.ai.status = 'done';
      }),
    ];

    const timer = setInterval(() => (now = Date.now()), 250);
    return () => { offs.forEach((off) => off()); clearInterval(timer); };
  });

  // Turning wake-word mode on (here or in the settings window) starts listening.
  let prevWake = settings.wakeWord;
  $effect(() => {
    const on = settings.wakeWord;
    untrack(() => {
      if (on && !prevWake && !listening && !starting && model?.installed) toggle();
      prevWake = on;
    });
  });

  // Wake mode: don't fall back to standby while the AI is replying (the
  // countdown starts over when the reply is finished).
  $effect(() => {
    const busy = holding;
    STTService.SetBusy(busy).catch(() => {});
  });

  // ---- listening --------------------------------------------------------
  /**
   * Switches between continuous listening (stays on until stopped) and wake
   * mode (writes only after the wake phrase). Restarts listening if needed.
   */
  async function setMode(wake) {
    if (settings.wakeWord === wake || starting) return;
    if (wake && !settings.commands.wake.length) {
      notify('Uyandırma modu için önce bir başlatma komutu ekleyin', 'info');
      openSettings('commands');
      return;
    }
    const wasListening = listening;
    if (wasListening) {
      starting = true;
      try { await STTService.Stop(); } finally { listening = false; starting = false; }
    }
    settings.wakeWord = wake;
    prevWake = wake; // handled here; keep the auto-start effect from firing too
    if (wasListening || wake) await toggle();
    notify(wake ? `Uyandırma modu: ${wakeHint} deyince yazar` : 'Sürekli mod: siz durdurana kadar dinler', 'info');
  }

  async function toggle() {
    if (starting) return;
    if (listening) {
      starting = true;
      try { await STTService.Stop(); } finally { listening = false; starting = false; }
      return;
    }
    if (!model?.installed) {
      notify('Önce bir tanıma modeli indirin', 'info');
      openSettings('models');
      return;
    }
    starting = true;
    try {
      await STTService.Start({
        modelId: settings.modelId,
        device: settings.device,
        language: settings.language,
        translate: settings.translate,
        threads: settings.threads,
        microphone: settings.microphone,
        silenceMs: settings.silenceMs,
        vad: settings.vad,
        vadModel: settings.vadModel,
        prompt: settings.prompt,
        vadSens: settings.vadSens,
        minSpeechMs: settings.minSpeechMs,
        wakeWord: settings.wakeWord,
        commands: $state.snapshot(settings.commands),
        wakeSec: settings.wakeSec,
      });
      awake = false;
      heard = '';
      listening = true;
      startedAt = Date.now();
    } catch (e) {
      notify(errMsg(e), 'error');
    } finally {
      starting = false;
    }
  }

  // Short two-note chime: rising when woken, falling when going back to standby.
  let audioCtx;
  function chime(up) {
    try {
      audioCtx ??= new AudioContext();
      const t = audioCtx.currentTime;
      (up ? [660, 990] : [990, 660]).forEach((f, i) => {
        const osc = audioCtx.createOscillator();
        const gain = audioCtx.createGain();
        osc.frequency.value = f;
        gain.gain.setValueAtTime(0.0001, t + i * 0.12);
        gain.gain.exponentialRampToValueAtTime(0.15, t + i * 0.12 + 0.02);
        gain.gain.exponentialRampToValueAtTime(0.0001, t + i * 0.12 + 0.18);
        osc.connect(gain).connect(audioCtx.destination);
        osc.start(t + i * 0.12);
        osc.stop(t + i * 0.12 + 0.2);
      });
    } catch {
      /* no audio output: the visual indicator is enough */
    }
  }

  // ---- AI ---------------------------------------------------------------
  async function ask(row) {
    // Earlier finished exchanges give the AI context (the backend trims to the memory setting).
    const history = [];
    for (const r of transcript) {
      if (r === row) break;
      if (r.ai?.status === 'done' && r.ai.text) history.push({role: 'user', text: withFileNames(r)}, {role: 'assistant', text: r.ai.text});
    }
    row.ai = {status: 'pending', text: '', error: '', model: aiModel};
    try {
      await AIService.Ask({id: String(row.id), text: row.text, history, attachments: $state.snapshot(row.attachments ?? [])});
    } catch (e) {
      row.ai.status = 'error';
      row.ai.error = errMsg(e);
    }
  }

  const cancelAsk = (row) => AIService.Cancel(String(row.id));

  // Earlier messages only send their text; name their files so the AI knows they were there.
  const withFileNames = (r) => (r.attachments?.length
    ? `${r.text}\n[Ekler: ${r.attachments.map((a) => a.name).join(', ')}]`.trim()
    : r.text);

  // Links in AI replies open in the system browser.
  function onReplyClick(e) {
    const a = e.target.closest?.('a[href]');
    if (!a) return;
    e.preventDefault();
    if (/^https?:/i.test(a.href)) Browser.OpenURL(a.href);
  }

  // ---- chat box ---------------------------------------------------------
  /** Adds a typed message (with any attached files) and asks the AI about it. */
  async function sendTyped({text, attachments}) {
    if (searchOpen) closeSearch(); // the new message might not match the search
    const at = Date.now();
    const row = {id: at + Math.random(), text, at, source: 'typed'};
    if (attachments.length) row.attachments = attachments;
    transcript.push(row);
    const live = transcript[transcript.length - 1];
    if (aiOn) ask(live);
    else notify('Yapay zekâ kapalı: mesaj yalnızca transkripte eklendi', 'info');
    await tick();
    scrollEl?.scrollTo({top: scrollEl.scrollHeight, behavior: 'smooth'});
  }

  // ---- search -----------------------------------------------------------
  async function openSearch() {
    searchOpen = true;
    await tick();
    searchInput?.focus();
    searchInput?.select();
  }

  function closeSearch() {
    searchOpen = false;
    query = '';
    scrollDown();
  }

  function showMatch(i) {
    if (!ranges.length) return;
    current = (i + ranges.length) % ranges.length;
    paintHighlights(ranges, current);
    const el = ranges[current].startContainer.parentElement;
    el?.scrollIntoView({block: 'center', behavior: 'smooth'});
  }

  // Re-find matches whenever the query or the shown text changes (after the DOM updates).
  $effect(() => {
    const q = foldedQuery;
    visible.forEach((s) => { s.text; s.ai?.text; }); // track row contents
    if (!q || !listEl) {
      ranges = [];
      matchCount = 0;
      clearHighlights();
      return;
    }
    ranges = findRanges(listEl, q);
    matchCount = ranges.length;
    current = untrack(() => Math.min(current, Math.max(0, ranges.length - 1)));
    paintHighlights(ranges, current);
  });
  // A new query starts from the first match.
  $effect(() => {
    foldedQuery;
    untrack(() => { current = 0; if (ranges.length) showMatch(0); });
  });

  function onSearchKey(e) {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeSearch(); }
    else if (e.key === 'Enter') { e.preventDefault(); showMatch(current + (e.shiftKey ? -1 : 1)); }
  }

  // ---- transcript actions ----------------------------------------------
  function exportText() {
    return transcript.map((s) => {
      let line = (settings.showTimestamps ? `[${formatTime(s.at)}] ` : '') + s.text;
      if (s.attachments?.length) line += ` [Ekler: ${s.attachments.map((a) => a.name).join(', ')}]`;
      if (s.ai?.text) line += `\n    ↳ Yapay zekâ: ${s.ai.text.replace(/\n/g, '\n      ')}`;
      return line;
    }).join(settings.showTimestamps || transcript.some((s) => s.ai?.text) ? '\n' : ' ');
  }

  async function copyAll() {
    if (!transcript.length) return;
    (await STTService.CopyText(exportText())) ? notify('Panoya kopyalandı', 'success') : notify('Kopyalanamadı', 'error');
  }

  async function copyText(text, what = 'Satır') {
    if (await STTService.CopyText(text)) notify(`${what} kopyalandı`, 'success');
  }

  async function save() {
    if (!transcript.length) return;
    const d = new Date();
    const p = (n) => String(n).padStart(2, '0');
    const stamp = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}_${p(d.getHours())}${p(d.getMinutes())}`;
    try {
      const path = await STTService.SaveText(exportText() + '\n', `transkript-${stamp}.txt`);
      if (path) notify('Kaydedildi: ' + path, 'success');
    } catch (e) {
      notify('Kaydedilemedi: ' + errMsg(e), 'error');
    }
  }

  let undoBuffer = null;
  function clearAll() {
    if (!transcript.length) return;
    undoBuffer = $state.snapshot(transcript);
    transcript.length = 0;
    partial = '';
    notify('Metin temizlendi — geri almak için Ctrl+Z', 'info');
  }

  function undo() {
    if (undoBuffer && !transcript.length) {
      transcript.push(...undoBuffer);
      undoBuffer = null;
      notify('Geri alındı', 'success');
    }
  }

  function editSegment(s, el) {
    const text = el.innerText.trim();
    // An emptied row goes away, unless it still carries attachments.
    if (!text && !s.attachments?.length) transcript.splice(transcript.indexOf(s), 1);
    else s.text = text;
  }

  function onKey(e) {
    if (preview.items.length) return; // the preview handles its own keys
    const t = e.target;
    const typing = t instanceof HTMLElement && (t.isContentEditable || ['INPUT', 'SELECT', 'TEXTAREA'].includes(t.tagName));
    if (e.code === 'Space' && !typing && !e.repeat) { e.preventDefault(); toggle(); return; }
    if (e.key === 'F3' && searchOpen) { e.preventDefault(); showMatch(current + (e.shiftKey ? -1 : 1)); return; }
    if (e.key === 'Escape' && searchOpen && !typing) { closeSearch(); return; }
    if (!e.ctrlKey) return;
    const k = e.key.toLowerCase();
    if (k === 'f') { e.preventDefault(); openSearch(); return; }
    const pages = {',': 'general', m: 'models', l: 'languages', k: 'commands', i: 'ai'};
    if (k === 's') { e.preventDefault(); save(); }
    else if (k === 'c' && e.shiftKey) { e.preventDefault(); copyAll(); }
    else if (k in pages) { e.preventDefault(); openSettings(pages[k]); }
    else if (k === 'z' && !typing) { e.preventDefault(); undo(); }
  }

  const meterBars = 24;
</script>

<svelte:window onkeydown={onKey}/>

<div class="flex h-screen min-w-0 flex-col overflow-hidden">
  <header class="flex min-w-0 shrink-0 items-center gap-1 border-b border-zinc-200 px-2 py-2 sm:gap-2 sm:px-4 sm:py-2.5 dark:border-zinc-800">
    <div class="mr-1 hidden size-8 shrink-0 place-items-center rounded-lg bg-accent-600 text-white sm:grid"><Mic class="size-4"/></div>
    <div class="min-w-0 flex-1">
      <div class="truncate text-sm font-medium">Transkript</div>
      <div class="truncate text-xs text-zinc-500 tabular-nums" title="{transcript.length} cümle · {wordCount} kelime · {fullText.length} karakter">
        {wordCount} kelime<span class="hidden sm:inline">{` · ${transcript.length} cümle · ${fullText.length} karakter`}</span>
      </div>
    </div>

    <!-- AI status: opens the AI settings page -->
    <button class="flex min-w-0 items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition
                   {aiOn ? 'border-accent-300 bg-accent-50 text-accent-700 hover:bg-accent-100 dark:border-accent-500/30 dark:bg-accent-500/10 dark:text-accent-300'
                         : 'border-zinc-200 text-zinc-500 hover:bg-zinc-100 dark:border-zinc-700 dark:hover:bg-zinc-800'}"
            onclick={() => openSettings('ai')} title="Yapay zekâ ayarları (Ctrl+I)">
      <Sparkles class="size-3.5 shrink-0"/>
      <span class="truncate">{aiOn ? (aiModel || 'Model seçilmedi') : 'Yapay zekâ kapalı'}</span>
    </button>

    {#each themes.filter(([id]) => id === settings.theme) as [id, label, Icn] (id)}
      <button class="btn-ghost px-2" onclick={nextTheme} title="{label} — değiştirmek için tıklayın" aria-label={label}><Icn class="size-4"/></button>
    {/each}
    <button class="btn-ghost hidden px-2 sm:inline-flex {settings.showTimestamps ? 'text-accent-600 dark:text-accent-400' : ''}"
            onclick={() => (settings.showTimestamps = !settings.showTimestamps)} title="Zaman damgaları"><Clock class="size-4"/></button>
    <div class="hidden items-center rounded-lg border border-zinc-200 md:flex dark:border-zinc-700" title="Yazı boyutu">
      <button class="px-2 py-1.5 text-zinc-500 hover:text-zinc-900 dark:hover:text-white"
              onclick={() => (settings.fontSize = Math.max(14, settings.fontSize - 2))} aria-label="Yazıyı küçült"><AArrowDown class="size-4"/></button>
      <button class="px-2 py-1.5 text-zinc-500 hover:text-zinc-900 dark:hover:text-white"
              onclick={() => (settings.fontSize = Math.min(40, settings.fontSize + 2))} aria-label="Yazıyı büyüt"><AArrowUp class="size-4"/></button>
    </div>
    <div class="mx-1 hidden h-6 w-px bg-zinc-200 md:block dark:bg-zinc-800"></div>
    <button class="btn-ghost px-2 {searchOpen ? 'text-accent-600 dark:text-accent-400' : ''}"
            onclick={() => (searchOpen ? closeSearch() : openSearch())} title="Transkriptte ara (Ctrl+F)" aria-label="Ara"
            aria-pressed={searchOpen}><Search class="size-4"/></button>
    <button class="btn-ghost px-2 xl:px-3" onclick={copyAll} disabled={!transcript.length} title="Tümünü kopyala (Ctrl+Shift+C)"><Copy class="size-4"/><span class="hidden xl:inline">Kopyala</span></button>
    <button class="btn-ghost px-2 xl:px-3" onclick={save} disabled={!transcript.length} title="Dosyaya kaydet (Ctrl+S)"><Save class="size-4"/><span class="hidden xl:inline">Kaydet</span></button>
    <button class="btn-danger px-2" onclick={clearAll} disabled={!transcript.length} title="Temizle"><Trash class="size-4"/></button>
    <button class="btn-ghost px-2" onclick={() => openSettings()} title="Ayarlar (Ctrl+,)" aria-label="Ayarlar"><Settings class="size-4"/></button>
  </header>

  {#if searchOpen}
    <div class="flex shrink-0 items-center gap-1 border-b border-zinc-200 bg-zinc-50 px-2 py-1.5 sm:gap-2 sm:px-4 dark:border-zinc-800 dark:bg-zinc-900/60"
         role="search">
      <div class="relative min-w-0 flex-1 sm:max-w-md">
        <Search class="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-zinc-400"/>
        <input bind:this={searchInput} bind:value={query} onkeydown={onSearchKey} type="search"
               class="ui-input w-full py-1.5 pl-8" placeholder="Transkriptte ara…" aria-label="Transkriptte ara"
               spellcheck="false" autocomplete="off"/>
      </div>
      <span class="shrink-0 text-xs text-zinc-500 tabular-nums dark:text-zinc-400" aria-live="polite">
        {#if foldedQuery}
          {#if matchCount}{current + 1}/{matchCount}<span class="hidden sm:inline"> eşleşme · {visible.length} satır</span>
          {:else}Eşleşme yok{/if}
        {/if}
      </span>
      <button class="btn-ghost px-2" onclick={() => showMatch(current - 1)} disabled={!matchCount} title="Önceki (Shift+Enter)" aria-label="Önceki eşleşme"><ChevronUp class="size-4"/></button>
      <button class="btn-ghost px-2" onclick={() => showMatch(current + 1)} disabled={!matchCount} title="Sonraki (Enter)" aria-label="Sonraki eşleşme"><ChevronDown class="size-4"/></button>
      <button class="btn-ghost px-2" onclick={closeSearch} title="Aramayı kapat (Esc)" aria-label="Aramayı kapat"><X class="size-4"/></button>
    </div>
  {/if}

  <section bind:this={scrollEl} class="selectable min-h-0 flex-1 overflow-y-auto px-2 py-4 sm:px-6 sm:py-6 lg:px-10">
    {#if foldedQuery && !visible.length}
      <div class="flex h-full flex-col items-center justify-center text-center text-zinc-500 dark:text-zinc-400">
        <SearchX class="mb-4 size-12" strokeWidth={1.5}/>
        <p class="text-lg font-medium">“{query.trim()}” bulunamadı</p>
        <p class="mt-1 text-sm">Büyük/küçük harf ve Türkçe karakter farkı gözetilmez (ör. “sik” → “şık”).</p>
      </div>
    {:else if !transcript.length && !partial}
      <div class="flex h-full flex-col items-center justify-center text-center text-zinc-500 dark:text-zinc-400">
        {#if standby}
          <Ear class="mb-4 size-12 animate-pulse text-amber-500" strokeWidth={1.5}/>
        {:else}
          <Mic class="mb-4 size-12" strokeWidth={1.5}/>
        {/if}
        <p class="text-lg font-medium">
          {standby ? `${wakeHint} deyin…` : listening ? 'Dinliyorum… konuşmaya başlayın.' : 'Mikrofona basın ve konuşun'}
        </p>
        {#if standby}
          <p class="mt-3 max-w-md text-sm">
            {#if heard}Duyulan: <span class="text-zinc-600 italic dark:text-zinc-300">“{heard}”</span>
            {:else}Henüz bir şey duyulmadı. Ses seviyesi göstergesi hareket ediyor mu?{/if}
          </p>
        {/if}
        {#if !listening}<p class="mt-1 text-sm">veya <span class="kbd">Boşluk</span> tuşuna basın</p>{/if}
        {#if aiOn}
          <p class="mt-4 flex items-center gap-1.5 text-sm text-accent-600 dark:text-accent-400">
            <Sparkles class="size-4"/> Söyledikleriniz {ai.config.auto ? 'otomatik olarak' : 'isteğinizle'} yapay zekâya sorulacak
          </p>
        {/if}
      </div>
    {:else}
      <div bind:this={listEl} class="mx-auto max-w-3xl space-y-1 text-zinc-800 dark:text-zinc-200"
           style="font-size: {settings.fontSize}px; line-height: 1.6">
        {#each visible as s (s.id)}
          {#if s.ai || s.source === 'typed'}
            <!-- Chat exchange: your message on the right, the AI's reply on the left. -->
            <div class="group/row py-4 first:pt-1" style="font-size: {chatSize}px">
              <div class="flex flex-col items-end">
                <div data-search-skip class="mb-1.5 flex items-center gap-1.5 pr-1 text-xs text-zinc-500 dark:text-zinc-400">
                  <span class="flex gap-0.5 opacity-0 transition group-hover/row:opacity-100 focus-within:opacity-100">
                    {#if aiOn && !s.ai}
                      <button class="rounded p-1 text-accent-600 hover:bg-zinc-100 dark:text-accent-400 dark:hover:bg-zinc-800" onclick={() => ask(s)} title="Yapay zekâya sor" aria-label="Yapay zekâya sor"><Sparkles class="size-3.5"/></button>
                    {/if}
                    <button class="rounded p-1 hover:bg-zinc-100 hover:text-zinc-800 dark:hover:bg-zinc-800 dark:hover:text-white" onclick={() => copyText(s.text, 'Mesaj')} title="Mesajı kopyala" aria-label="Mesajı kopyala"><Copy class="size-3.5"/></button>
                    <button class="rounded p-1 hover:bg-zinc-100 hover:text-red-600 dark:hover:bg-zinc-800" onclick={() => transcript.splice(transcript.indexOf(s), 1)} title="Sil" aria-label="Mesajı sil"><Trash class="size-3.5"/></button>
                  </span>
                  {#if s.source === 'typed'}<Keyboard class="size-3.5"/>{:else}<Mic class="size-3.5"/>{/if}
                  <span class="font-medium text-zinc-700 dark:text-zinc-300">Siz</span>
                  <span>· {s.source === 'typed' ? 'yazılı' : 'sesli'}{settings.showTimestamps ? ` · ${formatTime(s.at)}` : ''}</span>
                </div>
                {#if s.text || !s.attachments?.length}
                  <!-- svelte-ignore a11y_no_static_element_interactions -->
                  <p class="max-w-[85%] rounded-2xl rounded-tr-md bg-accent-600 px-4 py-2.5 leading-relaxed whitespace-pre-wrap text-white shadow-sm outline-none selection:bg-white/30 focus:ring-2 focus:ring-accent-300"
                     contenteditable="plaintext-only" spellcheck="false" onblur={(e) => editSegment(s, e.currentTarget)}
                     onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); e.currentTarget.blur(); } }}>{s.text}</p>
                {/if}
                {#if s.attachments?.length}
                  <div data-search-skip class="mt-2 flex max-w-[85%] flex-wrap justify-end gap-2">
                    {#each s.attachments as a, i (a.id)}
                      <AttachmentTile item={a} compact onopen={() => openPreview(s.attachments, i)}/>
                    {/each}
                  </div>
                {/if}
              </div>

              {#if s.ai}
                <div class="mt-4 flex gap-3">
                  <span data-search-skip class="grid size-8 shrink-0 place-items-center rounded-full bg-accent-100 text-accent-700 dark:bg-accent-500/20 dark:text-accent-300">
                    {#if s.ai.status === 'pending' || s.ai.status === 'streaming'}<LoaderCircle class="size-4 animate-spin"/>{:else}<Sparkles class="size-4"/>{/if}
                  </span>
                  <div class="min-w-0 flex-1">
                    <div data-search-skip class="flex min-h-8 items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400">
                      <span class="truncate font-medium text-zinc-700 dark:text-zinc-300">{s.ai.model || 'Yapay zekâ'}</span>
                      {#if s.ai.status === 'pending' && llama.state === 'starting'}<span class="truncate">· yerel model yükleniyor{llama.message && llama.message !== 'Başlatılıyor…' ? `: ${llama.message}` : '…'}</span>
                      {:else if s.ai.status === 'pending'}<span>· düşünüyor…</span>{:else if s.ai.status === 'streaming'}<span>· yazıyor…</span>
                      {:else if s.ai.status === 'done' && s.ai.stats?.outputTokens}
                        <span class="truncate tabular-nums" title={statsTitle(s.ai.stats)}>· {s.ai.stats.outputTokens} token · {fmtTps(s.ai.stats.tokensPerSec)} token/sn</span>
                      {/if}
                      <span class="ml-auto flex shrink-0 gap-0.5">
                        {#if s.ai.status === 'pending' || s.ai.status === 'streaming'}
                          <button class="rounded p-1 hover:bg-zinc-100 hover:text-zinc-800 dark:hover:bg-zinc-800 dark:hover:text-white" onclick={() => cancelAsk(s)} title="Durdur" aria-label="Yanıtı durdur"><CircleStop class="size-3.5"/></button>
                        {:else}
                          {#if s.ai.text}
                            <button class="rounded p-1 hover:bg-zinc-100 hover:text-zinc-800 dark:hover:bg-zinc-800 dark:hover:text-white" onclick={() => copyText(s.ai.text, 'Yanıt')} title="Yanıtı kopyala" aria-label="Yanıtı kopyala"><Copy class="size-3.5"/></button>
                          {/if}
                          {#if aiOn}
                            <button class="rounded p-1 hover:bg-zinc-100 hover:text-zinc-800 dark:hover:bg-zinc-800 dark:hover:text-white" onclick={() => ask(s)} title="Yeniden yanıtla" aria-label="Yeniden yanıtla"><RefreshCw class="size-3.5"/></button>
                          {/if}
                        {/if}
                      </span>
                    </div>
                    {#if s.ai.tools?.length}<ToolCalls calls={s.ai.tools}/>{/if}
                    {#if s.ai.text}
                      <!-- Reply HTML is sanitized by renderMarkdown (DOMPurify). -->
                      <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
                      <div class="prose prose-zinc dark:prose-invert max-w-none min-w-0 text-[1em] leading-relaxed break-words prose-p:my-2 prose-pre:my-2 prose-pre:max-w-full prose-pre:overflow-x-auto prose-ul:my-2 prose-ol:my-2 prose-hr:my-4"
                           onclick={onReplyClick}>{@html renderMarkdown(s.ai.text)}</div>
                    {:else if s.ai.status === 'pending' || s.ai.status === 'streaming'}
                      <div class="flex gap-1 py-2" aria-label="Yanıt bekleniyor">
                        {#each [0, 150, 300] as d (d)}<span class="size-2 animate-bounce rounded-full bg-zinc-400 dark:bg-zinc-500" style="animation-delay: {d}ms"></span>{/each}
                      </div>
                    {/if}
                    {#if s.ai.status === 'error'}
                      <p class="mt-1 rounded-lg bg-red-50 px-3 py-2 text-red-700 dark:bg-red-500/10 dark:text-red-400">{s.ai.error}</p>
                      {#if /anahtar|sunucu adresi|model seçilmemiş/i.test(s.ai.error)}
                        <button class="mt-1 text-xs font-medium text-accent-700 underline dark:text-accent-300" onclick={() => openSettings('ai')}>Yapay zekâ ayarlarını aç</button>
                      {/if}
                    {:else if s.ai.status === 'canceled'}
                      <p class="mt-1 text-xs text-zinc-500">Durduruldu.</p>
                    {/if}
                  </div>
                </div>
              {/if}
            </div>
          {:else}
            <!-- Plain spoken transcript line. -->
            <div class="group relative flex gap-2 rounded-lg px-2 py-1.5 transition hover:bg-zinc-100 sm:gap-4 sm:px-3 dark:hover:bg-zinc-900">
              {#if settings.showTimestamps}
                <span data-search-skip class="w-14 shrink-0 pt-[0.35em] font-mono text-[11px] text-zinc-500 tabular-nums sm:w-16 sm:text-xs dark:text-zinc-400">{formatTime(s.at)}</span>
              {/if}
              <div class="min-w-0 flex-1">
                <!-- svelte-ignore a11y_no_static_element_interactions -->
                <p class="rounded outline-none focus:ring-2 focus:ring-accent-500/40" contenteditable="plaintext-only"
                   spellcheck="false" onblur={(e) => editSegment(s, e.currentTarget)}
                   onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); e.currentTarget.blur(); } }}>{s.text}</p>
                {#if s.attachments?.length}
                  <div data-search-skip class="mt-2 flex flex-wrap gap-2">
                    {#each s.attachments as a, i (a.id)}
                      <AttachmentTile item={a} compact onopen={() => openPreview(s.attachments, i)}/>
                    {/each}
                  </div>
                {/if}
              </div>
              <div class="absolute top-1 right-1 hidden gap-0.5 rounded-md bg-white shadow-sm group-focus-within:flex group-hover:flex dark:bg-zinc-800">
                {#if aiOn}
                  <button class="rounded p-1.5 text-accent-600 hover:text-accent-800 dark:text-accent-400 dark:hover:text-accent-200" onclick={() => ask(s)} title="Yapay zekâya sor" aria-label="Yapay zekâya sor"><Sparkles class="size-3.5"/></button>
                {/if}
                <button class="rounded p-1.5 text-zinc-500 hover:text-zinc-800 dark:text-zinc-400 dark:hover:text-white" onclick={() => copyText(s.text)} title="Kopyala" aria-label="Satırı kopyala"><Copy class="size-3.5"/></button>
                <button class="rounded p-1.5 text-zinc-500 hover:text-red-600 dark:text-zinc-400" onclick={() => transcript.splice(transcript.indexOf(s), 1)} title="Sil" aria-label="Satırı sil"><Trash class="size-3.5"/></button>
              </div>
            </div>
          {/if}
        {/each}
        {#if partial}
          <div data-search-skip class="flex gap-2 px-2 py-1.5 sm:gap-4 sm:px-3">
            {#if settings.showTimestamps}
              <span class="flex w-14 shrink-0 items-center gap-1.5 pt-[0.35em] font-mono text-[11px] text-accent-500 sm:w-16 sm:text-xs">
                <span class="size-1.5 animate-pulse rounded-full bg-accent-500"></span>canlı
              </span>
            {/if}
            <p class="flex-1 text-zinc-500 italic dark:text-zinc-500">{partial}</p>
          </div>
        {/if}
        {#if standby}
          <div class="mt-4 flex items-center gap-2 rounded-lg border border-dashed border-amber-400/50 px-3 py-2 text-sm text-zinc-500">
            <Ear class="size-4 shrink-0 animate-pulse text-amber-500"/>
            <span class="truncate">{wakeHint} bekleniyor{#if heard} · Duyulan: <i>“{heard}”</i>{/if}</span>
          </div>
        {/if}
      </div>
    {/if}
  </section>

  <!-- Chat box: type a message (and attach files) instead of saying it -->
  <div class="shrink-0 px-3 pt-3 sm:px-6">
    <ChatComposer {aiOn} {aiModel} onsend={sendTyped}/>
  </div>

  <!-- Control bar -->
  <footer class="shrink-0 bg-white/80 px-3 py-3 backdrop-blur sm:px-6 sm:py-4 dark:border-zinc-800 dark:bg-zinc-900/60">
    <div class="mx-auto grid max-w-3xl grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-3 sm:gap-6">
      <div class="flex min-w-0 items-center gap-3">
        <div class="flex shrink-0 rounded-lg bg-zinc-100 p-0.5 dark:bg-zinc-800" role="radiogroup" aria-label="Dinleme modu">
          {#each [[false, 'Sürekli', Mic, 'Sürekli: butona basınca açılır, siz durdurana kadar yazar'], [true, 'Uyandırma', Ear, `Uyandırma: ${wakeHint} deyince yazar, sessizlikte beklemeye geçer`]] as [wake, label, Icn, hint] (label)}
            <button class="flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition disabled:opacity-50
                           {settings.wakeWord === wake ? 'bg-white text-zinc-900 shadow-sm dark:bg-zinc-950 dark:text-white'
                                                       : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100'}"
                    role="radio" aria-checked={settings.wakeWord === wake} title={hint} disabled={starting}
                    onclick={() => setMode(wake)}>
              <Icn class="size-3.5"/><span class="hidden sm:inline">{label}</span>
            </button>
          {/each}
        </div>
        <span class="hidden shrink-0 items-center gap-1.5 rounded-full px-2 py-0.5 lg:flex text-[11px] font-medium transition
                     {listening && speaking ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300'
                                            : 'bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-500'}"
              title="Konuşma algılandığında yanar">
          <span class="size-1.5 rounded-full {listening && speaking ? 'bg-emerald-500' : 'bg-zinc-300 dark:bg-zinc-600'}"></span>Konuşma
        </span>
        <div class="hidden h-8 min-w-0 items-end gap-[3px] overflow-hidden md:flex" title="Giriş seviyesi">
          {#each Array(meterBars) as _, i (i)}
            {@const on = listening && level * meterBars > i}
            <span class="w-1.5 shrink-0 rounded-full transition-all duration-75
                         {on ? (i > meterBars * 0.8 ? 'bg-red-500' : i > meterBars * 0.55 ? 'bg-amber-400' : 'bg-emerald-500') : 'bg-zinc-200 dark:bg-zinc-800'}"
                  style="height: {30 + (i / meterBars) * 70}%"></span>
          {/each}
        </div>
      </div>

      <button class="relative grid size-14 place-items-center rounded-full text-white shadow-lg transition active:scale-95 disabled:cursor-not-allowed disabled:opacity-50 sm:size-16
                     {standby ? 'bg-amber-500 hover:bg-amber-600' : listening ? 'bg-red-500 hover:bg-red-600' : 'bg-accent-600 hover:bg-accent-700'}"
              onclick={toggle} disabled={starting} aria-label={listening ? 'Dinlemeyi durdur' : 'Dinlemeyi başlat'}>
        {#if listening && !standby}<span class="absolute inset-0 animate-ping rounded-full bg-red-500/40"></span>{/if}
        {#if starting}
          <LoaderCircle class="size-7 animate-spin"/>
        {:else if standby}
          <Ear class="size-7"/>
        {:else if listening}
          <Square class="relative size-6"/>
        {:else}
          <Mic class="size-7"/>
        {/if}
      </button>

      <div class="min-w-0 text-right">
        <div class="font-mono text-base tabular-nums sm:text-lg {listening ? 'text-zinc-900 dark:text-white' : 'text-zinc-500 dark:text-zinc-400'}">{formatDuration(elapsed)}</div>
        <div class="truncate text-xs text-zinc-500">
          {#if starting}
            {listening ? 'Durduruluyor…' : 'Model yükleniyor…'}
          {:else if standby}
            <span class="text-amber-500">●</span> {wakeHint} bekleniyor
          {:else if holding}
            <span class="text-accent-500">●</span> Yapay zekâ yanıtlıyor · beklemeye geçmez
          {:else if listening}
            <span class="text-red-500">●</span> {settings.wakeWord ? 'Yazıyor · ' : ''}{model?.name} · {settings.device.toUpperCase()}
          {:else if model}
            Hazır · {model.name}
          {:else if installed.length === 0}
            <button class="underline" onclick={() => openSettings('models')}>Model indirin</button>
          {:else}
            Model seçilmedi
          {/if}
        </div>
      </div>
    </div>
  </footer>
</div>

<PreviewModal/>
<Toasts bottom="bottom-48"/>
