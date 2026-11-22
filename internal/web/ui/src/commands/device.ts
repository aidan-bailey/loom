// The phone's own commands (spec §23, Part 3 settlements 9, 10, 13 and 14): its notification
// settings, the quick replies every paired phone offers, unpairing, and pairing. The words are the
// classic app's (settings.mjs, pair.mjs). A write's answer is applied to the store as the stream's
// event would be, so it shows at once; when the held value changed while the write was in flight
// (the stream's event, a new snapshot, another write's answer) that value holds, since this answer
// was read before it, and the stream's event that follows every write brings the daemon's.
import type { DeviceBody } from "@/api/gen";
import { api, emitUnpaired } from "@/api/request";
import { defineCommand, type CommandContext } from "@/app/commands";
import { hrefFor } from "@/app/routes";
import { showMessage, showOutcome } from "@/app/toasts";
import { startFleet, useFleet } from "@/store/fleet";

// The fallback of showOutcome for a refusal with no text: the classic app's "That didn't work.".
const WHAT = "That";

const CANT_REACH = "Can't reach loom. Check the connection and try again.";

// onSettings is the `when` of the settings view's commands.
function onSettings(ctx: CommandContext): boolean {
  return ctx.panel === "settings";
}

// setFields are the names of the fields a device body sets, in order: what keys the in-flight
// state, so a write of one field does not hold a write of another, whatever the values.
function setFields(body: DeviceBody): string {
  return Object.entries(body)
    .filter(([, value]) => value !== undefined)
    .map(([name]) => name)
    .sort()
    .join(",");
}

defineCommand<DeviceBody>({
  id: "device.update",
  title: "Notification settings",
  key: setFields,
  when: onSettings,
  run: async (_ctx, args) => {
    const before = useFleet.getState().snapshot?.device;
    const outcome = await api.putDevice(args);
    if (outcome.kind !== "ok") {
      showOutcome(outcome, WHAT);
      return;
    }
    // Any device value other than the one held when the write went out (a stream event, a new
    // snapshot, or the answer of another write in flight with this one) wins: this answer is not
    // the newest. The stream's event that follows every write brings the daemon's value.
    if (useFleet.getState().snapshot?.device === before) {
      useFleet.getState().apply("device", outcome.value);
    }
  },
});

// null replies reset the list to the defaults: the daemon takes no field for that.
defineCommand<{ replies: string[] | null; onSaved?: () => void }>({
  id: "quickReplies.save",
  title: "Save",
  when: onSettings,
  run: async (_ctx, args) => {
    const before = useFleet.getState().snapshot?.quick_replies;
    const outcome = await api.putQuickReplies(
      args.replies === null ? {} : { replies: args.replies },
    );
    if (outcome.kind !== "ok") {
      showOutcome(outcome, WHAT);
      return;
    }
    if (useFleet.getState().snapshot?.quick_replies === before) {
      useFleet.getState().apply("quick_replies", outcome.value);
    }
    args.onSaved?.();
  },
});

defineCommand({
  id: "device.unpair",
  title: "Unpair this phone",
  palette: true,
  when: onSettings,
  confirm: () => ({
    title: "Unpair this phone?",
    body: "This phone stops getting notifications and goes back to pairing. Pair it again with loom web pair on the host.",
    action: "Unpair",
    destructive: true,
  }),
  run: async () => {
    const outcome = await api.deleteDevice();
    // The unpaired path clears what a 401 clears and opens pairing (main.tsx).
    if (outcome.kind === "ok") {
      emitUnpaired();
      return;
    }
    showOutcome(outcome, "Unpair");
  },
});

// device.pair is offline: it runs while unpaired, with no fresh snapshot (a pairing screen has
// none). A refusal is the daemon's own text, as a toast: classic's inline line.
defineCommand<{ code: string }>({
  id: "device.pair",
  title: "Pair",
  offline: true,
  when: (ctx) => ctx.panel === "pair",
  run: async (_ctx, args) => {
    const outcome = await api.pair({ code: args.code });
    switch (outcome.kind) {
      case "ok":
        startFleet();
        window.location.assign(hrefFor("fleet"));
        return;
      case "failed":
        showMessage(outcome.status === 0 ? CANT_REACH : outcome.message, true);
        return;
      case "refused":
      case "stopping":
      case "timeout":
        showMessage(outcome.message, true);
        return;
      case "pending":
      case "unpaired":
        showMessage("That didn't work.", true);
        return;
    }
  },
});
