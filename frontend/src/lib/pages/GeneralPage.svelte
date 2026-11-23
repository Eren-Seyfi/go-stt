<script>
  import {onMount} from 'svelte';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Monitor from '@lucide/svelte/icons/monitor';
  import RotateCcw from '@lucide/svelte/icons/rotate-ccw';
  import Moon from '@lucide/svelte/icons/moon';
  import Sun from '@lucide/svelte/icons/sun';
  import Section from '../ui/Section.svelte';
  import Toggle from '../ui/Toggle.svelte';
  import Slider from '../ui/Slider.svelte';
  import {settings, resetSettings} from '../settings.svelte.js';
  import {SettingsService} from '../../../bindings/changeme/backend';
  import {notify, errMsg} from '../ui/toast.svelte.js';

  let dbPath = $state('');
  let confirming = $state(false);
  let resetting = $state(false);
  let confirmTimer;
  onMount(() => {
    SettingsService.Path().then((p) => (dbPath = p));
    return () => clearTimeout(confirmTimer);
  });

  async function reset() {
    if (!confirming) {
      confirming = true;
      clearTimeout(confirmTimer);
      confirmTimer = setTimeout(() => (confirming = false), 4000);
      return;
    }
    clearTimeout(confirmTimer);
    confirming = false;
    resetting = true;
    try {
      await resetSettings();
      notify('Ayarlar varsayılana döndürüldü', 'success');
    } catch (e) {
      notify('Sıfırlanamadı: ' + errMsg(e), 'error');
    } finally {
      resetting = false;
    }
  }

  const themes = [['system', 'Sistem', Monitor], ['light', 'Açık', Sun], ['dark', 'Koyu', Moon]];
  const shortcuts = [
    ['Boşluk', 'Dinlemeyi başlat / durdur'], ['Ctrl+,', 'Ayarlar'], ['Ctrl+S', 'Transkripti kaydet'],
    ['Ctrl+Shift+C', 'Tümünü kopyala'], ['Ctrl+F', 'Transkriptte ara'], ['Ctrl+Z', 'Temizlemeyi geri al'], ['Ctrl+M', 'Modeller'],
    ['Ctrl+L', 'Diller'], ['Ctrl+K', 'Sesli komutlar'], ['Ctrl+I', 'Yapay zekâ'],
  ];
</script>

<div class="space-y-4">
  <Section title="Tema">
    <div class="grid grid-cols-3 gap-1 rounded-lg bg-zinc-100 p-1 sm:max-w-md dark:bg-zinc-800" role="radiogroup" aria-label="Tema">
      {#each themes as [id, label, Icn] (id)}
        <button class="flex items-center justify-center gap-1.5 rounded-md py-1.5 text-sm font-medium transition
                       {settings.theme === id ? 'bg-white text-zinc-900 shadow-sm dark:bg-zinc-950 dark:text-white'
                                              : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100'}"
                role="radio" aria-checked={settings.theme === id} onclick={() => (settings.theme = id)}>
          <Icn class="size-4"/> {label}
        </button>
      {/each}
    </div>
  </Section>

  <Section title="Transkript görünümü">
    <Slider label="Yazı boyutu" bind:value={settings.fontSize} min={14} max={40} step={2} display="{settings.fontSize} px"/>
    <Toggle label="Zaman damgalarını göster" bind:checked={settings.showTimestamps}/>
    <Toggle label="Yeni metne otomatik kaydır" bind:checked={settings.autoScroll}/>
  </Section>

  <Section title="Kayıt ve sıfırlama"
           description="Ayarlar penceresinde yaptığınız değişiklikler Kaydet düğmesine (veya Ctrl+S) basınca kaydedilir.">
    {#if dbPath}
      <p class="text-xs text-zinc-500 dark:text-zinc-400">Veritabanı: <span class="font-mono break-all">{dbPath}</span></p>
    {/if}
    <div class="flex flex-wrap items-center justify-between gap-3 border-t border-zinc-200 pt-4 dark:border-zinc-800">
      <div class="min-w-0 flex-1">
        <p class="text-sm font-medium">Varsayılan ayarlara dön</p>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">Tüm ayarlar (yapay zekâ ayarları ve sesli komutlar dahil) ilk hâline döner. API anahtarları ve indirilen modeller silinmez.</p>
      </div>
      <button class="shrink-0 {confirming ? 'btn bg-red-600 text-white shadow-sm hover:bg-red-700' : 'btn-danger border border-red-200 dark:border-red-900'}"
              onclick={reset} disabled={resetting}>
        {#if resetting}<LoaderCircle class="size-4 animate-spin"/>{:else}<RotateCcw class="size-4"/>{/if}
        {confirming ? 'Emin misiniz? Sıfırla' : 'Sıfırla'}
      </button>
    </div>
  </Section>

  <Section title="Klavye kısayolları" description="Ana pencerede geçerlidir.">
    <dl class="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
      {#each shortcuts as [k, d] (k)}
        <div class="flex items-center justify-between gap-3"><dt class="text-zinc-600 dark:text-zinc-300">{d}</dt><dd><span class="kbd">{k}</span></dd></div>
      {/each}
    </dl>
  </Section>
</div>
