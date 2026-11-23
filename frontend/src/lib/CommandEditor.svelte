<script>
  import {STTService} from '../../bindings/changeme/backend';
  import Ear from '@lucide/svelte/icons/ear';
  import MicOff from '@lucide/svelte/icons/mic-off';
  import Pause from '@lucide/svelte/icons/pause';
  import Plus from '@lucide/svelte/icons/plus';
  import RotateCcw from '@lucide/svelte/icons/rotate-ccw';
  import FlaskConical from '@lucide/svelte/icons/flask-conical';
  import X from '@lucide/svelte/icons/x';
  import {settings, defaultCommands} from './settings.svelte.js';

  /** @type {{listening: boolean, notify: (msg: string, kind?: string) => void}} */
  let {listening, notify} = $props();

  const groups = [
    {key: 'wake', title: 'Başlatma komutları', icon: Ear, tone: 'emerald',
     help: 'Bekleme modundayken bunlardan biri duyulunca yazmaya başlar. Komuttan sonra söylenenler yazılır.',
     placeholder: 'ör. hey yapay zeka'},
    {key: 'sleep', title: 'Bekleme komutları', icon: Pause, tone: 'amber',
     help: 'Yazmayı bırakır ama dinlemeye devam eder; tekrar başlatma komutu beklenir. Komuttan önce söylenenler korunur.',
     placeholder: 'ör. tamam yapay zeka'},
    {key: 'stop', title: 'Durdurma komutları', icon: MicOff, tone: 'red',
     help: 'Mikrofonu tamamen kapatır. Uyandırma modu kapalıyken de çalışır.',
     placeholder: 'ör. dinlemeyi kapat'},
  ];

  const tones = {
    emerald: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-500/15 dark:text-emerald-300',
    amber: 'bg-amber-100 text-amber-800 dark:bg-amber-500/15 dark:text-amber-300',
    red: 'bg-red-100 text-red-800 dark:bg-red-500/15 dark:text-red-300',
  };

  let drafts = $state({wake: '', sleep: '', stop: ''});
  let testText = $state('');
  let testResult = $state(null);

  const norm = (s) => s.toLocaleLowerCase('tr').replace(/[^\p{L}\p{N}]+/gu, ' ').trim();

  function add(key) {
    const phrase = drafts[key].trim();
    if (!norm(phrase)) return;
    for (const g of groups) {
      const clash = settings.commands[g.key].find((p) => norm(p) === norm(phrase));
      if (clash) {
        notify(`“${phrase}” zaten ${g.title.toLocaleLowerCase('tr')} arasında`, 'error');
        return;
      }
    }
    settings.commands[key].push(phrase);
    drafts[key] = '';
  }

  function remove(key, i) {
    if (key === 'wake' && settings.commands.wake.length === 1 && settings.wakeWord) {
      notify('Uyandırma modu açıkken en az bir başlatma komutu olmalı', 'error');
      return;
    }
    settings.commands[key].splice(i, 1);
  }

  function reset() {
    settings.commands = structuredClone(defaultCommands);
    notify('Komutlar varsayılana döndü', 'success');
  }

  // Live test using the backend's real matcher.
  $effect(() => {
    const text = testText;
    const cmds = $state.snapshot(settings.commands);
    if (!text.trim()) { testResult = null; return; }
    const t = setTimeout(() => STTService.TestCommand(text, cmds).then((r) => (testResult = r)), 150);
    return () => clearTimeout(t);
  });

  const resultLabel = {wake: 'Başlatma', sleep: 'Bekleme', stop: 'Durdurma'};
</script>

<section class="card flex flex-col" aria-labelledby="ce-title">
    <header class="flex shrink-0 items-start justify-between gap-4 border-b border-zinc-200 p-4 sm:p-5 dark:border-zinc-800">
      <div>
        <h2 id="ce-title" class="text-lg font-semibold">Sesli komutlar</h2>
        <p class="mt-0.5 text-sm text-zinc-500 dark:text-zinc-400">
          Her komut için birden fazla söz ekleyebilirsiniz. Büyük/küçük harf ve noktalama önemsizdir; sözler tam kelime olarak aranır.
        </p>
      </div>
      <div class="flex items-center gap-1">
        <button class="btn-ghost" onclick={reset} title="Varsayılan komutlara dön"><RotateCcw class="size-4"/><span class="hidden sm:inline">Varsayılan</span></button>
      </div>
    </header>

    <div class="min-h-0 flex-1 space-y-6 overflow-y-auto p-4 sm:p-5">
      {#if listening}
        <p class="rounded-lg bg-sky-50 p-2.5 text-xs text-sky-800 dark:bg-sky-500/10 dark:text-sky-300">
          Değişiklikler bir sonraki dinlemede geçerli olur.
        </p>
      {/if}

      {#each groups as g (g.key)}
        <section>
          <h3 class="flex items-center gap-2 font-medium"><g.icon class="size-4"/> {g.title}</h3>
          <p class="mt-0.5 text-sm text-zinc-500 dark:text-zinc-400">{g.help}</p>
          <div class="mt-2.5 flex flex-wrap gap-1.5">
            {#each settings.commands[g.key] as p, i (p)}
              <span class="inline-flex items-center gap-1 rounded-full py-1 pr-1 pl-3 text-sm {tones[g.tone]}">
                {p}
                <button class="rounded-full p-0.5 opacity-60 hover:bg-black/10 hover:opacity-100 dark:hover:bg-white/10"
                        onclick={() => remove(g.key, i)} aria-label="“{p}” sözünü kaldır"><X class="size-3.5"/></button>
              </span>
            {:else}
              <span class="text-sm text-zinc-500 dark:text-zinc-400 italic">Söz yok — bu komut kapalı.</span>
            {/each}
          </div>
          <form class="mt-2.5 flex gap-2" onsubmit={(e) => { e.preventDefault(); add(g.key); }}>
            <input class="ui-input" bind:value={drafts[g.key]} placeholder={g.placeholder} aria-label="{g.title}: yeni söz"/>
            <button class="btn-outline shrink-0" type="submit" disabled={!drafts[g.key].trim()}><Plus class="size-4"/> Ekle</button>
          </form>
        </section>
      {/each}

      <section class="rounded-xl border border-dashed border-zinc-300 p-4 dark:border-zinc-700">
        <h3 class="flex items-center gap-2 font-medium"><FlaskConical class="size-4"/> Dene</h3>
        <p class="mt-0.5 text-sm text-zinc-500 dark:text-zinc-400">
          Whisper'ın yazabileceği bir cümle girin; hangi komutun tetikleneceğini görün.
        </p>
        <input class="ui-input mt-2.5" bind:value={testText} placeholder="ör. Hey yapay zekâ, alışveriş listesi yap"/>
        {#if testResult}
          <div class="mt-2.5 text-sm">
            {#if testResult.kind}
              <span class="rounded-full px-2.5 py-0.5 font-medium {tones[groups.find((g) => g.key === testResult.kind).tone]}">
                {resultLabel[testResult.kind]}: “{testResult.phrase}”
              </span>
              <dl class="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-zinc-600 dark:text-zinc-400">
                {#if testResult.before}<dt class="text-zinc-500 dark:text-zinc-400">Önce</dt><dd>“{testResult.before}” <span class="text-xs">(yazılır)</span></dd>{/if}
                {#if testResult.after}<dt class="text-zinc-500 dark:text-zinc-400">Sonra</dt><dd>“{testResult.after}” <span class="text-xs">{testResult.kind === 'wake' ? '(yazılır)' : '(yok sayılır)'}</span></dd>{/if}
              </dl>
            {:else}
              <span class="text-zinc-500">Hiçbir komut tetiklenmez.</span>
            {/if}
          </div>
        {/if}
      </section>
    </div>
</section>
