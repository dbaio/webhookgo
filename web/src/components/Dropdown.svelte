<script lang="ts">
  // A menu in the browser's top layer (popover API): light dismiss, Esc and
  // stacking above dialogs come for free. On phones app.css turns it into a
  // sheet at the bottom of the screen, which ignores the position set here:
  // the popover fills the screen as the backdrop and .menu-panel is the sheet.
  // Its ::backdrop wouldn't do: browsers let taps through it, to the page.
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
    const w = Math.min(width ?? 240, vw - 16);
    const top = r.bottom + 6;
    const fromEnd = align === 'end' || r.left + w > vw - 8;
    pop.style.setProperty('--menu-top', `${top}px`);
    pop.style.setProperty('--menu-max-h', `${Math.max(160, innerHeight - top - 12)}px`);
    pop.style.setProperty('--menu-left', fromEnd ? 'auto' : `${Math.max(8, r.left)}px`);
    pop.style.setProperty('--menu-right', fromEnd ? `${Math.max(8, Math.min(vw - r.right, vw - w - 8))}px` : 'auto');
  }

  function onbeforetoggle(e: ToggleEvent) {
    open = e.newState === 'open';
    if (open) place();
  }

  const close = () => pop?.hidePopover();

  // Outside the sheet, but inside the popover, so no light dismiss to rely on
  // (iOS has shipped without it for touch: WebKit bugs 267688 and 305879).
  // On click, the last event of a tap: on an earlier one the rest would land
  // on the page below.
  function onclick(e: MouseEvent) {
    if (e.target === pop) close();
  }
</script>

<button bind:this={trigger} id={triggerId} type="button" class={cls} {title} aria-label={title} popovertarget={id} aria-expanded={open}>
  {@render button()}
</button>
<div bind:this={pop} {id} popover="auto" class="menu" role="presentation" style:--menu-w={width ? `${width}px` : undefined} {onbeforetoggle} {onclick}>
  <div class="menu-panel">
    {#if open}{@render children(close)}{/if}
  </div>
</div>
