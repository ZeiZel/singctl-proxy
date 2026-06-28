import { type CSSProperties } from "react";

// Wails marks a window drag handle via the CSS var --wails-draggable. With the
// title bar hidden, these regions let the user move the window; interactive
// children opt out with NO_DRAG_REGION.
export const DRAG_REGION = { "--wails-draggable": "drag" } as CSSProperties;
export const NO_DRAG_REGION = { "--wails-draggable": "no-drag" } as CSSProperties;
