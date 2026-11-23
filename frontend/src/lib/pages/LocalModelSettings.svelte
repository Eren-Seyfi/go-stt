<script>
  // Settings for the local llama.cpp model: the models kept in the app's
  // models/llm folder, downloading and importing models, hardware, and the
  // server's live status.
  import {onMount} from 'svelte';
  import {Events} from '@wailsio/runtime';
  import {AIService, LlamaService} from '../../../bindings/changeme/backend';
  import Gauge from '@lucide/svelte/icons/gauge';
  import {fmtTps, fmtMs} from '../speed.js';
  import ModelCaps from './ModelCaps.svelte';
  import Toggle from '../ui/Toggle.svelte';
  import Boxes from '@lucide/svelte/icons/boxes';
  import Check from '@lucide/svelte/icons/check';
  import Cpu from '@lucide/svelte/icons/cpu';
  import Download from '@lucide/svelte/icons/download';
  import Eye from '@lucide/svelte/icons/eye';
  import FolderInput from '@lucide/svelte/icons/folder-input';
  import FolderOpen from '@lucide/svelte/icons/folder-open';
  import HardDrive from '@lucide/svelte/icons/hard-drive';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Play from '@lucide/svelte/icons/play';
  import Square from '@lucide/svelte/icons/square';
  import Trash from '@lucide/svelte/icons/trash';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import X from '@lucide/svelte/icons/x';
  import {notify, errMsg} from '../ui/toast.svelte.js';
  import {formatSize} from '../chat/attachments.svelte.js';

  /** @type {{cfg: any}} */
  let {cfg} = $props();

  let status = $state({state: 'stopped', model: '', message: '', binary: '', gpu: '', log: []});
  let local = $state([]); // models in models/llm
  let external = $state([]); // models in other apps, for import
  let catalog = $state([]);
  let lms = $state({found: false, folders: [], models: [], imported: [], size: 0}); // LM Studio
  let progress = $state({}); // id → LLMProgress
  let repo = $state('');
  let logOpen = $state(false);
  let confirmDelete = $state('');
  let stats = $state({}); // "llamacpp:<spec>" → {replies, avgTokensPerSec, avgFirstTokenMs, bench}
  let testing = $state(''); // spec being benchmarked
  let openBench = $state(''); // spec whose result panel is open
  let benchError = $state({}); // spec → error text

  const statFor = (s) => stats[`llamacpp:${s}`];
  /** Best known speed: the chat average, else the last test. */
  const speedOf = (s) => statFor(s)?.avgTokensPerSec || statFor(s)?.bench?.genPerSec || 0;
  const originTags = {
    // Outlined, so where a model came from doesn't look like what it can do (filled badges).
    lmstudio: ['LM Studio', 'border border-indigo-300 text-indigo-700 dark:border-indigo-400/40 dark:text-indigo-300'],
    huggingface: ['Hugging Face', 'border border-yellow-400 text-yellow-800 dark:border-yellow-400/40 dark:text-yellow-300'],
    diger: ['Dosyadan', 'border border-zinc-300 text-zinc-600 dark:border-zinc-600 dark:text-zinc-400'],
  };

  async function benchmark(m) {
    if (testing) return;
    testing = m.spec;
    openBench = m.spec;
    delete benchError[m.spec];
    try {
      const r = await LlamaService.Benchmark(m.spec, $state.snapshot(cfg.llama));
      stats[`llamacpp:${m.spec}`] = {...(statFor(m.spec) ?? {}), bench: r};
    } catch (e) {
      benchError[m.spec] = errMsg(e);
    } finally {
      testing = '';
    }
  }

  const spec = $derived(cfg.models.llamacpp ?? '');
  const originNames = {lmstudio: 'LM Studio’dan aktarılanlar', huggingface: 'Hugging Face’ten indirilenler', diger: 'Diğer'};
  const label = (s) => (s || '').split('/').pop().replace(/\.gguf$/i, '');
  const busy = $derived(status.state === 'starting');
  const running = $derived(status.state === 'ready' || status.state === 'starting');
  const servingOther = $derived(running && status.model && status.model !== spec);
  const selectedMissing = $derived(spec && !local.some((m) => m.spec === spec));

  const states = {
    stopped: ['Çalışmıyor', 'bg-zinc-400'],
    starting: ['Yükleniyor', 'bg-amber-500 animate-pulse'],
    ready: ['Hazır', 'bg-emerald-500'],
    error: ['Hata', 'bg-red-500'],
  };

  async function refresh() {
    try {
      [local, external, catalog, lms] = await Promise.all([LlamaService.LocalModels(), LlamaService.ExternalModels(),
        LlamaService.Catalog(), LlamaService.LMStudio()]);
      local ??= []; external ??= []; catalog ??= [];
    } catch (e) {
      notify(errMsg(e), 'error');
    }
  }

  onMount(() => {
    LlamaService.Status().then((s) => (status = s));
    AIService.ModelStats().then((m) => (stats = m ?? {})).catch(() => {});
    refresh().then(() => {
      // No model chosen yet (or it's gone): pick one that's in the app folder.
      if ((!spec || selectedMissing) && local.length) cfg.models.llamacpp = (local.find((m) => m.vision) ?? local[0]).spec;
    });
    const offs = [
      Events.On('llama:status', (e) => (status = e.data)),
      Events.On('ai:stats', (e) => (stats = e.data ?? {})),
      Events.On('llm:progress', (e) => {
        const p = e.data;
        if (p.state === 'downloading' || p.state === 'copying') { progress[p.id] = p; return; }
        delete progress[p.id];
        if (p.state === 'done' && p.id === 'lmstudio') {
          notify(`LM Studio’dan ${p.items} model içe aktarıldı`, 'success');
          if (p.spec && (!cfg.models.llamacpp || selectedMissing)) cfg.models.llamacpp = p.spec;
        } else if (p.state === 'done') {
          notify(`${label(p.spec)} hazır`, 'success');
          if (!cfg.models.llamacpp || selectedMissing) cfg.models.llamacpp = p.spec;
        } else if (p.state === 'error') notify(p.error, 'error');
        refresh();
      }),
    ];
    return () => offs.forEach((off) => off());
  });

  const choose = (s) => (cfg.models.llamacpp = s);

  async function download(r) {
    try {
      await LlamaService.Download(r);
      progress[r] = {id: r, state: 'downloading', done: 0, total: 0, speed: 0, file: ''};
      repo = '';
    } catch (e) {
      notify(errMsg(e), 'error');
    }
  }

  async function importModel(path) {
    try {
      await LlamaService.ImportModel(path);
      progress[path] = {id: path, state: 'copying', done: 0, total: 0};
    } catch (e) {
      notify(errMsg(e), 'error');
    }
  }

  async function importLMStudio() {
    try {
      await LlamaService.ImportLMStudio();
      progress.lmstudio = {id: 'lmstudio', state: 'copying', item: 0, items: lms.models.length};
    } catch (e) {
      notify(errMsg(e), 'error');
    }
  }

  async function importFile() {
    try {
      const path = await LlamaService.ImportFile();
      if (path) progress[path] = {id: path, state: 'copying', done: 0, total: 0};
    } catch (e) {
      if (errMsg(e)) notify(errMsg(e), 'error');
    }
  }

  async function remove(m) {
    if (confirmDelete !== m.spec) {
      confirmDelete = m.spec;
      setTimeout(() => { if (confirmDelete === m.spec) confirmDelete = ''; }, 4000);
      return;
    }
    confirmDelete = '';
    try {
      await LlamaService.DeleteModel(m.spec);
      notify(`${m.name} silindi`, 'success');
      if (spec === m.spec) cfg.models.llamacpp = '';
      await refresh();
    } catch (e) {
      notify(errMsg(e), 'error');
    }
  }

  async function start() {
    try {
      await LlamaService.Start(spec, $state.snapshot(cfg.llama));
    } catch (e) {
      notify(errMsg(e), 'error');
    }
  }

  const pct = (p) => (p.total ? Math.min(100, (p.done / p.total) * 100) : 0);
  const eta = (p) => {
    if (!p.speed || !p.total) return '';
    const s = Math.max(0, (p.total - p.done) / p.speed);
    return s > 90 ? `~${Math.round(s / 60)} dk kaldı` : `~${Math.round(s)} sn kaldı`;
  };

  const gpuOptions = [
    ['auto', 'Otomatik (önerilen)', 'Ekran kartı belleğine sığdığı kadarını GPU’ya koyar.'],
    ['all', 'Tamamı GPU’da', 'En hızlısı; model ekran kartı belleğine sığmalı.'],
    ['0', 'Yalnızca işlemci', 'GPU kullanmaz; yavaştır ama her bilgisayarda çalışır.'],
  ];
  const contexts = [[4096, '4K'], [8192, '8K'], [16384, '16K'], [32768, '32K'], [0, 'Model varsayılanı']];
</script>

{#snippet bar(p)}
  <div class="mt-2">
    <div class="h-1.5 overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-800">
      <div class="h-full rounded-full bg-accent-600 transition-[width] {p.total ? '' : 'w-1/3 animate-pulse'}" style={p.total ? `width: ${pct(p)}%` : ''}></div>
    </div>
    <div class="mt-1 flex justify-between gap-2 text-[11px] text-zinc-500 tabular-nums dark:text-zinc-400">
      <span class="truncate">{p.state === 'copying' ? 'Kopyalanıyor…' : p.file || 'Başlıyor…'}</span>
      {#if p.total}<span class="shrink-0">{formatSize(p.done)} / {formatSize(p.total)}{p.speed ? ` · ${formatSize(p.speed)}/sn · ${eta(p)}` : ''}</span>{/if}
    </div>
  </div>
{/snippet}

<div class="space-y-6">
  {#if !status.binary}
    <p class="flex gap-2 rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-300">
      <TriangleAlert class="mt-0.5 size-4 shrink-0"/>
      <span>llama-server bulunamadı. <span class="font-mono">third_party/llama</span> altına llama.cpp’yi derleyin ya da <span class="font-mono">STT_LLAMA_SERVER</span> ortam değişkeniyle yolunu belirtin.</span>
    </p>
  {/if}

  <!-- Server status -->
  <div class="rounded-xl border border-zinc-200 p-3 dark:border-zinc-800">
    <div class="flex flex-wrap items-center gap-3">
      <span class="flex items-center gap-2 text-sm font-medium">
        <span class="size-2.5 rounded-full {states[status.state]?.[1]}"></span>{states[status.state]?.[0] ?? status.state}
      </span>
      {#if status.model && status.state !== 'stopped'}
        <span class="min-w-0 truncate font-mono text-xs text-zinc-500 dark:text-zinc-400" title={status.model}>{label(status.model)}</span>
      {/if}
      {#if status.gpu && status.state !== 'stopped'}
        <span class="inline-flex items-center gap-1 rounded-full bg-zinc-100 px-2 py-0.5 text-[11px] text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400">
          <Cpu class="size-3"/>{status.gpu}
        </span>
      {/if}
      <span class="ml-auto flex gap-2">
        {#if running}
          <button class="btn-outline px-2.5 py-1 text-sm" onclick={() => LlamaService.Stop()}><Square class="size-3.5"/> Durdur</button>
        {/if}
        {#if !running || servingOther}
          <button class="btn-primary px-2.5 py-1 text-sm" onclick={start} disabled={!spec || selectedMissing || !status.binary}>
            <Play class="size-3.5"/> {servingOther ? 'Bu modele geç' : 'Şimdi yükle'}
          </button>
        {/if}
      </span>
    </div>
    {#if status.message && status.state !== 'ready'}
      <p class="mt-2 flex items-start gap-1.5 text-xs break-all {status.state === 'error' ? 'text-red-600 dark:text-red-400' : 'text-zinc-500 dark:text-zinc-400'}">
        {#if busy}<LoaderCircle class="mt-px size-3.5 shrink-0 animate-spin"/>{/if}{status.message}
      </p>
    {/if}
    <p class="mt-2 text-xs text-zinc-500 dark:text-zinc-400">
      Model ilk soruda kendiliğinden yüklenir; başka bir sağlayıcıya geçince kapatılır ve ekran kartı belleği boşalır.
    </p>
    {#if status.log?.length}
      <button class="mt-2 text-xs text-accent-600 hover:underline dark:text-accent-400" onclick={() => (logOpen = !logOpen)}>
        {logOpen ? 'Sunucu kaydını gizle' : 'Sunucu kaydını göster'}
      </button>
      {#if logOpen}
        <pre class="selectable mt-2 max-h-56 overflow-auto rounded-lg bg-zinc-950 p-2.5 font-mono text-[11px] leading-snug whitespace-pre-wrap text-zinc-300">{status.log.slice(-60).join('\n')}</pre>
      {/if}
    {/if}
  </div>

  <!-- Installed models -->
  <div>
    <div class="mb-1.5 flex items-center justify-between gap-2">
      <span class="text-sm font-medium">Yüklü modeller <span class="font-normal text-zinc-500 dark:text-zinc-400">· models/llm</span></span>
      <button class="btn-ghost px-2 py-1 text-xs" onclick={() => LlamaService.OpenFolder()} title="Model klasörünü aç"><FolderOpen class="size-3.5"/> Klasörü aç</button>
    </div>
    {#if local.length}
      <div class="grid gap-2" role="radiogroup" aria-label="Yüklü modeller">
        {#each local as m, i (m.spec)}
          {@const active = m.spec === spec}
          {#if i === 0 || local[i - 1].origin !== m.origin}
            <p class="mt-1 flex items-center gap-2 text-xs font-medium text-zinc-500 first:mt-0 dark:text-zinc-400">
              {originNames[m.origin] ?? m.origin}
              <span class="font-mono font-normal text-zinc-400 dark:text-zinc-500">models/llm/{m.origin}/</span>
              <span class="h-px flex-1 bg-zinc-200 dark:bg-zinc-800"></span>
            </p>
          {/if}
          {@const tag = originTags[m.origin] ?? originTags.diger}
          {@const speed = speedOf(m.spec)}
          {@const bench = statFor(m.spec)?.bench}
          <div class="group rounded-lg border transition
                      {active ? 'border-accent-500 bg-accent-50/60 ring-1 ring-accent-500 dark:bg-accent-500/10'
                              : 'border-zinc-200 hover:border-zinc-300 dark:border-zinc-800 dark:hover:border-zinc-700'}">
            <div class="flex items-center gap-1">
              <button class="flex min-w-0 flex-1 items-center gap-3 p-2.5 text-left" role="radio" aria-checked={active} onclick={() => choose(m.spec)}>
                <span class="grid size-8 shrink-0 place-items-center rounded-lg bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"><HardDrive class="size-4"/></span>
                <span class="min-w-0 flex-1">
                  <span class="flex min-w-0 items-center gap-1.5">
                    <span class="truncate text-sm font-medium">{m.name}</span>
                    {#if active}<Check class="size-4 shrink-0 text-accent-600 dark:text-accent-400"/>{/if}
                  </span>
                  <span class="block truncate text-xs text-zinc-500 dark:text-zinc-400">{m.source.split(' · ').slice(1).join(' · ') || m.source} · {formatSize(m.size)}</span>
                  <span class="mt-1 flex flex-wrap gap-1">
                    <span class="rounded-full px-1.5 py-px text-[10px] font-medium {tag[1]}" title="Kaynak">{tag[0]}</span>
                    <ModelCaps caps={m.caps} vision={m.vision} details/>
                    {#if speed}
                      <span class="inline-flex items-center gap-1 rounded-full bg-emerald-100 px-1.5 py-px text-[10px] font-medium text-emerald-700 tabular-nums dark:bg-emerald-500/15 dark:text-emerald-300"
                            title={statFor(m.spec)?.replies ? `${statFor(m.spec).replies} yanıtın ortalaması` : 'Son testin sonucu'}>
                        <Gauge class="size-2.5"/> {fmtTps(speed)} token/sn
                      </span>
                    {/if}
                  </span>
                </span>
              </button>
              <button class="shrink-0 rounded-md px-2 py-1.5 text-xs font-medium text-accent-700 transition hover:bg-accent-50 disabled:opacity-50 dark:text-accent-300 dark:hover:bg-accent-500/10"
                      onclick={() => benchmark(m)} disabled={!!testing || !status.binary} title="Modeli yükleyip hızını ölç">
                {#if testing === m.spec}<LoaderCircle class="inline size-3.5 animate-spin"/> Test ediliyor…{:else}<Gauge class="inline size-3.5"/> Test et{/if}
              </button>
              <button class="mr-1.5 shrink-0 rounded-md px-2 py-1.5 text-xs transition
                             {confirmDelete === m.spec ? 'bg-red-600 text-white' : 'text-zinc-400 opacity-0 group-hover:opacity-100 hover:text-red-600 focus:opacity-100'}"
                      onclick={() => remove(m)} title="Modeli sil" aria-label="{m.name} sil">
                {#if confirmDelete === m.spec}Emin misiniz?{:else}<Trash class="size-4"/>{/if}
              </button>
            </div>
            {#if openBench === m.spec && (testing === m.spec || bench || benchError[m.spec])}
              <div class="border-t border-zinc-200 px-3 py-2.5 dark:border-zinc-800">
                {#if testing === m.spec}
                  <p class="flex items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400"><LoaderCircle class="size-3.5 animate-spin"/> {status.state === 'starting' ? status.message || 'Model yükleniyor…' : 'Yanıt üretiliyor…'}</p>
                {:else if benchError[m.spec]}
                  <p class="text-xs text-red-600 dark:text-red-400">{benchError[m.spec]}</p>
                {:else if bench}
                  <dl class="grid grid-cols-2 gap-2 sm:grid-cols-4">
                    {#each [['Üretim', `${fmtTps(bench.genPerSec)} token/sn`, `${bench.genTokens} token`],
                            ['İstem işleme', `${fmtTps(bench.promptPerSec)} token/sn`, `${bench.promptTokens} token`],
                            ['Yükleme', bench.loadMs ? fmtMs(bench.loadMs) : 'hazırdı', bench.device || ''],
                            ['Toplam', fmtMs(bench.totalMs), `bağlam ${bench.context ? bench.context / 1024 + 'K' : 'varsayılan'}`]] as [k, v, sub] (k)}
                      <div class="rounded-lg bg-zinc-100 px-2.5 py-1.5 dark:bg-zinc-800/70">
                        <dt class="text-[11px] text-zinc-500 dark:text-zinc-400">{k}</dt>
                        <dd class="text-sm font-semibold tabular-nums">{v}</dd>
                        {#if sub}<dd class="truncate text-[10px] text-zinc-500 dark:text-zinc-400" title={sub}>{sub}</dd>{/if}
                      </div>
                    {/each}
                  </dl>
                  {#if bench.reply}<p class="mt-2 line-clamp-3 text-xs text-zinc-600 italic dark:text-zinc-400">“{bench.reply}”</p>{/if}
                  {#if statFor(m.spec)?.replies}
                    <p class="mt-1.5 text-[11px] text-zinc-500 dark:text-zinc-400">Sohbetlerde ortalama {fmtTps(statFor(m.spec).avgTokensPerSec)} token/sn, ilk kelime {fmtMs(statFor(m.spec).avgFirstTokenMs)} ({statFor(m.spec).replies} yanıt)</p>
                  {/if}
                {/if}
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {:else}
      <p class="rounded-lg border border-dashed border-zinc-300 p-3 text-sm text-zinc-500 dark:border-zinc-700">
        Henüz yerel model yok. Aşağıdan bir model indirin ya da başka bir uygulamadan içe aktarın.
      </p>
    {/if}
    {#if selectedMissing}
      <p class="mt-2 flex gap-1.5 text-xs text-amber-700 dark:text-amber-400">
        <TriangleAlert class="size-3.5 shrink-0"/> Seçili model (<span class="font-mono break-all">{spec}</span>) uygulama klasöründe değil; indirin, içe aktarın veya başka bir model seçin.
      </p>
    {/if}
  </div>

  <!-- Download -->
  <div>
    <span class="mb-1.5 block text-sm font-medium">Model indir <span class="font-normal text-zinc-500 dark:text-zinc-400">· Hugging Face</span></span>
    <div class="grid gap-2">
      {#each catalog as c (c.repo)}
        {@const p = progress[c.repo]}
        <div class="rounded-lg border border-zinc-200 p-2.5 dark:border-zinc-800">
          <div class="flex items-center gap-3">
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-x-2 gap-y-0.5">
                <span class="text-sm font-medium">{c.name}</span>
                <span class="text-xs text-zinc-500 dark:text-zinc-400">~{formatSize(c.size)}</span>
                {#if c.vision}<span class="inline-flex items-center gap-1 rounded-full bg-sky-100 px-1.5 py-px text-[10px] text-sky-700 dark:bg-sky-500/15 dark:text-sky-300"><Eye class="size-2.5"/> Görüntü</span>{/if}
              </div>
              <p class="text-xs text-zinc-500 dark:text-zinc-400">{c.description}</p>
            </div>
            {#if p}
              <button class="btn-ghost shrink-0 px-2 py-1 text-xs" onclick={() => LlamaService.CancelTransfer(c.repo)}><X class="size-3.5"/> İptal</button>
            {:else if c.installed}
              <button class="shrink-0 rounded-md px-2 py-1 text-xs font-medium {c.installed === spec ? 'text-emerald-700 dark:text-emerald-400' : 'text-accent-700 hover:bg-accent-50 dark:text-accent-300 dark:hover:bg-accent-500/10'}"
                      onclick={() => choose(c.installed)} disabled={c.installed === spec}>
                {#if c.installed === spec}<span class="inline-flex items-center gap-1"><Check class="size-3.5"/> Seçili</span>{:else}Seç{/if}
              </button>
            {:else}
              <button class="btn-outline shrink-0 px-2.5 py-1 text-xs" onclick={() => download(c.repo)}><Download class="size-3.5"/> İndir</button>
            {/if}
          </div>
          {#if p}{@render bar(p)}{/if}
        </div>
      {/each}
    </div>
    <form class="mt-2 flex gap-2" onsubmit={(e) => { e.preventDefault(); download(repo.trim()); }}>
      <input class="ui-input font-mono text-sm" bind:value={repo} spellcheck="false" aria-label="Hugging Face deposu"
             placeholder="Başka bir model: kullanıcı/depo-GGUF"/>
      <button class="btn-outline shrink-0" type="submit" disabled={!repo.trim()}><Download class="size-4"/> İndir</button>
    </form>
    {#each Object.values(progress).filter((p) => p.state === 'downloading' && !catalog.some((c) => c.repo === p.id)) as p (p.id)}
      <div class="mt-2 rounded-lg border border-zinc-200 p-2.5 dark:border-zinc-800">
        <div class="flex items-center justify-between gap-2 text-sm"><span class="truncate font-mono">{p.id}</span>
          <button class="btn-ghost px-2 py-1 text-xs" onclick={() => LlamaService.CancelTransfer(p.id)}><X class="size-3.5"/> İptal</button></div>
        {@render bar(p)}
      </div>
    {/each}
    <p class="mt-1.5 text-xs text-zinc-500 dark:text-zinc-400">Modeller <span class="font-mono">models/llm/huggingface</span> klasörüne iner; yarıda kalan indirme tekrar başlatınca kaldığı yerden devam eder.</p>
  </div>

  <!-- Import -->
  <div>
    {#if lms.found}
      {@const p = progress.lmstudio}
      <div class="mb-3 rounded-xl border border-zinc-200 bg-zinc-50 p-3 dark:border-zinc-800 dark:bg-zinc-900/40">
        <div class="flex flex-wrap items-center gap-3">
          <span class="grid size-9 shrink-0 place-items-center rounded-lg bg-accent-600 text-white"><Boxes class="size-5"/></span>
          <div class="min-w-0 flex-1">
            <p class="text-sm font-medium">LM Studio bulundu</p>
            <p class="text-xs text-zinc-500 dark:text-zinc-400">
              {#if p}
                İçe aktarılıyor{p.items ? ` · ${p.item || 1}/${p.items}` : ''}{p.file ? ` · ${p.file}` : '…'}
              {:else if lms.models.length}
                İndirdiğiniz {lms.models.length} model ({formatSize(lms.size)}) henüz uygulamada değil{lms.imported.length ? ` · ${lms.imported.length} model zaten aktarılmış` : ''}.
              {:else if lms.imported.length}
                LM Studio’daki {lms.imported.length} modelin hepsi uygulamaya aktarılmış.
              {:else}
                LM Studio’da henüz indirilmiş model yok.
              {/if}
            </p>
          </div>
          {#if p}
            <button class="btn-ghost shrink-0 px-2 py-1 text-xs" onclick={() => LlamaService.CancelTransfer('lmstudio')}><X class="size-3.5"/> İptal</button>
          {:else if lms.models.length}
            <button class="btn-primary shrink-0 px-3 py-1.5 text-sm" onclick={importLMStudio}><FolderInput class="size-4"/> Tümünü içe aktar</button>
          {:else}
            <Check class="size-5 shrink-0 text-emerald-600 dark:text-emerald-400"/>
          {/if}
        </div>
        {#if p?.items}
          <div class="mt-2.5 h-1.5 overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-800">
            <div class="h-full rounded-full bg-accent-600 transition-[width]" style="width: {Math.max(4, ((p.item - 1) / p.items) * 100)}%"></div>
          </div>
        {/if}
        {#if lms.folders.length}
          <p class="mt-2 truncate font-mono text-[11px] text-zinc-400 dark:text-zinc-500" title={lms.folders.join('\n')}>{lms.folders[0]}{lms.folders.length > 1 ? ` +${lms.folders.length - 1}` : ''}</p>
        {/if}
      </div>
    {/if}

    <div class="mb-1.5 flex items-center justify-between gap-2">
      <span class="text-sm font-medium">Başka uygulamalardan içe aktar</span>
      <button class="btn-ghost px-2 py-1 text-xs" onclick={importFile}><FolderInput class="size-3.5"/> Dosyadan…</button>
    </div>
    {#if external.length}
      <div class="grid gap-2">
        {#each external as m (m.spec)}
          {@const p = progress[m.spec]}
          <div class="rounded-lg border border-zinc-200 p-2.5 dark:border-zinc-800">
            <div class="flex items-center gap-3">
              <div class="min-w-0 flex-1">
                <span class="block truncate text-sm font-medium">{m.name}</span>
                <span class="block truncate text-xs text-zinc-500 dark:text-zinc-400">{m.source} · {formatSize(m.size)}</span>
                <span class="mt-1 flex flex-wrap gap-1"><ModelCaps caps={m.caps} vision={m.vision} details/></span>
              </div>
              {#if p}
                <LoaderCircle class="size-4 shrink-0 animate-spin text-accent-600"/>
              {:else}
                <button class="btn-outline shrink-0 px-2.5 py-1 text-xs" onclick={() => importModel(m.spec)}><FolderInput class="size-3.5"/> İçe aktar</button>
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {:else}
      <p class="text-xs text-zinc-500 dark:text-zinc-400">LM Studio veya Hugging Face önbelleğinde içe aktarılmamış model yok. Herhangi bir .gguf dosyasını “Dosyadan…” ile ekleyebilirsiniz.</p>
    {/if}
    <p class="mt-1.5 text-xs text-zinc-500 dark:text-zinc-400">LM Studio modelleri <span class="font-mono">models/llm/lmstudio</span>, diğerleri <span class="font-mono">models/llm/diger</span> klasörüne kopyalanır (btrfs’te anında ve ek yer kaplamadan).</p>
  </div>

  <!-- Thinking -->
  <div>
    <Toggle label="Düşünme modu" bind:checked={cfg.localThinking}
            description={cfg.localThinking
              ? 'Açık: Gemma 4, Qwen3 gibi modeller yanıtlamadan önce gizlice düşünür. Zor sorularda daha doğru ama çok daha yavaş; düşünme, yanıt için ayrılan tokenleri de harcar.'
              : 'Kapalı (önerilen): model hemen yanıtlar. Sesli sohbet ve kısa sorular için daha uygun. “Her zaman düşünen” modelleri (ör. Qwen3 Thinking) etkilemez.'}/>
  </div>

  <!-- Hardware -->
  <div>
    <span class="mb-1.5 block text-sm font-medium">Donanım</span>
    <div class="grid gap-2 sm:grid-cols-3" role="radiogroup" aria-label="GPU kullanımı">
      {#each gpuOptions as [id, name, desc] (id)}
        <label class="flex cursor-pointer gap-2 rounded-lg border p-2.5 text-sm transition
                      {cfg.llama.gpuLayers === id ? 'border-accent-500 bg-accent-50/60 dark:bg-accent-500/10' : 'border-zinc-200 dark:border-zinc-800'}">
          <input type="radio" class="ui-radio mt-0.5" name="gpu-layers" value={id} bind:group={cfg.llama.gpuLayers}/>
          <span><span class="font-medium">{name}</span><span class="block text-xs text-zinc-500 dark:text-zinc-400">{desc}</span></span>
        </label>
      {/each}
    </div>
  </div>

  <div>
    <span class="mb-1.5 block text-sm font-medium">Bağlam uzunluğu</span>
    <div class="flex flex-wrap gap-1 rounded-lg bg-zinc-100 p-1 dark:bg-zinc-800" role="radiogroup" aria-label="Bağlam uzunluğu">
      {#each contexts as [n, name] (n)}
        <button class="flex-1 rounded-md px-2 py-1.5 text-sm font-medium whitespace-nowrap transition
                       {cfg.llama.context === n ? 'bg-white text-zinc-900 shadow-sm dark:bg-zinc-950 dark:text-white' : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100'}"
                role="radio" aria-checked={cfg.llama.context === n} onclick={() => (cfg.llama.context = n)}>{name}</button>
      {/each}
    </div>
    <p class="mt-1.5 text-xs text-zinc-500 dark:text-zinc-400">
      Modelin aynı anda aklında tutabildiği metin (token). Büyük değerler daha fazla bellek kullanır; değişiklik model yeniden yüklenince geçerli olur.
    </p>
  </div>
</div>
