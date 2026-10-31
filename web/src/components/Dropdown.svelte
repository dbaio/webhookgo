<script lang="ts">
  // A menu in the browser's top layer (popover API): light dismiss, Esc and
  // stacking above dialogs come for free.
  import type { Snippet } from 'svelte';

  interface Props {
    button: Snippet;
    children: Snippet<[() => void]>;
    class?: string;
    title?: string;
    align?: 'start' | 'end';
    width?: number;
    /** The trigger's id, to open the menu from elsewhere (a shortcut). */
    id?: string;
  }
  let { button, children, class: cls = 'btn', title, align = 'end', width, id: triggerId }: Props = $props();

  const id = `menu-${Math.random().toString(36).slice(2, 9)}`;
  let trigger = $state<HTMLButtonElement>();
  let pop = $state<HTMLDivElement>();
  let open = $state(false);

  function place() {
    if (!trigger || !pop) return;
    const r = trigger.getBoundingClientRect();
    const vw = document.documentElement.clientWidth;
    const top = r.bottom + 6;
    pop.style.top = `${top}px`;
    pop.style.maxHeight = `${Math.max(160, innerHeight - top - 12)}px`;
    if (align === 'end' || r.left + (width ?? 240) > vw - 8) {
      pop.style.left = 'auto';
      pop.style.right = `${Math.max(8, vw - r.right)}px`;
    } else {
      pop.style.right = 'auto';
      pop.style.left = `${Math.max(8, r.left)}px`;
    }
  }

  function onbeforetoggle(e: ToggleEvent) {
    open = e.newState === 'open';
    if (open) place();
  }

  const close = () => pop?.hidePopover();
</script>

<button bind:this={trigger} id={triggerId} type="button" class={cls} {title} aria-label={title} popovertarget={id} aria-expanded={open}>
  {@render button()}
</button>
<div bind:this={pop} {id} popover="auto" class="menu" style:width={width ? `${width}px` : undefined} {onbeforetoggle}>
  {#if open}{@render children(close)}{/if}
</div>
