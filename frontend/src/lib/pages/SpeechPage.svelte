<script>
  import Cpu from '@lucide/svelte/icons/cpu';
  import Gpu from '@lucide/svelte/icons/gpu';
  import Info from '@lucide/svelte/icons/info';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import Section from '../ui/Section.svelte';
  import Toggle from '../ui/Toggle.svelte';
  import Slider from '../ui/Slider.svelte';
  import {settings, langName} from '../settings.svelte.js';

  /** @type {{models: any[], system: any, mics: any[], defaultThreads: number, listening: boolean,
   *          onrefreshmics: () => void, goto: (page: string) => void}} */
  let {models, system, mics, defaultThreads, listening, onrefreshmics, goto} = $props();

  const installed = $derived(models.filter((m) => m.installed && m.engine !== 'vad'));
  const vadModels = $derived(models.filter((m) => m.engine === 'vad'));
  const model = $derived(models.find((m) => m.id === settings.modelId));
  const fixedLang = $derived(model?.language || '');
  const gpuAvailable = $derived(system.gpus.length > 0);
  const slowOnCpu = $derived(!!model && settings.device === 'cpu' && model.speed <= 3);
  const usingBluetooth = $derived((settings.microphone || mics.find((m) => m.default)?.name || '').startsWith('bluez'));
</script>

<div class="space-y-4">
  {#if listening}
    <p class="flex gap-2 rounded-lg bg-sky-50 p-3 text-sm text-sky-800 dark:bg-sky-500/10 dark:text-sky-300">
      <Info class="mt-0.5 size-4 shrink-0"/> Dinleme sürerken bu ayarlar kilitlidir; değiştirmek için önce dinlemeyi durdurun.
    </p>
  {/if}

  <Section title="Tanıma modeli" description="Konuşmayı yazıya çeviren Whisper modeli.">
    {#if installed.length}
      <select class="ui-select" bind:value={settings.modelId} disabled={listening} aria-label="Model">
        {#each installed as m (m.id)}<option value={m.id}>{m.name}</option>{/each}
      </select>
      {#if model}<p class="-mt-3 text-xs text-zinc-500 dark:text-zinc-400">{model.description}</p>{/if}
    {:else}
      <p class="rounded-lg border border-dashed border-zinc-300 p-3 text-sm text-zinc-500 dark:border-zinc-700">
        Yüklü model yok. Başlamak için bir model indirin.
      </p>
    {/if}
    <button class="btn-outline w-full sm:w-auto" onclick={() => goto('models')}>Modelleri yönet <ChevronRight class="size-4"/></button>
  </Section>

  <Section title="İşlem birimi" description="Model CPU'da ya da ekran kartında (GPU) çalışabilir.">
    <div class="grid grid-cols-2 gap-1 rounded-lg bg-zinc-100 p-1 sm:max-w-sm dark:bg-zinc-800">
      {#each [['cpu', 'CPU', Cpu], ['gpu', 'GPU', Gpu]] as [id, label, Icn] (id)}
        <button class="flex items-center justify-center gap-1.5 rounded-md py-1.5 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-40
                       {settings.device === id ? 'bg-white text-zinc-900 shadow-sm dark:bg-zinc-950 dark:text-white'
                                               : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100'}"
                disabled={listening || (id === 'gpu' && !gpuAvailable)} onclick={() => (settings.device = id)}>
          <Icn class="size-4"/> {label}
        </button>
      {/each}
    </div>
    <p class="-mt-3 text-xs text-zinc-500 dark:text-zinc-400">
      {settings.device === 'gpu' && gpuAvailable
        ? `${system.gpus[0].name} · ${(system.gpus[0].memoryMB / 1024).toFixed(1)} GB`
        : system.cpuName || 'CPU'}
    </p>
    {#if !gpuAvailable && system.gpuNote}
      <p class="flex gap-1.5 rounded-lg bg-amber-50 p-2.5 text-xs text-amber-800 dark:bg-amber-500/10 dark:text-amber-300">
        <TriangleAlert class="mt-px size-3.5 shrink-0"/> GPU yok: {system.gpuNote}
      </p>
    {/if}
    {#if slowOnCpu}
      <p class="flex gap-1.5 rounded-lg bg-sky-50 p-2.5 text-xs text-sky-800 dark:bg-sky-500/10 dark:text-sky-300">
        <Info class="mt-px size-3.5 shrink-0"/> Bu model CPU'da yavaş kalabilir. Anlık yazı için GPU ya da Whisper Small önerilir.
      </p>
    {/if}
    <div class:opacity-50={settings.device === 'gpu'}>
      <Slider label="CPU iş parçacığı" bind:value={settings.threads} min={0} max={system.cpuThreads || 16}
              display={settings.threads ? String(settings.threads) : `Otomatik (${defaultThreads})`} disabled={listening}/>
    </div>
  </Section>

  <Section title="Dil">
    {#if fixedLang}
      <select class="ui-select" disabled aria-label="Dil"><option>{langName(fixedLang)}</option></select>
      <p class="-mt-3 text-xs text-zinc-500">Seçili model yalnızca {langName(fixedLang)} tanır.</p>
    {:else}
      <select class="ui-select" bind:value={settings.language} disabled={listening} aria-label="Dil">
        <option value="auto">Otomatik algıla</option>
        {#each settings.languages as code (code)}<option value={code}>{langName(code)}</option>{/each}
      </select>
    {/if}
    <button class="btn-outline w-full sm:w-auto" onclick={() => goto('languages')}>Dilleri yönet <ChevronRight class="size-4"/></button>
    <Toggle label="İngilizceye çevir" bind:checked={settings.translate} disabled={listening || !!fixedLang}
            description="Konuşmayı hangi dilde yaparsanız yapın, metin İngilizce yazılır."/>
  </Section>

  <Section title="Mikrofon">
    <div class="flex gap-2">
      <select class="ui-select" bind:value={settings.microphone} disabled={listening} aria-label="Mikrofon">
        <option value="">Sistem varsayılanı</option>
        {#each mics as m (m.name)}<option value={m.name}>{m.description}{m.default ? ' (varsayılan)' : ''}</option>{/each}
      </select>
      <button class="btn-outline shrink-0 px-3" onclick={onrefreshmics} title="Listeyi yenile" aria-label="Mikrofon listesini yenile">
        <RefreshCw class="size-4"/>
      </button>
    </div>
    {#if usingBluetooth}
      <p class="-mt-3 text-xs text-zinc-500">Bluetooth mikrofonlar sesi sıkıştırır; dahili mikrofon daha iyi sonuç verebilir.</p>
    {/if}
  </Section>

  <Section title="Konuşma algılama" description="Ne zaman konuşulduğunu ve cümlenin ne zaman bittiğini belirler.">
    <Toggle label="Silero VAD" bind:checked={settings.vad} disabled={listening}
            description="Konuşmayı gürültüden yapay zekâ ile ayırır. Kapalıyken ses yüksekliğine bakılır."/>
    {#if settings.vad && vadModels.length > 1}
      <fieldset class="-mt-2 space-y-1.5" disabled={listening}>
        <legend class="sr-only">VAD modeli</legend>
        {#each vadModels as m (m.id)}
          <label class="flex cursor-pointer items-center gap-2 text-sm">
            <input type="radio" class="ui-radio" name="vad-model" value={m.id} bind:group={settings.vadModel}/>
            <span>{m.name}</span>
            {#if !m.installed}<span class="text-xs text-zinc-500">· ilk kullanımda iner</span>{/if}
          </label>
        {/each}
      </fieldset>
    {/if}
    <Slider label="Hassasiyet" bind:value={settings.vadSens} min={0} max={100} step={5} disabled={listening}
            display="{settings.vadSens < 35 ? 'Düşük' : settings.vadSens > 65 ? 'Yüksek' : 'Orta'} · {settings.vadSens}"
            minLabel="Sadece net konuşma" maxLabel="Fısıltı da"
            description="Gürültü yazıya dökülüyorsa düşürün; komutlar ya da kısık ses kaçırılıyorsa yükseltin. Ana penceredeki “Konuşma” göstergesiyle deneyebilirsiniz."/>
    <Slider label="En kısa konuşma" bind:value={settings.minSpeechMs} min={32} max={512} step={32} disabled={listening}
            display="{settings.minSpeechMs} ms" description="Bundan kısa sesler (tıklama, öksürük) konuşma sayılmaz."/>
    <Slider label="Cümle sonu sessizliği" bind:value={settings.silenceMs} min={400} max={2500} step={100} disabled={listening}
            display="{(settings.silenceMs / 1000).toFixed(1)} sn" description="Bu kadar sustuğunuzda cümle kesinleşir (ve yapay zekâya gönderilir)."/>
  </Section>

  <Section title="İpucu kelimeler" description="Özel isimleri ve terimleri yazın; Whisper bunları doğru yazmaya çalışır.">
    <textarea class="ui-textarea resize-y" rows="3" bind:value={settings.prompt} disabled={listening}
              placeholder="ör. Ada, Wails, Svelte, Eren" aria-label="İpucu kelimeler"></textarea>
  </Section>
</div>
