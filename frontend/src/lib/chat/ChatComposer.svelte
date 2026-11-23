<script>
  // The chat box: a growing text field with attachments (button, drag & drop
  // anywhere on the window, or paste), shown as previewable tiles.
  import {onMount, tick} from 'svelte';
  import ArrowUp from '@lucide/svelte/icons/arrow-up';
  import Paperclip from '@lucide/svelte/icons/paperclip';
  import Upload from '@lucide/svelte/icons/upload';
  import AttachmentTile from './AttachmentTile.svelte';
  import {uploadFile, deleteFile, openPreview, MAX_SIZE, formatSize} from './attachments.svelte.js';
  import {notify, errMsg} from '../ui/toast.svelte.js';

  /**
   * @type {{
   *   aiOn: boolean,
   *   aiModel?: string,
   *   onsend: (msg: {text: string, attachments: {id: string, name: string, mime: string, size: number}[]}) => void,
   * }}
   */
  let {aiOn, aiModel = '', onsend} = $props();

  const MAX_FILES = 20;

  let draft = $state('');
  /** @type {{key: number, id?: string, name: string, mime: string, size: number, url?: string, uploading: boolean, error?: string}[]} */
  let items = $state([]);
  let textEl = $state();
  let fileInput = $state();
  let dragging = $state(false);
  let focused = $state(false);
  let nextKey = 0;

  const uploading = $derived(items.some((i) => i.uploading));
  const ready = $derived(items.filter((i) => i.id && !i.error));
  const canSend = $derived(!uploading && (draft.trim() !== '' || ready.length > 0));

  export function focus() {
    textEl?.focus();
  }

  function fit() {
    if (!textEl) return;
    textEl.style.height = 'auto';
    textEl.style.height = Math.min(textEl.scrollHeight, 240) + 'px';
  }

  /** @param {FileList | File[]} files */
  function addFiles(files) {
    const list = [...files];
    if (!list.length) return;
    const room = MAX_FILES - items.length;
    if (list.length > room) notify(`En fazla ${MAX_FILES} dosya eklenebilir`, 'info');
    for (const file of list.slice(0, Math.max(room, 0))) {
      const visual = /^(image|video|audio)\//.test(file.type) || file.type === 'application/pdf';
      const item = {
        key: nextKey++, name: file.name || 'yapistirilan', mime: file.type, size: file.size,
        url: visual ? URL.createObjectURL(file) : undefined, uploading: true,
      };
      items.push(item);
      const live = items[items.length - 1]; // the reactive proxy
      if (file.size > MAX_SIZE) {
        live.uploading = false;
        live.error = `Çok büyük (en fazla ${formatSize(MAX_SIZE)})`;
        continue;
      }
      uploadFile(file).then(
        (a) => {
          if (!items.includes(live)) return deleteFile(a); // removed while uploading
          Object.assign(live, {id: a.id, mime: a.mime, size: a.size, uploading: false});
        },
        (e) => Object.assign(live, {uploading: false, error: errMsg(e) || 'Yüklenemedi'}),
      );
    }
    focus();
  }

  function remove(item) {
    items.splice(items.indexOf(item), 1);
    if (item.url) URL.revokeObjectURL(item.url);
    if (item.id) deleteFile(item);
  }

  function preview(item) {
    const shown = items.filter((i) => !i.error && !i.uploading);
    const i = shown.indexOf(item);
    if (i >= 0) openPreview(shown, i);
  }

  async function send() {
    if (!canSend) return;
    const failed = items.filter((i) => i.error);
    onsend({text: draft.trim(), attachments: ready.map(({id, name, mime, size}) => ({id, name, mime, size}))});
    for (const i of items) if (i.url) URL.revokeObjectURL(i.url);
    items = [];
    draft = '';
    if (failed.length) notify(`${failed.length} dosya yüklenemediği için gönderilmedi`, 'info');
    await tick();
    fit();
  }

  function onKey(e) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); send(); }
    else if (e.key === 'Escape') textEl?.blur();
  }

  function onPaste(e) {
    const files = [...(e.clipboardData?.files ?? [])];
    if (files.length) { e.preventDefault(); addFiles(files); }
  }

  // Drag & drop anywhere on the window.
  onMount(() => {
    let depth = 0;
    const hasFiles = (e) => [...(e.dataTransfer?.types ?? [])].includes('Files');
    const enter = (e) => { if (!hasFiles(e)) return; e.preventDefault(); depth++; dragging = true; };
    const over = (e) => { if (!hasFiles(e)) return; e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; };
    const leave = (e) => { if (!hasFiles(e)) return; depth = Math.max(0, depth - 1); if (!depth) dragging = false; };
    const drop = (e) => {
      if (!hasFiles(e)) return;
      e.preventDefault(); // don't let the webview navigate to the file
      depth = 0;
      dragging = false;
      addFiles(e.dataTransfer.files);
    };
    window.addEventListener('dragenter', enter);
    window.addEventListener('dragover', over);
    window.addEventListener('dragleave', leave);
    window.addEventListener('drop', drop);
    return () => {
      window.removeEventListener('dragenter', enter);
      window.removeEventListener('dragover', over);
      window.removeEventListener('dragleave', leave);
      window.removeEventListener('drop', drop);
      for (const i of items) if (i.url) URL.revokeObjectURL(i.url);
    };
  });
</script>

<form class="mx-auto w-full max-w-3xl" onsubmit={(e) => { e.preventDefault(); send(); }}>
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="cursor-text rounded-2xl border bg-white shadow-sm transition dark:bg-zinc-900
              {focused ? 'border-zinc-300 shadow-md dark:border-zinc-600' : 'border-zinc-200 dark:border-zinc-700/80'}"
       onclick={(e) => { if (e.target === e.currentTarget) focus(); }}>
    {#if items.length}
      <div class="flex gap-2.5 overflow-x-auto px-3 pt-3 pb-1" aria-label="Ekler">
        {#each items as item (item.key)}
          <AttachmentTile {item} onopen={() => preview(item)} onremove={() => remove(item)}/>
        {/each}
      </div>
    {/if}

    <textarea bind:this={textEl} bind:value={draft} oninput={fit} onkeydown={onKey} onpaste={onPaste}
              onfocus={() => (focused = true)} onblur={() => (focused = false)} rows="1"
              class="block max-h-60 min-h-12 w-full resize-none border-0 bg-transparent px-4 shadow-none focus:border-0 focus:ring-0 pt-3 pb-1 text-[15px] leading-6 text-zinc-900 outline-none placeholder:text-zinc-400 dark:text-zinc-100 dark:placeholder:text-zinc-500"
              placeholder={aiOn ? 'Yapay zekâya yazın…' : 'Transkripte yazılı not ekleyin…'} aria-label="Mesaj yaz"></textarea>

    <div class="flex items-center gap-1 px-2 pb-2">
      <input bind:this={fileInput} type="file" multiple class="hidden"
             onchange={(e) => { addFiles(e.currentTarget.files ?? []); e.currentTarget.value = ''; }}/>
      <button type="button" class="grid size-8 place-items-center rounded-lg text-zinc-500 transition hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100"
              onclick={() => fileInput?.click()} title="Dosya ekle — resim, belge, video, ses (sürükleyip bırakabilir veya yapıştırabilirsiniz)"
              aria-label="Dosya ekle"><Paperclip class="size-[18px]"/></button>
      <span class="ml-1 hidden truncate text-xs text-zinc-400 sm:inline dark:text-zinc-500">
        {#if uploading}Yükleniyor…{:else}Enter gönderir · Shift+Enter yeni satır{/if}
      </span>
      <span class="ml-auto truncate pr-1 text-xs text-zinc-400 dark:text-zinc-500">{aiOn ? aiModel : 'Yapay zekâ kapalı'}</span>
      <button type="submit" disabled={!canSend}
              class="grid size-8 shrink-0 place-items-center rounded-lg bg-accent-600 text-white transition hover:bg-accent-700 disabled:cursor-not-allowed disabled:bg-zinc-200 disabled:text-zinc-400 dark:disabled:bg-zinc-800 dark:disabled:text-zinc-600"
              title={uploading ? 'Dosyalar yükleniyor…' : 'Gönder (Enter)'} aria-label="Gönder"><ArrowUp class="size-[18px]"/></button>
    </div>
  </div>
</form>

{#if dragging}
  <div class="pointer-events-none fixed inset-0 z-40 grid place-items-center bg-accent-600/10 p-6 backdrop-blur-[2px]">
    <div class="flex flex-col items-center gap-3 rounded-2xl border-2 border-dashed border-accent-500 bg-white/90 px-10 py-8 text-center shadow-xl dark:bg-zinc-900/90">
      <Upload class="size-10 text-accent-600 dark:text-accent-400"/>
      <p class="text-lg font-semibold">Dosyaları buraya bırakın</p>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">Resim, belge, video, ses · dosya başına en fazla {formatSize(MAX_SIZE)}</p>
    </div>
  </div>
{/if}
