<script>
  // Capability badges for a local model (from its GGUF metadata): vision,
  // audio, tool use, reasoning; plus size / architecture / quantization / context.
  import AudioLines from '@lucide/svelte/icons/audio-lines';
  import Brain from '@lucide/svelte/icons/brain';
  import Eye from '@lucide/svelte/icons/eye';
  import Wrench from '@lucide/svelte/icons/wrench';

  /** @type {{caps?: any, vision?: boolean, details?: boolean}} */
  let {caps = null, vision = false, details = false} = $props();

  const fmtCtx = (n) => (n >= 1024 ? `${Math.round(n / 1024)}K` : String(n));

  const badges = $derived.by(() => {
    const c = caps ?? {};
    const out = [];
    if (c.vision || vision) out.push(['Görüntü', Eye, 'bg-sky-100 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300', 'Resimleri görebilir (sohbete resim ekleyebilirsiniz).']);
    if (c.audio) out.push(['Ses', AudioLines, 'bg-pink-100 text-pink-700 dark:bg-pink-500/15 dark:text-pink-300', 'Modelin ses kodlayıcısı var: ses girdisini anlayabilir.']);
    if (c.tools) out.push(['Araç kullanımı', Wrench, 'bg-orange-100 text-orange-700 dark:bg-orange-500/15 dark:text-orange-300', 'Araç / fonksiyon çağırmayı destekler (tool use).']);
    if (c.reasoning) out.push([c.reasoning === 'always' ? 'Akıl yürütme' : 'Akıl yürütme', Brain,
      'bg-violet-100 text-violet-700 dark:bg-violet-500/15 dark:text-violet-300',
      c.reasoning === 'always' ? 'Yanıtlamadan önce her zaman düşünür; daha doğru ama daha yavaş.'
                               : 'Düşünme modu var (açılıp kapatılabilir).']);
    return out;
  });

  const meta = $derived(caps ? [caps.params, caps.arch, caps.quant, caps.context ? `${fmtCtx(caps.context)} bağlam` : ''].filter(Boolean) : []);
</script>

{#each badges as [label, Icon, cls, title] (label)}
  <span class="inline-flex items-center gap-1 rounded-full px-1.5 py-px text-[10px] font-medium {cls}" {title}>
    <Icon class="size-2.5"/>{label}{#if label === 'Akıl yürütme' && caps?.reasoning === 'always'}<span class="opacity-70">· her zaman</span>{/if}
  </span>
{/each}
{#if details && meta.length}
  <span class="inline-flex items-center rounded-full bg-zinc-100 px-1.5 py-px font-mono text-[10px] text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400"
        title="Parametre boyutu · mimari · sıkıştırma · en uzun bağlam">{meta.join(' · ')}</span>
{/if}
