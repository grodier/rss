// Search boxes' clear button: a.search-clear[data-search-clear] in a
// .search-field, after its input. The server renders it as a link to the
// page without the query, hidden when the box is empty; this shows it while
// the box has text and hides it when it's emptied, whatever empties it.
//
// Pressing it, or Escape in a box with text, clears the box in place when
// the page updates as the reader types (data-search-clear="in-place": live
// search, My feeds' filter) or when the page shows no query yet (the box's
// default value is empty), so there are no results to reset: it empties the
// box, dispatches an input event so the page's own search reacts as it does
// to typing, and keeps focus in the box. Otherwise (Saved, showing a
// query's results) it follows the link, so the page reloads without the
// query rather than keep results that no longer match the box. Escape in
// an empty box, or while an input method is composing, is left alone.
export function init(): void {
  for (const clear of document.querySelectorAll<HTMLAnchorElement>("a[data-search-clear]")) {
    const input = clear.closest(".search-field")?.querySelector<HTMLInputElement>('input[type="search"]');
    if (!input) continue;

    const sync = () => {
      clear.hidden = input.value === "";
    };
    const inPlace = () => clear.dataset.searchClear === "in-place" || input.defaultValue === "";
    const clearInPlace = () => {
      input.value = "";
      sync();
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.focus();
    };

    sync();
    input.addEventListener("input", sync);
    // A form's value comes back on Back without an input event.
    window.addEventListener("pageshow", sync);
    clear.addEventListener("click", (e) => {
      // A modified click opens the link as the reader asked.
      if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || !inPlace()) return;
      e.preventDefault();
      clearInPlace();
    });
    input.addEventListener("keydown", (e) => {
      // Escape while an input method is composing cancels the composition.
      if (e.key !== "Escape" || e.isComposing || input.value === "") return;
      e.preventDefault();
      // As a press of the button: in place, or the link is followed.
      clear.click();
    });
  }
}
