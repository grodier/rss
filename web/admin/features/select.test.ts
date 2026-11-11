import { beforeEach, describe, expect, it } from "vitest";
import { isolate } from "../../shared/test/setup";

isolate();

beforeEach(() => {
  document.body.innerHTML = `
    <form data-select>
      <table>
        <thead><tr><th><input type="checkbox" data-select-all hidden></th></tr></thead>
        <tbody>
          ${[1, 2, 3, 4, 5].map((i) => `<tr><td><input type="checkbox" name="id" value="${i}"></td></tr>`).join("")}
        </tbody>
      </table>
      <p data-select-count hidden></p>
    </form>`;
});

const start = async () => (await import("./select")).init();
const all = () => document.querySelector<HTMLInputElement>("[data-select-all]")!;
const box = (i: number) => document.querySelector<HTMLInputElement>(`input[name=id][value="${i}"]`)!;
const checked = () => [...document.querySelectorAll<HTMLInputElement>("input[name=id]:checked")].map((b) => b.value);
const count = () => document.querySelector<HTMLElement>("[data-select-count]")!;
// A click toggles the checkbox before the listeners run, as in a browser.
const click = (el: HTMLInputElement, shiftKey = false) => {
  el.dispatchEvent(new MouseEvent("click", { bubbles: true, shiftKey }));
};
const toggleAll = () => {
  all().checked = !all().checked;
  all().dispatchEvent(new Event("change", { bubbles: true }));
};

describe("select", () => {
  it("shows the select-all checkbox and the count", async () => {
    await start();
    expect(all().hidden).toBe(false);
    expect(count().hidden).toBe(false);
    expect(count().textContent).toBe("0 selected");
  });

  it("selects and clears every row", async () => {
    await start();
    toggleAll();
    expect(checked()).toEqual(["1", "2", "3", "4", "5"]);
    expect(count().textContent).toBe("5 selected");
    toggleAll();
    expect(checked()).toEqual([]);
  });

  it("marks select-all mixed when some rows are selected, and checked when all are", async () => {
    await start();
    click(box(2));
    expect(all().indeterminate).toBe(true);
    expect(all().checked).toBe(false);
    expect(count().textContent).toBe("1 selected");
    for (const i of [1, 3, 4, 5]) click(box(i));
    expect(all().indeterminate).toBe(false);
    expect(all().checked).toBe(true);
  });

  it("selects the range from the last row clicked with shift", async () => {
    await start();
    click(box(4));
    click(box(2), true);
    expect(checked()).toEqual(["2", "3", "4"]);
  });

  it("clears a range with shift when the clicked row is cleared", async () => {
    await start();
    toggleAll();
    click(box(1));
    click(box(4), true);
    expect(checked()).toEqual(["5"]);
  });

  it("selects only the clicked row without shift", async () => {
    await start();
    click(box(1));
    click(box(3));
    expect(checked()).toEqual(["1", "3"]);
  });
});
