import { describe, expect, it } from "vitest";

import { commandById } from "@/app/commands";

import "./index";

describe("the commands module", () => {
  it.each([
    "session.send",
    "session.pause",
    "session.resume",
    "session.push",
    "session.merge",
    "session.kill",
    "ask.answer",
    "ask.chat",
    "ask.allow",
    "ask.deny",
    "job.start",
    "job.park",
    "job.unpark",
    "job.pause",
    "job.resume",
    "session.create",
    "device.update",
    "quickReplies.save",
    "device.unpair",
    "device.pair",
    "push.enable",
  ])("registers %s on import", (id) => {
    expect(commandById(id)).toBeDefined();
  });
});
