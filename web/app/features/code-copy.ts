import { announce } from "../../shared/lib/announce";
import * as copied from "./copied";

// Copy buttons on an article's code blocks (.article-body pre). The
// article's HTML is untrusted and its sanitizer allows no button, and the
// button does nothing without JavaScript, so this adds it: a copy of the
// page's template[data-code-copy] (the copy icon, named "Copy code"), once
// per block (data-copy-ready), and only where the Clipboard API works (it
// needs a secure context). data-copy-ready also gives every block the bar
// it sits in (article.css), which names the block's language when the
// sanitizer found one (data-lang), so the button never covers the code. On
// click it copies the block's text exactly, less one trailing line break,
// and shows the outcome with copied.ts's showCopied, announced in the
// page's polite live region. Nothing about copying is recorded.
export function init(): void {
  const template = document.querySelector<HTMLTemplateElement>("template[data-code-copy]");
  if (!template || !navigator.clipboard?.writeText) return;
  copied.init();
  for (const pre of document.querySelectorAll<HTMLPreElement>(".article-body pre:not([data-copy-ready])")) {
    const button = (template.content.cloneNode(true) as DocumentFragment).firstElementChild;
    if (!(button instanceof HTMLButtonElement)) return;
    pre.dataset.copyReady = "";
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
