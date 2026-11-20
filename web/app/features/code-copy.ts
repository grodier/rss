import { announce } from "../../shared/lib/announce";
import * as copied from "./copied";

// Copy buttons on an article's code blocks (.article-body pre). The
// article's HTML is untrusted and its sanitizer allows no button, and the
// button does nothing without JavaScript, so this adds it, once per block
// (data-copy-ready), and only where the Clipboard API works (it needs a
// secure context). It's the block's first child, floated into its top
// corner (article.css), so the first line wraps around it rather than
// running under it. On click it copies the block's text exactly, less one
// trailing line break, and shows the outcome with copied.ts's showCopied,
// announced in the page's polite live region. Nothing about copying is
// recorded.
export function init(): void {
  if (!navigator.clipboard?.writeText) return;
  copied.init();
  for (const pre of document.querySelectorAll<HTMLPreElement>(".article-body pre:not([data-copy-ready])")) {
    pre.dataset.copyReady = "";
    const button = document.createElement("button");
    button.type = "button";
    button.className = "btn btn-sm code-copy";
    button.setAttribute("aria-label", "Copy code");
    button.textContent = "Copy";
    const label = document.createElement("span");
    label.className = "copied-label";
    label.setAttribute("aria-hidden", "true");
    button.append(label);
    pre.prepend(button);
    button.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(codeText(pre));
      } catch {
        copied.showCopied(button, false, "Code copied");
        announce("Couldn't copy");
        return;
      }
      copied.showCopied(button, true, "Code copied");
      announce("Code copied");
    });
  }
}

// codeText is the block's text without its button, less one trailing line
// break.
function codeText(pre: HTMLPreElement): string {
  let text = "";
  for (const node of pre.childNodes) {
    if (node instanceof Element && node.classList.contains("code-copy")) continue;
    text += node.textContent;
  }
  return text.replace(/\r?\n$/, "");
}
