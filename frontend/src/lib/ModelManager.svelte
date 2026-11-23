<script>
  import {ModelService} from '../../bindings/changeme/backend';
  import Check from '@lucide/svelte/icons/check';
  import Download from '@lucide/svelte/icons/download';
  import FolderOpen from '@lucide/svelte/icons/folder-open';
  import Target from '@lucide/svelte/icons/target';
  import Trash from '@lucide/svelte/icons/trash';
  import X from '@lucide/svelte/icons/x';
  import Zap from '@lucide/svelte/icons/zap';
  import {formatBytes} from './settings.svelte.js';

  /** @type {{models: any[], progress: Record<string, {received: number, total: number}>, selectedId: string,
   *          listening: boolean, onselect: (id: string) => void,
   *          onchange: () => void, notify: (msg: string, kind?: string) => void}} */
  let {models, progress, selectedId, listening, onselect, onchange, notify} = $props();

  let filter = $state('all'); // all | whisper | vad | installed
  let confirmDelete = $state('');

  const shown = $derived(models.filter((m) =>
    filter === 'all' ? true : filter === 'installed' ? m.installed : m.engine === filter));
  const installedMB = $derived(models.filter((m) => m.installed).reduce((s, m) => s + m.sizeMB, 0));

  async function download(m) {
    try {
      await ModelService.Download(m.id);
      onchange();
    } catch (e) {
      notify(String(e?.message ?? e), 'error');
    }
  }

  async function remove(m) {
    if (confirmDelete !== m.id) {
      confirmDelete = m.id;
      setTimeout(() => { if (confirmDelete === m.id) confirmDelete = ''; }, 3000);
      return;
    }
    confirmDelete = '';
    try {
      await ModelService.Delete(m.id);
      notify(`${m.name} silindi`);
      onchange();
    } catch (e) {
      notify(String(e?.message ?? e), 'error');
    }
  }

  function pct(id) {
    const p = progress[id];
    return p && p.total > 0 ? Math.min(100, (p.received / p.total) * 100) : 0;
  }
</script>

<section class="card flex flex-col" aria-labelledby="mm-title">
    <header class="flex items-start justify-between gap-4 border-b border-zinc-200 p-4 sm:p-5 dark:border-zinc-800">
      <div>
        <h2 id="mm-title" class="text-lg font-semibold">Modeller</h2>
        <p class="mt-0.5 text-sm text-zinc-500 dark:text-zinc-400">
          Modelleri indirip silebilirsiniz. Diskte: {formatBytes(installedMB * (1 << 20))}
        </p>
      </div>
      <div class="flex items-center gap-1">
        <button class="btn-ghost" onclick={() => ModelService.OpenFolder()} title="Model klasörünü aç">
          <FolderOpen class="size-4"/><span class="hidden sm:inline">Klasörü aç</span>
        </button>
      </div>
    </header>

    <div class="flex gap-1 border-b border-zinc-200 px-3 py-2 sm:px-5 dark:border-zinc-800">
      {#each [['all', 'Tümü'], ['whisper', 'Whisper'], ['vad', 'VAD'], ['installed', 'Yüklü']] as [id, label] (id)}
        <button class="rounded-md px-3 py-1.5 text-sm font-medium transition
                       {filter === id ? 'bg-accent-50 text-accent-700 dark:bg-accent-500/15 dark:text-accent-400'
                                      : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100'}"
                onclick={() => (filter = id)}>{label}</button>
      {/each}
    </div>

    <ul class="min-h-0 flex-1 space-y-2 overflow-y-auto p-3 sm:p-5">
      {#each shown as m (m.id)}
        {@const active = selectedId === m.id}
        <li class="rounded-xl border p-4 transition
                   {active ? 'border-accent-500 bg-accent-50/50 dark:bg-accent-500/5' : 'border-zinc-200 dark:border-zinc-800'}">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-semibold">{m.name}</span>
                <span class="rounded-full px-2 py-0.5 text-[11px] font-medium
                             {m.engine === 'vad' ? 'bg-violet-100 text-violet-700 dark:bg-violet-500/15 dark:text-violet-300'
                                                 : 'bg-sky-100 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300'}">
                  {m.engine === 'vad' ? 'VAD' : 'Whisper'}
                </span>
                {#if m.installed}
                  <span class="inline-flex items-center gap-1 rounded-full bg-emerald-100 px-2 py-0.5 text-[11px] font-medium text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300">
                    <Check class="size-3"/> Yüklü
                  </span>
                {/if}
              </div>
              <p class="mt-1 text-sm text-zinc-600 dark:text-zinc-400">{m.description}</p>
              <div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-zinc-500">
                <span>{formatBytes(m.sizeMB * (1 << 20))}</span>
                <span>{m.languages}</span>
                <span class="flex items-center gap-1" title="Doğruluk">
                  <Target class="size-3.5"/>
                  {#each Array(5) as _, i (i)}<span class="h-1.5 w-3 rounded-full {i < m.accuracy ? 'bg-emerald-500' : 'bg-zinc-200 dark:bg-zinc-700'}"></span>{/each}
                </span>
                <span class="flex items-center gap-1" title="Hız">
                  <Zap class="size-3.5"/>
                  {#each Array(5) as _, i (i)}<span class="h-1.5 w-3 rounded-full {i < m.speed ? 'bg-sky-500' : 'bg-zinc-200 dark:bg-zinc-700'}"></span>{/each}
                </span>
              </div>
            </div>

            <div class="flex shrink-0 items-center gap-1.5">
              {#if m.downloading}
                <button class="btn-outline" onclick={() => ModelService.CancelDownload(m.id)}>
                  <X class="size-4"/> İptal
                </button>
              {:else if m.installed}
                <button class="btn-danger px-2.5" onclick={() => remove(m)}
                        disabled={listening && active} title="Sil">
                  <Trash class="size-4"/>{#if confirmDelete === m.id}<span>Emin misiniz?</span>{/if}
                </button>
                {#if m.engine !== 'vad'}
                  <button class={active ? 'btn-outline' : 'btn-primary'} onclick={() => onselect(m.id)}
                          disabled={active || listening}>
                    {active ? 'Seçili' : 'Kullan'}
                  </button>
                {/if}
              {:else}
                <button class="btn-primary" onclick={() => download(m)}>
                  <Download class="size-4"/> İndir
                </button>
              {/if}
            </div>
          </div>

          {#if m.downloading}
            {@const p = progress[m.id]}
            <div class="mt-3">
              <div class="h-2 overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-800">
                <div class="h-full rounded-full bg-accent-500 transition-[width] duration-200" style="width: {pct(m.id)}%"></div>
              </div>
              <div class="mt-1 flex justify-between text-xs text-zinc-500">
                <span>{p ? formatBytes(p.received) : 'Başlıyor…'}{p?.total > 0 ? ' / ' + formatBytes(p.total) : ''}</span>
                <span>{pct(m.id).toFixed(0)}%</span>
              </div>
            </div>
          {/if}
        </li>
      {:else}
        <li class="py-10 text-center text-sm text-zinc-500">Bu filtrede model yok.</li>
      {/each}
    </ul>
</section>
