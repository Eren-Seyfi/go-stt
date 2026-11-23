<script>
  // One attachment: a thumbnail for images/videos, a file card otherwise.
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Play from '@lucide/svelte/icons/play';
  import X from '@lucide/svelte/icons/x';
  import {kindOf, kindIcon, extBadge, formatSize, fileURL} from './attachments.svelte.js';

  /**
   * @type {{
   *   item: {id?: string, name: string, mime: string, size: number, url?: string, uploading?: boolean, error?: string},
   *   compact?: boolean,
   *   onopen?: () => void,
   *   onremove?: () => void,
   * }}
   */
  let {item, compact = false, onopen, onremove} = $props();

  const kind = $derived(kindOf(item));
  const src = $derived(item.url ?? (item.id ? fileURL(item) : ''));
  const visual = $derived((kind === 'image' || kind === 'video') && src && !item.error);
  const Icon = $derived(kindIcon[kind]);
  let broken = $state(false);
</script>

<div class="group relative shrink-0" title="{item.name} · {formatSize(item.size)}">
  <button type="button" onclick={onopen} disabled={item.uploading}
          class="block overflow-hidden rounded-xl border text-left transition focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500
                 {item.error ? 'border-red-300 bg-red-50 dark:border-red-500/40 dark:bg-red-500/10'
                             : 'border-zinc-200 bg-white hover:border-zinc-300 dark:border-zinc-700 dark:bg-zinc-800/60 dark:hover:border-zinc-600'}"
          aria-label="{item.name} önizle">
    {#if visual && !broken}
      <div class="relative {compact ? 'size-20' : 'size-16'} bg-zinc-100 dark:bg-zinc-900">
        {#if kind === 'image'}
          <img {src} alt={item.name} class="size-full object-cover" loading="lazy" draggable="false" onerror={() => (broken = true)}/>
        {:else}
          <!-- First frame as the thumbnail. -->
          <video src="{src}#t=0.1" class="size-full object-cover" preload="metadata" muted playsinline onerror={() => (broken = true)}></video>
          <span class="absolute inset-0 grid place-items-center">
            <span class="grid size-7 place-items-center rounded-full bg-black/55 text-white"><Play class="size-3.5 translate-x-px fill-current"/></span>
          </span>
        {/if}
      </div>
    {:else}
      <div class="flex items-center gap-2.5 p-2 pr-3 {compact ? 'h-14 w-56' : 'h-16 w-52'}">
        <span class="grid size-10 shrink-0 place-items-center rounded-lg
                     {item.error ? 'bg-red-100 text-red-600 dark:bg-red-500/20 dark:text-red-300' : 'bg-accent-50 text-accent-600 dark:bg-accent-500/15 dark:text-accent-300'}">
          {#if item.error}<CircleAlert class="size-5"/>{:else}<Icon class="size-5"/>{/if}
        </span>
        <span class="min-w-0 flex-1">
          <span class="block truncate text-sm leading-5 font-medium text-zinc-800 dark:text-zinc-100">{item.name}</span>
          <span class="block truncate text-xs leading-4 {item.error ? 'text-red-600 dark:text-red-400' : 'text-zinc-500 dark:text-zinc-400'}">
            {item.error ? item.error : `${extBadge(item)} · ${formatSize(item.size)}`}
          </span>
        </span>
      </div>
    {/if}
    {#if item.uploading}
      <span class="absolute inset-0 grid place-items-center rounded-xl bg-white/70 dark:bg-zinc-900/70">
        <LoaderCircle class="size-5 animate-spin text-accent-600"/>
      </span>
    {/if}
  </button>
  {#if onremove}
    <button type="button" onclick={onremove}
            class="absolute -top-1.5 -right-1.5 grid size-5 place-items-center rounded-full border border-zinc-200 bg-white text-zinc-600 opacity-0 shadow-sm transition group-focus-within:opacity-100 group-hover:opacity-100 hover:text-zinc-900 focus:opacity-100 dark:border-zinc-600 dark:bg-zinc-800 dark:text-zinc-300 dark:hover:text-white"
            aria-label="{item.name} kaldır" title="Kaldır"><X class="size-3"/></button>
  {/if}
</div>
