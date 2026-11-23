<script>
  // The tools the AI called while answering, as compact chips; click one to
  // see what it was asked and what it returned.
  import Calculator from '@lucide/svelte/icons/calculator';
  import CircleX from '@lucide/svelte/icons/circle-x';
  import Clipboard from '@lucide/svelte/icons/clipboard';
  import Clock from '@lucide/svelte/icons/clock';
  import CloudSun from '@lucide/svelte/icons/cloud-sun';
  import ExternalLink from '@lucide/svelte/icons/external-link';
  import Globe from '@lucide/svelte/icons/globe';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Search from '@lucide/svelte/icons/search';
  import Wrench from '@lucide/svelte/icons/wrench';

  /** @type {{calls: {call: string, name: string, input: string, output?: string, error?: string, state: string}[]}} */
  let {calls} = $props();
  let open = $state('');

  const tools = {
    saat: ['Saat ve tarih', Clock], hesapla: ['Hesap makinesi', Calculator], transkript_ara: ['Transkriptte arandı', Search],
    hava_durumu: ['Hava durumu', CloudSun], web_sayfasi_oku: ['Web sayfası okundu', Globe],
    panoya_kopyala: ['Panoya kopyalandı', Clipboard], tarayicida_ac: ['Tarayıcıda açıldı', ExternalLink],
  };

  /** The main argument, to show next to the tool's name. */
  function arg(input) {
    try {
      const v = Object.values(JSON.parse(input || '{}')).find((x) => typeof x === 'string' && x.trim());
      return v ? (v.length > 40 ? v.slice(0, 40) + '…' : v) : '';
    } catch {
      return '';
    }
  }

  const pretty = (s) => {
    try {
      return JSON.stringify(JSON.parse(s), null, 2);
    } catch {
      return s;
    }
  };
</script>

<div class="mb-2 flex flex-wrap gap-1.5" data-search-skip>
  {#each calls as c (c.call)}
    {@const [label, Icon] = tools[c.name] ?? [c.name, Wrench]}
    {@const a = arg(c.input)}
    <button class="inline-flex max-w-full items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs transition
                   {c.state === 'error' ? 'border-red-200 text-red-700 dark:border-red-500/30 dark:text-red-400'
                                        : 'border-zinc-200 text-zinc-600 hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-300 dark:hover:bg-zinc-800'}"
            onclick={() => (open = open === c.call ? '' : c.call)} aria-expanded={open === c.call} title="Ayrıntıları göster">
      {#if c.state === 'running'}<LoaderCircle class="size-3.5 shrink-0 animate-spin"/>{:else if c.state === 'error'}<CircleX class="size-3.5 shrink-0"/>{:else}<Icon class="size-3.5 shrink-0"/>{/if}
      <span class="shrink-0 font-medium">{label}</span>
      {#if a}<span class="truncate text-zinc-500 dark:text-zinc-400">· {a}</span>{/if}
    </button>
  {/each}
</div>
{#each calls.filter((c) => c.call === open) as c (c.call)}
  <div class="mb-3 overflow-hidden rounded-lg border border-zinc-200 text-xs dark:border-zinc-700" data-search-skip>
    <div class="border-b border-zinc-200 bg-zinc-50 px-3 py-1.5 font-medium text-zinc-500 dark:border-zinc-700 dark:bg-zinc-800/60 dark:text-zinc-400">
      Araç: <span class="font-mono">{c.name}</span>
    </div>
    <pre class="selectable max-h-24 overflow-auto px-3 py-2 font-mono whitespace-pre-wrap break-words text-zinc-600 dark:text-zinc-300">{pretty(c.input) || '{}'}</pre>
    <div class="border-t border-zinc-200 px-3 py-1.5 font-medium text-zinc-500 dark:border-zinc-700 dark:text-zinc-400">{c.error ? 'Hata' : 'Sonuç'}</div>
    <pre class="selectable max-h-48 overflow-auto px-3 py-2 font-mono whitespace-pre-wrap break-words {c.error ? 'text-red-600 dark:text-red-400' : 'text-zinc-600 dark:text-zinc-300'}">{c.error || c.output || (c.state === 'running' ? 'Çalışıyor…' : '(boş)')}</pre>
  </div>
{/each}
