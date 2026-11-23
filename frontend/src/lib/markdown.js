// Renders AI replies (Markdown) to sanitized HTML.
// Sanitizing matters: replies are untrusted text, and the page can call the
// Go backend — raw HTML from a reply must never be able to run script.
import { marked } from "marked";
import DOMPurify from "dompurify";

marked.setOptions({ gfm: true, breaks: true });

// Links open in the system browser, never inside the app window.
DOMPurify.addHook("afterSanitizeAttributes", (node) => {
  if (node.tagName === "A") {
    node.setAttribute("target", "_blank");
    node.setAttribute("rel", "noopener noreferrer");
  }
});

export function renderMarkdown(text) {
  return DOMPurify.sanitize(marked.parse(text ?? "", { async: false }), {
    FORBID_TAGS: ["style", "form", "input", "button", "iframe", "img"],
    FORBID_ATTR: ["style"],
  });
}
