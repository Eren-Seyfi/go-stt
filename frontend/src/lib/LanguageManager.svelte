<script>
  import {ModelService} from '../../bindings/changeme/backend';
  import Check from '@lucide/svelte/icons/check';
  import Download from '@lucide/svelte/icons/download';
  import Plus from '@lucide/svelte/icons/plus';
  import Search from '@lucide/svelte/icons/search';
  import Trash from '@lucide/svelte/icons/trash';
  import X from '@lucide/svelte/icons/x';
  import {settings, langName, nativeName, formatBytes} from './settings.svelte.js';

  /** @type {{languages: {code: string, name: string}[], models: any[],
   *          progress: Record<string, {received: number, total: number}>, listening: boolean,
   *          onchange: () => void, onselectmodel: (id: string) => void,
   *          notify: (msg: string, kind?: string) => void}} */
  let {languages, models, progress, listening, onchange, onselectmodel, notify} = $props();

  let query = $state('');
  let filter = $state('all'); // all | added | special
  let searchEl;

  // Each language with its display names and any models made just for it.
  const rows = $derived(languages.map((l) => ({
    ...l,
    tr: langName(l.code, l.name),
    native: nativeName(l.code),
    special: models.filter((m) => m.language === l.code),
  })).sort((a, b) =>
    Number(settings.languages.includes(b.code)) - Number(settings.languages.includes(a.code)) ||
    a.tr.localeCompare(b.tr, 'tr')));

  const shown = $derived(rows.filter((r) => {
    if (filter === 'added' && !settings.languages.includes(r.code)) return false;
    if (filter === 'special' && !r.special.length) return false;
    const q = query.trim().toLocaleLowerCase('tr');
    return !q || [r.tr, r.native, r.name, r.code].some((s) => s.toLocaleLowerCase('tr').includes(q));
  }));

  $effect(() => searchEl?.focus());

  function toggleLang(code) {
    const i = settings.languages.indexOf(code);
    if (i >= 0) {
      settings.languages.splice(i, 1);
      if (settings.language === code) settings.language = settings.languages[0] ?? 'auto';
      notify(`${langName(code)} dil listesinden kaldırıldı`);
    } else {
      settings.languages.push(code);
      notify(`${langName(code)} eklendi`, 'success');
    }
  }

  async function download(m) {
    try {
      await ModelService.Download(m.id);
      onchange();
    } catch (e) {
      notify(String(e?.message ?? e), 'error');
    }
  }

  async function remove(m) {
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

<section class="card flex flex-col" aria-labelledby="lm-title">
    <header class="flex items-start justify-between gap-4 border-b border-zinc-200 p-4 sm:p-5 dark:border-zinc-800">
      <div>
        <h2 id="lm-title" class="text-lg font-semibold">Diller</h2>
        <p class="mt-0.5 text-sm text-zinc-500 dark:text-zinc-400">
          Whisper {languages.length} dili tanır; hepsi modellerin içinde gelir. Kullandıklarınızı dil menüsüne ekleyin.
          Bazı dillerin ayrıca indirilebilen, daha isabetli özel modelleri vardır.
        </p>
      </div>
    </header>

    <div class="flex flex-wrap items-center gap-2 border-b border-zinc-200 px-3 py-3 sm:px-5 dark:border-zinc-800">
      <div class="relative w-full min-w-0 sm:w-auto sm:min-w-48 sm:flex-1">
        <Search class="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-zinc-500 dark:text-zinc-400"/>
        <input bind:this={searchEl} bind:value={query} type="search" class="ui-input pl-9" placeholder="Dil ara (ör. Almanca, Deutsch, de)"/>
      </div>
      <div class="flex gap-1">
        {#each [['all', 'Tümü'], ['added', `Eklenen (${settings.languages.length})`], ['special', 'Özel modeli olan']] as [id, label] (id)}
          <button class="rounded-md px-3 py-1.5 text-sm font-medium transition
                         {filter === id ? 'bg-accent-50 text-accent-700 dark:bg-accent-500/15 dark:text-accent-400'
                                        : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100'}"
                  onclick={() => (filter = id)}>{label}</button>
        {/each}
      </div>
    </div>

    <ul class="flex-1 divide-y divide-zinc-100 overflow-y-auto px-3 sm:px-5 dark:divide-zinc-800/70">
      {#each shown as r (r.code)}
        {@const added = settings.languages.includes(r.code)}
        <li class="py-3">
          <div class="flex items-center gap-3">
            <span class="w-12 shrink-0 rounded bg-zinc-100 py-0.5 text-center font-mono text-xs text-zinc-500 dark:bg-zinc-800">{r.code}</span>
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-baseline gap-x-2">
                <span class="font-medium">{r.tr}</span>
                {#if r.native && r.native !== r.tr}<span class="text-sm text-zinc-500">{r.native}</span>{/if}
              </div>
              <div class="mt-0.5 flex flex-wrap gap-1.5 text-[11px]">
                <span class="rounded-full bg-zinc-100 px-2 py-0.5 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400">Dahili · indirme gerekmez</span>
                {#if r.special.length}
                  <span class="rounded-full bg-sky-100 px-2 py-0.5 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300">
                    {r.special.length} özel model
                  </span>
                {/if}
              </div>
            </div>
            <button class={added ? 'btn-outline' : 'btn-primary'} onclick={() => toggleLang(r.code)}>
              {#if added}<Check class="size-4"/> Eklendi{:else}<Plus class="size-4"/> Ekle{/if}
            </button>
          </div>

          {#if r.special.length}
            <ul class="mt-3 space-y-2 sm:ml-15">
              {#each r.special as m (m.id)}
                {@const active = settings.modelId === m.id}
                <li class="rounded-lg border p-3 {active ? 'border-accent-500' : 'border-zinc-200 dark:border-zinc-800'}">
                  <div class="flex flex-wrap items-center justify-between gap-2">
                    <div class="min-w-0">
                      <div class="text-sm font-medium">{m.name}</div>
                      <div class="text-xs text-zinc-500">{formatBytes(m.sizeMB * (1 << 20))} · {m.description}</div>
                    </div>
                    <div class="flex shrink-0 items-center gap-1.5">
                      {#if m.downloading}
                        <button class="btn-outline" onclick={() => ModelService.CancelDownload(m.id)}><X class="size-4"/> İptal</button>
                      {:else if m.installed}
                        <button class="btn-danger px-2.5" onclick={() => remove(m)} disabled={listening && active} title="Sil"><Trash class="size-4"/></button>
                        <button class={active ? 'btn-outline' : 'btn-primary'} onclick={() => onselectmodel(m.id)} disabled={active || listening}>
                          {active ? 'Seçili' : 'Kullan'}
                        </button>
                      {:else}
                        <button class="btn-primary" onclick={() => download(m)}><Download class="size-4"/> İndir</button>
                      {/if}
                    </div>
                  </div>
                  {#if m.downloading}
                    <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-800">
                      <div class="h-full rounded-full bg-accent-500 transition-[width] duration-200" style="width: {pct(m.id)}%"></div>
                    </div>
                  {/if}
                </li>
              {/each}
            </ul>
          {/if}
        </li>
      {:else}
        <li class="py-10 text-center text-sm text-zinc-500">Eşleşen dil yok.</li>
      {/each}
    </ul>
</section>
