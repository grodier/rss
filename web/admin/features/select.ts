// Selecting rows: in form[data-select], a table's rows have checkboxes
// (input[name=id]). This shows the header's select-all checkbox
// ([data-select-all], hidden without JavaScript), which selects or clears
// every row and is mixed when some are selected; makes shift-click select or
// clear the range from the last row clicked; and says how many are selected
// in [data-select-count].
export function init(): void {
  for (const form of document.querySelectorAll<HTMLFormElement>("form[data-select]")) setUp(form);
}

function setUp(form: HTMLFormElement): void {
  const boxes = () => [...form.querySelectorAll<HTMLInputElement>("input[type=checkbox][name=id]")];
  const all = form.querySelector<HTMLInputElement>("input[data-select-all]");
  const count = form.querySelector<HTMLElement>("[data-select-count]");
  let last: HTMLInputElement | null = null;

  const update = () => {
    const n = boxes().filter((b) => b.checked).length;
    if (all) {
      all.checked = n > 0 && n === boxes().length;
      all.indeterminate = n > 0 && n < boxes().length;
    }
    if (count) {
      count.hidden = false;
      count.textContent = n === 1 ? "1 selected" : `${n} selected`;
    }
  };

  if (all) {
    all.hidden = false;
    all.addEventListener("change", () => {
      for (const b of boxes()) b.checked = all.checked;
      last = null;
      update();
    });
  }

  form.addEventListener("click", (e) => {
    const box = e.target;
    if (!(box instanceof HTMLInputElement) || box.name !== "id" || box.type !== "checkbox") return;
    const list = boxes();
    if (e instanceof MouseEvent && e.shiftKey && last && last !== box) {
      const [from, to] = [list.indexOf(last), list.indexOf(box)].sort((a, b) => a - b);
      for (const b of list.slice(from, to + 1)) b.checked = box.checked;
    }
    last = box;
    update();
  });

  update();
}
