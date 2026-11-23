<script>
  import {onMount} from 'svelte';
  import {Events} from '@wailsio/runtime';
  import {STTService, ModelService, WindowService} from '../bindings/changeme/backend';
  import Boxes from '@lucide/svelte/icons/boxes';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Save from '@lucide/svelte/icons/save';
  import Undo2 from '@lucide/svelte/icons/undo-2';
  import Languages from '@lucide/svelte/icons/languages';
  import MessageSquareText from '@lucide/svelte/icons/message-square-text';
  import Mic from '@lucide/svelte/icons/mic';
  import Settings from '@lucide/svelte/icons/settings';
  import Sparkles from '@lucide/svelte/icons/sparkles';
  import ModelManager from './lib/ModelManager.svelte';
  import LanguageManager from './lib/LanguageManager.svelte';
  import GeneralPage from './lib/pages/GeneralPage.svelte';
  import SpeechPage from './lib/pages/SpeechPage.svelte';
  import CommandsPage from './lib/pages/CommandsPage.svelte';
  import AIPage from './lib/pages/AIPage.svelte';
  import Toasts from './lib/ui/Toasts.svelte';
  import {notify, errMsg} from './lib/ui/toast.svelte.js';
  import {settings, saveState, hasUnsaved, saveAll, discardAll} from './lib/settings.svelte.js';

  const pages = [
    {id: 'general', label: 'Genel', icon: Settings},
    {id: 'speech', label: 'Ses tanıma', icon: Mic},
    {id: 'models', label: 'Modeller', icon: Boxes},
    {id: 'languages', label: 'Diller', icon: Languages},
    {id: 'commands', label: 'Sesli komutlar', icon: MessageSquareText},
    {id: 'ai', label: 'Yapay zekâ', icon: Sparkles},
  ];
  const pageFromHash = () => {
    const id = location.hash.replace(/^#\/settings\/?/, '');
    return pages.some((p) => p.id === id) ? id : 'general';
  };
  let page = $state(pageFromHash());
  const current = $derived(pages.find((p) => p.id === page));

  function goto(id) {
    page = id;
    history.replaceState(null, '', `#/settings/${id}`);
  }

  // Data the pages share.
  let models = $state([]);
  let progress = $state({});
  let system = $state({cpuName: '', cpuThreads: 0, gpus: [], gpuNote: ''});
  let mics = $state([]);
  let defaultThreads = $state(8);
  let allLanguages = $state([]);
  let listening = $state(false);

  // ---- save / discard ---------------------------------------------------
  const dirty = $derived(hasUnsaved());
  let saving = $state(false);
  let askClose = $state(false); // "unsaved changes" dialog shown on close
  let closeDialog = $state();

  // The backend holds back closing the window while there are unsaved changes.
  $effect(() => { WindowService.SetSettingsUnsaved(dirty); });

  async function save() {
    if (saving || !dirty) return false;
    saving = true;
    try {
      const ok = await saveAll();
      if (ok) notify('Ayarlar kaydedildi', 'success');
      else notify('Kaydedilemedi: ' + saveState.error, 'error');
      return ok;
    } finally {
      saving = false;
    }
  }

  function discard() {
    discardAll();
    notify('Değişiklikler geri alındı', 'info');
  }

  async function saveAndClose() {
    if (await save()) { askClose = false; WindowService.CloseSettings(); }
  }

  function closeWithoutSaving() {
    askClose = false;
    WindowService.DiscardAndCloseSettings();
  }

  $effect(() => { if (askClose) closeDialog?.focus(); });

  async function refreshModels() {
    models = await ModelService.List();
  }
  async function refreshMics() {
    try {
      mics = await STTService.Microphones();
    } catch (e) {
      notify('Mikrofonlar listelenemedi: ' + errMsg(e), 'error');
    }
  }

  onMount(() => {
    refreshModels();
    refreshMics();
    STTService.System().then((s) => (system = s));
    STTService.DefaultThreads().then((n) => (defaultThreads = n));
    STTService.Languages().then((l) => (allLanguages = l));
    STTService.IsListening().then((l) => (listening = l));

    const offs = [
      Events.On('settings:page', (e) => goto(e.data)),
      Events.On('settings:close-requested', () => (askClose = true)),
      Events.On('stt:started', () => (listening = true)),
      Events.On('stt:stopped', () => (listening = false)),
      Events.On('model:progress', (e) => (progress[e.data.id] = e.data)),
      Events.On('model:done', async (e) => {
        delete progress[e.data.id];
        const name = models.find((m) => m.id === e.data.id)?.name ?? e.data.id;
        if (e.data.error) notify(`${name}: ${e.data.error}`, e.data.error === 'iptal edildi' ? 'info' : 'error');
        else notify(`${name} indirildi`, 'success');
        await refreshModels();
      }),
    ];
    return () => offs.forEach((off) => off());
  });

  function selectModel(id) {
    settings.modelId = id;
    notify((models.find((m) => m.id === id)?.name ?? id) + ' seçildi', 'success');
  }
</script>

<svelte:window onkeydown={(e) => {
  const typing = e.target instanceof HTMLElement && ['INPUT', 'SELECT', 'TEXTAREA'].includes(e.target.tagName);
  if (e.ctrlKey && e.key.toLowerCase() === 's') { e.preventDefault(); save(); return; }
  if (e.key === 'Escape' && askClose) { askClose = false; return; }
  if (e.key === 'Escape' && !typing) WindowService.CloseSettings(); // asks first if there are unsaved changes
}}/>

<div class="flex h-screen flex-col overflow-hidden md:flex-row">
  <!-- Page navigation: a sidebar on wide windows, a scrollable tab bar on narrow ones. -->
  <nav class="shrink-0 border-b border-zinc-200 bg-white md:w-56 md:border-r md:border-b-0 dark:border-zinc-800 dark:bg-zinc-900/60"
       aria-label="Ayar sayfaları">
    <div class="hidden items-center gap-2.5 px-4 py-4 md:flex">
      <div class="grid size-8 place-items-center rounded-lg bg-accent-600 text-white"><Settings class="size-4"/></div>
      <span class="font-semibold">Ayarlar</span>
    </div>
    <ul class="flex gap-1 overflow-x-auto px-2 py-2 md:flex-col md:overflow-visible md:px-3 md:py-0">
      {#each pages as p (p.id)}
        <li class="shrink-0">
          <button class="flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-sm font-medium whitespace-nowrap transition
                         {page === p.id ? 'bg-accent-50 text-accent-700 dark:bg-accent-500/15 dark:text-accent-300'
                                        : 'text-zinc-600 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100'}"
                  aria-current={page === p.id ? 'page' : undefined} onclick={() => goto(p.id)}>
            <p.icon class="size-4 shrink-0"/> {p.label}
            {#if p.id === 'models' && Object.keys(progress).length}
              <span class="ml-auto size-2 animate-pulse rounded-full bg-accent-500" title="İndirme sürüyor"></span>
            {/if}
          </button>
        </li>
      {/each}
    </ul>
  </nav>

  <main class="min-h-0 min-w-0 flex-1 overflow-y-auto">
    <div class="mx-auto max-w-3xl px-3 py-4 sm:px-6 sm:py-6">
      {#if page !== 'models' && page !== 'languages'}<h1 class="mb-4 text-xl font-semibold">{current.label}</h1>{/if}
      {#if page === 'general'}
        <GeneralPage/>
      {:else if page === 'speech'}
        <SpeechPage {models} {system} {mics} {defaultThreads} {listening} onrefreshmics={refreshMics} {goto}/>
      {:else if page === 'models'}
        <ModelManager {models} {progress} selectedId={settings.modelId} {listening} {notify}
                      onselect={selectModel} onchange={refreshModels}/>
      {:else if page === 'languages'}
        <LanguageManager languages={allLanguages} {models} {progress} {listening} {notify}
                         onselectmodel={selectModel} onchange={refreshModels}/>
      {:else if page === 'commands'}
        <CommandsPage {listening} {notify}/>
      {/if}
      <!-- Kept mounted so unsaved AI edits survive switching pages. -->
      <div class={page === 'ai' ? '' : 'hidden'}><AIPage/></div>
    </div>

    {#if dirty || saving}
      <!-- Save bar: shown while there are unsaved changes. -->
      <div class="sticky bottom-0 z-10 border-t border-zinc-200 bg-white/95 backdrop-blur dark:border-zinc-800 dark:bg-zinc-900/95">
        <div class="mx-auto flex max-w-3xl flex-wrap items-center gap-2 px-3 py-3 sm:px-6">
          <span class="flex min-w-0 flex-1 items-center gap-2 text-sm font-medium" aria-live="polite">
            {#if saveState.status === 'error'}
              <CircleAlert class="size-4 shrink-0 text-red-500"/><span class="truncate text-red-600 dark:text-red-400">Kaydedilemedi: {saveState.error}</span>
            {:else}
              <span class="size-2 shrink-0 rounded-full bg-amber-500"></span>Kaydedilmemiş değişiklikler var
            {/if}
          </span>
          <button class="btn-ghost" onclick={discard} disabled={saving}><Undo2 class="size-4"/> Vazgeç</button>
          <button class="btn-primary" onclick={save} disabled={saving} title="Kaydet (Ctrl+S)">
            {#if saving}<LoaderCircle class="size-4 animate-spin"/>{:else}<Save class="size-4"/>{/if} Kaydet
          </button>
        </div>
      </div>
    {/if}
  </main>
</div>

{#if askClose}
  <div class="fixed inset-0 z-50 grid place-items-center bg-black/40 p-4">
    <div bind:this={closeDialog} tabindex="-1" role="alertdialog" aria-modal="true" aria-labelledby="close-title"
         class="card w-full max-w-md p-5 outline-none">
      <h2 id="close-title" class="text-lg font-semibold">Kaydedilmemiş değişiklikler var</h2>
      <p class="mt-1 text-sm text-zinc-500 dark:text-zinc-400">Pencereyi kapatmadan önce değişiklikleri kaydetmek ister misiniz?</p>
      <div class="mt-5 flex flex-wrap justify-end gap-2">
        <button class="btn-ghost" onclick={() => (askClose = false)}>İptal</button>
        <button class="btn-danger" onclick={closeWithoutSaving}>Kaydetmeden kapat</button>
        <button class="btn-primary" onclick={saveAndClose} disabled={saving}><Save class="size-4"/> Kaydet ve kapat</button>
      </div>
    </div>
  </div>
{/if}

<Toasts bottom={dirty ? 'bottom-20' : 'bottom-4'}/>
