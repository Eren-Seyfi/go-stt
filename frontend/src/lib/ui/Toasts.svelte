<script>
  import Check from '@lucide/svelte/icons/check';
  import Info from '@lucide/svelte/icons/info';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import {toasts} from './toast.svelte.js';

  let {bottom = 'bottom-24'} = $props();
</script>

<div class="pointer-events-none fixed right-3 left-3 z-50 flex flex-col gap-2 sm:left-auto sm:w-80 {bottom}" aria-live="polite">
  {#each toasts as t (t.id)}
    {@const TIcon = t.kind === 'error' ? TriangleAlert : t.kind === 'success' ? Check : Info}
    <div class="pointer-events-auto flex items-start gap-2 rounded-lg border px-3 py-2.5 text-sm shadow-lg
                {t.kind === 'error' ? 'border-red-200 bg-red-50 text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200'
                  : t.kind === 'success' ? 'border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-200'
                  : 'border-zinc-200 bg-white text-zinc-800 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-200'}">
      <TIcon class="mt-0.5 size-4 shrink-0"/>
      <span class="break-words">{t.message}</span>
    </div>
  {/each}
</div>
