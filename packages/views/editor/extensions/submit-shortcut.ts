import { Extension } from "@tiptap/core";

/**
 * `onSubmit` must return true when it actually handled the event and false
 * when there's no submit handler wired up. That lets us fall through to the
 * default Enter behaviour — inserting a newline — when appropriate.
 *
 * `submitOnEnter` — when true, bare Enter also submits (chat-style) and
 * Shift-Enter inserts a soft line break (Tiptap's HardBreak default). When
 * false, only Mod-Enter submits and bare Enter keeps its default (newline).
 *
 * Even with `submitOnEnter`, Enter falls through (does NOT submit) inside a
 * code block, list item, or blockquote so it keeps continuing that structure
 * — otherwise Enter-as-send would steal the only key that adds the next
 * bullet / quote line, trapping the user after one item. To send from inside
 * one of those, use Mod-Enter, or exit the structure first (e.g. a second
 * Enter on an empty bullet lifts it back to a paragraph).
 */
const FALL_THROUGH_NODES = [
  "codeBlock",
  "listItem",
  "taskItem",
  "blockquote",
] as const;
export function createSubmitExtension(
  onSubmit: () => boolean,
  { submitOnEnter }: { submitOnEnter: boolean },
) {
  return Extension.create({
    name: "submitShortcut",
    addKeyboardShortcuts() {
      const shortcuts: Record<string, () => boolean> = {
        "Mod-Enter": () => onSubmit(),
      };
      if (submitOnEnter) {
        shortcuts.Enter = () => {
          const editor = this.editor;
          // IME guard — never submit while composing a multi-key input
          // (Chinese pinyin, Japanese kana, etc). `view.composing` is set
          // by ProseMirror between compositionstart and compositionend.
          if (editor.view.composing) return false;
          // Let Enter keep its structural behaviour inside code blocks,
          // list items, and blockquotes (see the doc comment above).
          if (FALL_THROUGH_NODES.some((name) => editor.isActive(name)))
            return false;
          return onSubmit();
        };
      }
      return shortcuts;
    },
  });
}
