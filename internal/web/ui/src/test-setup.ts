import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach } from "vitest";

import { setViewportWidth } from "./test-layout";

// Every test starts on the phone layout: happy-dom's 1024px window would read `desk2`.
beforeEach(() => {
  setViewportWidth(390);
});

afterEach(() => {
  cleanup();
});
