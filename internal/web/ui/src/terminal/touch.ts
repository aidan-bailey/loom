// Touch scrolling for the terminal's frame (spec §38): xterm.js 6 binds no touch handler, so one
// finger's vertical drag on an element becomes scrollLines calls, and a flick carries on as
// momentum on animation frames. No xterm.js import: the page gives it a Scroller.

// A lift is a flick above this speed (px/ms), strictly.
const FLICK_MIN = 0.3;
// A finger that stood still this long (ms) before it lifted is not a flick.
const FLICK_STALE_MS = 100;
// Momentum's speed is multiplied by this each frame.
const DECAY = 0.95;
// Momentum ends once its speed (px/ms) is under this.
const STOP_BELOW = 0.02;

/** What a drag scrolls: a negative count shows older lines (as xterm.js's `scrollLines`), and `cellHeight` is one row's height in CSS pixels. */
export interface Scroller {
  scrollLines(lines: number): void;
  cellHeight(): number;
}

/**
 * Binds one-finger drag scrolling and flick momentum on `el` to `scroller`: the content follows the
 * finger. Returns the function that removes the listeners, cancels any momentum frame and ends
 * any drag; calling it twice is harmless.
 */
export function bindTouchScroll(el: HTMLElement, scroller: Scroller): () => void {
  let dragging = false;
  let lastY = 0;
  let lastT = 0;
  let acc = 0;
  let velocity = 0;
  let frame: number | null = null;

  // One routine for a drag's moves and for momentum; the remainder under a cell carries on.
  function feed(dy: number): void {
    acc += dy;
    const h = scroller.cellHeight();
    if (!Number.isFinite(h) || h <= 0) return;
    const lines = Math.trunc(acc / h);
    if (lines !== 0) {
      acc -= lines * h;
      scroller.scrollLines(-lines);
    }
  }

  function stopMomentum(): void {
    if (frame !== null) {
      cancelAnimationFrame(frame);
      frame = null;
    }
  }

  function startMomentum(speed: number): void {
    let v = speed;
    let last = performance.now();
    const tick = (): void => {
      const now = performance.now();
      const dt = Math.max(0, now - last);
      last = now;
      feed(v * dt);
      v *= DECAY;
      if (Math.abs(v) < STOP_BELOW) {
        frame = null;
        return;
      }
      frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
  }

  const onStart = (event: TouchEvent): void => {
    event.preventDefault();
    stopMomentum();
    const y = event.touches[0]?.clientY;
    if (event.touches.length === 1 && y !== undefined) {
      dragging = true;
      lastY = y;
      lastT = performance.now();
      acc = 0;
      velocity = 0;
    } else if (event.touches.length > 1) {
      dragging = false;
    }
  };

  const onMove = (event: TouchEvent): void => {
    event.preventDefault();
    if (!dragging) return;
    const y = event.touches[0]?.clientY;
    if (event.touches.length !== 1 || y === undefined) {
      dragging = false;
      return;
    }
    const now = performance.now();
    const dy = y - lastY;
    const dt = now - lastT;
    if (dt > 0) velocity = dy / dt;
    lastY = y;
    lastT = now;
    feed(dy);
  };

  const onEnd = (event: TouchEvent): void => {
    if (!dragging || event.touches.length !== 0) return;
    dragging = false;
    if (performance.now() - lastT <= FLICK_STALE_MS && Math.abs(velocity) > FLICK_MIN) {
      startMomentum(velocity);
    }
  };

  const onCancel = (): void => {
    dragging = false;
  };

  el.addEventListener("touchstart", onStart, { passive: false });
  el.addEventListener("touchmove", onMove, { passive: false });
  el.addEventListener("touchend", onEnd);
  el.addEventListener("touchcancel", onCancel);

  return () => {
    el.removeEventListener("touchstart", onStart);
    el.removeEventListener("touchmove", onMove);
    el.removeEventListener("touchend", onEnd);
    el.removeEventListener("touchcancel", onCancel);
    stopMomentum();
    dragging = false;
  };
}
