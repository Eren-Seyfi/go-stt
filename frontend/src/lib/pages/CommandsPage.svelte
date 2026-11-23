<script>
  import Section from '../ui/Section.svelte';
  import Toggle from '../ui/Toggle.svelte';
  import Slider from '../ui/Slider.svelte';
  import CommandEditor from '../CommandEditor.svelte';
  import {settings} from '../settings.svelte.js';

  /** @type {{listening: boolean, notify: (msg: string, kind?: string) => void}} */
  let {listening, notify} = $props();
</script>

<div class="space-y-4">
  <Section title="Başlatma komutuyla yaz"
           description="Açıkken uygulama başlar başlamaz dinler ama yazmaz; bir başlatma komutu duyunca yazmaya başlar, bekleme komutunda ya da sessizlikte tekrar bekler.">
    <Toggle label="Uyandırma modu" bind:checked={settings.wakeWord} disabled={listening}
            description={listening ? 'Dinleme sürerken modu ana penceredeki Sürekli / Uyandırma düğmesinden değiştirebilirsiniz.' : 'Ana pencere açılınca dinleme otomatik başlar. Kapalıyken sürekli mod kullanılır: butona basınca açılır, siz durdurana kadar yazar.'}/>
    {#if settings.wakeWord}
      <Slider label="Beklemeye dönme süresi" bind:value={settings.wakeSec} min={3} max={30} display="{settings.wakeSec} sn"
              disabled={listening} description="Bu kadar süre bir şey söylenmezse tekrar başlatma komutu beklenir."/>
      <Toggle label="Yapay zekâ yanıtlarken beklemeye geçme" bind:checked={settings.wakeHoldForAI}
              description="Yanıt gelene kadar uyanık kalır; süre yanıt bittikten sonra yeniden başlar. Böylece her soruda başlatma komutunu tekrar söylemeniz gerekmez."/>
      <Toggle label="Sesli uyarı" bind:checked={settings.wakeSound} description="Uyanınca yükselen, beklemeye dönerken alçalan kısa bir ses çalar."/>
    {/if}
  </Section>

  <CommandEditor {listening} {notify}/>
</div>
