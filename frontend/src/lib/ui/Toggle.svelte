<script>
  // On/off switch with a label and optional description.
  let {checked = $bindable(false), label, description = '', disabled = false, onchange = undefined} = $props();
</script>

<div>
  <label class="flex cursor-pointer items-center justify-between gap-3 text-sm" class:cursor-not-allowed={disabled}>
    <span>{label}</span>
    <input type="checkbox" class="peer sr-only" bind:checked {disabled} {onchange}/>
    <span class="relative h-5 w-9 shrink-0 rounded-full bg-zinc-300 transition peer-checked:bg-accent-600 peer-focus-visible:ring-2
                 peer-focus-visible:ring-accent-500/40 peer-disabled:opacity-50 dark:bg-zinc-700
                 after:absolute after:top-0.5 after:left-0.5 after:size-4 after:rounded-full after:bg-white after:transition peer-checked:after:translate-x-4"></span>
  </label>
  {#if description}<p class="mt-1 text-xs text-zinc-500 dark:text-zinc-400">{description}</p>{/if}
</div>
