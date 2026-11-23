<script>
  // Full-window preview of an attachment: image, video, audio, PDF, text;
  // anything else can be opened in the system's default app.
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import ExternalLink from '@lucide/svelte/icons/external-link';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import X from '@lucide/svelte/icons/x';
  import {preview, closePreview, kindOf, kindIcon, extBadge, formatSize, fileURL, openExternally} from './attachments.svelte.js';
  import {notify, errMsg} from '../ui/toast.svelte.js';

  const MAX_TEXT = 1024 * 1024;

  const item = $derived(preview.items[preview.index]);
  const kind = $derived(item ? kindOf(item) : 'file');
  const src = $derived(item ? (item.url ?? fileURL(item)) : '');
  const Icon = $derived(kindIcon[kind]);
  const many = $derived(preview.items.length > 1);
  let closeBtn = $state();

  // Text files are fetched and shown as-is.
  let text = $state({loading: false, value: '', error: '', cut: false});
  $effect(() => {
    if (!item || (kind !== 'text' && kind !== 'code')) return;
    const url = src;
    let alive = true;
    text = {loading: true, value: '', error: '', cut: false};
    fetch(url)
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then((t) => { if (alive) text = {loading: false, value: t.slice(0, MAX_TEXT), error: '', cut: t.length > MAX_TEXT}; })
      .catch((e) => { if (alive) text = {loading: false, value: '', error: errMsg(e), cut: false}; });
    return () => { alive = false; };
  });

  $effect(() => { if (item) closeBtn?.focus(); });

  const go = (d) => { preview.index = (preview.index + d + preview.items.length) % preview.items.length; };

  async function openExt() {
    try {
      await openExternally(item);
    } catch (e) {
      notify('Açılamadı: ' + errMsg(e), 'error');
    }
  }

  function onKey(e) {
    if (!item) return;
    if (e.key === 'Escape') { e.preventDefault(); closePreview(); }
    else if (e.key === 'ArrowLeft' && many) { e.preventDefault(); go(-1); }
    else if (e.key === 'ArrowRight' && many) { e.preventDefault(); go(1); }
  }
</script>

<svelte:window onkeydown={onKey}/>

{#if item}
  <div class="fixed inset-0 z-50 flex flex-col bg-zinc-950/90 text-zinc-100 backdrop-blur-sm" role="dialog" aria-modal="true" aria-label="{item.name} önizleme">
    <header class="flex shrink-0 items-center gap-3 border-b border-white/10 px-3 py-2.5 sm:px-5">
      <span class="grid size-9 shrink-0 place-items-center rounded-lg bg-white/10"><Icon class="size-5"/></span>
      <div class="min-w-0 flex-1">
        <div class="truncate text-sm font-medium">{item.name}</div>
        <div class="truncate text-xs text-zinc-400">{extBadge(item)} · {formatSize(item.size)}{many ? ` · ${preview.index + 1}/${preview.items.length}` : ''}</div>
      </div>
      {#if item.id}
        <button class="flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-sm text-zinc-300 transition hover:bg-white/10 hover:text-white"
                onclick={openExt} title="Sistemdeki varsayılan uygulamayla aç">
          <ExternalLink class="size-4"/><span class="hidden sm:inline">Uygulamada aç</span>
        </button>
      {/if}
      <button bind:this={closeBtn} class="grid size-9 place-items-center rounded-lg text-zinc-300 transition hover:bg-white/10 hover:text-white"
              onclick={closePreview} title="Kapat (Esc)" aria-label="Kapat"><X class="size-5"/></button>
    </header>

    <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
    <div class="relative flex min-h-0 flex-1 items-center justify-center p-3 sm:p-6"
         onclick={(e) => { if (e.target === e.currentTarget) closePreview(); }}>
      {#key src}
        {#if kind === 'image'}
          <img {src} alt={item.name} class="max-h-full max-w-full rounded-lg object-contain shadow-2xl" draggable="false"/>
        {:else if kind === 'video'}
          <!-- svelte-ignore a11y_media_has_caption -->
          <video {src} controls autoplay class="max-h-full max-w-full rounded-lg bg-black shadow-2xl"></video>
        {:else if kind === 'audio'}
          <div class="flex w-full max-w-md flex-col items-center gap-5 rounded-2xl bg-white/5 p-8">
            <span class="grid size-20 place-items-center rounded-2xl bg-accent-500/20 text-accent-300"><Icon class="size-10"/></span>
            <p class="max-w-full truncate text-center font-medium">{item.name}</p>
            <!-- svelte-ignore a11y_media_has_caption -->
            <audio {src} controls autoplay class="w-full"></audio>
          </div>
        {:else if kind === 'pdf'}
          <iframe {src} title={item.name} class="size-full rounded-lg bg-white shadow-2xl"></iframe>
        {:else if kind === 'text' || kind === 'code'}
          <div class="flex size-full max-w-5xl flex-col overflow-hidden rounded-lg bg-white text-zinc-900 shadow-2xl dark:bg-zinc-900 dark:text-zinc-100">
            {#if text.loading}
              <div class="grid flex-1 place-items-center"><LoaderCircle class="size-6 animate-spin text-zinc-400"/></div>
            {:else if text.error}
              <div class="grid flex-1 place-items-center text-sm text-red-500">Dosya okunamadı: {text.error}</div>
            {:else}
              <pre class="selectable min-h-0 flex-1 overflow-auto p-4 font-mono text-[13px] leading-relaxed whitespace-pre-wrap break-words">{text.value}</pre>
              {#if text.cut}<div class="border-t border-zinc-200 px-4 py-2 text-xs text-zinc-500 dark:border-zinc-800">İlk 1 MB gösteriliyor.</div>{/if}
            {/if}
          </div>
        {:else}
          <div class="flex flex-col items-center gap-4 text-center">
            <span class="grid size-24 place-items-center rounded-3xl bg-white/10"><Icon class="size-12"/></span>
            <p class="font-medium">{item.name}</p>
            <p class="text-sm text-zinc-400">Bu dosya türü için uygulama içinde önizleme yok.</p>
            {#if item.id}
              <button class="btn-primary" onclick={openExt}><ExternalLink class="size-4"/> Uygulamada aç</button>
            {/if}
          </div>
        {/if}
      {/key}

      {#if many}
        <button class="absolute top-1/2 left-2 grid size-10 -translate-y-1/2 place-items-center rounded-full bg-black/50 text-white transition hover:bg-black/70 sm:left-4"
                onclick={() => go(-1)} title="Önceki (←)" aria-label="Önceki"><ChevronLeft class="size-5"/></button>
        <button class="absolute top-1/2 right-2 grid size-10 -translate-y-1/2 place-items-center rounded-full bg-black/50 text-white transition hover:bg-black/70 sm:right-4"
                onclick={() => go(1)} title="Sonraki (→)" aria-label="Sonraki"><ChevronRight class="size-5"/></button>
      {/if}
    </div>
  </div>
{/if}
