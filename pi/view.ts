// view.ts — local presentation only.
//
// Every screen here drives typed local controls; the compiled Go child remains
// authoritative for grammar, minutes/allocations, revisions, identity,
// persistence and readiness. Screens never compute accounting values, never
// write state directly, and never place full records into the Pi transcript or
// model context. Untrusted text (evidence, descriptions, provider, model and
// diagnostic text) is sanitized for the terminal at this presentation boundary;
// underlying protocol values are never edited.

import {
  Container,
  SelectList,
  Text,
  wrapTextWithAnsi,
  type Component,
} from "@earendil-works/pi-tui";
import type { ExtensionContext } from "@earendil-works/pi-coding-agent";

// --- untrusted text handling ------------------------------------------------

const ANSI_CSI = /\x1b\[[0-9;?<=>!]*[ \-/]*[@-~]/g;
const ANSI_OSC = /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g;
const ANSI_DCS = /\x1b[PX^_][^\x1b]*(?:\x1b\\)/g;
const ANSI_SIMPLE = /\x1b[@-Z\\-_]/g;
const CONTROL = /[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/g;

/** Presentation-only sanitization: strip terminal escape/control sequences and
 *  other control bytes — including OSC/CSI and OSC-52 clipboard shapes — before
 *  any untrusted text reaches the terminal. Sanitized output is never
 *  interpolated into shell or editor commands, resource discovery, or URLs
 *  that execute actions; underlying protocol values are never edited. */
export function sanitizeText(input: unknown): string {
  if (typeof input !== "string") return "";
  return input
    .replace(ANSI_OSC, "")
    .replace(ANSI_DCS, "")
    .replace(ANSI_CSI, "")
    .replace(ANSI_SIMPLE, "")
    .replace(CONTROL, "");
}

/** Multi-line variant: sanitize line-by-line, keep newlines. */
export function sanitizeLines(input: unknown): string {
  if (typeof input !== "string") return "";
  return input
    .replace(/\r\n/g, "\n")
    .split("\n")
    .map(sanitizeText)
    .join("\n");
}

// --- shared rendering shapes (local copies of the Go child's result shapes) --

export interface ListChoice {
  value: string;
  label: string;
  description?: string;
}

export interface BlockerShape {
  code: string;
  date?: string;
  local_id?: string;
  remote_id?: string;
  detail?: string;
}

export interface SegmentShape {
  interval_id: string;
  date: string;
  start: string;
  end: string;
  minutes: number;
  project: string;
  classification?: string;
  description: string;
  evidence_refs?: string[];
  origin_date: string;
  locked: boolean;
  crosses_midnight: boolean;
  allocations?: Array<{ date: string; start: string; end: string; minutes: number }>;
}

export interface ViewShape {
  segments: SegmentShape[];
  total_minutes: number;
}

export function line(text: string): string {
  return sanitizeText(text);
}

export function minutesLabel(minutes: number): string {
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")} (${minutes}m)`;
}

export function renderBlocker(b: BlockerShape): string {
  const where = [b.date, b.local_id, b.remote_id].filter(Boolean).join(" ");
  return `- ${line(b.code)}${where ? " (" + line(where) + ")" : ""}${b.detail ? ": " + line(b.detail) : ""}`;
}

export function renderSegment(seg: SegmentShape): string {
  const pad = (s: string, w: number) => (s.length >= w ? s : s + " ".repeat(w - s.length));
  const flags = [seg.locked ? "locked" : "", seg.crosses_midnight ? "cross-midnight" : ""]
    .filter(Boolean)
    .join(", ");
  const head =
    `${pad(seg.interval_id, 18)} ${pad(seg.date, 10)} ` +
    `${pad(seg.start + "-" + seg.end, 14)} ${pad(seg.project, 8)} ` +
    `${pad(seg.classification ?? "", 10)} ${pad(String(seg.minutes), 5)}m` +
    (flags ? " [" + flags + "]" : "");
  const extra: string[] = [];
  if (seg.description) extra.push("    desc: " + line(seg.description));
  if (seg.evidence_refs?.length) {
    extra.push("    evidence: " + seg.evidence_refs.map((r) => line(r)).join(", "));
  }
  if (seg.allocations?.length) {
    for (const a of seg.allocations) {
      extra.push(`    allocation ${a.date} ${a.start}-${a.end} (${a.minutes}m)`);
    }
  }
  if (seg.origin_date !== seg.date) extra.push("    origin: " + line(seg.origin_date));
  return [head, ...extra].join("\n");
}

export function renderView(view: ViewShape | undefined): string[] {
  // An empty draft is a valid state, including a confirmed zero-work revision.
  const segments = view?.segments ?? [];
  if (!segments.length) return ["  (none)", `  total: ${minutesLabel(view?.total_minutes ?? 0)}`];
  const out: string[] = [];
  for (const seg of segments) out.push(renderSegment(seg));
  out.push(`  total: ${minutesLabel(view?.total_minutes ?? 0)}`);
  return out;
}

// --- local screens ----------------------------------------------------------

export interface ScreenOptions {
  title: string;
  header?: string[];
  detail?: string[];
  actions?: ListChoice[];
  footer?: string[];
}

/** Lifetime lease for one custom screen. Pi disposes a custom component when a
 *  reload/session replacement resets extension UI, but it does NOT settle the
 *  `custom()` promise in that path: the component's `done` callback is simply
 *  never called. An owner therefore registers a canceller here and calls it
 *  from its idempotent session_shutdown, so the awaiting command unwinds and
 *  the next command can run (Pi's interactive loop awaits each command). */
export interface ScreenLease {
  track(cancel: () => void): () => void;
}

const PLAIN_SELECT_THEME = {
  selectedPrefix: (text: string) => "❯ " + text,
  selectedText: (text: string) => text,
  description: (text: string) => text,
  scrollInfo: (text: string) => text,
  noMatch: (text: string) => text,
};

/**
 * One full local screen shown in the editor slot: title, optional context
 * header, a scrollable detail area, an optional bottom action menu, and an
 * optional footer. Resolves with the chosen action's value, or undefined on
 * Escape. Arrow up/down scrolls detail when the menu is absent.
 */
export async function openScreen(
  ctx: ExtensionContext,
  options: ScreenOptions,
  lease?: ScreenLease,
): Promise<string | undefined> {
  return ctx.ui.custom<string | undefined>(
    (_tui, _theme, _keybindings, done) => {
      // One close path: Pi may call `done` after the component was detached,
      // and the owner may cancel simultaneously from session_shutdown. Exactly
      // one resolution and one lease release happen per screen.
      let closed = false;
      let releaseLease: (() => void) | undefined;
      const close = (value: string | undefined): void => {
        if (closed) return;
        closed = true;
        releaseLease?.();
        done(value);
      };
      releaseLease = lease?.track(() => close(undefined));
      // The visible detail window follows the real terminal height so the
      // paging label and rows stay inspectable in short/narrow terminals.
      const MAX_DETAIL_WINDOW = 16;
      const DETAIL_PADDING_X = 1; // matches the detail Text component below
      // Rows the pinned Pi 1.4.0 main screen keeps outside this custom editor
      // slot: at least one transcript row and the footer row, plus one safety
      // row. Terminal rows alone therefore overstate the space this component
      // actually gets; ignoring that let the component be cropped after layout
      // even though its own paging believed the rows fit.
      const PI_OUTER_ROWS = 3;
      let windowSize = 12;
      const logicalLines = (options.detail ?? [])
        .join("\n")
        .replace(/\r\n/g, "\n")
        .split("\n")
        .map((text) => sanitizeText(text));
      let offset = 0;
      let displayLines: string[] = logicalLines.slice();
      let wrappedWidth = -1;
      const container = new Container();
      container.addChild(new Text(line(options.title), 2, 0));
      if (options.header?.length) {
        container.addChild(new Text(sanitizeLines(options.header.join("\n")), 2, 0));
      }
      const detailText = new Text("", 1, 0);
      const detailLabel = new Text("", 2, 0);
      /** Wrap the detail at the REAL display-cell width of the detail Text,
       *  using the pinned Pi grapheme/wide-character-aware wrapper. Counting
       *  UTF-16 code units here (the old behavior) let Text wrap wide CJK or
       *  emoji rows into more physical rows than paging counted, so the true
       *  tail of a long row could not be reached at a narrow width. */
      const ensureWrapped = (width: number): void => {
        if (width === wrappedWidth) return;
        wrappedWidth = width;
        const paddingX = Math.min(DETAIL_PADDING_X, Math.max(0, Math.floor((width - 1) / 2)));
        const contentWidth = Math.max(1, width - paddingX * 2);
        displayLines = [];
        for (const logical of logicalLines) {
          // Text normalizes tabs before wrapping; mirror it so our row count is
          // exactly the number of physical rows the component will render.
          const normalized = logical.replace(/\t/g, "   ");
          for (const row of wrapTextWithAnsi(normalized, contentWidth)) displayLines.push(row);
        }
        offset = Math.max(0, Math.min(offset, Math.max(0, displayLines.length - windowSize)));
      };
      const renderDetail = () => {
        if (!displayLines.length || windowSize <= 0) {
          // No room for even one detail row: show nothing rather than forcing a
          // window that would push the local controls off-screen.
          detailText.setText("");
          detailLabel.setText("");
          return;
        }
        const start = Math.max(0, Math.min(offset, Math.max(0, displayLines.length - windowSize)));
        offset = start;
        const end = Math.min(displayLines.length, start + windowSize);
        detailText.setText(displayLines.slice(start, end).join("\n"));
        detailLabel.setText(
          displayLines.length > windowSize
            ? `lines ${start + 1}-${end} of ${displayLines.length} (j/k scroll)`
            : "",
        );
      };
      let list: SelectList | null = null;
      if (options.actions?.length) {
        list = new SelectList(
          options.actions.map((a) => ({
            value: a.value,
            label: line(a.label),
            description: a.description ? line(a.description) : undefined,
          })),
          Math.max(3, Math.min(8, options.actions.length)),
          PLAIN_SELECT_THEME,
        );
        list.onCancel = () => close(undefined);
        list.onSelect = (item: { value: string }) => close(item.value);
        container.addChild(new Text(
          "Actions (↑/↓ pick, Enter run; j/k or g/G scroll details; Esc close)",
          2, 0,
        ));
        container.addChild(list);
      }
      // Detail comes after the menu so a long/wrapped review can never push the
      // local controls off-screen at narrow widths.
      renderDetail();
      container.addChild(detailText);
      container.addChild(detailLabel);
      if (options.footer?.length) {
        container.addChild(new Text(sanitizeLines(options.footer.join("\n")), 2, 0));
      }
      const scrollDetail = (delta: number) => {
        if (windowSize <= 0 || displayLines.length <= windowSize) return;
        offset = Math.max(0, Math.min(offset + delta, displayLines.length - windowSize));
        renderDetail();
      };
      const screen: Component = {
        invalidate(): void {},
        render(width: number): string[] {
          const rows = _tui.terminal?.rows ?? 40;
          // Measure the real chrome (title/header/menu/footer) at this width so
          // the detail window, its paging label and the controls all fit.
          detailText.setText("");
          detailLabel.setText("");
          const chromeRows = container.render(width).length;
          // Reserve the pinned Pi outer rows, this component's own chrome and
          // one row for the paging label; never claim more rows than fit. There
          // is deliberately no forced minimum: a window larger than the
          // available space would be cropped and hide its own tail.
          const available = rows - chromeRows - PI_OUTER_ROWS - 1;
          windowSize = Math.max(0, Math.min(MAX_DETAIL_WINDOW, available));
          ensureWrapped(width);
          renderDetail();
          return container.render(width);
        },
        handleInput(data: string): void {
          if (data === "\x1b" || data === "\x1b\x1b") {
            close(undefined);
            return;
          }
          if (data === "\x1b[5~") {
            scrollDetail(-windowSize);
            return;
          }
          if (data === "\x1b[6~") {
            scrollDetail(windowSize);
            return;
          }
          if (data === "\x1b[H" || data === "\x1b[1~") {
            scrollDetail(-displayLines.length);
            return;
          }
          // PageUp/PageDown are intercepted by the surrounding TUI, so j/k and
          // g/G are the portable detail paging keys (arrows stay with the menu).
          if (data === "j") {
            scrollDetail(1);
            return;
          }
          if (data === "k") {
            scrollDetail(-1);
            return;
          }
          if (data === "g") {
            scrollDetail(-displayLines.length);
            return;
          }
          if (data === "G") {
            scrollDetail(displayLines.length);
            return;
          }
          if (list) {
            list.handleInput(data); // arrows/enter belong to the action menu
            return;
          }
        },
      };
      // The screen owns keyboard focus so detail scrolling always works; the
      // action list receives every key it needs through handleInput.
      _tui.setFocus(screen);
      return screen;
    },
  );
}
