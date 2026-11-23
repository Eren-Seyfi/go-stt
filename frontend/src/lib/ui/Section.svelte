<script>
  // A titled settings card.
  let {title, description = '', children} = $props();
</script>

<section class="card p-4 sm:p-5">
  <h3 class="font-semibold">{title}</h3>
  {#if description}<p class="mt-0.5 text-sm text-zinc-500 dark:text-zinc-400">{description}</p>{/if}
  <div class="mt-4 space-y-5">{@render children()}</div>
</section>
