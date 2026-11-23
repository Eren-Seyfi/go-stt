<script>
  import {onMount} from 'svelte';
  import {Browser, Events} from '@wailsio/runtime';
  import {AIService} from '../../../bindings/changeme/backend';
  import Check from '@lucide/svelte/icons/check';
  import ExternalLink from '@lucide/svelte/icons/external-link';
  import Eye from '@lucide/svelte/icons/eye';
  import EyeOff from '@lucide/svelte/icons/eye-off';
  import KeyRound from '@lucide/svelte/icons/key-round';
  import ListRestart from '@lucide/svelte/icons/list-restart';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Plug from '@lucide/svelte/icons/plug';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import Trash from '@lucide/svelte/icons/trash';
  import Section from '../ui/Section.svelte';
  import LocalModelSettings from './LocalModelSettings.svelte';
  import Toggle from '../ui/Toggle.svelte';
  import Slider from '../ui/Slider.svelte';
  import {notify, errMsg} from '../ui/toast.svelte.js';
  import {unsaved, registerSaver} from '../settings.svelte.js';
  import {fmtTps, fmtMs} from '../speed.js';

  let providers = $state([]);
  let cfg = $state(null); // editable copy of the AI config
  let keys = $state({}); // per-provider key status (never the key itself)
  let configPath = $state('');
  let fetched = $state({}); // provider → model IDs fetched from its API
  let fetching = $state(false);
  let keyDraft = $state('');
  let showKey = $state(false);
  let testing = $state(false);
  let testResult = $state(null); // {ok, text, stats?}
  let modelStats = $state({});
  let toolList = $state([]); // [{name, label, description, internet}] // "<provider>:<model>" → {replies, avgTokensPerSec, avgFirstTokenMs, bench}
  let customModel = $state('');
  let savedCfg = $state(''); // JSON of the config as saved

  const provider = $derived(providers.find((p) => p.id === cfg?.provider));
  const currentStats = $derived(provider ? modelStats[`${provider.id}:${cfg.models[provider.id]}`] : null);
  const keyStatus = $derived(keys[cfg?.provider] ?? {});
  const hasKey = $derived(keyStatus.saved || keyStatus.fromEnv);
  const modelOptions = $derived.by(() => {
    if (!provider) return [];
    const set = new Set([...(provider.models ?? []), ...(fetched[provider.id] ?? [])]);
    const current = cfg.models[provider.id];
    if (current) set.add(current);
    return [...set];
  });

  function applyView(v) {
    keys = v.keys;
    configPath = v.path;
    const c = structuredClone(v.config);
    c.models ??= {};
    c.baseURLs ??= {};
    c.llama ??= {gpuLayers: 'auto', context: 8192};
    c.localThinking ??= false;
    c.noTools ??= false;
    c.disabledTools ??= [];
    cfg = c;
    savedCfg = JSON.stringify(c);
  }

  async function saveConfig() {
    const snapshot = $state.snapshot(cfg);
    await AIService.SetConfig(snapshot);
    savedCfg = JSON.stringify(snapshot);
  }

  onMount(() => {
    AIService.ModelStats().then((m) => (modelStats = m ?? {})).catch(() => {});
    AIService.Tools().then((t) => (toolList = t ?? [])).catch(() => {});
    Promise.all([AIService.Providers(), AIService.Config()]).then(([p, v]) => {
      providers = p;
      applyView(v);
    });
    const unregister = registerSaver('ai', {
      save: saveConfig,
      discard: () => { cfg = JSON.parse(savedCfg); },
    });
    // Key changes (and edits from elsewhere) come back as ai:config.
    const offs = [
      Events.On('ai:config', (e) => { keys = e.data.keys; }),
      Events.On('ai:stats', (e) => (modelStats = e.data ?? {})),
      // "Reset to defaults" also resets the AI settings: reload them.
      Events.On('settings:changed', (e) => { if (e.data.reset) AIService.Config().then(applyView); }),
    ];
    return () => { unregister(); offs.forEach((off) => off()); };
  });

  // Edits are saved with the settings window's "Kaydet" button.
  $effect(() => {
    unsaved.ai = !!cfg && JSON.stringify($state.snapshot(cfg)) !== savedCfg;
  });

  function selectProvider(id) {
    cfg.provider = id;
    keyDraft = '';
    testResult = null;
    const p = providers.find((x) => x.id === id);
    if (!cfg.models[id] && p?.models?.length) cfg.models[id] = p.models[0];
  }

  async function saveKey() {
    try {
      await AIService.SetAPIKey(cfg.provider, keyDraft);
      keyDraft = '';
      showKey = false;
      notify('API anahtarı kaydedildi', 'success');
    } catch (e) {
      notify(errMsg(e), 'error');
    }
  }

  async function removeKey() {
    await AIService.SetAPIKey(cfg.provider, '');
    notify('API anahtarı silindi');
  }

  async function fetchModels() {
    fetching = true;
    try {
      const ids = await AIService.FetchModels(cfg.provider);
      fetched[cfg.provider] = ids;
      notify(`${ids.length} model bulundu`, 'success');
      if (!cfg.models[cfg.provider] && ids.length) cfg.models[cfg.provider] = ids[0];
    } catch (e) {
      notify(errMsg(e), 'error');
    } finally {
      fetching = false;
    }
  }

  function useCustomModel() {
    const id = customModel.trim();
    if (!id) return;
    cfg.models[cfg.provider] = id;
    customModel = '';
  }

  async function test() {
    testing = true;
    testResult = null;
    try {
      // The test uses the saved settings, so save the AI edits first.
      if (unsaved.ai) await saveConfig();
      const r = await AIService.Test();
      testResult = {ok: true, text: r.text, stats: r.stats};
    } catch (e) {
      testResult = {ok: false, text: errMsg(e)};
    } finally {
      testing = false;
    }
  }

  async function resetPrompt() {
    cfg.systemPrompt = await AIService.DefaultSystemPrompt();
  }

  const efforts = [
    ['low', 'Hızlı', 'Kısa yanıtlar, en düşük gecikme. Sohbet için önerilir.'],
    ['medium', 'Dengeli', 'Biraz daha düşünür; daha karmaşık sorular için.'],
    ['high', 'Derin', 'En kapsamlı yanıt; en yavaş ve en pahalı.'],
  ];
</script>

{#if cfg}
  <div class="space-y-4">
    <Section title="Yapay zekâ yanıtları"
             description="Konuştuklarınız yazıya çevrildikten sonra arka planda seçtiğiniz yapay zekâya gönderilir; yanıtı ana pencerede cümlenin altında görünür.">
      <Toggle label="Konuşulanları yapay zekâya sor" bind:checked={cfg.enabled}/>
      {#if cfg.enabled}
        <Toggle label="Her cümle bitince otomatik gönder" bind:checked={cfg.auto}
                description={cfg.auto ? 'Cümle kesinleşir kesinleşmez gönderilir.' : 'Sadece satırdaki “Yapay zekâya sor” butonuyla gönderilir.'}/>
      {/if}
    </Section>

    <Section title="Sağlayıcı">
      <div class="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-label="Yapay zekâ sağlayıcısı">
        {#each providers as p (p.id)}
          {@const active = p.id === cfg.provider}
          {@const ks = keys[p.id] ?? {}}
          <button class="rounded-xl border p-3 text-left transition
                         {active ? 'border-accent-500 bg-accent-50/60 ring-1 ring-accent-500 dark:bg-accent-500/10'
                                 : 'border-zinc-200 hover:border-zinc-300 dark:border-zinc-800 dark:hover:border-zinc-700'}"
                  role="radio" aria-checked={active} onclick={() => selectProvider(p.id)}>
            <div class="flex items-center justify-between gap-2">
              <span class="font-medium">{p.name}</span>
              {#if !p.needsKey}
                <span class="rounded-full bg-zinc-100 px-2 py-0.5 text-[11px] text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400">Anahtarsız</span>
              {:else if ks.saved || ks.fromEnv}
                <span class="inline-flex items-center gap-1 rounded-full bg-emerald-100 px-2 py-0.5 text-[11px] text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300">
                  <Check class="size-3"/> Anahtar var
                </span>
              {/if}
            </div>
            <p class="mt-1 text-xs text-zinc-500 dark:text-zinc-400">{p.description}</p>
          </button>
        {/each}
      </div>
    </Section>

    {#if provider}
      <Section title="{provider.name} ayarları">
        {#if provider.needsKey}
          <div>
            <div class="mb-1.5 flex flex-wrap items-center justify-between gap-2 text-sm">
              <span class="flex items-center gap-1.5 font-medium"><KeyRound class="size-4"/> API anahtarı</span>
              {#if provider.keyURL}
                <button class="inline-flex items-center gap-1 text-xs text-accent-600 hover:underline dark:text-accent-400"
                        onclick={() => Browser.OpenURL(provider.keyURL)}>Anahtar al <ExternalLink class="size-3"/></button>
              {/if}
            </div>
            <p class="mb-2 text-xs text-zinc-500 dark:text-zinc-400">
              {#if keyStatus.saved}
                Kayıtlı anahtar: <span class="font-mono">••••{keyStatus.last4}</span>
              {:else if keyStatus.fromEnv}
                Ortam değişkeninden okunuyor: <span class="font-mono">{provider.keyEnv}</span>
              {:else}
                Anahtar yok. Buraya girin ya da <span class="font-mono">{provider.keyEnv}</span> ortam değişkenini tanımlayın.
              {/if}
            </p>
            <form class="flex gap-2" onsubmit={(e) => { e.preventDefault(); saveKey(); }}>
              <div class="relative min-w-0 flex-1">
                <input class="ui-input pr-10 font-mono" type={showKey ? 'text' : 'password'} bind:value={keyDraft}
                       autocomplete="off" spellcheck="false" aria-label="API anahtarı"
                       placeholder={keyStatus.saved ? 'Yeni anahtar ile değiştir' : 'Anahtarı yapıştırın'}/>
                <button type="button" class="absolute top-1/2 right-2 -translate-y-1/2 p-1 text-zinc-500 hover:text-zinc-800 dark:hover:text-zinc-200"
                        onclick={() => (showKey = !showKey)} aria-label={showKey ? 'Anahtarı gizle' : 'Anahtarı göster'}>
                  {#if showKey}<EyeOff class="size-4"/>{:else}<Eye class="size-4"/>{/if}
                </button>
              </div>
              <button class="btn-primary shrink-0" type="submit" disabled={!keyDraft.trim()}>Kaydet</button>
              {#if keyStatus.saved}
                <button class="btn-danger shrink-0 px-2.5" type="button" onclick={removeKey} title="Kayıtlı anahtarı sil" aria-label="Kayıtlı anahtarı sil"><Trash class="size-4"/></button>
              {/if}
            </form>
            <p class="mt-1.5 text-[11px] text-zinc-500 dark:text-zinc-400">
              Anahtar yalnızca bu bilgisayarda, sadece sizin okuyabileceğiniz yerel veritabanında saklanır: <span class="font-mono break-all">{configPath}</span>
            </p>
          </div>
        {/if}

        {#if provider.id === 'llamacpp'}
          <LocalModelSettings {cfg}/>
        {:else}
        {#if provider.needsBaseURL || provider.id === 'ollama'}
          <div>
            <label class="mb-1.5 block text-sm font-medium" for="base-url">Sunucu adresi</label>
            <input id="base-url" class="ui-input font-mono" bind:value={cfg.baseURLs[provider.id]}
                   placeholder={provider.defaultBaseURL || 'http://localhost:1234/v1'} spellcheck="false"/>
          </div>
        {/if}

        <div>
          <div class="mb-1.5 flex items-center justify-between gap-2">
            <span class="text-sm font-medium">Model</span>
            <button class="btn-ghost px-2 py-1 text-xs" onclick={fetchModels}
                    disabled={fetching || (provider.needsKey && !hasKey) || (provider.needsBaseURL && !cfg.baseURLs[provider.id])}
                    title="Sağlayıcıdaki modelleri listele">
              {#if fetching}<LoaderCircle class="size-3.5 animate-spin"/>{:else}<RefreshCw class="size-3.5"/>{/if} Listeyi getir
            </button>
          </div>
          {#if modelOptions.length}
            <select class="ui-select font-mono" bind:value={cfg.models[provider.id]} aria-label="Model">
              {#each modelOptions as m (m)}<option value={m}>{m}</option>{/each}
            </select>
          {:else}
            <p class="rounded-lg border border-dashed border-zinc-300 p-3 text-sm text-zinc-500 dark:border-zinc-700">
              {provider.needsKey && !hasKey ? 'Modelleri listelemek için önce API anahtarını kaydedin.' : '“Listeyi getir” ile modelleri yükleyin ya da aşağıya model adını yazın.'}
            </p>
          {/if}
          <form class="mt-2 flex gap-2" onsubmit={(e) => { e.preventDefault(); useCustomModel(); }}>
            <input class="ui-input font-mono" bind:value={customModel} placeholder="ya da model adını elle yazın" spellcheck="false" aria-label="Model adı"/>
            <button class="btn-outline shrink-0" type="submit" disabled={!customModel.trim()}>Kullan</button>
          </form>
        </div>
        {/if}

        {#if provider.supportsEffort}
          <div>
            <span class="mb-1.5 block text-sm font-medium">Yanıt derinliği</span>
            <div class="grid gap-2 sm:grid-cols-3" role="radiogroup" aria-label="Yanıt derinliği">
              {#each efforts as [id, label, desc] (id)}
                <label class="flex cursor-pointer gap-2 rounded-lg border p-2.5 text-sm transition
                              {cfg.effort === id ? 'border-accent-500 bg-accent-50/60 dark:bg-accent-500/10' : 'border-zinc-200 dark:border-zinc-800'}">
                  <input type="radio" class="ui-radio mt-0.5" name="effort" value={id} bind:group={cfg.effort}/>
                  <span><span class="font-medium">{label}</span><span class="block text-xs text-zinc-500 dark:text-zinc-400">{desc}</span></span>
                </label>
              {/each}
            </div>
          </div>
        {/if}

        <div class="flex flex-wrap items-center gap-3">
          <button class="btn-outline" onclick={test} disabled={testing || !cfg.models[provider.id]}>
            {#if testing}<LoaderCircle class="size-4 animate-spin"/>{:else}<Plug class="size-4"/>{/if} Bağlantıyı dene
          </button>
          {#if testResult}
            <p class="min-w-0 flex-1 text-sm {testResult.ok ? 'text-emerald-700 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'}">
              {testResult.ok ? '✓ ' : '✕ '}{testResult.text}
            </p>
          {/if}
        </div>
        {#if testResult?.stats}
          {@const st = testResult.stats}
          <dl class="grid grid-cols-2 gap-2 sm:grid-cols-4">
            {#each [['Üretim hızı', `${fmtTps(st.tokensPerSec)} token/sn`], ['İlk kelime', fmtMs(st.firstTokenMs)],
                    ['Üretilen', `${st.outputTokens}${st.approximate ? ' ~' : ''} token`], ['Toplam süre', fmtMs(st.totalMs)]] as [k, v] (k)}
              <div class="rounded-lg bg-zinc-100 px-3 py-2 dark:bg-zinc-800/70">
                <dt class="text-[11px] text-zinc-500 dark:text-zinc-400">{k}</dt>
                <dd class="text-sm font-semibold tabular-nums">{v}</dd>
              </div>
            {/each}
          </dl>
          <p class="-mt-2 text-[11px] text-zinc-500 dark:text-zinc-400">{st.exact ? 'Model sunucusunun kendi ölçümü (kesin).' : 'Uygulama tarafından ölçüldü; ağ gecikmesi dahildir.'}</p>
        {/if}
        {#if currentStats?.replies}
          <p class="text-xs text-zinc-500 dark:text-zinc-400">
            Bu modelin ortalaması: <span class="font-medium text-zinc-700 tabular-nums dark:text-zinc-300">{fmtTps(currentStats.avgTokensPerSec)} token/sn</span>,
            ilk kelime {fmtMs(currentStats.avgFirstTokenMs)} · {currentStats.replies} yanıt üzerinden
          </p>
        {/if}
      </Section>
    {/if}

    <Section title="Araçlar" description="Yapay zekâ yanıt verirken bu araçları kendisi kullanabilir: saati sorabilir, hesap yapabilir, hava durumuna bakabilir…">
      <Toggle label="Yapay zekâ araç kullanabilsin"
              bind:checked={() => !cfg.noTools, (v) => (cfg.noTools = !v)}
              description="Çağrılan araçlar yanıtın üstünde gösterilir; tıklayınca ne sorulduğu ve ne döndüğü görünür. Yalnızca araç kullanımını destekleyen modellerde çalışır (yerel modellerde “Araç kullanımı” etiketi olanlar)."/>
      {#if !cfg.noTools}
        <div class="grid gap-2 sm:grid-cols-2">
          {#each toolList as t (t.name)}
            {@const on = !cfg.disabledTools.includes(t.name)}
            <label class="flex cursor-pointer gap-2.5 rounded-lg border p-2.5 text-sm transition
                          {on ? 'border-zinc-200 dark:border-zinc-700' : 'border-zinc-200 opacity-60 dark:border-zinc-800'}">
              <input type="checkbox" class="ui-checkbox mt-0.5" checked={on}
                     onchange={(e) => (cfg.disabledTools = e.currentTarget.checked
                       ? cfg.disabledTools.filter((n) => n !== t.name)
                       : [...cfg.disabledTools, t.name])}/>
              <span class="min-w-0">
                <span class="flex flex-wrap items-center gap-1.5 font-medium">{t.label}
                  {#if t.internet}<span class="rounded-full bg-amber-100 px-1.5 py-px text-[10px] font-medium text-amber-800 dark:bg-amber-500/15 dark:text-amber-300" title="Bu araç internete bağlanır">İnternet</span>{/if}
                </span>
                <span class="block text-xs text-zinc-500 dark:text-zinc-400">{t.description}</span>
              </span>
            </label>
          {/each}
        </div>
        <p class="text-xs text-zinc-500 dark:text-zinc-400">“İnternet” etiketli araçlar sorgunuzu (ör. şehir adı, sayfa adresi) ilgili siteye gönderir. Web sayfası okuma, bu bilgisayardaki ve yerel ağdaki adreslere erişemez.</p>
      {/if}
    </Section>

    <Section title="Talimatlar" description="Yapay zekânın nasıl yanıt vereceğini anlatan sistem talimatı.">
      <textarea class="ui-textarea resize-y text-sm" rows="5" bind:value={cfg.systemPrompt} aria-label="Sistem talimatı"></textarea>
      <button class="btn-ghost -mt-2 text-xs" onclick={resetPrompt}><ListRestart class="size-4"/> Varsayılana dön</button>
    </Section>

    <Section title="Gelişmiş">
      <Slider label="Sohbet hafızası" bind:value={cfg.memory} min={0} max={20}
              display={cfg.memory ? `Son ${cfg.memory} soru-yanıt` : 'Kapalı'}
              description="Önceki konuşmaların kaçı bağlam olarak gönderilsin. Kapalıyken her cümle bağımsız sorulur."/>
      <Slider label="En uzun yanıt" bind:value={cfg.maxTokens} min={256} max={16000} step={256}
              display="{cfg.maxTokens} token" description="Yanıtın en fazla uzunluğu. Uzun yanıtlar daha pahalıdır."/>
    </Section>
  </div>
{:else}
  <p class="flex items-center gap-2 text-sm text-zinc-500"><LoaderCircle class="size-4 animate-spin"/> Yükleniyor…</p>
{/if}
