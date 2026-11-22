import { describe, expect, it } from "vitest";

import { allCommands, commandById, type CommandContext } from "@/app/commands";

import "@/commands";
import "@/commands/view";

const ctx: CommandContext = { session: undefined, link: "live", stale: false, panel: "fleet" };

const PALETTE = [
  "app.backToClassic",
  "device.unpair",
  "push.enable",
  "session.kill",
  "session.merge",
  "session.pause",
  "session.push",
  "session.resume",
  "view.toggleInspector",
  "view.toggleSidebar",
];

const TAKE_ARGS = [
  "session.send",
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
  "device.pair",
];

describe("the palette's commands", () => {
  it("the palette lists exactly these ten commands", () => {
    const ids = allCommands()
      .filter((c) => c.palette === true)
      .map((c) => c.id)
      .sort();
    expect(ids).toEqual(PALETTE);
  });

  it("no command that takes args sets the mark", () => {
    for (const id of TAKE_ARGS) {
      const command = commandById(id);
      expect(command, id).toBeDefined();
      expect(command?.palette, id).not.toBe(true);
    }
  });

  it("the palette commands are the ones that take no args", () => {
    for (const id of PALETTE) {
      const command = commandById(id);
      expect(command, id).toBeDefined();
      expect(() => command?.when(ctx), id).not.toThrow();
      expect(command?.key, id).toBeUndefined();
    }
  });
});
