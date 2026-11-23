<script>
  // Labelled range slider; `display` is the text shown on the right.
  let {value = $bindable(0), min = 0, max = 100, step = 1, label, display = '', description = '', disabled = false,
       minLabel = '', maxLabel = ''} = $props();
  const pct = $derived(max > min ? ((value - min) / (max - min)) * 100 : 0);
</script>

<div>
  <div class="mb-1 flex justify-between gap-3 text-sm">
    <span>{label}</span>
    <span class="shrink-0 text-zinc-500 tabular-nums">{display || value}</span>
  </div>
  <input type="range" {min} {max} {step} bind:value class="w-full" style="--pct: {pct}%" {disabled} aria-label={label}/>
  {#if minLabel || maxLabel}
    <div class="mt-0.5 flex justify-between text-[11px] text-zinc-500 dark:text-zinc-400"><span>{minLabel}</span><span>{maxLabel}</span></div>
  {/if}
  {#if description}<p class="mt-1 text-xs text-zinc-500 dark:text-zinc-400">{description}</p>{/if}
</div>
