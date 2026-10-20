<script>
  import Icon from './Icon.svelte'
  let { icon = 'sparkle', title, description = '', loading = false, children } = $props()
</script>

<div class="empty-state" role={loading ? 'status' : undefined}>
  <div class="empty-icon" class:loading><Icon name={loading ? 'refresh' : icon} size={24} /></div>
  <h2>{title}</h2>
  {#if description}<p>{description}</p>{/if}
  {#if children}<div class="empty-actions">{@render children()}</div>{/if}
</div>
